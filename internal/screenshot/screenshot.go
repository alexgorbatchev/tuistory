package screenshot

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strconv"
	"strings"

	"github.com/gitpod-io/xterm-go"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
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
	{0x00, 0x00, 0x00, 0xff}, // 0: Black
	{0xcd, 0x31, 0x31, 0xff}, // 1: Red
	{0x0d, 0xbc, 0x79, 0xff}, // 2: Green
	{0xe5, 0xe5, 0x10, 0xff}, // 3: Yellow
	{0x24, 0x72, 0xc8, 0xff}, // 4: Blue
	{0xbc, 0x3f, 0xbc, 0xff}, // 5: Magenta
	{0x11, 0xa8, 0xcd, 0xff}, // 6: Cyan
	{0xe5, 0xe5, 0xe5, 0xff}, // 7: White
	{0x66, 0x66, 0x66, 0xff}, // 8: Bright Black
	{0xf1, 0x4c, 0x4c, 0xff}, // 9: Bright Red
	{0x23, 0xd1, 0x8b, 0xff}, // 10: Bright Green
	{0xf5, 0xf5, 0x43, 0xff}, // 11: Bright Yellow
	{0x3b, 0x8e, 0xea, 0xff}, // 12: Bright Blue
	{0xd6, 0x70, 0xd6, 0xff}, // 13: Bright Magenta
	{0x29, 0xb8, 0xdb, 0xff}, // 14: Bright Cyan
	{0xff, 0xff, 0xff, 0xff}, // 15: Bright White
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

// RenderTerminal renders a terminal's screen buffer to PNG bytes.
func RenderTerminal(term *xterm.Terminal, opts Options) ([]byte, error) {
	cols := term.Cols()
	rows := term.Rows()
	if cols <= 0 || rows <= 0 {
		return nil, fmt.Errorf("invalid terminal dimensions %dx%d", cols, rows)
	}

	bg := parseColor(opts.Background, color.RGBA{0x1a, 0x1b, 0x26, 0xff})
	fg := parseColor(opts.Foreground, color.RGBA{0xc0, 0xca, 0xf5, 0xff})
	frameBg := bg
	if opts.FrameColor != "" {
		frameBg = parseColor(opts.FrameColor, bg)
	}

	cellWidth := 7
	cellHeight := 13

	paddingCells := opts.Padding
	if paddingCells < 0 {
		paddingCells = 0
	}
	paddingPx := paddingCells * cellWidth

	imgWidth := cols*cellWidth + paddingPx*2
	imgHeight := rows*cellHeight + paddingPx*2

	img := image.NewRGBA(image.Rect(0, 0, imgWidth, imgHeight))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: frameBg}, image.Point{}, draw.Src)

	innerRect := image.Rect(paddingPx, paddingPx, paddingPx+cols*cellWidth, paddingPx+rows*cellHeight)
	draw.Draw(img, innerRect, &image.Uniform{C: bg}, image.Point{}, draw.Src)

	buf := term.Buffer()
	face := basicfont.Face7x13

	for y := 0; y < rows; y++ {
		lineIdx := buf.YBase + y
		if lineIdx >= buf.Lines.Length() {
			continue
		}
		line := buf.Lines.Get(lineIdx)
		if line == nil {
			continue
		}

		for x := 0; x < cols; x++ {
			if x >= line.Len {
				continue
			}
			cell := line.LoadCell(x, xterm.NewCellData())

			cellBg := CellColor(cell.GetBgColor(), cell.IsBgRGB(), cell.IsBgPalette(), bg)
			cellFg := CellColor(cell.GetFgColor(), cell.IsFgRGB(), cell.IsFgPalette(), fg)

			cellX := paddingPx + x*cellWidth
			cellY := paddingPx + y*cellHeight

			if cellBg != bg {
				cellBox := image.Rect(cellX, cellY, cellX+cellWidth, cellY+cellHeight)
				draw.Draw(img, cellBox, &image.Uniform{C: cellBg}, image.Point{}, draw.Src)
			}

			charStr := cell.GetChars()
			if charStr != "" && charStr != " " {
				d := &font.Drawer{
					Dst:  img,
					Src:  &image.Uniform{C: cellFg},
					Face: face,
					Dot:  fixed.Point26_6{X: fixed.I(cellX), Y: fixed.I(cellY + 11)}, // 11 is baseline offset for 7x13
				}
				d.DrawString(charStr)
			}
		}
	}

	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, fmt.Errorf("encoding png: %w", err)
	}

	return out.Bytes(), nil
}
