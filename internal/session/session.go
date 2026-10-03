package session

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gitpod-io/xterm-go"
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

	subscribers      map[int]*subscription
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
		subscribers:      make(map[int]*subscription),
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
			var subscribers []*subscription
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
					subscribers = append(subscribers, sub)
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
			for _, sub := range subscribers {
				select {
				case <-sub.done:
				case sub.chunks <- chunk:
				}
			}
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
