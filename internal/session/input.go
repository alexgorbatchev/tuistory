package session

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/creack/pty"
	"github.com/gitpod-io/xterm-go"
	"github.com/remorses/tuistory/internal/keys"
)

// WriteRaw writes raw bytes directly to the PTY.
func (s *Session) WriteRaw(data string) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return errors.New("cannot writeRaw: session is closed")
	}
	if s.isDead {
		s.mu.RUnlock()
		return errors.New("cannot writeRaw: PTY process has exited")
	}
	ptmx := s.ptmx
	s.mu.RUnlock()

	_, err := ptmx.WriteString(data)
	return err
}

// Type sends text character by character with 1ms delay.
func (s *Session) Type(text string) error {
	for _, ch := range text {
		if err := s.WriteRaw(string(ch)); err != nil {
			return err
		}
		time.Sleep(time.Millisecond)
	}
	return s.WaitIdle(s.idleDelay * 2)
}

// Press sends key chords to the PTY.
func (s *Session) Press(keyTokens []string) error {
	code := keys.GetKeyCode(keyTokens)
	if code == "" {
		return nil
	}
	if err := s.WriteRaw(code); err != nil {
		return err
	}
	return s.WaitIdle(s.idleDelay * 2)
}

// Resize updates the terminal dimensions and notifies the PTY and emulator.
func (s *Session) Resize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cols = cols
	s.rows = rows
	s.term.Resize(cols, rows)

	if s.ptmx != nil && !s.closed && !s.isDead {
		return pty.Setsize(s.ptmx, &pty.Winsize{
			Rows: uint16(rows),
			Cols: uint16(cols),
		})
	}
	return nil
}

// ClickAt sends an SGR mouse click at 0-indexed terminal coordinates.
func (s *Session) ClickAt(x, y int) error {
	s.mu.RLock()
	if s.closed || s.isDead {
		s.mu.RUnlock()
		return errors.New("cannot clickAt: session not running")
	}
	ptmx := s.ptmx
	idleDelay := s.idleDelay
	s.mu.RUnlock()

	press := fmt.Sprintf("\x1b[<0;%d;%dM", x+1, y+1)
	release := fmt.Sprintf("\x1b[<0;%d;%dm", x+1, y+1)

	if _, err := ptmx.WriteString(press); err != nil {
		return err
	}
	if _, err := ptmx.WriteString(release); err != nil {
		return err
	}
	return s.WaitIdle(idleDelay)
}

// Click finds pattern on screen and clicks its location.
func (s *Session) Click(pattern string, first bool, timeout time.Duration) error {
	var re *regexp.Regexp
	if strings.HasPrefix(pattern, "/") && strings.LastIndex(pattern, "/") > 0 {
		lastSlash := strings.LastIndex(pattern, "/")
		body := pattern[1:lastSlash]
		flags := pattern[lastSlash+1:]
		expr := body
		if strings.Contains(flags, "i") {
			expr = "(?i)" + expr
		}
		if parsed, err := regexp.Compile(expr); err == nil {
			re = parsed
		}
	}
	if re == nil {
		re = regexp.MustCompile(regexp.QuoteMeta(pattern))
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_ = s.WaitIdle(15 * time.Millisecond)

		s.mu.RLock()
		buf := s.term.Buffer()
		var matches [][2]int
		for y := 0; y < s.rows; y++ {
			lineIdx := buf.YBase + y
			if lineIdx >= buf.Lines.Length() {
				continue
			}
			line := buf.Lines.Get(lineIdx)
			if line == nil {
				continue
			}
			lineStr := line.TranslateToString(true, 0, s.cols)
			locs := re.FindAllStringIndex(lineStr, -1)
			for _, loc := range locs {
				matches = append(matches, [2]int{columnAtByte(line, loc[0]), y})
			}
		}
		s.mu.RUnlock()

		if len(matches) == 1 || (len(matches) > 1 && first) {
			return s.ClickAt(matches[0][0], matches[0][1])
		}
		if len(matches) > 1 && !first {
			return fmt.Errorf("click(%q) found %d matches. Use { first: true } to click the first match", pattern, len(matches))
		}

		time.Sleep(20 * time.Millisecond)
	}

	return fmt.Errorf("click(%q) timed out after %v - pattern not found", pattern, timeout)
}

func columnAtByte(line *xterm.BufferLine, index int) int {
	offset := 0
	for x := 0; x < line.Len; x++ {
		cell := line.LoadCell(x, xterm.NewCellData())
		if cell.GetWidth() == 0 {
			continue
		}
		if offset >= index {
			return x
		}
		chars := cell.GetChars()
		if chars == "" {
			chars = " "
		}
		offset += len(chars)
		if offset > index {
			return x
		}
	}
	return line.Len
}

// ScrollUp sends SGR mouse scroll up events.
func (s *Session) ScrollUp(lines int, x, y *int) error {
	s.mu.RLock()
	col := s.cols / 2
	row := s.rows / 2
	s.mu.RUnlock()
	if x != nil {
		col = *x
	}
	if y != nil {
		row = *y
	}
	event := fmt.Sprintf("\x1b[<64;%d;%dM", col+1, row+1)
	return s.WriteRaw(strings.Repeat(event, lines))
}

// ScrollDown sends SGR mouse scroll down events.
func (s *Session) ScrollDown(lines int, x, y *int) error {
	s.mu.RLock()
	col := s.cols / 2
	row := s.rows / 2
	s.mu.RUnlock()
	if x != nil {
		col = *x
	}
	if y != nil {
		row = *y
	}
	event := fmt.Sprintf("\x1b[<65;%d;%dM", col+1, row+1)
	return s.WriteRaw(strings.Repeat(event, lines))
}

// CaptureFrames sends keys and captures frameCount frames separated by interval.
func (s *Session) CaptureFrames(keyTokens []string, count int, interval time.Duration) ([]string, error) {
	code := keys.GetKeyCode(keyTokens)
	if code != "" {
		if err := s.WriteRaw(code); err != nil {
			return nil, err
		}
	}

	frames := make([]string, count)
	for i := 0; i < count; i++ {
		txt, err := s.Text(TextOptions{Immediate: true})
		if err != nil {
			return nil, err
		}
		frames[i] = txt
		if i < count-1 {
			time.Sleep(interval)
		}
	}
	_ = s.WaitIdle(s.idleDelay)
	return frames, nil
}
