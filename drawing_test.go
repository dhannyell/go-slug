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

func near(a, b float32) bool {
	return math.Abs(float64(a-b)) <= 1e-5*max(1, math.Abs(float64(b)))
}
