package relay

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestClosingSessionDoesNotForwardExitBeforeClosing(t *testing.T) {
	srv := NewServer("test", 19999, ".")
	s := relaySession(t, "printf ready; exec cat")
	closing := make(chan struct{})
	resume := make(chan struct{})
	var resumed sync.Once
	release := func() { resumed.Do(func() { close(resume) }) }
	t.Cleanup(release)
	s.OnClosing(func(string) {
		close(closing)
		<-resume
	})
	srv.AddSession("closing", s)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn := relayConnection(t, ctx, ts)
	defer conn.CloseNow()
	writeControl(t, ctx, conn, map[string]any{"type": "attach", "session": "closing"})
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { s.Close("ordered-closing"); close(closed) }()
	<-closing
	type readResult struct {
		data []byte
		err  error
	}
	message := make(chan readResult, 1)
	go func() {
		_, data, err := conn.Read(ctx)
		message <- readResult{data, err}
	}()
	if !s.WaitForExit(time.Second) {
		t.Error("closing child did not exit")
	}
	var result readResult
	select {
	case result = <-message:
		t.Errorf("notification escaped before closing callback: %s/%v", result.data, result.err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if result.data == nil && result.err == nil {
		result = <-message
	}
	var control map[string]any
	if err := json.Unmarshal(result.data, &control); err != nil || result.err != nil || control["type"] != "closing" || control["reason"] != "ordered-closing" {
		t.Errorf("first notification must preserve closing reason: %s/%v/%v", result.data, result.err, err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "acknowledge closing") // Best-effort close acknowledgement unblocks the server callback.
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("closing callback did not finish")
	}
}
