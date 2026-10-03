package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/remorses/tuistory/internal/relay"
	"github.com/remorses/tuistory/internal/session"
)

func TestAttachRawInputUsesBinaryFrames(t *testing.T) {
	for _, data := range [][]byte{[]byte(`{"type":"kill"}`), {0xff, 0xc0, 0x80, 0, 0x1b}, {0x03}} {
		t.Run(string(data), func(t *testing.T) {
			stdin, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			defer writer.Close()
			old := os.Stdin
			os.Stdin = stdin
			defer func() { os.Stdin = old }()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				typ, _, err := conn.Read(ctx)
				if err != nil || typ != websocket.MessageText {
					t.Errorf("handshake: %v %v", typ, err)
					return
				}
				if _, err := writer.Write(data); err != nil {
					t.Error(err)
					return
				}
				typ, got, err := conn.Read(ctx)
				if err != nil || typ != websocket.MessageBinary || !bytes.Equal(got, data) {
					t.Errorf("raw input: %v %q %v, want Binary %q", typ, got, err, data)
				}
				if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"closing"}`)); err != nil {
					t.Error(err)
				}
				_, _, _ = conn.Read(ctx) // Hold transport until attach closes it.
			}))
			defer ts.Close()
			if err := RunAttach(ts.Listener.Addr().(*net.TCPAddr).Port, "binary"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAttachReplaysRetainedTerminalBytes(t *testing.T) {
	for _, history := range []string{`{"type":"closing"}`, strings.Repeat("x", 65536)} {
		t.Run(fmt.Sprintf("%d bytes", len(history)), func(t *testing.T) {
			sess, err := session.New(session.LaunchOptions{Command: "sh", Args: []string{"-c", "printf '%s' '" + history + "'; sleep 10"}})
			if err != nil {
				t.Fatal(err)
			}
			defer sess.Close("test")
			deadline := time.Now().Add(2 * time.Second)
			for {
				var n atomic.Int64
				unsubscribe := sess.SubscribeWithBuffer(func(_ context.Context, data string) { n.Add(int64(len(data))) })
				unsubscribe()
				if n.Load() == int64(len(history)) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("PTY failed to retain complete history")
				}
				time.Sleep(time.Millisecond)
			}
			ts := httptest.NewUnstartedServer(nil)
			port := ts.Listener.Addr().(*net.TCPAddr).Port
			srv := relay.NewServer("binary", port, ".")
			srv.AddSession("binary", sess)
			ts.Config.Handler = srv.Handler()
			ts.Start()
			defer ts.Close()
			stdin, input, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			defer input.Close()
			output, stdout, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			defer stdout.Close()
			oldIn, oldOut := os.Stdin, os.Stdout
			os.Stdin, os.Stdout = stdin, stdout
			defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()
			done := make(chan error, 1)
			go func() { done <- RunAttach(port, "binary") }()
			captured := make(chan []byte, 1)
			go func() { data := make([]byte, len(history)); n, _ := io.ReadFull(output, data); captured <- data[:n] }()
			var got []byte
			ended := false
			select {
			case got = <-captured:
			case <-done:
				_ = stdout.Close() // Release reader after premature attach termination.
				got = <-captured
				t.Errorf("attach ended before retained output replay")
				ended = true
			case <-time.After(2 * time.Second):
				t.Error("retained output replay timed out")
			}
			sess.Close("test")
			_ = input.Close() // Also releases attach on a regression in close notification.
			if !ended {
				<-done
			}
			_ = stdout.Close()
			if string(got) != history {
				t.Errorf("replayed %d bytes, want %d", len(got), len(history))
			}
		})
	}
}
