// Derived from the Slug reference shaders.
// Copyright 2017 by Eric Lengyel. Used under the MIT License;
// see LICENSES/Slug-MIT.txt.

package slug

import (
	"errors"
	"fmt"
	"image"
	"math"
)

// ErrSingularView reports a view that Affine.Invert cannot invert.
var ErrSingularView = errors.New("slug: view has no inverse")

// nearZero is the limit of the reference shader, for em-space coordinates.
const nearZero = 1.0 / 65536

// Rasterize draws all layers of e over dst, in order. view maps em space to
// dst pixels; it usually flips Y, because em space is Y-up and dst is Y-down.
// color is the color of a CurrentColor layer. It is straight RGB, like
// Paint.Color. Rasterize blends in sRGB.
func (p *Pack) Rasterize(e Entry, view Affine, color [4]float32, dst *image.RGBA) error {
	inverse, ok := view.Invert()
	if !ok {
		return ErrSingularView
	}
	if uint64(e.FirstLayer)+uint64(e.LayerCount) > uint64(len(p.layers)/LayerWords) {
		return fmt.Errorf("slug: entry %v is outside the pack", e.Key)
	}
	for i := range e.LayerCount {
		l := p.layerAt(e.FirstLayer + i)
		c := color
		if l.kind == Solid {
			c = l.color
		}
		ems := emPerPixel(inverse)
		p.eachPixel(l, view, inverse, dst.Bounds(), func(x, y int, coord Point, cov float32) {
			if l.kind >= LinearGradient {
				c = p.gradientColor(l.kind, &l.gradient, coord, ems)
			}
			blendOver(dst.Pix[dst.PixOffset(x, y):][:4:4], c, cov)
		})
	}
	return nil
}

// Coverage writes the coverage of one layer into dst, for a single-color atlas
// bitmap. Pixels away from the layer do not change.
func (p *Pack) Coverage(layer uint32, view Affine, dst *image.Alpha) error {
	inverse, ok := view.Invert()
	if !ok {
		return ErrSingularView
	}
	if uint64(layer) >= uint64(len(p.layers)/LayerWords) {
		return fmt.Errorf("slug: layer %d is outside the pack", layer)
	}
	p.eachPixel(p.layerAt(layer), view, inverse, dst.Bounds(), func(x, y int, _ Point, cov float32) {
		dst.Pix[dst.PixOffset(x, y)] = unorm8(cov)
	})
	return nil
}

// layerRecord is the decoded form of one record of Pack.Layers.
type layerRecord struct {
	bandLoc       uint32
	nH, nV        int
	rule          FillRule
	kind          PaintKind
	scale, offset Point
	bounds        Rect
	color         [4]float32
	gradient      layerGradient
}

func (p *Pack) layerAt(i uint32) layerRecord {
	r := p.layers[i*LayerWords:][:LayerWords]
	f := math.Float32frombits
	l := layerRecord{
		bandLoc: r[0],
		nH:      int(r[1] & 0xFFFF),
		nV:      int(r[1] >> 16),
		rule:    FillRule(r[2] & 0xFF),
		kind:    PaintKind(r[2] >> 8 & 0xFF),
		scale:   Point{f(r[4]), f(r[5])},
		offset:  Point{f(r[6]), f(r[7])},
		bounds:  Rect{Point{f(r[8]), f(r[9])}, Point{f(r[10]), f(r[11])}},
		color:   [4]float32{f(r[12]), f(r[13]), f(r[14]), f(r[15])},
	}
	if l.kind >= LinearGradient {
		l.gradient = layerGradient{
			gradientRecord: gradientRecord{
				toPaint: Affine{f(r[16]), f(r[17]), f(r[18]), f(r[19]), f(r[20]), f(r[21])},
				a:       f(r[14]),
				b:       f(r[15]),
				cx:      f(r[22]),
			},
			stops:         r[12],
			count:         r[13] & 0xFFFF,
			spread:        Spread(r[13] >> 16 & 0xFF),
			premultiplied: r[13]>>24&1 != 0,
		}
	}
	return l
}

// eachPixel calls visit for each pixel of area that l can touch.
func (p *Pack) eachPixel(l layerRecord, view, inverse Affine, area image.Rectangle, visit func(x, y int, coord Point, cov float32)) {
	ems := emPerPixel(inverse)
	pixelsPerEm := Point{1 / ems.X, 1 / ems.Y}
	// A coverage is not 0 only up to half of ems from the bounds.
	reach := Point{ems.X / 2, ems.Y / 2}
	box := Rect{Point{l.bounds.Min.X - reach.X, l.bounds.Min.Y - reach.Y}, Point{l.bounds.Max.X + reach.X, l.bounds.Max.Y + reach.Y}}
	r := pixelRange(box, view, area)
	for dy := range r.Dy() {
		for dx := range r.Dx() {
			x, y := r.Min.X+dx, r.Min.Y+dy
			coord := inverse.Apply(Point{float32(x) + 0.5, float32(y) + 0.5})
			visit(x, y, coord, p.coverageAt(l, coord, pixelsPerEm))
		}
	}
}

// pixelRange returns the part of area that bounds covers after view. The margin
// of one pixel keeps the antialiased edge inside the range.
func pixelRange(bounds Rect, view Affine, area image.Rectangle) image.Rectangle {
	lo := view.Apply(bounds.Min)
	hi := lo
	for _, c := range []Point{{bounds.Max.X, bounds.Min.Y}, {bounds.Min.X, bounds.Max.Y}, bounds.Max} {
		q := view.Apply(c)
		lo = Point{min(lo.X, q.X), min(lo.Y, q.Y)}
		hi = Point{max(hi.X, q.X), max(hi.Y, q.Y)}
	}
	// Clamp as float64: a far coordinate does not fit an int.
	clamp := func(v float64, a, b int) int { return int(min(max(v, float64(a)), float64(b))) }
	return image.Rectangle{
		Min: image.Point{
			clamp(math.Floor(float64(lo.X))-1, area.Min.X, area.Max.X),
			clamp(math.Floor(float64(lo.Y))-1, area.Min.Y, area.Max.Y),
		},
		Max: image.Point{
			clamp(math.Ceil(float64(hi.X))+1, area.Min.X, area.Max.X),
			clamp(math.Ceil(float64(hi.Y))+1, area.Min.Y, area.Max.Y),
		},
	}
}

// emPerPixel is the size of one pixel in em space: fwidth(renderCoord).
func emPerPixel(inverse Affine) Point {
	return Point{abs32(inverse[0]) + abs32(inverse[2]), abs32(inverse[1]) + abs32(inverse[3])}
}

// coverageAt returns the coverage of one layer at coord.
func (p *Pack) coverageAt(l layerRecord, coord, pixelsPerEm Point) float32 {
	h := min(max(int(coord.Y*l.scale.Y+l.offset.Y), 0), l.nH-1)
	v := min(max(int(coord.X*l.scale.X+l.offset.X), 0), l.nV-1)
	xcov, xwgt := p.rayCoverage(p.bandList(l, h), coord, false, pixelsPerEm.X)
	ycov, ywgt := p.rayCoverage(p.bandList(l, l.nH+v), coord, true, pixelsPerEm.Y)
	return combine(xcov, ycov, xwgt, ywgt, l.rule)
}

// bandList returns the curve list of a band. The vertical bands come after the
// horizontal bands.
func (p *Pack) bandList(l layerRecord, band int) []uint32 {
	header := p.bands[l.bandLoc+uint32(2*band):]
	return p.bands[l.bandLoc+header[1]:][:header[0]]
}

// rayCoverage walks one curve list. A vertical ray is a horizontal ray with X
// and Y swapped. The swap reverses the orientation, so its coverage has the
// opposite sign.
func (p *Pack) rayCoverage(list []uint32, coord Point, vertical bool, pixelsPerUnit float32) (cov, wgt float32) {
	for _, loc := range list {
		c := p.curves[loc*4:][:6]
		x1, y1 := c[0]-coord.X, c[1]-coord.Y
		x2, y2 := c[2]-coord.X, c[3]-coord.Y
		x3, y3 := c[4]-coord.X, c[5]-coord.Y
		if vertical {
			x1, y1, x2, y2, x3, y3 = y1, x1, y2, x2, y3, x3
		}

		// The list is in descending order: no later curve can reach this pixel.
		if max(x1, x2, x3)*pixelsPerUnit < -0.5 {
			break
		}
		code := rootCode(y1, y2, y3)
		if code == 0 {
			continue
		}
		r1, r2 := solvePoly(x1, y1, x2, y2, x3, y3)
		r1, r2 = r1*pixelsPerUnit, r2*pixelsPerUnit
		if code&1 != 0 {
			cov += sat(r1 + 0.5)
			wgt = max(wgt, sat(1-abs32(r1)*2))
		}
		if code > 1 {
			cov -= sat(r2 + 0.5)
			wgt = max(wgt, sat(1-abs32(r2)*2))
		}
	}
	if vertical {
		cov = -cov
	}
	return cov, wgt
}

// rootCode returns which roots contribute: bit 0 and bit 8. It reads the sign
// bits, as the shader does, so -0 counts as negative.
func rootCode(y1, y2, y3 float32) uint32 {
	i1 := math.Float32bits(y1) >> 31
	i2 := math.Float32bits(y2) >> 30
	i3 := math.Float32bits(y3) >> 29
	shift := i2&2 | i1&^2
	shift = i3&4 | shift&^4
	return 0x2E74 >> shift & 0x0101
}

// solvePoly returns the x of the two crossings of y = 0. Imaginary roots become
// a double root at the extreme of the curve. A division by zero is safe here: a
// nearly linear curve replaces the result, and sat removes a NaN.
func solvePoly(x1, y1, x2, y2, x3, y3 float32) (r1, r2 float32) {
	ax, ay := x1-x2*2+x3, y1-y2*2+y3
	bx, by := x1-x2, y1-y2
	d := float32(math.Sqrt(float64(max(by*by-ay*y1, 0))))
	t1, t2 := (by-d)/ay, (by+d)/ay
	if abs32(ay) < nearZero {
		t1 = y1 * 0.5 / by
		t2 = t1
	}
	return (ax*t1-bx*2)*t1 + x1, (ax*t2-bx*2)*t2 + x1
}

// combine joins the two rays and applies the fill rule.
func combine(xcov, ycov, xwgt, ywgt float32, rule FillRule) float32 {
	c := max(abs32(xcov*xwgt+ycov*ywgt)/max(xwgt+ywgt, nearZero), min(abs32(xcov), abs32(ycov)))
	if rule == EvenOdd {
		half := c * 0.5
		return 1 - abs32(1-(half-float32(math.Floor(float64(half))))*2)
	}
	return sat(c)
}

// blendOver draws color at coverage cov over one premultiplied RGBA pixel.
func blendOver(px []uint8, color [4]float32, cov float32) {
	a := color[3] * cov
	for i := range 3 {
		px[i] = unorm8(color[i]*a + float32(px[i])/255*(1-a))
	}
	px[3] = unorm8(a + float32(px[3])/255*(1-a))
}

func unorm8(v float32) uint8 { return uint8(sat(v)*255 + 0.5) }

// sat is the HLSL saturate: unlike min and max, it maps NaN to 0.
func sat(v float32) float32 {
	if !(v > 0) {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func abs32(v float32) float32 { return float32(math.Abs(float64(v))) }
