package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/remorses/tuistory/internal/relay"
	"github.com/remorses/tuistory/internal/session"
)

func TestPatternCommandsRejectInvalidSyntax(t *testing.T) {
	for _, tt := range []struct {
		command, pattern, want string
	}{
		{"wait", "/[broken/", "invalid regex"},
		{"click", "/ready/y", "unsupported regex flag"},
	} {
		t.Run(tt.command, func(t *testing.T) {
			reg, s := inputCommandSession(t)
			res := ExecuteCommand([]string{tt.command, tt.pattern, "-s", "input", "--timeout", "50"}, reg, ".", nil)
			if res.ExitCode == 0 || !strings.Contains(res.Stderr, tt.want) || res.Stdout != "" {
				t.Errorf("invalid pattern command: %+v; want %q", res, tt.want)
			}
			if s.WaitForUnreadOutput(50*time.Millisecond) || s.ReadAll() != "ready" {
				t.Errorf("invalid pattern sent input: %q", s.ReadAll())
			}
		})
	}
}

func TestRegexWaitReturnsMatchingContext(t *testing.T) {
	reg := relay.NewSessionRegistry()
	s, err := session.New(session.LaunchOptions{Command: "sh", Args: []string{"-c", "i=0; while [ $i -lt 35 ]; do printf 'line%02d\\n' $i; i=$((i + 1)); done; sleep 10"}, Cols: 40, Rows: 40, IdleDelay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	reg.Set("context", s)
	t.Cleanup(func() { reg.CloseAll("test") })
	if _, err := s.WaitForText("line34", time.Second); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		pattern    string
		start, end int
	}{
		{"line15", 5, 25},
		{"/^LINE15$/im", 5, 25},
		{"/LINE14.*LINE15/sig", 4, 24},
	} {
		var want strings.Builder
		for i := tt.start; i <= tt.end; i++ {
			fmt.Fprintf(&want, "line%02d\n", i)
		}
		res := ExecuteCommand([]string{"wait", tt.pattern, "-s", "context", "--timeout", "1000"}, reg, ".", nil)
		expected := strings.TrimSuffix(want.String(), "\n")
		if res.ExitCode != 0 || res.Stdout != expected {
			t.Errorf("wait %q: %+v; want %q", tt.pattern, res, expected)
		}
	}
}
