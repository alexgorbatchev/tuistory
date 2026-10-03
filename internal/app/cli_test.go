package app

import (
	"encoding/json"
	"strings"
	"testing"

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
