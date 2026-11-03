package slug

import "testing"

func TestCurveBounds(t *testing.T) {
	cases := []struct {
		name string
		c    curve
		want Rect
	}{
		{"peak inside", curve{Point{0, 4}, Point{1, 6}, Point{2, 4}}, Rect{Point{0, 4}, Point{2, 5}}},
		{"line, zero denominator", lineCurve(Point{0, 0}, Point{3, 3}), Rect{Point{0, 0}, Point{3, 3}}},
		{"extremum outside (0,1)", curve{Point{0, 0}, Point{1, 1}, Point{3, 3}}, Rect{Point{0, 0}, Point{3, 3}}},
	}
	for _, c := range cases {
		if got := c.c.bounds(); got != c.want {
			t.Errorf("%s: Bounds() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestUnion(t *testing.T) {
	a := Rect{Point{0, -1}, Point{2, 1}}
	b := Rect{Point{1, -3}, Point{3, 0}}
	want := Rect{Point{0, -3}, Point{3, 1}}
	if got := union(a, b); got != want {
		t.Errorf("union = %v, want %v", got, want)
	}
}
