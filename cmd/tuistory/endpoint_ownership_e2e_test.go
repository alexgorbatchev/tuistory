package main

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestE2E_RelayEndpointOwnership(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("..", "..", "bin", "tuistory-endpoint-listener"))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", fixture, "./testdata/endpoint-listener").CombinedOutput(); err != nil {
		t.Fatalf("build independent listener: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if err := os.Remove(fixture); err != nil {
			t.Error(err)
		}
	})
	for _, host := range []string{"127.0.0.2", "::1", "127.0.0.1"} {
		t.Run(host, func(t *testing.T) {
			if out, errOut, code := runCLI("daemon-stop"); code != 0 {
				t.Fatalf("stop owned fixture daemon: %q %q code=%d", out, errOut, code)
			}
			address := net.JoinHostPort(host, fmt.Sprint(testPort))
			listener, err := net.Listen("tcp", address)
			if err != nil {
				t.Skipf("neighbor address unavailable: %v", err)
			}
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(fixture)
			cmd.Env = append(os.Environ(), "ENDPOINT_LISTENER_ADDRESS="+address)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			t.Cleanup(func() {
				_ = cmd.Process.Kill() // The test owns the independent fixture process.
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("independent fixture did not exit")
				}
			})
			ready := make(chan bool, 1)
			go func() {
				scanner := bufio.NewScanner(stdout)
				ready <- scanner.Scan() && scanner.Text() == "ready"
			}()
			select {
			case ok := <-ready:
				if !ok {
					t.Fatal("independent listener did not report readiness")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("independent listener readiness timed out")
			}
			if host != "127.0.0.1" {
				if out, errOut, code := runCLI("sessions"); code != 0 {
					t.Fatalf("start owned relay: %q %q code=%d", out, errOut, code)
				}
			}
			out, errOut, code := runCLI("daemon-stop")
			if host == "127.0.0.1" {
				if code == 0 || !strings.Contains(errOut, "refusing") {
					t.Errorf("foreign owner refusal: %q %q code=%d", out, errOut, code)
				}
			} else if code != 0 || out != "Daemon stopped" {
				t.Errorf("daemon-stop: %q %q code=%d", out, errOut, code)
			}
			select {
			case err := <-done:
				// Preserve the exit result for the process owner's cleanup.
				done <- err
				t.Fatalf("unrelated listener was terminated: %v", err)
			default:
			}
			conn, err := net.DialTimeout("tcp", address, time.Second)
			if err != nil {
				t.Fatalf("unrelated listener stopped accepting: %v", err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
