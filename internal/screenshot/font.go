package screenshot

import (
	"embed"
	"fmt"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

// Fonts and license texts are embedded so rendering never depends on host-installed fonts.
// Regular and fallback assets are unmodified ghostty-opentui@1.5.0 assets (the TS lockfile).
// Bold/italic variants are from ryanoasis/nerd-fonts patched-fonts/JetBrainsMono/Ligatures.
//
//go:embed fonts/*
var fontAssets embed.FS

var fontPaths = []string{
	"jetbrains-mono-nerd.ttf", "jetbrains-mono-nerd-bold.ttf",
	"jetbrains-mono-nerd-italic.ttf", "jetbrains-mono-nerd-bolditalic.ttf",
	"symbols-nerd-font-mono-regular.ttf", "noto-sans-regular.ttf",
	"noto-sans-symbols-regular.ttf", "noto-sans-symbols-2-regular.ttf",
	"noto-sans-cjk-sc-regular.otf",
}

var parsedFonts struct {
	once  sync.Once
	fonts []*opentype.Font
	err   error
}

func parseFonts() ([]*opentype.Font, error) {
	parsedFonts.once.Do(func() {
		for _, path := range fontPaths {
			data, err := fontAssets.ReadFile("fonts/" + path)
			if err != nil {
				parsedFonts.err = fmt.Errorf("reading embedded font: %w", err)
				return
			}
			f, err := opentype.Parse(data)
			if err != nil {
				parsedFonts.err = fmt.Errorf("parsing embedded font %s: %w", path, err)
				return
			}
			parsedFonts.fonts = append(parsedFonts.fonts, f)
		}
	})
	return parsedFonts.fonts, parsedFonts.err
}

type fontFaces []font.Face

func loadFaces(size float64) (fontFaces, error) {
	fonts, err := parseFonts()
	if err != nil {
		return nil, err
	}
	faces := make(fontFaces, 0, len(fonts))
	for _, f := range fonts {
		face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
		if err != nil {
			faces.close()
			return nil, fmt.Errorf("creating terminal font face: %w", err)
		}
		faces = append(faces, face)
	}
	return faces, nil
}

func (f fontFaces) close() {
	for _, face := range f {
		_ = face.Close() /* OpenType faces have no resources and Close returns nil. */
	}
}

func (f fontFaces) selectFace(cellBold, cellItalic bool, r rune) font.Face {
	i := 0
	if cellBold {
		i++
	}
	if cellItalic {
		i += 2
	}
	if _, ok := f[i].GlyphAdvance(r); ok {
		return f[i]
	}
	for _, face := range f[4:] {
		if _, ok := face.GlyphAdvance(r); ok {
			return face
		}
	}
	return f[i]
}
