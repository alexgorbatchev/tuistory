package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/remorses/tuistory/internal/app"
	"github.com/remorses/tuistory/internal/relay"
)

func getPort() int {
	if p := os.Getenv("TUISTORY_PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			return n
		}
	}
	return 19977
}

func main() {
	port := getPort()

	isRelay := os.Getenv("TUISTORY_RELAY") == "1" || (len(os.Args) > 1 && os.Args[1] == "relay-server")
	if isRelay {
		runRelayServer(port)
		return
	}

	runClient(port)
}

func runRelayServer(port int) {
	home, err := os.UserHomeDir()
	if err == nil {
		_ = os.Chdir(home)
	}

	logFile := relay.LogFilePath()
	_ = os.MkdirAll(filepath.Dir(logFile), 0755)
	logF, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logF = os.Stderr
	}
	defer logF.Close()

	logger := slog.New(slog.NewTextHandler(logF, &slog.HandlerOptions{}))

	srv := relay.NewServer(version, port, home)
	srv.SetCLIRunner(func(req relay.CLIRequest, reg *relay.SessionRegistry, l *slog.Logger) relay.CLIResult {
		args := req.Argv
		if len(args) > 1 {
			args = args[1:]
		} else {
			args = nil
		}
		return app.ExecuteCommand(args, reg, req.Cwd, req.Env)
	})

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		var sysErr syscall.Errno
		if errors.As(err, &sysErr) && sysErr == syscall.EADDRINUSE {
			logger.Info("Port already in use, exiting cleanly", "port", port)
			os.Exit(0)
		}
		logger.Error("Listen error", "error", err)
		os.Exit(1)
	}

	pidFile := relay.PidFilePath(port)
	_ = os.MkdirAll(filepath.Dir(pidFile), 0755)
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644)

	logger.Info("Relay server started", "port", port, "pid", os.Getpid(), "version", version)

	httpServer := &http.Server{
		Handler: srv.Handler(),
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("Shutting down relay server", "signal", sig.String())
		srv.Sessions().CloseAll("daemon-shutdown")

		// Remove PID file if still owned
		data, err := os.ReadFile(pidFile)
		if err == nil && strings.TrimSpace(string(data)) == strconv.Itoa(os.Getpid()) {
			_ = os.Remove(pidFile)
		}

		_ = httpServer.Close()
		os.Exit(0)
	}()

	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("Server error", "error", err)
		os.Exit(1)
	}
}

func printRelayLogTail() {
	data, err := os.ReadFile(relay.LogFilePath())
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	tail := lines
	if len(lines) > 15 {
		tail = lines[len(lines)-15:]
	}
	if len(tail) > 0 {
		fmt.Fprintf(os.Stderr, "\n--- Last %d lines from %s ---\n", len(tail), relay.LogFilePath())
		for _, l := range tail {
			fmt.Fprintln(os.Stderr, l)
		}
		fmt.Fprintln(os.Stderr, "--- end of log ---")
	}
}
