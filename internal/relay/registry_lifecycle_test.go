package relay

import (
	"testing"
	"time"

	"github.com/remorses/tuistory/internal/session"
)

func TestCloseAllAllowsRegistryCallbacks(t *testing.T) {
	r := NewSessionRegistry()
	s, err := session.New(session.LaunchOptions{Command: "true"})
	if err != nil {
		t.Fatal(err)
	}
	r.Set("old", s)
	called := make(chan struct{})
	s.OnClosing(func(string) {
		if r.Get("old") != nil {
			t.Error("closing session still registered")
		}
		r.Set("replacement", s)
		close(called)
	})
	done := make(chan struct{})
	go func() { r.CloseAll("test"); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("CloseAll deadlocked in registry callback")
	}
	select {
	case <-called:
	default:
		t.Fatal("close callback not invoked")
	}
	if r.Get("replacement") != s {
		t.Fatal("callback-created session was removed")
	}
}
