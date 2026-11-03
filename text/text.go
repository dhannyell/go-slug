// Package text turns a string into positioned glyphs of one font, with kerning,
// ligatures, right-to-left text and line breaks. HarfBuzz rules come from
// go-text/typesetting, so this package is a separate module: the slug core does
// not depend on a shaper.
//
//	f, err := text.Parse(fontData, 0)
//	for _, line := range f.Shape("office", text.Options{}) {
//		for _, g := range line.Glyphs {
//			entry, err := f.Add(&pack, g)
//			// Draw entry with g.At(size), then move by the baseline of the line.
//		}
//	}
package text

import (
	"bytes"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/go-text/typesetting/di"
	gofont "github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	slug "github.com/dhannyell/go-slug"
	"github.com/dhannyell/go-slug/font"
)

// shapeSize is the size that the shaper uses. It keeps the fraction of an em in
// the 26.6 values of the shaper.
const shapeSize = 2048

// Font shapes text with one font, and reads the outlines of the result.
//
// A Font is not safe for concurrent use.
type Font struct {
	outlines *font.Font
	face     *gofont.Face
	source   uint32
	shaper   shaping.HarfbuzzShaper
	segments shaping.Segmenter
	wrapper  shaping.LineWrapper
}

// Parse parses a TrueType or OpenType font. source is the Key.Source of the
// glyphs of this font in a Pack: use a different value for each font.
func Parse(data []byte, source uint32) (*Font, error) {
	outlines, err := font.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("text: %w", err)
	}
	face, err := gofont.ParseTTF(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("text: %w", err)
	}
	return &Font{outlines: outlines, face: face, source: source}, nil
}

// Options controls Shape. The zero value is a left-to-right paragraph with no
// limit on the width of a line.
type Options struct {
	RTL      bool    // the base direction of the paragraph
	Width    float32 // the limit of a line, in em; 0 is no limit
	Language string  // a BCP 47 tag that selects the rules of a language; "" is none
}

// Glyph is one glyph at its position on a line. X and Y are in em, from the
// start of the baseline of the line, with Y up.
type Glyph struct {
	ID      uint32
	X, Y    float32
	Cluster int // the index of the first rune of this glyph, in the runes of the text
}

// Line is one line of glyphs, in the order from left to right on the screen.
type Line struct {
	Glyphs []Glyph
	Width  float32 // in em
}

// ResolveFace implements shaping.Fontmap: all runes use the one face.
func (f *Font) ResolveFace(rune) *gofont.Face { return f.face }

// Shape returns the lines of s. A newline in s starts a new paragraph, and the
// base direction applies to each paragraph.
func (f *Font) Shape(s string, o Options) []Line {
	var lines []Line
	offset := 0
	for paragraph := range strings.SplitSeq(s, "\n") {
		runes := []rune(paragraph)
		lines = append(lines, f.shapeParagraph(runes, offset, o)...)
		offset += len(runes) + 1
	}
	return lines
}

// shapeParagraph returns the lines of a text without a newline. An empty text is
// one empty line. offset is the index of the first rune in the text of Shape.
func (f *Font) shapeParagraph(runes []rune, offset int, o Options) []Line {
	if len(runes) == 0 {
		return []Line{{}}
	}
	direction := di.DirectionLTR
	if o.RTL {
		direction = di.DirectionRTL
	}
	input := shaping.Input{
		Text: runes, RunEnd: len(runes), Direction: direction, Face: f.face,
		Size: fixed.I(shapeSize), Language: language.NewLanguage(o.Language),
	}
	var runs []shaping.Output
	for _, part := range f.segments.Split(input, f) {
		runs = append(runs, f.shaper.Shape(part))
	}

	limit := math.MaxInt32 >> 6
	if o.Width > 0 {
		limit = int(o.Width * shapeSize)
	}
	wrapped, _ := f.wrapper.WrapParagraph(shaping.WrapConfig{Direction: direction}, limit, runes, shaping.NewSliceIterator(runs))

	lines := make([]Line, len(wrapped))
	for i, runs := range wrapped {
		lines[i] = layOut(runs, offset)
	}
	return lines
}

// layOut places the runs of one line. A line holds its runs in the order of the
// text, and VisualIndex gives their order on the screen.
func layOut(runs shaping.Line, offset int) Line {
	runs = slices.Clone(runs)
	slices.SortFunc(runs, func(a, b shaping.Output) int { return int(a.VisualIndex - b.VisualIndex) })

	var line Line
	var pen fixed.Int26_6
	for _, run := range runs {
		for _, g := range run.Glyphs {
			line.Glyphs = append(line.Glyphs, Glyph{
				ID:      uint32(g.GlyphID),
				X:       em(pen + g.XOffset),
				Y:       em(g.YOffset),
				Cluster: offset + g.TextIndex(),
			})
			pen += g.Advance
		}
	}
	line.Width = em(pen)
	return line
}

func em(v fixed.Int26_6) float32 { return float32(v) / (64 * shapeSize) }

// Add puts the outline of g into p, and returns its Entry. A glyph without
// contours, such as a space, has no layers.
func (f *Font) Add(p *slug.Pack, g Glyph) (slug.Entry, error) {
	key := slug.Key{Source: f.source, ID: g.ID}
	if e, ok := p.Lookup(key); ok {
		return e, nil
	}
	outline, err := f.outlines.Glyph(sfnt.GlyphIndex(g.ID))
	if err != nil {
		return slug.Entry{}, fmt.Errorf("text: %w", err)
	}
	return p.Add(key, outline.Drawing)
}

// At returns the matrix from the em space of g to a Y-down space, in which the
// line starts at the origin and one em has the given size.
func (g Glyph) At(size float32) slug.Affine {
	return slug.Affine{size, 0, 0, -size, g.X * size, -g.Y * size}
}
