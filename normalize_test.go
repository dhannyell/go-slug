package slug

import (
	"errors"
	"math"
	"testing"
)

func TestNormalizeRejectsBrokenPaths(t *testing.T) {
	nan := float32(math.NaN())
	cases := []struct {
		name string
		p    Path
	}{
		{"LineTo without MoveTo", Path{Verbs: []Verb{LineTo}, Points: []Point{{1, 1}}}},
		{"LineTo after Close", Path{Verbs: []Verb{MoveTo, LineTo, Close, LineTo}, Points: []Point{{0, 0}, {1, 0}, {1, 1}}}},
		{"missing points", Path{Verbs: []Verb{MoveTo, QuadTo}, Points: []Point{{0, 0}, {1, 1}}}},
		{"extra points", Path{Verbs: []Verb{MoveTo, LineTo}, Points: []Point{{0, 0}, {1, 0}, {1, 1}}}},
		{"unknown verb", Path{Verbs: []Verb{MoveTo, Verb(99)}, Points: []Point{{0, 0}}}},
		{"NaN point", Path{Verbs: []Verb{MoveTo, LineTo}, Points: []Point{{0, 0}, {nan, 1}}}},
	}
	for _, c := range cases {
		_, err := normalize(Drawing{Layers: []Layer{{Path: c.p}}}, 0.1)
		if !errors.Is(err, ErrInvalidPath) {
			t.Errorf("%s: err = %v, want ErrInvalidPath", c.name, err)
		}
	}
}

func TestNormalizeCloses(t *testing.T) {
	var open, closed Path
	for _, p := range []*Path{&open, &closed} {
		p.MoveTo(Point{0, 0})
		p.LineTo(Point{4, 0})
		p.LineTo(Point{0, 3})
	}
	closed.Close()

	a := mustNormalize(t, Drawing{Layers: []Layer{{Path: open}}})
	b := mustNormalize(t, Drawing{Layers: []Layer{{Path: closed}}})
	want := []Curve{
		lineCurve(Point{0, 0}, Point{4, 0}),
		lineCurve(Point{4, 0}, Point{0, 3}),
		lineCurve(Point{0, 3}, Point{0, 0}),
	}
	for _, s := range [][]shape{a, b} {
		if len(s) != 1 || !equalCurves(s[0].Curves, want) {
			t.Errorf("shapes = %v, want one shape with %v", s, want)
		}
	}
}

// A layer with no curves (a space glyph) makes no shape.
func TestNormalizeSkipsEmptyLayers(t *testing.T) {
	var onlyMove Path
	onlyMove.MoveTo(Point{1, 1})
	d := Drawing{Layers: []Layer{{}, {Path: onlyMove}}}
	if s := mustNormalize(t, d); len(s) != 0 {
		t.Errorf("shapes = %v, want none", s)
	}
}

func TestNormalizeTransform(t *testing.T) {
	var p Path
	p.MoveTo(Point{1, 1})
	p.LineTo(Point{3, 1})
	p.LineTo(Point{3, 2})
	before := append([]Point(nil), p.Points...)

	double := Affine{2, 0, 0, 2, 0, 0}
	d := Drawing{Layers: []Layer{{Path: p}, {Path: p, Transform: &double}}}
	s := mustNormalize(t, d)

	if want := (Rect{Point{1, 1}, Point{3, 2}}); s[0].Bounds != want {
		t.Errorf("nil transform: Bounds = %v, want %v", s[0].Bounds, want)
	}
	if want := (Rect{Point{2, 2}, Point{6, 4}}); s[1].Bounds != want {
		t.Errorf("double: Bounds = %v, want %v", s[1].Bounds, want)
	}
	// The layers share Points; normalize must not write into them.
	if !equalPoints(p.Points, before) {
		t.Errorf("Points changed to %v, want %v", p.Points, before)
	}
}

func TestCubicToQuads(t *testing.T) {
	const tol = 0.05
	c := [4]Point{{0, 0}, {120, 160}, {-20, 160}, {100, 0}} // the controls cross: a sharp bend
	quads := cubicToQuads(c[0], c[1], c[2], c[3], tol, nil)

	if len(quads) < 2 {
		t.Fatalf("got %d quadratics, want a split", len(quads))
	}
	if quads[0].P0 != c[0] || quads[len(quads)-1].P2 != c[3] {
		t.Errorf("ends = %v, %v, want %v, %v", quads[0].P0, quads[len(quads)-1].P2, c[0], c[3])
	}
	for i := 1; i < len(quads); i++ {
		if quads[i].P0 != quads[i-1].P2 {
			t.Errorf("gap between quadratics %d and %d", i-1, i)
		}
	}

	// Measure the distance from the cubic to the quadratics, sampled as a
	// fine polyline.
	var poly []Point
	for _, q := range quads {
		for j := range 64 {
			poly = append(poly, quadAt(q, float32(j)/64))
		}
	}
	poly = append(poly, c[3])
	for j := range 501 {
		pt := cubicAt(c, float32(j)/500)
		if d := distToPolyline(pt, poly); d > tol*1.01 {
			t.Errorf("t=%v: distance %v > tol %v", float32(j)/500, d, tol)
		}
	}
}

func mustNormalize(t *testing.T, d Drawing) []shape {
	t.Helper()
	s, err := normalize(d, 0.1)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func equalCurves(a, b []Curve) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalPoints(a, b []Point) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func quadAt(q Curve, t float32) Point {
	return lerp(lerp(q.P0, q.P1, t), lerp(q.P1, q.P2, t), t)
}

func cubicAt(c [4]Point, t float32) Point {
	left, _ := splitCubic(c, t)
	return left[3]
}

func distToPolyline(p Point, poly []Point) float32 {
	best := float32(math.Inf(1))
	for i := 1; i < len(poly); i++ {
		a, b := poly[i-1], poly[i]
		ab, ap := sub(b, a), sub(p, a)
		t := float32(0)
		if l := ab.X*ab.X + ab.Y*ab.Y; l > 0 {
			t = min(max((ap.X*ab.X+ap.Y*ab.Y)/l, 0), 1)
		}
		best = min(best, length(sub(p, lerp(a, b, t))))
	}
	return best
}
