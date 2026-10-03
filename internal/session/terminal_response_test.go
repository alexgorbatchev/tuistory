package session

import (
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"golang.org/x/term"
)

func TestTerminalQueryChild(t *testing.T) {
	if os.Getenv(runtimeHelperEnv) != "terminal-query" {
		return
	}
	state, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := term.Restore(int(os.Stdin.Fd()), state); err != nil {
			t.Error(err)
		}
	}()
	queries := []struct {
		query string
		end   byte
	}{
		{query: "\x1b[3;7H\x1b[6n", end: 'R'},
		{query: "\x1b[c", end: 'c'},
	}
	for _, query := range queries {
		if _, err := io.WriteString(os.Stdout, query.query); err != nil {
			t.Fatal(err)
		}
		var response []byte
		buf := make([]byte, 1)
		for {
			if _, err := io.ReadFull(os.Stdin, buf); err != nil {
				t.Fatal(err)
			}
			response = append(response, buf[0])
			if buf[0] == query.end {
				break
			}
		}
		fmt.Fprintf(os.Stdout, "RESPONSE=%x\r\n", response)
	}
}

func TestTerminalRepliesReachChild(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{
		Command: os.Args[0], Args: []string{"-test.run=^TestTerminalQueryChild$"},
		Env: map[string]string{runtimeHelperEnv: "terminal-query"},
	})
	if _, err := s.WaitForText("RESPONSE=1b5b333b3752", 3*time.Second); err != nil {
		t.Fatalf("cursor position reply did not reach child: %v", err)
	}
	if _, err := s.WaitForText("RESPONSE=1b5b3f", 3*time.Second); err != nil {
		t.Fatalf("device attributes reply did not reach child: %v", err)
	}
	if !s.WaitForExit(3 * time.Second) {
		t.Fatal("query child did not finish")
	}
}

func TestTerminalReplyBackpressureDoesNotBlockClose(t *testing.T) {
	s := launchTestSession(t, LaunchOptions{Command: "sh", Args: []string{"-c", "trap '' HUP TERM; stty raw -echo; printf ready; while :; do printf '\033[c'; done"}})
	if _, err := s.WaitForText("ready", time.Second); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.terminalReplyQueue) == cap(s.terminalReplyQueue) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(s.terminalReplyQueue) != cap(s.terminalReplyQueue) {
		t.Fatal("child did not fill the terminal response queue")
	}
	closed := make(chan struct{})
	go func() { s.Close("test"); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("terminal response backpressure blocked Close")
	}
}
