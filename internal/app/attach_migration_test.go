package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
	"github.com/remorses/tuistory/internal/relay"
	"golang.org/x/sys/unix"
)

func TestAttachCancellationWithoutInput(t *testing.T) {
	for _, message := range []string{`{"type":"closing"}`, `{"type":"error","message":"closed"}`, "disconnect"} {
		t.Run(message, func(t *testing.T) {
			stdin, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			defer writer.Close()
			old := os.Stdin
			os.Stdin = stdin
			defer func() { os.Stdin = old }()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if _, _, err := conn.Read(ctx); err != nil {
					t.Error(err)
					return
				}
				if message != "disconnect" {
					if err := conn.Write(ctx, websocket.MessageText, []byte(message)); err != nil {
						t.Error(err)
					}
					_, _, _ = conn.Read(ctx) // Keep transport open; notification itself must cancel attach.
				}
			}))
			defer server.Close()
			port := server.Listener.Addr().(*net.TCPAddr).Port
			done := make(chan error, 1)
			go func() { done <- RunAttach(port, "test") }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(300 * time.Millisecond):
				_, _ = writer.Write([]byte("wake"))
				<-done
				t.Fatal("attach failed to cancel while stdin was idle")
			}
			if _, err := writer.Write([]byte("still-open")); err != nil {
				t.Fatalf("attach closed caller stdin: %v", err)
			}
		})
	}
}

func TestAttachControlKeys(t *testing.T) {
	for _, tt := range []struct {
		name, input, want string
		coalesced         bool
	}{
		{"ordinary", "hello", "hello", false},
		{"single interrupt", "\x03", "\x03", false},
		{"double detach", "\x03", "", false},
		{"double kill", "\x18", `{"type":"kill"}`, false},
		{"coalesced detach", "\x03\x03", "", true},
		{"coalesced kill", "\x18\x18", `{"type":"kill"}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stdin, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			defer writer.Close()
			old := os.Stdin
			os.Stdin = stdin
			defer func() { os.Stdin = old }()
			ready := make(chan struct{})
			messages := make(chan string, 10)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				if _, _, err := conn.Read(ctx); err != nil {
					t.Error(err)
					return
				}
				close(ready)
				for {
					_, data, err := conn.Read(ctx)
					if err != nil {
						return
					}
					messages <- string(data)
				}
			}))
			defer server.Close()
			done := make(chan error, 1)
			go func() { done <- RunAttach(server.Listener.Addr().(*net.TCPAddr).Port, "test") }()
			<-ready
			if _, err := writer.Write([]byte(tt.input)); err != nil {
				t.Fatal(err)
			}
			if tt.name == "double detach" || tt.name == "double kill" {
				time.Sleep(50 * time.Millisecond)
				if _, err := writer.Write([]byte(tt.input)); err != nil {
					t.Fatal(err)
				}
			}
			if tt.want != "" {
				select {
				case got := <-messages:
					if got != tt.want {
						t.Errorf("got %q want %q", got, tt.want)
					}
				case <-time.After(time.Second):
					t.Error("no forwarded control/input")
				}
			}
			if tt.name == "ordinary" || tt.name == "single interrupt" {
				_ = writer.Close()
			}
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(300 * time.Millisecond):
				_ = writer.Close()
				<-done
				t.Error("control input did not detach")
			}
			select {
			case extra := <-messages:
				if tt.want == "" {
					t.Errorf("detach leaked input %q", extra)
				}
			default:
			}
		})
	}
}

func TestAttachAutomaticSessionSelection(t *testing.T) {
	for _, tt := range []struct {
		name, body, want string
		wantErr          bool
	}{
		{"one alive", `[{"name":"dead","dead":true},{"name":"alive"}]`, "alive", false},
		{"many alive", `[{"name":"first"},{"name":"second"}]`, "first", false},
		{"empty", `[]`, "", true},
		{"invalid", `invalid`, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/sessions" {
					_, _ = w.Write([]byte(tt.body))
					return
				}
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_, data, err := conn.Read(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				var msg struct {
					Session string `json:"session"`
				}
				if err := json.Unmarshal(data, &msg); err != nil {
					t.Error(err)
				}
				if msg.Session != tt.want {
					t.Errorf("selected %q want %q", msg.Session, tt.want)
				}
				_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"closing"}`))
			}))
			defer server.Close()
			err := RunAttach(server.Listener.Addr().(*net.TCPAddr).Port, "")
			if (err != nil) != tt.wantErr {
				t.Fatalf("error %v wantErr %v", err, tt.wantErr)
			}
		})
	}
	if err := RunAttach(0, ""); err == nil {
		t.Fatal("unavailable daemon accepted")
	}
	if err := RunAttach(0, "test"); err == nil {
		t.Fatal("unavailable websocket accepted")
	}
}

func TestAttachTerminalOutputAndResize(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	if err := pty.Setsize(slave, &pty.Winsize{Cols: 40, Rows: 10}); err != nil {
		t.Fatal(err)
	}
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = slave, slave
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()
	ready := make(chan struct{})
	resized := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		var dimensions struct{ Cols, Rows int }
		if err := json.Unmarshal(data, &dimensions); err != nil {
			t.Error(err)
		}
		if dimensions.Cols != 40 || dimensions.Rows != 10 {
			t.Errorf("dimensions: %+v", dimensions)
		}
		for _, data := range []string{`{"type":"exit"}`, `{broken`, "output"} {
			if err := conn.Write(ctx, websocket.MessageText, []byte(data)); err != nil {
				t.Error(err)
			}
		}
		close(ready)
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var msg struct{ Type string }
			_ = json.Unmarshal(data, &msg)
			if msg.Type == "resize" {
				close(resized)
				_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"closing"}`))
				return
			}
		}
	}))
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- RunAttach(server.Listener.Addr().(*net.TCPAddr).Port, "test") }()
	<-ready
	// The resize watcher starts immediately after raw-mode setup; retry the native
	// signal until a resize response is observed instead of assuming scheduling.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := unix.Kill(os.Getpid(), unix.SIGWINCH); err != nil {
			t.Fatal(err)
		}
		select {
		case <-resized:
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatal("terminal resize was not forwarded")
}

func TestFetchSessionsRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]relay.SessionInfo{{Name: "review"}})
	}))
	defer server.Close()
	sessions, err := fetchSessions(server.Listener.Addr().(*net.TCPAddr).Port)
	if err != nil || len(sessions) != 1 || sessions[0].Name != "review" {
		t.Fatalf("sessions: %v / %v", sessions, err)
	}
}

func TestPollInputLifecycle(t *testing.T) {
	stdin, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pollInput(ctx, int(stdin.Fd())); err == nil {
		t.Fatal("cancelled poll succeeded")
	}
	if ready, err := pollInput(context.Background(), int(stdin.Fd())); err != nil || ready {
		t.Fatalf("idle poll %v/%v", ready, err)
	}
	if _, err := writer.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	if ready, err := pollInput(context.Background(), int(stdin.Fd())); err != nil || !ready {
		t.Fatalf("ready poll %v/%v", ready, err)
	}
	fd := int(stdin.Fd())
	_ = stdin.Close()
	if _, err := pollInput(context.Background(), fd); err == nil {
		t.Fatal("invalid descriptor accepted")
	}
}
