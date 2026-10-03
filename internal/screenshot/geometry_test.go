package screenshot

import (
	"image/color"
	"testing"
)

func TestGeometryGlyphCoverage(t *testing.T) {
	const glyphs = "█▀▔▐▕▁▂▃▄▅▆▇▉▊▋▌▍▎▏░▒▓▖▗▘▙▚▛▜▝▞▟─━│┃┌┐└┘├┤┬┴┼╴╵╶╷╸╹╺╻═║╔╗╚╝╬⠁⡀⢀⣿"
	for _, r := range glyphs {
		t.Run(string(r), func(t *testing.T) {
			img := renderImage(t, "\x1b[38;2;255;255;255m"+string(r), Options{FontSize: 20, Background: "#000"})
			ink := 0
			for y := 0; y < 30; y++ {
				for x := 0; x < 12; x++ {
					red, _, _, _ := img.At(x, y).RGBA()
					if red > 0 {
						ink++
					}
				}
			}
			if ink == 0 {
				t.Fatal("geometry glyph has no visible pixels")
			}
			if r == '█' && ink != 360 {
				t.Fatalf("full block covered %d of 360 pixels", ink)
			}
			if r == '▄' && ink != 180 {
				t.Fatalf("half block covered %d of 360 pixels", ink)
			}
		})
	}
}

func TestGeometryFractionsAndShading(t *testing.T) {
	tests := []struct {
		name, glyph string
		x, y        int
		want        color.RGBA
	}{
		{"lower inside", "▁", 6, 29, color.RGBA{255, 255, 255, 255}},
		{"lower outside", "▁", 6, 10, color.RGBA{0, 0, 0, 255}},
		{"left inside", "▌", 1, 10, color.RGBA{255, 255, 255, 255}},
		{"left outside", "▌", 10, 10, color.RGBA{0, 0, 0, 255}},
		{"quadrant inside", "▘", 1, 1, color.RGBA{255, 255, 255, 255}},
		{"quadrant outside", "▘", 10, 20, color.RGBA{0, 0, 0, 255}},
		{"shade light", "░", 5, 10, color.RGBA{63, 63, 63, 255}},
		{"shade medium", "▒", 5, 10, color.RGBA{127, 127, 127, 255}},
		{"shade dark", "▓", 5, 10, color.RGBA{191, 191, 191, 255}},
		{"triangle inside", "", 1, 15, color.RGBA{255, 255, 255, 255}},
		{"triangle outside", "", 10, 1, color.RGBA{0, 0, 0, 255}},
		{"braille empty", "⠀", 6, 15, color.RGBA{0, 0, 0, 255}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img := renderImage(t, "\x1b[38;2;255;255;255m"+tt.glyph, Options{FontSize: 20, Background: "#000"})
			got := color.RGBAModel.Convert(img.At(tt.x, tt.y)).(color.RGBA)
			if got != tt.want {
				t.Fatalf("pixel (%d,%d)=%v, want %v", tt.x, tt.y, got, tt.want)
			}
		})
	}
}

func TestScreenshotStyledGeometry(t *testing.T) {
	normal := renderImage(t, "█", Options{FontSize: 20, Background: "#000", Foreground: "#fff"})
	faint := renderImage(t, "\x1b[2m█", Options{FontSize: 20, Background: "#000", Foreground: "#fff"})
	red, _, _, _ := normal.At(1, 1).RGBA()
	dim, _, _, _ := faint.At(1, 1).RGBA()
	if dim >= red || dim == 0 {
		t.Fatalf("faint block brightness=%d, normal=%d", dim, red)
	}
	for _, style := range []string{"4", "9"} {
		img := renderImage(t, "\x1b["+style+"m▁", Options{FontSize: 20, Background: "#000", Foreground: "#fff"})
		decorated := false
		for y := 0; y < 26; y++ {
			bright, _, _, _ := img.At(1, y).RGBA()
			if bright != 0 {
				decorated = true
			}
		}
		if !decorated {
			t.Fatalf("style %s added no decoration above lower block", style)
		}
	}
}
