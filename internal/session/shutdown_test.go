package session

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestCloseAndWaitEscalatesBeforeCompletion(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "trap '' HUP TERM; printf ready; while :; do sleep 1; done"}})
	if _, err := s.WaitForText("ready", time.Second); err != nil {
		t.Fatal(err)
	}
	var notifications atomic.Int32
	s.OnClosing(func(string) { notifications.Add(1) })
	ctx, cancel := context.WithTimeout(context.Background(), killGraceDuration+3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := s.CloseAndWait(ctx, "test"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if !s.IsDead() {
		t.Fatal("shutdown completion preceded process exit")
	}
	if err := s.cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("reaped root is still alive after shutdown completion")
	}
	info := s.ExitInfo()
	if info == nil || info.Signal != int(syscall.SIGKILL) {
		t.Fatalf("shutdown did not escalate to SIGKILL: %+v", info)
	}
	if notifications.Load() != 1 {
		t.Fatalf("closing notifications = %d, want 1", notifications.Load())
	}
	for name, done := range map[string]<-chan struct{}{
		"reader": s.readDone, "terminal writer": s.terminalReplyDone, "shutdown": s.closeDone,
	} {
		select {
		case <-done:
		default:
			t.Errorf("%s remains active after shutdown completion", name)
		}
	}
}

func TestCloseAndWaitCancellationLeavesShutdownOwned(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "trap '' HUP TERM; printf ready; while :; do sleep 1; done"}})
	if _, err := s.WaitForText("ready", time.Second); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.CloseAndWait(ctx, "first"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled join = %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), killGraceDuration+3*time.Second)
	defer cancel()
	if err := s.CloseAndWait(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	s.mu.RLock()
	reason := s.closeReason
	s.mu.RUnlock()
	if reason != "first" {
		t.Fatalf("first close reason changed: %q", reason)
	}
}

func TestCloseAndWaitAfterNormalExit(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "echo", Args: []string{"done"}})
	if !s.WaitForExit(time.Second) {
		t.Fatal("child did not exit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.CloseAndWait(ctx, "test"); err != nil {
		t.Fatalf("already reaped child waited for kill grace: %v", err)
	}
}
