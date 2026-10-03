package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestDirectChildOutputSurvivesImmediateExit(t *testing.T) {
	for i := range 200 {
		want := fmt.Sprintf("immediate-%d", i)
		s, err := New(LaunchOptions{Command: "echo", Args: []string{want}})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WaitForData(time.Second); err != nil {
			s.Close("test")
			t.Fatalf("child %d output lost: %v", i, err)
		}
		text, err := s.WaitForText(want, time.Second)
		if err != nil || !strings.Contains(text, want) {
			s.Close("test")
			t.Fatalf("child %d output %q: %v", i, text, err)
		}
		if !s.WaitForExit(time.Second) {
			s.Close("test")
			t.Fatalf("child %d did not exit", i)
		}
		select {
		case <-s.readDone:
		case <-time.After(time.Second):
			s.Close("test")
			t.Fatal("PTY reader did not finish")
		}
		got := s.ReadAll()
		s.Close("test")
		if got != want+"\n" || s.ExitInfo().ExitCode != 0 {
			t.Fatalf("child %d output/exit: %q / %+v", i, got, s.ExitInfo())
		}
	}
}

func TestFailedStartReleasesPTYDescriptors(t *testing.T) {
	const helperEnv = "TUISTORY_STARTUP_DESCRIPTOR_HELPER"
	if os.Getenv(helperEnv) != "1" {
		// Other tests close sessions asynchronously, which opens process-table
		// descriptors. Measure only this subprocess's owned startup attempts.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFailedStartReleasesPTYDescriptors$")
		cmd.Env = append(os.Environ(), helperEnv+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated descriptor probe: %v (context: %v)\n%s", err, ctx.Err(), output)
		}
		return
	}
	opts := []LaunchOptions{
		{Command: "/nonexistent-startup-command"},
		{Command: "echo", Cwd: "/nonexistent-startup-directory"},
	}
	for _, opt := range opts {
		assertFailedStart(t, opt)
	}
	before := descriptorNames(t)
	probe, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	withProbe := descriptorNames(t)
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	if len(withProbe) != len(before)+1 {
		t.Fatalf("descriptor enumeration missed open file: %d -> %d", len(before), len(withProbe))
	}
	for range 50 {
		for _, opt := range opts {
			assertFailedStart(t, opt)
		}
	}
	after := descriptorNames(t)
	if len(after) > len(before) {
		t.Fatalf("failed starts leaked descriptors: %d -> %d", len(before), len(after))
	}
}

func assertFailedStart(t *testing.T, opt LaunchOptions) {
	t.Helper()
	s, err := New(opt)
	if err != nil {
		return
	}
	if s == nil {
		t.Fatalf("invalid child started: options=%+v; nil session without error", opt)
	}
	pid := s.cmd.Process.Pid
	exited := s.WaitForExit(time.Second)
	info := s.ExitInfo()
	output := s.ReadAll()
	s.Close("unexpected-start")
	if !exited {
		s.WaitForExit(time.Second)
	}
	t.Fatalf("invalid child started: options=%+v; path=%q; pid=%d; exited=%v; exit=%+v; output=%q", opt, s.cmd.Path, pid, exited, info, output)
}

func descriptorNames(t *testing.T) []string {
	t.Helper()
	dir, err := os.Open("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	// Darwin descriptor entries cannot be statted as ordinary directory entries.
	names, err := dir.Readdirnames(-1)
	closeErr := dir.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return names
}
