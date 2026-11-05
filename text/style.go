package text

import (
	gofont "github.com/go-text/typesetting/font"

	slug "github.com/dhannyell/go-slug"
)

// Style changes the outline of each glyph. It does not change the advances or the
// metrics, so a styled font fits the line of its parsed font. All sizes are in em.
type Style struct {
	// Bold moves each edge of a glyph out. 0.01 looks like a medium weight.
	Bold float32
	// Slant moves each point to the right by this value for each unit of Y. 0.2 is
	// about 11 degrees.
	Slant float32
	// Outline is the width of a line along the edges. A value over 0 draws only this
	// line, without the fill.
	Outline float32
	// Join is the shape of the corners of Outline.
	Join slug.Join
}

// Styled returns a font with the same glyph IDs and the outlines of s. A real bold or
// italic font looks better, because a designer drew it: use Styled when there is none.
//
// source is the Key.Source of the new font in a Pack. It must differ from the source
// of f, because the two have different outlines for one ID. Styled panics if it does
// not. Style replaces the style of f, and does not add to it.
//
// To draw a fill and an outline, shape with one font and Add each glyph to each font.
func (f *Font) Styled(s Style, source uint32) *Font {
	if source == f.source {
		panic("text: Styled needs a different source")
	}
	// A new face, because a Family finds the font of a run by its face.
	styled := &Font{
		outlines: f.outlines,
		face:     gofont.NewFace(f.face.Font),
		source:   source,
		base:     f.parsed(),
		style:    s,
	}
	styled.self.fonts = []*Font{styled}
	return styled
}

// parsed returns the font without a style.
func (f *Font) parsed() *Font {
	if f.base != nil {
		return f.base
	}
	return f
}

// apply returns d with the style. d must have no other user.
func (s Style) apply(d slug.Drawing) (slug.Drawing, error) {
	var err error
	if s.Bold > 0 {
		if d, err = d.Embolden(s.Bold * d.Em); err != nil {
			return d, err
		}
	}
	if s.Outline > 0 {
		if d, err = d.Stroke(slug.Stroke{Width: s.Outline * d.Em, Join: s.Join}); err != nil {
			return d, err
		}
	}
	if s.Slant != 0 {
		slant := slug.Affine{1, 0, s.Slant, 1, 0, 0}
		for i := range d.Layers {
			d.Layers[i].Transform = &slant
		}
	}
	return d, nil
}
