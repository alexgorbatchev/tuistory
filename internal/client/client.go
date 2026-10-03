package client

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/remorses/tuistory/internal/relay"
	"golang.org/x/sys/unix"
)

// RelayStatusKind classifies the state of the relay port.
type RelayStatusKind string

const (
	StatusHealthy              RelayStatusKind = "healthy"
	StatusNoListener           RelayStatusKind = "no-listener"
	StatusOccupiedUnresponsive RelayStatusKind = "occupied-unresponsive"
	restartLockStaleDuration                   = 10 * time.Second
)

// RelayStatus holds the probe result of the relay port.
type RelayStatus struct {
	Kind    RelayStatusKind
	Version string
}

// CompareVersions compares two semver strings: -1 if v1 < v2, 0 if v1 == v2, 1 if v1 > v2.
func CompareVersions(v1, v2 string) int {
	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")
	maxLen := max(len(parts1), len(parts2))

	for i := 0; i < maxLen; i++ {
		var n1, n2 int
		if i < len(parts1) {
			n1, _ = strconv.Atoi(parts1[i])
		}
		if i < len(parts2) {
			n2, _ = strconv.Atoi(parts2[i])
		}
		if n1 != n2 {
			if n1 < n2 {
				return -1
			}
			return 1
		}
	}
	return 0
}

var nonAlphaNumRegex = regexp.MustCompile(`[^a-z0-9]+`)
var multiHyphenRegex = regexp.MustCompile(`-{2,}`)

// GetDefaultSessionName computes `<cwd-basename>-<cwd-hash>-<command>` in kebab-case.
func GetDefaultSessionName(command, cwd string) string {
	base := filepath.Base(cwd)
	h := sha256.Sum256([]byte(cwd))
	hash := hex.EncodeToString(h[:])[:4]
	raw := fmt.Sprintf("%s-%s-%s", base, hash, command)

	s := strings.ToLower(raw)
	s = nonAlphaNumRegex.ReplaceAllString(s, "-")
	s = multiHyphenRegex.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// ProbeRelay probes the relay port to classify whether a healthy daemon is running.
func ProbeRelay(port int) RelayStatus {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/version", port))
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		var data struct {
			Version string `json:"version"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&data); err == nil && data.Version != "" {
			return RelayStatus{Kind: StatusHealthy, Version: data.Version}
		}
	}

	if isPortOccupied(port) {
		return RelayStatus{Kind: StatusOccupiedUnresponsive}
	}
	return RelayStatus{Kind: StatusNoListener}
}

func isPortOccupied(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return true
	}
	// Also test trying to bind the port
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return true
	}
	_ = l.Close()
	return false
}

func readPidFile(port int) (int, error) {
	data, err := os.ReadFile(relay.PidFilePath(port))
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid pid in pidfile: %s", string(data))
	}
	return n, nil
}

// KillRelay terminates whatever process owns the relay port.
func KillRelay(port int) (bool, error) {
	pid, _ := readPidFile(port)
	if pid > 0 {
		_ = unix.Kill(pid, unix.SIGTERM)
		start := time.Now()
		for time.Since(start) < 3*time.Second {
			if err := unix.Kill(pid, 0); errors.Is(err, unix.ESRCH) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		_ = unix.Kill(pid, unix.SIGKILL)
	}

	// Port-based sweep to catch orphans
	if isPortOccupied(port) {
		killProcessOnPort(port)
	}

	// Wait for port free
	start := time.Now()
	for time.Since(start) < 5*time.Second {
		if !isPortOccupied(port) {
			_ = os.Remove(relay.PidFilePath(port))
			return true, nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	_ = os.Remove(relay.PidFilePath(port))
	return !isPortOccupied(port), nil
}

// killProcessOnPort searches /proc on Linux for the socket inode and kills the owning PID.
func killProcessOnPort(port int) {
	pids, err := findPIDsListeningOnPort(port)
	if err == nil {
		for _, p := range pids {
			_ = unix.Kill(p, unix.SIGKILL)
		}
	}
}

func findPIDsListeningOnPort(targetPort int) ([]int, error) {
	tcpData, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return nil, err
	}

	portHex := fmt.Sprintf("%04X", targetPort)
	var inodes []string
	lines := strings.Split(string(tcpData), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 10 {
			localAddr := fields[1]
			parts := strings.Split(localAddr, ":")
			if len(parts) == 2 && strings.EqualFold(parts[1], portHex) {
				state := fields[3]
				if state == "0A" { // 0A is LISTEN state
					inodes = append(inodes, fields[9])
				}
			}
		}
	}

	if len(inodes) == 0 {
		return nil, nil
	}

	var matchedPIDs []int
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}

		fdDir := filepath.Join("/proc", entry.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}

		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			for _, inode := range inodes {
				if strings.Contains(link, fmt.Sprintf("[%s]", inode)) {
					matchedPIDs = append(matchedPIDs, pid)
					break
				}
			}
		}
	}

	return matchedPIDs, nil
}

func acquireRestartLock(port int) bool {
	lockFile := relay.RestartLockFilePath(port)
	_ = os.MkdirAll(filepath.Dir(lockFile), 0755)

	payload := fmt.Sprintf("%d:%d", os.Getpid(), time.Now().UnixMilli())
	f, err := os.OpenFile(lockFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err == nil {
		_, _ = f.WriteString(payload)
		_ = f.Close()
		return true
	}

	// Check if existing lock is stale
	data, err := os.ReadFile(lockFile)
	if err == nil {
		parts := strings.Split(strings.TrimSpace(string(data)), ":")
		if len(parts) == 2 {
			lockPid, _ := strconv.Atoi(parts[0])
			lockTime, _ := strconv.ParseInt(parts[1], 10, 64)
			isStale := time.Since(time.UnixMilli(lockTime)) > restartLockStaleDuration
			if !isStale && lockPid > 0 {
				if err := unix.Kill(lockPid, 0); errors.Is(err, unix.ESRCH) {
					isStale = true
				}
			}

			if isStale {
				_ = os.Remove(lockFile)
				f2, err := os.OpenFile(lockFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
				if err == nil {
					_, _ = f2.WriteString(payload)
					_ = f2.Close()
					return true
				}
			}
		}
	}

	return false
}

func releaseRestartLock(port int) {
	lockFile := relay.RestartLockFilePath(port)
	data, err := os.ReadFile(lockFile)
	if err != nil {
		return
	}
	parts := strings.Split(strings.TrimSpace(string(data)), ":")
	if len(parts) > 0 {
		pid, _ := strconv.Atoi(parts[0])
		if pid == os.Getpid() {
			_ = os.Remove(lockFile)
		}
	}
}

// WaitForRelay polls until the relay is answering with version >= minVersion.
func WaitForRelay(port int, timeout time.Duration, minVersion string) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status := ProbeRelay(port)
		if status.Kind == StatusHealthy {
			if minVersion == "" || CompareVersions(status.Version, minVersion) >= 0 {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// SpawnRelayServer launches the background daemon detached.
func SpawnRelayServer(port int) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("getting executable path: %w", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/tmp"
	}

	cmd := exec.Command(execPath, "relay-server")
	cmd.Dir = homeDir
	cmd.Env = append(os.Environ(), "TUISTORY_RELAY=1", fmt.Sprintf("TUISTORY_PORT=%d", port))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	return cmd.Start()
}

// EnsureRelayRunning ensures a healthy daemon of at least our version is running.
func EnsureRelayRunning(port int, version string) error {
	status := ProbeRelay(port)
	if status.Kind == StatusHealthy && CompareVersions(status.Version, version) >= 0 {
		return nil
	}

	if !acquireRestartLock(port) {
		if WaitForRelay(port, 15*time.Second, version) {
			return nil
		}
		return fmt.Errorf("timed out waiting for another client to start relay v%s", version)
	}
	defer releaseRestartLock(port)

	// Re-probe under lock
	current := ProbeRelay(port)
	if current.Kind == StatusHealthy && CompareVersions(current.Version, version) >= 0 {
		return nil
	}

	if current.Kind == StatusHealthy {
		fmt.Fprintf(os.Stderr, "Relay server version mismatch (server: %s, client: %s), restarting...\n", current.Version, version)
	} else if current.Kind == StatusOccupiedUnresponsive {
		fmt.Fprintf(os.Stderr, "Relay port %d is occupied but not answering; replacing wedged daemon...\n", port)
	}

	if current.Kind != StatusNoListener {
		freed, err := KillRelay(port)
		if err != nil || !freed {
			return fmt.Errorf("port %d still in use after killing old daemon: %w", port, err)
		}
	}

	if err := SpawnRelayServer(port); err != nil {
		return fmt.Errorf("spawning relay daemon: %w", err)
	}

	if !WaitForRelay(port, 5*time.Second, version) {
		return fmt.Errorf("timed out waiting for relay v%s on port %d", version, port)
	}

	return nil
}

// ForwardCLI forwards an invocation request to the relay daemon over POST /cli.
func ForwardCLI(port int, req relay.CLIRequest) (relay.CLIResult, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return relay.CLIResult{}, err
	}

	client := &http.Client{Timeout: 0}
	resp, err := client.Post(
		fmt.Sprintf("http://127.0.0.1:%d/cli", port),
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		return relay.CLIResult{}, fmt.Errorf("connecting to relay on port %d: %w", port, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return relay.CLIResult{}, fmt.Errorf("relay returned status %d: %s", resp.StatusCode, string(body))
	}

	var res relay.CLIResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return relay.CLIResult{}, fmt.Errorf("decoding relay response: %w", err)
	}

	return res, nil
}
