package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/remorses/tuistory/internal/relay"
)

const helperVersion = "1.2.3"

func TestMain(m *testing.M) {
	if os.Getenv("TUISTORY_CLIENT_TEST_HELPER") == "1" && os.Getenv("TUISTORY_RELAY") == "1" {
		os.Exit(serveClientTestRelay())
	}
	tmp, err := filepath.Abs(filepath.Join("..", "..", ".tmp", "client-tests"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(tmp, 0755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("TMPDIR", tmp); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// Re-execution gives SpawnRelayServer the same executable lifecycle it has in
// the CLI. The helper serves the real relay and owns its listening socket.
func serveClientTestRelay() int {
	port, err := strconv.Atoi(os.Getenv("TUISTORY_PORT"))
	if err != nil {
		return 1
	}
	version := os.Getenv("TUISTORY_CLIENT_TEST_VERSION")
	if version == "" {
		version = helperVersion
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return 1
	}
	handler := relay.NewServer(version, port, "").Handler()
	mode := os.Getenv("TUISTORY_CLIENT_TEST_MODE")
	if mode == "unresponsive" {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		})
	}
	server := &http.Server{Handler: handler}
	signals := make(chan os.Signal, 1)
	if mode == "ignore-term" {
		signal.Ignore(syscall.SIGTERM)
	} else {
		signal.Notify(signals, syscall.SIGTERM)
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = server.Shutdown(ctx) // Child teardown is also enforced by its owner.
		case <-done:
		}
	}()
	err = server.Serve(listener)
	close(done)
	signal.Stop(signals)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return 1
	}
	return 0
}

func reserveClientPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func isolateClientState(t *testing.T) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("TUISTORY_CLIENT_TEST_HELPER", "1")
	t.Setenv("TUISTORY_CLIENT_TEST_VERSION", helperVersion)
	t.Setenv("TUISTORY_CLIENT_TEST_MODE", "")
}

type clientRelayChild struct {
	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error
}

func startOwnedClientRelay(t *testing.T, port int, version, mode string) *clientRelayChild {
	t.Helper()
	cmd := exec.Command(os.Args[0], "relay-server")
	cmd.Env = append(os.Environ(), "TUISTORY_CLIENT_TEST_HELPER=1", "TUISTORY_RELAY=1", fmt.Sprintf("TUISTORY_PORT=%d", port), "TUISTORY_CLIENT_TEST_VERSION="+version, "TUISTORY_CLIENT_TEST_MODE="+mode)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	child := &clientRelayChild{cmd: cmd, done: make(chan struct{})}
	go func() { child.waitErr = cmd.Wait(); close(child.done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill() // Best effort: KillRelay may already have terminated it.
		select {
		case <-child.done:
		case <-time.After(5 * time.Second):
			t.Error("owned relay child did not exit")
		}
	})
	return child
}

func startClientRelayChild(t *testing.T, port int, version, mode string) *clientRelayChild {
	t.Helper()
	child := startOwnedClientRelay(t, port, version, mode)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status := ProbeRelay(port)
		if mode == "unresponsive" && status.Kind == StatusOccupiedUnresponsive {
			return child
		}
		if status.Kind == StatusHealthy && status.Version == version {
			return child
		}
		select {
		case <-child.done:
			t.Fatal("relay child exited before listening")
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("relay child did not listen")
	return nil
}

// SpawnRelayServer intentionally detaches its executable; locate and reap only
// the native child socket owners created by the current test.
func cleanupSpawnedClientRelay(t *testing.T, port int) {
	t.Helper()
	pids, err := findPIDsListeningOnPort(port)
	if err != nil {
		t.Error(err)
		return
	}
	if free, err := KillRelay(port); err != nil || !free {
		t.Errorf("cleanup relay: %v %v", free, err)
	}
	for _, pid := range pids {
		process, err := os.FindProcess(pid)
		if err != nil {
			t.Error(err)
			continue
		}
		_, _ = process.Wait() // Reap the detached direct child, if not already reaped.
	}
}

func TestProbeAndForwardResponses(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		status     int
		healthy    bool
	}{
		{"healthy", `{"version":"1.2.3","protocol":"tuistory-go/1"}`, 200, true},
		{"empty version", `{"version":""}`, 200, false},
		{"malformed", `not-json`, 200, false},
		{"http failure", `unavailable`, 503, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			status := ProbeRelay(server.Listener.Addr().(*net.TCPAddr).Port)
			if (status.Kind == StatusHealthy) != tt.healthy {
				t.Fatalf("probe: %+v", status)
			}
		})
	}
	t.Run("closed listener", func(t *testing.T) {
		port := reserveClientPort(t)
		if status := ProbeRelay(port); status.Kind != StatusNoListener {
			t.Fatal(status)
		}
		if _, err := ForwardCLI(port, relay.CLIRequest{}); err == nil || !strings.Contains(err.Error(), "connecting to relay") {
			t.Fatal(err)
		}
		if WaitForRelay(port, time.Millisecond, "") {
			t.Fatal("closed listener accepted")
		}
	})
	for _, tt := range []struct {
		name, response, wantError string
		status                    int
	}{
		{"success", `{"stdout":"returned","stderr":"diagnostic","exitCode":7}`, "", 200},
		{"http failure", `unavailable`, "status 503: unavailable", 503},
		{"bad json", `not-json`, "decoding relay response", 200},
	} {
		t.Run("forward "+tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/cli" || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("request: %s %s %v", r.Method, r.URL, r.Header)
				}
				var req relay.CLIRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Cwd != "/caller" || req.Env["VALUE"] != "one,two" || len(req.Argv) != 2 {
					t.Errorf("request body: %+v %v", req, err)
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.response)
			}))
			defer server.Close()
			res, err := ForwardCLI(server.Listener.Addr().(*net.TCPAddr).Port, relay.CLIRequest{Argv: []string{"tuistory", "sessions"}, Cwd: "/caller", Env: map[string]string{"VALUE": "one,two"}})
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || res.Stdout != "returned" || res.Stderr != "diagnostic" || res.ExitCode != 7 {
				t.Fatalf("response: %+v %v", res, err)
			}
		})
	}
}

func TestRestartLockOwnershipAndRecovery(t *testing.T) {
	isolateClientState(t)
	port := reserveClientPort(t)
	path := relay.RestartLockFilePath(port)
	if !acquireRestartLock(port) || acquireRestartLock(port) {
		t.Fatal("live lock was not exclusive")
	}
	releaseRestartLock(port)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned lock remained: %v", err)
	}
	for _, tt := range []struct {
		name, payload string
		want          bool
	}{
		{"expired", fmt.Sprintf("%d:%d", os.Getpid(), time.Now().Add(-time.Minute).UnixMilli()), true},
		{"malformed", "invalid", false},
		{"invalid pid", fmt.Sprintf("0:%d", time.Now().UnixMilli()), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tt.payload), 0600); err != nil {
				t.Fatal(err)
			}
			if got := acquireRestartLock(port); got != tt.want {
				t.Fatalf("lock acquired=%v want=%v", got, tt.want)
			}
			releaseRestartLock(port)
			if !tt.want {
				if data, err := os.ReadFile(path); err != nil || string(data) != tt.payload {
					t.Fatalf("unowned lock changed: %q %v", data, err)
				}
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		})
	}
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", blocked)
	if acquireRestartLock(port) {
		t.Fatal("lock acquired beneath a regular file")
	}
	releaseRestartLock(port)
}

func TestOwnedRelayRecoveryAndStartup(t *testing.T) {
	for _, mode := range []string{"", "ignore-term"} {
		t.Run("kill "+mode, func(t *testing.T) {
			isolateClientState(t)
			port := reserveClientPort(t)
			child := startClientRelayChild(t, port, helperVersion, mode)
			if free, err := KillRelay(port); err != nil || !free {
				t.Fatalf("KillRelay: %v %v", free, err)
			}
			if status := ProbeRelay(port); status.Kind != StatusNoListener {
				t.Fatal(status)
			}
			<-child.done
			if mode == "" && child.waitErr != nil {
				t.Fatalf("graceful relay exit: %v", child.waitErr)
			}
			if mode == "ignore-term" && child.waitErr == nil {
				t.Fatal("SIGTERM-resistant child was not forcefully killed")
			}
		})
	}
	t.Run("invalid recovery port", func(t *testing.T) {
		if free, err := KillRelay(-1); free || err == nil || !strings.Contains(err.Error(), "invalid port") {
			t.Fatalf("invalid port: %v %v", free, err)
		}
	})
	for _, mode := range []string{"absent", "old version", "unresponsive"} {
		t.Run("ensure "+mode, func(t *testing.T) {
			isolateClientState(t)
			port := reserveClientPort(t)
			if mode != "absent" {
				childMode := ""
				if mode == "unresponsive" {
					childMode = mode
				}
				startClientRelayChild(t, port, "0.0.0", childMode)
			}
			if err := EnsureRelayRunning(port, helperVersion); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { cleanupSpawnedClientRelay(t, port) })
			if !WaitForRelay(port, time.Second, "") {
				t.Fatal("new daemon protocol not compatible")
			}
			if err := EnsureRelayRunning(port, helperVersion); err != nil {
				t.Fatalf("reuse: %v", err)
			}
			if _, err := os.Stat(relay.RestartLockFilePath(port)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("restart lock retained: %v", err)
			}
		})
	}
}

func TestEnsureWaitsForLockOwner(t *testing.T) {
	isolateClientState(t)
	port := reserveClientPort(t)
	if !acquireRestartLock(port) {
		t.Fatal("setup lock failed")
	}
	defer releaseRestartLock(port)
	startOwnedClientRelay(t, port, helperVersion, "")
	if err := EnsureRelayRunning(port, helperVersion); err != nil {
		t.Fatal(err)
	}
	if !WaitForRelay(port, time.Second, helperVersion) {
		t.Fatal("lock owner's daemon not accepted")
	}
	data, err := os.ReadFile(relay.RestartLockFilePath(port))
	if err != nil || !strings.HasPrefix(string(data), fmt.Sprintf("%d:", os.Getpid())) {
		t.Fatalf("lock owner changed: %q %v", data, err)
	}
}

func TestEnsureReprobesAfterAcquiringLock(t *testing.T) {
	isolateClientState(t)
	server := httptest.NewUnstartedServer(nil)
	port := server.Listener.Addr().(*net.TCPAddr).Port
	old := relay.NewServer("0.0.0", port, "").Handler()
	current := relay.NewServer(helperVersion, port, "").Handler()
	var requests atomic.Int32
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			old.ServeHTTP(w, r)
		} else {
			current.ServeHTTP(w, r)
		}
	})
	server.Start()
	defer server.Close()
	if err := EnsureRelayRunning(port, helperVersion); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("expected two probes, got %d", requests.Load())
	}
	if _, err := os.Stat(relay.RestartLockFilePath(port)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restart lock retained: %v", err)
	}
}

func TestEnsureLockOwnerTimeout(t *testing.T) {
	isolateClientState(t)
	port := reserveClientPort(t)
	if !acquireRestartLock(port) {
		t.Fatal("setup lock failed")
	}
	defer releaseRestartLock(port)
	err := EnsureRelayRunning(port, helperVersion)
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for another client") {
		t.Fatalf("lock owner timeout: %v", err)
	}
}
