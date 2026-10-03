package relay

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/remorses/tuistory/internal/session"
)

func TestRegistryReplacementAndEviction(t *testing.T) {
	r := NewSessionRegistry()
	first := relaySession(t, "printf first; sleep 10")
	second := relaySession(t, "printf second; sleep 10")
	r.Set("replace", first)
	r.Set("replace", second)
	r.DeleteIfMatches("replace", first)
	if r.Get("replace") != second {
		t.Fatal("old deletion removed replacement")
	}
	r.DeleteIfMatches("replace", second)
	if r.Get("replace") != nil {
		t.Fatal("matching deletion failed")
	}
	r.Set("delete", first)
	r.Delete("delete")
	if r.Get("delete") != nil {
		t.Fatal("delete failed")
	}
	dead := relaySession(t, "printf dead; exit 0")
	if !dead.WaitForExit(time.Second) {
		t.Fatal("expected exit")
	}
	r.Set("dead", dead)
	r.Set("live", second)
	r.EvictStaleDead()
	if r.Get("dead") == nil {
		t.Fatal("recent dead session evicted")
	}
	closed := make(chan struct{})
	dead.OnClosing(func(reason string) {
		if r.Get("dead") != nil || reason != "stale-eviction" {
			t.Errorf("eviction state/reason %q", reason)
		}
		r.Set("replacement", first)
		close(closed)
	})
	r.evictStaleDead(time.Now().Add(25 * time.Hour))
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("eviction callback deadlock")
	}
	if r.Get("live") != second || r.Get("replacement") != first {
		t.Fatal("eviction affected live/replacement session")
	}
	if len(r.List()) != 2 {
		t.Fatal("registry list wrong")
	}
	r.CloseAll("test")
	if len(r.List()) != 0 {
		t.Fatal("CloseAll retained entries")
	}
}

func TestRegistryListSortsNewestFirst(t *testing.T) {
	r := NewSessionRegistry()
	first := relaySession(t, "printf first; sleep 10")
	time.Sleep(2 * time.Millisecond)
	second := relaySession(t, "printf second; sleep 10")
	r.Set("first", first)
	r.Set("second", second)
	for range 20 {
		list := r.List()
		if len(list) != 2 || list[0].Name != "second" || list[1].Name != "first" {
			t.Fatalf("not sorted newest first: %+v", list)
		}
	}
}

func TestRelayCLIHTTPBehavior(t *testing.T) {
	srv := NewServer("review", 19999, ".")
	if srv.Sessions() == nil {
		t.Fatal("registry unavailable")
	}
	sess := relaySession(t, "printf ready; sleep 10")
	srv.AddSession("review", sess)
	for _, tt := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/sessions", "", http.StatusOK},
		{"POST", "/cli", "not-json", http.StatusBadRequest},
		{"GET", "/cli", "", http.StatusMethodNotAllowed},
		{"POST", "/cli", `{"argv":["snapshot"],"cwd":"/work","env":{"CHECK":"value"}}`, http.StatusOK},
		{"GET", "/attach", "", http.StatusUpgradeRequired},
		{"GET", "/missing", "", http.StatusNotFound},
	} {
		req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
		req.Host = "127.0.0.1:19999"
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != tt.status {
			t.Fatalf("%s %s status %d want %d", tt.method, tt.path, w.Code, tt.status)
		}
		if tt.method == "POST" && w.Code == http.StatusOK {
			var result CLIResult
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.ExitCode != 1 || result.Stderr != "cli runner not configured" {
				t.Fatalf("unconfigured runner: %+v", result)
			}
		}
	}
	srv.SetCLIRunner(func(req CLIRequest, registry *SessionRegistry, logger *slog.Logger) CLIResult {
		if len(req.Argv) != 1 || req.Argv[0] != "snapshot" || req.Cwd != "/work" || req.Env["CHECK"] != "value" || registry.Get("review") != sess || logger == nil {
			t.Errorf("runner request: %+v", req)
		}
		return CLIResult{Stdout: sess.ReadAll()}
	})
	req := httptest.NewRequest("POST", "/cli", strings.NewReader(`{"argv":["snapshot"],"cwd":"/work","env":{"CHECK":"value"}}`))
	req.Host = "127.0.0.1:19999"
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var result CLIResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, "ready") {
		t.Fatalf("runner output: %+v", result)
	}
}

func TestLocalOnlyHeaderMatrix(t *testing.T) {
	for _, tt := range []struct {
		host, origin, fetch string
		allowed             bool
	}{
		{"LOCALHOST:19999", "", "same-origin", true},
		{"[::1]:19999", "", "none", true},
		{"127.0.0.1:19999", "", "", true},
		{"localhost:19999", "http://localhost:19999", "", false},
		{"localhost:19999", "", "same-site", false},
		{"localhost:19998", "", "", false},
	} {
		req := httptest.NewRequest("GET", "/version", nil)
		req.Host = tt.host
		req.Header.Set("Origin", tt.origin)
		req.Header.Set("Sec-Fetch-Site", tt.fetch)
		if err := CheckLocalOnly(req, 19999); (err == nil) != tt.allowed {
			t.Fatalf("headers %+v error %v", tt, err)
		}
	}
}

func TestRelayAttachMissingAndDeadSessions(t *testing.T) {
	srv := NewServer("review", 19999, ".")
	dead := relaySession(t, "printf final; exit 7")
	if !dead.WaitForExit(time.Second) {
		t.Fatal("process did not exit")
	}
	srv.AddSession("dead", dead)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	for _, name := range []string{"missing", "dead"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn := relayConnection(t, ctx, ts)
			defer conn.CloseNow()
			writeControl(t, ctx, conn, map[string]any{"type": "attach", "session": name, "cols": 30, "rows": 3})
			if name == "missing" {
				msg := readControl(t, ctx, conn)
				if msg["type"] != "error" || !strings.Contains(msg["message"].(string), "missing") {
					t.Fatalf("error: %+v", msg)
				}
				return
			}
			_, raw, err := conn.Read(ctx)
			if err != nil || !strings.Contains(string(raw), "final") {
				t.Fatalf("raw %q/%v", raw, err)
			}
			msg := readControl(t, ctx, conn)
			if msg["type"] != "exit" || msg["exitCode"] != float64(7) {
				t.Fatalf("exit: %+v", msg)
			}
			quiet, cancelQuiet := context.WithTimeout(ctx, 30*time.Millisecond)
			defer cancelQuiet()
			_, data, err := conn.Read(quiet)
			if err == nil {
				t.Fatalf("duplicate dead-session notification: %s", data)
			}
		})
	}
}

func TestRelayAttachResizeRawAndKill(t *testing.T) {
	srv := NewServer("review", 19999, ".")
	sess := relaySession(t, "stty raw -echo; printf ready; cat")
	srv.AddSession("live", sess)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn := relayConnection(t, ctx, ts)
	defer conn.CloseNow()
	writeControl(t, ctx, conn, map[string]any{"type": "resize", "cols": 30, "rows": 3})
	writeControl(t, ctx, conn, map[string]any{"type": "kill"})
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("discard-before-attach")); err != nil {
		t.Fatal(err)
	}
	writeControl(t, ctx, conn, map[string]any{"type": "attach", "session": "live", "cols": 30, "rows": 3})
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if sess.Cols() != 30 || sess.Rows() != 3 {
		t.Fatal("handshake resize failed")
	}
	writeControl(t, ctx, conn, map[string]any{"type": "resize", "cols": 50, "rows": 5})
	writeControl(t, ctx, conn, map[string]any{"type": "resize", "cols": 0, "rows": 0})
	for _, payload := range []string{"plain", "{broken", `{"type":"unknown"}`} {
		if err := conn.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
			t.Fatal(err)
		}
		if _, err := sess.WaitForText(payload, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if sess.Cols() != 50 || sess.Rows() != 5 {
		t.Fatal("resize control failed")
	}
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("binary")); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.WaitForText("binary", time.Second); err != nil {
		t.Fatal(err)
	}
	writeControl(t, ctx, conn, map[string]any{"type": "kill"})
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var msg map[string]any
		if json.Unmarshal(data, &msg) == nil && msg["type"] == "exit" {
			break
		}
	}
	if !sess.IsDead() {
		t.Fatal("kill did not exit process")
	}
	writeControl(t, ctx, conn, map[string]any{"type": "resize", "cols": 70, "rows": 7})
	if err := conn.Write(ctx, websocket.MessageText, []byte("ignored-dead")); err != nil {
		t.Fatal(err)
	}
}

func TestRelayReattachUnsubscribesOldSession(t *testing.T) {
	srv := NewServer("review", 19999, ".")
	first := relaySession(t, "stty raw -echo; printf first; cat")
	second := relaySession(t, "stty raw -echo; printf second; cat")
	srv.AddSession("first", first)
	srv.AddSession("second", second)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn := relayConnection(t, ctx, ts)
	defer conn.CloseNow()
	for _, name := range []string{"first", "second"} {
		writeControl(t, ctx, conn, map[string]any{"type": "attach", "session": name})
		_, data, err := conn.Read(ctx)
		if err != nil || !strings.Contains(string(data), name) {
			t.Fatalf("history %q/%v", data, err)
		}
	}
	if err := first.WriteRaw("OLD"); err != nil {
		t.Fatal(err)
	}
	first.Close("old-closing")
	if err := conn.Write(ctx, websocket.MessageText, []byte("NEW")); err != nil {
		t.Fatal(err)
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("old closing terminated new attachment: %v", err)
		}
		if strings.Contains(string(data), "OLD") || strings.Contains(string(data), "old-closing") {
			t.Fatalf("old session leaked into new attachment: %s", data)
		}
		if strings.Contains(string(data), "NEW") {
			break
		}
	}
	closed := make(chan struct{})
	go func() { second.Close("new-closing"); close(closed) }()
	msg := readControl(t, ctx, conn)
	if msg["type"] != "closing" || msg["reason"] != "new-closing" {
		t.Fatalf("close: %+v", msg)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "acknowledge closing")
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("closing callback did not complete")
	}
}

func TestRelayRuntimePaths(t *testing.T) {
	base := filepath.Join(".tmp", "relay-tests")
	t.Setenv("TMPDIR", base)
	t.Setenv("TUISTORY_LOG_FILE_PATH", "")
	if got := LogFilePath(); got != filepath.Join(base, "tuistory", "relay-server.log") {
		t.Fatalf("log path %q", got)
	}
	if got := PidFilePath(1234); got != filepath.Join(base, "tuistory", "relay-1234.pid") {
		t.Fatalf("pid path %q", got)
	}
	if got := RestartLockFilePath(1234); got != filepath.Join(base, "tuistory", "restart-1234.lock") {
		t.Fatalf("lock path %q", got)
	}
	t.Setenv("TUISTORY_LOG_FILE_PATH", filepath.Join(base, "custom.log"))
	if got := LogFilePath(); got != filepath.Join(base, "custom.log") {
		t.Fatalf("custom log path %q", got)
	}
	t.Setenv("TMPDIR", "")
	t.Setenv("TUISTORY_LOG_FILE_PATH", "")
	if !filepath.IsAbs(LogFilePath()) || !filepath.IsAbs(PidFilePath(1234)) || !filepath.IsAbs(RestartLockFilePath(1234)) {
		t.Fatal("default runtime paths not absolute")
	}
}

func relaySession(t *testing.T, script string) *session.Session {
	t.Helper()
	sess, err := session.New(session.LaunchOptions{Command: "sh", Args: []string{"-c", script}, Cols: 80, Rows: 10, IdleDelay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close("test") })
	if err := sess.WaitForData(time.Second); err != nil {
		t.Fatal(err)
	}
	return sess
}

func relayConnection(t *testing.T, ctx context.Context, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/attach", &websocket.DialOptions{Host: "127.0.0.1:19999"})
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func writeControl(t *testing.T, ctx context.Context, conn *websocket.Conn, control map[string]any) {
	t.Helper()
	data, err := json.Marshal(control)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func readControl(t *testing.T, ctx context.Context, conn *websocket.Conn) map[string]any {
	t.Helper()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var control map[string]any
	if err := json.Unmarshal(data, &control); err != nil {
		t.Fatalf("control %q: %v", data, err)
	}
	return control
}
