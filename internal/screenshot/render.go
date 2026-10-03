package screenshot

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/gitpod-io/xterm-go"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

const (
	defaultFontSize   = 14
	defaultLineHeight = 1.5
	maxImagePixels    = 64 * 1024 * 1024
	maxFontPixels     = 1 << 20
	// JetBrains Mono's advance is 600/1000 em, as in the original renderer.
	monoWidthFactor = 0.6
)

type layout struct {
	width, height                         int
	padding, cellWidth, cellHeight, ratio float64
}

func newLayout(cols, rows int, opts Options) (layout, error) {
	ratio := opts.PixelRatio
	if ratio == 0 {
		ratio = 1
	}
	lineHeight := opts.LineHeight
	if lineHeight == 0 {
		lineHeight = defaultLineHeight
	}
	size := opts.FontSize
	if size == 0 {
		size = defaultFontSize
	}
	if cols <= 0 || rows <= 0 || opts.Width < 0 || size < 0 || ratio <= 0 || lineHeight <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) || math.IsNaN(lineHeight) || math.IsInf(lineHeight, 0) {
		return layout{}, fmt.Errorf("invalid terminal screenshot dimensions or scale")
	}
	cellWidth := float64(size) * monoWidthFactor
	padding := math.Round(float64(max(0, opts.Padding)) * cellWidth)
	width := math.Ceil(float64(cols)*cellWidth + 2*padding)
	if opts.Width > 0 {
		width = float64(opts.Width)
	}
	cellHeight := math.Max(1, math.Round(float64(size)*lineHeight))
	height := float64(rows)*cellHeight + 2*padding
	if width <= 2*padding || cellWidth <= 0 || width*ratio > maxImagePixels || height*ratio > maxImagePixels || width*height*ratio*ratio > maxImagePixels {
		return layout{}, fmt.Errorf("terminal screenshot dimensions exceed rendering limits")
	}
	return layout{int(math.Ceil(width * ratio)), int(math.Ceil(height * ratio)), padding * ratio, cellWidth * ratio, cellHeight * ratio, ratio}, nil
}

func (l layout) cellRect(x, y, width int) image.Rectangle {
	return image.Rect(int(math.Round(l.padding+float64(x)*l.cellWidth)), int(math.Round(l.padding+float64(y)*l.cellHeight)), int(math.Round(l.padding+float64(x+width)*l.cellWidth)), int(math.Round(l.padding+float64(y+1)*l.cellHeight)))
}

func colors(cell *xterm.CellData, bg, fg color.RGBA) (color.RGBA, color.RGBA) {
	cellBg := CellColor(cell.GetBgColor(), cell.IsBgRGB(), cell.IsBgPalette(), bg)
	cellFg := CellColor(cell.GetFgColor(), cell.IsFgRGB(), cell.IsFgPalette(), fg)
	if cell.IsInverse() != 0 {
		cellBg, cellFg = cellFg, cellBg
	}
	return cellBg, cellFg
}

func contentRows(term *xterm.Terminal) int {
	buf := term.Buffer()
	rows := buf.Lines.Length()
	for rows > 0 {
		line := buf.Lines.Get(rows - 1)
		hasContent := false
		if line != nil {
			for x := 0; x < line.Len; x++ {
				cell := line.LoadCell(x, xterm.NewCellData())
				if strings.TrimSpace(cell.GetChars()) != "" || cell.IsBgRGB() || cell.IsBgPalette() || cell.IsInverse() != 0 {
					hasContent = true
					break
				}
			}
		}
		if hasContent {
			break
		}
		rows--
	}
	return rows
}

func frameColor(term *xterm.Terminal, rows int, bg, fg color.RGBA) color.RGBA {
	counts := make(map[color.RGBA]int)
	chosen := bg
	largest := 0
	buf := term.Buffer()
	for y := 0; y < rows; y++ {
		line := buf.Lines.Get(y)
		if line == nil {
			continue
		}
		for x := 0; x < min(term.Cols(), line.Len); x++ {
			if y != 0 && y != rows-1 && x != 0 && x != term.Cols()-1 {
				continue
			}
			c, _ := colors(line.LoadCell(x, xterm.NewCellData()), bg, fg)
			counts[c]++
			if counts[c] > largest {
				chosen = c
				largest = counts[c]
			}
		}
	}
	return chosen
}

// RenderTerminal renders the terminal buffer to PNG. The caller must synchronize terminal access.
func RenderTerminal(term *xterm.Terminal, opts Options) ([]byte, error) {
	if term == nil {
		return nil, fmt.Errorf("terminal is required")
	}
	size := opts.FontSize
	if size == 0 {
		size = defaultFontSize
	}
	ratio := opts.PixelRatio
	if ratio == 0 {
		ratio = 1
	}
	// Validate before constructing a font face, whose fixed-point scale must remain representable.
	if size < 0 || ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) || float64(size)*ratio > maxFontPixels {
		return nil, fmt.Errorf("invalid font size or pixel ratio")
	}
	faces, err := loadFaces(float64(size) * ratio)
	if err != nil {
		return nil, err
	}
	defer faces.close()
	rows := contentRows(term)
	if rows == 0 {
		return nil, fmt.Errorf("no content to render")
	}
	grid, err := newLayout(term.Cols(), rows, opts)
	if err != nil {
		return nil, err
	}
	bg := parseColor(opts.Background, color.RGBA{26, 27, 38, 255})
	fg := parseColor(opts.Foreground, color.RGBA{192, 202, 245, 255})
	frame := frameColor(term, rows, bg, fg)
	if opts.FrameColor != "" {
		frame = parseColor(opts.FrameColor, bg)
	}
	img := image.NewRGBA(image.Rect(0, 0, grid.width, grid.height))
	draw.Draw(img, img.Bounds(), image.NewUniform(frame), image.Point{}, draw.Src)
	draw.Draw(img, grid.cellRect(0, 0, term.Cols()).Union(grid.cellRect(0, rows-1, term.Cols())), image.NewUniform(bg), image.Point{}, draw.Src)
	renderCells(img, term, rows, grid, faces, bg, fg)
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, fmt.Errorf("encoding PNG: %w", err)
	}
	return out.Bytes(), nil
}

func renderCells(img *image.RGBA, term *xterm.Terminal, rows int, grid layout, faces fontFaces, bg, fg color.RGBA) {
	buf := term.Buffer()
	for y := 0; y < rows; y++ {
		line := buf.Lines.Get(y)
		if line == nil {
			continue
		}
		for x := 0; x < min(term.Cols(), line.Len); x++ {
			cell := line.LoadCell(x, xterm.NewCellData())
			if cell.GetWidth() == 0 {
				continue
			}
			rect := grid.cellRect(x, y, max(1, cell.GetWidth())).Intersect(img.Bounds())
			cellBg, cellFg := colors(cell, bg, fg)
			if cell.IsDim() != 0 {
				cellFg = color.RGBA{uint8((uint16(cellFg.R) + uint16(cellBg.R)) / 2), uint8((uint16(cellFg.G) + uint16(cellBg.G)) / 2), uint8((uint16(cellFg.B) + uint16(cellBg.B)) / 2), 255}
			}
			draw.Draw(img, rect, image.NewUniform(cellBg), image.Point{}, draw.Src)
			r, _ := utf8.DecodeRuneInString(cell.GetChars())
			face := faces.selectFace(cell.IsBold() != 0, cell.IsItalic() != 0, r)
			metrics := face.Metrics()
			baseline := rect.Min.Y + (rect.Dy()-metrics.Ascent.Ceil()-metrics.Descent.Ceil())/2 + metrics.Ascent.Ceil()
			if chars := cell.GetChars(); chars != "" && chars != " " {
				// Clip italic overhang and wide glyphs to their terminal cell span.
				d := font.Drawer{Dst: img.SubImage(rect).(*image.RGBA), Src: image.NewUniform(cellFg), Face: face, Dot: fixed.P(rect.Min.X, baseline)}
				if !renderGeometry(img, rect, chars, cellFg) {
					d.DrawString(chars)
				}
			}
			if cell.IsUnderline() != 0 {
				underline := image.Rect(rect.Min.X, baseline+1, rect.Max.X, baseline+1+max(1, int(math.Round(grid.ratio)))).Intersect(rect)
				draw.Draw(img, underline, image.NewUniform(cellFg), image.Point{}, draw.Src)
			}
			if cell.IsStrikethrough() != 0 {
				y := baseline - metrics.XHeight.Ceil()/2
				strike := image.Rect(rect.Min.X, y, rect.Max.X, y+max(1, int(math.Round(grid.ratio)))).Intersect(rect)
				draw.Draw(img, strike, image.NewUniform(cellFg), image.Point{}, draw.Src)
			}
		}
	}
}
