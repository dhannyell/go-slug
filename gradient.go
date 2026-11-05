package slug

import (
	"errors"
	"fmt"
	"math"
)

// ErrInvalidPaint reports a Paint that Pack.Add cannot use.
var ErrInvalidPaint = errors.New("slug: invalid paint")

const (
	stopWords = 5 // offset, then red, green, blue, alpha
	maxStops  = 1<<16 - 1
	tau       = 2 * math.Pi
)

// gradientRecord is a Gradient in the canonical form of FORMAT.md. toPaint maps em
// space to the space of the canonical form.
type gradientRecord struct {
	toPaint Affine
	a, b    float32 // radial: r0, r1; sweep: start, end
	cx      float32 // radial: the x of the second center, 0 or 1
}

// canonicalPaint returns the paint and the record that Pack.Add writes. toDrawing is
// the Layer.Transform. A gradient that has no direction becomes a Solid paint.
func canonicalPaint(p Paint, toDrawing Affine, em float32) (Paint, *gradientRecord, error) {
	if p.Kind < LinearGradient {
		return p, nil, nil
	}
	g := p.Gradient
	if p.Kind > SweepGradient {
		return p, nil, fmt.Errorf("%w: unknown kind %d", ErrInvalidPaint, p.Kind)
	}
	if g == nil {
		return p, nil, fmt.Errorf("%w: kind %d needs a Gradient", ErrInvalidPaint, p.Kind)
	}
	if err := g.check(); err != nil {
		return p, nil, err
	}
	last := Paint{Kind: Solid, Color: g.Stops[len(g.Stops)-1].Color}
	if len(g.Stops) == 1 {
		return last, nil, nil
	}

	var r gradientRecord
	var fromGradient Affine
	switch p.Kind {
	case LinearGradient:
		if g.P0 == g.P1 {
			return last, nil, nil
		}
		fromGradient = axisFrom(g.P0, g.P1)
	case RadialGradient:
		if g.P0 == g.P1 && g.R0 == g.R1 {
			return last, nil, nil
		}
		fromGradient = Affine{1, 0, 0, 1, -g.P0.X, -g.P0.Y}
		r.a, r.b = g.R0, g.R1
		if g.P0 != g.P1 {
			fromGradient = axisFrom(g.P0, g.P1)
			size := 1 / float32(math.Hypot(float64(g.P1.X-g.P0.X), float64(g.P1.Y-g.P0.Y)))
			r.a, r.b, r.cx = g.R0*size, g.R1*size, 1
		}
	case SweepGradient:
		if g.Start == g.End {
			return last, nil, nil
		}
		fromGradient = Affine{1, 0, 0, 1, -g.P0.X, -g.P0.Y}
		r.a, r.b = g.Start, g.End
	}

	toEm := toDrawing.Then(Affine{1 / em, 0, 0, 1 / em, 0, 0})
	if g.Transform != nil {
		toEm = g.Transform.Then(toEm)
	}
	inverse, ok := toEm.Invert()
	if !ok {
		return p, nil, fmt.Errorf("%w: the gradient transform has no inverse", ErrInvalidPaint)
	}
	r.toPaint = inverse.Then(fromGradient)
	for _, v := range [...]float32{r.toPaint[0], r.toPaint[1], r.toPaint[2], r.toPaint[3], r.toPaint[4], r.toPaint[5], r.a, r.b} {
		if !finite(Point{v, 0}) {
			return p, nil, fmt.Errorf("%w: the gradient overflows", ErrInvalidPaint)
		}
	}
	return p, &r, nil
}

// axisFrom returns the matrix that moves p0 to (0, 0) and p1 to (1, 0), without shear.
func axisFrom(p0, p1 Point) Affine {
	// float64: the square of a small distance underflows float32.
	dx, dy := float64(p1.X-p0.X), float64(p1.Y-p0.Y)
	l := dx*dx + dy*dy
	x0, y0 := float64(p0.X), float64(p0.Y)
	return Affine{
		float32(dx / l), float32(-dy / l),
		float32(dy / l), float32(dx / l),
		float32(-(x0*dx + y0*dy) / l), float32((x0*dy - y0*dx) / l),
	}
}

func (g *Gradient) check() error {
	if len(g.Stops) == 0 || len(g.Stops) > maxStops {
		return fmt.Errorf("%w: a gradient has %d stops", ErrInvalidPaint, len(g.Stops))
	}
	if g.Spread > Reflect {
		return fmt.Errorf("%w: unknown spread %d", ErrInvalidPaint, g.Spread)
	}
	for i, s := range g.Stops {
		if !finite(Point{s.Offset, 0}) || (i > 0 && s.Offset < g.Stops[i-1].Offset) {
			return fmt.Errorf("%w: stop %d has offset %v, and offsets must rise", ErrInvalidPaint, i, s.Offset)
		}
	}
	if g.R0 < 0 || g.R1 < 0 {
		return fmt.Errorf("%w: a radius is negative", ErrInvalidPaint)
	}
	return nil
}

// layerGradient is the decoded gradient of a layer record.
type layerGradient struct {
	gradientRecord
	stops         uint32
	count         uint32
	spread        Spread
	premultiplied bool
}

// gradientColor returns the straight color of a gradient layer at coord. It is the
// oracle of slug_paint in slug_core.wgsl.
func (p *Pack) gradientColor(kind PaintKind, g *layerGradient, coord Point) [4]float32 {
	at := g.toPaint.Apply(coord)
	var t float32
	switch kind {
	case LinearGradient:
		t = at.X
	case RadialGradient:
		var ok bool
		if t, ok = radialT(at, g.a, g.b, g.cx); !ok {
			return [4]float32{}
		}
	case SweepGradient:
		t = sweepT(at, g.a, g.b)
	}
	switch g.spread {
	case Repeat:
		t -= float32(math.Floor(float64(t)))
	case Reflect:
		t -= 2 * float32(math.Floor(float64(t/2)))
		if t > 1 {
			t = 2 - t
		}
	}
	return p.stopColor(g, t)
}

// radialT returns the larger t whose circle has a radius of at least 0 and passes
// through at. The circles have the center (t*cx, 0) and the radius r0 + t*(r1-r0).
func radialT(at Point, r0, r1, cx float32) (float32, bool) {
	dr := r1 - r0
	a := cx*cx - dr*dr
	b := at.X*cx + r0*dr
	c := at.X*at.X + at.Y*at.Y - r0*r0
	var t1, t2 float32
	if abs32(a) < nearZero {
		if b == 0 {
			return 0, false
		}
		t1 = c * 0.5 / b
		t2 = t1
	} else {
		d := b*b - a*c
		if d < 0 {
			return 0, false
		}
		s := float32(math.Sqrt(float64(d)))
		t1, t2 = (b+s)/a, (b-s)/a
		if t2 > t1 {
			t1, t2 = t2, t1
		}
	}
	if r0+t1*dr >= 0 {
		return t1, true
	}
	if r0+t2*dr >= 0 {
		return t2, true
	}
	return 0, false
}

// sweepT measures the angle from start in the direction of end, from 0 to one turn.
func sweepT(at Point, start, end float32) float32 {
	span := end - start
	angle := float32(math.Atan2(float64(at.Y), float64(at.X))) - start
	if span < 0 {
		angle = -angle
		span = -span
	}
	angle -= tau * float32(math.Floor(float64(angle/tau)))
	return angle / span
}

func (p *Pack) stopColor(g *layerGradient, t float32) [4]float32 {
	stop := func(i uint32) (float32, [4]float32) {
		w := p.bands[g.stops+i*stopWords:][:stopWords]
		f := math.Float32frombits
		return f(w[0]), [4]float32{f(w[1]), f(w[2]), f(w[3]), f(w[4])}
	}
	lo, loColor := stop(0)
	if t <= lo {
		return loColor
	}
	for i := uint32(1); i < g.count; i++ {
		hi, hiColor := stop(i)
		if t <= hi {
			// t is over lo, so the two offsets differ.
			return mixStops(loColor, hiColor, (t-lo)/(hi-lo), g.premultiplied)
		}
		lo, loColor = hi, hiColor
	}
	return loColor
}

func mixStops(a, b [4]float32, f float32, premultiplied bool) [4]float32 {
	if !premultiplied {
		return [4]float32{a[0] + (b[0]-a[0])*f, a[1] + (b[1]-a[1])*f, a[2] + (b[2]-a[2])*f, a[3] + (b[3]-a[3])*f}
	}
	alpha := a[3] + (b[3]-a[3])*f
	if alpha <= 0 {
		return [4]float32{}
	}
	var c [4]float32
	for i := range 3 {
		c[i] = (a[i]*a[3] + (b[i]*b[3]-a[i]*a[3])*f) / alpha
	}
	c[3] = alpha
	return c
}
