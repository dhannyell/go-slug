package scene

import (
	"math"

	"golang.org/x/image/font/gofont/gobold"

	slug "github.com/dhannyell/go-slug"
	"github.com/dhannyell/go-slug/font"
)

var (
	sunrise = []slug.Stop{
		{Offset: 0, Color: [4]float32{0.98, 0.78, 0.20, 1}},
		{Offset: 0.5, Color: [4]float32{0.90, 0.25, 0.35, 1}},
		{Offset: 1, Color: [4]float32{0.20, 0.15, 0.55, 1}},
	}
	rainbow = []slug.Stop{
		{Offset: 0, Color: [4]float32{0.90, 0.20, 0.25, 1}},
		{Offset: 0.25, Color: [4]float32{0.95, 0.80, 0.15, 1}},
		{Offset: 0.5, Color: [4]float32{0.20, 0.70, 0.40, 1}},
		{Offset: 0.75, Color: [4]float32{0.15, 0.45, 0.85, 1}},
		{Offset: 1, Color: [4]float32{0.90, 0.20, 0.25, 1}},
	}
	stripes = []slug.Stop{
		{Offset: 0.5, Color: [4]float32{0.10, 0.12, 0.30, 1}},
		{Offset: 0.5, Color: [4]float32{0.95, 0.70, 0.15, 1}},
	}
	fade = []slug.Stop{
		{Offset: 0, Color: [4]float32{0.90, 0.20, 0.25, 1}},
		{Offset: 1, Color: [4]float32{0.15, 0.45, 0.85, 0}},
	}
)

func spread(p slug.Paint, s slug.Spread) slug.Paint {
	p.Gradient.Spread = s
	return p
}

func premultiplied(p slug.Paint) slug.Paint {
	p.Gradient.Premultiplied = true
	return p
}

func sheared(p slug.Paint) slug.Paint {
	p.Gradient.Transform = &slug.Affine{1, 0, 0.6, 1, 0, 0}
	return p
}

// gradients shows each kind and spread, the two mixes of alpha, a stroke, a clip and
// text.
func (s *Scene) gradients() error {
	center := slug.Point{X: 0.5, Y: 0.5}
	shapes := []slug.Paint{
		slug.LinearPaint(slug.Point{X: 0.1, Y: 0}, slug.Point{X: 0.9, Y: 1}, sunrise...),
		spread(slug.LinearPaint(slug.Point{X: 0.35, Y: 0}, slug.Point{X: 0.65, Y: 0}, sunrise...), slug.Reflect),
		slug.RadialPaint(center, 0, center, 0.5, sunrise...),
		spread(slug.RadialPaint(slug.Point{X: 0.35, Y: 0.6}, 0, center, 0.25, sunrise...), slug.Repeat),
		slug.SweepPaint(center, 0, 2*math.Pi, rainbow...),
		spread(slug.SweepPaint(center, -math.Pi/2, -math.Pi/6, sunrise...), slug.Reflect),
		sheared(spread(slug.LinearPaint(slug.Point{}, slug.Point{X: 0.2}, stripes...), slug.Repeat)),
		slug.LinearPaint(slug.Point{}, slug.Point{X: 1}, fade...),
		premultiplied(slug.LinearPaint(slug.Point{}, slug.Point{X: 1}, fade...)),
		// The second circle does not hold the first, so the circles make a cone.
		slug.RadialPaint(slug.Point{X: 0.1, Y: 0.5}, 0, slug.Point{X: 0.6, Y: 0.5}, 0.3, sunrise...),
	}
	const size, step = 130, 150
	for i, paint := range shapes {
		d := slug.Drawing{Em: 1, Layers: []slug.Layer{{Path: circle(0.5, 0.5, 0.5), Paint: paint}}}
		place := slug.Affine{size, 0, 0, -size, 30 + float32(i%6)*step, 150 + float32(i/6)*step}
		if err := s.add(slug.Key{Source: sourceGradient, ID: uint32(i)}, d, place, ink); err != nil {
			return err
		}
	}

	outline, err := slug.Drawing{Em: 1, Layers: []slug.Layer{{
		Path:  circle(0.5, 0.5, 0.42),
		Paint: slug.SweepPaint(center, 0, 2*math.Pi, rainbow...),
	}}}.Stroke(slug.Stroke{Width: 0.12})
	if err != nil {
		return err
	}
	place := slug.Affine{size, 0, 0, -size, 30 + 4*step, 150 + step}
	if err := s.add(slug.Key{Source: sourceGradient, ID: uint32(len(shapes))}, outline, place, ink); err != nil {
		return err
	}

	// The clip keeps the disc inside the union of two smaller circles.
	clipped := slug.Drawing{Em: 1, Layers: []slug.Layer{{
		Path:  circle(0.5, 0.5, 0.5),
		Paint: slug.SweepPaint(center, 0, 2*math.Pi, rainbow...),
		Clip:  []slug.ClipPath{{Path: circle(0.32, 0.5, 0.3)}, {Path: circle(0.68, 0.5, 0.3)}},
	}}}
	place = slug.Affine{size, 0, 0, -size, 30 + 5*step, 150 + step}
	if err := s.add(slug.Key{Source: sourceGradient, ID: uint32(len(shapes) + 1)}, clipped, place, ink); err != nil {
		return err
	}
	return s.gradientText("gradients", 150, 40, 500, uint32(len(shapes)+2))
}

// gradientText paints a line with one gradient. Each glyph has its own space, so its
// line of the gradient starts at the pen.
func (s *Scene) gradientText(str string, size, x, y float32, id uint32) error {
	f, err := font.Parse(gobold.TTF)
	if err != nil {
		return err
	}
	var width float32
	for _, r := range str {
		i, err := f.GlyphIndex(r)
		if err != nil {
			return err
		}
		g, err := f.Glyph(i)
		if err != nil {
			return err
		}
		width += g.Advance / g.Drawing.Em
	}

	var pen float32
	for _, r := range str {
		i, err := f.GlyphIndex(r)
		if err != nil {
			return err
		}
		g, err := f.Glyph(i)
		if err != nil {
			return err
		}
		em := g.Drawing.Em
		paint := slug.LinearPaint(slug.Point{X: -pen * em}, slug.Point{X: (width - pen) * em}, rainbow...)
		for l := range g.Drawing.Layers {
			g.Drawing.Layers[l].Paint = paint
		}
		place := slug.Affine{size, 0, 0, -size, x + pen*size, y}
		if err := s.add(slug.Key{Source: sourceGradient, ID: id}, g.Drawing, place, ink); err != nil {
			return err
		}
		id++
		pen += g.Advance / em
	}
	return nil
}
