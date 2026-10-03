package relay

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/remorses/tuistory/internal/session"
)

func TestShutdownJoinsPendingRestart(t *testing.T) {
	r := NewSessionRegistry()
	opts := session.LaunchOptions{Command: "sh", Args: []string{"-c", "printf ready; exec cat"}}
	s, created, err := r.Launch("pending", opts)
	if err != nil || !created {
		t.Fatalf("launch: %v/%v", created, err)
	}
	t.Cleanup(func() { s.Close("test") })
	if err := s.WaitForData(time.Second); err != nil {
		t.Fatal(err)
	}
	preparing := make(chan struct{})
	resume := make(chan struct{})
	restarted := make(chan error, 1)
	go func() {
		_, err := r.Restart("pending", func(*session.Session) session.LaunchOptions {
			close(preparing)
			<-resume
			return opts
		})
		restarted <- err
	}()
	<-preparing
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := r.Shutdown(ctx, "test-shutdown"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown did not wait for pending restart: %v", err)
	}
	if _, _, err := r.Launch("after-shutdown", opts); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("shutdown accepted new owner: %v", err)
	}
	close(resume)
	if err := <-restarted; !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("restart installed child after shutdown: %v", err)
	}
	if err := r.Shutdown(context.Background(), "second-caller"); err != nil {
		t.Fatal(err)
	}
	if len(r.List()) != 0 || !s.IsDead() {
		t.Fatalf("shutdown lost live child ownership: %+v", r.List())
	}
	if err := r.Close("pending", "test"); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("close accepted stopped registry: %v", err)
	}
}

func TestSessionOperationsDoNotHoldGlobalLock(t *testing.T) {
	r := NewSessionRegistry()
	t.Cleanup(func() { r.CloseAll("test") })
	opts := session.LaunchOptions{Command: "sh", Args: []string{"-c", "printf ready; exec cat"}}
	s, _, err := r.Launch("blocked", opts)
	if err != nil {
		t.Fatal(err)
	}
	callback := make(chan struct{})
	resume := make(chan struct{})
	s.OnClosing(func(string) {
		if r.Get("blocked") != nil {
			t.Error("closing child remains registered")
		}
		close(callback)
		<-resume
	})
	closed := make(chan error, 1)
	go func() { closed <- r.Close("blocked", "test") }()
	<-callback
	launched := make(chan error, 1)
	go func() {
		_, _, err := r.Launch("unrelated", opts)
		launched <- err
	}()
	select {
	case err := <-launched:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("unrelated launch blocked by callback")
	}
	close(resume)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}

func TestRegistryRecoversFromStartFailure(t *testing.T) {
	r := NewSessionRegistry()
	t.Cleanup(func() { r.CloseAll("test") })
	if _, _, err := r.Launch("retry", session.LaunchOptions{Command: "/nonexistent-registry-child"}); err == nil {
		t.Fatal("missing executable accepted")
	}
	s, created, err := r.Launch("retry", session.LaunchOptions{Command: "cat"})
	if err != nil || !created || r.Get("retry") != s {
		t.Fatalf("failed launch retained name reservation: %v/%v", created, err)
	}
	if _, err := r.Restart("missing", func(*session.Session) session.LaunchOptions { return session.LaunchOptions{} }); err == nil {
		t.Fatal("missing restart accepted")
	}
	if err := r.Close("missing", "test"); err == nil {
		t.Fatal("missing close accepted")
	}
}

func TestDeadRelaunchPreservesClosingReplacement(t *testing.T) {
	r := NewSessionRegistry()
	t.Cleanup(func() { r.CloseAll("test") })
	dead := relaySession(t, "printf old; exit 0")
	if !dead.WaitForExit(time.Second) {
		t.Fatal("old child did not exit")
	}
	replacement := relaySession(t, "printf new; exec cat")
	r.Set("shared", dead)
	dead.OnClosing(func(string) { r.Set("shared", replacement) })
	if _, _, err := r.Launch("shared", session.LaunchOptions{Command: "cat"}); err == nil {
		t.Fatal("relaunch overwrote callback replacement")
	}
	if r.Get("shared") != replacement || replacement.IsDead() {
		t.Fatal("replacement was lost or closed")
	}
}

func TestShutdownClosesChildrenConcurrently(t *testing.T) {
	r := NewSessionRegistry()
	const children = 4
	for range children {
		s := relaySession(t, "trap '' HUP TERM; printf ready; exec sleep 60")
		r.Set(s.StartedAt().String(), s)
	}
	var wg sync.WaitGroup
	errs := make(chan error, children)
	started := time.Now()
	for range children {
		wg.Go(func() { errs <- r.Shutdown(context.Background(), "test") })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("shutdown serialized four grace periods: %s", elapsed)
	}
	if len(r.List()) != 0 {
		t.Fatal("shutdown retained children")
	}
}

func TestShutdownJoinsRejectedCallbackChild(t *testing.T) {
	r := NewSessionRegistry()
	current := relaySession(t, "printf ready; exec cat")
	r.Set("closing", current)
	var rejected *session.Session
	current.OnClosing(func(string) {
		var err error
		rejected, err = session.New(session.LaunchOptions{Command: "sh", Args: []string{"-c", "trap '' HUP TERM; printf stubborn; exec sleep 60"}})
		if err != nil {
			t.Error(err)
			return
		}
		if err := rejected.WaitForData(time.Second); err != nil {
			t.Error(err)
		}
		r.Set("replacement", rejected)
	})
	if err := r.Shutdown(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	if rejected == nil {
		t.Fatal("callback did not create replacement")
	}
	t.Cleanup(func() {
		if err := rejected.CloseAndWait(context.Background(), "test"); err != nil {
			t.Error(err)
		}
	})
	if !rejected.IsDead() || len(r.List()) != 0 {
		t.Fatal("shutdown returned while rejected callback child was alive")
	}
}
