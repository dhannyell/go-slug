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
	if g.Space > LinearSRGB {
		return fmt.Errorf("%w: unknown color space %d", ErrInvalidPaint, g.Space)
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
	linear        bool // the stops are in linear light
}

// gradientColor returns the straight color of a gradient layer at coord, filtered over
// a pixel of size ems. It is the oracle of slug_paint in slug_core.wgsl.
//
// The color is the mean of the stops over the t range of the pixel, in closed form. A
// jump of color then has no steps, and the cost does not depend on the jump.
func (p *Pack) gradientColor(kind PaintKind, g *layerGradient, coord, ems Point) [4]float32 {
	at := g.toPaint.Apply(coord)
	m := g.toPaint
	// The change of at for one pixel in x, and for one pixel in y.
	du, dv := Point{m[0] * ems.X, m[1] * ems.X}, Point{m[2] * ems.Y, m[3] * ems.Y}

	var t, w, period float32
	cover := float32(1)
	switch kind {
	case RadialGradient:
		t, w, cover = radialPixel(at, du, dv, g.a, g.b, g.cx)
		if cover <= 0 {
			return [4]float32{}
		}
	case SweepGradient:
		span := abs32(g.b - g.a)
		t, period = sweepT(at, g.a, g.b), tau/span
		w = period
		if r2 := at.X*at.X + at.Y*at.Y; r2 > 0 {
			w = hypot32(at.X*du.Y-at.Y*du.X, at.X*dv.Y-at.Y*dv.X) / r2 / span
		}
	default:
		t, w = at.X, hypot32(du.X, dv.X)
	}

	// The range keeps a width at each scale of t.
	w = max(w, abs32(t)/(1<<21)+1e-20)
	lo, hi := t-w/2, t+w/2
	var sum [4]float32
	switch {
	case period > 0 && w >= period:
		lo, hi = 0, period
		p.addSpread(&sum, g, lo, hi)
	case period > 0 && lo < 0: // the seam of a sweep
		p.addSpread(&sum, g, lo+period, period)
		p.addSpread(&sum, g, 0, hi)
	case period > 0 && hi > period:
		p.addSpread(&sum, g, lo, period)
		p.addSpread(&sum, g, 0, hi-period)
	default:
		p.addSpread(&sum, g, lo, hi)
	}
	if sum[3] <= 0 {
		return [4]float32{}
	}
	c := [4]float32{sum[0] / sum[3], sum[1] / sum[3], sum[2] / sum[3], sum[3] / (hi - lo) * cover}
	if g.linear {
		for i := range 3 {
			c[i] = toSRGB(c[i])
		}
	}
	return c
}

// toLinear and toSRGB are the transfer function of sRGB.
func toLinear(v float32) float32 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return float32(math.Pow(float64((v+0.055)/1.055), 2.4))
}

func toSRGB(v float32) float32 {
	if v <= 0.0031308 {
		return v * 12.92
	}
	return 1.055*float32(math.Pow(float64(v), 1/2.4)) - 0.055
}

// addSpread adds the premultiplied integral of the gradient from lo to hi, with spread.
func (p *Pack) addSpread(sum *[4]float32, g *layerGradient, lo, hi float32) {
	if g.spread == Pad {
		p.addStops(sum, g, lo, hi, 1)
		return
	}
	first := float32(math.Floor(float64(lo)))
	last := float32(math.Floor(float64(hi)))
	if first == last {
		p.addUnit(sum, g, first, lo-first, hi-first)
		return
	}
	p.addUnit(sum, g, first, lo-first, 1)
	p.addUnit(sum, g, last, 0, hi-last)
	if whole := last - first - 1; whole > 0 {
		p.addStops(sum, g, 0, 1, whole)
	}
}

// addUnit adds the part from a to b of the unit that starts at the integer n.
func (p *Pack) addUnit(sum *[4]float32, g *layerGradient, n, a, b float32) {
	if g.spread == Reflect && n-2*float32(math.Floor(float64(n/2))) != 0 {
		a, b = 1-b, 1-a
	}
	p.addStops(sum, g, a, b, 1)
}

// addStops adds scale times the premultiplied integral of the stops from a to b. Each
// segment adds only its own part, so a small range keeps its precision.
func (p *Pack) addStops(sum *[4]float32, g *layerGradient, a, b, scale float32) {
	stop := func(i uint32) (float32, [4]float32) {
		w := p.bands[g.stops+i*stopWords:][:stopWords]
		f := math.Float32frombits
		return f(w[0]), [4]float32{f(w[1]), f(w[2]), f(w[3]), f(w[4])}
	}
	add := func(c [4]float32, width float32) {
		for i := range sum {
			sum[i] += c[i] * width * scale
		}
	}
	premultiply := func(c [4]float32) [4]float32 {
		return [4]float32{c[0] * c[3], c[1] * c[3], c[2] * c[3], c[3]}
	}

	lo, loColor := stop(0)
	if a < lo {
		add(premultiply(loColor), min(b, lo)-a)
	}
	for i := uint32(1); i < g.count; i++ {
		hi, hiColor := stop(i)
		if x0, x1 := max(a, lo), min(b, hi); x1 > x0 {
			// x1 is over x0, so the two offsets differ.
			f0, f1 := (x0-lo)/(hi-lo), (x1-lo)/(hi-lo)
			add(segmentMean(loColor, hiColor, f0, f1, g.premultiplied), x1-x0)
		}
		lo, loColor = hi, hiColor
	}
	if b > lo {
		add(premultiply(loColor), b-max(a, lo))
	}
}

// segmentMean returns the premultiplied mean of a segment from f0 to f1. A straight mix
// has a product of two lines, so its mean has a term for the curve.
func segmentMean(a, b [4]float32, f0, f1 float32, premultiplied bool) [4]float32 {
	f := (f0 + f1) / 2
	alpha := a[3] + (b[3]-a[3])*f
	c := [4]float32{0, 0, 0, alpha}
	for i := range 3 {
		if premultiplied {
			c[i] = a[i]*a[3] + (b[i]*b[3]-a[i]*a[3])*f
		} else {
			curve := (b[i] - a[i]) * (b[3] - a[3]) * (f1 - f0) * (f1 - f0) / 12
			c[i] = (a[i]+(b[i]-a[i])*f)*alpha + curve
		}
	}
	return c
}

// radialPixel returns the t of a pixel, the width of its t range, and the part of the
// pixel inside the cone. The t is the larger t whose circle has a radius of at least 0
// and passes through at. The circles have the center (t*cx, 0) and the radius
// r0 + t*(r1-r0). du and dv are the change of at for one pixel.
func radialPixel(at, du, dv Point, r0, r1, cx float32) (t, w, cover float32) {
	dr := r1 - r0
	a := cx*cx - dr*dr
	b := at.X*cx + r0*dr
	c := at.X*at.X + at.Y*at.Y - r0*r0
	db := hypot32(du.X*cx, dv.X*cx)

	var limit float32 // the largest width, near the border of the cone
	cover = 1
	if abs32(a) < nearZero {
		// One root, and the border of the cone is the line b = 0.
		side := b
		if dr < 0 {
			side = -b
		}
		if cover = clamp01(0.5 + side/db); cover <= 0 {
			return 0, 0, 0
		}
		if side < 1e-6 {
			b = 1e-6
			if dr < 0 {
				b = -b
			}
		}
		t = c * 0.5 / b
		limit = 1e30
	} else {
		d := b*b - a*c
		// The change of d for one pixel.
		dx := 2 * (b*cx*du.X - a*(at.X*du.X+at.Y*du.Y))
		dy := 2 * (b*cx*dv.X - a*(at.X*dv.X+at.Y*dv.Y))
		g := hypot32(dx, dy)
		if a > 0 {
			// Nested circles fill the plane, so only a cone has a border.
			if g > 0 {
				cover = clamp01(0.5 + d/g)
			} else if d < 0 {
				cover = 0
			}
			if cover <= 0 {
				return 0, 0, 0
			}
		}
		d = max(d, 0)
		s := sqrt32(d)
		t1, t2 := max((b+s)/a, (b-s)/a), min((b+s)/a, (b-s)/a)
		t = t1
		if r0+t1*dr < 0 {
			if r0+t2*dr < 0 {
				return 0, 0, 0
			}
			t = t2
		}
		limit = (sqrt32(d+g/2) - sqrt32(max(d-g/2, 0)) + db) / abs32(a)
	}

	// The gradient of t is (at - center) / (b - a*t). It has no limit at the border.
	w = limit
	if slope := abs32(b - a*t); slope > 0 {
		x := at.X - t*cx
		w = min(limit, hypot32(x*du.X+at.Y*du.Y, x*dv.X+at.Y*dv.Y)/slope)
	}
	return t, w, cover
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

func hypot32(x, y float32) float32 {
	return sqrt32(x*x + y*y)
}

func sqrt32(v float32) float32 {
	return float32(math.Sqrt(float64(v)))
}

func clamp01(v float32) float32 {
	return min(max(v, 0), 1)
}
