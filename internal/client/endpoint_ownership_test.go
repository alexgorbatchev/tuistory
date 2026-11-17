package client

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/remorses/tuistory/internal/relay"
)

func TestRelayNeighborSurvives(t *testing.T) {
	for _, host := range []string{"127.0.0.2", "::1"} {
		for _, action := range []string{"stop", "force-stop", "recover-old", "recover-wedged"} {
			t.Run(host+"/"+action, func(t *testing.T) {
				isolateClientState(t)
				port := reserveClientPort(t)
				address := net.JoinHostPort(host, fmt.Sprint(port))
				listener, err := net.Listen("tcp", address)
				if err != nil {
					t.Skipf("neighbor address unavailable: %v", err)
				}
				if err := listener.Close(); err != nil {
					t.Fatal(err)
				}
				neighbor := startOwnedClientListener(t, port, host, helperVersion, "")
				waitForNeighbor(t, address, neighbor)
				version, mode := helperVersion, ""
				switch action {
				case "force-stop":
					mode = "ignore-term"
				case "recover-old":
					version = "0.0.0"
				case "recover-wedged":
					mode = "unresponsive"
				}
				relay := startClientRelayChild(t, port, version, mode)
				if action == "stop" || action == "force-stop" {
					if free, err := KillRelay(port); err != nil || !free {
						t.Fatalf("stop: free=%v err=%v", free, err)
					}
				} else {
					if err := EnsureRelayRunning(port, helperVersion); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { cleanupSpawnedClientRelay(t, port) })
				}
				select {
				case <-relay.done:
				case <-time.After(time.Second):
					t.Fatal("replaced relay remains alive")
				}
				waitForNeighbor(t, address, neighbor)
			})
		}
	}
}

func TestUnverifiedEndpointOwnerSurvives(t *testing.T) {
	for _, action := range []string{"stop", "recover"} {
		for _, forgedPID := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/forged-pid=%v", action, forgedPID), func(t *testing.T) {
				isolateClientState(t)
				port := reserveClientPort(t)
				address := net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
				child := startOwnedClientListener(t, port, "127.0.0.1", helperVersion, "foreign")
				waitForNeighbor(t, address, child)
				if forgedPID {
					path := relay.PidFilePath(port)
					if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(fmt.Sprint(child.cmd.Process.Pid)), 0600); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				if action == "stop" {
					var freed bool
					freed, err = KillRelay(port)
					if freed {
						t.Error("reported an unrelated occupied endpoint as freed")
					}
				} else {
					err = EnsureRelayRunning(port, helperVersion)
					// If the regression spawned a replacement, reap only that test child.
					select {
					case <-child.done:
						t.Cleanup(func() { cleanupSpawnedClientRelay(t, port) })
					default:
					}
				}
				if err == nil || !strings.Contains(err.Error(), "refusing") {
					t.Errorf("expected explicit unverified-owner refusal, got %v", err)
				}
				waitForNeighbor(t, address, child)
			})
		}
	}
}

func waitForNeighbor(t *testing.T, address string, child *clientRelayChild) {
	t.Helper()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 100 * time.Millisecond, Transport: transport}
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case <-child.done:
			t.Fatalf("unrelated listener %s exited: %v", address, child.waitErr)
		default:
		}
		resp, err := client.Get("http://" + address + "/version")
		if err == nil {
			if err := resp.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("unrelated listener %s not answering: %v", address, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
