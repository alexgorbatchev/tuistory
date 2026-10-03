package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/remorses/tuistory/internal/client"
	"github.com/remorses/tuistory/internal/process"
	"github.com/remorses/tuistory/internal/relay"
	"golang.org/x/sys/unix"
)

func reviewLaunch(t *testing.T, name string, argv ...string) {
	t.Helper()
	args := append([]string{"-s", name, "--no-wait", "--"}, argv...)
	if out, errOut, code := runCLI(args...); code != 0 {
		t.Fatalf("launch: %q %q code=%d", out, errOut, code)
	}
	t.Cleanup(func() {
		reviewClose(t, name)
	})
}

func reviewClose(t *testing.T, name string) {
	t.Helper()
	if out, errOut, code := runCLI("-s", name, "close"); code != 0 {
		t.Errorf("close: %q %q code=%d", out, errOut, code)
	}
}

func TestE2E_ReviewCallerPATH(t *testing.T) {
	if out, errOut, code := runCLI("sessions"); code != 0 {
		t.Fatalf("start daemon: %q %q code=%d", out, errOut, code)
	}
	dir := t.TempDir()
	name := "tuistory-caller-path-fixture"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nprintf 'caller-path:%s\\n' \"$1\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := runCLIWithEnv([]string{"PATH=" + dir}, dir, "-s", "review-path", "--", name, "one two;$HOME")
	if code != 0 {
		t.Fatalf("caller PATH launch: %q %q code=%d", out, errOut, code)
	}
	t.Cleanup(func() { reviewClose(t, "review-path") })
	out, errOut, code = runCLI("-s", "review-path", "read", "--all")
	if code != 0 || !strings.Contains(out, "caller-path:one two;$HOME") {
		t.Fatalf("caller PATH output: %q %q code=%d", out, errOut, code)
	}
}

func TestE2E_ReviewRegexFlags(t *testing.T) {
	reviewLaunch(t, "review-regex", "sh", "-c", "printf 'TOP\\nbottom\\n'; exec cat")
	for _, pattern := range []string{"/^bottom$/m", "/top.*bottom/is"} {
		out, errOut, code := runCLI("-s", "review-regex", "wait", pattern, "--timeout", "500")
		if code != 0 || !strings.Contains(out, "bottom") {
			t.Errorf("wait %q: %q %q code=%d", pattern, out, errOut, code)
		}
	}
	for _, pattern := range []string{"/bottom/z", "/[broken/", "/bottom/y"} {
		out, errOut, code := runCLI("-s", "review-regex", "wait", pattern, "--timeout", "500")
		if code == 0 || !strings.Contains(errOut, "regex") || strings.Contains(errOut, "timed out") {
			t.Errorf("invalid regex %q: %q %q code=%d", pattern, out, errOut, code)
		}
	}
}

func TestE2E_ReviewInvalidInput(t *testing.T) {
	reviewLaunch(t, "review-invalid-input", "cat")
	for _, args := range [][]string{
		{"capture-frames", "z", "--count", "-1"},
		{"capture-frames", "z", "--interval", "-1"},
		{"capture-frames", "z", "--interval", "18446744073710"},
		{"resize", "--", "-1", "1"},
		{"resize", "65536", "1"},
	} {
		out, errOut, code := runCLI(append([]string{"-s", "review-invalid-input"}, args...)...)
		if code == 0 || errOut == "" || strings.Contains(errOut, "EOF") {
			t.Errorf("invalid input %v: %q %q code=%d", args, out, errOut, code)
		}
	}
	out, errOut, code := runCLI("-s", "review-invalid-input", "snapshot", "--immediate", "--trim", "--no-cursor")
	if code != 0 || out != "" {
		t.Errorf("rejected input changed screen: %q %q code=%d", out, errOut, code)
	}
	if out, errOut, code := runCLI("-s", "review-invalid-input", "resize", "40", "8"); code != 0 {
		t.Fatalf("valid resize: %q %q code=%d", out, errOut, code)
	}
}

func TestE2E_ReviewRelativeScreenshot(t *testing.T) {
	reviewLaunch(t, "review-screenshot", "sh", "-c", "printf ready; exec cat")
	dir := t.TempDir()
	filename := filepath.Join(filepath.Base(dir), "screen.png")
	if err := os.Mkdir(filepath.Join(dir, filepath.Base(dir)), 0755); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := runCLIWithEnv(nil, dir, "-s", "review-screenshot", "screenshot", "-o", filename, "--immediate")
	want := filepath.Join(dir, filename)
	if code != 0 || !strings.Contains(out, want) {
		t.Fatalf("relative screenshot: %q %q code=%d, want %s", out, errOut, code, want)
	}
	data, err := os.ReadFile(want)
	if err != nil || len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("caller screenshot not PNG: bytes=%d err=%v", len(data), err)
	}
}

func TestE2E_ReviewPerRequestAgentOutput(t *testing.T) {
	reviewLaunch(t, "review-agent", "cat")
	for _, env := range [][]string{{"AGENT=1", "AI_AGENT="}, {"AGENT=0", "AI_AGENT=review"}} {
		out, errOut, code := runCLIWithEnv(env, "", "sessions")
		if code != 0 || strings.Contains(out, "\x1b") {
			t.Fatalf("agent sessions: %q %q code=%d", out, errOut, code)
		}
		found := false
		for _, line := range strings.Split(out, "\n") {
			var item struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal([]byte(line), &item); err != nil {
				t.Fatalf("agent session line: %q: %v", line, err)
			}
			found = found || item.Name == "review-agent"
		}
		if !found {
			t.Fatalf("agent session missing: %q", out)
		}
	}
	out, errOut, code := runCLIWithEnv([]string{"AGENT=0", "AI_AGENT="}, "", "sessions")
	if code != 0 || !strings.Contains(out, "review-agent") || !strings.Contains(out, "\x1b[") {
		t.Fatalf("human sessions after agent: %q %q code=%d", out, errOut, code)
	}
}

func TestE2E_ReviewTerminalReplies(t *testing.T) {
	reviewLaunch(t, "review-terminal-reply", "bash", "-c", "stty raw -echo; printf '\\033[6n'; IFS= read -r -d R -t 2 reply; printf '\\r\\nREPLY:%q\\r\\n' \"$reply\"; exec cat")
	out, errOut, code := runCLI("-s", "review-terminal-reply", "wait", "REPLY:", "--timeout", "3000")
	if code != 0 || !strings.Contains(out, "1;1") {
		t.Fatalf("terminal query reply: %q %q code=%d", out, errOut, code)
	}
}

func TestE2E_ReviewDaemonShutdown(t *testing.T) {
	out, errOut, code := runCLI("-s", "review-shutdown", "--", "bash", "-c", "trap '' HUP TERM; printf 'PID:%s\\n' \"$$\"; while :; do sleep 0.1; done")
	if code != 0 {
		t.Fatalf("shutdown launch: %q %q code=%d", out, errOut, code)
	}
	out, errOut, code = runCLI("-s", "review-shutdown", "read", "--all")
	_, number, found := strings.Cut(out, "PID:")
	pid, err := strconv.Atoi(strings.TrimSpace(number))
	if code != 0 || !found || err != nil || pid <= 0 {
		t.Fatalf("shutdown child PID: %q %q code=%d err=%v", out, errOut, code, err)
	}
	t.Cleanup(func() { process.KillSessionGroups(pid, unix.SIGKILL) })
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	type stopResult struct {
		out, err string
		code     int
	}
	stopped := make(chan stopResult, 1)
	go func() {
		out, errOut, code := runCLI("daemon-stop")
		stopped <- stopResult{out: out, err: errOut, code: code}
	}()
	// The stubborn child holds shutdown in its grace period. Observe the
	// drained registry over HTTP before trying to start a new native child.
	gateDeadline := time.Now().Add(time.Second)
	for {
		res, err := client.ForwardCLI(testPort, relay.CLIRequest{Argv: []string{"tuistory", "sessions", "--json"}, Cwd: cwd})
		if err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) == "[]" {
			res, err := client.ForwardCLI(testPort, relay.CLIRequest{
				Argv: []string{"tuistory", "-s", "review-late-launch", "--no-wait", "--", "printf", "late-launch"}, Cwd: cwd,
			})
			if err != nil || res.ExitCode == 0 || !strings.Contains(res.Stderr, "shutting down") {
				t.Errorf("launch during shutdown: %+v err=%v", res, err)
			}
			break
		}
		if time.Now().After(gateDeadline) {
			t.Errorf("shutdown gate unavailable: %+v err=%v", res, err)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case res := <-stopped:
		if res.code != 0 {
			t.Errorf("daemon-stop: %q %q code=%d", res.out, res.err, res.code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon-stop did not finish")
	}
	deadline := time.Now().Add(time.Second)
	for {
		groups, err := process.Groups(pid)
		if err != nil {
			t.Fatal(err)
		}
		if len(groups) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon-stop left session %d groups alive: %v", pid, groups)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
