package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/remorses/tuistory/internal/relay"
)

func TestLaunchDiagnosticsUseActualDimensions(t *testing.T) {
	for _, tt := range []struct {
		name string
		env  map[string]string
	}{
		{"human", nil},
		{"agent", map[string]string{"AGENT": "1"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg := relay.NewSessionRegistry()
			t.Cleanup(func() { reg.CloseAll("test") })
			res := ExecuteCommand([]string{"-s", "default-dimensions", "--cols", "0", "--rows", "0", "--background", "--", "sh", "-c", "stty size; exec cat"}, reg, t.TempDir(), tt.env)
			if res.ExitCode != 0 {
				t.Fatal(res.Stderr)
			}
			s := reg.Get("default-dimensions")
			if s == nil || s.Cols() <= 0 || s.Rows() <= 0 {
				t.Fatal("zero flags did not create a session with actual dimensions")
			}
			if _, err := s.WaitForText(fmt.Sprintf("%d %d", s.Rows(), s.Cols()), time.Second); err != nil {
				t.Fatalf("native child terminal differs from registry dimensions: %v", err)
			}
			want := fmt.Sprintf("cols:    %d\n  rows:    %d", s.Cols(), s.Rows())
			if tt.env != nil {
				want = fmt.Sprintf("cols:%d rows:%d", s.Cols(), s.Rows())
			}
			if !strings.Contains(res.Stdout, want) {
				t.Fatalf("diagnostics %q differ from actual session dimensions %q", res.Stdout, want)
			}
		})
	}
}
