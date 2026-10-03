package session

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A separate subreaper owns the orphan fixture without changing how the main
// test process adopts unrelated descendants.
func TestCloseAndWaitEscalatesAfterRootExit(t *testing.T) {
	const env = "TUISTORY_SHUTDOWN_ORPHAN_HELPER"
	if os.Getenv(env) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCloseAndWaitEscalatesAfterRootExit$")
		cmd.Env = append(os.Environ(), env+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("orphan shutdown helper: %v\n%s", err, output)
		}
		return
	}
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "trap '' HUP TERM; sleep 60 & printf 'PID=%d\\n' $!; exit 7"}})
	if !s.WaitForExit(time.Second) {
		t.Fatal("root did not exit")
	}
	if _, err := s.WaitForText("PID=", time.Second); err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(s.ReadAll())
	pid, err := strconv.Atoi(strings.TrimPrefix(fields[0], "PID="))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = unix.Kill(pid, unix.SIGKILL) // Reap the owned fixture on assertion failure.
		var status unix.WaitStatus
		_, _ = unix.Wait4(pid, &status, 0, nil)
	}()
	if err := unix.Kill(pid, 0); err != nil {
		t.Fatalf("descendant was not alive before shutdown: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), killGraceDuration+3*time.Second)
	defer cancel()
	if err := s.CloseAndWait(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var status unix.WaitStatus
		waited, err := unix.Wait4(pid, &status, unix.WNOHANG, nil)
		if err != nil {
			t.Fatal(err)
		}
		if waited == pid {
			if !status.Signaled() || status.Signal() != unix.SIGKILL {
				t.Fatalf("descendant exit = %v, want SIGKILL", status)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("descendant survived joined shutdown after root exit")
}
