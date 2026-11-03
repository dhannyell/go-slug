package slug

import (
	"math"
	"slices"
	"testing"
)

// A curve missing from the band of a pixel shows as a hole on the screen.
func TestBandsCoverEveryCurve(t *testing.T) {
	// A ring of quadratics: many curves, each on a small part of the box.
	const count = 24
	var curves []Curve
	at := func(i float64, r float64) Point {
		a := i * 2 * math.Pi / count
		return Point{float32(r * math.Cos(a)), float32(r * math.Sin(a))}
	}
	for i := range count {
		curves = append(curves, Curve{at(float64(i), 100), at(float64(i)+0.5, 103), at(float64(i)+1, 100)})
	}
	bounds, _ := boundsOf(curves)
	bs := buildBands(curves, bounds, 0)

	if len(bs.H) < 2 || len(bs.V) < 2 {
		t.Fatalf("got %d H and %d V bands, want a split on both axes", len(bs.H), len(bs.V))
	}

	check := func(name string, bands []band, lo, hi float32, extent func(Rect) (float32, float32)) {
		for i := range 1001 {
			v := lo + (hi-lo)*float32(i)/1000
			got := bands[bandIndex(v, lo, hi, len(bands))]
			for ci, c := range curves {
				eLo, eHi := extent(c.Bounds())
				if eLo <= v && v <= eHi && !slices.Contains(got, uint32(ci)) {
					t.Errorf("%s: curve %d spans %v but is not in its band", name, ci, v)
				}
			}
		}
	}
	check("H", bs.H, bounds.Min.Y, bounds.Max.Y, func(r Rect) (float32, float32) { return r.Min.Y, r.Max.Y })
	check("V", bs.V, bounds.Min.X, bounds.Max.X, func(r Rect) (float32, float32) { return r.Min.X, r.Max.X })
}

func TestBandsLeaveOutFlatCurves(t *testing.T) {
	curves := []Curve{
		lineCurve(Point{0, 0}, Point{4, 0}), // horizontal
		lineCurve(Point{4, 0}, Point{4, 3}), // vertical
		lineCurve(Point{4, 3}, Point{0, 0}), // diagonal
	}
	bs := buildBands(curves, Rect{Point{0, 0}, Point{4, 3}}, 0)

	inH, inV := slices.Concat(bs.H...), slices.Concat(bs.V...)
	if slices.Contains(inH, 0) || !slices.Contains(inH, 1) || !slices.Contains(inH, 2) {
		t.Errorf("H = %v, want curves 1 and 2 only", bs.H)
	}
	if slices.Contains(inV, 1) || !slices.Contains(inV, 0) || !slices.Contains(inV, 2) {
		t.Errorf("V = %v, want curves 0 and 2 only", bs.V)
	}
}

func TestBandOrderIsDescendingAndStable(t *testing.T) {
	// All curves span the full height, so every n ties and H has one band.
	curves := []Curve{
		lineCurve(Point{1, 0}, Point{1, 4}),
		lineCurve(Point{3, 0}, Point{3, 4}),
		lineCurve(Point{3, 4}, Point{3, 0}), // same key as curve 1
		lineCurve(Point{2, 0}, Point{2, 4}),
	}
	bs := buildBands(curves, Rect{Point{1, 0}, Point{3, 4}}, 0)

	want := band{1, 2, 3, 0}
	if len(bs.H) != 1 || !slices.Equal(bs.H[0], want) {
		t.Errorf("H = %v, want one band %v", bs.H, want)
	}
}

// The shader stops at the first curve behind the pixel, so the key must be the
// maximum of all three points. A lower key hides a curve: a hole on the screen.
func TestBandKeyUsesAllControlPoints(t *testing.T) {
	curves := []Curve{
		{Point{0, 0}, Point{0, 2}, Point{7, 4}}, // P2 has the max
		{Point{8, 0}, Point{0, 2}, Point{0, 4}}, // P0 has the max
		{Point{0, 0}, Point{9, 2}, Point{0, 4}}, // P1 has the max
	}
	want := band{2, 1, 0}

	bs := buildBands(curves, Rect{Point{0, 0}, Point{9, 4}}, 0)
	if len(bs.H) != 1 || !slices.Equal(bs.H[0], want) {
		t.Errorf("H = %v, want one band %v", bs.H, want)
	}

	swapped := make([]Curve, len(curves))
	for i, c := range curves {
		swapped[i] = Curve{Point{c.P0.Y, c.P0.X}, Point{c.P1.Y, c.P1.X}, Point{c.P2.Y, c.P2.X}}
	}
	bs = buildBands(swapped, Rect{Point{0, 0}, Point{4, 9}}, 0)
	if len(bs.V) != 1 || !slices.Equal(bs.V[0], want) {
		t.Errorf("V = %v, want one band %v", bs.V, want)
	}
}

func TestBandCountSeparatesCurves(t *testing.T) {
	spans := []span{{lo: 0, hi: 1}, {lo: 9, hi: 10}}
	if n := bandCount(spans, 0, 10, 0); n != 2 {
		t.Errorf("bandCount = %d, want 2", n)
	}
}

func TestBandRangeOverlap(t *testing.T) {
	cases := []struct {
		name        string
		s           span
		overlap     float32
		first, last int
	}{
		{"short of the boundary", span{lo: 0, hi: 4.99}, 0, 0, 0},
		{"reaches over the boundary", span{lo: 0, hi: 4.99}, 0.1, 0, 1},
		{"after the boundary", span{lo: 5.01, hi: 10}, 0, 1, 1},
		{"reaches back over the boundary", span{lo: 5.01, hi: 10}, 0.1, 0, 1},
	}
	for _, c := range cases {
		first, last := bandRange(c.s, 0, 10, c.overlap, 2)
		if first != c.first || last != c.last {
			t.Errorf("%s: range = %d..%d, want %d..%d", c.name, first, last, c.first, c.last)
		}
	}
}

func TestBandsOfFlatShape(t *testing.T) {
	curves := []Curve{lineCurve(Point{0, 2}, Point{4, 2})}
	bs := buildBands(curves, Rect{Point{0, 2}, Point{4, 2}}, 0.01)

	if len(bs.H) != 1 || len(bs.H[0]) != 0 {
		t.Errorf("H = %v, want one empty band", bs.H)
	}
	if !slices.Contains(slices.Concat(bs.V...), 0) {
		t.Errorf("V = %v, want curve 0", bs.V)
	}
}
