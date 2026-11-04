// Package text turns a string into positioned glyphs of one font, with kerning,
// ligatures, right-to-left text, line breaks and alignment. HarfBuzz rules come
// from go-text/typesetting, so this package is a separate module: the slug core
// does not depend on a shaper.
//
//	f, err := text.Parse(fontData, 0)
//	for _, line := range f.Shape("office", text.Options{}) {
//		for _, g := range line.Glyphs {
//			entry, err := f.Add(&pack, g)
//			// Draw entry with g.At(size), then move by line.Height() * size.
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
	"github.com/go-text/typesetting/harfbuzz"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/segmenter"
	"github.com/go-text/typesetting/shaping"
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
	breaks   segmenter.Segmenter
	carets   *harfbuzz.Font // made at the first ligature; reads the GDEF table
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

// Metrics is the vertical size of a line, in em, with Y up. Descent is usually
// negative.
type Metrics struct {
	Ascent, Descent, Gap float32
}

// Height returns the distance from one baseline to the next.
func (m Metrics) Height() float32 { return m.Ascent - m.Descent + m.Gap }

// Metrics returns the metrics of a line of this font.
func (f *Font) Metrics() Metrics {
	e, _ := f.face.FontHExtents()
	upem := float32(f.face.Upem())
	return Metrics{e.Ascender / upem, e.Descender / upem, e.LineGap / upem}
}

// Align selects the position of a line in its width.
type Align uint8

const (
	AlignStart  Align = iota // at the side where the paragraph starts: left, or right if RTL
	AlignCenter              // in the middle
	AlignEnd                 // at the side where the paragraph ends: right, or left if RTL
)

// Options controls Shape. The zero value is a left-to-right paragraph with no
// limit on the width of a line.
type Options struct {
	RTL      bool    // the base direction of the paragraph
	Width    float32 // the limit of a line, in em; 0 is no limit
	Language string  // a BCP 47 tag that selects the rules of a language; "" is none
	// Align positions each line in Width. If Width is 0, it uses the widest line.
	Align Align
	// MaxLines is the limit on all the lines of the text; 0 is no limit.
	MaxLines int
	// Ellipsis goes at the end of the last line if MaxLines removes text. "" is none.
	Ellipsis string
}

// Glyph is one glyph at its position on a line. X and Y are in em, from the
// start of the baseline of the line, with Y up.
type Glyph struct {
	ID      uint32
	X, Y    float32
	Advance float32 // the distance to the next glyph on the line, in em
	Cluster int     // the index of the first rune of this glyph, in the runes of the text
	Runes   int     // the count of runes of the cluster; more than 1 for a ligature
	RTL     bool    // the glyph is in a right-to-left run

	pen float32 // X without the offset of the shaper, for a caret
}

// Line is one line of glyphs, in the order from left to right on the screen.
type Line struct {
	Glyphs []Glyph
	Width  float32 // in em
	Left   float32 // the X where the line starts, in em, after Align
	Start  int     // the index of the first rune of this line
	End    int     // the index after the last rune; it includes runes of the paragraph that an ellipsis removed
	Metrics

	stops     []int // the first rune of each grapheme of the paragraph, in order
	ligatures []ligature
}

// ligature is the carets that a font gives for the one spacing glyph of a cluster.
type ligature struct {
	cluster int
	x       []float32 // in em, from left to right; nil if the font has no usable carets
}

// oneFace implements shaping.Fontmap: all runes use the one face.
type oneFace struct{ face *gofont.Face }

func (o oneFace) ResolveFace(rune) *gofont.Face { return o.face }

// Shape returns the lines of s. A newline in s starts a new paragraph, and the
// base direction applies to each paragraph.
func (f *Font) Shape(s string, o Options) []Line {
	config := shaping.WrapConfig{Direction: direction(o)}
	if o.MaxLines > 0 && o.Ellipsis != "" {
		runes := []rune(o.Ellipsis)
		config.Truncator = f.shaper.Shape(f.input(runes, o))
	}

	var lines []Line
	offset := 0
	paragraphs := strings.Split(s, "\n")
	for i, paragraph := range paragraphs {
		if o.MaxLines > 0 {
			if len(lines) >= o.MaxLines {
				break
			}
			config.TruncateAfterLines = o.MaxLines - len(lines)
			config.TextContinues = i < len(paragraphs)-1
		}
		runes := []rune(paragraph)
		lines = append(lines, f.shapeParagraph(runes, offset, config, o)...)
		offset += len(runes) + 1
	}
	align(lines, o)
	return lines
}

func direction(o Options) di.Direction {
	if o.RTL {
		return di.DirectionRTL
	}
	return di.DirectionLTR
}

func (f *Font) input(runes []rune, o Options) shaping.Input {
	return shaping.Input{
		Text: runes, RunEnd: len(runes), Direction: direction(o), Face: f.face,
		Size: fixed.I(shapeSize), Language: language.NewLanguage(o.Language),
	}
}

// shapeParagraph returns the lines of a text without a newline. An empty text is
// one empty line. offset is the index of the first rune in the text of Shape.
func (f *Font) shapeParagraph(runes []rune, offset int, config shaping.WrapConfig, o Options) []Line {
	truncator := config.Truncator.Glyphs
	if len(runes) == 0 {
		// The wrapper needs text, so the last empty line adds the ellipsis here.
		if config.TruncateAfterLines == 1 && config.TextContinues && len(truncator) > 0 {
			mark := config.Truncator
			mark.Runes = shaping.Range{}
			line := layOut(shaping.Line{mark}, offset, truncator)
			line.Metrics = f.Metrics()
			return []Line{line}
		}
		return []Line{{Start: offset, End: offset, Metrics: f.Metrics()}}
	}
	var runs []shaping.Output
	for _, part := range f.segments.Split(f.input(runes, o), oneFace{f.face}) {
		runs = append(runs, f.shaper.Shape(part))
	}

	limit := math.MaxInt32 >> 6
	if o.Width > 0 {
		limit = int(o.Width * shapeSize)
	}
	wrapped, _ := f.wrapper.WrapParagraph(config, limit, runes, shaping.NewSliceIterator(runs))

	stops := f.graphemes(runes, offset)
	lines := make([]Line, len(wrapped))
	for i, runs := range wrapped {
		lines[i] = layOut(runs, offset, truncator)
		lines[i].Metrics = f.Metrics()
		lines[i].stops = stops
		lines[i].ligatures = f.ligatures(lines[i].Glyphs)
	}
	return lines
}

// ligatures reads the carets of the glyphs that hold more than one rune. A cluster
// with two spacing glyphs has no carets, because the carets of one do not cover both.
func (f *Font) ligatures(glyphs []Glyph) []ligature {
	var out []ligature
	for _, g := range glyphs {
		if g.Runes < 2 || g.Advance <= 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].cluster == g.Cluster {
			out[n-1].x = nil
			continue
		}
		out = append(out, ligature{g.Cluster, f.caretsOf(g)})
	}
	return out
}

// caretsOf returns the carets of g, or nil if they are not in order inside the glyph.
// The font is input data, so its values can be wrong.
func (f *Font) caretsOf(g Glyph) []float32 {
	if f.carets == nil {
		f.carets = harfbuzz.NewFont(f.face)
		f.carets.XScale, f.carets.YScale = int32(f.face.Upem()), int32(f.face.Upem())
	}
	carets := f.carets.GetOTLigatureCarets(harfbuzz.LeftToRight, harfbuzz.GID(g.ID))
	if len(carets) == 0 {
		return nil
	}
	x := make([]float32, len(carets))
	last := g.X
	for i, c := range carets {
		x[i] = g.X + float32(c)/float32(f.face.Upem())
		if x[i] <= last || x[i] >= g.X+g.Advance {
			return nil
		}
		last = x[i]
	}
	return x
}

// graphemes returns the index of the first rune of each grapheme in runes.
func (f *Font) graphemes(runes []rune, offset int) []int {
	f.breaks.Init(runes)
	var stops []int
	for it := f.breaks.GraphemeIterator(); it.Next(); {
		stops = append(stops, offset+it.Grapheme().Offset)
	}
	return stops
}

// layOut places the runs of one line. A line holds its runs in the order of the
// text, and VisualIndex gives their order on the screen. truncator is the glyphs of
// the ellipsis: the wrapper shares them, and their clusters index the ellipsis.
func layOut(runs shaping.Line, offset int, truncator []shaping.Glyph) Line {
	runs = slices.Clone(runs)
	slices.SortFunc(runs, func(a, b shaping.Output) int { return int(a.VisualIndex - b.VisualIndex) })

	var line Line
	var pen fixed.Int26_6
	for i, run := range runs {
		start, end := offset+run.Runes.Offset, offset+run.Runes.Offset+run.Runes.Count
		if i == 0 {
			line.Start, line.End = start, end
		}
		line.Start, line.End = min(line.Start, start), max(line.End, end)

		isTruncator := len(truncator) > 0 && len(run.Glyphs) > 0 && &run.Glyphs[0] == &truncator[0]
		rtl := run.Direction.Progression() == di.TowardTopLeft
		for _, g := range run.Glyphs {
			cluster, count := offset+g.TextIndex(), g.RunesCount()
			if isTruncator {
				cluster, count = start, run.Runes.Count
			}
			line.Glyphs = append(line.Glyphs, Glyph{
				ID:      uint32(g.GlyphID),
				X:       em(pen + g.XOffset),
				Y:       em(g.YOffset),
				Advance: em(g.Advance),
				Cluster: cluster,
				Runes:   count,
				RTL:     rtl,
				pen:     em(pen),
			})
			pen += g.Advance
		}
	}
	line.Width = em(pen)
	return line
}

// align moves each line to its position in the width.
func align(lines []Line, o Options) {
	box := o.Width
	if box <= 0 {
		for _, l := range lines {
			box = max(box, l.Width)
		}
	}
	right := (o.Align == AlignEnd) != o.RTL
	for i := range lines {
		l := &lines[i]
		switch {
		case o.Align == AlignCenter:
			l.Left = (box - l.Width) / 2
		case right:
			l.Left = box - l.Width
		}
		for j := range l.Glyphs {
			l.Glyphs[j].X += l.Left
			l.Glyphs[j].pen += l.Left
		}
		for _, lig := range l.ligatures {
			for j := range lig.x {
				lig.x[j] += l.Left
			}
		}
	}
}

func em(v fixed.Int26_6) float32 { return float32(v) / (64 * shapeSize) }

// cluster is the glyphs of one or more runes: a caret moves in it.
type cluster struct {
	start, runes int
	left, right  float32
	rtl          bool
	carets       []float32 // the carets of the font, from left to right
}

// stopX returns the X of caret k of count graphemes, in the order of the text. It uses
// the carets of the font only if they match the graphemes.
func (c cluster) stopX(k, count int) float32 {
	if k > 0 && k < count && len(c.carets) == count-1 {
		if c.rtl {
			return c.carets[count-1-k]
		}
		return c.carets[k-1]
	}
	return c.at(float32(k) / float32(max(count, 1)))
}

// at returns the X at the fraction t of the runes, in the order of the text.
func (c cluster) at(t float32) float32 {
	if c.rtl {
		return c.right - t*(c.right-c.left)
	}
	return c.left + t*(c.right-c.left)
}

func (l Line) clusters() []cluster {
	var out []cluster
	ligatures := l.ligatures
	for _, g := range l.Glyphs {
		if n := len(out); n > 0 && out[n-1].start == g.Cluster {
			out[n-1].right = g.pen + g.Advance
			continue
		}
		c := cluster{start: g.Cluster, runes: g.Runes, left: g.pen, right: g.pen + g.Advance, rtl: g.RTL}
		if len(ligatures) > 0 && ligatures[0].cluster == g.Cluster {
			c.carets = ligatures[0].x
			ligatures = ligatures[1:]
		}
		out = append(out, c)
	}
	return out
}

// stopsIn returns the position in l.stops of the first grapheme that starts in c,
// and the count of graphemes that start in c. A grapheme can use more than one cluster.
func (l Line) stopsIn(c cluster) (first, count int) {
	first, _ = slices.BinarySearch(l.stops, c.start)
	end, _ := slices.BinarySearch(l.stops, c.start+c.runes)
	return first, end - first
}

// graphemeStart returns the first rune of the grapheme that holds the rune at index.
func (l Line) graphemeStart(index int) int {
	i, found := slices.BinarySearch(l.stops, index)
	if found || i == 0 {
		return index
	}
	return l.stops[i-1]
}

// XOf returns the X of the caret before the rune at index, in em. An index inside a
// grapheme moves to the start of the grapheme, because a caret cannot stop in it. In a
// ligature, XOf uses the carets of the font. If the font has none, it divides the width
// by the graphemes. It returns the start of the line for an index before Start, and the
// end of the line for an index from End.
func (l Line) XOf(index int) float32 {
	clusters := l.clusters()
	if len(clusters) == 0 {
		return l.Left
	}
	index = l.graphemeStart(index)
	first, last := clusters[0], clusters[0]
	for _, c := range clusters {
		if index >= c.start && index < c.start+c.runes {
			stop, count := l.stopsIn(c)
			at, _ := slices.BinarySearch(l.stops, index)
			return c.stopX(at-stop, count)
		}
		if c.start < first.start {
			first = c
		}
		if c.start+c.runes > last.start+last.runes {
			last = c
		}
	}
	if index < first.start {
		return first.at(0)
	}
	return last.at(1)
}

// ClusterAt returns the index of the grapheme after the caret that is nearest to x, for
// a pointer on the line. x is in em. The result can be End, after the last rune.
// Where two directions meet, one X has two carets, and the result is one of them.
func (l Line) ClusterAt(x float32) int {
	clusters := l.clusters()
	if len(clusters) == 0 {
		return l.Start
	}
	c := clusters[len(clusters)-1]
	for _, candidate := range clusters {
		if x < candidate.right {
			c = candidate
			break
		}
	}
	stop, count := l.stopsIn(c)
	if count == 0 {
		return l.graphemeStart(c.start)
	}
	nearest, distance := 0, float32(math.Inf(1))
	for k := range count + 1 {
		d := c.stopX(k, count) - x
		if d < 0 {
			d = -d
		}
		if d < distance {
			nearest, distance = k, d
		}
	}
	// At the end of c, the next grapheme is the next stop. It can be on the next line.
	if at := stop + nearest; at < len(l.stops) {
		return min(l.stops[at], l.End)
	}
	return l.End
}

// Add puts the outline of g into p, and returns its Entry. A glyph without
// contours, such as a space, has no layers.
func (f *Font) Add(p *slug.Pack, g Glyph) (slug.Entry, error) {
	key := slug.Key{Source: f.source, ID: g.ID}
	if e, ok := p.Lookup(key); ok {
		return e, nil
	}
	outline, err := f.outlines.Glyph(g.ID)
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
