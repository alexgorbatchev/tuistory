package session

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The helper owns reparented descendants and reaps them with wait4. Running it in
// a separate process keeps Linux's subreaper setting out of the main test suite.
func TestSessionClosesDescendantsAfterParentExit(t *testing.T) {
	if os.Getenv("TUISTORY_ORPHAN_PROBE") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSessionClosesDescendantsAfterParentExit$")
		cmd.Env = append(os.Environ(), "TUISTORY_ORPHAN_PROBE=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("owned orphan probe: %v\n%s", err, output)
		}
		return
	}
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"kill", "close"} {
		t.Run(action, func(t *testing.T) {
			s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "trap '' HUP; sleep 60 & printf 'PID=%d\\n' $!; exit 7"}})
			if err := s.WaitForData(time.Second); err != nil {
				t.Fatal(err)
			}
			fields := strings.Fields(s.ReadAll())
			if len(fields) == 0 {
				t.Fatal("missing child PID")
			}
			pid, err := strconv.Atoi(strings.TrimPrefix(fields[0], "PID="))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = unix.Kill(pid, unix.SIGKILL) // Reap even when the behavioral assertion fails.
				var status unix.WaitStatus
				_, _ = unix.Wait4(pid, &status, 0, nil)
			}()
			if !s.WaitForExit(time.Second) {
				t.Fatal("parent did not exit")
			}
			if err := unix.Kill(pid, 0); err != nil {
				t.Fatalf("descendant not alive before action: %v", err)
			}
			if action == "kill" {
				s.KillProcess()
			} else {
				s.Close("test")
			}
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				var status unix.WaitStatus
				waited, err := unix.Wait4(pid, &status, unix.WNOHANG, nil)
				if err != nil {
					t.Fatal(err)
				}
				if waited == pid {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("descendant survived after root process exited")
		})
	}
}
