// Package scene holds the sample drawings, and draws them on the CPU. It does not
// import a GPU package, so the two renderers stay apart.
package scene

import (
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/font/gofont/goregular"

	slug "github.com/dhannyell/go-slug"
	"github.com/dhannyell/go-slug/font"
	"github.com/dhannyell/go-slug/text"
)

//go:embed fonts/Amiri-Regular.ttf
var amiri []byte

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
	sourceShaped
	sourceHall
	sourceNewsHead
	sourceNewsTitle
)

// Item places one entry of the pack.
type Item struct {
	Entry slug.Entry
	Place slug.Affine // em space to design pixels
	Color [4]float32
}

// Span is a range of Items that one matrix draws.
type Span struct {
	First, Count int
	View         Projection // from the coordinates of the items to target pixels
}

// MaxSpans is the largest number of spans in a frame. A versus view has each span
// twice.
const MaxSpans = 64

// Scene is the pack, the items that use it, and the camera.
type Scene struct {
	Name     string
	Animated bool // the view changes with the time
	Versus   bool // an atlas can draw the scene: each glyph has one color
	Pack     slug.Pack
	Items    []Item
	Orbit    *Orbit // the view that the user controls, or nil

	frame func(t float64, w, h int) []Span
}

// Names lists the scenes that New can build.
var Names = []string{"overview", "zoom", "perspective", "text", "hall", "dive", "news"}

// New builds the scene with the given name.
func New(name string) (*Scene, error) {
	s := &Scene{Name: name}
	switch name {
	case "overview":
		s.frame = s.whole(still)
		return s, s.overview()
	case "zoom":
		s.Animated, s.frame = true, s.whole(zoom)
		return s, s.overview()
	case "perspective":
		s.Animated, s.frame = true, s.whole(perspective)
		return s, s.overview()
	case "text":
		s.frame = s.whole(still)
		return s, s.shaped()
	case "hall":
		return s, s.hall()
	case "dive":
		return s, s.dive()
	case "news":
		return s, s.news()
	}
	return nil, fmt.Errorf("scene: no scene %q", name)
}

// Frame returns the spans at t seconds, for a target of w by h pixels, in the order
// from back to front.
func (s *Scene) Frame(t float64, w, h int) []Span { return s.frame(t, w, h) }

// whole draws all items of the scene through one view.
func (s *Scene) whole(view func(t float64, w, h int) Projection) func(float64, int, int) []Span {
	return func(t float64, w, h int) []Span {
		return []Span{{0, len(s.Items), view(t, w, h)}}
	}
}

func (s *Scene) overview() error {
	f, err := font.Parse(goregular.TTF)
	if err != nil {
		return err
	}
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
			return err
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
			return err
		}
	}
	return nil
}

// shaped shows what the text module adds to the font package.
func (s *Scene) shaped() error {
	f, err := text.Parse(amiri, sourceShaped)
	if err != nil {
		return err
	}
	plain, err := font.Parse(amiri)
	if err != nil {
		return err
	}

	const sample = "office affluent fjord"
	if err := s.line(f, "Shaped: "+sample, text.Options{}, 54, 40, 90, ink); err != nil {
		return err
	}
	if err := s.text(plain, "Plain:  "+sample, 54, slug.Affine{1, 0, 0, 1, 40, 158}, faded); err != nil {
		return err
	}
	// Cursive joins, marks over the letters, and a run from right to left.
	if err := s.line(f, "مَرْحَبًا بِالعَالَم", text.Options{RTL: true}, 78, 920, 262, accent); err != nil {
		return err
	}
	if err := s.line(f, "Slug رسم الخطوط works in one line", text.Options{}, 44, 40, 340, ink); err != nil {
		return err
	}

	const size = 24
	paragraph := "The text module shapes a string into glyphs, and breaks it into lines. " +
		"The pack stores each glyph once, and a ligature is one more glyph."
	y := float32(400)
	for _, l := range f.Shape(paragraph, text.Options{Width: 880.0 / size}) {
		if err := s.place(f, l, size, 40, y, ink); err != nil {
			return err
		}
		y += size * 1.35
	}
	return nil
}

// line adds the first line of a shaped string. With a right-to-left base, x is
// the right end of the line.
func (s *Scene) line(f *text.Font, str string, o text.Options, size, x, y float32, c [4]float32) error {
	l := f.Shape(str, o)[0]
	if o.RTL {
		x -= l.Width * size
	}
	return s.place(f, l, size, x, y, c)
}

func (s *Scene) place(f *text.Font, l text.Line, size, x, y float32, c [4]float32) error {
	for _, g := range l.Glyphs {
		e, err := f.Add(&s.Pack, g)
		if err != nil {
			return err
		}
		s.Items = append(s.Items, Item{e, g.At(size).Then(slug.Affine{1, 0, 0, 1, x, y}), c})
	}
	return nil
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
		if err := s.add(slug.Key{Source: sourceFont, ID: i}, g.Drawing, place, c); err != nil {
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

func still(_ float64, w, h int) Projection { return FromAffine(Fit(w, h)) }

// ZoomPeriod is the time of one zoom in and out. The peak is at half of it.
const ZoomPeriod = 20.0

// zoom moves from the whole design to one letter of the 16-pixel line.
func zoom(t float64, w, h int) Projection {
	const peak = 256
	const targetX, targetY = 45.0, 178.0

	fit := FromAffine(Fit(w, h))
	k := math.Pow(peak, 0.5-0.5*math.Cos(2*math.Pi*t/ZoomPeriod))
	// The target moves to the center of the image as the zoom grows.
	x, y := fit.Apply(targetX, targetY)
	blend := 1 - 1/k
	cx := x + (float64(w)/2-x)*blend
	cy := y + (float64(h)/2-y)*blend
	return fit.Then(Projection{k, 0, cx - k*x, 0, k, cy - k*y, 0, 0, 1})
}

// perspective turns the design as a plane in front of a camera.
func perspective(t float64, w, h int) Projection {
	const distance = 1100 // from the camera to the plane, in design pixels
	const margin = 0.85   // the corners stay inside the image as the plane turns
	sinY, cosY := math.Sincos(0.65 * math.Sin(t*0.5))
	sinX, cosX := math.Sincos(0.40 * math.Cos(t*0.35))
	turned := Projection{
		cosY, sinY * sinX, 0,
		0, cosX, 0,
		-sinY / distance, cosY * sinX / distance, 1,
	}
	return Projection{1, 0, -Width / 2, 0, 1, -Height / 2, 0, 0, 1}.
		Then(turned).
		Then(Projection{margin, 0, Width / 2, 0, margin, Height / 2, 0, 0, 1}).
		Then(FromAffine(Fit(w, h)))
}

// Rasterize draws the scene on the CPU, over Background.
func (s *Scene) Rasterize(w, h int, frame []Span) (*image.RGBA, error) {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(Background), image.Point{}, draw.Src)
	for _, span := range frame {
		if err := s.rasterizeSpan(span, dst); err != nil {
			return nil, err
		}
	}
	return dst, nil
}

func (s *Scene) rasterizeSpan(span Span, dst *image.RGBA) error {
	items := s.Items[span.First:][:span.Count]
	if span.View.IsAffine() {
		m := span.View.Tangent(0, 0)
		for _, it := range items {
			if err := s.Pack.Rasterize(it.Entry, it.Place.Then(m), it.Color, dst); err != nil {
				return err
			}
		}
		return nil
	}
	for _, it := range items {
		if it.Entry.LayerCount == 0 {
			continue
		}
		area := reach(it, span.View).Intersect(dst.Rect)
		for y := area.Min.Y; y < area.Max.Y; y++ {
			for x := area.Min.X; x < area.Max.X; x++ {
				px := dst.SubImage(image.Rect(x, y, x+1, y+1)).(*image.RGBA)
				if err := s.Pack.Rasterize(it.Entry, it.Place.Then(pixelView(span.View, x, y)), it.Color, px); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// pixelView returns the tangent of view at the center of one pixel. The tangent
// is exact at the only point that the pixel samples, so a perspective needs only
// the affine API of the pack.
func pixelView(view Projection, x, y int) slug.Affine {
	dx, dy := view.Invert().Apply(float64(x)+0.5, float64(y)+0.5)
	return view.Tangent(dx, dy)
}

// Nearby is the part of a span that can touch one pixel.
type Nearby struct {
	Items []Item
	View  Projection
}

// Near returns the items that can touch the pixel x, y through a view moved by
// less than one pixel, in the order of the frame.
func (s *Scene) Near(frame []Span, x, y int) []Nearby {
	var near []Nearby
	for _, span := range frame {
		var items []Item
		for _, it := range s.Items[span.First:][:span.Count] {
			if it.Entry.LayerCount > 0 && image.Pt(x, y).In(reach(it, span.View)) {
				items = append(items, it)
			}
		}
		if items != nil {
			near = append(near, Nearby{items, span.View})
		}
	}
	return near
}

// Pixel draws one pixel on the CPU, with the result of Near and the views moved by
// dx and dy.
func (s *Scene) Pixel(near []Nearby, dx, dy float64, x, y int) (color.RGBA, error) {
	px := image.NewRGBA(image.Rect(x, y, x+1, y+1))
	px.SetRGBA(x, y, Background)
	for _, n := range near {
		m := pixelView(n.View.Moved(dx, dy), x, y)
		for _, it := range n.Items {
			if err := s.Pack.Rasterize(it.Entry, it.Place.Then(m), it.Color, px); err != nil {
				return color.RGBA{}, err
			}
		}
	}
	return px.RGBAAt(x, y), nil
}

// QuadPixel draws one pixel with the sizes that a GPU computes for fwidth: the
// differences between the pixels of a 2x2 block, and not the tangent. The first
// row and column of the block give the coarse form, and the row and column of the
// pixel give the fine form. A plane near its edge has a large difference.
func (s *Scene) QuadPixel(near []Nearby, fine bool, x, y int) (color.RGBA, error) {
	px := image.NewRGBA(image.Rect(x, y, x+1, y+1))
	px.SetRGBA(x, y, Background)
	left, top := x&^1, y&^1
	row, column := top, left
	if fine {
		row, column = y, x
	}
	for _, n := range near {
		inverse := n.View.Invert()
		at := func(i, j int) (float64, float64) { return inverse.Apply(float64(i)+0.5, float64(j)+0.5) }
		x0, y0 := at(left, row)
		x1, y1 := at(left+1, row)
		x2, y2 := at(column, top)
		x3, y3 := at(column, top+1)
		ux, vx, uy, vy := x1-x0, y1-y0, x3-x2, y3-y2
		det := ux*vy - uy*vx
		a, c, b, d := vy/det, -uy/det, -vx/det, ux/det
		cx, cy := at(x, y)
		m := slug.Affine{
			float32(a), float32(b), float32(c), float32(d),
			float32(float64(x) + 0.5 - a*cx - c*cy), float32(float64(y) + 0.5 - b*cx - d*cy),
		}
		for _, it := range n.Items {
			if err := s.Pack.Rasterize(it.Entry, it.Place.Then(m), it.Color, px); err != nil {
				return color.RGBA{}, err
			}
		}
	}
	return px.RGBAAt(x, y), nil
}

// reach returns the pixels near the bounds of an item, as the shader dilates them:
// each corner moves by half of the size of a pixel in em space at that corner. A
// plane near its edge has a large size, so the area is more than a small margin.
func reach(it Item, view Projection) image.Rectangle {
	const margin = 2
	b := it.Entry.Bounds
	x0, y0 := math.Inf(1), math.Inf(1)
	x1, y1 := math.Inf(-1), math.Inf(-1)
	for _, c := range []struct{ em, side slug.Point }{
		{b.Min, slug.Point{X: -1, Y: -1}},
		{slug.Point{X: b.Max.X, Y: b.Min.Y}, slug.Point{X: 1, Y: -1}},
		{slug.Point{X: b.Min.X, Y: b.Max.Y}, slug.Point{X: -1, Y: 1}},
		{b.Max, slug.Point{X: 1, Y: 1}},
	} {
		p := it.Place.Apply(c.em)
		inverse, ok := it.Place.Then(view.Tangent(float64(p.X), float64(p.Y))).Invert()
		if !ok {
			continue
		}
		moved := slug.Point{
			X: c.em.X + c.side.X*(abs32(inverse[0])+abs32(inverse[2]))/2,
			Y: c.em.Y + c.side.Y*(abs32(inverse[1])+abs32(inverse[3]))/2,
		}
		p = it.Place.Apply(moved)
		// A corner behind the camera has no image, so the item can touch any pixel.
		if view[6]*float64(p.X)+view[7]*float64(p.Y)+view[8] <= 0 {
			return image.Rect(math.MinInt32, math.MinInt32, math.MaxInt32, math.MaxInt32)
		}
		x, y := view.Apply(float64(p.X), float64(p.Y))
		x0, y0 = min(x0, x), min(y0, y)
		x1, y1 = max(x1, x), max(y1, y)
	}
	return image.Rect(int(x0)-margin, int(y0)-margin, int(x1)+margin, int(y1)+margin)
}

// Label draws a short text with its baseline at x, y over dst.
func Label(dst *image.RGBA, x, y float32, str string) error {
	f, err := font.Parse(goregular.TTF)
	if err != nil {
		return err
	}
	s := &Scene{}
	if err := s.text(f, str, 18, slug.Affine{1, 0, 0, 1, x, y}, accent); err != nil {
		return err
	}
	for _, it := range s.Items {
		if err := s.Pack.Rasterize(it.Entry, it.Place, it.Color, dst); err != nil {
			return err
		}
	}
	return nil
}

func abs32(v float32) float32 { return float32(math.Abs(float64(v))) }

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
