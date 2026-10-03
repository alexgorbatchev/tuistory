package session

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSessionCloseState(t *testing.T) {
	for _, command := range []string{"cat", "true"} {
		t.Run(command, func(t *testing.T) {
			s := launchTestSession(t, LaunchOptions{Command: command})
			if command == "true" && !s.WaitForExit(time.Second) {
				t.Fatal("short-lived child did not exit")
			}
			if s.IsClosed() {
				t.Fatal("process exit or startup closed the session")
			}
			s.OnClosing(func(string) {
				if !s.IsClosed() {
					t.Error("closing callback ran before closed state")
				}
			})
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					for range 100 {
						s.IsClosed()
					}
				})
			}
			s.Close("test")
			wg.Wait()
			if !s.IsClosed() {
				t.Fatal("Close did not expose closed state")
			}
			ctx, cancel := context.WithTimeout(context.Background(), killGraceDuration+time.Second)
			defer cancel()
			if err := s.CloseAndWait(ctx, "again"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
