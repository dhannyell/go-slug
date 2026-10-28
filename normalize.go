package slug

import (
	"errors"
	"fmt"
	"math"
)

// ErrInvalidPath reports a Path that breaks the Path contract.
var ErrInvalidPath = errors.New("slug: invalid path")

// shape is one layer after normalization: quadratics in final space.
type shape struct {
	Curves []Curve
	Bounds Rect
	Rule   FillRule
	Paint  Paint
}

// cubicErr is √3/36, the error factor of one cubic-to-quadratic step.
const cubicErr float32 = 0.048112522

// maxCubicSplits limits the quadratics made from one cubic.
const maxCubicSplits = 64

// normalize converts every layer of d into quadratics.
// tol is the maximum distance between a cubic and its quadratics,
// in final units. Layers without curves make no shape.
func normalize(d Drawing, tol float32) ([]shape, error) {
	if !(tol > 0) {
		return nil, fmt.Errorf("slug: tolerance %v is not positive", tol)
	}

	var shapes []shape
	for li, layer := range d.Layers {
		m := Identity()
		if layer.Transform != nil {
			m = *layer.Transform
		}
		curves, err := flattenPath(layer.Path, m, tol, nil)
		if err != nil {
			return nil, fmt.Errorf("%w: layer %d, %v", ErrInvalidPath, li, err)
		}
		bounds, ok := boundsOf(curves)
		if !ok {
			continue
		}
		shapes = append(shapes, shape{
			Curves: curves,
			Bounds: bounds,
			Rule:   layer.Path.Rule,
			Paint:  layer.Paint,
		})
	}
	return shapes, nil
}

// flattenPath checks p against the Path contract and appends its curves to
// dst. It does not change p.
func flattenPath(p Path, m Affine, tol float32, dst []Curve) ([]Curve, error) {
	var (
		i          int
		start, cur Point
		open       bool
		pts        [3]Point
	)

	closeContour := func() {
		if open && cur != start {
			dst = append(dst, lineCurve(cur, start))
		}
		cur = start
	}

	for k, verb := range p.Verbs {
		n := verb.PointCount()
		if n < 0 {
			return nil, fmt.Errorf("verb %d: unknown verb %d", k, verb)
		}
		if i+n > len(p.Points) {
			return nil, fmt.Errorf("verb %d: missing points", k)
		}
		if verb != MoveTo && verb != Close && !open {
			return nil, fmt.Errorf("verb %d: drawing verb without MoveTo", k)
		}
		for j := range n {
			pt := m.Apply(p.Points[i+j])
			if !finite(pt) {
				return nil, fmt.Errorf("verb %d: point %v is not finite", k, pt)
			}
			pts[j] = pt
		}
		i += n

		switch verb {
		case MoveTo:
			closeContour()
			start, cur, open = pts[0], pts[0], true
		case LineTo:
			if pts[0] != cur {
				dst = append(dst, lineCurve(cur, pts[0]))
			}
			cur = pts[0]
		case QuadTo:
			if c := (Curve{cur, pts[0], pts[1]}); !degenerate(c) {
				dst = append(dst, c)
			}
			cur = pts[1]
		case CubeTo:
			dst = cubicToQuads(cur, pts[0], pts[1], pts[2], tol, dst)
			cur = pts[2]
		case Close:
			closeContour()
			open = false
		}
	}
	closeContour()

	if i != len(p.Points) {
		return nil, fmt.Errorf("%d extra points", len(p.Points)-i)
	}
	return dst, nil
}

// cubicToQuads appends quadratics that stay within tol of the cubic.
// It makes at most maxCubicSplits quadratics, so a very small tol can give a
// larger error.
func cubicToQuads(p0, p1, p2, p3 Point, tol float32, dst []Curve) []Curve {
	d := sub(add(p3, scale(p1, 3)), add(scale(p2, 3), p0))
	r := math.Ceil(math.Cbrt(float64(cubicErr * length(d) / tol)))
	n := maxCubicSplits
	if r < maxCubicSplits {
		n = max(int(r), 1)
	}

	c := [4]Point{p0, p1, p2, p3}
	for k := n; k >= 2; k-- {
		var left [4]Point
		left, c = splitCubic(c, 1/float32(k))
		dst = appendQuad(dst, left)
	}
	return appendQuad(dst, c)
}

// appendQuad appends the quadratic that matches the cubic c at both ends.
func appendQuad(dst []Curve, c [4]Point) []Curve {
	ctrl := scale(sub(scale(add(c[1], c[2]), 3), add(c[0], c[3])), 0.25)
	q := Curve{c[0], ctrl, c[3]}
	if degenerate(q) {
		return dst
	}
	return append(dst, q)
}

// splitCubic splits the cubic c at t with de Casteljau.
func splitCubic(c [4]Point, t float32) (left, right [4]Point) {
	a, b, cc := lerp(c[0], c[1], t), lerp(c[1], c[2], t), lerp(c[2], c[3], t)
	ab, bc := lerp(a, b, t), lerp(b, cc, t)
	mid := lerp(ab, bc, t)
	return [4]Point{c[0], a, ab, mid}, [4]Point{mid, bc, cc, c[3]}
}

// boundsOf returns the union of the bounds of curves; false when empty.
func boundsOf(curves []Curve) (Rect, bool) {
	if len(curves) == 0 {
		return Rect{}, false
	}
	r := curves[0].Bounds()
	for _, c := range curves[1:] {
		r = union(r, c.Bounds())
	}
	return r, true
}

// degenerate reports a curve that is a single point.
func degenerate(c Curve) bool {
	return c.P0 == c.P1 && c.P1 == c.P2
}

func finite(p Point) bool {
	return !math.IsNaN(float64(p.X)) && !math.IsInf(float64(p.X), 0) &&
		!math.IsNaN(float64(p.Y)) && !math.IsInf(float64(p.Y), 0)
}

func add(a, b Point) Point           { return Point{a.X + b.X, a.Y + b.Y} }
func sub(a, b Point) Point           { return Point{a.X - b.X, a.Y - b.Y} }
func scale(a Point, s float32) Point { return Point{a.X * s, a.Y * s} }

func length(a Point) float32 {
	return float32(math.Hypot(float64(a.X), float64(a.Y)))
}

// lerp returns the point at t on the line from a to b.
func lerp(a, b Point, t float32) Point {
	return add(a, scale(sub(b, a), t))
}
