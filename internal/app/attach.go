package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/remorses/tuistory/internal/relay"
	"golang.org/x/term"
)

const doubleCtrlTimeout = 450 * time.Millisecond

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

	go func() {
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
	go func() {
		defer close(doneReading)
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
	var (
		lastCtrlC time.Time
		lastCtrlX time.Time
	)

	inBuf := make([]byte, 256)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-doneReading:
			return nil
		default:
		}

		n, err := os.Stdin.Read(inBuf)
		if err != nil {
			break
		}
		if n == 0 {
			continue
		}

		data := inBuf[:n]
		if n == 1 && data[0] == 0x03 { // Ctrl+C
			if time.Since(lastCtrlC) < doubleCtrlTimeout {
				// Double Ctrl+C: detach
				return nil
			}
			lastCtrlC = time.Now()
			// Send first Ctrl+C after delay if no second Ctrl+C comes
			time.AfterFunc(doubleCtrlTimeout, func() {
				if time.Since(lastCtrlC) >= doubleCtrlTimeout {
					_ = conn.Write(ctx, websocket.MessageText, []byte{0x03})
				}
			})
			continue
		}

		if n == 1 && data[0] == 0x18 { // Ctrl+X
			if time.Since(lastCtrlX) < doubleCtrlTimeout {
				// Double Ctrl+X: kill process and detach
				killMsg, _ := json.Marshal(map[string]string{"type": "kill"})
				_ = conn.Write(ctx, websocket.MessageText, killMsg)
				time.Sleep(100 * time.Millisecond)
				return nil
			}
			lastCtrlX = time.Now()
			continue
		}

		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			break
		}
	}

	return nil
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
