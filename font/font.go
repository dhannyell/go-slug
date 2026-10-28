// Package font reads glyph outlines from TrueType and OpenType fonts as
// slug Drawings.
package font

import (
	"fmt"

	"github.com/dhannyell/go-slug"
	xfont "golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Font reads glyphs from one font. Coordinates are in font units, Y-up.
//
// A Font is not safe for concurrent use.
type Font struct {
	sf  *sfnt.Font
	buf sfnt.Buffer
	// ppem makes sfnt return font units: it scales by ppem/unitsPerEm.
	ppem fixed.Int26_6
}

// Glyph is one glyph outline with its metrics, in font units.
type Glyph struct {
	Drawing slug.Drawing // no layers for a glyph without contours
	Advance float32
	Bounds  slug.Rect
}

// Parse parses a TrueType or OpenType font.
func Parse(data []byte) (*Font, error) {
	sf, err := sfnt.Parse(data)
	if err != nil {
		return nil, err
	}
	return FromSFNT(sf), nil
}

// FromSFNT wraps a font that the caller already parsed.
func FromSFNT(sf *sfnt.Font) *Font {
	return &Font{sf: sf, ppem: fixed.Int26_6(sf.UnitsPerEm())}
}

// UnitsPerEm returns the size of the em square in font units.
func (f *Font) UnitsPerEm() int {
	return int(f.sf.UnitsPerEm())
}

// GlyphIndex returns the glyph for r. It returns 0 (the missing glyph) when
// the font has no glyph for r.
func (f *Font) GlyphIndex(r rune) (sfnt.GlyphIndex, error) {
	return f.sf.GlyphIndex(&f.buf, r)
}

// Glyph returns the outline and metrics of glyph i.
func (f *Font) Glyph(i sfnt.GlyphIndex) (Glyph, error) {
	segs, err := f.sf.LoadGlyph(&f.buf, i, f.ppem, nil)
	if err != nil {
		return Glyph{}, fmt.Errorf("font: glyph %d: %w", i, err)
	}
	b, adv, err := f.sf.GlyphBounds(&f.buf, i, f.ppem, xfont.HintingNone)
	if err != nil {
		return Glyph{}, fmt.Errorf("font: glyph %d: %w", i, err)
	}

	g := Glyph{
		Advance: float32(adv),
		// sfnt is Y-down: flip, so the old Max.Y becomes the new Min.Y.
		Bounds: slug.Rect{
			Min: slug.Point{X: float32(b.Min.X), Y: -float32(b.Max.Y)},
			Max: slug.Point{X: float32(b.Max.X), Y: -float32(b.Min.Y)},
		},
	}
	if len(segs) == 0 {
		return g, nil
	}

	// sfnt leaves contours open; the Path contract closes them.
	var p slug.Path
	for _, s := range segs {
		a := s.Args
		switch s.Op {
		case sfnt.SegmentOpMoveTo:
			p.MoveTo(point(a[0]))
		case sfnt.SegmentOpLineTo:
			p.LineTo(point(a[0]))
		case sfnt.SegmentOpQuadTo:
			p.QuadTo(point(a[0]), point(a[1]))
		case sfnt.SegmentOpCubeTo:
			p.CubeTo(point(a[0]), point(a[1]), point(a[2]))
		}
	}
	g.Drawing.Layers = []slug.Layer{{Path: p}}
	return g, nil
}

func point(p fixed.Point26_6) slug.Point {
	return slug.Point{X: float32(p.X), Y: -float32(p.Y)}
}
