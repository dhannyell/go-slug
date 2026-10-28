package slug

// Curve is a quadratic Bézier: P0 start, P1 control, P2 end.
type Curve struct {
	P0, P1, P2 Point
}

// lineCurve returns a straight line as a quadratic: {a, b, b}.
func lineCurve(a, b Point) Curve {
	return Curve{a, b, b}
}

// Bounds returns the exact box of c.
func (c Curve) Bounds() Rect {
	loX, hiX := extent(c.P0.X, c.P1.X, c.P2.X)
	loY, hiY := extent(c.P0.Y, c.P1.Y, c.P2.Y)

	return Rect{
		Min: Point{loX, loY},
		Max: Point{hiX, hiY},
	}
}

// extent returns the min and max of a quadratic on one axis.
func extent(p0, p1, p2 float32) (lo, hi float32) {
	lo, hi = min(p0, p2), max(p0, p2)
	den := p0 - 2*p1 + p2
	if den == 0 {
		return
	}
	t := (p0 - p1) / den
	if t <= 0 || t >= 1 {
		return
	}
	u := 1 - t
	v := u*u*p0 + 2*u*t*p1 + t*t*p2
	lo, hi = min(lo, v), max(hi, v)
	return
}

// union returns the smallest box that contains a and b.
func union(a, b Rect) Rect {
	return Rect{
		Min: Point{X: min(a.Min.X, b.Min.X), Y: min(a.Min.Y, b.Min.Y)},
		Max: Point{X: max(a.Max.X, b.Max.X), Y: max(a.Max.Y, b.Max.Y)},
	}
}
