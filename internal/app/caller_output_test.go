package app

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/remorses/tuistory/internal/relay"
)

func TestCallerControlsSessionDiagnostics(t *testing.T) {
	t.Setenv("AGENT", "1")
	t.Setenv("AI_AGENT", "daemon-agent")
	reg := relay.NewSessionRegistry()
	t.Cleanup(func() { reg.CloseAll("test") })
	cwd := t.TempDir()
	res := ExecuteCommand([]string{"-s", "diagnostics", "--", "sh", "-c", "printf ready; exec cat"}, reg, cwd, nil)
	if res.ExitCode != 0 {
		t.Fatal(res.Stderr)
	}
	for _, tt := range []struct {
		name  string
		env   map[string]string
		agent bool
	}{
		{"human", nil, false},
		{"disabled", map[string]string{"AGENT": "0"}, false},
		{"one", map[string]string{"AGENT": "1"}, true},
		{"truthy", map[string]string{"AGENT": " TRUE "}, true},
		{"named", map[string]string{"AI_AGENT": "codex"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := ExecuteCommand([]string{"sessions"}, reg, cwd, tt.env)
			if res.ExitCode != 0 {
				t.Fatal(res.Stderr)
			}
			if !tt.agent {
				if !strings.Contains(res.Stdout, "\x1b[") || !strings.Contains(res.Stdout, "    ") {
					t.Fatalf("human formatting lost: %q", res.Stdout)
				}
				return
			}
			var info relay.SessionInfo
			if err := json.Unmarshal([]byte(res.Stdout), &info); err != nil || info.Name != "diagnostics" || info.Cwd != cwd {
				t.Fatalf("compact machine session: %q/%v", res.Stdout, err)
			}
			if strings.Contains(res.Stdout, "\x1b[") || strings.Contains(res.Stdout, "\n") {
				t.Fatalf("agent formatting contains decoration: %q", res.Stdout)
			}
			for _, args := range [][]string{{"-s", "diagnostics", "--background", "--", "cat"}, {"-s", "new-" + tt.name, "--background", "--no-wait", "--", "cat"}} {
				res := ExecuteCommand(args, reg, cwd, tt.env)
				if res.ExitCode != 0 || strings.Contains(res.Stdout, "\n") || strings.Contains(res.Stdout, "  ") || strings.Contains(res.Stdout, "\x1b[") {
					t.Fatalf("agent launch diagnostic: %+v", res)
				}
			}
			res = ExecuteCommand([]string{"close", "-s", "new-" + tt.name}, reg, cwd, tt.env)
			if res.ExitCode != 0 {
				t.Fatal(res.Stderr)
			}
		})
	}
}

func TestHelpUsesEachCallerEnvironment(t *testing.T) {
	t.Setenv("AGENT", "0")
	t.Setenv("AI_AGENT", "")
	reg := relay.NewSessionRegistry()
	const callers = 24
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			agentMode := i%2 == 0
			env := map[string]string{"AGENT": "0"}
			if agentMode {
				env["AI_AGENT"] = "codex"
			}
			for _, args := range [][]string{{"--help"}, {"help", "sessions"}, {"sessions", "--help"}} {
				res := ExecuteCommand(args, reg, ".", env)
				alert := strings.HasPrefix(res.Stdout, "ALERT: Agents must read `AGENT=1 tuistory skill` before using this tool.\n")
				if res.ExitCode != 0 || alert != agentMode {
					t.Errorf("caller mode %v help %v: %+v", agentMode, args, res)
				}
			}
		})
	}
	wg.Wait()
}

func TestAgentSilentLaunchDiagnosticIsCompact(t *testing.T) {
	reg := relay.NewSessionRegistry()
	t.Cleanup(func() { reg.CloseAll("test") })
	res := ExecuteCommand([]string{"-s", "silent-agent", "--timeout", "1", "--", "cat"}, reg, t.TempDir(), map[string]string{"AGENT": "yes"})
	if res.ExitCode != 0 || res.Stderr == "" || strings.Contains(res.Stderr, "\n") || strings.Contains(res.Stderr, "  ") {
		t.Fatalf("silent agent launch diagnostic: %+v", res)
	}
}
