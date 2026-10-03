package screenshot

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"

	"github.com/gitpod-io/xterm-go"
)

func renderImage(t *testing.T, text string, opts Options) image.Image {
	t.Helper()
	term := xterm.New(xterm.WithCols(10), xterm.WithRows(2))
	term.WriteString(text)
	data, err := RenderTerminal(term, opts)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestScreenshotSizing(t *testing.T) {
	base := renderImage(t, "héllo", Options{})
	tests := []struct {
		name  string
		opts  Options
		check func(*testing.T, image.Rectangle)
	}{
		{"width", Options{Width: 240}, func(t *testing.T, b image.Rectangle) {
			if b.Dx() != 240 {
				t.Fatalf("width=%d, want 240", b.Dx())
			}
		}},
		{"font", Options{FontSize: 28}, func(t *testing.T, b image.Rectangle) {
			if b.Dx() <= base.Bounds().Dx() || b.Dy() <= base.Bounds().Dy() {
				t.Fatalf("larger font did not increase dimensions: %v", b)
			}
		}},
		{"line height", Options{LineHeight: 3}, func(t *testing.T, b image.Rectangle) {
			if b.Dx() != base.Bounds().Dx() || b.Dy() <= base.Bounds().Dy() {
				t.Fatalf("line height did not increase row spacing: %v", b)
			}
		}},
		{"pixel ratio", Options{PixelRatio: 2}, func(t *testing.T, b image.Rectangle) {
			if b.Dx() != base.Bounds().Dx()*2 || b.Dy() != base.Bounds().Dy()*2 {
				t.Fatalf("pixel ratio not applied: base=%v scaled=%v", base.Bounds(), b)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.check(t, renderImage(t, "héllo", tt.opts).Bounds()) })
	}
}

func imagePixels(img image.Image) []byte {
	b := img.Bounds()
	out := make([]byte, 0, b.Dx()*b.Dy()*4)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
			out = append(out, c.R, c.G, c.B, c.A)
		}
	}
	return out
}

func TestScreenshotGlyphsAndStyles(t *testing.T) {
	plain := imagePixels(renderImage(t, "M", Options{}))
	for _, text := range []string{"é", "\x1b[1mM", "\x1b[3mM", "\x1b[1;3mM", "\x1b[4mM", "\x1b[7mM", "\x1b[2mM", "\x1b[9mM"} {
		t.Run(text, func(t *testing.T) {
			if bytes.Equal(plain, imagePixels(renderImage(t, text, Options{}))) {
				t.Fatalf("glyph/style %q was ignored", text)
			}
		})
	}
	accented := imagePixels(renderImage(t, "é", Options{}))
	if bytes.Equal(accented, imagePixels(renderImage(t, ".", Options{}))) {
		t.Fatal("Unicode glyph was blank")
	}
}

func TestScreenshotScrollbackAndTrim(t *testing.T) {
	term := xterm.New(xterm.WithCols(10), xterm.WithRows(2))
	term.WriteString("first\r\nsecond\r\nthird\r\n")
	data, err := RenderTerminal(term, Options{})
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dy() != 63 {
		t.Fatalf("scrollback/trimming height=%d, want 3 rendered rows", img.Bounds().Dy())
	}
	empty := xterm.New(xterm.WithCols(10), xterm.WithRows(2))
	if _, err := RenderTerminal(empty, Options{}); err == nil {
		t.Fatal("empty terminal should have no content to render")
	}
}

func TestScreenshotFallbackGlyphs(t *testing.T) {
	missing := imagePixels(renderImage(t, "\U0010ffff", Options{}))
	for _, text := range []string{"界", "\ue0a0", "⏣"} {
		t.Run(text, func(t *testing.T) {
			got := imagePixels(renderImage(t, text, Options{}))
			if bytes.Equal(got, missing) {
				t.Fatalf("%q rendered as missing glyph", text)
			}
		})
	}
}

func TestScreenshotGeometry(t *testing.T) {
	img := renderImage(t, "\x1b[38;2;18;52;86m█", Options{})
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < 8; x++ {
			if got := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA); got != (color.RGBA{18, 52, 86, 255}) {
				t.Fatalf("full block leaves gap at (%d,%d): %v", x, y, got)
			}
		}
	}
	box := renderImage(t, "\x1b[38;2;18;52;86m──", Options{})
	for x := 0; x < 17; x++ {
		if got := color.RGBAModel.Convert(box.At(x, 10)).(color.RGBA); got != (color.RGBA{18, 52, 86, 255}) {
			t.Fatalf("box line leaves gap at (%d,10): %v", x, got)
		}
	}
}

func TestScreenshotRejectsInvalidDimensions(t *testing.T) {
	tests := []Options{{Width: -1}, {Width: 1, Padding: 100}, {Width: 1 << 30}, {FontSize: -1}, {FontSize: 1 << 30}, {LineHeight: -1}, {LineHeight: math.NaN()}, {LineHeight: math.Inf(1)}, {PixelRatio: -1}, {PixelRatio: math.NaN()}, {PixelRatio: math.Inf(1)}}
	for _, opts := range tests {
		term := xterm.New(xterm.WithCols(10), xterm.WithRows(2))
		term.WriteString("M")
		if _, err := RenderTerminal(term, opts); err == nil {
			t.Errorf("accepted invalid dimensions %+v", opts)
		}
	}
	if _, err := RenderTerminal(nil, Options{}); err == nil {
		t.Fatal("accepted nil terminal")
	}
}

func TestScreenshotWideCellBackground(t *testing.T) {
	img := renderImage(t, "\x1b[48;2;18;52;86m界\x1b[0mX", Options{})
	span := int(math.Round(float64(img.Bounds().Dx()) / 10 * 2))
	want := color.RGBA{18, 52, 86, 255}
	for x := 0; x < span; x++ {
		if got := color.RGBAModel.Convert(img.At(x, 0)).(color.RGBA); got != want {
			t.Fatalf("wide cell background at %d=%v want %v", x, got, want)
		}
	}
	if got := color.RGBAModel.Convert(img.At(span, 0)).(color.RGBA); got == want {
		t.Fatal("wide cell painted following cell")
	}
}

func TestScreenshotColors(t *testing.T) {
	tests := []struct {
		name, text string
		opts       Options
		want       color.RGBA
	}{
		{"rgb", "\x1b[48;2;18;52;86m ", Options{}, color.RGBA{18, 52, 86, 255}},
		{"cube", "\x1b[48;5;17m ", Options{}, color.RGBA{0, 0, 95, 255}},
		{"gray", "\x1b[48;5;233m ", Options{}, color.RGBA{18, 18, 18, 255}},
		{"ansi", "\x1b[41m ", Options{}, color.RGBA{205, 49, 49, 255}},
		{"inverse", "\x1b[7m ", Options{Foreground: "#123456"}, color.RGBA{18, 52, 86, 255}},
		{"theme", "M", Options{Background: " #abc "}, color.RGBA{170, 187, 204, 255}},
		{"invalid theme", "M", Options{Background: "#gggggg"}, color.RGBA{26, 27, 38, 255}},
		{"frame", "M", Options{Padding: 2, FrameColor: "#123456"}, color.RGBA{18, 52, 86, 255}},
		{"auto frame", "\x1b[48;2;18;52;86m\x1b[2J", Options{Padding: 2}, color.RGBA{18, 52, 86, 255}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img := renderImage(t, tt.text, tt.opts)
			got := color.RGBAModel.Convert(img.At(0, 0)).(color.RGBA)
			if got != tt.want {
				t.Fatalf("pixel=%v, want %v", got, tt.want)
			}
		})
	}
}
