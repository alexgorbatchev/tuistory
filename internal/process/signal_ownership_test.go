package process

import (
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestKillSessionGroupsRejectsNegativeID(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()       // Exit status isn't relevant to ownership.
	defer func() { _ = cmd.Process.Kill(); <-done }() // Owned fixture cleanup.
	KillSessionGroups(-cmd.Process.Pid, unix.SIGTERM)
	select {
	case <-done:
		t.Fatal("invalid session ID signalled an unrelated process")
	case <-time.After(50 * time.Millisecond):
	}
}
