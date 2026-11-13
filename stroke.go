package slug

import (
	"fmt"
	"math"
)

// Cap is the shape of the end of an open contour.
type Cap uint8

const (
	ButtCap   Cap = iota // ends at the last point
	RoundCap             // adds a half circle
	SquareCap            // adds half of the width
)

// Join is the shape of the outer side of a corner.
type Join uint8

const (
	MiterJoin Join = iota // a sharp corner, or a bevel over MiterLimit
	RoundJoin             // an arc
	BevelJoin             // a straight line
)

// Stroke is the style of a line that follows a Path.
type Stroke struct {
	// Width is the full width of the line, in drawing units.
	Width float32
	Cap   Cap
	Join  Join
	// MiterLimit is the longest miter, as a multiple of Width. 0 is 4.
	MiterLimit float32
	// Tolerance is the maximum error of the result, in drawing units. 0 is Width/256.
	Tolerance float32
}

// maxStrokeSplits limits how many times one curve splits: 2^6 pieces at most.
const maxStrokeSplits = 6

// arcStep is the largest angle of one quadratic in an arc. The error is under
// 0.3% of the radius.
const arcStep = math.Pi / 4

// Stroke returns a path whose fill is a line along p. The result uses NonZero and
// can overlap itself. Size it with the Width of s: each curve can become many.
//
// A contour with Close has joins on all its corners. A contour without Close has
// caps, although a fill closes it. A contour without length draws nothing.
//
// A Layer.Transform applies after the stroke, so a non-uniform scale changes the
// width along the line.
func (p Path) Stroke(s Stroke) (Path, error) {
	st, err := newStroker(s, s.Width/2)
	if err != nil {
		return Path{}, err
	}
	if _, err := flattenPath(p, Identity(), st.tol, nil); err != nil {
		return Path{}, fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	st.path(p)
	return st.out, nil
}

// Embolden returns p with each edge moved out by amount, in drawing units. It keeps
// the holes of a NonZero path, and an EvenOdd path is an error. Corners stay sharp up
// to the default miter limit.
func (p Path) Embolden(amount float32) (Path, error) {
	if p.Rule != NonZero {
		return Path{}, fmt.Errorf("%w: Embolden needs NonZero", ErrInvalidPath)
	}
	s := Stroke{Width: 2 * amount}
	st, err := newStroker(s, amount)
	if err != nil {
		return Path{}, err
	}
	curves, err := flattenPath(p, Identity(), st.tol, nil)
	if err != nil {
		return Path{}, fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	// The ring must have the winding of the fill, or the two cancel where they overlap.
	if signedArea(curves) < 0 {
		st.d = -st.d
	}
	st.path(p)
	return Path{
		Verbs:  append(p.Verbs[:len(p.Verbs):len(p.Verbs)], st.out.Verbs...),
		Points: append(p.Points[:len(p.Points):len(p.Points)], st.out.Points...),
	}, nil
}

// Stroke returns d with Path.Stroke applied to each layer.
func (d Drawing) Stroke(s Stroke) (Drawing, error) {
	return d.mapPaths(func(p Path) (Path, error) { return p.Stroke(s) })
}

// Embolden returns d with Path.Embolden applied to each layer and to each path of
// its Clip, so a clip grows with the shapes inside it.
func (d Drawing) Embolden(amount float32) (Drawing, error) {
	d, err := d.mapPaths(func(p Path) (Path, error) { return p.Embolden(amount) })
	if err != nil {
		return d, err
	}
	for i, layer := range d.Layers {
		if layer.Clip == nil {
			continue
		}
		clip := make([]ClipPath, len(layer.Clip))
		for k, c := range layer.Clip {
			if c.Path, err = c.Path.Embolden(amount); err != nil {
				return Drawing{}, fmt.Errorf("layer %d, clip %d: %w", i, k, err)
			}
			clip[k] = c
		}
		d.Layers[i].Clip = clip
	}
	return d, nil
}

// mapPaths changes the path of each layer. It keeps each Clip.
func (d Drawing) mapPaths(change func(Path) (Path, error)) (Drawing, error) {
	layers := make([]Layer, len(d.Layers))
	for i, layer := range d.Layers {
		path, err := change(layer.Path)
		if err != nil {
			return Drawing{}, fmt.Errorf("layer %d: %w", i, err)
		}
		layer.Path = path
		layers[i] = layer
	}
	return Drawing{Layers: layers, Em: d.Em}, nil
}

// signedArea returns the area inside curves, positive for counterclockwise. The
// chords are enough: only the sign of the sum is used.
func signedArea(curves []curve) float32 {
	var sum float32
	for _, c := range curves {
		sum += cross(c.P0, c.P2)
	}
	return sum / 2
}

// stroker writes the path of one stroke.
type stroker struct {
	style Stroke
	// d is the distance of the offset, positive to the right of the direction of
	// travel. A contour walks in both directions, so the ring winds counterclockwise
	// if d is positive.
	d, tol, miter float32
	out           Path
	cur           Point
	covers        []Point // the centers of the discs of this contour
}

func newStroker(s Stroke, d float32) (*stroker, error) {
	if !(s.Width > 0) || math.IsInf(float64(s.Width), 0) {
		return nil, fmt.Errorf("%w: stroke width %v", ErrInvalidPath, s.Width)
	}
	st := &stroker{style: s, d: d, tol: s.Tolerance, miter: s.MiterLimit}
	if !(st.tol > 0) {
		st.tol = s.Width / 256
	}
	if !(st.miter > 0) {
		st.miter = 4
	}
	return st, nil
}

// path strokes each contour of p, which flattenPath has checked.
func (st *stroker) path(p Path) {
	var (
		segs       []curve
		start, cur Point
		i          int
		open       bool
	)
	flush := func(closed bool) {
		if open && len(segs) > 0 {
			st.contour(segs, closed)
		}
		segs = segs[:0]
	}
	for _, verb := range p.Verbs {
		pts := p.Points[i : i+verb.PointCount()]
		i += len(pts)
		switch verb {
		case MoveTo:
			flush(false)
			start, cur, open = pts[0], pts[0], true
		case LineTo:
			if pts[0] != cur {
				segs = append(segs, lineCurve(cur, pts[0]))
			}
			cur = pts[0]
		case QuadTo:
			if c := (curve{cur, pts[0], pts[1]}); !degenerate(c) {
				segs = append(segs, c)
			}
			cur = pts[1]
		case CubeTo:
			segs = cubicToQuads(cur, pts[0], pts[1], pts[2], st.tol, segs)
			cur = pts[2]
		case Close:
			if cur != start {
				segs = append(segs, lineCurve(cur, start))
			}
			flush(true)
			open, cur = false, start
		}
	}
	flush(false)
}

func (st *stroker) contour(segs []curve, closed bool) {
	var forward []curve
	for _, c := range segs {
		forward = st.refine(c, 0, forward)
	}
	backward := make([]curve, len(forward))
	for i, c := range forward {
		backward[len(forward)-1-i] = curve{c.P2, c.P1, c.P0}
	}
	if closed {
		st.ring(forward)
		st.ring(backward)
	} else {
		st.moveTo(st.side(forward[0], true))
		st.walk(forward)
		st.cap(forward[len(forward)-1])
		st.walk(backward)
		st.cap(backward[len(backward)-1])
		st.out.Close()
	}
	st.writeCovers()
}

// writeCovers writes a disc at each center. All the area of a disc is in the stroke,
// because the curve under it is smooth.
func (st *stroker) writeCovers() {
	for _, center := range st.covers {
		from := Point{st.abs(), 0}
		st.moveTo(add(center, from))
		st.arc(center, from, math.Copysign(2*math.Pi, float64(st.d)))
		st.out.Close()
	}
	st.covers = st.covers[:0]
}

// addCover asks for a disc at p, if the last disc is not near.
func (st *stroker) addCover(p Point) {
	if n := len(st.covers); n > 0 && length(sub(p, st.covers[n-1])) < st.abs()/4 {
		return
	}
	st.covers = append(st.covers, p)
}

// ring writes one closed offset of pieces.
func (st *stroker) ring(pieces []curve) {
	st.moveTo(st.side(pieces[0], true))
	st.walk(pieces)
	st.join(pieces[len(pieces)-1], pieces[0])
	st.out.Close()
}

// walk writes the offset of pieces, with the joins between them.
func (st *stroker) walk(pieces []curve) {
	for i, c := range pieces {
		if i > 0 {
			st.join(pieces[i-1], c)
		}
		st.offset(c)
	}
}

// side returns the point at the offset of the start or the end of c.
func (st *stroker) side(c curve, start bool) Point {
	if start {
		return add(c.P0, scale(right(startTangent(c)), st.d))
	}
	return add(c.P2, scale(right(endTangent(c)), st.d))
}

// offset writes the offset of one piece from refine.
func (st *stroker) offset(c curve) {
	q, flipped := offsetCurve(c, st.d)
	if flipped {
		// The center of curvature is nearer than d, so this side runs backward. Its loop
		// has the opposite winding and cuts a hole in the ring. The line stays on the
		// curve, and a disc covers the area that this line cuts.
		st.addCover(c.P0)
		st.addCover(c.P2)
		st.lineTo(c.P0)
		st.out.QuadTo(c.P1, c.P2)
		st.cur = c.P2
		st.lineTo(q.P2)
		return
	}
	st.out.QuadTo(q.P1, q.P2)
	st.cur = q.P2
}

// join connects the offsets of two pieces at their shared point.
func (st *stroker) join(a, b curve) {
	pivot := b.P0
	to := st.side(b, true)
	if length(sub(to, st.cur)) < st.tol/16 {
		return
	}
	t0, t1 := endTangent(a), startTangent(b)
	sweep := st.sweep(t0, t1)
	if sweep*float64(st.d) < 0 {
		// The inner side. The pivot keeps the winding of the overlap positive.
		st.lineTo(pivot)
		st.lineTo(to)
		return
	}
	switch st.style.Join {
	case RoundJoin:
		st.arc(pivot, scale(right(t0), st.d), sweep)
	case MiterJoin:
		half := float64(sweep) / 2
		if c := float32(math.Cos(half)); c > 0 && 1/c <= st.miter {
			st.lineTo(add(st.cur, scale(t0, st.abs()*float32(math.Tan(math.Abs(half))))))
		}
	}
	st.lineTo(to)
}

// cap connects the two sides at the end of c, the last piece of a walk.
func (st *stroker) cap(c curve) {
	t := endTangent(c)
	n := scale(right(t), st.d)
	to := sub(c.P2, n)
	switch st.style.Cap {
	case RoundCap:
		st.arc(c.P2, n, math.Copysign(math.Pi, float64(st.d)))
	case SquareCap:
		out := scale(t, st.abs())
		st.lineTo(add(st.cur, out))
		st.lineTo(add(to, out))
	}
	st.lineTo(to)
}

// sweep returns the signed angle from t0 to t1. A reversal goes around the outer side.
func (st *stroker) sweep(t0, t1 Point) float64 {
	c, d := cross(t0, t1), dot(t0, t1)
	if d < 0 && abs32(c) < 1e-4 {
		return math.Copysign(math.Pi, float64(st.d))
	}
	return math.Atan2(float64(c), float64(d))
}

// arc writes the arc around center from center+from, by sweep radians.
func (st *stroker) arc(center, from Point, sweep float64) {
	steps := max(int(math.Ceil(math.Abs(sweep)/arcStep)), 1)
	step := sweep / float64(steps)
	sin, cos := math.Sincos(step)
	sin32, cos32 := float32(sin), float32(cos)
	a := from
	for i := range steps {
		b := Point{a.X*cos32 - a.Y*sin32, a.X*sin32 + a.Y*cos32}
		if i == steps-1 && steps > 1 {
			// Keep the rounding of many rotations out of the last point.
			sinAll, cosAll := math.Sincos(sweep)
			b = Point{
				from.X*float32(cosAll) - from.Y*float32(sinAll),
				from.X*float32(sinAll) + from.Y*float32(cosAll),
			}
		}
		ctrl := scale(add(a, b), 1/(1+cos32))
		end := add(center, b)
		st.out.QuadTo(add(center, ctrl), end)
		st.cur = end
		a = b
	}
}

func (st *stroker) moveTo(p Point) {
	st.out.MoveTo(p)
	st.cur = p
}

func (st *stroker) lineTo(p Point) {
	if p != st.cur {
		st.out.LineTo(p)
		st.cur = p
	}
}

func (st *stroker) abs() float32 { return abs32(st.d) }

// refine appends the parts of c whose offsets stay within the tolerance.
func (st *stroker) refine(c curve, depth int, dst []curve) []curve {
	if depth == maxStrokeSplits || (st.flat(c, st.d) && st.flat(c, -st.d)) {
		return append(dst, c)
	}
	a, b := splitQuad(c)
	return st.refine(b, depth+1, st.refine(a, depth+1, dst))
}

// flat reports whether the offset of c at d stays within the tolerance. A side that
// runs backward does not use its offset, so its error does not count.
func (st *stroker) flat(c curve, d float32) bool {
	q, flipped := offsetCurve(c, d)
	if flipped {
		return true
	}
	for _, t := range [...]float32{0.25, 0.5, 0.75} {
		exact := add(pointAt(c, t), scale(right(tangentAt(c, t)), d))
		if length(sub(exact, pointAt(q, t))) > st.tol {
			return false
		}
	}
	return true
}

// offsetCurve returns the quadratic near the offset of c. Its control point is on
// the tangents at its ends. flipped reports a side that runs against c.
func offsetCurve(c curve, d float32) (q curve, flipped bool) {
	t0, t1 := startTangent(c), endTangent(c)
	q.P0 = add(c.P0, scale(right(t0), d))
	q.P2 = add(c.P2, scale(right(t1), d))
	flipped = dot(sub(q.P2, q.P0), sub(c.P2, c.P0)) <= 0
	if den := cross(t0, t1); abs32(den) > 1e-4 {
		q.P1 = add(q.P0, scale(t0, cross(sub(q.P2, q.P0), t1)/den))
	} else {
		q.P1 = add(c.P1, scale(right(add(t0, t1)), d/2))
	}
	return q, flipped
}

func splitQuad(c curve) (a, b curve) {
	l, r := lerp(c.P0, c.P1, 0.5), lerp(c.P1, c.P2, 0.5)
	mid := lerp(l, r, 0.5)
	return curve{c.P0, l, mid}, curve{mid, r, c.P2}
}

func pointAt(c curve, t float32) Point {
	return lerp(lerp(c.P0, c.P1, t), lerp(c.P1, c.P2, t), t)
}

// startTangent returns the unit direction at the start of c.
func startTangent(c curve) Point {
	if c.P1 != c.P0 {
		return unit(sub(c.P1, c.P0))
	}
	return unit(sub(c.P2, c.P0))
}

// endTangent returns the unit direction at the end of c.
func endTangent(c curve) Point {
	if c.P2 != c.P1 {
		return unit(sub(c.P2, c.P1))
	}
	return unit(sub(c.P2, c.P0))
}

func tangentAt(c curve, t float32) Point {
	d := sub(lerp(c.P1, c.P2, t), lerp(c.P0, c.P1, t))
	if d == (Point{}) {
		return startTangent(c)
	}
	return unit(d)
}

// right returns the direction to the right of t, in Y-up coordinates.
func right(t Point) Point { return Point{t.Y, -t.X} }

func unit(p Point) Point {
	if l := length(p); l > 0 {
		return scale(p, 1/l)
	}
	return Point{}
}

func cross(a, b Point) float32 { return a.X*b.Y - a.Y*b.X }
func dot(a, b Point) float32   { return a.X*b.X + a.Y*b.Y }
