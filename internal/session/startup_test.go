package session

import (
	"fmt"
	"os"
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
		if got != want+"\r\n" || s.ExitInfo().ExitCode != 0 {
			t.Fatalf("child %d output/exit: %q / %+v", i, got, s.ExitInfo())
		}
	}
}

func TestFailedStartReleasesPTYDescriptors(t *testing.T) {
	opts := []LaunchOptions{
		{Command: "/nonexistent-startup-command"},
		{Command: "echo", Cwd: "/nonexistent-startup-directory"},
	}
	for _, opt := range opts {
		if _, err := New(opt); err == nil {
			t.Fatal("invalid child started")
		}
	}
	before, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	for range 50 {
		for _, opt := range opts {
			if _, err := New(opt); err == nil {
				t.Fatal("invalid child started")
			}
		}
	}
	after, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) > len(before) {
		t.Fatalf("failed starts leaked descriptors: %d -> %d", len(before), len(after))
	}
}
