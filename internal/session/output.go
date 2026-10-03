package session

import (
	"fmt"
	"image/color"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gitpod-io/xterm-go"
	"github.com/remorses/tuistory/internal/screenshot"
)

var ansiRegex = regexp.MustCompile(`\x1b(\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|[PX^_][^\x1b]*\x1b\\|[NOnEHM78])`)

// StripANSI removes ANSI escape codes from string content.
func StripANSI(s string) string {
	cleaned := ansiRegex.ReplaceAllString(s, "")
	cleaned = strings.ReplaceAll(cleaned, "\r\n", "\n")
	cleaned = strings.ReplaceAll(cleaned, "\r", "")
	return cleaned
}

// WaitForData blocks until the first byte arrives or timeout occurs.
func (s *Session) WaitForData(timeout time.Duration) error {
	s.mu.Lock()
	if s.hasReceivedData {
		s.mu.Unlock()
		return nil
	}
	if s.closed || (s.isDead && s.readFinished) {
		info := s.exitInfo
		s.mu.Unlock()
		return formatExitError(info, "")
	}

	ch := make(chan struct{})
	s.dataWaiters = append(s.dataWaiters, ch)
	readDone := s.readDone
	processDone := s.processDone
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.dataWaiters = slices.DeleteFunc(s.dataWaiters, func(waiter chan struct{}) bool { return waiter == ch })
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ch:
	case <-readDone:
	case <-timer.C:
		return fmt.Errorf("waitForData timed out after %v - no data received from PTY", timeout)
	}
	s.mu.RLock()
	received, closed := s.hasReceivedData, s.closed
	s.mu.RUnlock()
	if received {
		return nil
	}
	if !closed {
		select {
		case <-processDone:
		case <-timer.C:
			return fmt.Errorf("waitForData timed out after %v - no data received from PTY", timeout)
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return formatExitError(s.exitInfo, "")
}

// WaitIdle blocks until terminal output has paused for idleDelay.
func (s *Session) WaitIdle(timeout time.Duration) error {
	s.mu.Lock()
	if s.idleTimer == nil {
		s.mu.Unlock()
		time.Sleep(15 * time.Millisecond)
		return nil
	}

	ch := make(chan struct{})
	s.idleWaiters = append(s.idleWaiters, ch)
	s.mu.Unlock()

	select {
	case <-ch:
		return nil
	case <-time.After(timeout):
		s.mu.Lock()
		s.idleWaiters = slices.DeleteFunc(s.idleWaiters, func(waiter chan struct{}) bool { return waiter == ch })
		s.mu.Unlock()
		return nil
	}
}

// Text extracts the terminal screen as formatted text.
func (s *Session) Text(opts TextOptions) (string, error) {
	if opts.Immediate {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.renderTextLocked(opts.Only, opts.TrimEnd, opts.ShowCursor), nil
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 1000 * time.Millisecond
	}

	waitFor := opts.WaitFor
	if waitFor == nil {
		waitFor = func(txt string) bool { return len(strings.TrimSpace(txt)) > 0 }
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_ = s.WaitIdle(15 * time.Millisecond)

		s.mu.RLock()
		txt := s.renderTextLocked(opts.Only, opts.TrimEnd, opts.ShowCursor)
		waitText := s.renderTextLocked(opts.Only, opts.TrimEnd, boolPtr(false))
		isDead := s.isDead
		readFinished := s.readFinished
		exitInfo := s.exitInfo
		s.mu.RUnlock()

		cleanWaitText := strings.ReplaceAll(waitText, CursorChar, "")
		if waitFor(cleanWaitText) {
			_ = s.WaitIdle(15 * time.Millisecond)
			s.mu.RLock()
			finalTxt := s.renderTextLocked(opts.Only, opts.TrimEnd, opts.ShowCursor)
			s.mu.RUnlock()
			return finalTxt, nil
		}

		if isDead && readFinished {
			return "", formatExitError(exitInfo, txt)
		}

		time.Sleep(15 * time.Millisecond)
	}

	s.mu.RLock()
	finalTxt := s.renderTextLocked(opts.Only, opts.TrimEnd, opts.ShowCursor)
	isDead := s.isDead
	exitInfo := s.exitInfo
	s.mu.RUnlock()

	if isDead {
		return "", formatExitError(exitInfo, finalTxt)
	}

	return "", fmt.Errorf("text() timed out after %v waiting for condition. Current terminal content:\n%s", timeout, finalTxt)
}

func (s *Session) renderTextLocked(filter *StyleFilter, trimEnd bool, showCursorOpt *bool) string {
	showCursor := s.showCursor
	if showCursorOpt != nil {
		showCursor = *showCursorOpt
	}

	buf := s.term.Buffer()
	cursorX := s.term.CursorX()
	cursorY := s.term.CursorY()
	cursorVisible := !s.term.IsCursorHidden()

	rows := buf.Lines.Length()
	lines := make([]string, rows)

	for y := 0; y < rows; y++ {
		lineIdx := y
		if lineIdx >= buf.Lines.Length() {
			lines[y] = ""
			continue
		}

		line := buf.Lines.Get(lineIdx)
		if line == nil {
			lines[y] = ""
			continue
		}

		var sb strings.Builder
		for x := 0; x < s.cols; x++ {
			if x >= line.Len {
				sb.WriteRune(' ')
				continue
			}

			cell := line.LoadCell(x, xterm.NewCellData())
			if cell.GetWidth() == 0 {
				continue
			}
			charStr := cell.GetChars()
			if charStr == "" {
				charStr = " "
			}
			if showCursor && cursorVisible && y == buf.YBase+cursorY && x == cursorX {
				sb.WriteString(CursorChar)
				if cell.GetWidth() > 1 {
					sb.WriteString(strings.Repeat(" ", cell.GetWidth()-1))
				}
				continue
			}

			if filter != nil {
				matches := true
				if filter.Bold != nil {
					matches = matches && ((cell.IsBold() != 0) == *filter.Bold)
				}
				if filter.Italic != nil {
					matches = matches && ((cell.IsItalic() != 0) == *filter.Italic)
				}
				if filter.Underline != nil {
					matches = matches && ((cell.IsUnderline() != 0) == *filter.Underline)
				}
				if filter.Foreground != "" {
					matches = matches && !cell.IsFgDefault() && matchesColor(filter.Foreground, screenshot.CellColor(cell.GetFgColor(), cell.IsFgRGB(), cell.IsFgPalette(), color.RGBA{}))
				}
				if filter.Background != "" {
					matches = matches && !cell.IsBgDefault() && matchesColor(filter.Background, screenshot.CellColor(cell.GetBgColor(), cell.IsBgRGB(), cell.IsBgPalette(), color.RGBA{}))
				}
				if matches {
					sb.WriteString(charStr)
				} else {
					sb.WriteString(strings.Repeat(" ", cell.GetWidth()))
				}
			} else {
				sb.WriteString(charStr)
			}
		}

		lineStr := sb.String()

		lines[y] = strings.TrimRight(lineStr, " \t\r")
	}

	if !trimEnd {
		return "\n" + strings.Join(lines, "\n")
	}

	lastNonEmpty := len(lines) - 1
	for lastNonEmpty >= 0 && strings.TrimSpace(lines[lastNonEmpty]) == "" {
		lastNonEmpty--
	}
	if lastNonEmpty < 0 {
		return "\n"
	}

	return "\n" + strings.Join(lines[:lastNonEmpty+1], "\n")
}

// WaitForText waits for a plain string or regex pattern to appear.
func (s *Session) WaitForText(pattern string, timeout time.Duration) (string, error) {
	re, err := ParsePattern(pattern)
	if err != nil {
		return "", err
	}

	return s.Text(TextOptions{
		Timeout: timeout,
		WaitFor: func(t string) bool {
			return re.MatchString(t)
		},
	})
}

// Read returns all process output since the last Read call, stripped of ANSI escapes.
func (s *Session) Read() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.outputReadIndex >= len(s.outputChunks) {
		return ""
	}

	var sb strings.Builder
	for s.outputReadIndex < len(s.outputChunks) {
		sb.WriteString(s.outputChunks[s.outputReadIndex])
		s.outputReadIndex++
	}

	return StripANSI(sb.String())
}

// ReadAll returns all buffered output (up to 1MB) without advancing the read cursor.
func (s *Session) ReadAll() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.outputChunks) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, chunk := range s.outputChunks {
		sb.WriteString(chunk)
	}

	return StripANSI(sb.String())
}

// HasUnreadOutput checks if new unread output is waiting.
func (s *Session) HasUnreadOutput() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.outputReadIndex < len(s.outputChunks)
}

// WaitForUnreadOutput blocks until new output, process exit, closing, or timeout.
func (s *Session) WaitForUnreadOutput(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		s.mu.RLock()
		if s.outputReadIndex < len(s.outputChunks) || (s.isDead && s.readFinished) || s.closed {
			unread := s.outputReadIndex < len(s.outputChunks)
			s.mu.RUnlock()
			return unread
		}
		changed := s.outputChanged
		s.mu.RUnlock()
		select {
		case <-changed:
		case <-timer.C:
			return s.HasUnreadOutput()
		}
	}
}

// GetRawOutput returns the full buffered output with ANSI escape codes intact.
func (s *Session) GetRawOutput() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.Join(s.outputChunks, "")
}

// RenderScreenshot holds the session lock throughout terminal buffer reads.
func (s *Session) RenderScreenshot(opts screenshot.Options) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return screenshot.RenderTerminal(s.term, opts)
}

func matchesColor(raw string, c color.RGBA) bool {
	return strings.EqualFold(raw, fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B))
}

func formatExitError(info *ExitInfo, lastOutput string) error {
	code := 1
	sig := ""
	if info != nil {
		code = info.ExitCode
		if info.Signal != 0 {
			sig = fmt.Sprintf(" (signal: %d)", info.Signal)
		}
	}
	lines := strings.Split(lastOutput, "\n")
	trimmedLast := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			start := i - 30
			if start < 0 {
				start = 0
			}
			trimmedLast = strings.Join(lines[start:i+1], "\n")
			break
		}
	}
	if trimmedLast == "" {
		trimmedLast = "<no output>"
	}
	return fmt.Errorf("Process exited with code %d%s while waiting for condition. Last output:\n%s", code, sig, trimmedLast)
}

func boolPtr(b bool) *bool {
	return &b
}
