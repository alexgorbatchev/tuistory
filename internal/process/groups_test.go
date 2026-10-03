package process

import (
	"os"
	"os/exec"
	"slices"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestGroupsCurrentProcess(t *testing.T) {
	pid := os.Getpid()
	sid, err := unix.Getsid(pid)
	if err != nil {
		t.Fatalf("Getsid error: %v", err)
	}

	pgid, err := unix.Getpgid(pid)
	if err != nil {
		t.Fatalf("Getpgid error: %v", err)
	}

	groups, err := Groups(sid)
	if err != nil {
		t.Fatalf("Groups error: %v", err)
	}

	if !slices.Contains(groups, pgid) {
		t.Fatalf("expected groups to contain %d, got: %v", pgid, groups)
	}
}

func TestKillSessionGroupsTerminatesRealProcess(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() }) // Best-effort safety if the assertion fails.
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("live probe failed: %v", err)
	}
	groups, err := Groups(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(groups, cmd.Process.Pid) {
		t.Fatalf("session group missing: %v", groups)
	}
	KillSessionGroups(cmd.Process.Pid, unix.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("sleep exited successfully after signal")
		}
	case <-time.After(time.Second):
		t.Fatal("process remained alive after group signal")
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("terminated process still responds to native signal probe")
	}
}

func TestGroupsInvalidAndMissingSessions(t *testing.T) {
	if _, err := Groups(0); err == nil {
		t.Fatal("invalid session accepted")
	}
	groups, err := Groups(1 << 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Fatalf("unexpected groups for absent session: %v", groups)
	}
	KillSessionGroups(1<<30, unix.SIGTERM)
}
