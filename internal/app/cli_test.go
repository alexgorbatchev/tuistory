package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/remorses/tuistory/internal/relay"
)

func TestAppLaunchAndRead(t *testing.T) {
	reg := relay.NewSessionRegistry()
	defer reg.CloseAll("test-done")

	// Launch a session
	res := ExecuteCommand([]string{"launch", "echo hello-app", "-s", "app-test"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 {
		t.Fatalf("launch failed: %s", res.Stderr)
	}
	if !strings.Contains(res.Stdout, `Session "app-test" started`) {
		t.Fatalf("expected launch stdout, got: %q", res.Stdout)
	}

	// Wait for text
	res = ExecuteCommand([]string{"wait", "hello-app", "-s", "app-test", "--timeout", "5000"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 {
		t.Fatalf("wait failed: %s", res.Stderr)
	}
	if !strings.Contains(res.Stdout, "hello-app") {
		t.Fatalf("expected wait stdout to contain 'hello-app', got: %q", res.Stdout)
	}

	// Read
	res = ExecuteCommand([]string{"read", "-s", "app-test", "--all", "--trim"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 {
		t.Fatalf("read failed: %s", res.Stderr)
	}
	if !strings.Contains(res.Stdout, "hello-app") {
		t.Fatalf("expected read stdout to contain 'hello-app', got: %q", res.Stdout)
	}

	// Close
	res = ExecuteCommand([]string{"close", "-s", "app-test"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 {
		t.Fatalf("close failed: %s", res.Stderr)
	}
	if !strings.Contains(res.Stdout, `Session "app-test" closed`) {
		t.Fatalf("expected close stdout, got: %q", res.Stdout)
	}
}

func TestCommandSessionValidation(t *testing.T) {
	commands := [][]string{{"snapshot"}, {"read"}, {"screenshot"}, {"type", "hello"}, {"press", "enter"}, {"click", "hello"}, {"click-at", "0", "0"}, {"wait", "hello"}, {"wait-idle"}, {"scroll", "up"}, {"resize", "80", "24"}, {"capture-frames", "enter"}, {"close"}, {"restart"}}
	for _, args := range commands {
		t.Run(args[0], func(t *testing.T) {
			reg := relay.NewSessionRegistry()
			for _, name := range []string{"", "nonexistent"} {
				argv := append([]string{}, args...)
				want := "-s/--session is required"
				if name != "" {
					argv = append(argv, "-s", name)
					want = "not found"
				}
				res := ExecuteCommand(argv, reg, ".", nil)
				if res.ExitCode == 0 || !strings.Contains(res.Stderr, want) {
					t.Fatalf("%v: %+v", argv, res)
				}
			}
		})
	}
}

func TestReadFollowReportsExit(t *testing.T) {
	reg := relay.NewSessionRegistry()
	defer reg.CloseAll("test-done")
	res := ExecuteCommand([]string{"launch", "sleep 0.1; exit 7", "-s", "follow-exit", "--no-wait"}, reg, ".", nil)
	if res.ExitCode != 0 {
		t.Fatal(res.Stderr)
	}
	res = ExecuteCommand([]string{"read", "-s", "follow-exit", "--follow", "--timeout", "1000"}, reg, ".", nil)
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "[process exited with code 7]") {
		t.Fatalf("exit lost: %+v", res)
	}
}

func TestScrollCoordinatesPreserveExplicitZero(t *testing.T) {
	for _, tt := range []struct {
		name, direction string
		flags           []string
		want            string
		invalid         bool
	}{
		{"default center", "up", nil, "\x1b[<64;11;4M", false},
		{"left edge", "up", []string{"--x", "0"}, "\x1b[<64;1;4M", false},
		{"top edge", "down", []string{"--y", "0"}, "\x1b[<65;11;1M", false},
		{"top left", "down", []string{"--x", "0", "--y", "0"}, "\x1b[<65;1;1M", false},
		{"explicit positive", "up", []string{"--x", "3", "--y", "2"}, "\x1b[<64;4;3M", false},
		{"negative x", "up", []string{"--x", "-1"}, "x coordinate must be nonnegative", true},
		{"negative y", "down", []string{"--y", "-1"}, "y coordinate must be nonnegative", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg := relay.NewSessionRegistry()
			defer reg.CloseAll("test-done")
			res := ExecuteCommand([]string{"launch", "stty raw -echo; printf ready; cat", "-s", "mouse", "--cols", "20", "--rows", "6"}, reg, ".", nil)
			if res.ExitCode != 0 {
				t.Fatal(res.Stderr)
			}
			s := reg.Get("mouse")
			s.Read() // Consume readiness output before waiting for the echoed event.
			args := append([]string{"scroll", tt.direction, "-s", "mouse"}, tt.flags...)
			res = ExecuteCommand(args, reg, ".", nil)
			if tt.invalid {
				if res.ExitCode == 0 {
					s.WaitForUnreadOutput(time.Second)
					t.Fatalf("invalid coordinates accepted; PTY output=%q", s.GetRawOutput())
				}
				if !strings.Contains(res.Stderr, tt.want) {
					t.Fatalf("coordinate validation: %+v", res)
				}
				if s.WaitForUnreadOutput(20 * time.Millisecond) {
					t.Fatalf("invalid mouse event reached PTY: %q", s.GetRawOutput())
				}
				return
			}
			if res.ExitCode != 0 || res.Stdout != "OK" {
				t.Fatalf("scroll: %+v", res)
			}
			deadline := time.Now().Add(time.Second)
			for !strings.Contains(s.GetRawOutput(), tt.want) && time.Now().Before(deadline) {
				s.WaitForUnreadOutput(time.Until(deadline))
				s.Read()
			}
			if raw := s.GetRawOutput(); !strings.Contains(raw, tt.want) {
				t.Fatalf("mouse event=%q want=%q", raw, tt.want)
			}
		})
	}
}

func TestScreenshotConcurrentOutput(t *testing.T) {
	reg := relay.NewSessionRegistry()
	defer reg.CloseAll("test-done")
	cwd := t.TempDir()
	res := ExecuteCommand([]string{"launch", "while :; do printf '\\033[31mchanging\\033[0m\\r'; done", "-s", "changing", "--cols", "20", "--rows", "3"}, reg, cwd, nil)
	if res.ExitCode != 0 {
		t.Fatal(res.Stderr)
	}
	output := filepath.Join(cwd, "screen.png")
	for range 10 {
		res = ExecuteCommand([]string{"screenshot", "-s", "changing", "--immediate", "-o", output}, reg, cwd, nil)
		if res.ExitCode != 0 {
			t.Fatal(res.Stderr)
		}
	}
	if data, err := os.ReadFile(output); err != nil || len(data) == 0 {
		t.Fatalf("screenshot: %v", err)
	}
}

func TestCommandOutputVariants(t *testing.T) {
	reg := relay.NewSessionRegistry()
	defer reg.CloseAll("test-done")
	cwd := t.TempDir()
	run := func(args ...string) relay.CLIResult { return ExecuteCommand(args, reg, cwd, nil) }
	res := run("launch", "printf '\033[1;3;4mstyled\033[0m'; sleep 0.2", "-s", "styled")
	if res.ExitCode != 0 {
		t.Fatal(res.Stderr)
	}
	for _, flag := range []string{"--bold", "--italic", "--underline"} {
		res = run("snapshot", "-s", "styled", flag, "--immediate", "--no-cursor")
		if res.ExitCode != 0 || !strings.Contains(res.Stdout, "styled") {
			t.Fatalf("filter %s: %+v", flag, res)
		}
	}
	res = run("read", "-s", "styled", "--follow", "--trim")
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "styled") {
		t.Fatalf("follow unread: %+v", res)
	}
	if !reg.Get("styled").WaitForExit(time.Second) {
		t.Fatal("process did not exit")
	}
	res = run("snapshot", "-s", "styled", "--json", "--immediate", "--no-cursor")
	var snap struct {
		Text     string
		Dead     bool
		ExitCode int
	}
	if err := json.Unmarshal([]byte(res.Stdout), &snap); err != nil || !snap.Dead || snap.ExitCode != 0 || !strings.Contains(snap.Text, "styled") {
		t.Fatalf("dead snapshot: %+v %v", res, err)
	}
	res = run("read", "-s", "styled", "--follow", "--trim")
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "process exited with code 0") {
		t.Fatalf("dead follow: %+v", res)
	}
	res = run("launch", "printf relaunched", "-s", "styled")
	if res.ExitCode != 0 {
		t.Fatalf("dead relaunch: %+v", res)
	}
	res = run("--cwd", cwd, "--", "printf", "%s:%s", "a'b", "")
	if res.ExitCode != 0 {
		t.Fatalf("default argv session: %+v", res)
	}
	res = run("launch", "cat", "-s", "silent", "--timeout", "1")
	if res.ExitCode != 0 || !strings.Contains(res.Stderr, "produced no output") {
		t.Fatalf("silent launch: %+v", res)
	}
	res = run("read", "-s", "silent", "--follow", "--timeout", "1")
	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "No new output") {
		t.Fatalf("follow timeout: %+v", res)
	}
	for _, args := range [][]string{{"launch"}, {"--cwd", filepath.Join(cwd, "missing"), "--", "cat"}, {"unknown"}} {
		res = run(args...)
		if res.ExitCode == 0 {
			t.Fatalf("invalid launch accepted %v: %+v", args, res)
		}
	}
}

func TestCommandLaunchVariants(t *testing.T) {
	reg := relay.NewSessionRegistry()
	defer reg.CloseAll("test-done")
	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, "child"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-s", "live", "--cwd", "child", "--env", "VALUE=a,b", "--background", "--no-wait", "--", "sh", "-c", "printf ready; exec cat"},
		{"launch", "cat", "-s", "live", "--background"},
		{"launch", "cat", "-s", "live"},
	} {
		res := ExecuteCommand(args, reg, cwd, map[string]string{"BASE": "kept"})
		if res.ExitCode != 0 || !strings.Contains(res.Stdout, "live") {
			t.Fatalf("launch %v: %+v", args, res)
		}
	}
	s := reg.Get("live")
	if err := s.WaitForData(time.Second); err != nil {
		t.Fatal(err)
	}
	if s.Cwd() != filepath.Join(cwd, "child") || s.Env()["VALUE"] != "a,b" || s.Env()["BASE"] != "kept" {
		t.Fatalf("launch context lost: %s %v", s.Cwd(), s.Env())
	}
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"resize", "bad", "10"}, "invalid cols"},
		{[]string{"resize", "10", "bad"}, "invalid rows"},
		{[]string{"click-at", "bad", "0"}, "invalid x"},
		{[]string{"click-at", "0", "bad"}, "invalid y"},
		{[]string{"scroll", "sideways"}, "Invalid direction"},
		{[]string{"capture-frames", "bad-key"}, "Invalid key"},
		{[]string{"wait", "absent", "--timeout", "1"}, "timed out"},
		{[]string{"click", "absent", "--timeout", "1"}, "timed out"},
		{[]string{"screenshot", "--immediate", "-o", filepath.Join(cwd, "missing", "shot.png")}, "writing screenshot"},
	} {
		res := ExecuteCommand(append(tt.args, "-s", "live"), reg, cwd, nil)
		if res.ExitCode == 0 || !strings.Contains(strings.ToLower(res.Stderr), strings.ToLower(tt.want)) {
			t.Fatalf("%v: %+v", tt.args, res)
		}
	}
	for _, args := range [][]string{{"logfile"}, {"daemon-stop"}, {"attach"}, {"--help"}, {"snapshot", "--help"}} {
		res := ExecuteCommand(args, reg, cwd, nil)
		if res.ExitCode != 0 || res.Stdout == "" {
			t.Fatalf("metadata %v: %+v", args, res)
		}
	}
}

func TestAppSessionsListing(t *testing.T) {
	reg := relay.NewSessionRegistry()
	defer reg.CloseAll("test-done")

	// Empty list
	res := ExecuteCommand([]string{"sessions"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 {
		t.Fatalf("sessions failed: %s", res.Stderr)
	}
	if !strings.Contains(res.Stdout, "No active sessions") {
		t.Fatalf("expected 'No active sessions', got: %q", res.Stdout)
	}

	// JSON empty list
	res = ExecuteCommand([]string{"sessions", "--json"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 {
		t.Fatalf("sessions --json failed: %s", res.Stderr)
	}
	if res.Stdout != "[]" {
		t.Fatalf("expected '[]', got: %q", res.Stdout)
	}

	// Launch a session and list
	_ = ExecuteCommand([]string{"launch", "echo active", "-s", "sess-listed", "--no-wait"}, reg, t.TempDir(), nil)
	defer ExecuteCommand([]string{"close", "-s", "sess-listed"}, reg, t.TempDir(), nil)

	res = ExecuteCommand([]string{"sessions"}, reg, t.TempDir(), nil)
	if !strings.Contains(res.Stdout, "sess-listed") {
		t.Fatalf("expected sessions list to contain sess-listed, got: %q", res.Stdout)
	}

	res = ExecuteCommand([]string{"sessions", "--json"}, reg, t.TempDir(), nil)
	var list []map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &list); err != nil {
		t.Fatalf("sessions --json unmarshal error: %v", err)
	}
	if len(list) != 1 || list[0]["name"] != "sess-listed" {
		t.Fatalf("unexpected JSON sessions: %v", list)
	}
}

func TestAppInteractiveCommands(t *testing.T) {
	reg := relay.NewSessionRegistry()
	defer reg.CloseAll("test-done")

	_ = ExecuteCommand([]string{"launch", "cat", "-s", "cat-sess", "--no-wait"}, reg, t.TempDir(), nil)
	defer ExecuteCommand([]string{"close", "-s", "cat-sess"}, reg, t.TempDir(), nil)

	// Type
	res := ExecuteCommand([]string{"type", "echo-test", "-s", "cat-sess"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 || res.Stdout != "OK" {
		t.Fatalf("type failed: %s %s", res.Stdout, res.Stderr)
	}

	// Press
	res = ExecuteCommand([]string{"press", "enter", "-s", "cat-sess"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 || res.Stdout != "OK" {
		t.Fatalf("press failed: %s %s", res.Stdout, res.Stderr)
	}

	// Wait idle
	res = ExecuteCommand([]string{"wait-idle", "-s", "cat-sess", "--timeout", "300"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 || res.Stdout != "OK" {
		t.Fatalf("wait-idle failed: %s %s", res.Stdout, res.Stderr)
	}

	// Snapshot
	res = ExecuteCommand([]string{"snapshot", "-s", "cat-sess", "--trim"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "echo-test") {
		t.Fatalf("snapshot failed: %s %s", res.Stdout, res.Stderr)
	}

	// Snapshot JSON
	res = ExecuteCommand([]string{"snapshot", "-s", "cat-sess", "--json"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 {
		t.Fatalf("snapshot --json failed: %s", res.Stderr)
	}
	var snapJSON map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &snapJSON); err != nil {
		t.Fatalf("snapshot json unmarshal: %v", err)
	}
	if snapJSON["session"] != "cat-sess" {
		t.Fatalf("unexpected session in json: %v", snapJSON)
	}

	// Resize
	res = ExecuteCommand([]string{"resize", "100", "30", "-s", "cat-sess"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 || res.Stdout != "OK" {
		t.Fatalf("resize failed: %s", res.Stderr)
	}

	// Scroll
	res = ExecuteCommand([]string{"scroll", "up", "2", "-s", "cat-sess"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 || res.Stdout != "OK" {
		t.Fatalf("scroll up failed: %s", res.Stderr)
	}
	res = ExecuteCommand([]string{"scroll", "down", "2", "-s", "cat-sess"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 || res.Stdout != "OK" {
		t.Fatalf("scroll down failed: %s", res.Stderr)
	}

	// Click at
	res = ExecuteCommand([]string{"click-at", "5", "2", "-s", "cat-sess"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 || res.Stdout != "OK" {
		t.Fatalf("click-at failed: %s", res.Stderr)
	}

	// Click pattern
	res = ExecuteCommand([]string{"click", "echo-test", "-s", "cat-sess", "--first"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 || res.Stdout != "OK" {
		t.Fatalf("click pattern failed: %s", res.Stderr)
	}

	// Capture frames
	res = ExecuteCommand([]string{"capture-frames", "enter", "-s", "cat-sess", "--count", "3", "--interval", "10"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 {
		t.Fatalf("capture-frames failed: %s", res.Stderr)
	}
	var frames []string
	if err := json.Unmarshal([]byte(res.Stdout), &frames); err != nil {
		t.Fatalf("capture-frames unmarshal error: %v", err)
	}
	if len(frames) != 3 {
		t.Fatalf("expected 3 frames, got %d", len(frames))
	}

	// Screenshot
	res = ExecuteCommand([]string{"screenshot", "-s", "cat-sess", "--immediate"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 {
		t.Fatalf("screenshot failed: %s", res.Stderr)
	}

	// Logfile
	res = ExecuteCommand([]string{"logfile"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "relay-server.log") {
		t.Fatalf("logfile failed: %s", res.Stdout)
	}

	// Restart
	res = ExecuteCommand([]string{"restart", "-s", "cat-sess", "--no-wait"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, `Session "cat-sess" restarted`) {
		t.Fatalf("restart failed: %s", res.Stdout)
	}
}

func TestAppInvalidKey(t *testing.T) {
	reg := relay.NewSessionRegistry()
	defer reg.CloseAll("test-done")

	// Launch
	res := ExecuteCommand([]string{"launch", "cat", "-s", "key-test", "--no-wait"}, reg, t.TempDir(), nil)
	if res.ExitCode != 0 {
		t.Fatalf("launch failed: %s", res.Stderr)
	}
	defer ExecuteCommand([]string{"close", "-s", "key-test"}, reg, t.TempDir(), nil)

	// Press invalid key
	res = ExecuteCommand([]string{"press", "invalidkey", "-s", "key-test"}, reg, t.TempDir(), nil)
	if res.ExitCode != 1 {
		t.Fatalf("expected exit code 1 for invalid key, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "Invalid key(s): invalidkey") {
		t.Fatalf("expected Invalid key(s) in stderr, got: %q", res.Stderr)
	}
}
