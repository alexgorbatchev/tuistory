package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/remorses/tuistory/internal/relay"
)

func TestScreenshotUsesCallerDirectory(t *testing.T) {
	reg := relay.NewSessionRegistry()
	t.Cleanup(func() { reg.CloseAll("test") })
	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, "images"), 0755); err != nil {
		t.Fatal(err)
	}
	res := ExecuteCommand([]string{"-s", "screen", "--", "sh", "-c", "printf screenshot; exec cat"}, reg, cwd, nil)
	if res.ExitCode != 0 {
		t.Fatal(res.Stderr)
	}
	for _, path := range []string{"images/relative.png", "images/../normalized.png", filepath.Join(cwd, "absolute.png")} {
		t.Run(path, func(t *testing.T) {
			res := ExecuteCommand([]string{"screenshot", "-s", "screen", "--immediate", "-o", path}, reg, cwd, nil)
			want := path
			if !filepath.IsAbs(want) {
				want = filepath.Join(cwd, want)
			}
			if res.ExitCode != 0 || res.Stdout != want {
				t.Fatalf("screenshot %+v; want resolved path %q", res, want)
			}
			data, err := os.ReadFile(want)
			if err != nil || !strings.HasPrefix(string(data), "\x89PNG\r\n\x1a\n") {
				t.Fatalf("PNG at caller path: %v", err)
			}
		})
	}
}
