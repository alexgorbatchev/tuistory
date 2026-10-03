package screenshot

import (
	"image/color"
	"testing"
)

func TestCellColor(t *testing.T) {
	fallback := color.RGBA{1, 2, 3, 255}
	tests := []struct {
		name         string
		code         int
		rgb, palette bool
		want         color.RGBA
	}{
		{"RGB", 0x123456, true, false, color.RGBA{18, 52, 86, 255}},
		{"ANSI red", 1, false, true, color.RGBA{205, 49, 49, 255}},
		{"cube blue", 17, false, true, color.RGBA{0, 0, 95, 255}},
		{"cube white", 231, false, true, color.RGBA{255, 255, 255, 255}},
		{"gray", 233, false, true, color.RGBA{18, 18, 18, 255}},
		{"unset", -1, false, true, fallback},
		{"invalid", 256, false, true, fallback},
		{"default", 1, false, false, fallback},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CellColor(tt.code, tt.rgb, tt.palette, fallback); got != tt.want {
				t.Fatalf("color=%v want %v", got, tt.want)
			}
		})
	}
}
