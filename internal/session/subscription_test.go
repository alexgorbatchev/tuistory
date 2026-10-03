package session

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSlowSubscriberReceivesEveryChunk(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "stty raw -echo; printf ready; cat"}})
	if err := s.WaitForData(time.Second); err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	entered := make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	t.Cleanup(release)
	received := make(chan string, 512)
	unsub := s.Subscribe(func(chunk string) {
		enterOnce.Do(func() { close(entered); <-gate })
		_ = s.GetRawOutput() // Callback access must remain available during backpressure.
		received <- chunk
	})
	t.Cleanup(unsub)
	progress := make(chan struct{})
	written := make(chan error, 1)
	go func() {
		for i := range 256 {
			marker := fmt.Sprintf("marker%03d\n", i)
			if err := s.WriteRaw(marker); err != nil {
				written <- err
				return
			}
			deadline := time.Now().Add(3 * time.Second)
			for !strings.Contains(s.GetRawOutput(), marker) {
				if time.Now().After(deadline) {
					written <- fmt.Errorf("output stalled at marker %d", i)
					return
				}
				time.Sleep(100 * time.Microsecond)
			}
			if i == 200 {
				close(progress)
			}
		}
		written <- nil
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("subscriber not invoked")
	}
	select {
	case <-progress:
	case <-time.After(time.Second):
	}
	release()
	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("writer did not resume")
	}
	var expected strings.Builder
	for i := range 256 {
		fmt.Fprintf(&expected, "marker%03d\n", i)
	}
	var actual strings.Builder
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for actual.Len() < expected.Len() {
		select {
		case chunk := <-received:
			actual.WriteString(chunk)
		case <-deadline.C:
			t.Fatalf("subscriber lost bytes: received %d of %d", actual.Len(), expected.Len())
		}
	}
	if actual.String() != expected.String() {
		t.Fatalf("subscriber reordered data: %q", actual.String())
	}
}

func TestBufferedSubscriptionOrdersHistoryAndConcurrentOutput(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "stty raw -echo; printf ready; cat"}})
	if err := s.WaitForData(time.Second); err != nil {
		t.Fatal(err)
	}
	written := make(chan error, 1)
	go func() { written <- writeSubscriptionMarkers(context.Background(), s, 200) }()
	received := make(chan string, 512)
	unsub := s.SubscribeWithBuffer(func(ctx context.Context, chunk string) {
		if ctx.Err() != nil {
			return
		}
		_ = s.GetRawOutput()
		received <- chunk
	})
	defer unsub()
	first := <-received
	if !strings.HasPrefix(first, "ready") {
		t.Fatalf("history was not delivered first: %q", first)
	}
	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("writer stalled")
	}
	var actual strings.Builder
	actual.WriteString(first)
	expected := s.GetRawOutput()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for actual.Len() < len(expected) {
		select {
		case chunk := <-received:
			actual.WriteString(chunk)
		case <-deadline.C:
			t.Fatalf("missing history/live bytes: %d of %d", actual.Len(), len(expected))
		}
	}
	if actual.String() != expected {
		t.Fatalf("history/live stream differs:\n got %q\nwant %q", actual.String(), expected)
	}
}

func TestCloseCancelsBlockedHistoricalSubscription(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "printf ready; sleep 10"}})
	if err := s.WaitForData(time.Second); err != nil {
		t.Fatal(err)
	}
	entered := make(chan context.Context, 1)
	returned := make(chan func(), 1)
	go func() {
		returned <- s.SubscribeWithBuffer(func(ctx context.Context, _ string) {
			_ = s.GetRawOutput()
			entered <- ctx
			<-ctx.Done()
		})
	}()
	ctx := <-entered
	s.OnClosing(func(string) {
		if ctx.Err() == nil {
			t.Error("subscription was not canceled before closing callback")
		}
		_ = s.GetRawOutput()
	})
	s.Close("test")
	select {
	case unsub := <-returned:
		unsub()
		unsub()
	case <-time.After(time.Second):
		t.Fatal("history callback did not cancel")
	}
}

func TestCloseCancelsSubscriberBackpressure(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "stty raw -echo; printf ready; cat"}})
	if err := s.WaitForData(time.Second); err != nil {
		t.Fatal(err)
	}
	entered := make(chan context.Context, 1)
	returned := make(chan func(), 1)
	go func() {
		returned <- s.SubscribeWithBuffer(func(ctx context.Context, _ string) { entered <- ctx; <-ctx.Done() })
	}()
	ctx := <-entered
	written := make(chan error, 1)
	go func() { written <- writeSubscriptionMarkers(ctx, s, 256) }()
	// The canceled callback holds the subscription while the bounded queue fills.
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(s.GetRawOutput(), "marker128") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(s.GetRawOutput(), "marker128") {
		s.Close("cleanup")
		t.Fatal("bounded queue did not receive output")
	}
	s.Close("test")
	select {
	case unsub := <-returned:
		unsub()
	case <-time.After(time.Second):
		t.Fatal("subscription did not unblock")
	}
	select {
	case <-written:
	case <-time.After(time.Second):
		t.Fatal("writer remained blocked after cancellation")
	}
	select {
	case <-s.readDone:
	case <-time.After(time.Second):
		t.Fatal("PTY reader remained blocked after cancellation")
	}
}

func TestBufferedSubscriptionCanUnsubscribeItself(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "cat"})
	done := make(chan struct{})
	ready := make(chan struct{})
	var unsub func()
	unsub = s.SubscribeWithBuffer(func(ctx context.Context, _ string) {
		<-ready
		unsub()
		if ctx.Err() == nil {
			t.Error("unsubscribe did not cancel context")
		}
		close(done)
	})
	close(ready)
	defer unsub()
	if err := s.WriteRaw("hello\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("callback self-unsubscribe deadlocked")
	}
}

func TestSubscribeClosedSessionDoesNotRegister(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "cat"})
	s.Close("test")
	unsub := s.SubscribeWithBuffer(func(context.Context, string) { t.Error("closed callback invoked") })
	unsub()
	unsub()
	s.mu.RLock()
	count := len(s.subscribers)
	s.mu.RUnlock()
	if count != 0 {
		t.Fatal("closed session retained subscriber")
	}
}

func writeSubscriptionMarkers(ctx context.Context, s *Session, count int) error {
	for i := range count {
		marker := fmt.Sprintf("marker%03d\n", i)
		if err := s.WriteRaw(marker); err != nil {
			return err
		}
		deadline := time.Now().Add(3 * time.Second)
		for !strings.Contains(s.GetRawOutput(), marker) {
			if err := ctx.Err(); err != nil {
				return err
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("output stalled at marker %d", i)
			}
			time.Sleep(100 * time.Microsecond)
		}
	}
	return nil
}
