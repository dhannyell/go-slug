// Package scene holds the sample drawing, and draws it on the CPU. It does not
// import a GPU package, so the two renderers stay apart.
package scene

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/font/gofont/goregular"

	slug "github.com/dhannyell/go-slug"
	"github.com/dhannyell/go-slug/font"
)

// Width and Height are the design size, in pixels.
const (
	Width  = 960
	Height = 540
)

// Background is the color under the scene.
var Background = color.RGBA{246, 244, 238, 255}

var (
	ink    = [4]float32{0.08, 0.09, 0.13, 1}
	accent = [4]float32{0.62, 0.13, 0.20, 1}
	faded  = [4]float32{0.08, 0.09, 0.13, 0.55}
)

const (
	sourceFont uint32 = iota
	sourceShape
)

// Item places one entry of the pack.
type Item struct {
	Entry slug.Entry
	Place slug.Affine // em space to design pixels
	Color [4]float32
}

// Scene is the pack and the items that use it.
type Scene struct {
	Pack  slug.Pack
	Items []Item
}

// New builds the scene.
func New() (*Scene, error) {
	f, err := font.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	s := &Scene{}
	lines := []struct {
		text string
		size float32
		line slug.Affine
		c    [4]float32
	}{
		{"Slug draws curves", 84, slug.Affine{1, 0, 0, 1, 40, 96}, ink},
		{"The GPU reads the outline, not a bitmap.", 34, slug.Affine{1, 0, 0, 1, 40, 150}, ink},
		{"Small text keeps its shape at 16 pixels, and at 11.", 16, slug.Affine{1, 0, 0, 1, 40, 184}, ink},
		{"The quick brown fox jumps over the lazy dog 0123456789", 11, slug.Affine{1, 0, 0, 1, 40, 206}, ink},
		{"rotated", 44, turn(-18).Then(slug.Affine{1, 0, 0, 1, 60, 340}), accent},
		{"sheared", 44, slug.Affine{1, 0, -0.45, 1, 90, 410}, faded},
		{"go-slug", 150, slug.Affine{1, 0, 0, 1, 40, 520}, accent},
	}
	for _, l := range lines {
		if err := s.text(f, l.text, l.size, l.line, l.c); err != nil {
			return nil, err
		}
	}

	shapes := []struct {
		d     slug.Drawing
		place slug.Affine
		c     [4]float32
	}{
		{slug.Drawing{Em: 1, Layers: []slug.Layer{{Path: star(slug.NonZero)}}}, slug.Affine{150, 0, 0, -150, 610, 400}, ink},
		{slug.Drawing{Em: 1, Layers: []slug.Layer{{Path: star(slug.EvenOdd)}}}, slug.Affine{150, 0, 0, -150, 780, 400}, ink},
		{badge(), slug.Affine{120, 0, 0, -120, 800, 530}, accent},
	}
	for i, d := range shapes {
		if err := s.add(slug.Key{Source: sourceShape, ID: uint32(i)}, d.d, d.place, d.c); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// text adds one line without shaping. line moves the baseline from the design
// origin, and it can rotate or shear the line.
func (s *Scene) text(f *font.Font, text string, size float32, line slug.Affine, c [4]float32) error {
	var pen float32
	for _, r := range text {
		i, err := f.GlyphIndex(r)
		if err != nil {
			return fmt.Errorf("scene: rune %q: %w", r, err)
		}
		g, err := f.Glyph(i)
		if err != nil {
			return fmt.Errorf("scene: rune %q: %w", r, err)
		}
		place := slug.Affine{size, 0, 0, -size, pen, 0}.Then(line)
		if err := s.add(slug.Key{Source: sourceFont, ID: uint32(i)}, g.Drawing, place, c); err != nil {
			return err
		}
		pen += g.Advance / g.Drawing.Em * size
	}
	return nil
}

func (s *Scene) add(key slug.Key, d slug.Drawing, place slug.Affine, c [4]float32) error {
	e, ok := s.Pack.Lookup(key)
	if !ok {
		var err error
		if e, err = s.Pack.Add(key, d); err != nil {
			return fmt.Errorf("scene: add %v: %w", key, err)
		}
	}
	s.Items = append(s.Items, Item{e, place, c})
	return nil
}

// Fit returns the matrix from design pixels to a target of w by h pixels. It
// keeps the aspect and centers the design.
func Fit(w, h int) slug.Affine {
	k := min(float32(w)/Width, float32(h)/Height)
	return slug.Affine{k, 0, 0, k, (float32(w) - Width*k) / 2, (float32(h) - Height*k) / 2}
}

// Rasterize draws the scene on the CPU, over Background. dx and dy move the
// scene, in pixels.
func (s *Scene) Rasterize(w, h int, dx, dy float32) (*image.RGBA, error) {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(Background), image.Point{}, draw.Src)
	fit := Fit(w, h)
	fit[4] += dx
	fit[5] += dy
	for _, it := range s.Items {
		if err := s.Pack.Rasterize(it.Entry, it.Place.Then(fit), it.Color, dst); err != nil {
			return nil, err
		}
	}
	return dst, nil
}

func turn(degrees float64) slug.Affine {
	sin, cos := math.Sincos(degrees * math.Pi / 180)
	return slug.Affine{float32(cos), float32(sin), float32(-sin), float32(cos), 0, 0}
}

// star has one contour that crosses itself, so the two fill rules differ.
func star(rule slug.FillRule) slug.Path {
	p := slug.Path{Rule: rule}
	for i := range 5 {
		sin, cos := math.Sincos(float64(i) * 4 * math.Pi / 5)
		v := slug.MoveTo
		if i > 0 {
			v = slug.LineTo
		}
		p.Verbs = append(p.Verbs, v)
		p.Points = append(p.Points, slug.Point{X: 0.5 + 0.5*float32(sin), Y: 0.5 + 0.5*float32(cos)})
	}
	p.Verbs = append(p.Verbs, slug.Close)
	return p
}

// badge has a solid layer, a translucent layer, and a layer in the host color.
func badge() slug.Drawing {
	return slug.Drawing{Em: 1, Layers: []slug.Layer{
		{Path: circle(0.42, 0.5, 0.42), Paint: slug.Paint{Kind: slug.Solid, Color: [4]float32{0.13, 0.36, 0.62, 1}}},
		{Path: circle(0.72, 0.5, 0.3), Paint: slug.Paint{Kind: slug.Solid, Color: [4]float32{0.95, 0.70, 0.15, 0.6}}},
		{Path: circle(0.42, 0.5, 0.14)},
	}}
}

// circle uses cubic curves, so the pack must convert them to quadratic curves.
func circle(x, y, r float32) slug.Path {
	const kappa = 0.5522848
	k := r * kappa
	return slug.Path{
		Verbs: []slug.Verb{slug.MoveTo, slug.CubeTo, slug.CubeTo, slug.CubeTo, slug.CubeTo, slug.Close},
		Points: []slug.Point{
			{X: x + r, Y: y},
			{X: x + r, Y: y + k}, {X: x + k, Y: y + r}, {X: x, Y: y + r},
			{X: x - k, Y: y + r}, {X: x - r, Y: y + k}, {X: x - r, Y: y},
			{X: x - r, Y: y - k}, {X: x - k, Y: y - r}, {X: x, Y: y - r},
			{X: x + k, Y: y - r}, {X: x + r, Y: y - k}, {X: x + r, Y: y},
		},
	}
}
