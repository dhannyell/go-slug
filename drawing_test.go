package slug

import (
	"math"
	"testing"
)

func TestAffineThen(t *testing.T) {
	sin, cos := float32(math.Sin(0.5)), float32(math.Cos(0.5))
	scale := Affine{2, 0, 0, 3, 0, 0}
	move := Affine{1, 0, 0, 1, 5, -7}
	rotate := Affine{cos, sin, -sin, cos, 0, 0}
	shear := Affine{1, 0.5, 0.25, 1, 2, 1}

	cases := []struct {
		name    string
		m, next Affine
	}{
		{"scale then move", scale, move},
		{"move then scale", move, scale},
		{"rotate then shear", rotate, shear},
		{"shear then rotate", shear, rotate},
		// Two singular matrices whose product is the zero matrix: every
		// point must collapse to the origin.
		{"only x then only y", Affine{1, 0, 0, 0, 0, 0}, Affine{0, 0, 0, 1, 0, 0}},
		{"zero then shear", Affine{}, shear},
	}
	points := []Point{{0, 0}, {1, 0}, {0, 1}, {-3, 4.5}}

	for _, c := range cases {
		got := c.m.Then(c.next)
		for _, p := range points {
			a, b := got.Apply(p), c.next.Apply(c.m.Apply(p))
			if !near(a.X, b.X) || !near(a.Y, b.Y) {
				t.Errorf("%s: Then.Apply(%v) = %v, want %v", c.name, p, a, b)
			}
		}
	}
}

func TestAffineInvert(t *testing.T) {
	m := Affine{2, 0.5, 0.25, 3, 5, -7}
	inv, ok := m.Invert()
	if !ok {
		t.Fatalf("Invert(%v) = false", m)
	}
	for _, p := range []Point{{0, 0}, {1, 0}, {0, 1}, {-3, 4.5}} {
		if got := inv.Apply(m.Apply(p)); !near(got.X, p.X) || !near(got.Y, p.Y) {
			t.Errorf("inverse of Apply(%v) = %v", p, got)
		}
	}

	// The determinant of a small scale underflows float32.
	if _, ok := (Affine{1e-25, 0, 0, 1e-25, 0, 0}).Invert(); !ok {
		t.Errorf("small scale: Invert = false")
	}
	for _, m := range []Affine{{}, {1, 2, 2, 4, 0, 0}, {float32(math.NaN()), 0, 0, 1, 0, 0}} {
		if _, ok := m.Invert(); ok {
			t.Errorf("Invert(%v) = true", m)
		}
	}
}

func near(a, b float32) bool {
	return math.Abs(float64(a-b)) <= 1e-5*max(1, math.Abs(float64(b)))
}
