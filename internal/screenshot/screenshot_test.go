package screenshot

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/gitpod-io/xterm-go"
)

func TestRenderTerminalToPNG(t *testing.T) {
	term := xterm.New(xterm.WithCols(40), xterm.WithRows(10))
	term.WriteString("\x1b[32mgreen\x1b[0m \x1b[1mbold\x1b[0m normal\r\nline two")

	pngBytes, err := RenderTerminal(term, Options{
		FontSize:   14,
		LineHeight: 1.5,
		Background: "#1a1b26",
		Foreground: "#c0caf5",
		Padding:    2,
	})
	if err != nil {
		t.Fatalf("RenderTerminal error: %v", err)
	}

	if len(pngBytes) < 100 {
		t.Fatalf("expected PNG output, got only %d bytes", len(pngBytes))
	}

	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("png.Decode error: %v", err)
	}

	bounds := img.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		t.Fatalf("invalid image dimensions: %v", bounds)
	}
}
