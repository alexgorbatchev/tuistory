package session

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gitpod-io/xterm-go"
	"github.com/remorses/tuistory/internal/keys"
	"github.com/remorses/tuistory/internal/process"
	"github.com/remorses/tuistory/internal/screenshot"
	"golang.org/x/sys/unix"
)

const (
	// CursorChar is the character used to mark cursor position in snapshots.
	CursorChar = "█"

	defaultCols       = 120
	defaultRows       = 36
	defaultIdleDelay  = 200 * time.Millisecond
	maxOutputBuffer   = 1_000_000
	killGraceDuration = 2 * time.Second
)

// LaunchOptions configures a new terminal session.
type LaunchOptions struct {
	Command     string
	Args        []string
	Cols        int
	Rows        int
	Cwd         string
	Env         map[string]string
	ShowCursor  bool
	Label       string
	IdleDelay   time.Duration
	WaitForData bool
}

// TextOptions configures snapshot text capture.
type TextOptions struct {
	Only       *StyleFilter
	WaitFor    func(string) bool
	Timeout    time.Duration
	TrimEnd    bool
	Immediate  bool
	ShowCursor *bool
}

// StyleFilter filters captured text by styling.
type StyleFilter struct {
	Bold       *bool
	Italic     *bool
	Underline  *bool
	Foreground string
	Background string
}

// ExitInfo captures child process termination metadata.
type ExitInfo struct {
	ExitCode int
	Signal   int
}

// Session manages a single background PTY session and its virtual terminal emulator.
type Session struct {
	mu sync.RWMutex

	ptmx *os.File
	cmd  *exec.Cmd
	term *xterm.Terminal

	cols       int
	rows       int
	cwd        string
	command    string
	env        map[string]string
	idleDelay  time.Duration
	showCursor bool
	startedAt  time.Time
	exitedAt   *time.Time

	isDead       bool
	exitInfo     *ExitInfo
	closed       bool
	closeReason  string
	readFinished bool
	readDone     chan struct{}
	processDone  chan struct{}

	hasReceivedData bool
	dataWaiters     []chan struct{}
	idleWaiters     []chan struct{}
	idleTimer       *time.Timer
	idleGeneration  uint64

	outputChunks    []string
	outputTotalLen  int
	outputReadIndex int
	outputChanged   chan struct{}

	subscribers      map[int]chan string
	nextSubscriberID int

	exitListeners  []exitListener
	closeListeners []closeListener
	nextListenerID int

	termcastDbSuffix string
}

type exitListener struct {
	id       int
	callback func(ExitInfo)
}

type closeListener struct {
	id       int
	callback func(string)
}

var ansiRegex = regexp.MustCompile(`\x1b(\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|[PX^_][^\x1b]*\x1b\\|[NOnEHM78])`)

// StripANSI removes ANSI escape codes from string content.
func StripANSI(s string) string {
	cleaned := ansiRegex.ReplaceAllString(s, "")
	cleaned = strings.ReplaceAll(cleaned, "\r\n", "\n")
	cleaned = strings.ReplaceAll(cleaned, "\r", "")
	return cleaned
}

// New creates and starts a new PTY session.
func New(opts LaunchOptions) (*Session, error) {
	cols := opts.Cols
	if cols <= 0 {
		cols = defaultCols
	}
	rows := opts.Rows
	if rows <= 0 {
		rows = defaultRows
	}

	idleDelay := opts.IdleDelay
	if idleDelay <= 0 {
		idleDelay = defaultIdleDelay
	}

	targetCwd := opts.Cwd
	if targetCwd == "" {
		targetCwd, _ = os.Getwd()
	}

	label := opts.Label
	if label == "" {
		if len(opts.Args) > 0 {
			label = opts.Command + " " + strings.Join(opts.Args, " ")
		} else {
			label = opts.Command
		}
	}

	termcastSuffix := opts.Env["TERMCAST_DB_SUFFIX"]
	var generatedTermcastSuffix string
	if termcastSuffix == "" {
		generatedTermcastSuffix = fmt.Sprintf("tuistory-%d-%d", os.Getpid(), time.Now().UnixNano())
		termcastSuffix = generatedTermcastSuffix
	}

	envMap := make(map[string]string)
	for k, v := range opts.Env {
		envMap[k] = v
	}

	cmdEnv := os.Environ()
	for k, v := range envMap {
		cmdEnv = append(cmdEnv, fmt.Sprintf("%s=%s", k, v))
	}
	cmdEnv = append(cmdEnv, "TERM=xterm-truecolor", "COLORTERM=truecolor", "TERMCAST_DB_SUFFIX="+termcastSuffix)

	cmd := exec.Command(opts.Command, opts.Args...)
	cmd.Dir = targetCwd
	cmd.Env = cmdEnv

	term := xterm.New(
		xterm.WithCols(cols),
		xterm.WithRows(rows),
		xterm.WithScrollback(1000),
	)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
	if err != nil {
		return nil, fmt.Errorf("starting pty: %w", err)
	}

	s := &Session{
		ptmx:             ptmx,
		cmd:              cmd,
		term:             term,
		cols:             cols,
		rows:             rows,
		cwd:              targetCwd,
		command:          label,
		env:              envMap,
		idleDelay:        idleDelay,
		showCursor:       opts.ShowCursor,
		startedAt:        time.Now(),
		outputChanged:    make(chan struct{}),
		readDone:         make(chan struct{}),
		processDone:      make(chan struct{}),
		subscribers:      make(map[int]chan string),
		termcastDbSuffix: generatedTermcastSuffix,
	}

	go s.readLoop()
	go s.waitLoop()

	return s, nil
}

func (s *Session) readLoop() {
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.readFinished = true
		close(s.readDone)
		close(s.outputChanged)
		s.outputChanged = make(chan struct{})
		for _, ch := range s.dataWaiters {
			close(ch)
		}
		s.dataWaiters = nil
	}()
	buf := make([]byte, 8192)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			s.mu.Lock()
			if !s.closed {
				_, _ = s.term.Write(buf[:n])

				s.outputChunks = append(s.outputChunks, chunk)
				close(s.outputChanged)
				s.outputChanged = make(chan struct{})
				s.outputTotalLen += len(chunk)
				for s.outputTotalLen > maxOutputBuffer && len(s.outputChunks) > 1 {
					dropped := s.outputChunks[0]
					s.outputChunks = s.outputChunks[1:]
					s.outputTotalLen -= len(dropped)
					if s.outputReadIndex > 0 {
						s.outputReadIndex--
					}
				}

				for _, sub := range s.subscribers {
					select {
					case sub <- chunk:
					default:
					}
				}

				if !s.hasReceivedData {
					s.hasReceivedData = true
					for _, ch := range s.dataWaiters {
						close(ch)
					}
					s.dataWaiters = nil
				}

				if s.idleTimer != nil {
					s.idleTimer.Stop()
				}
				s.idleGeneration++
				generation := s.idleGeneration
				s.idleTimer = time.AfterFunc(s.idleDelay, func() {
					s.mu.Lock()
					defer s.mu.Unlock()
					if s.closed || s.idleGeneration != generation {
						return
					}
					s.idleTimer = nil
					for _, ch := range s.idleWaiters {
						close(ch)
					}
					s.idleWaiters = nil
				})
			}
			s.mu.Unlock()
		}

		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
				return
			}
			return
		}
	}
}

func (s *Session) waitLoop() {
	err := s.cmd.Wait()

	s.mu.Lock()

	now := time.Now()
	s.exitedAt = &now
	s.isDead = true
	close(s.outputChanged)
	s.outputChanged = make(chan struct{})

	exitCode := 0
	signal := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				signal = int(status.Signal())
			}
		} else {
			exitCode = 1
		}
	}
	info := ExitInfo{ExitCode: exitCode, Signal: signal}
	s.exitInfo = &info
	close(s.processDone)

	for _, ch := range s.idleWaiters {
		close(ch)
	}
	s.idleWaiters = nil

	listeners := s.exitListeners
	s.exitListeners = nil
	s.mu.Unlock()
	for _, fn := range listeners {
		fn.callback(info)
	}
}

// IsDead returns whether the child process has exited.
func (s *Session) IsDead() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isDead
}

// ExitInfo returns the exit code and signal if dead.
func (s *Session) ExitInfo() *ExitInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.exitInfo == nil {
		return nil
	}
	info := *s.exitInfo
	return &info
}

// Cols returns the current terminal column width.
func (s *Session) Cols() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cols
}

// Rows returns the current terminal row height.
func (s *Session) Rows() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rows
}

// Cwd returns the session working directory.
func (s *Session) Cwd() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cwd
}

// Command returns the launch command label.
func (s *Session) Command() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.command
}

// Env returns the environment variables set at launch.
func (s *Session) Env() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make(map[string]string, len(s.env))
	for k, v := range s.env {
		res[k] = v
	}
	return res
}

// StartedAt returns the session start timestamp.
func (s *Session) StartedAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.startedAt
}

// ExitedAt returns when the process exited, if it has.
func (s *Session) ExitedAt() *time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.exitedAt == nil {
		return nil
	}
	exited := *s.exitedAt
	return &exited
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
	var re *regexp.Regexp
	if strings.HasPrefix(pattern, "/") && strings.LastIndex(pattern, "/") > 0 {
		lastSlash := strings.LastIndex(pattern, "/")
		body := pattern[1:lastSlash]
		flags := pattern[lastSlash+1:]
		expr := body
		if strings.Contains(flags, "i") {
			expr = "(?i)" + expr
		}
		parsed, err := regexp.Compile(expr)
		if err == nil {
			re = parsed
		}
	}
	if re == nil {
		re = regexp.MustCompile(regexp.QuoteMeta(pattern))
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

// Terminal returns the underlying virtual terminal emulator.
func (s *Session) Terminal() *xterm.Terminal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.term
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

	if s.cmd != nil && s.cmd.Process != nil && !s.isDead {
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
	isDead := s.isDead
	termcastSuffix := s.termcastDbSuffix
	cwd := s.cwd
	s.mu.Unlock()
	for _, fn := range listeners {
		fn.callback(reason)
	}

	if ptmx != nil {
		_ = ptmx.Close()
	}

	if cmd != nil && cmd.Process != nil && !isDead {
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
