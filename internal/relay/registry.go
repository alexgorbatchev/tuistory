package relay

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/remorses/tuistory/internal/session"
)

// SessionRegistry manages session ownership and serializes lifecycle operations
// for each name. Closing callbacks can inspect the registry and Set replacements;
// they must not synchronously begin another lifecycle operation for the same name.
type SessionRegistry struct {
	mu           sync.RWMutex
	sessions     map[string]*session.Session
	pending      map[string]*sessionOperation
	stopped      bool
	shutdownDone chan struct{}
	shutdownErr  error
}

type sessionOperation struct {
	done    chan struct{}
	changed bool
}

// ErrShuttingDown rejects session ownership after terminal shutdown begins.
var ErrShuttingDown = errors.New("session registry is shutting down")

// NewSessionRegistry creates a new session registry.
func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{
		sessions: make(map[string]*session.Session),
		pending:  make(map[string]*sessionOperation),
	}
}

// Get returns the session with the given name, if present.
func (r *SessionRegistry) Get(name string) *session.Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessions[name]
}

// Set stores a session under the given name.
func (r *SessionRegistry) Set(name string, s *session.Session) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		if err := s.CloseAndWait(context.Background(), "daemon-shutdown"); err != nil {
			slog.Error("closing rejected session", "name", name, "error", err)
		}
		return
	}
	previous := r.sessions[name]
	r.invalidate(name)
	r.sessions[name] = s
	r.mu.Unlock()
	if previous != nil && previous != s {
		if err := previous.CloseAndWait(context.Background(), "registry-replaced"); err != nil {
			slog.Error("closing replaced session", "name", name, "error", err)
		}
	}
}

// Delete removes a session if present.
func (r *SessionRegistry) Delete(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.invalidate(name)
	delete(r.sessions, name)
}

// DeleteIfMatches removes the session only if it currently points to target.
func (r *SessionRegistry) DeleteIfMatches(name string, target *session.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sessions[name] == target {
		r.invalidate(name)
		delete(r.sessions, name)
	}
}

func (r *SessionRegistry) invalidate(name string) {
	if op := r.pending[name]; op != nil {
		op.changed = true
	}
}

func (r *SessionRegistry) begin(name string) (*sessionOperation, *session.Session, error) {
	for {
		r.mu.Lock()
		if r.stopped {
			r.mu.Unlock()
			return nil, nil, ErrShuttingDown
		}
		if current := r.pending[name]; current != nil {
			r.mu.Unlock()
			<-current.done
			continue
		}
		op := &sessionOperation{done: make(chan struct{})}
		r.pending[name] = op
		s := r.sessions[name]
		r.mu.Unlock()
		return op, s, nil
	}
}

func (r *SessionRegistry) finish(name string, op *sessionOperation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pending, name)
	close(op.done)
}

func (r *SessionRegistry) detach(name string, op *sessionOperation, target *session.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return ErrShuttingDown
	}
	if op.changed || r.sessions[name] != target {
		return fmt.Errorf("Session %q changed during operation", name)
	}
	delete(r.sessions, name)
	return nil
}

func (r *SessionRegistry) install(name string, op *sessionOperation, s *session.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return ErrShuttingDown
	}
	if op.changed {
		return fmt.Errorf("Session %q changed during operation", name)
	}
	r.sessions[name] = s
	return nil
}

// Launch owns the name before starting a child, and reuses an existing live session.
func (r *SessionRegistry) Launch(name string, opts session.LaunchOptions) (*session.Session, bool, error) {
	op, current, err := r.begin(name)
	if err != nil {
		return nil, false, err
	}
	defer r.finish(name, op)
	if current != nil && !current.IsDead() {
		return current, false, nil
	}
	if err := r.detach(name, op, current); err != nil {
		return nil, false, err
	}
	if current != nil {
		if err := current.CloseAndWait(context.Background(), "relaunch"); err != nil {
			return nil, false, err
		}
	}
	s, err := r.start(name, op, opts)
	return s, err == nil, err
}

func (r *SessionRegistry) start(name string, op *sessionOperation, opts session.LaunchOptions) (*session.Session, error) {
	// Recheck after closing callbacks, before creating a process we cannot install.
	if err := r.detach(name, op, nil); err != nil {
		return nil, err
	}
	s, err := session.New(opts)
	if err != nil {
		return nil, err
	}
	if err := r.install(name, op, s); err != nil {
		closeErr := s.CloseAndWait(context.Background(), "launch-cancelled")
		return nil, errors.Join(err, closeErr)
	}
	return s, nil
}

// Close removes the owned session before notifying callbacks, and joins termination.
func (r *SessionRegistry) Close(name, reason string) error {
	op, current, err := r.begin(name)
	if err != nil {
		return err
	}
	defer r.finish(name, op)
	if current == nil {
		return fmt.Errorf("Session %q not found", name)
	}
	if err := r.detach(name, op, current); err != nil {
		return err
	}
	return current.CloseAndWait(context.Background(), reason)
}

// Restart reserves the name through preparation, termination, and replacement.
func (r *SessionRegistry) Restart(name string, prepare func(*session.Session) session.LaunchOptions) (*session.Session, error) {
	op, current, err := r.begin(name)
	if err != nil {
		return nil, err
	}
	defer r.finish(name, op)
	if current == nil {
		return nil, fmt.Errorf("Session %q not found", name)
	}
	opts := prepare(current)
	if err := r.detach(name, op, current); err != nil {
		return nil, err
	}
	if err := current.CloseAndWait(context.Background(), "session-restarting"); err != nil {
		return nil, err
	}
	return r.start(name, op, opts)
}

// List returns a snapshot of all active sessions sorted by most recently started first.
func (r *SessionRegistry) List() []SessionInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]SessionInfo, 0, len(r.sessions))
	for name, s := range r.sessions {
		list = append(list, SessionInfo{
			Name:      name,
			Command:   s.Command(),
			Cwd:       s.Cwd(),
			Cols:      s.Cols(),
			Rows:      s.Rows(),
			Dead:      s.IsDead(),
			StartedAt: s.StartedAt().UnixMilli(),
		})
	}
	slices.SortFunc(list, func(a, b SessionInfo) int {
		if order := cmp.Compare(b.StartedAt, a.StartedAt); order != 0 {
			return order
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return list
}

// EvictStaleDead cleans up dead sessions older than 24 hours.
func (r *SessionRegistry) EvictStaleDead() {
	r.evictStaleDead(time.Now())
}

func (r *SessionRegistry) evictStaleDead(now time.Time) {
	list := r.List()
	const oneDay = 24 * time.Hour
	for _, item := range list {
		if !item.Dead {
			continue
		}
		op, s, err := r.begin(item.Name)
		if err != nil {
			return
		}
		if s != nil && s.IsDead() {
			if exited := s.ExitedAt(); exited != nil && now.Sub(*exited) > oneDay {
				if err := r.detach(item.Name, op, s); err == nil {
					if err := s.CloseAndWait(context.Background(), "stale-eviction"); err != nil {
						slog.Error("closing stale session", "name", item.Name, "error", err)
					}
				}
			}
		}
		r.finish(item.Name, op)
	}
}

// CloseAll closes every running session and empties the registry.
func (r *SessionRegistry) CloseAll(reason string) {
	r.mu.Lock()
	sessions, pending := r.drain()
	r.mu.Unlock()
	if err := closeSessions(sessions, pending, reason); err != nil {
		slog.Error("closing sessions", "error", err)
	}
}

func (r *SessionRegistry) drain() ([]*session.Session, []<-chan struct{}) {
	sessions := make([]*session.Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		sessions = append(sessions, s)
	}
	r.sessions = make(map[string]*session.Session)
	pending := make([]<-chan struct{}, 0, len(r.pending))
	for _, op := range r.pending {
		op.changed = true
		pending = append(pending, op.done)
	}
	return sessions, pending
}

func closeSessions(sessions []*session.Session, pending []<-chan struct{}, reason string) error {
	errs := make(chan error, len(sessions))
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Go(func() { errs <- s.CloseAndWait(context.Background(), reason) })
	}
	for _, done := range pending {
		<-done
	}
	wg.Wait()
	close(errs)
	var result error
	for err := range errs {
		result = errors.Join(result, err)
	}
	return result
}

// Shutdown permanently rejects new owners and joins all installed or pending children.
// Cancellation stops the caller's wait; the owned cleanup continues to completion.
func (r *SessionRegistry) Shutdown(ctx context.Context, reason string) error {
	r.mu.Lock()
	if !r.stopped {
		r.stopped = true
		r.shutdownDone = make(chan struct{})
		sessions, pending := r.drain()
		go func() {
			err := closeSessions(sessions, pending, reason)
			r.mu.Lock()
			defer r.mu.Unlock()
			r.shutdownErr = err
			close(r.shutdownDone)
		}()
	}
	done := r.shutdownDone
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.shutdownErr
	}
}
