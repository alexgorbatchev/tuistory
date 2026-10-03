package session

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestCaptureFramesRejectsInvalidOptionsBeforeInput(t *testing.T) {
	for _, tt := range []struct {
		name     string
		count    int
		interval time.Duration
		want     string
	}{
		{"negative count", -1, 0, "count must be positive"},
		{"zero count", 0, 0, "count must be positive"},
		{"negative interval", 1, -time.Millisecond, "interval must be nonnegative"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := inputEchoSession(t)
			var frames []string
			var err error
			func() {
				defer func() {
					if v := recover(); v != nil {
						t.Errorf("CaptureFrames panicked on user input: %v", v)
					}
				}()
				frames, err = s.CaptureFrames([]string{"z"}, tt.count, tt.interval)
			}()
			if err == nil || !strings.Contains(err.Error(), tt.want) || frames != nil {
				t.Errorf("CaptureFrames = %q, %v; want validation error %q", frames, err, tt.want)
			}
			assertNoInput(t, s)
		})
	}
}

func TestCaptureFramesZeroInterval(t *testing.T) {
	s := inputEchoSession(t)
	frames, err := s.CaptureFrames([]string{"z"}, 2, 0)
	if err != nil || len(frames) != 2 {
		t.Fatalf("CaptureFrames = %q, %v", frames, err)
	}
	if _, err := s.WaitForText("z", time.Second); err != nil {
		t.Fatalf("keypress not delivered: %v", err)
	}
}

func TestCaptureFramesStopsWhenClosed(t *testing.T) {
	for _, count := range []int{100, math.MaxInt} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s := inputEchoSession(t)
			done := make(chan error, 1)
			go func() {
				var err error
				defer func() {
					if v := recover(); v != nil {
						err = fmt.Errorf("CaptureFrames panicked: %v", v)
					}
					done <- err
				}()
				_, err = s.CaptureFrames([]string{"z"}, count, time.Hour)
			}()
			if _, err := s.WaitForText("z", time.Second); err != nil {
				t.Fatalf("keypress not delivered before cancellation: %v", err)
			}
			s.Close("cancel-frame-capture")
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "session is closed") {
					t.Fatalf("capture did not stop with a close error: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("capture did not interrupt its interval when closed")
			}
		})
	}
}

func TestCaptureFramesRejectsClosedSessionWithoutKeys(t *testing.T) {
	s := inputEchoSession(t)
	s.Close("test")
	frames, err := s.CaptureFrames(nil, 1, 0)
	if err == nil || !strings.Contains(err.Error(), "session is closed") || frames != nil {
		t.Fatalf("closed session captured stale frames: %q, error %v", frames, err)
	}
}

func TestScrollRejectsNegativeLinesBeforeInput(t *testing.T) {
	for _, direction := range []string{"up", "down"} {
		t.Run(direction, func(t *testing.T) {
			s := inputEchoSession(t)
			var err error
			func() {
				defer func() {
					if v := recover(); v != nil {
						t.Errorf("scroll panicked on user input: %v", v)
					}
				}()
				if direction == "up" {
					err = s.ScrollUp(-1, nil, nil)
				} else {
					err = s.ScrollDown(-1, nil, nil)
				}
			}()
			if err == nil || !strings.Contains(err.Error(), "lines must be nonnegative") {
				t.Errorf("scroll = %v, want nonnegative lines error", err)
			}
			assertNoInput(t, s)
		})
	}
}

func TestScrollLargeCountStopsWhenClosed(t *testing.T) {
	for _, direction := range []string{"up", "down"} {
		t.Run(direction, func(t *testing.T) {
			s := inputEchoSession(t)
			done := make(chan error, 1)
			go func() {
				var err error
				defer func() {
					if v := recover(); v != nil {
						err = fmt.Errorf("scroll panicked: %v", v)
					}
					done <- err
				}()
				if direction == "up" {
					err = s.ScrollUp(math.MaxInt, nil, nil)
				} else {
					err = s.ScrollDown(math.MaxInt, nil, nil)
				}
			}()
			event := "\x1b[<64;21;2M"
			if direction == "down" {
				event = "\x1b[<65;21;2M"
			}
			deadline := time.Now().Add(time.Second)
			for !strings.Contains(s.GetRawOutput(), event) && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if !strings.Contains(s.GetRawOutput(), event) {
				t.Error("no actual scroll event received before cancellation")
			}
			s.Close("cancel-scrolling")
			select {
			case err := <-done:
				if err == nil || strings.Contains(err.Error(), "panicked") {
					t.Fatalf("scroll did not stop with a write error: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("scroll did not stop when session closed")
			}
		})
	}
}

func TestResizeRejectsInvalidDimensionsWithoutMutation(t *testing.T) {
	for _, tt := range []struct {
		name       string
		cols, rows int
		want       string
	}{
		{"negative columns", -1, 3, "cols must be between 2 and 65535"},
		{"zero columns", 0, 3, "cols must be between 2 and 65535"},
		{"one column", 1, 3, "cols must be between 2 and 65535"},
		{"overflow columns", 65536, 3, "cols must be between 2 and 65535"},
		{"negative rows", 40, -1, "rows must be between 1 and 65535"},
		{"zero rows", 40, 0, "rows must be between 1 and 65535"},
		{"overflow rows", 40, 65536, "rows must be between 1 and 65535"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := inputEchoSession(t)
			before, err := s.Text(TextOptions{Immediate: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Resize(tt.cols, tt.rows); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Resize(%d, %d) = %v, want %q", tt.cols, tt.rows, err, tt.want)
			}
			assertTerminalDimensions(t, s, 40, 3)
			after, err := s.Text(TextOptions{Immediate: true})
			if err != nil || after != before {
				t.Errorf("Resize changed snapshot: before %.80q (%d bytes), after %.80q (%d bytes), error %v", before, len(before), after, len(after), err)
			}
		})
	}
}

func TestResizeNativeDimensionBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name       string
		cols, rows int
	}{
		{"minimum", 2, 1},
		{"maximum columns", 65535, 1},
		{"maximum rows", 2, 65535},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := terminalSession(t, 40, 3, "ready")
			master, slave, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := master.Close(); err != nil {
					t.Error(err)
				}
				if err := slave.Close(); err != nil {
					t.Error(err)
				}
			})
			s.ptmx = master
			if err := s.Resize(tt.cols, tt.rows); err != nil {
				t.Fatal(err)
			}
			assertTerminalDimensions(t, s, tt.cols, tt.rows)
		})
	}
}

func TestResizePTYFailurePreservesTerminalState(t *testing.T) {
	s := terminalSession(t, 40, 3, "ready")
	f, err := os.Create(filepath.Join(t.TempDir(), "not-a-pty"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	s.ptmx = f
	before, err := s.Text(TextOptions{Immediate: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Resize(80, 6); err == nil {
		t.Error("Resize accepted a file that is not a PTY")
	}
	if s.Cols() != 40 || s.Rows() != 3 || s.term.Cols() != 40 || s.term.Rows() != 3 {
		t.Errorf("failed PTY resize changed dimensions to %dx%d, emulator %dx%d", s.Cols(), s.Rows(), s.term.Cols(), s.term.Rows())
	}
	after, err := s.Text(TextOptions{Immediate: true})
	if err != nil || after != before {
		t.Errorf("failed PTY resize changed snapshot: %q -> %q, error %v", before, after, err)
	}
}

func TestInputRejectsStoppedSessionWithoutMutation(t *testing.T) {
	for _, state := range []string{"closed", "exited"} {
		t.Run(state, func(t *testing.T) {
			s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "printf ready; exit 7"}, Cols: 40, Rows: 3})
			if !s.WaitForExit(time.Second) {
				t.Fatal("child did not exit")
			}
			if err := s.WaitForData(time.Second); err != nil {
				t.Fatal(err)
			}
			want := "PTY process has exited"
			if state == "closed" {
				s.Close("test")
				want = "session is closed"
			}
			before, err := s.Text(TextOptions{Immediate: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, op := range []struct {
				name string
				run  func() error
			}{
				{"resize", func() error { return s.Resize(80, 6) }},
				{"empty type", func() error { return s.Type("") }},
				{"modifier capture", func() error {
					frames, err := s.CaptureFrames([]string{"ctrl"}, 1, 0)
					if frames != nil {
						t.Errorf("stopped capture returned frames: %q", frames)
					}
					return err
				}},
			} {
				t.Run(op.name, func(t *testing.T) {
					if err := op.run(); err == nil || !strings.Contains(err.Error(), want) {
						t.Errorf("%s = %v, want %q", op.name, err, want)
					}
				})
			}
			s.mu.RLock()
			cols, rows := s.cols, s.rows
			termCols, termRows := s.term.Cols(), s.term.Rows()
			s.mu.RUnlock()
			if cols != 40 || rows != 3 || termCols != 40 || termRows != 3 {
				t.Errorf("stopped resize changed dimensions: stored %dx%d, emulator %dx%d", cols, rows, termCols, termRows)
			}
			after, err := s.Text(TextOptions{Immediate: true})
			if err != nil || after != before {
				t.Errorf("stopped resize changed screen: %q -> %q, error %v", before, after, err)
			}
		})
	}
}

func inputEchoSession(t *testing.T) *Session {
	t.Helper()
	s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "stty raw -echo; printf ready; cat"}, Cols: 40, Rows: 3, IdleDelay: time.Millisecond})
	if _, err := s.WaitForText("ready", time.Second); err != nil {
		t.Fatal(err)
	}
	s.Read()
	return s
}

func assertNoInput(t *testing.T, s *Session) {
	t.Helper()
	if s.WaitForUnreadOutput(50 * time.Millisecond) {
		t.Errorf("invalid operation sent PTY input: %q", s.Read())
	}
	if got := s.ReadAll(); got != "ready" {
		t.Errorf("invalid operation changed PTY output: %q", got)
	}
}

func assertTerminalDimensions(t *testing.T, s *Session, cols, rows int) {
	t.Helper()
	s.mu.RLock()
	defer s.mu.RUnlock()
	ws, err := pty.GetsizeFull(s.ptmx)
	if err != nil {
		t.Fatal(err)
	}
	if s.cols != cols || s.rows != rows || s.term.Cols() != cols || s.term.Rows() != rows || int(ws.Cols) != cols || int(ws.Rows) != rows {
		t.Errorf("dimensions: stored %dx%d, emulator %dx%d, PTY %dx%d; want %dx%d", s.cols, s.rows, s.term.Cols(), s.term.Rows(), ws.Cols, ws.Rows, cols, rows)
	}
}
