package main

import (
	"bytes"
	"errors"
	"log/slog"
	"net"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/remorses/tuistory/internal/app"
	"github.com/remorses/tuistory/internal/relay"
)

func executeCommand(args ...string) (string, error) {
	buf := new(bytes.Buffer)
	cmd := newRootCommand()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)

	err := cmd.Execute()
	return buf.String(), err
}

func TestClientForwarding(t *testing.T) {
	server := httptest.NewUnstartedServer(nil)
	port := server.Listener.Addr().(*net.TCPAddr).Port
	srv := relay.NewServer(version, port, ".")
	srv.SetCLIRunner(func(req relay.CLIRequest, reg *relay.SessionRegistry, logger *slog.Logger) relay.CLIResult {
		return app.ExecuteCommand(req.Argv[1:], reg, req.Cwd, req.Env)
	})
	server.Config.Handler = srv.Handler()
	server.Start()
	defer server.Close()
	for _, tt := range []struct {
		args []string
		want string
		code int
	}{
		{[]string{"sessions", "--json"}, "[]", 0},
		{[]string{"-s", "missing", "snapshot"}, "not found", 1},
	} {
		argv := append([]string{"tuistory"}, tt.args...)
		cmd := newClientCommand(port, argv)
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs(tt.args)
		err := cmd.Execute()
		var exitErr commandExitError
		if tt.code == 0 && err != nil {
			t.Fatal(err)
		}
		if tt.code != 0 && (!errors.As(err, &exitErr) || exitErr.code != tt.code) {
			t.Fatalf("exit: %v", err)
		}
		if !strings.Contains(stdout.String()+stderr.String(), tt.want) {
			t.Fatalf("response: %q %q", stdout.String(), stderr.String())
		}
	}
}

func TestClientOfflineAndPassthrough(t *testing.T) {
	t.Setenv("TUISTORY_SESSION", "outer")
	for _, tt := range []struct {
		args []string
		want string
		code int
	}{
		{[]string{"--", "printf", "%s", "one two"}, "one two", 0},
		{[]string{"launch", "exit 7"}, "", 7},
		{[]string{"--", "command-that-does-not-exist-tuistory"}, "", -1},
		{[]string{}, "tuistory", 0},
	} {
		cmd := newClientCommand(1, append([]string{"tuistory"}, tt.args...))
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(tt.args)
		err := cmd.Execute()
		if tt.code == 0 && (err != nil || !strings.Contains(out.String(), tt.want)) {
			t.Fatalf("%v: %q %v", tt.args, out.String(), err)
		}
		if tt.code == 7 {
			var exitErr commandExitError
			if !errors.As(err, &exitErr) || exitErr.code != 7 {
				t.Fatalf("exit: %v", err)
			}
			if exitErr.Error() != "command exited with code 7" {
				t.Fatal(exitErr.Error())
			}
		}
		if tt.code == -1 && err == nil {
			t.Fatal("missing executable accepted")
		}
	}
}

func TestPortAndEnvironment(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  int
	}{{"", 19977}, {"invalid", 19977}, {"-1", 19977}, {"12345", 12345}} {
		t.Setenv("TUISTORY_PORT", tt.value)
		if got := getPort(); got != tt.want {
			t.Fatalf("getPort(%q)=%d", tt.value, got)
		}
	}
	value := "a=b,c"
	m := envToMap([]string{"KEY=" + value, "invalid", "=ignored", "PORT=" + strconv.Itoa(os.Getpid())})
	if m["KEY"] != value || m["PORT"] == "" || len(m) != 2 {
		t.Fatal(m)
	}
}

type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, errors.New("output unavailable") }

func TestSkillOutputContract(t *testing.T) {
	for _, mode := range []string{"0", "1", "true", "yes"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT", mode)
			out, err := executeCommand("skill")
			if err != nil || out != skillContent {
				t.Fatalf("skill mismatch: %v", err)
			}
		})
	}
	cmd := newRootCommand()
	cmd.SetOut(failingWriter{})
	cmd.SetArgs([]string{"skill"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "printing skill") {
		t.Fatalf("write failure: %v", err)
	}
}

func TestRootCommand_Help(t *testing.T) {
	t.Setenv("AGENT", "0")
	out, err := executeCommand("--help")
	if err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if !strings.Contains(out, "launch [command]") {
		t.Errorf("expected launch command in help, got %q", out)
	}
	if !strings.Contains(out, "snapshot") {
		t.Errorf("expected snapshot in help, got %q", out)
	}
	if !strings.Contains(out, "read") {
		t.Errorf("expected read in help, got %q", out)
	}
	if !strings.Contains(out, "skill") {
		t.Errorf("expected skill in help, got %q", out)
	}
}

func TestRootCommand_Version(t *testing.T) {
	outVer, errVer := executeCommand("--version")
	if errVer != nil {
		t.Fatalf("version failed: %v", errVer)
	}
	if outVer != version+"\n" {
		t.Errorf("expected '%s\\n', got %q", version, outVer)
	}
}

func TestSkillCommand(t *testing.T) {
	out, err := executeCommand("skill")
	if err != nil {
		t.Fatalf("skill command failed: %v", err)
	}
	if !strings.Contains(out, "name: tuistory") {
		t.Errorf("expected frontmatter in skill, got %q", out)
	}
	if !strings.Contains(out, "Command Reference") {
		t.Errorf("expected Command Reference in skill, got %q", out)
	}
}

func TestAgentModeHelpAlert(t *testing.T) {
	t.Setenv("AGENT", "1")
	out, err := executeCommand("--help")
	if err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if !strings.Contains(out, "ALERT: Agents must read `AGENT=1 tuistory skill` before using this tool.") {
		t.Errorf("expected alert banner in agent mode, got %q", out)
	}
}
