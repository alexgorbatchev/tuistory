package session

import "log/slog"

const terminalReplyCapacity = 128

// The emulator's callback runs inside readLoop's terminal lock. Collect replies
// from one bounded read before delivering them outside the lock, so input
// backpressure cannot prevent Close from stopping the reader and writer.
func (s *Session) startTerminalReplies() {
	s.terminalReplySub = s.term.OnData(func(data string) {
		s.terminalReplies = append(s.terminalReplies, data)
	})
	go func() {
		defer close(s.terminalReplyDone)
		for {
			select {
			case <-s.terminalReplyStop:
				return
			case data, ok := <-s.terminalReplyQueue:
				if !ok {
					return
				}
				if _, err := s.ptmx.WriteString(data); err != nil {
					s.mu.RLock()
					closed := s.closed
					s.mu.RUnlock()
					if !closed {
						slog.Debug("writing terminal response", "error", err)
					}
					return
				}
			}
		}
	}()
}

func (s *Session) sendTerminalReplies(replies []string) bool {
	for _, reply := range replies {
		select {
		case <-s.terminalReplyStop:
			return false
		case <-s.terminalReplyDone:
			return false
		case s.terminalReplyQueue <- reply:
		}
	}
	return true
}
