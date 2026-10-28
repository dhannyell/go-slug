package slug

// MoveTo starts a new contour at pt.
func (p *Path) MoveTo(pt Point) {
	p.Verbs = append(p.Verbs, MoveTo)
	p.Points = append(p.Points, pt)
}

// LineTo adds a straight line to pt.
func (p *Path) LineTo(pt Point) {
	p.Verbs = append(p.Verbs, LineTo)
	p.Points = append(p.Points, pt)
}

// QuadTo adds a quadratic Bézier with control ctrl,
// ending at end.
func (p *Path) QuadTo(ctrl, end Point) {
	p.Verbs = append(p.Verbs, QuadTo)
	p.Points = append(p.Points, ctrl, end)
}

// CubeTo adds a cubic Bézier with controls c1 and c2,
// ending at end.
func (p *Path) CubeTo(c1, c2, end Point) {
	p.Verbs = append(p.Verbs, CubeTo)
	p.Points = append(p.Points, c1, c2, end)
}

// Close ends the current contour with a line back to its MoveTo point.
func (p *Path) Close() {
	p.Verbs = append(p.Verbs, Close)
}
