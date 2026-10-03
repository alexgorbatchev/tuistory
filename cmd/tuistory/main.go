package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/remorses/tuistory/internal/app"
	"github.com/remorses/tuistory/internal/client"
	"github.com/remorses/tuistory/internal/relay"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
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

func runClient(port int) {
	// 1. Version flag: `tuistory --version` or `tuistory -v`
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("tuistory/%s\n", version)
		return
	}

	// 2. Help flag on root: `tuistory --help` or `tuistory -h`
	if len(os.Args) == 2 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
		cmd := newRootCommand()
		_ = cmd.Help()
		return
	}

	// 3. Check for bare unknown commands before `--`
	// E.g.: `tuistory "printf hello" -s foo` without `launch` or `--`
	knownCommands := map[string]struct{}{
		"launch": {}, "snapshot": {}, "read": {}, "screenshot": {}, "type": {},
		"press": {}, "click": {}, "click-at": {}, "wait": {}, "wait-idle": {},
		"scroll": {}, "resize": {}, "capture-frames": {}, "close": {}, "restart": {},
		"sessions": {}, "logfile": {}, "daemon-stop": {}, "attach": {}, "skill": {},
		"help": {},
	}

	hasDash := slices.Contains(os.Args, "--")
	firstArg := ""
	for _, a := range os.Args[1:] {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			firstArg = a
			break
		}
	}

	if firstArg != "" && !hasDash {
		if _, ok := knownCommands[firstArg]; !ok {
			fmt.Fprintf(os.Stderr, "Unknown command: %s\n", firstArg)
			os.Exit(1)
		}
	}

	isLaunch := firstArg == "launch" || hasDash

	// 4. Passthrough mode (inside traforo, sigillo, or nested tuistory)
	shouldPassthrough := os.Getenv("TRAFORO_URL") != "" || os.Getenv("SIGILLO") != "" || os.Getenv("TUISTORY_SESSION") != ""
	if isLaunch && shouldPassthrough {
		launchCmd := extractLaunchCommand(os.Args)
		if launchCmd != "" {
			runPassthrough(launchCmd)
			return
		}
	}

	// 5. skill command (works offline without daemon)
	if firstArg == "skill" {
		fmt.Print(skillContent)
		return
	}

	// 6. daemon-stop command
	if firstArg == "daemon-stop" {
		status := client.ProbeRelay(port)
		if status.Kind == client.StatusNoListener {
			fmt.Println("No daemon running")
			return
		}
		freed, err := client.KillRelay(port)
		if err != nil || !freed {
			fmt.Fprintf(os.Stderr, "Failed to stop daemon: port %d is still in use\n", port)
			os.Exit(1)
		}
		fmt.Println("Daemon stopped")
		return
	}

	// 6. attach command
	if firstArg == "attach" {
		if err := client.EnsureRelayRunning(port, version); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", err)
			os.Exit(1)
		}
		sessionName := parseSessionFlag(os.Args[2:])
		if err := app.RunAttach(port, sessionName); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", err)
			os.Exit(1)
		}
		return
	}

	// 7. Standard client command forwarding
	if err := client.EnsureRelayRunning(port, version); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		printRelayLogTail()
		os.Exit(1)
	}

	cwd, _ := os.Getwd()
	envMap := make(map[string]string)
	for _, e := range os.Environ() {
		if idx := strings.Index(e, "="); idx > 0 {
			envMap[e[:idx]] = e[idx+1:]
		}
	}

	res, err := client.ForwardCLI(port, relay.CLIRequest{
		Argv: os.Args,
		Cwd:  cwd,
		Env:  envMap,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		printRelayLogTail()
		os.Exit(1)
	}

	if res.Stderr != "" {
		fmt.Fprintln(os.Stderr, res.Stderr)
	}

	if isLaunch && res.ExitCode == 0 {
		isBackground := slices.Contains(os.Args, "--background")
		isAgent := os.Getenv("AGENT") == "1" || os.Getenv("AI_AGENT") != ""
		shouldAttach := term.IsTerminal(int(os.Stdout.Fd())) && !isAgent && !isBackground
		if shouldAttach {
			sessionName := parseSessionFlag(os.Args[1:])
			if sessionName == "" {
				launchCmd := extractLaunchCommand(os.Args)
				sessionName = client.GetDefaultSessionName(launchCmd, cwd)
			}
			_ = app.RunAttach(port, sessionName)
			os.Exit(0)
		}
		if res.Stdout != "" {
			fmt.Println(res.Stdout)
		}
		os.Exit(0)
	}

	if res.Stdout != "" {
		fmt.Println(res.Stdout)
	}
	os.Exit(res.ExitCode)
}

func extractLaunchCommand(args []string) string {
	if idx := slices.Index(args, "--"); idx != -1 && idx+1 < len(args) {
		return strings.Join(args[idx+1:], " ")
	}
	launchIdx := slices.Index(args, "launch")
	if launchIdx != -1 {
		for _, a := range args[launchIdx+1:] {
			if a == "--" {
				break
			}
			if !strings.HasPrefix(a, "-") {
				return a
			}
		}
	}
	return ""
}

func parseSessionFlag(args []string) string {
	for i, a := range args {
		if a == "-s" || a == "--session" {
			if i+1 < len(args) {
				return args[i+1]
			}
		}
		if strings.HasPrefix(a, "--session=") {
			return strings.TrimPrefix(a, "--session=")
		}
		if strings.HasPrefix(a, "-s=") {
			return strings.TrimPrefix(a, "-s=")
		}
	}
	return ""
}

func runPassthrough(launchCmd string) {
	cmd := exec.Command("sh", "-c", launchCmd)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	isNested := os.Getenv("TUISTORY_SESSION") != ""
	if !isNested {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to spawn command: %v\n", err)
		os.Exit(1)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		for sig := range sigChan {
			if !isNested && cmd.Process != nil {
				_ = unix.Kill(-cmd.Process.Pid, sig.(syscall.Signal))
			} else if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

	err := cmd.Wait()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
	os.Exit(0)
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
