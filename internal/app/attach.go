package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/remorses/tuistory/internal/relay"
	"github.com/remorses/tuistory/internal/session"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const (
	doubleCtrlTimeout  = 450 * time.Millisecond
	attachPollInterval = 25
)

// RunAttach attaches interactively to a session over WebSocket.
func RunAttach(port int, targetSession string) error {
	// If no session specified, query sessions list to auto-select
	if targetSession == "" {
		sessions, err := fetchSessions(port)
		if err != nil {
			return fmt.Errorf("listing sessions: %w", err)
		}
		if len(sessions) == 0 {
			return fmt.Errorf("No sessions. Launch one first with: tuistory launch <command>")
		}
		var alive []relay.SessionInfo
		for _, s := range sessions {
			if !s.Dead {
				alive = append(alive, s)
			}
		}
		if len(alive) == 1 {
			targetSession = alive[0].Name
			fmt.Fprintf(os.Stderr, "Auto-selecting session: %s\n", targetSession)
		} else {
			targetSession = sessions[0].Name
			fmt.Fprintf(os.Stderr, "Selecting session: %s\n", targetSession)
		}
	}

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/attach", port)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Host: fmt.Sprintf("127.0.0.1:%d", port),
	})
	if err != nil {
		return fmt.Errorf("connecting to daemon WebSocket: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "detach")
	conn.SetReadLimit(session.MaxOutputBufferBytes)
	var workers sync.WaitGroup
	defer func() {
		cancel()
		_ = conn.CloseNow() // Interrupt a blocked WebSocket reader before joining workers.
		workers.Wait()
	}()

	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || cols <= 0 || rows <= 0 {
		cols = 120
		rows = 36
	}

	handshake, _ := json.Marshal(map[string]any{
		"type":    "attach",
		"session": targetSession,
		"cols":    cols,
		"rows":    rows,
	})
	if err := conn.Write(ctx, websocket.MessageText, handshake); err != nil {
		return fmt.Errorf("sending attach handshake: %w", err)
	}

	stdinFd := int(os.Stdin.Fd())
	if term.IsTerminal(stdinFd) {
		oldState, err := term.MakeRaw(stdinFd)
		if err == nil {
			defer func() { _ = term.Restore(stdinFd, oldState) }()
		}
	}

	// Watch for terminal resize
	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)
	defer signal.Stop(sigwinch)

	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-sigwinch:
				c, r, err := term.GetSize(int(os.Stdout.Fd()))
				if err == nil && c > 0 && r > 0 {
					resizeMsg, _ := json.Marshal(map[string]any{
						"type": "resize",
						"cols": c,
						"rows": r,
					})
					_ = conn.Write(ctx, websocket.MessageText, resizeMsg)
				}
			}
		}
	}()

	// WebSocket -> Stdout
	doneReading := make(chan struct{})
	workers.Add(1)
	go func() {
		defer workers.Done()
		defer close(doneReading)
		defer cancel()
		for {
			typ, r, err := conn.Reader(ctx)
			if err != nil {
				return
			}
			data, err := io.ReadAll(r)
			if err != nil {
				return
			}
			if typ == websocket.MessageText && len(data) > 0 && data[0] == '{' {
				var msg struct {
					Type     string `json:"type"`
					Message  string `json:"message"`
					ExitCode int    `json:"exitCode"`
					Signal   int    `json:"signal"`
					Reason   string `json:"reason"`
				}
				if err := json.Unmarshal(data, &msg); err == nil {
					if msg.Type == "error" {
						fmt.Fprintf(os.Stderr, "\r\nError: %s\r\n", msg.Message)
						cancel()
						return
					}
					if msg.Type == "closing" {
						cancel()
						return
					}
					if msg.Type == "exit" {
						// Process exited; continue streaming remaining output
						continue
					}
				}
			}
			_, _ = os.Stdout.Write(data)
		}
	}()

	// Stdin -> WebSocket with double Ctrl+C / Ctrl+X handling
	var input attachInput

	inBuf := make([]byte, 256)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-doneReading:
			return nil
		default:
		}

		if err := input.flushInterrupt(ctx, conn); err != nil {
			return nil
		}
		ready, err := pollInput(ctx, stdinFd)
		if err != nil {
			return nil
		}
		if !ready {
			continue
		}
		n, err := unix.Read(stdinFd, inBuf)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			break
		}
		if n == 0 {
			break
		}

		stop, err := input.forward(ctx, conn, inBuf[:n])
		if stop || err != nil {
			return nil
		}
	}

	return nil
}

// Input control state belongs to the stdin loop, including deferred interrupts.
type attachInput struct {
	lastCtrlC, lastCtrlX time.Time
	pendingCtrlC         bool
}

func (a *attachInput) flushInterrupt(ctx context.Context, conn *websocket.Conn) error {
	if !a.pendingCtrlC || time.Since(a.lastCtrlC) < doubleCtrlTimeout {
		return nil
	}
	a.pendingCtrlC = false
	return conn.Write(ctx, websocket.MessageBinary, []byte{0x03})
}

func (a *attachInput) forward(ctx context.Context, conn *websocket.Conn, data []byte) (bool, error) {
	start := 0
	for i, b := range data {
		if b != 0x03 && b != 0x18 {
			continue
		}
		if i > start {
			if err := conn.Write(ctx, websocket.MessageBinary, data[start:i]); err != nil {
				return false, err
			}
		}
		start = i + 1
		if b == 0x03 {
			if time.Since(a.lastCtrlC) < doubleCtrlTimeout {
				return true, nil
			}
			a.lastCtrlC = time.Now()
			a.pendingCtrlC = true
		} else {
			if time.Since(a.lastCtrlX) < doubleCtrlTimeout {
				return true, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"kill"}`))
			}
			a.lastCtrlX = time.Now()
		}
	}
	if start < len(data) {
		return false, conn.Write(ctx, websocket.MessageBinary, data[start:])
	}
	return false, nil
}

// pollInput uses readiness instead of an uncancellable stdin read. Stdin remains
// owned by the caller; cancellation never closes or changes its descriptor.
func pollInput(ctx context.Context, fd int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	_, err := unix.Poll(fds, attachPollInterval)
	if errors.Is(err, unix.EINTR) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if fds[0].Revents&unix.POLLNVAL != 0 {
		return false, unix.EBADF
	}
	return fds[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) != 0, nil
}

func fetchSessions(port int) ([]relay.SessionInfo, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/sessions", port))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var sessions []relay.SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}
