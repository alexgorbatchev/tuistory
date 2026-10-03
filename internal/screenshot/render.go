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
	pixelWidth, pixelHeight := math.Ceil(width*ratio), math.Ceil(height*ratio)
	if width <= 2*padding || cellWidth <= 0 || pixelWidth > maxImagePixels || pixelHeight > maxImagePixels || pixelWidth*pixelHeight > maxImagePixels {
		return layout{}, fmt.Errorf("terminal screenshot dimensions exceed rendering limits")
	}
	return layout{int(pixelWidth), int(pixelHeight), padding * ratio, cellWidth * ratio, cellHeight * ratio, ratio}, nil
}

func (l layout) cellRect(x, y, width int) image.Rectangle {
	return image.Rect(int(math.Round(l.padding+float64(x)*l.cellWidth)), int(math.Round(l.padding+float64(y)*l.cellHeight)), int(math.Round(l.padding+float64(x+width)*l.cellWidth)), int(math.Round(l.padding+float64(y+1)*l.cellHeight)))
}

func (l layout) contentRect() image.Rectangle {
	padding := int(math.Round(l.padding))
	return image.Rect(padding, padding, l.width-padding, l.height-padding)
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
	if err := validateRasterBounds(term, rows, grid, faces); err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, grid.width, grid.height))
	draw.Draw(img, img.Bounds(), image.NewUniform(frame), image.Point{}, draw.Src)
	draw.Draw(img, grid.contentRect(), image.NewUniform(bg), image.Point{}, draw.Src)
	renderCells(img, term, rows, grid, faces, bg, fg)
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, fmt.Errorf("encoding PNG: %w", err)
	}
	return out.Bytes(), nil
}

func renderCells(img *image.RGBA, term *xterm.Terminal, rows int, grid layout, faces fontFaces, bg, fg color.RGBA) {
	content := img.SubImage(grid.contentRect()).(*image.RGBA)
	_ = forEachCell(term, rows, func(x, y int, cell *xterm.CellData) error {
		cellRect := grid.cellRect(x, y, max(1, cell.GetWidth()))
		rect := cellRect.Intersect(content.Bounds())
		if rect.Empty() {
			return nil
		}
		cellBg, cellFg := colors(cell, bg, fg)
		if cell.IsDim() != 0 {
			cellFg = color.RGBA{uint8((uint16(cellFg.R) + uint16(cellBg.R)) / 2), uint8((uint16(cellFg.G) + uint16(cellBg.G)) / 2), uint8((uint16(cellFg.B) + uint16(cellBg.B)) / 2), 255}
		}
		draw.Draw(img, rect, image.NewUniform(cellBg), image.Point{}, draw.Src)
		face, position := cellFont(faces, cell, cellRect)
		metrics := face.Metrics()
		baseline := position.Y
		if chars := cell.GetChars(); chars != "" && chars != " " {
			// Clip italic overhang and wide glyphs to their terminal cell span.
			if !drawGeometry(content, cellRect, rect, chars, cellFg) {
				d := font.Drawer{Dst: content.SubImage(rect).(*image.RGBA), Src: image.NewUniform(cellFg), Face: face, Dot: fixed.P(position.X, position.Y)}
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
		return nil
	}) // Rendering is infallible after preflight; this callback never returns an error.
}

func forEachCell(term *xterm.Terminal, rows int, visit func(int, int, *xterm.CellData) error) error {
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
			if err := visit(x, y, cell); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRasterBounds(term *xterm.Terminal, rows int, grid layout, faces fontFaces) error {
	return forEachCell(term, rows, func(x, y int, cell *xterm.CellData) error {
		chars := cell.GetChars()
		rect := grid.cellRect(x, y, max(1, cell.GetWidth()))
		if rect.Intersect(grid.contentRect()).Empty() || chars == "" || chars == " " {
			return nil
		}
		if isGeometry(chars) {
			if !rasterWithinLimit(rect.Dx(), rect.Dy()) {
				return fmt.Errorf("terminal geometry raster exceeds rendering limits")
			}
			return nil
		}
		face, position := cellFont(faces, cell, rect)
		dotX, dotY := int64(position.X)*int64(fixed.I(1)), int64(position.Y)*int64(fixed.I(1))
		prev := rune(-1)
		for _, r := range chars {
			if prev >= 0 {
				dotX += int64(face.Kern(prev, r))
			}
			if !nativeCoordinate(dotX) || !nativeCoordinate(dotY) {
				return fmt.Errorf("terminal font coordinates exceed native fixed-point range")
			}
			bounds, advance, _ := face.GlyphBounds(r)
			minX, minY := int64(bounds.Min.X)+dotX, int64(bounds.Min.Y)+dotY
			maxX, maxY := int64(bounds.Max.X)+dotX, int64(bounds.Max.Y)+dotY
			// OpenType adds the dot in fixed.Int26_6, and Ceil adds 63 before
			// shifting. Validate both operations in int64 before rasterization.
			ceilBias := int64(fixed.I(1)) - 1
			if !nativeCoordinate(minX) || !nativeCoordinate(minY) || !nativeCoordinate(maxX+ceilBias) || !nativeCoordinate(maxY+ceilBias) {
				return fmt.Errorf("terminal font coordinates exceed native fixed-point range")
			}
			bounds = fixed.Rectangle26_6{Min: fixed.Point26_6{X: fixed.Int26_6(minX), Y: fixed.Int26_6(minY)}, Max: fixed.Point26_6{X: fixed.Int26_6(maxX), Y: fixed.Int26_6(maxY)}}
			width, height := bounds.Max.X.Ceil()-bounds.Min.X.Floor(), bounds.Max.Y.Ceil()-bounds.Min.Y.Floor()
			if !rasterWithinLimit(width, height) {
				return fmt.Errorf("terminal glyph raster exceeds rendering limits")
			}
			dotX += int64(advance)
			prev = r
		}
		return nil
	})
}

func nativeCoordinate(value int64) bool {
	return value >= math.MinInt32 && value <= math.MaxInt32
}

func cellFont(faces fontFaces, cell *xterm.CellData, rect image.Rectangle) (font.Face, image.Point) {
	r, _ := utf8.DecodeRuneInString(cell.GetChars())
	face := faces.selectFace(cell.IsBold() != 0, cell.IsItalic() != 0, r)
	metrics := face.Metrics()
	baseline := rect.Min.Y + (rect.Dy()-metrics.Ascent.Ceil()-metrics.Descent.Ceil())/2 + metrics.Ascent.Ceil()
	return face, image.Pt(rect.Min.X, baseline)
}

func rasterWithinLimit(width, height int) bool {
	return width >= 0 && height >= 0 && float64(width)*float64(height) <= maxImagePixels
}

func drawGeometry(img *image.RGBA, cellRect, clip image.Rectangle, chars string, fg color.RGBA) bool {
	if !isGeometry(chars) {
		return false
	}
	if cellRect == clip {
		return renderGeometry(img, cellRect, chars, fg)
	}
	// vector.Draw's fast RGBA path requires the full destination rectangle.
	// Render at native cell size, then crop with image/draw's clipping semantics.
	glyph := image.NewRGBA(cellRect)
	renderGeometry(glyph, cellRect, chars, fg)
	draw.Draw(img, clip, glyph, clip.Min, draw.Over)
	return true
}
