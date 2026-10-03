package relay

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestRelayTerminalBytesUseBinaryFrames(t *testing.T) {
	sess := relaySession(t, `stty raw -echo; printf '{"type":"closing"}'; cat`)
	srv := NewServer("binary", 19999, ".")
	srv.AddSession("binary", sess)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn := relayConnection(t, ctx, ts)
	defer conn.CloseNow()
	writeControl(t, ctx, conn, map[string]any{"type": "attach", "session": "binary"})
	history := []byte(`{"type":"closing"}`)
	readBinaryBytes(t, ctx, conn, history)
	for _, data := range [][]byte{[]byte(`{"type":"kill"}`), {0xff, 0xc0, 0x80, 0, 0x1b}} {
		if err := conn.Write(ctx, websocket.MessageBinary, data); err != nil {
			t.Fatal(err)
		}
		readBinaryBytes(t, ctx, conn, data)
		if sess.IsDead() {
			t.Fatal("raw terminal input killed the child")
		}
	}
}

func readBinaryBytes(t *testing.T, ctx context.Context, conn *websocket.Conn, want []byte) {
	t.Helper()
	var got []byte
	for len(got) < len(want) {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if typ != websocket.MessageBinary {
			t.Fatalf("terminal bytes sent as %v: %q", typ, data)
		}
		got = append(got, data...)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("terminal bytes %q, want %q", got, want)
	}
}
