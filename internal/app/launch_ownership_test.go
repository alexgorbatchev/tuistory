package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/remorses/tuistory/internal/process"
	"github.com/remorses/tuistory/internal/relay"
	"golang.org/x/sys/unix"
)

func TestConcurrentLaunchOwnsOneChild(t *testing.T) {
	reg := relay.NewSessionRegistry()
	t.Cleanup(func() { reg.CloseAll("test") })
	cwd := t.TempDir()
	pidsPath := filepath.Join(cwd, "pids")
	t.Cleanup(func() {
		data, err := os.ReadFile(pidsPath)
		if err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
		for _, raw := range strings.Fields(string(data)) {
			pid, err := strconv.Atoi(raw)
			if err != nil {
				t.Error(err)
				continue
			}
			process.KillSessionGroups(pid, unix.SIGKILL)
		}
	})
	const callers = 32
	results := make([]relay.CLIResult, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			<-start
			results[i] = ExecuteCommand([]string{"-s", "shared", "--", "sh", "-c", "printf '%s\\n' \"$$\" >> pids; printf ready; exec cat"}, reg, cwd, nil)
		})
	}
	close(start)
	wg.Wait()
	started := 0
	for _, res := range results {
		if res.ExitCode != 0 {
			t.Fatalf("launch: %+v", res)
		}
		if strings.Contains(res.Stdout, `Session "shared" started`) {
			started++
		}
	}
	data, err := os.ReadFile(pidsPath)
	if err != nil {
		t.Fatal(err)
	}
	if pids := strings.Fields(string(data)); started != 1 || len(pids) != 1 {
		t.Fatalf("concurrent launch started %d sessions and children %v; want exactly one", started, pids)
	}
	if len(reg.List()) != 1 {
		t.Fatalf("registry lost ownership: %+v", reg.List())
	}
}

func TestClosePreservesCallbackReplacement(t *testing.T) {
	reg := relay.NewSessionRegistry()
	t.Cleanup(func() { reg.CloseAll("test") })
	cwd := t.TempDir()
	for _, name := range []string{"closing", "replacement"} {
		res := ExecuteCommand([]string{"-s", name, "--", "sh", "-c", "printf ready; exec cat"}, reg, cwd, nil)
		if res.ExitCode != 0 {
			t.Fatal(res.Stderr)
		}
	}
	old := reg.Get("closing")
	replacement := reg.Get("replacement")
	old.OnClosing(func(string) { reg.Set("closing", replacement) })
	res := ExecuteCommand([]string{"close", "-s", "closing"}, reg, cwd, nil)
	if res.ExitCode != 0 || reg.Get("closing") != replacement {
		t.Fatalf("close deleted callback replacement: %+v", res)
	}
	if !old.WaitForExit(time.Second) {
		t.Fatal("original child retained after close")
	}
}
