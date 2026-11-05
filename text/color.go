package text

import (
	"fmt"
	"math"
	"slices"

	"github.com/go-text/typesetting/font/opentype/tables"

	slug "github.com/dhannyell/go-slug"
)

// foreground is the palette index of the color of the text.
const foreground = 0xFFFF

// none is a paint that draws nothing. A zero Paint is the foreground.
var none = slug.Paint{Kind: slug.Solid}

// maxPaintDepth stops a cycle of PaintColrGlyph tables in a bad font.
const maxPaintDepth = 64

// Palettes returns the count of color palettes. It is 0 for a font without color glyphs.
func (f *Font) Palettes() int {
	if f.face.Font.COLR == nil {
		return 0
	}
	return len(f.face.Font.CPAL)
}

// Palette returns a font that draws its color glyphs with palette i. Palette 0 is the
// default, and Add uses it for a parsed font.
//
// source is the Key.Source of the new font in a Pack. It must differ from the source
// of f, because the two have different colors for one ID. Palette panics if it does
// not.
func (f *Font) Palette(i int, source uint32) (*Font, error) {
	if i < 0 || i >= f.Palettes() {
		return nil, fmt.Errorf("text: palette %d is not in the %d palettes of the font", i, f.Palettes())
	}
	if source == f.source {
		panic("text: Palette needs a different source")
	}
	return f.variant(source, f.style, i), nil
}

// colorDrawing returns the color glyph id, and false if the font has none. approx
// counts the paints that the drawing does not follow exactly.
func (f *Font) colorDrawing(id uint32) (d slug.Drawing, approx int, ok bool, err error) {
	colr := f.face.Font.COLR
	if id > math.MaxUint16 || colr == nil || f.palette >= len(f.face.Font.CPAL) {
		return d, 0, false, nil
	}
	root, ok := colr.Search(tables.GlyphID(id))
	if !ok {
		return d, 0, false, nil
	}
	w := colorWalker{f: f, colr: colr, palette: f.face.Font.CPAL[f.palette]}
	w.drawing.Em = float32(f.face.Upem())
	if err := w.paint(root, slug.Identity(), 0); err != nil {
		return d, 0, true, fmt.Errorf("text: color glyph %d: %w", id, err)
	}
	return w.drawing, w.approx, true, nil
}

// colorWalker turns a tree of COLR paints into the layers of one Drawing.
type colorWalker struct {
	f       *Font
	colr    *tables.COLR1
	palette []tables.ColorRecord
	drawing slug.Drawing
	approx  int
}

// paint adds the layers of p. m maps the space of p to font units.
func (w *colorWalker) paint(p tables.PaintTable, m slug.Affine, depth int) error {
	if depth > maxPaintDepth {
		return fmt.Errorf("the paints are deeper than %d", maxPaintDepth)
	}
	p = withoutVariation(p)
	if t, child, ok := transformOf(p); ok {
		return w.paint(child, t.Then(m), depth+1)
	}
	switch p := p.(type) {
	case tables.PaintColrLayersResolved: // version 0
		for _, l := range p {
			if err := w.fill(uint32(l.GlyphID), tables.PaintSolid{PaletteIndex: l.PaletteIndex, Alpha: 1 << 14}, m, slug.Identity()); err != nil {
				return err
			}
		}
	case tables.PaintColrLayers:
		layers, err := w.colr.LayerList.Resolve(p)
		if err != nil {
			return err
		}
		for _, l := range layers {
			if err := w.paint(l, m, depth+1); err != nil {
				return err
			}
		}
	case tables.PaintColrGlyph:
		child, ok := w.colr.Search(tables.GlyphID(p.GlyphID))
		if !ok {
			return fmt.Errorf("no color glyph %d", p.GlyphID)
		}
		return w.paint(child, m, depth+1)
	case tables.PaintGlyph:
		return w.fill(uint32(p.GlyphID), p.Paint, m, slug.Identity())
	case tables.PaintComposite:
		// A composite mode needs a layer off the screen. The backdrop alone is closer
		// than a source over it.
		w.approx++
		return w.paint(p.BackdropPaint, m, depth+1)
	default:
		// A fill without a glyph covers the clip box. No real font needs it yet.
		w.approx++
	}
	return nil
}

// fill adds the outline of glyph id with the paint p. m maps font units of the glyph to
// font units of the color glyph; inner maps the space of p to the font units of the
// glyph.
func (w *colorWalker) fill(id uint32, p tables.PaintTable, m, inner slug.Affine) error {
	for {
		p = withoutVariation(p)
		t, child, ok := transformOf(p)
		if !ok {
			break
		}
		p, inner = child, t.Then(inner)
	}
	paint, ok := w.paintOf(p, inner)
	if !ok {
		// A glyph inside a glyph is a clip, and the core has none yet.
		w.approx++
		return nil
	}
	if paint == none {
		return nil
	}
	outline, err := w.f.outlines.Glyph(id)
	if err != nil {
		return err
	}
	for _, l := range outline.Drawing.Layers {
		l.Paint = paint
		if m != slug.Identity() {
			l.Transform = &m
		}
		w.drawing.Layers = append(w.drawing.Layers, l)
	}
	return nil
}

// paintOf returns the slug paint of a solid or a gradient. inner maps the space of p
// to the units of the layer.
func (w *colorWalker) paintOf(p tables.PaintTable, inner slug.Affine) (slug.Paint, bool) {
	var paint slug.Paint
	var line tables.ColorLine
	switch p := p.(type) {
	case tables.PaintSolid:
		if p.PaletteIndex == foreground {
			if p.Alpha != 1<<14 {
				w.approx++ // CurrentColor has no alpha of its own
			}
			return slug.Paint{Kind: slug.CurrentColor}, true
		}
		return slug.Paint{Kind: slug.Solid, Color: w.color(p.PaletteIndex, p.Alpha)}, true
	case tables.PaintLinearGradient:
		p0 := slug.Point{X: float32(p.X0), Y: float32(p.Y0)}
		p1 := slug.Point{X: float32(p.X1), Y: float32(p.Y1)}
		p2 := slug.Point{X: float32(p.X2), Y: float32(p.Y2)}
		// p2 turns the lines of one color: offset 1 is where the line p0p2 crosses
		// the line through p1.
		n := slug.Point{X: p0.Y - p2.Y, Y: p2.X - p0.X}
		nn := n.X*n.X + n.Y*n.Y
		d := (p1.X-p0.X)*n.X + (p1.Y-p0.Y)*n.Y
		if nn == 0 || d == 0 {
			return none, true // the specification forbids a draw
		}
		paint = slug.LinearPaint(p0, slug.Point{X: p0.X + n.X*d/nn, Y: p0.Y + n.Y*d/nn})
		line = p.ColorLine
	case tables.PaintRadialGradient:
		paint = slug.RadialPaint(
			slug.Point{X: float32(p.X0), Y: float32(p.Y0)}, float32(p.Radius0),
			slug.Point{X: float32(p.X1), Y: float32(p.Y1)}, float32(p.Radius1))
		line = p.ColorLine
	case tables.PaintSweepGradient:
		// The angles have a bias of one half turn.
		start := (f214(p.StartAngle) + 1) * math.Pi
		end := (f214(p.EndAngle) + 1) * math.Pi
		paint = slug.SweepPaint(slug.Point{X: float32(p.CenterX), Y: float32(p.CenterY)}, start, end)
		line = p.ColorLine
	default:
		return slug.Paint{}, false
	}

	g := paint.Gradient
	g.Spread = slug.Spread(line.Extend)
	g.Space = slug.LinearSRGB // the CPAL table requires linear light and premultiplied alpha
	g.Premultiplied = true
	if inner != slug.Identity() {
		g.Transform = &inner
	}
	g.Stops = make([]slug.Stop, len(line.ColorStops))
	for i, s := range line.ColorStops {
		if s.PaletteIndex == foreground {
			w.approx++ // a stop cannot hold the color of the host
		}
		g.Stops[i] = slug.Stop{Offset: f214(s.StopOffset), Color: w.color(s.PaletteIndex, s.Alpha)}
	}
	slices.SortStableFunc(g.Stops, func(a, b slug.Stop) int {
		return cmpFloat(a.Offset, b.Offset)
	})
	if len(g.Stops) == 0 {
		return none, true
	}
	return paint, true
}

// color returns entry i of the palette, with alpha multiplied in. The foreground and
// a bad index are black.
func (w *colorWalker) color(i uint16, alpha tables.Fixed214) [4]float32 {
	a := f214(alpha)
	if int(i) >= len(w.palette) {
		return [4]float32{0, 0, 0, a}
	}
	c := w.palette[i]
	return [4]float32{float32(c.Red) / 255, float32(c.Green) / 255, float32(c.Blue) / 255, float32(c.Alpha) / 255 * a}
}

// transformOf returns the matrix of a transform paint and its child.
func transformOf(p tables.PaintTable) (slug.Affine, tables.PaintTable, bool) {
	switch p := p.(type) {
	case tables.PaintTransform:
		t := p.Transform
		return slug.Affine{t.Xx, t.Yx, t.Xy, t.Yy, t.Dx, t.Dy}, p.Paint, true
	case tables.PaintTranslate:
		return slug.Affine{1, 0, 0, 1, float32(p.Dx), float32(p.Dy)}, p.Paint, true
	case tables.PaintScale:
		return slug.Affine{f214(p.ScaleX), 0, 0, f214(p.ScaleY), 0, 0}, p.Paint, true
	case tables.PaintScaleAroundCenter:
		return around(slug.Affine{f214(p.ScaleX), 0, 0, f214(p.ScaleY), 0, 0}, p.CenterX, p.CenterY), p.Paint, true
	case tables.PaintScaleUniform:
		s := f214(p.Scale)
		return slug.Affine{s, 0, 0, s, 0, 0}, p.Paint, true
	case tables.PaintScaleUniformAroundCenter:
		s := f214(p.Scale)
		return around(slug.Affine{s, 0, 0, s, 0, 0}, p.CenterX, p.CenterY), p.Paint, true
	case tables.PaintRotate:
		return rotation(p.Angle), p.Paint, true
	case tables.PaintRotateAroundCenter:
		return around(rotation(p.Angle), p.CenterX, p.CenterY), p.Paint, true
	case tables.PaintSkew:
		return skew(p.XSkewAngle, p.YSkewAngle), p.Paint, true
	case tables.PaintSkewAroundCenter:
		return around(skew(p.XSkewAngle, p.YSkewAngle), p.CenterX, p.CenterY), p.Paint, true
	}
	return slug.Affine{}, nil, false
}

// rotation turns counterclockwise by half turns.
func rotation(angle tables.Fixed214) slug.Affine {
	s, c := math.Sincos(float64(f214(angle)) * math.Pi)
	return slug.Affine{float32(c), float32(s), float32(-s), float32(c), 0, 0}
}

// skew follows the sign of the specification: a positive X angle leans the top to the left.
func skew(x, y tables.Fixed214) slug.Affine {
	return slug.Affine{1, float32(math.Tan(float64(f214(y)) * math.Pi)), float32(-math.Tan(float64(f214(x)) * math.Pi)), 1, 0, 0}
}

// around applies m with the point (cx, cy) fixed.
func around(m slug.Affine, cx, cy int16) slug.Affine {
	x, y := float32(cx), float32(cy)
	m[4] = x - (m[0]*x + m[2]*y)
	m[5] = y - (m[1]*x + m[3]*y)
	return m
}

// withoutVariation returns the default instance of a variable paint: the deltas of a
// variable font wait for the support of variations.
func withoutVariation(p tables.PaintTable) tables.PaintTable {
	switch p := p.(type) {
	case tables.PaintVarSolid:
		return tables.PaintSolid{PaletteIndex: p.PaletteIndex, Alpha: p.Alpha}
	case tables.PaintVarLinearGradient:
		return tables.PaintLinearGradient{ColorLine: plainLine(p.ColorLine), X0: p.X0, Y0: p.Y0, X1: p.X1, Y1: p.Y1, X2: p.X2, Y2: p.Y2}
	case tables.PaintVarRadialGradient:
		return tables.PaintRadialGradient{ColorLine: plainLine(p.ColorLine), X0: p.X0, Y0: p.Y0, Radius0: p.Radius0, X1: p.X1, Y1: p.Y1, Radius1: p.Radius1}
	case tables.PaintVarSweepGradient:
		return tables.PaintSweepGradient{ColorLine: plainLine(p.ColorLine), CenterX: p.CenterX, CenterY: p.CenterY, StartAngle: p.StartAngle, EndAngle: p.EndAngle}
	case tables.PaintVarTransform:
		t := p.Transform
		return tables.PaintTransform{Paint: p.Paint, Transform: tables.Affine2x3{Xx: t.Xx, Yx: t.Yx, Xy: t.Xy, Yy: t.Yy, Dx: t.Dx, Dy: t.Dy}}
	case tables.PaintVarTranslate:
		return tables.PaintTranslate{Paint: p.Paint, Dx: p.Dx, Dy: p.Dy}
	case tables.PaintVarScale:
		return tables.PaintScale{Paint: p.Paint, ScaleX: p.ScaleX, ScaleY: p.ScaleY}
	case tables.PaintVarScaleAroundCenter:
		return tables.PaintScaleAroundCenter{Paint: p.Paint, ScaleX: p.ScaleX, ScaleY: p.ScaleY, CenterX: p.CenterX, CenterY: p.CenterY}
	case tables.PaintVarScaleUniform:
		return tables.PaintScaleUniform{Paint: p.Paint, Scale: p.Scale}
	case tables.PaintVarScaleUniformAroundCenter:
		return tables.PaintScaleUniformAroundCenter{Paint: p.Paint, Scale: p.Scale, CenterX: p.CenterX, CenterY: p.CenterY}
	case tables.PaintVarRotate:
		return tables.PaintRotate{Paint: p.Paint, Angle: p.Angle}
	case tables.PaintVarRotateAroundCenter:
		return tables.PaintRotateAroundCenter{Paint: p.Paint, Angle: p.Angle, CenterX: p.CenterX, CenterY: p.CenterY}
	case tables.PaintVarSkew:
		return tables.PaintSkew{Paint: p.Paint, XSkewAngle: p.XSkewAngle, YSkewAngle: p.YSkewAngle}
	case tables.PaintVarSkewAroundCenter:
		return tables.PaintSkewAroundCenter{Paint: p.Paint, XSkewAngle: p.XSkewAngle, YSkewAngle: p.YSkewAngle, CenterX: p.CenterX, CenterY: p.CenterY}
	}
	return p
}

func plainLine(l tables.VarColorLine) tables.ColorLine {
	out := tables.ColorLine{Extend: l.Extend, ColorStops: make([]tables.ColorStop, len(l.ColorStops))}
	for i, s := range l.ColorStops {
		out.ColorStops[i] = tables.ColorStop{StopOffset: s.StopOffset, PaletteIndex: s.PaletteIndex, Alpha: s.Alpha}
	}
	return out
}

// f214 reads a 2.14 fixed point number.
func f214(v tables.Fixed214) float32 { return float32(v) / (1 << 14) }

func cmpFloat(a, b float32) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
