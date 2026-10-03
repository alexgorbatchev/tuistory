package session

import (
	"bytes"
	"image/png"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gitpod-io/xterm-go"
	"github.com/remorses/tuistory/internal/screenshot"
)

func TestSilentStartupExit(t *testing.T) {
	s, err := New(LaunchOptions{Command: "sh", Args: []string{"-c", "sleep 0.05; exit 7"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close("test") })
	err = s.WaitForData(time.Second)
	if err == nil || !strings.Contains(err.Error(), "code 7") {
		t.Fatalf("expected exit 7, got %v", err)
	}
}

func TestSessionCallbacksOutsideLock(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "sleep 0.05; exit 0"}})
	exited := make(chan struct{})
	s.OnExit(func(info ExitInfo) {
		if !s.IsDead() {
			t.Error("exit callback before death")
		}
		close(exited)
	})
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("exit callback deadlocked")
	}
	late := make(chan struct{})
	s.OnExit(func(ExitInfo) { close(late) })
	select {
	case <-late:
	case <-time.After(time.Second):
		t.Fatal("late exit callback not invoked")
	}
	closing := make(chan struct{})
	s.OnClosing(func(reason string) {
		if s.Command() != "sh -c sleep 0.05; exit 0" {
			t.Error("callback metadata unavailable")
		}
		close(closing)
	})
	s.Close("test")
	select {
	case <-closing:
	case <-time.After(time.Second):
		t.Fatal("closing callback deadlocked")
	}
}

func TestWaiterTimeoutCleanup(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "cat"})
	if err := s.WaitForData(time.Millisecond); err == nil {
		t.Fatal("expected no-output timeout")
	}
	s.mu.RLock()
	count := len(s.dataWaiters)
	s.mu.RUnlock()
	if count != 0 {
		t.Fatalf("retained %d data waiters", count)
	}
	if err := s.WriteRaw("ready\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitForData(time.Second); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitIdle(time.Millisecond); err != nil {
		t.Fatal(err)
	}
	s.mu.RLock()
	count = len(s.idleWaiters)
	s.mu.RUnlock()
	if count != 0 {
		t.Fatalf("retained %d idle waiters", count)
	}
	if err := s.WaitForData(time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForUnreadOutput(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "sleep 0.05; printf ready; sleep 0.05; exit 7"}})
	if s.WaitForUnreadOutput(time.Millisecond) {
		t.Fatal("unexpected initial output")
	}
	if !s.WaitForUnreadOutput(time.Second) || !strings.Contains(s.Read(), "ready") {
		t.Fatal("missing waited output")
	}
	if s.WaitForUnreadOutput(time.Second) || !s.IsDead() || s.ExitInfo().ExitCode != 7 {
		t.Fatal("exit did not wake output waiter")
	}
	if s.WaitForUnreadOutput(time.Second) {
		t.Fatal("unexpected output after exit")
	}
	closed := launchTestSession(t, LaunchOptions{Command: "cat"})
	done := make(chan bool, 1)
	go func() { done <- closed.WaitForUnreadOutput(time.Second) }()
	closed.Close("test")
	select {
	case unread := <-done:
		if unread {
			t.Fatal("unexpected closing output")
		}
	case <-time.After(time.Second):
		t.Fatal("close did not wake waiter")
	}
}

func TestClickUnicodeCoordinates(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "stty raw -echo; printf 'é界target'; cat"}, Cols: 40, Rows: 3, IdleDelay: time.Millisecond})
	if _, err := s.WaitForText("target", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := s.Click("target", true, time.Second); err != nil {
		t.Fatal(err)
	}
	if !s.WaitForUnreadOutput(time.Second) {
		t.Fatal("no mouse response")
	}
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(s.GetRawOutput(), "\x1b[<0;4;1M") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(s.GetRawOutput(), "\x1b[<0;4;1M") {
		t.Fatalf("wrong cell coordinate: %q", s.GetRawOutput())
	}
}

func TestScrollDuringResize(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "cat"})
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 100 {
			if err := s.Resize(40, 10); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Go(func() {
		for range 100 {
			if err := s.ScrollUp(1, nil, nil); err != nil {
				t.Error(err)
			}
			if err := s.ScrollDown(1, nil, nil); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Wait()
}

func TestScreenshotWhileOutputChanges(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "while :; do printf '\\033[Habcdefgh'; done"}, Cols: 20, Rows: 3})
	if err := s.WaitForData(time.Second); err != nil {
		t.Fatal(err)
	}
	for range 30 {
		data, err := s.RenderScreenshot(screenshot.Options{Padding: 2})
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() <= 0 {
			t.Fatal("empty screenshot")
		}
	}
}

func TestSessionMetadataAndLifecycle(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "printf '%s\\n' \"$REVIEW_ENV\"; sleep 10"}, Cwd: cwd, Env: map[string]string{"REVIEW_ENV": "value"}, Label: "review", Cols: 30, Rows: 4})
	if _, err := s.WaitForText("value", time.Second); err != nil {
		t.Fatal(err)
	}
	if s.Cols() != 30 || s.Rows() != 4 || s.Cwd() != cwd || s.Command() != "review" || s.StartedAt().IsZero() || s.IsDead() || s.ExitedAt() != nil || s.ExitInfo() != nil {
		t.Fatal("incorrect running metadata")
	}
	env := s.Env()
	env["REVIEW_ENV"] = "changed"
	if s.Env()["REVIEW_ENV"] != "value" {
		t.Fatal("environment mutated")
	}
	if !s.HasUnreadOutput() || !strings.Contains(s.GetRawOutput(), "value") {
		t.Fatal("missing buffered output")
	}
	if s.Read() == "" || s.Read() != "" || s.HasUnreadOutput() {
		t.Fatal("read cursor did not advance")
	}
	if err := s.Resize(40, 5); err != nil {
		t.Fatal(err)
	}
	if s.Cols() != 40 || s.Rows() != 5 {
		t.Fatal("resize did not update dimensions")
	}
	if s.WaitForExit(time.Millisecond) {
		t.Fatal("live process reported exited")
	}
	s.KillProcess()
	if !s.WaitForExit(time.Second) || !s.IsDead() || s.ExitInfo() == nil || s.ExitedAt() == nil {
		t.Fatal("missing exit metadata")
	}
	if err := s.WriteRaw("x"); err == nil {
		t.Fatal("write to dead process succeeded")
	}
	if err := s.ClickAt(1, 1); err == nil {
		t.Fatal("click to dead process succeeded")
	}
	s.Close("test")
	if err := s.WriteRaw("x"); err == nil {
		t.Fatal("write to closed process succeeded")
	}
	if err := s.Type("x"); err == nil {
		t.Fatal("type to closed process succeeded")
	}
	if err := s.Press([]string{"enter"}); err == nil {
		t.Fatal("press to closed process succeeded")
	}
	if err := s.Press(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CaptureFrames([]string{"enter"}, 1, time.Millisecond); err == nil {
		t.Fatal("capture write to closed process succeeded")
	}
	if err := s.Resize(25, 3); err != nil {
		t.Fatal(err)
	}
}

func TestCloseEscalatesForSignalIgnoringProcess(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "trap '' HUP TERM; printf ready; exec sleep 60"}})
	if err := s.WaitForData(time.Second); err != nil {
		t.Fatal(err)
	}
	s.Close("test")
	if s.WaitForExit(100 * time.Millisecond) {
		t.Fatal("process did not ignore TERM")
	}
	if !s.WaitForExit(killGraceDuration + time.Second) {
		t.Fatal("Close did not escalate to SIGKILL")
	}
	if info := s.ExitInfo(); info == nil || info.Signal != int(syscall.SIGKILL) {
		t.Fatalf("expected SIGKILL exit, got %+v", info)
	}
}

func TestMouseAndFrameCapture(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "stty raw -echo; printf 'target target'; cat"}, Cols: 40, Rows: 4, IdleDelay: 10 * time.Millisecond})
	if _, err := s.WaitForText("target", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := s.Click("target", false, time.Second); err == nil || !strings.Contains(err.Error(), "2 matches") {
		t.Fatalf("expected ambiguous match: %v", err)
	}
	if err := s.Click("/TARGET/i", true, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := s.Click("/[invalid/", true, 30*time.Millisecond); err == nil {
		t.Fatal("missing literal unexpectedly matched")
	}
	if err := s.Click("absent", false, 30*time.Millisecond); err == nil {
		t.Fatal("missing pattern unexpectedly matched")
	}
	x, y := 2, 1
	for _, err := range []error{s.ScrollUp(2, nil, nil), s.ScrollDown(2, &x, &y), s.ScrollUp(1, &x, &y), s.ScrollDown(1, nil, nil)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.WaitForText("target", time.Second); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.GetRawOutput(), "\x1b[<0;1;1M") {
		t.Fatalf("mouse coordinates missing: %q", s.GetRawOutput())
	}
	frames, err := s.CaptureFrames([]string{"z"}, 2, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || !strings.Contains(frames[1], "z") {
		t.Fatalf("frames missing input: %q", frames)
	}
	if _, err := s.CaptureFrames(nil, 1, time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionsReceiveOutput(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "cat"})
	data := make(chan string, 10)
	unsub := s.Subscribe(func(chunk string) { data <- chunk })
	if err := s.WriteRaw("hello\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case chunk := <-data:
		if !strings.Contains(chunk, "hello") {
			t.Fatalf("chunk %q", chunk)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber received no data")
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(unsub)
	}
	wg.Wait()
}

func TestEmptyAndStyledSnapshots(t *testing.T) {
	s := terminalSession(t, 40, 3, "\x1b[1mbold\x1b[0m \x1b[3mitalic\x1b[0m \x1b[4munder\x1b[0m")
	for _, tt := range []struct {
		filter *StyleFilter
		want   string
	}{
		{&StyleFilter{Bold: boolPtr(true)}, "\nbold"},
		{&StyleFilter{Italic: boolPtr(true)}, "\n     italic"},
		{&StyleFilter{Underline: boolPtr(true)}, "\n            under"},
	} {
		got, err := s.Text(TextOptions{Immediate: true, TrimEnd: true, Only: tt.filter})
		if err != nil || got != tt.want {
			t.Fatalf("got %q/%v want %q", got, err, tt.want)
		}
	}
	if _, err := s.WaitForText("/BOLD/i", time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WaitForText("/[broken/", 20*time.Millisecond); err == nil {
		t.Fatal("unexpected regex match")
	}
	empty := terminalSession(t, 10, 2, "")
	if empty.Read() != "" || empty.ReadAll() != "" {
		t.Fatal("empty output not empty")
	}
	got, err := empty.Text(TextOptions{Immediate: true, TrimEnd: true})
	if err != nil || got != "\n" {
		t.Fatalf("empty snapshot %q/%v", got, err)
	}
	if _, err := empty.Text(TextOptions{Timeout: time.Millisecond}); err == nil {
		t.Fatal("empty snapshot did not time out")
	}
	dead := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "exit 7"}})
	if !dead.WaitForExit(time.Second) {
		t.Fatal("child did not exit")
	}
	if err := dead.WaitForData(time.Second); err == nil {
		t.Fatal("dead no-output startup succeeded")
	}
	if _, err := dead.Text(TextOptions{Timeout: time.Nanosecond}); err == nil {
		t.Fatal("dead snapshot succeeded")
	}
	if _, err := New(LaunchOptions{Command: "/nonexistent-review-program"}); err == nil {
		t.Fatal("missing command launched")
	}
}

func launchTestSession(t *testing.T, opts LaunchOptions) *Session {
	t.Helper()
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close("test") })
	return s
}

func TestAlreadyIdle(t *testing.T) {
	s, err := New(LaunchOptions{Command: "sh", Args: []string{"-c", "echo ready; sleep 10"}, IdleDelay: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close("test") })
	if err := s.WaitForData(time.Second); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitIdle(time.Second); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := s.WaitIdle(300 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("already idle took %v", elapsed)
	}
}

func TestUnsubscribeAfterClose(t *testing.T) {
	s, err := New(LaunchOptions{Command: "cat"})
	if err != nil {
		t.Fatal(err)
	}
	unsubscribe := s.Subscribe(func(string) {})
	s.Close("test")
	unsubscribe()
	unsubscribe()
}

func TestSnapshotCellCoordinates(t *testing.T) {
	tests := []struct{ name, input, want string }{
		{"unicode", "éé", "\néé█"},
		{"wide", "界X", "\n界X█"},
		{"combining", "e\u0301X", "\ne\u0301X█"},
		{"overwrite", "éé\r\x1b[C", "\né█"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := terminalSession(t, 20, 3, tt.input)
			got, err := s.Text(TextOptions{Immediate: true, TrimEnd: true, ShowCursor: boolPtr(true)})
			if err != nil {
				t.Fatal(err)
			}
			if !utf8.ValidString(got) || got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestSnapshotIncludesScrollback(t *testing.T) {
	s := terminalSession(t, 20, 3, "line01\r\nline02\r\nline03\r\nline04\r\nline05")
	got, err := s.Text(TextOptions{Immediate: true, TrimEnd: true})
	if err != nil {
		t.Fatal(err)
	}
	if got != "\nline01\nline02\nline03\nline04\nline05" {
		t.Fatalf("snapshot: %q", got)
	}
	if _, err := s.WaitForText("line01", 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotColorFilters(t *testing.T) {
	s := terminalSession(t, 40, 3, "\x1b[38;2;255;0;0mred\x1b[0m plain \x1b[48;2;0;0;255mblue\x1b[0m")
	for _, tt := range []struct {
		name   string
		filter *StyleFilter
		want   string
	}{
		{"foreground", &StyleFilter{Foreground: "#ff0000"}, "\nred"},
		{"background", &StyleFilter{Background: "#0000ff"}, "\n          blue"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.Text(TextOptions{Immediate: true, TrimEnd: true, Only: tt.filter})
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func terminalSession(t *testing.T, cols, rows int, input string) *Session {
	t.Helper()
	term := xterm.New(xterm.WithCols(cols), xterm.WithRows(rows), xterm.WithScrollback(1000))
	if _, err := term.Write([]byte(input)); err != nil {
		t.Fatal(err)
	}
	return &Session{term: term, cols: cols, rows: rows}
}
