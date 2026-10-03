package session

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/remorses/tuistory/internal/process"
	"golang.org/x/sys/unix"
)

// Subscribe registers a listener for raw PTY data chunks and returns an unsubscribe func.
func (s *Session) Subscribe(cb func(string)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.nextSubscriberID
	s.nextSubscriberID++
	ch := make(chan string, 128)
	s.subscribers[id] = ch

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-ch:
				if !ok {
					return
				}
				cb(data)
			}
		}
	}()

	return func() {
		cancel()
		s.mu.Lock()
		defer s.mu.Unlock()
		if current, ok := s.subscribers[id]; ok {
			delete(s.subscribers, id)
			close(current)
		}
	}
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

// Close terminates the PTY session and kills its process group.
func (s *Session) Close(reason string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.closeReason = reason
	close(s.outputChanged)
	s.outputChanged = make(chan struct{})

	listeners := s.closeListeners
	s.closeListeners = nil

	for _, sub := range s.subscribers {
		close(sub)
	}
	s.subscribers = make(map[int]chan string)

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
	for _, fn := range listeners {
		fn.callback(reason)
	}

	if ptmx != nil {
		_ = ptmx.Close()
	}

	if cmd != nil && cmd.Process != nil {
		pid := cmd.Process.Pid
		process.KillSessionGroups(pid, unix.SIGTERM)
		time.AfterFunc(killGraceDuration, func() {
			process.KillSessionGroups(pid, unix.SIGKILL)
		})
	}

	if termcastSuffix != "" {
		bundleDir := filepath.Join(cwd, ".termcast-bundle")
		_ = os.Remove(filepath.Join(bundleDir, "data-"+termcastSuffix+".db"))
		_ = os.Remove(filepath.Join(bundleDir, "data-"+termcastSuffix+".db-shm"))
		_ = os.Remove(filepath.Join(bundleDir, "data-"+termcastSuffix+".db-wal"))
	}
}
