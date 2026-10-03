package session

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/remorses/tuistory/internal/process"
	"golang.org/x/sys/unix"
)

const subscriptionCapacity = 128

type subscription struct {
	chunks chan string
	done   <-chan struct{}
	cancel context.CancelFunc
}

// Subscribe registers a live-only listener for raw PTY data chunks. Callbacks
// run outside the session lock and must return to allow delivery to continue.
func (s *Session) Subscribe(cb func(string)) func() {
	return s.subscribe(false, func(_ context.Context, data string) { cb(data) })
}

// SubscribeWithBuffer atomically registers a listener and delivers buffered
// output before live chunks. Closing cancels even an in-flight history callback.
func (s *Session) SubscribeWithBuffer(cb func(context.Context, string)) func() {
	return s.subscribe(true, cb)
}

func (s *Session) subscribe(replay bool, cb func(context.Context, string)) func() {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return func() {}
	}

	id := s.nextSubscriberID
	s.nextSubscriberID++
	sub := &subscription{chunks: make(chan string, subscriptionCapacity), done: ctx.Done(), cancel: cancel}
	history := ""
	if replay {
		history = strings.Join(s.outputChunks, "")
	}
	s.subscribers[id] = sub
	s.mu.Unlock()
	unsubscribe := func() {
		cancel()
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.subscribers, id)
	}
	if history != "" {
		cb(ctx, history)
	}
	if ctx.Err() != nil {
		unsubscribe()
		return unsubscribe
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case data := <-sub.chunks:
				if ctx.Err() != nil {
					return
				}
				cb(ctx, data)
			}
		}
	}()

	return unsubscribe
}

// KillProcess sends SIGTERM to the session process groups.
func (s *Session) KillProcess() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd != nil && s.cmd.Process != nil {
		process.KillSessionGroups(s.cmd.Process.Pid, unix.SIGTERM)
	}
}

// OnExit registers a listener called when the PTY process exits.
func (s *Session) OnExit(cb func(ExitInfo)) func() {
	s.mu.Lock()
	if s.exitInfo != nil {
		info := *s.exitInfo
		s.mu.Unlock()
		cb(info)
		return func() {}
	}
	id := s.nextListenerID
	s.nextListenerID++
	s.exitListeners = append(s.exitListeners, exitListener{id: id, callback: cb})
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.exitListeners = slices.DeleteFunc(s.exitListeners, func(listener exitListener) bool { return listener.id == id })
	}
}

// OnClosing registers a callback for session closing.
func (s *Session) OnClosing(cb func(string)) func() {
	s.mu.Lock()
	if s.closed {
		reason := s.closeReason
		s.mu.Unlock()
		cb(reason)
		return func() {}
	}
	id := s.nextListenerID
	s.nextListenerID++
	s.closeListeners = append(s.closeListeners, closeListener{id: id, callback: cb})
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.closeListeners = slices.DeleteFunc(s.closeListeners, func(listener closeListener) bool { return listener.id == id })
	}
}

// WaitForExit waits up to timeout for the child process to exit.
func (s *Session) WaitForExit(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.RLock()
		isDead := s.isDead
		s.mu.RUnlock()
		if isDead {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// Close stops terminal I/O and starts termination of the session process groups.
// CloseAndWait joins termination when the caller must wait for cleanup.
func (s *Session) Close(reason string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.terminalReplyStop)
	s.closeReason = reason
	close(s.outputChanged)
	s.outputChanged = make(chan struct{})

	listeners := s.closeListeners
	s.closeListeners = nil

	for _, sub := range s.subscribers {
		sub.cancel()
	}
	s.subscribers = make(map[int]*subscription)

	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}

	for _, ch := range s.dataWaiters {
		close(ch)
	}
	s.dataWaiters = nil

	for _, ch := range s.idleWaiters {
		close(ch)
	}
	s.idleWaiters = nil

	cmd := s.cmd
	ptmx := s.ptmx
	termcastSuffix := s.termcastDbSuffix
	cwd := s.cwd
	s.mu.Unlock()

	if ptmx != nil {
		_ = ptmx.Close() // Closing cancels pending PTY I/O before joining it.
	}
	go s.finishClose(cmd, termcastSuffix, cwd)

	for _, fn := range listeners {
		fn.callback(reason)
	}
}
