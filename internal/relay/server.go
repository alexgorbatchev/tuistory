package relay

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/remorses/tuistory/internal/session"
)

// Protocol identifies the CLI wire contract, independently of release ordering.
// TypeScript daemons use a different argv layout and must not accept Go clients.
const Protocol = "tuistory-go/1"

// SessionInfo models the JSON representation of an active session.
type SessionInfo struct {
	Name      string `json:"name"`
	Command   string `json:"command"`
	Cwd       string `json:"cwd"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
	Dead      bool   `json:"dead"`
	StartedAt int64  `json:"startedAt"`
}

// CLIRequest represents an incoming command invocation to be executed in-daemon.
type CLIRequest struct {
	Argv []string          `json:"argv"`
	Cwd  string            `json:"cwd"`
	Env  map[string]string `json:"env"`
}

// CLIResult represents the outcome of an in-daemon command execution.
type CLIResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

// CLIRunnerFunc defines the signature for executing CLI commands against the daemon's sessions.
type CLIRunnerFunc func(req CLIRequest, sessions *SessionRegistry, logger *slog.Logger) CLIResult

// SessionRegistry manages thread-safe storage of sessions.
type SessionRegistry struct {
	mu       sync.RWMutex
	sessions map[string]*session.Session
}

// NewSessionRegistry creates a new session registry.
func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{
		sessions: make(map[string]*session.Session),
	}
}

// Get returns the session with the given name, if present.
func (r *SessionRegistry) Get(name string) *session.Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessions[name]
}

// Set stores a session under the given name.
func (r *SessionRegistry) Set(name string, s *session.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[name] = s
}

// Delete removes a session if present.
func (r *SessionRegistry) Delete(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, name)
}

// DeleteIfMatches removes the session only if it currently points to target.
func (r *SessionRegistry) DeleteIfMatches(name string, target *session.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sessions[name] == target {
		delete(r.sessions, name)
	}
}

// List returns a snapshot of all active sessions sorted by most recently started first.
func (r *SessionRegistry) List() []SessionInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]SessionInfo, 0, len(r.sessions))
	for name, s := range r.sessions {
		list = append(list, SessionInfo{
			Name:      name,
			Command:   s.Command(),
			Cwd:       s.Cwd(),
			Cols:      s.Cols(),
			Rows:      s.Rows(),
			Dead:      s.IsDead(),
			StartedAt: s.StartedAt().UnixMilli(),
		})
	}
	slices.SortFunc(list, func(a, b SessionInfo) int {
		if order := cmp.Compare(b.StartedAt, a.StartedAt); order != 0 {
			return order
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return list
}

// EvictStaleDead cleans up dead sessions older than 24 hours.
func (r *SessionRegistry) EvictStaleDead() {
	r.evictStaleDead(time.Now())
}

func (r *SessionRegistry) evictStaleDead(now time.Time) {
	r.mu.Lock()
	var stale []*session.Session

	const oneDay = 24 * time.Hour
	for name, s := range r.sessions {
		if s.IsDead() {
			if exited := s.ExitedAt(); exited != nil && now.Sub(*exited) > oneDay {
				delete(r.sessions, name)
				stale = append(stale, s)
			}
		}
	}
	r.mu.Unlock()
	for _, s := range stale {
		s.Close("stale-eviction")
	}
}

// CloseAll closes every running session and empties the registry.
func (r *SessionRegistry) CloseAll(reason string) {
	r.mu.Lock()
	sessions := r.sessions
	r.sessions = make(map[string]*session.Session)
	r.mu.Unlock()

	// Closing invokes application callbacks, which may access the registry.
	for _, s := range sessions {
		s.Close(reason)
	}
}

// Server handles relay daemon HTTP and WebSocket requests.
type Server struct {
	version   string
	port      int
	dir       string
	sessions  *SessionRegistry
	logger    *slog.Logger
	cliRunner CLIRunnerFunc
}

// NewServer initializes a new Server.
func NewServer(version string, port int, dir string) *Server {
	return &Server{
		version:  version,
		port:     port,
		dir:      dir,
		sessions: NewSessionRegistry(),
		logger:   slog.Default(),
	}
}

// SetCLIRunner sets the callback for handling POST /cli requests.
func (s *Server) SetCLIRunner(fn CLIRunnerFunc) {
	s.cliRunner = fn
}

// Sessions returns the server's session registry.
func (s *Server) Sessions() *SessionRegistry {
	return s.sessions
}

// AddSession registers a session directly (convenience for tests).
func (s *Server) AddSession(name string, sess *session.Session) {
	s.sessions.Set(name, sess)
}

// CheckLocalOnly validates that a request originated locally and not from a browser.
func CheckLocalOnly(r *http.Request, port int) error {
	origin := r.Header.Get("Origin")
	if origin != "" {
		return fmt.Errorf("forbidden: cross-origin request from %s", origin)
	}

	host := strings.ToLower(r.Host)
	expectedHosts := map[string]struct{}{
		fmt.Sprintf("127.0.0.1:%d", port): {},
		fmt.Sprintf("localhost:%d", port): {},
		fmt.Sprintf("[::1]:%d", port):     {},
	}
	if _, ok := expectedHosts[host]; !ok {
		return fmt.Errorf("forbidden: unexpected Host header %q", r.Host)
	}

	fetchSite := strings.ToLower(r.Header.Get("Sec-Fetch-Site"))
	if fetchSite != "" && fetchSite != "same-origin" && fetchSite != "none" {
		return fmt.Errorf("forbidden: Sec-Fetch-Site is %q", fetchSite)
	}

	return nil
}

// Handler returns the HTTP handler with all daemon endpoints and security middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"version":  s.version,
			"protocol": Protocol,
		})
	})

	mux.HandleFunc("GET /sessions", func(w http.ResponseWriter, r *http.Request) {
		list := s.sessions.List()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	})

	mux.HandleFunc("POST /cli", func(w http.ResponseWriter, r *http.Request) {
		var req CLIRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		res := CLIResult{ExitCode: 1, Stderr: "cli runner not configured"}
		if s.cliRunner != nil {
			res = s.cliRunner(req, s.sessions, s.logger)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})

	mux.HandleFunc("/attach", s.handleAttach)

	// Wrap mux with security middleware
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := CheckLocalOnly(r, s.port); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) handleAttach(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.logger.Error("websocket accept error", "error", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var (
		attachedSession *session.Session
		sessionName     string
		unsubData       func()
		unsubExit       func()
		unsubClosing    func()
	)
	unsubscribe := func() {
		if unsubData != nil {
			unsubData()
			unsubData = nil
		}
		if unsubExit != nil {
			unsubExit()
			unsubExit = nil
		}
		if unsubClosing != nil {
			unsubClosing()
			unsubClosing = nil
		}
	}
	defer unsubscribe()

	for {
		typ, reader, err := conn.Reader(ctx)
		if err != nil {
			return
		}

		payload, err := io.ReadAll(reader)
		if err != nil {
			return
		}

		if typ == websocket.MessageText && len(payload) > 0 && payload[0] == '{' {
			var ctrl struct {
				Type    string `json:"type"`
				Session string `json:"session"`
				Cols    int    `json:"cols"`
				Rows    int    `json:"rows"`
				Reason  string `json:"reason"`
			}
			if err := json.Unmarshal(payload, &ctrl); err == nil && ctrl.Type != "" {
				switch ctrl.Type {
				case "attach":
					unsubscribe()
					sessionName = ctrl.Session
					attachedSession = s.sessions.Get(sessionName)
					if attachedSession == nil {
						errMsg, _ := json.Marshal(map[string]string{
							"type":    "error",
							"message": fmt.Sprintf("Session %q not found", sessionName),
						})
						_ = conn.Write(ctx, websocket.MessageText, errMsg)
						return
					}

					if ctrl.Cols > 0 && ctrl.Rows > 0 && !attachedSession.IsDead() {
						_ = attachedSession.Resize(ctrl.Cols, ctrl.Rows)
					}

					// Atomically replay history and register live delivery, without gaps.
					unsubData = attachedSession.SubscribeWithBuffer(func(ctx context.Context, data string) {
						if err := conn.Write(ctx, websocket.MessageBinary, []byte(data)); err != nil {
							cancel()
						}
					})

					// Forward exit notification
					unsubExit = attachedSession.OnExit(func(info session.ExitInfo) {
						exitMsg, _ := json.Marshal(map[string]any{
							"type":     "exit",
							"exitCode": info.ExitCode,
							"signal":   info.Signal,
						})
						_ = conn.Write(ctx, websocket.MessageText, exitMsg)
					})

					// Forward closing notification
					unsubClosing = attachedSession.OnClosing(func(reason string) {
						closeMsg, _ := json.Marshal(map[string]string{
							"type":   "closing",
							"reason": reason,
						})
						_ = conn.Write(ctx, websocket.MessageText, closeMsg)
						_ = conn.Close(websocket.StatusNormalClosure, reason)
					})

					continue

				case "resize":
					if attachedSession != nil && ctrl.Cols > 0 && ctrl.Rows > 0 && !attachedSession.IsDead() {
						_ = attachedSession.Resize(ctrl.Cols, ctrl.Rows)
					}
					continue

				case "kill":
					if attachedSession != nil {
						attachedSession.KillProcess()
					}
					continue
				}
			}
		}

		// Raw text input forwarded to session PTY
		if attachedSession != nil && !attachedSession.IsDead() {
			_ = attachedSession.WriteRaw(string(payload))
		}
	}
}

// LogFilePath returns the file path for daemon log writes.
func LogFilePath() string {
	if custom := os.Getenv("TUISTORY_LOG_FILE_PATH"); custom != "" {
		return custom
	}
	base := "/tmp"
	if os.Getenv("TMPDIR") != "" {
		base = os.Getenv("TMPDIR")
	}
	return filepath.Join(base, "tuistory", "relay-server.log")
}

// PidFilePath returns the pid file path for the given port.
func PidFilePath(port int) string {
	base := "/tmp"
	if os.Getenv("TMPDIR") != "" {
		base = os.Getenv("TMPDIR")
	}
	return filepath.Join(base, "tuistory", fmt.Sprintf("relay-%d.pid", port))
}

// RestartLockFilePath returns the lock file path for restarting the daemon.
func RestartLockFilePath(port int) string {
	base := "/tmp"
	if os.Getenv("TMPDIR") != "" {
		base = os.Getenv("TMPDIR")
	}
	return filepath.Join(base, "tuistory", fmt.Sprintf("restart-%d.lock", port))
}
