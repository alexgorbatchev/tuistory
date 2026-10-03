package client

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/remorses/tuistory/internal/relay"
)

func TestWaitForRelayRequiresCompatibleProtocol(t *testing.T) {
	for _, tt := range []struct {
		name    string
		payload string
		want    bool
	}{
		{"typescript higher version", `{"version":"0.11.0"}`, false},
		{"foreign protocol", `{"version":"99.0.0","protocol":"foreign/1"}`, false},
		{"compatible", `{"version":"0.0.1","protocol":"tuistory-go/1"}`, true},
		{"outdated compatible", `{"version":"0.0.0","protocol":"tuistory-go/1"}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tt.payload)
			}))
			defer srv.Close()
			_, rawPort, err := net.SplitHostPort(srv.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(rawPort)
			if err != nil {
				t.Fatal(err)
			}
			if got := WaitForRelay(port, 20*time.Millisecond, "0.0.1"); got != tt.want {
				t.Fatalf("WaitForRelay() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCurrentServerIsCompatible(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	srv := httptest.NewUnstartedServer(relay.NewServer("0.0.1", port, "").Handler())
	srv.Listener.Close()
	srv.Listener = listener
	srv.Start()
	defer srv.Close()
	if !WaitForRelay(port, time.Second, "0.0.1") {
		t.Fatal("current server rejected")
	}
}
