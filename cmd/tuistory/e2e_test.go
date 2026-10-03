package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/remorses/tuistory/internal/client"
)

var testPort int

var binaryPath string

func TestMain(m *testing.M) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testPort = listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Build an instrumented test binary in the project's ignored bin directory.
	projectTmp := filepath.Join("..", "..", ".tmp")
	if err := os.MkdirAll(projectTmp, 0755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	absTmp, err := filepath.Abs(projectTmp)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("TMPDIR", absTmp); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	projectBin := filepath.Join("..", "..", "bin")
	if err := os.MkdirAll(projectBin, 0755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	absBinaryPath, err := filepath.Abs(filepath.Join(projectBin, "tuistory-e2e"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get abs path: %v\n", err)
		os.Exit(1)
	}
	binaryPath = absBinaryPath
	coverDir, err := filepath.Abs(filepath.Join(projectTmp, "e2e-coverage"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.RemoveAll(coverDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(coverDir, 0755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("GOCOVERDIR", coverDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	buildCmd := exec.Command("go", "build", "-cover", "-covermode=atomic", "-coverpkg=github.com/remorses/tuistory/...", "-o", binaryPath, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build test binary: %v\noutput: %s\n", err, string(out))
		os.Exit(1)
	}

	// Clean up any stale test daemon before tests
	killTestDaemon(testPort)

	code := m.Run()

	// Clean up test daemon after tests
	killTestDaemon(testPort)
	if err := os.Remove(binaryPath); err != nil {
		fmt.Fprintf(os.Stderr, "removing test binary: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

func TestE2E_MigrationCLIRegressions(t *testing.T) {
	t.Run("argv fidelity", func(t *testing.T) {
		for _, nested := range []bool{false, true} {
			env := []string{}
			if nested {
				env = append(env, "TUISTORY_SESSION=outer")
			}
			out, errOut, code := runCLIWithEnv(env, "", "-s", "argv-regression", "--", "printf", "%s", "one two;$HOME")
			if code != 0 {
				t.Fatalf("argv: %q %q %d", out, errOut, code)
			}
			if !nested {
				out, errOut, code = runCLI("read", "-s", "argv-regression", "--all")
				runCLI("close", "-s", "argv-regression")
			}
			if !strings.Contains(out, "one two;$HOME") {
				t.Fatalf("argv nested=%v: %q %q %d", nested, out, errOut, code)
			}
		}
	})
	t.Run("session prefix", func(t *testing.T) {
		out, errOut, code := runCLI("-s", "prefix-regression", "--no-wait", "--", "cat")
		if code != 0 {
			t.Fatalf("launch: %s %s", out, errOut)
		}
		defer runCLI("close", "-s", "prefix-regression")
		_, errOut, code = runCLI("-s", "prefix-regression", "snapshot", "--trim", "--immediate")
		if code != 0 {
			t.Fatalf("prefix snapshot: %s", errOut)
		}
	})
	t.Run("nested cwd and environment", func(t *testing.T) {
		cwd := filepath.Join("..", "..", ".tmp")
		abs, err := filepath.Abs(cwd)
		if err != nil {
			t.Fatal(err)
		}
		out, errOut, code := runCLIWithEnv([]string{"TUISTORY_SESSION=outer"}, "", "--cwd", abs, "--env", "MIGRATION_VALUE=one,two", "--", "sh", "-c", "printf '%s|%s' \"$PWD\" \"$MIGRATION_VALUE\"")
		if code != 0 || !strings.Contains(out, abs+"|one,two") {
			t.Fatalf("nested: %q %q %d", out, errOut, code)
		}
	})
	t.Run("comma environment", func(t *testing.T) {
		out, errOut, code := runCLI("-s", "comma-regression", "--env", "MIGRATION_VALUE=one,two", "--", "sh", "-c", "printf '%s' \"$MIGRATION_VALUE\"")
		if code != 0 {
			t.Fatalf("launch: %s %s", out, errOut)
		}
		defer runCLI("close", "-s", "comma-regression")
		out, errOut, code = runCLI("read", "-s", "comma-regression", "--all")
		if code != 0 || !strings.Contains(out, "one,two") {
			t.Fatalf("comma: %q %q %d", out, errOut, code)
		}
	})
	t.Run("skill rejects args", func(t *testing.T) {
		_, _, code := runCLI("skill", "extra")
		if code == 0 {
			t.Fatal("skill accepted extra argument")
		}
	})
	t.Run("raw version", func(t *testing.T) {
		out, errOut, code := runCLI("--version")
		if code != 0 || out != version {
			t.Fatalf("version: %q %q %d", out, errOut, code)
		}
	})
	t.Run("offline help paths", func(t *testing.T) {
		for _, args := range [][]string{{"snapshot", "--help"}, {"help", "snapshot"}, {"completion", "bash", "--help"}, {"skill", "--help"}} {
			out, errOut, code := runCLIWithEnv([]string{"AGENT=yes", "TUISTORY_PORT=1"}, "", args...)
			if code != 0 || !strings.HasPrefix(out, "ALERT: Agents must read") {
				t.Fatalf("help %v: %q %q %d", args, out, errOut, code)
			}
		}
	})
}

func killTestDaemon(port int) {
	_, _ = client.KillRelay(port)
}

func runCLI(args ...string) (string, string, int) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.Command(binaryPath, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), fmt.Sprintf("TUISTORY_PORT=%d", testPort), "AGENT=0")

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if ok := errorsAsExitError(err, &exitErr); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), exitCode
}

func runCLIWithEnv(env []string, cwd string, args ...string) (string, string, int) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.Command(binaryPath, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = cwd
	cmdEnv := append(os.Environ(), fmt.Sprintf("TUISTORY_PORT=%d", testPort), "AGENT=0")
	cmdEnv = append(cmdEnv, env...)
	cmd.Env = cmdEnv

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if ok := errorsAsExitError(err, &exitErr); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), exitCode
}

func errorsAsExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

func TestE2E_BasicWorkflow(t *testing.T) {
	// Launch
	out, errOut, code := runCLI("launch", "bash --norc --noprofile", "-s", "e2e-basic", "--env", "PS1=$ ")
	if code != 0 {
		t.Fatalf("launch failed (%d): %s %s", code, out, errOut)
	}
	if !strings.Contains(out, `Session "e2e-basic" started`) {
		t.Fatalf("expected launch started, got %q", out)
	}

	// Type
	out, errOut, code = runCLI("type", "echo hello-e2e", "-s", "e2e-basic")
	if code != 0 {
		t.Fatalf("type failed: %s %s", out, errOut)
	}
	if out != "OK" {
		t.Fatalf("expected OK, got %q", out)
	}

	// Press
	out, errOut, code = runCLI("press", "enter", "-s", "e2e-basic")
	if code != 0 {
		t.Fatalf("press failed: %s %s", out, errOut)
	}
	if out != "OK" {
		t.Fatalf("expected OK, got %q", out)
	}

	// Wait
	out, errOut, code = runCLI("wait", "hello-e2e", "-s", "e2e-basic", "--timeout", "5000")
	if code != 0 {
		t.Fatalf("wait failed: %s %s", out, errOut)
	}

	// Snapshot
	out, errOut, code = runCLI("snapshot", "-s", "e2e-basic", "--trim")
	if code != 0 {
		t.Fatalf("snapshot failed: %s %s", out, errOut)
	}
	if !strings.Contains(out, "echo hello-e2e") || !strings.Contains(out, "hello-e2e") {
		t.Fatalf("expected snapshot to contain output, got: %q", out)
	}

	// Close
	out, errOut, code = runCLI("close", "-s", "e2e-basic")
	if code != 0 {
		t.Fatalf("close failed: %s %s", out, errOut)
	}
	if !strings.Contains(out, `Session "e2e-basic" closed`) {
		t.Fatalf("expected close confirmation, got %q", out)
	}
}

func TestE2E_RelativeCwd(t *testing.T) {
	tmpDir := t.TempDir()
	childDir := filepath.Join(tmpDir, "child")
	_ = os.MkdirAll(childDir, 0755)

	out, errOut, code := runCLIWithEnv(nil, tmpDir, "launch", "pwd", "-s", "rel-cwd-test", "--cwd", "child")
	if code != 0 {
		t.Fatalf("launch failed: %s %s", out, errOut)
	}

	out, _, code = runCLI("read", "-s", "rel-cwd-test", "--all", "--trim")
	if code != 0 || !strings.Contains(out, childDir) {
		t.Fatalf("expected output to contain %q, got: %q", childDir, out)
	}

	runCLI("close", "-s", "rel-cwd-test")
}

func TestE2E_DefaultSessionName(t *testing.T) {
	out, errOut, code := runCLI("launch", "printf hello-def")
	if code != 0 {
		t.Fatalf("launch failed: %s %s", out, errOut)
	}

	// Verify name has format: <basename>-<hash>-printf-hello-def
	if !strings.Contains(out, "printf-hello-def") {
		t.Fatalf("expected session name to contain printf-hello-def, got: %q", out)
	}

	var sessionName string
	for _, word := range strings.Fields(out) {
		trimmed := strings.Trim(word, `"'`)
		if strings.Contains(trimmed, "printf-hello-def") {
			sessionName = trimmed
			break
		}
	}
	if sessionName == "" {
		t.Fatalf("could not extract session name from %q", out)
	}

	out, _, code = runCLI("read", "-s", sessionName, "--all", "--trim")
	if code != 0 || !strings.Contains(out, "hello-def") {
		t.Fatalf("expected output to contain hello-def, got: %q", out)
	}

	runCLI("close", "-s", sessionName)
}

func TestE2E_BarePositionalArgRejected(t *testing.T) {
	out, errOut, code := runCLI("printf hello", "-s", "bare-test")
	if code == 0 {
		t.Fatalf("expected bare positional arg to fail, got code 0: %s %s", out, errOut)
	}
	if !strings.Contains(errOut, "Unknown command") {
		t.Fatalf("expected Unknown command in stderr, got: %q", errOut)
	}
}

func TestE2E_DashPassthrough(t *testing.T) {
	out, errOut, code := runCLI("launch", "-s", "dash-e2e", "--", "printf", "hello")
	if code != 0 {
		t.Fatalf("launch failed: %s %s", out, errOut)
	}

	out, _, code = runCLI("read", "-s", "dash-e2e", "--all", "--trim")
	if code != 0 || !strings.Contains(out, "hello") {
		t.Fatalf("expected output to contain hello, got: %q", out)
	}

	runCLI("close", "-s", "dash-e2e")
}

func TestE2E_RootDashPassthrough(t *testing.T) {
	out, errOut, code := runCLI("-s", "root-dash", "--background", "--", "printf", "hello-root-dash")
	if code != 0 {
		t.Fatalf("launch failed: %s %s", out, errOut)
	}

	out, _, code = runCLI("read", "-s", "root-dash", "--all", "--trim")
	if code != 0 || !strings.Contains(out, "hello-root-dash") {
		t.Fatalf("expected output to contain hello-root-dash, got: %q", out)
	}

	runCLI("close", "-s", "root-dash")
}

func TestE2E_ConcurrentSessions(t *testing.T) {
	runCLI("launch", "bash --norc --noprofile", "-s", "sess-1", "--env", "PS1=A> ")
	runCLI("launch", "bash --norc --noprofile", "-s", "sess-2", "--env", "PS1=B> ")

	runCLI("type", "echo AAA-UNIQUE", "-s", "sess-1")
	runCLI("press", "enter", "-s", "sess-1")
	runCLI("wait", "AAA-UNIQUE", "-s", "sess-1", "--timeout", "5000")

	runCLI("type", "echo BBB-UNIQUE", "-s", "sess-2")
	runCLI("press", "enter", "-s", "sess-2")
	runCLI("wait", "BBB-UNIQUE", "-s", "sess-2", "--timeout", "5000")

	snap1, _, _ := runCLI("snapshot", "-s", "sess-1", "--trim")
	if !strings.Contains(snap1, "AAA-UNIQUE") || strings.Contains(snap1, "BBB-UNIQUE") {
		t.Fatalf("isolation failure in sess-1: %s", snap1)
	}

	snap2, _, _ := runCLI("snapshot", "-s", "sess-2", "--trim")
	if !strings.Contains(snap2, "BBB-UNIQUE") || strings.Contains(snap2, "AAA-UNIQUE") {
		t.Fatalf("isolation failure in sess-2: %s", snap2)
	}

	runCLI("close", "-s", "sess-1")
	runCLI("close", "-s", "sess-2")
}

func TestE2E_DuplicateSessionReuse(t *testing.T) {
	runCLI("launch", "bash --norc --noprofile", "-s", "dup-test", "--env", "PS1=$ ")
	out, _, code := runCLI("launch", "bash --norc --noprofile", "-s", "dup-test")
	if code != 0 {
		t.Fatalf("duplicate launch failed: %s", out)
	}
	if !strings.Contains(out, `Session "dup-test" already running`) {
		t.Fatalf("expected already running message, got: %q", out)
	}
	runCLI("close", "-s", "dup-test")
}

func TestE2E_ErrorHandling(t *testing.T) {
	// Close nonexistent
	_, errOut, code := runCLI("close", "-s", "ghost-session")
	if code == 0 || !strings.Contains(errOut, "not found") {
		t.Fatalf("expected not found for nonexistent session, got code %d: %s", code, errOut)
	}

	// Snapshot without -s
	_, errOut, code = runCLI("snapshot")
	if code == 0 || !strings.Contains(errOut, "-s/--session is required") {
		t.Fatalf("expected session required, got code %d: %s", code, errOut)
	}

	// Press invalid key
	runCLI("launch", "cat", "-s", "invalid-key-s", "--no-wait")
	_, errOut, code = runCLI("press", "boguskey", "-s", "invalid-key-s")
	if code == 0 || !strings.Contains(errOut, "Invalid key(s): boguskey") {
		t.Fatalf("expected Invalid key error, got code %d: %s", code, errOut)
	}
	runCLI("close", "-s", "invalid-key-s")
}

func TestE2E_NestedPassthrough(t *testing.T) {
	out, errOut, code := runCLIWithEnv([]string{"TUISTORY_SESSION=outer-active"}, "", "launch", "echo nested-ok", "-s", "nested-run")
	if code != 0 {
		t.Fatalf("passthrough failed: %s %s", out, errOut)
	}
	if !strings.Contains(out, "nested-ok") {
		t.Fatalf("expected output to contain nested-ok, got: %q", out)
	}

	// Verify no session was created on daemon
	sessionsOut, _, _ := runCLI("sessions")
	if strings.Contains(sessionsOut, "nested-run") {
		t.Fatalf("expected nested-run NOT to be in daemon sessions, got: %s", sessionsOut)
	}
}

func TestE2E_CallerEnvInherited(t *testing.T) {
	out, _, code := runCLIWithEnv([]string{"CUSTOM_INHERIT=hello-inherited-env"}, "", "launch", `printf "$CUSTOM_INHERIT"`, "-s", "env-inherit-test")
	if code != 0 {
		t.Fatalf("launch failed: %s", out)
	}

	readOut, _, _ := runCLI("read", "-s", "env-inherit-test", "--all", "--trim")
	if !strings.Contains(readOut, "hello-inherited-env") {
		t.Fatalf("expected output to contain hello-inherited-env, got: %q", readOut)
	}

	runCLI("close", "-s", "env-inherit-test")
}

func TestE2E_DaemonStop(t *testing.T) {
	runCLI("launch", "sleep 60", "-s", "stop-test", "--no-wait")
	out, _, code := runCLI("daemon-stop")
	if code != 0 || !strings.Contains(out, "Daemon stopped") {
		t.Fatalf("daemon-stop failed (%d): %q", code, out)
	}

	sessOut, _, code := runCLI("sessions")
	if code != 0 || !strings.Contains(sessOut, "No active sessions") {
		t.Fatalf("expected No active sessions after stop, got: %s", sessOut)
	}
}

func TestE2E_RegexWait(t *testing.T) {
	runCLI("launch", "bash --norc --noprofile", "-s", "regex-s", "--env", "PS1=$ ")
	runCLI("type", "echo 'value: 42'", "-s", "regex-s")
	runCLI("press", "enter", "-s", "regex-s")

	out, _, code := runCLI("wait", "/value: \\d+/", "-s", "regex-s", "--timeout", "5000")
	if code != 0 || !strings.Contains(out, "value: 42") {
		t.Fatalf("wait regex failed (%d): %s", code, out)
	}

	runCLI("close", "-s", "regex-s")
}

func TestE2E_WaitExitsEarlyOnCrash(t *testing.T) {
	runCLI("launch", `bash -c "echo crashing-e2e && exit 1"`, "-s", "crash-wait-s")
	_, errOut, code := runCLI("wait", "/never-matches/", "-s", "crash-wait-s", "--timeout", "10000")
	if code == 0 {
		t.Fatalf("expected non-zero exit for crash")
	}
	if !strings.Contains(errOut, "Process exited with code 1") {
		t.Fatalf("expected exit code in error, got: %s", errOut)
	}
	if !strings.Contains(errOut, "crashing-e2e") {
		t.Fatalf("expected last output in error, got: %s", errOut)
	}
	runCLI("close", "-s", "crash-wait-s")
}

func TestE2E_ReadCommands(t *testing.T) {
	runCLI("launch", "bash --norc --noprofile", "-s", "read-cmd-s", "--env", "PS1=$ ")

	runCLI("type", "echo part-one", "-s", "read-cmd-s")
	runCLI("press", "enter", "-s", "read-cmd-s")
	runCLI("wait", "part-one", "-s", "read-cmd-s")

	read1, _, _ := runCLI("read", "-s", "read-cmd-s")
	if !strings.Contains(read1, "part-one") {
		t.Fatalf("expected part-one in read1: %s", read1)
	}

	readEmpty, _, _ := runCLI("read", "-s", "read-cmd-s")
	if readEmpty != "" {
		t.Fatalf("expected empty read on second call: %s", readEmpty)
	}

	runCLI("type", "echo part-two", "-s", "read-cmd-s")
	runCLI("press", "enter", "-s", "read-cmd-s")
	runCLI("wait", "part-two", "-s", "read-cmd-s")

	read2, _, _ := runCLI("read", "-s", "read-cmd-s")
	if !strings.Contains(read2, "part-two") || strings.Contains(read2, "part-one") {
		t.Fatalf("expected only part-two in read2: %s", read2)
	}

	readAll, _, _ := runCLI("read", "-s", "read-cmd-s", "--all")
	if !strings.Contains(readAll, "part-one") || !strings.Contains(readAll, "part-two") {
		t.Fatalf("expected both in readAll: %s", readAll)
	}

	runCLI("close", "-s", "read-cmd-s")
}

func TestE2E_Restart(t *testing.T) {
	tmpDir := t.TempDir()

	runCLIWithEnv(nil, tmpDir, "launch", "bash --norc --noprofile", "-s", "restart-e2e-s", "--env", "PS1=$ ")

	runCLI("type", "echo first-run", "-s", "restart-e2e-s")
	runCLI("press", "enter", "-s", "restart-e2e-s")
	runCLI("wait", "first-run", "-s", "restart-e2e-s")

	out, _, code := runCLI("restart", "-s", "restart-e2e-s")
	if code != 0 || !strings.Contains(out, `Session "restart-e2e-s" restarted`) {
		t.Fatalf("restart failed: %s", out)
	}

	snap, _, _ := runCLI("snapshot", "-s", "restart-e2e-s", "--trim")
	if strings.Contains(snap, "first-run") {
		t.Fatalf("expected fresh terminal after restart, got: %s", snap)
	}

	// Verify cwd was preserved
	runCLI("type", "pwd", "-s", "restart-e2e-s")
	runCLI("press", "enter", "-s", "restart-e2e-s")
	runCLI("wait", tmpDir, "-s", "restart-e2e-s")

	snapCwd, _, _ := runCLI("snapshot", "-s", "restart-e2e-s", "--trim")
	if !strings.Contains(snapCwd, tmpDir) {
		t.Fatalf("expected cwd preserved, got: %s", snapCwd)
	}

	runCLI("close", "-s", "restart-e2e-s")
}

func TestE2E_Screenshot(t *testing.T) {
	runCLI("launch", "echo screenshot-e2e", "-s", "shot-e2e-s")

	out, _, code := runCLI("screenshot", "-s", "shot-e2e-s", "--immediate")
	if code != 0 || !strings.HasSuffix(out, ".png") {
		t.Fatalf("screenshot failed (%d): %s", code, out)
	}
	defer os.Remove(out)

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("failed to read screenshot file: %v", err)
	}
	// Verify PNG magic header: 0x89 0x50 0x4E 0x47
	if len(data) < 8 || data[0] != 0x89 || data[1] != 0x50 || data[2] != 0x4e || data[3] != 0x47 {
		t.Fatalf("invalid PNG header in screenshot")
	}

	runCLI("close", "-s", "shot-e2e-s")
}

func TestE2E_Click(t *testing.T) {
	runCLI("launch", "bash --norc --noprofile", "-s", "click-e2e-s", "--env", "PS1=$ ")
	runCLI("type", "echo click-target", "-s", "click-e2e-s")
	runCLI("press", "enter", "-s", "click-e2e-s")
	runCLI("wait", "click-target", "-s", "click-e2e-s")

	out, _, code := runCLI("click", "click-target", "-s", "click-e2e-s", "--first")
	if code != 0 || out != "OK" {
		t.Fatalf("click failed (%d): %s", code, out)
	}

	out, _, code = runCLI("click-at", "0", "0", "-s", "click-e2e-s")
	if code != 0 || out != "OK" {
		t.Fatalf("click-at failed (%d): %s", code, out)
	}

	runCLI("close", "-s", "click-e2e-s")
}

func TestE2E_WaitNearbyContext(t *testing.T) {
	runCLI("launch", "bash --norc --noprofile", "-s", "ctx-e2e-s", "--env", "PS1=$ ")
	runCLI("type", "for i in $(seq 1 25); do echo line-$i; done", "-s", "ctx-e2e-s")
	runCLI("press", "enter", "-s", "ctx-e2e-s")

	out, _, code := runCLI("wait", "line-15", "-s", "ctx-e2e-s", "--timeout", "5000")
	if code != 0 {
		t.Fatalf("wait failed (%d): %s", code, out)
	}
	if !strings.Contains(out, "line-15") || !strings.Contains(out, "line-5") || !strings.Contains(out, "line-25") {
		t.Fatalf("expected nearby context in output, got: %s", out)
	}
	if strings.Contains(out, "line-4\n") {
		t.Fatalf("expected line-4 to be outside context window, got: %s", out)
	}

	runCLI("close", "-s", "ctx-e2e-s")
}

func TestE2E_ReadExitSuffix(t *testing.T) {
	runCLI("launch", "printf finished", "-s", "exit-suffix-s")

	// Wait for process to exit
	runCLI("wait", "finished", "-s", "exit-suffix-s")

	out, _, code := runCLI("read", "-s", "exit-suffix-s", "--all", "--trim")
	if code != 0 {
		t.Fatalf("read failed: %s", out)
	}
	if !strings.Contains(out, "finished") || !strings.Contains(out, "[process exited with code 0]") {
		t.Fatalf("expected exit code suffix, got: %s", out)
	}

	runCLI("close", "-s", "exit-suffix-s")
}

func TestE2E_ReadANSI(t *testing.T) {
	runCLI("launch", "bash --norc --noprofile", "-s", "ansi-e2e-s", "--env", "PS1=$ ")
	runCLI("type", `printf "\033[31mred text\033[0m normal"`, "-s", "ansi-e2e-s")
	runCLI("press", "enter", "-s", "ansi-e2e-s")
	runCLI("wait", "normal", "-s", "ansi-e2e-s")

	out, _, code := runCLI("read", "-s", "ansi-e2e-s")
	if code != 0 {
		t.Fatalf("read failed: %s", out)
	}
	if !strings.Contains(out, "red text") || !strings.Contains(out, "normal") {
		t.Fatalf("expected plain text, got: %s", out)
	}
	if strings.Contains(out, "\x1b[31m") || strings.Contains(out, "\x1b[0m") {
		t.Fatalf("expected ANSI codes to be stripped, got: %q", out)
	}

	runCLI("close", "-s", "ansi-e2e-s")
}

func TestE2E_Logfile(t *testing.T) {
	out, _, code := runCLI("logfile")
	if code != 0 || !strings.Contains(out, "relay-server.log") {
		t.Fatalf("logfile failed (%d): %s", code, out)
	}
}
