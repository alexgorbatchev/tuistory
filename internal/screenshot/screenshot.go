package screenshot

import (
	"image/color"
	"strconv"
	"strings"
)

// Options specifies terminal screenshot rendering parameters.
type Options struct {
	Width      int
	FontSize   int
	LineHeight float64
	Background string
	Foreground string
	PixelRatio float64
	Padding    int
	FrameColor string
}

var ansiPalette = [16]color.RGBA{
	// The TypeScript implementation's ghostty-opentui@1.5.0 default palette.
	{0x1d, 0x1f, 0x21, 0xff}, // 0: Black
	{0xcc, 0x66, 0x66, 0xff}, // 1: Red
	{0xb5, 0xbd, 0x68, 0xff}, // 2: Green
	{0xf0, 0xc6, 0x74, 0xff}, // 3: Yellow
	{0x81, 0xa2, 0xbe, 0xff}, // 4: Blue
	{0xb2, 0x94, 0xbb, 0xff}, // 5: Magenta
	{0x8a, 0xbe, 0xb7, 0xff}, // 6: Cyan
	{0xc5, 0xc8, 0xc6, 0xff}, // 7: White
	{0x66, 0x66, 0x66, 0xff}, // 8: Bright Black
	{0xd5, 0x4e, 0x53, 0xff}, // 9: Bright Red
	{0xb9, 0xca, 0x4a, 0xff}, // 10: Bright Green
	{0xe7, 0xc5, 0x47, 0xff}, // 11: Bright Yellow
	{0x7a, 0xa6, 0xda, 0xff}, // 12: Bright Blue
	{0xc3, 0x97, 0xd8, 0xff}, // 13: Bright Magenta
	{0x70, 0xc0, 0xb1, 0xff}, // 14: Bright Cyan
	{0xea, 0xea, 0xea, 0xff}, // 15: Bright White
}

func parseColor(raw string, fallback color.RGBA) color.RGBA {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) == 6 {
		val, err := strconv.ParseUint(s, 16, 32)
		if err == nil {
			return color.RGBA{
				R: uint8(val >> 16),
				G: uint8((val >> 8) & 0xff),
				B: uint8(val & 0xff),
				A: 255,
			}
		}
	}
	return fallback
}

// CellColor resolves a terminal color using its RGB or indexed color mode.
func CellColor(code int, isRGB, isPalette bool, def color.RGBA) color.RGBA {
	if isRGB && code >= 0 {
		return color.RGBA{
			R: uint8(code >> 16),
			G: uint8((code >> 8) & 0xff),
			B: uint8(code & 0xff),
			A: 255,
		}
	}
	if isPalette && code >= 0 && code < 16 {
		return ansiPalette[code]
	}
	if isPalette && code >= 16 && code <= 231 {
		// 6x6x6 color cube
		c := code - 16
		levels := [...]uint8{0, 95, 135, 175, 215, 255}
		return color.RGBA{R: levels[c/36], G: levels[(c/6)%6], B: levels[c%6], A: 255}
	}
	if isPalette && code >= 232 && code <= 255 {
		// Grayscale ramp
		gray := (code-232)*10 + 8
		return color.RGBA{R: uint8(gray), G: uint8(gray), B: uint8(gray), A: 255}
	}
	return def
}
