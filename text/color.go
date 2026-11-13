package text

import (
	"errors"
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
	w := colorWalker{f: f, colr: colr, palette: f.face.Font.CPAL[f.palette], coords: f.face.Coords(), alpha: 1}
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
	coords  []tables.Coord // the axes of the font; nil is the default instance
	drawing slug.Drawing
	approx  int

	clip  []slug.ClipPath  // the clip of the next layers
	alpha float32          // multiplies the alpha of the next layers
	mask  *[]slug.ClipPath // if set, the walk collects outlines here and draws nothing
}

// paint adds the layers of p. m maps the space of p to font units.
func (w *colorWalker) paint(p tables.PaintTable, m slug.Affine, depth int) error {
	if depth > maxPaintDepth {
		return fmt.Errorf("the paints are deeper than %d", maxPaintDepth)
	}
	p = w.vary(p)
	if t, child, ok := transformOf(p); ok {
		return w.paint(child, t.Then(m), depth+1)
	}
	switch p := p.(type) {
	case tables.PaintColrLayersResolved: // version 0
		for _, l := range p {
			if err := w.fill(uint32(l.GlyphID), tables.PaintSolid{PaletteIndex: l.PaletteIndex, Alpha: 1 << 14}, m, slug.Identity(), depth+1); err != nil {
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
		return w.fill(uint32(p.GlyphID), p.Paint, m, slug.Identity(), depth+1)
	case tables.PaintComposite:
		return w.composite(p, m, depth+1)
	default:
		w.flood(p, m)
	}
	return nil
}

// composite adds the layers of one composite mode. A mode that needs a layer off the
// screen draws the backdrop alone.
func (w *colorWalker) composite(p tables.PaintComposite, m slug.Affine, depth int) error {
	src, dst := p.SourcePaint, p.BackdropPaint
	draw := func(q tables.PaintTable) error { return w.paint(q, m, depth) }
	switch p.CompositeMode {
	case tables.CompositeClear:
		return nil
	case tables.CompositeSrc:
		return draw(src)
	case tables.CompositeDest:
		return draw(dst)
	case tables.CompositeSrcOver:
		return errors.Join(draw(dst), draw(src))
	case tables.CompositeDestOver:
		return errors.Join(draw(src), draw(dst))
	case tables.CompositeSrcIn:
		return w.inside(src, dst, m, depth)
	case tables.CompositeDestIn:
		return w.inside(dst, src, m, depth)
	case tables.CompositeSrcAtop:
		return errors.Join(draw(dst), w.inside(src, dst, m, depth))
	case tables.CompositeDestAtop:
		return errors.Join(draw(src), w.inside(dst, src, m, depth))
	}
	w.approx++
	return draw(dst)
}

// inside adds the layers of content where mask covers. It is exact for an opaque mask.
func (w *colorWalker) inside(content, mask tables.PaintTable, m slug.Affine, depth int) error {
	q := w.vary(mask)
	for {
		_, child, ok := transformOf(q)
		if !ok {
			break
		}
		q = w.vary(child)
	}
	// A solid without a glyph covers all: it changes only the alpha.
	if s, ok := q.(tables.PaintSolid); ok {
		saved := w.alpha
		w.alpha *= w.color(s.PaletteIndex, s.Alpha)[3]
		err := w.paint(content, m, depth)
		w.alpha = saved
		return err
	}

	var shapes []slug.ClipPath
	savedMask, savedClip, savedAlpha := w.mask, w.clip, w.alpha
	w.mask, w.clip, w.alpha = &shapes, nil, 1
	err := w.paint(mask, m, depth)
	w.mask, w.clip, w.alpha = savedMask, savedClip, savedAlpha
	if err != nil || len(shapes) == 0 {
		return err // an empty mask hides the content
	}
	if w.mask != nil || len(shapes) > slug.MaxClipPaths {
		// A mask inside a mask, or a large mask, keeps all of the content.
		w.approx++
		return w.paint(content, m, depth)
	}
	return w.within(shapes, func() error { return w.paint(content, m, depth) })
}

// within runs add with clip on the next layers. The core has one clip for each layer,
// so an inner clip replaces an outer clip.
func (w *colorWalker) within(clip []slug.ClipPath, add func() error) error {
	saved := w.clip
	if saved != nil {
		w.approx++
	}
	w.clip = clip
	err := add()
	w.clip = saved
	return err
}

// flood adds a paint without a glyph. It covers the clip; without a clip it would
// cover the clip box, and no real font needs that yet.
func (w *colorWalker) flood(p tables.PaintTable, m slug.Affine) {
	paint, ok := w.paintOf(p, m)
	if !ok || w.clip == nil || w.mask != nil {
		w.approx++
		return
	}
	if paint == none {
		return
	}
	box := clipBounds(w.clip)
	var path slug.Path
	path.MoveTo(box.Min)
	path.LineTo(slug.Point{X: box.Max.X, Y: box.Min.Y})
	path.LineTo(box.Max)
	path.LineTo(slug.Point{X: box.Min.X, Y: box.Max.Y})
	path.Close()
	w.drawing.Layers = append(w.drawing.Layers, slug.Layer{Path: path, Paint: w.fade(paint), Clip: w.clip})
}

// fill adds the outline of glyph id with the paint p. m maps font units of the glyph to
// font units of the color glyph; inner maps the space of p to the font units of the
// glyph.
func (w *colorWalker) fill(id uint32, p tables.PaintTable, m, inner slug.Affine, depth int) error {
	for {
		p = w.vary(p)
		t, child, ok := transformOf(p)
		if !ok {
			break
		}
		p, inner = child, t.Then(inner)
	}
	outline, err := w.f.outline(id)
	if err != nil {
		return err
	}
	var tm *slug.Affine
	if m != slug.Identity() {
		tm = &m
	}
	paint, ok := w.paintOf(p, inner)
	if !ok {
		// Other paints inside a glyph draw only inside the glyph.
		var clip []slug.ClipPath
		for _, l := range outline.Layers {
			clip = append(clip, slug.ClipPath{Path: l.Path, Transform: tm})
		}
		if clip == nil {
			return nil
		}
		return w.within(clip, func() error { return w.paint(p, inner.Then(m), depth+1) })
	}
	if paint == none {
		return nil
	}
	for _, l := range outline.Layers {
		if w.mask != nil {
			if !opaque(paint) {
				w.approx++ // the clip has no alpha
			}
			*w.mask = append(*w.mask, slug.ClipPath{Path: l.Path, Transform: tm})
			continue
		}
		l.Paint = w.fade(paint)
		l.Transform = tm
		l.Clip = w.clip
		w.drawing.Layers = append(w.drawing.Layers, l)
	}
	return nil
}

// fade multiplies the alpha of p by the alpha of the walk.
func (w *colorWalker) fade(p slug.Paint) slug.Paint {
	if w.alpha == 1 {
		return p
	}
	switch p.Kind {
	case slug.CurrentColor:
		w.approx++ // CurrentColor has no alpha of its own
	case slug.Solid:
		p.Color[3] *= w.alpha
	default:
		g := *p.Gradient
		g.Stops = slices.Clone(g.Stops)
		for i := range g.Stops {
			g.Stops[i].Color[3] *= w.alpha
		}
		p.Gradient = &g
	}
	return p
}

// opaque reports a paint without transparent parts. The color of the host counts as opaque.
func opaque(p slug.Paint) bool {
	switch p.Kind {
	case slug.CurrentColor:
		return true
	case slug.Solid:
		return p.Color[3] == 1
	}
	for _, s := range p.Gradient.Stops {
		if s.Color[3] != 1 {
			return false
		}
	}
	return true
}

// clipBounds returns a box around the points of the clip paths.
func clipBounds(clip []slug.ClipPath) slug.Rect {
	first := true
	var r slug.Rect
	for _, c := range clip {
		for _, q := range c.Path.Points {
			if c.Transform != nil {
				q = c.Transform.Apply(q)
			}
			if first {
				r, first = slug.Rect{Min: q, Max: q}, false
				continue
			}
			r.Min = slug.Point{X: min(r.Min.X, q.X), Y: min(r.Min.Y, q.Y)}
			r.Max = slug.Point{X: max(r.Max.X, q.X), Y: max(r.Max.Y, q.Y)}
		}
	}
	return r
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

// vary returns the plain paint of a variable paint, with the deltas of the axes of the
// font added. Each value keeps the type of its field, as the specification requires.
func (w *colorWalker) vary(p tables.PaintTable) tables.PaintTable {
	switch p := p.(type) {
	case tables.PaintVarSolid:
		d := w.deltas(p.VarIndexBase, 1)
		return tables.PaintSolid{PaletteIndex: p.PaletteIndex, Alpha: addF214(p.Alpha, d[0])}
	case tables.PaintVarLinearGradient:
		d := w.deltas(p.VarIndexBase, 6)
		return tables.PaintLinearGradient{ColorLine: w.line(p.ColorLine),
			X0: addI16(p.X0, d[0]), Y0: addI16(p.Y0, d[1]), X1: addI16(p.X1, d[2]), Y1: addI16(p.Y1, d[3]), X2: addI16(p.X2, d[4]), Y2: addI16(p.Y2, d[5])}
	case tables.PaintVarRadialGradient:
		d := w.deltas(p.VarIndexBase, 6)
		return tables.PaintRadialGradient{ColorLine: w.line(p.ColorLine),
			X0: addI16(p.X0, d[0]), Y0: addI16(p.Y0, d[1]), Radius0: addU16(p.Radius0, d[2]),
			X1: addI16(p.X1, d[3]), Y1: addI16(p.Y1, d[4]), Radius1: addU16(p.Radius1, d[5])}
	case tables.PaintVarSweepGradient:
		d := w.deltas(p.VarIndexBase, 4)
		return tables.PaintSweepGradient{ColorLine: w.line(p.ColorLine), CenterX: addI16(p.CenterX, d[0]), CenterY: addI16(p.CenterY, d[1]),
			StartAngle: addF214(p.StartAngle, d[2]), EndAngle: addF214(p.EndAngle, d[3])}
	case tables.PaintVarTransform:
		t := p.Transform
		d := w.deltas(t.VarIndexBase, 6)
		const unit = 1 << 16 // a delta of a 16.16 field
		return tables.PaintTransform{Paint: p.Paint, Transform: tables.Affine2x3{
			Xx: t.Xx + d[0]/unit, Yx: t.Yx + d[1]/unit, Xy: t.Xy + d[2]/unit, Yy: t.Yy + d[3]/unit, Dx: t.Dx + d[4]/unit, Dy: t.Dy + d[5]/unit}}
	case tables.PaintVarTranslate:
		d := w.deltas(p.VarIndexBase, 2)
		return tables.PaintTranslate{Paint: p.Paint, Dx: addI16(p.Dx, d[0]), Dy: addI16(p.Dy, d[1])}
	case tables.PaintVarScale:
		d := w.deltas(p.VarIndexBase, 2)
		return tables.PaintScale{Paint: p.Paint, ScaleX: addF214(p.ScaleX, d[0]), ScaleY: addF214(p.ScaleY, d[1])}
	case tables.PaintVarScaleAroundCenter:
		d := w.deltas(p.VarIndexBase, 4)
		return tables.PaintScaleAroundCenter{Paint: p.Paint, ScaleX: addF214(p.ScaleX, d[0]), ScaleY: addF214(p.ScaleY, d[1]),
			CenterX: addI16(p.CenterX, d[2]), CenterY: addI16(p.CenterY, d[3])}
	case tables.PaintVarScaleUniform:
		d := w.deltas(p.VarIndexBase, 1)
		return tables.PaintScaleUniform{Paint: p.Paint, Scale: addF214(p.Scale, d[0])}
	case tables.PaintVarScaleUniformAroundCenter:
		d := w.deltas(p.VarIndexBase, 3)
		return tables.PaintScaleUniformAroundCenter{Paint: p.Paint, Scale: addF214(p.Scale, d[0]), CenterX: addI16(p.CenterX, d[1]), CenterY: addI16(p.CenterY, d[2])}
	case tables.PaintVarRotate:
		d := w.deltas(p.VarIndexBase, 1)
		return tables.PaintRotate{Paint: p.Paint, Angle: addF214(p.Angle, d[0])}
	case tables.PaintVarRotateAroundCenter:
		d := w.deltas(p.VarIndexBase, 3)
		return tables.PaintRotateAroundCenter{Paint: p.Paint, Angle: addF214(p.Angle, d[0]), CenterX: addI16(p.CenterX, d[1]), CenterY: addI16(p.CenterY, d[2])}
	case tables.PaintVarSkew:
		d := w.deltas(p.VarIndexBase, 2)
		return tables.PaintSkew{Paint: p.Paint, XSkewAngle: addF214(p.XSkewAngle, d[0]), YSkewAngle: addF214(p.YSkewAngle, d[1])}
	case tables.PaintVarSkewAroundCenter:
		d := w.deltas(p.VarIndexBase, 4)
		return tables.PaintSkewAroundCenter{Paint: p.Paint, XSkewAngle: addF214(p.XSkewAngle, d[0]), YSkewAngle: addF214(p.YSkewAngle, d[1]),
			CenterX: addI16(p.CenterX, d[2]), CenterY: addI16(p.CenterY, d[3])}
	}
	return p
}

// line returns the plain color line of a variable color line.
func (w *colorWalker) line(l tables.VarColorLine) tables.ColorLine {
	out := tables.ColorLine{Extend: l.Extend, ColorStops: make([]tables.ColorStop, len(l.ColorStops))}
	for i, s := range l.ColorStops {
		d := w.deltas(s.VarIndexBase, 2)
		out.ColorStops[i] = tables.ColorStop{StopOffset: addF214(s.StopOffset, d[0]), PaletteIndex: s.PaletteIndex, Alpha: addF214(s.Alpha, d[1])}
	}
	return out
}

// noVariation is the VarIndexBase of a value without deltas.
const noVariation = 0xFFFFFFFF

// deltas returns the deltas of n consecutive values from base, in the units of each field.
func (w *colorWalker) deltas(base uint32, n int) [6]float32 {
	var d [6]float32
	store := w.colr.ItemVariationStore
	if base == noVariation || store == nil || len(w.coords) == 0 {
		return d
	}
	for k := range n {
		i := base + uint32(k)
		// Without a map, the index holds the outer index in its high 16 bits.
		at := tables.VariationStoreIndex{DeltaSetOuter: uint16(i >> 16), DeltaSetInner: uint16(i)}
		if m := w.colr.VarIndexMap; m != nil && len(m.Map) > 0 {
			at = m.Map[min(int(i), len(m.Map)-1)]
		}
		d[k] = store.GetDelta(at, w.coords)
	}
	return d
}

func addI16(v int16, d float32) int16 {
	return int16(min(max(math.Round(float64(v)+float64(d)), math.MinInt16), math.MaxInt16))
}

func addU16(v uint16, d float32) uint16 {
	return uint16(min(max(math.Round(float64(v)+float64(d)), 0), math.MaxUint16))
}

// addF214 adds a delta in units of 1/16384.
func addF214(v tables.Fixed214, d float32) tables.Fixed214 {
	return tables.Fixed214(addI16(int16(v), d))
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
