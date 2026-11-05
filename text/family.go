package text

import (
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	gofont "github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/math/fixed"
)

// Family is an ordered list of fonts. Each rune uses the first font that has it, so
// a text can mix alphabets that no single font holds. A rune that no font has uses
// the primary font, which draws its missing glyph.
//
// A Family holds no style such as bold. To change the style of a part of a text,
// give that part a Span with another Family.
//
// A Family shapes with the primary font, so it is not safe for concurrent use.
type Family struct {
	fonts []*Font
}

// NewFamily returns a Family that tries primary first, then fallbacks in order. It
// panics if two fonts have the same source, because a Pack would then mix their
// glyphs: give a different source to Parse for each font.
func NewFamily(primary *Font, fallbacks ...*Font) *Family {
	fonts := append([]*Font{primary}, fallbacks...)
	for i, f := range fonts {
		for _, other := range fonts[:i] {
			if f != other && f.source == other.source {
				panic("text: NewFamily needs a different source for each font")
			}
		}
	}
	return &Family{fonts: fonts}
}

// Metrics returns the metrics of the primary font.
func (fam *Family) Metrics() Metrics { return fam.fonts[0].Metrics() }

// Span is a part of a text with its own style. Text can hold newlines. A ligature
// does not cross two spans, but the shape of a joining script does.
type Span struct {
	Text string
	// Family is the fonts of this part. nil is the Family that shapes the text.
	Family *Family
	// Scale is the size of this part, relative to one em of the text. 0 is 1.
	Scale float32
}

// style is a Span without its text. start and end are rune indices in all the text.
type style struct {
	index      int
	start, end int
	family     *Family
	scale      float32
}

// Shape returns the lines of s. See Font.Shape for the rules of a text.
func (fam *Family) Shape(s string, o Options) []Line {
	return fam.ShapeSpans([]Span{{Text: s}}, o)
}

// ShapeSpans returns the lines of the text of all the spans, in order. The Cluster,
// Start and End values index the runes of all that text, and Glyph.Span tells which
// span holds a glyph. Line breaks, bidi and alignment see one text.
func (fam *Family) ShapeSpans(spans []Span, o Options) []Line {
	text, styles := fam.styles(spans)
	primary := fam.fonts[0]

	config := shaping.WrapConfig{Direction: direction(o)}
	if o.MaxLines > 0 && o.Ellipsis != "" {
		runes := []rune(o.Ellipsis)
		input := primary.input(runes, o)
		input.Face = fam.fontWith(runes).face
		config.Truncator = primary.shaper.Shape(input)
	}

	var lines []Line
	offset := 0
	paragraphs := strings.Split(text, "\n")
	for i, paragraph := range paragraphs {
		if o.MaxLines > 0 {
			if len(lines) >= o.MaxLines {
				break
			}
			config.TruncateAfterLines = o.MaxLines - len(lines)
			config.TextContinues = i < len(paragraphs)-1
		}
		runes := []rune(paragraph)
		lines = append(lines, fam.shapeParagraph(runes, offset, styles, config, o)...)
		offset += len(runes) + 1
	}
	align(lines, o)
	return lines
}

// styles returns all the text, and the styles of the spans that are not empty.
func (fam *Family) styles(spans []Span) (string, []style) {
	styles := make([]style, 0, len(spans))
	start := 0
	for i, s := range spans {
		end := start + utf8.RuneCountInString(s.Text)
		if end == start {
			continue
		}
		family, scale := s.Family, s.Scale
		if family == nil {
			family = fam
		}
		if !(scale > 0) {
			scale = 1
		}
		styles = append(styles, style{i, start, end, family, scale})
		start = end
	}
	if len(styles) == 1 {
		return spans[styles[0].index].Text, styles
	}
	var text strings.Builder
	for _, s := range styles {
		text.WriteString(spans[s.index].Text)
	}
	return text.String(), styles
}

// styleAt returns the style that holds the rune at index. An index after the last
// style uses that style.
func styleAt(styles []style, index int) style {
	i, _ := slices.BinarySearchFunc(styles, index, func(s style, index int) int {
		if s.end <= index {
			return -1
		}
		if s.start > index {
			return 1
		}
		return 0
	})
	return styles[min(i, len(styles)-1)]
}

// shapeParagraph returns the lines of a text without a newline. An empty text is
// one empty line. offset is the index of the first rune in all the text.
func (fam *Family) shapeParagraph(runes []rune, offset int, styles []style, config shaping.WrapConfig, o Options) []Line {
	primary := fam.fonts[0]
	truncator := config.Truncator.Glyphs
	place := func(runs shaping.Line) Line {
		line := layOut(runs, offset, truncator, fam, styles)
		// The primary font always counts, so a line of fallback glyphs has a stable height.
		line.Metrics = primary.Metrics()
		for _, run := range runs {
			line.Metrics = line.union(fam.fontOf(run.Face, styles).Metrics().scaled(runScale(run)))
		}
		return line
	}
	if len(runes) == 0 {
		// The wrapper needs text, so the last empty line adds the ellipsis here.
		if config.TruncateAfterLines == 1 && config.TextContinues && len(truncator) > 0 {
			mark := config.Truncator
			mark.Runes = shaping.Range{}
			return []Line{place(shaping.Line{mark})}
		}
		return []Line{{Start: offset, End: offset, Metrics: primary.Metrics()}}
	}
	var runs []shaping.Output
	for _, part := range fam.inputs(runes, offset, styles, o) {
		runs = append(runs, primary.shaper.Shape(part))
	}

	limit := math.MaxInt32 >> 6
	if o.Width > 0 {
		limit = int(o.Width * shapeSize)
	}
	wrapped, _ := primary.wrapper.WrapParagraph(config, limit, runes, shaping.NewSliceIterator(runs))

	stops := primary.graphemes(runes, offset)
	lines := make([]Line, len(wrapped))
	for i, runs := range wrapped {
		lines[i] = place(runs)
		lines[i].stops = stops
		lines[i].ligatures = ligatures(lines[i].Glyphs)
	}
	return lines
}

// inputs cuts a paragraph into the runs of the shaper. Bidi and scripts come from
// the whole paragraph, so a span does not change the direction of its neighbors.
func (fam *Family) inputs(runes []rune, offset int, styles []style, o Options) []shaping.Input {
	primary := fam.fonts[0]
	var out []shaping.Input
	for _, run := range primary.segments.Split(primary.input(runes, o), oneFace{primary.face}) {
		for start := run.RunStart; start < run.RunEnd; {
			s := styleAt(styles, offset+start)
			piece := run
			piece.RunStart, piece.RunEnd = start, min(run.RunEnd, s.end-offset)
			piece.Size = fixed.Int26_6(s.scale * shapeSize * 64)
			out = s.family.splitByFont(piece, out)
			start = piece.RunEnd
		}
	}
	return out
}

// splitByFont appends the parts of input that use one font each.
func (fam *Family) splitByFont(input shaping.Input, out []shaping.Input) []shaping.Input {
	if len(fam.fonts) == 1 {
		input.Face = fam.fonts[0].face
		return append(out, input)
	}
	return append(out, shaping.SplitByFace(input, &fallback{fonts: fam.fonts})...)
}

// fontWith returns the first font that has all the runes, or else the primary font.
func (fam *Family) fontWith(runes []rune) *Font {
	for _, f := range fam.fonts {
		missing := slices.ContainsFunc(runes, func(r rune) bool { return !hasRune(f.face, r) })
		if !missing {
			return f
		}
	}
	return fam.fonts[0]
}

// fontOf returns the font of a run of this text.
func (fam *Family) fontOf(face *gofont.Face, styles []style) *Font {
	for _, f := range fam.fonts {
		if f.face == face {
			return f
		}
	}
	for _, s := range styles {
		for _, f := range s.family.fonts {
			if f.face == face {
				return f
			}
		}
	}
	return fam.fonts[0]
}

// fallback implements shaping.Fontmap. The shaper calls it for the runes in order.
type fallback struct {
	fonts []*Font
	last  *gofont.Face
}

// ResolveFace keeps a mark with its base if it can, because the shaper can place a
// mark only on a base of the same font.
func (b *fallback) ResolveFace(r rune) *gofont.Face {
	mark := b.last != nil && unicode.Is(unicode.M, r)
	if mark && hasRune(b.last, r) {
		return b.last
	}
	for _, f := range b.fonts {
		if hasRune(f.face, r) {
			b.last = f.face
			return f.face
		}
	}
	if mark {
		return b.last
	}
	return b.fonts[0].face
}

func hasRune(face *gofont.Face, r rune) bool {
	_, ok := face.NominalGlyph(r)
	return ok
}

// runScale returns the scale of a run from its size.
func runScale(run shaping.Output) float32 {
	return float32(run.Size) / (64 * shapeSize)
}

// scaled returns m at a size relative to one em.
func (m Metrics) scaled(scale float32) Metrics {
	return Metrics{m.Ascent * scale, m.Descent * scale, m.Gap * scale}
}

// union returns the metrics of a line that holds both m and other.
func (m Metrics) union(other Metrics) Metrics {
	return Metrics{max(m.Ascent, other.Ascent), min(m.Descent, other.Descent), max(m.Gap, other.Gap)}
}
