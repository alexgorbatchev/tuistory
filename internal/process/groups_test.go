package process

import (
	"os"
	"slices"
	"testing"

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
