package client

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/remorses/tuistory/internal/relay"
)

func TestFindListenerOwners(t *testing.T) {
	for _, network := range []string{"tcp4", "tcp6"} {
		t.Run(network, func(t *testing.T) {
			address := "127.0.0.1:0"
			if network == "tcp6" {
				address = "[::1]:0"
			}
			listener, err := net.Listen(network, address)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			pids, err := findPIDsListeningOnPort(listener.Addr().(*net.TCPAddr).Port)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(pids, os.Getpid()) {
				t.Fatalf("owners %v exclude current pid %d", pids, os.Getpid())
			}
		})
	}
}

func TestKillRelayDoesNotKillStalePid(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	defer func() {
		_ = cmd.Process.Kill() // Best effort: it may already have exited.
		<-done
	}()
	path := relay.PidFilePath(port)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprint(cmd.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	if free, err := KillRelay(port); err != nil || !free {
		t.Fatalf("KillRelay = %v, %v", free, err)
	}
	select {
	case <-done:
		t.Fatalf("unrelated process killed from stale pidfile: %v", waitErr)
	case <-time.After(50 * time.Millisecond):
	}
}
