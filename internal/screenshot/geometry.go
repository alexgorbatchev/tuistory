package screenshot

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"unicode/utf8"

	"golang.org/x/image/vector"
)

// Terminal geometry follows ghostty-opentui@1.5.0's cell-relative block, box,
// braille and powerline geometry, using the native x/image vector rasterizer.
func renderGeometry(img *image.RGBA, rect image.Rectangle, chars string, fg color.RGBA) bool {
	if utf8.RuneCountInString(chars) != 1 {
		return false
	}
	r, _ := utf8.DecodeRuneInString(chars)
	if renderBlock(img, rect, r, fg) || renderBox(img, rect, r, fg) {
		return true
	}
	if r >= 0x2800 && r <= 0x28ff {
		renderBraille(img, rect, r, fg)
		return true
	}
	return renderPowerline(img, rect, r, fg)
}

func fillRect(img *image.RGBA, rect image.Rectangle, fg color.RGBA) {
	draw.Draw(img, rect, image.NewUniform(fg), image.Point{}, draw.Over)
}

func fractionRect(rect image.Rectangle, x, y, w, h float64) image.Rectangle {
	return image.Rect(rect.Min.X+int(math.Round(x*float64(rect.Dx()))), rect.Min.Y+int(math.Round(y*float64(rect.Dy()))), rect.Min.X+int(math.Round((x+w)*float64(rect.Dx()))), rect.Min.Y+int(math.Round((y+h)*float64(rect.Dy()))))
}

func renderBlock(img *image.RGBA, rect image.Rectangle, r rune, fg color.RGBA) bool {
	switch {
	case r == '█':
		fillRect(img, rect, fg)
	case r == '▀':
		fillRect(img, fractionRect(rect, 0, 0, 1, .5), fg)
	case r == '▔':
		fillRect(img, fractionRect(rect, 0, 0, 1, .125), fg)
	case r == '▐':
		fillRect(img, fractionRect(rect, .5, 0, .5, 1), fg)
	case r == '▕':
		fillRect(img, fractionRect(rect, .875, 0, .125, 1), fg)
	case r >= '▁' && r <= '▇':
		height := float64(r-'▁'+1) / 8
		fillRect(img, fractionRect(rect, 0, 1-height, 1, height), fg)
	case r >= '▉' && r <= '▏':
		width := float64('▏'-r+1) / 8
		fillRect(img, fractionRect(rect, 0, 0, width, 1), fg)
	case r >= '░' && r <= '▓':
		alpha := uint8((int(r-'░') + 1) * 255 / 4)
		fillRect(img, rect, color.RGBA{uint8(uint16(fg.R) * uint16(alpha) / 255), uint8(uint16(fg.G) * uint16(alpha) / 255), uint8(uint16(fg.B) * uint16(alpha) / 255), alpha})
	default:
		quadrants := map[rune]uint8{'▖': 4, '▗': 8, '▘': 1, '▙': 13, '▚': 9, '▛': 7, '▜': 11, '▝': 2, '▞': 6, '▟': 14}
		bits, ok := quadrants[r]
		if !ok {
			return false
		}
		for i := 0; i < 4; i++ {
			if bits&(1<<i) != 0 {
				fillRect(img, fractionRect(rect, float64(i%2)/2, float64(i/2)/2, .5, .5), fg)
			}
		}
	}
	return true
}

func renderBox(img *image.RGBA, rect image.Rectangle, r rune, fg color.RGBA) bool {
	// Sides are up, right, down, left; 1=light, 2=heavy, 3=double.
	sides := map[rune][4]int{
		'─': {0, 1, 0, 1}, '━': {0, 2, 0, 2}, '│': {1, 0, 1, 0}, '┃': {2, 0, 2, 0},
		'┌': {0, 1, 1, 0}, '┐': {0, 0, 1, 1}, '└': {1, 1, 0, 0}, '┘': {1, 0, 0, 1},
		'├': {1, 1, 1, 0}, '┤': {1, 0, 1, 1}, '┬': {0, 1, 1, 1}, '┴': {1, 1, 0, 1}, '┼': {1, 1, 1, 1},
		'╴': {0, 0, 0, 1}, '╵': {1, 0, 0, 0}, '╶': {0, 1, 0, 0}, '╷': {0, 0, 1, 0},
		'╸': {0, 0, 0, 2}, '╹': {2, 0, 0, 0}, '╺': {0, 2, 0, 0}, '╻': {0, 0, 2, 0},
		'═': {0, 3, 0, 3}, '║': {3, 0, 3, 0}, '╔': {0, 3, 3, 0}, '╗': {0, 0, 3, 3}, '╚': {3, 3, 0, 0}, '╝': {3, 0, 0, 3}, '╬': {3, 3, 3, 3},
	}
	strokes, ok := sides[r]
	if !ok {
		return false
	}
	light := max(1, int(math.Round(float64(min(rect.Dx(), rect.Dy()))/10)))
	cx, cy := rect.Min.X+rect.Dx()/2, rect.Min.Y+rect.Dy()/2
	for side, kind := range strokes {
		if kind == 0 {
			continue
		}
		thickness := light
		if kind == 2 {
			thickness *= 2
		}
		offsets := []int{0}
		if kind == 3 {
			offsets = []int{-light, light}
		}
		for _, offset := range offsets {
			var stroke image.Rectangle
			if side%2 == 0 {
				top, bottom := rect.Min.Y, cy+1
				if side == 2 {
					top, bottom = cy, rect.Max.Y
				}
				stroke = image.Rect(cx+offset-thickness/2, top, cx+offset-thickness/2+thickness, bottom)
			} else {
				left, right := rect.Min.X, cx+1
				if side == 1 {
					left, right = cx, rect.Max.X
				}
				stroke = image.Rect(left, cy+offset-thickness/2, right, cy+offset-thickness/2+thickness)
			}
			fillRect(img, stroke.Intersect(rect), fg)
		}
	}
	return true
}

func renderBraille(img *image.RGBA, rect image.Rectangle, r rune, fg color.RGBA) {
	width, height := float32(rect.Dx()), float32(rect.Dy())
	radius := max(float32(1), min(width/7, height/12))
	const circleBezier = float32(.55228475)
	dots := [][3]int{{0, 0, 1}, {0, 1, 2}, {0, 2, 4}, {1, 0, 8}, {1, 1, 16}, {1, 2, 32}, {0, 3, 64}, {1, 3, 128}}
	xs := [2]float32{width * .32, width * .68}
	ys := [4]float32{height * .18, height * .4, height * .62, height * .84}
	raster := vector.NewRasterizer(rect.Dx(), rect.Dy())
	for _, dot := range dots {
		if int(r-0x2800)&dot[2] == 0 {
			continue
		}
		x, y := xs[dot[0]], ys[dot[1]]
		c := radius * circleBezier
		raster.MoveTo(x+radius, y)
		raster.CubeTo(x+radius, y+c, x+c, y+radius, x, y+radius)
		raster.CubeTo(x-c, y+radius, x-radius, y+c, x-radius, y)
		raster.CubeTo(x-radius, y-c, x-c, y-radius, x, y-radius)
		raster.CubeTo(x+c, y-radius, x+radius, y-c, x+radius, y)
		raster.ClosePath()
	}
	raster.Draw(img, rect, image.NewUniform(fg), image.Point{})
}

func renderPowerline(img *image.RGBA, rect image.Rectangle, r rune, fg color.RGBA) bool {
	if !strings.ContainsRune("", r) {
		return false
	}
	w, h := float32(rect.Dx()), float32(rect.Dy())
	if r == '' || r == '' {
		thickness := max(float32(1), w/7)
		raster := vector.NewRasterizer(rect.Dx(), rect.Dy())
		for _, y := range []float32{0, h / 2} {
			x1, x2 := float32(0), w
			if r == '' {
				x1, x2 = x2, x1
			}
			if y > 0 {
				x1, x2 = x2, x1
			}
			dx, dy := x2-x1, h/2
			length := float32(math.Hypot(float64(dx), float64(dy)))
			nx, ny := dy/length*thickness/2, -dx/length*thickness/2
			raster.MoveTo(x1+nx, y+ny)
			raster.LineTo(x2+nx, y+h/2+ny)
			raster.LineTo(x2-nx, y+h/2-ny)
			raster.LineTo(x1-nx, y-ny)
			raster.ClosePath()
		}
		raster.Draw(img, rect, image.NewUniform(fg), image.Point{})
		return true
	}
	points := map[rune][3][2]float32{
		'': {{0, 0}, {w, h / 2}, {0, h}}, '': {{w, 0}, {0, h / 2}, {w, h}},
		'': {{0, 0}, {w, h}, {0, h}}, '': {{w, 0}, {w, h}, {0, h}},
		'': {{0, 0}, {w, 0}, {0, h}}, '': {{0, 0}, {w, 0}, {w, h}},
	}[r]
	raster := vector.NewRasterizer(rect.Dx(), rect.Dy())
	raster.MoveTo(points[0][0], points[0][1])
	raster.LineTo(points[1][0], points[1][1])
	raster.LineTo(points[2][0], points[2][1])
	raster.ClosePath()
	raster.Draw(img, rect, image.NewUniform(fg), image.Point{})
	return true
}
