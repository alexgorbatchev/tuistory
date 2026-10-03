package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/remorses/tuistory/internal/session"
)

func TestRelaySecurityMiddleware(t *testing.T) {
	srv := NewServer("0.0.1", 19999, t.TempDir())
	handler := srv.Handler()

	// Legitimate request: no Origin, valid Host
	req := httptest.NewRequest("GET", "/version", nil)
	req.Host = "127.0.0.1:19999"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Attack 1: Cross-origin request with Origin header
	req = httptest.NewRequest("GET", "/version", nil)
	req.Host = "127.0.0.1:19999"
	req.Header.Set("Origin", "https://malicious.com")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for Origin header, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "forbidden") {
		t.Fatalf("expected 'forbidden' in response, got %q", w.Body.String())
	}

	// Attack 2: DNS rebinding with invalid Host header
	req = httptest.NewRequest("GET", "/version", nil)
	req.Host = "malicious.com:19999"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for invalid Host header, got %d", w.Code)
	}

	// Attack 3: Sec-Fetch-Site cross-site
	req = httptest.NewRequest("GET", "/version", nil)
	req.Host = "127.0.0.1:19999"
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for Sec-Fetch-Site cross-site, got %d", w.Code)
	}
}

func TestRelayVersionAndSessions(t *testing.T) {
	srv := NewServer("0.0.1", 19999, t.TempDir())
	handler := srv.Handler()

	// GET /version
	req := httptest.NewRequest("GET", "/version", nil)
	req.Host = "127.0.0.1:19999"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var versionResp struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &versionResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if versionResp.Version != "0.0.1" {
		t.Fatalf("expected version 0.0.1, got %q", versionResp.Version)
	}

	// GET /sessions initially empty
	req = httptest.NewRequest("GET", "/sessions", nil)
	req.Host = "127.0.0.1:19999"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var sessionsList []SessionInfo
	if err := json.Unmarshal(w.Body.Bytes(), &sessionsList); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(sessionsList) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessionsList))
	}
}

func TestRelayWebSocketAttach(t *testing.T) {
	srv := NewServer("0.0.1", 19999, t.TempDir())

	// Create a test session
	s, err := session.New(session.LaunchOptions{
		Command: "cat",
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	defer s.Close("test-done")

	srv.AddSession("test-attach", s)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/attach"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Host: "127.0.0.1:19999",
	})
	if err != nil {
		t.Fatalf("websocket.Dial error: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	// Send attach handshake
	handshake := map[string]any{
		"type":    "attach",
		"session": "test-attach",
		"cols":    80,
		"rows":    24,
	}
	handshakeBytes, _ := json.Marshal(handshake)
	if err := conn.Write(ctx, websocket.MessageText, handshakeBytes); err != nil {
		t.Fatalf("Write handshake error: %v", err)
	}

	// Send input through WebSocket
	if err := conn.Write(ctx, websocket.MessageText, []byte("hello from ws\r")); err != nil {
		t.Fatalf("Write input error: %v", err)
	}

	// Read echoed output from cat
	matched, err := s.WaitForText("hello from ws", 3*time.Second)
	if err != nil {
		t.Fatalf("WaitForText error: %v", err)
	}
	if !strings.Contains(matched, "hello from ws") {
		t.Fatalf("expected output to contain 'hello from ws', got %q", matched)
	}
}
