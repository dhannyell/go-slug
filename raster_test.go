package slug

import (
	"cmp"
	"errors"
	"image"
	"math"
	"slices"
	"testing"
)

// With this em and pixelView, one drawing unit is one pixel.
const rasterEm = 64

var pixelView = Affine{rasterEm, 0, 0, rasterEm, 0, 0}

// tolerance of a coverage comparison, in steps of 8 bits. Fused float operations
// change the last bits between architectures.
const coverTol = 2

// circlePath is a closed circle of quadratics. Each control point is on the
// tangents at the ends of its arc.
func circlePath(cx, cy, r float64, count int, reverse bool) Path {
	at := func(i, radius float64) Point {
		a := i * 2 * math.Pi / float64(count)
		if reverse {
			a = -a
		}
		return Point{float32(cx + radius*math.Cos(a)), float32(cy + radius*math.Sin(a))}
	}
	var p Path
	p.MoveTo(at(0, r))
	for i := range count {
		end := at(float64(i+1), r)
		if i == count-1 {
			end = p.Points[0]
		}
		p.QuadTo(at(float64(i)+0.5, r/math.Cos(math.Pi/float64(count))), end)
	}
	p.Close()
	return p
}

// joined returns one path with the contours of all paths.
func joined(paths ...Path) Path {
	var out Path
	for _, p := range paths {
		out.Verbs = append(out.Verbs, p.Verbs...)
		out.Points = append(out.Points, p.Points...)
	}
	return out
}

// coverage renders layer 0 of a drawing of path into a square bitmap.
func coverage(t *testing.T, path Path, view Affine, size int) *image.Alpha {
	t.Helper()
	var p Pack
	mustAdd(t, &p, 1, Drawing{Em: rasterEm, Layers: []Layer{{Path: path}}})
	dst := image.NewAlpha(image.Rect(0, 0, size, size))
	if err := p.Coverage(0, view, dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

func checkCover(t *testing.T, img *image.Alpha, x, y int, want uint8) {
	t.Helper()
	got := img.AlphaAt(x, y).A
	if d := int(got) - int(want); d < -coverTol || d > coverTol {
		t.Errorf("pixel %d,%d: coverage %d, want %d", x, y, got, want)
	}
}

// area returns the sum of the coverage, in pixels.
func area(img *image.Alpha) float64 {
	var sum float64
	for _, v := range img.Pix {
		sum += float64(v) / 255
	}
	return sum
}

func checkArea(t *testing.T, img *image.Alpha, want, tolerance float64) {
	t.Helper()
	if got := area(img); math.Abs(got-want) > want*tolerance {
		t.Errorf("area = %v, want %v within %v%%", got, want, tolerance*100)
	}
}

// Bands are only an optimization. The result must equal a walk of all curves.
func TestRasterBandsDoNotChangeCoverage(t *testing.T) {
	paths := map[string]Path{
		"circle": circlePath(32, 32, 28, 24, false),
		"comb":   combPath(12),
	}
	views := map[string]Affine{
		"large": pixelView,
		"small": {8, 0, 0, 8, 0.3, 0.7},
		"turn":  Affine{1, 0, 0, 1, -32, -32}.Then(Affine{40, 25, -25, 40, 32, 32}),
	}
	for pathName, path := range paths {
		var p Pack
		mustAdd(t, &p, 1, Drawing{Em: rasterEm, Layers: []Layer{{Path: path}}})
		l := p.layerAt(0)

		// All curves in descending key order: the walk stops at the same place.
		var all []uint32
		for b := range l.nH + l.nV {
			for _, loc := range p.bandList(l, b) {
				if !slices.Contains(all, loc) {
					all = append(all, loc)
				}
			}
		}
		key := func(loc uint32, axis int) float32 {
			c := p.Curves()[loc*4:]
			return max(c[axis], c[axis+2], c[axis+4])
		}
		sorted := func(axis int) []uint32 {
			s := slices.Clone(all)
			slices.SortStableFunc(s, func(a, b uint32) int { return cmp.Compare(key(b, axis), key(a, axis)) })
			return s
		}
		byX, byY := sorted(0), sorted(1)

		for viewName, view := range views {
			inverse, ok := view.Invert()
			if !ok {
				t.Fatal("view has no inverse")
			}
			ems := emPerPixel(inverse)
			ppe := Point{1 / ems.X, 1 / ems.Y}
			bounds := image.Rect(0, 0, 64, 110)
			p.eachPixel(l, view, inverse, bounds, func(x, y int, _ Point, got float32) {
				coord := inverse.Apply(Point{float32(x) + 0.5, float32(y) + 0.5})
				xcov, xwgt := p.rayCoverage(byX, coord, false, ppe.X)
				ycov, ywgt := p.rayCoverage(byY, coord, true, ppe.Y)
				want := combine(xcov, ycov, xwgt, ywgt, l.rule)
				if math.Abs(float64(got-want)) > 1e-4 {
					t.Errorf("%s %s pixel %d,%d: coverage %v, want %v", pathName, viewName, x, y, got, want)
				}
			})
		}
	}
}

func TestRasterSquareEdges(t *testing.T) {
	// The side edges are at the center of a column: only the ray along X sees
	// them. The other edges cover 3/4 of a row: only the ray along Y sees them.
	var path Path
	path.MoveTo(Point{8.5, 8.25})
	path.LineTo(Point{24.5, 8.25})
	path.LineTo(Point{24.5, 23.75})
	path.LineTo(Point{8.5, 23.75})
	path.Close()
	img := coverage(t, path, pixelView, 32)

	checkCover(t, img, 7, 16, 0)
	checkCover(t, img, 8, 16, 128)
	checkCover(t, img, 9, 16, 255)
	checkCover(t, img, 16, 16, 255)
	checkCover(t, img, 24, 16, 128)
	checkCover(t, img, 25, 16, 0)
	checkCover(t, img, 16, 7, 0)
	checkCover(t, img, 16, 8, 191)
	checkCover(t, img, 16, 9, 255)
	checkCover(t, img, 16, 23, 191)
	checkCover(t, img, 16, 24, 0)
}

// A control point at the middle of a quadratic gives a = 0: the solver must take
// the linear path.
func TestRasterLinearQuadratic(t *testing.T) {
	var path Path
	path.MoveTo(Point{8, 8})
	path.QuadTo(Point{16, 8}, Point{24, 8})
	path.QuadTo(Point{24, 16}, Point{24, 24})
	path.QuadTo(Point{16, 24}, Point{8, 24})
	path.QuadTo(Point{8, 16}, Point{8, 8})
	path.Close()
	checkArea(t, coverage(t, path, pixelView, 32), 16*16, 0.005)
}

// combine adds the two rays, so they must have the same sign. The min term of
// combine hides a wrong sign at most pixels.
func TestRasterRaysAgreeInSign(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		var p Pack
		mustAdd(t, &p, 1, Drawing{Em: rasterEm, Layers: []Layer{{Path: circlePath(32, 32, 24, 24, reverse)}}})
		l := p.layerAt(0)
		for y := range 64 {
			for x := range 64 {
				coord := Point{(float32(x) + 0.5) / rasterEm, (float32(y) + 0.5) / rasterEm}
				h := min(max(int(coord.Y*l.scale.Y+l.offset.Y), 0), l.nH-1)
				v := min(max(int(coord.X*l.scale.X+l.offset.X), 0), l.nV-1)
				xcov, _ := p.rayCoverage(p.bandList(l, h), coord, false, rasterEm)
				ycov, _ := p.rayCoverage(p.bandList(l, l.nH+v), coord, true, rasterEm)
				if xcov*ycov < 0 {
					t.Fatalf("pixel %d,%d: xcov %v and ycov %v have opposite signs", x, y, xcov, ycov)
				}
			}
		}
	}
}

// The value of fwidth in the shader: each axis adds the change along both pixel
// axes. A rotated view needs this to keep the width of the antialiased edge.
func TestEmPerPixel(t *testing.T) {
	got := emPerPixel(Affine{2, 3, -5, 7, 100, 200})
	if want := (Point{7, 10}); got != want {
		t.Errorf("emPerPixel = %v, want %v", got, want)
	}
}

// A curve that is nearly flat can give a NaN root. The HLSL saturate maps it to
// 0; a NaN must not reach the pixel.
func TestSatMapsNaNToZero(t *testing.T) {
	if got := sat(float32(math.NaN())); got != 0 {
		t.Errorf("sat(NaN) = %v, want 0", got)
	}
}

// Each axis has its own antialiasing width. A swap of the two widths makes the
// edge pixels wrong.
func TestRasterAnisotropicView(t *testing.T) {
	view := Affine{rasterEm * 4, 0, 0, rasterEm / 4, 0, 0}
	img := coverage(t, squarePath(2, 16, 4), view, 32)

	// The square covers X 8 to 24 and Y 4 to 5.
	checkCover(t, img, 7, 4, 0)
	checkCover(t, img, 8, 4, 255)
	checkCover(t, img, 23, 4, 255)
	checkCover(t, img, 24, 4, 0)
	checkCover(t, img, 16, 3, 0)
	checkCover(t, img, 16, 5, 0)
}

func TestRasterCircleArea(t *testing.T) {
	img := coverage(t, circlePath(32, 32, 24, 24, false), pixelView, 64)
	checkArea(t, img, math.Pi*24*24, 0.005)
}

func TestRasterHole(t *testing.T) {
	path := joined(circlePath(32, 32, 24, 24, false), circlePath(32, 32, 12, 24, true))
	img := coverage(t, path, pixelView, 64)

	checkCover(t, img, 32, 32, 0)
	checkCover(t, img, 32, 50, 255)
	checkArea(t, img, math.Pi*(24*24-12*12), 0.005)
}

func TestRasterFillRule(t *testing.T) {
	// Two nested squares with the same winding.
	path := joined(squarePath(4, 4, 24), squarePath(12, 12, 8))
	nonZero := coverage(t, path, pixelView, 32)
	path.Rule = EvenOdd
	evenOdd := coverage(t, path, pixelView, 32)

	checkCover(t, nonZero, 16, 16, 255)
	checkCover(t, evenOdd, 16, 16, 0)
	checkCover(t, nonZero, 8, 16, 255)
	checkCover(t, evenOdd, 8, 16, 255)
}

func TestRasterReversedContour(t *testing.T) {
	a := coverage(t, circlePath(32, 32, 24, 24, false), pixelView, 64)
	b := coverage(t, circlePath(32, 32, 24, 24, true), pixelView, 64)
	for y := range 64 {
		for x := range 64 {
			checkCover(t, b, x, y, a.AlphaAt(x, y).A)
		}
	}
}

func TestRasterRotatedView(t *testing.T) {
	sin, cos := float32(math.Sin(math.Pi/6)), float32(math.Cos(math.Pi/6))
	view := Affine{rasterEm * cos, rasterEm * sin, -rasterEm * sin, rasterEm * cos, 40, 8}
	img := coverage(t, squarePath(4, 4, 24), view, 64)
	checkArea(t, img, 24*24, 0.01)
}

func TestRasterizeLayers(t *testing.T) {
	var p Pack
	e := mustAdd(t, &p, 1, Drawing{Em: rasterEm, Layers: []Layer{
		{Path: squarePath(0, 0, 8)},
		{Path: squarePath(4, 0, 8), Paint: Paint{Kind: Solid, Color: [4]float32{0, 0, 1, 0.5}}},
	}})
	dst := image.NewRGBA(image.Rect(0, 0, 16, 8))
	if err := p.Rasterize(e, pixelView, [4]float32{1, 0, 0, 1}, dst); err != nil {
		t.Fatal(err)
	}
	check := func(x int, want [4]uint8) {
		t.Helper()
		c := dst.RGBAAt(x, 4)
		if got := [4]uint8{c.R, c.G, c.B, c.A}; got != want {
			t.Errorf("pixel x=%d = %v, want %v", x, got, want)
		}
	}
	check(2, [4]uint8{255, 0, 0, 255})   // the host color
	check(6, [4]uint8{128, 0, 128, 255}) // half blue over red
	check(10, [4]uint8{0, 0, 128, 128})  // premultiplied half blue
	check(14, [4]uint8{0, 0, 0, 0})
}

// Em space is Y-up and an image is Y-down: the usual view flips Y.
func TestRasterizeFlippedView(t *testing.T) {
	var p Pack
	e := mustAdd(t, &p, 1, Drawing{Em: rasterEm, Layers: []Layer{{Path: squarePath(0, 0, 4)}}})
	dst := image.NewRGBA(image.Rect(0, 0, 8, 8))
	view := Affine{rasterEm, 0, 0, -rasterEm, 0, 8}
	if err := p.Rasterize(e, view, [4]float32{1, 1, 1, 1}, dst); err != nil {
		t.Fatal(err)
	}
	if got := dst.RGBAAt(2, 6).A; got != 255 {
		t.Errorf("bottom row: alpha %d, want 255", got)
	}
	if got := dst.RGBAAt(2, 1).A; got != 0 {
		t.Errorf("top row: alpha %d, want 0", got)
	}
}

// A destination can start away from the origin, and a shape can leave it.
func TestRasterSubImage(t *testing.T) {
	path := circlePath(32, 32, 24, 24, false)
	full := coverage(t, path, pixelView, 64)

	var p Pack
	mustAdd(t, &p, 1, Drawing{Em: rasterEm, Layers: []Layer{{Path: path}}})
	area := image.Rect(20, 30, 50, 44)
	sub := image.NewAlpha(area)
	if err := p.Coverage(0, pixelView, sub); err != nil {
		t.Fatal(err)
	}
	for dy := range area.Dy() {
		for dx := range area.Dx() {
			x, y := area.Min.X+dx, area.Min.Y+dy
			if got, want := sub.AlphaAt(x, y).A, full.AlphaAt(x, y).A; got != want {
				t.Errorf("pixel %d,%d: coverage %d, want %d", x, y, got, want)
			}
		}
	}
}

func TestRasterRejectsBadArguments(t *testing.T) {
	var p Pack
	e := mustAdd(t, &p, 1, Drawing{Em: rasterEm, Layers: []Layer{{Path: squarePath(0, 0, 8)}}})
	rgba := image.NewRGBA(image.Rect(0, 0, 8, 8))
	alpha := image.NewAlpha(image.Rect(0, 0, 8, 8))
	color := [4]float32{1, 1, 1, 1}
	singular := Affine{1, 2, 2, 4, 0, 0}

	if err := p.Rasterize(e, singular, color, rgba); !errors.Is(err, ErrSingularView) {
		t.Errorf("Rasterize: err = %v, want ErrSingularView", err)
	}
	if err := p.Coverage(0, singular, alpha); !errors.Is(err, ErrSingularView) {
		t.Errorf("Coverage: err = %v, want ErrSingularView", err)
	}
	if err := p.Coverage(e.LayerCount, pixelView, alpha); err == nil {
		t.Errorf("Coverage outside the pack: err = nil")
	}
	e.LayerCount++
	if err := p.Rasterize(e, pixelView, color, rgba); err == nil {
		t.Errorf("Rasterize outside the pack: err = nil")
	}
}

var (
	black = [4]float32{0, 0, 0, 1}
	white = [4]float32{1, 1, 1, 1}
	ramp  = []Stop{{0, black}, {1, white}}
)

// gradientImage draws a square of 32 pixels with the paint.
func gradientImage(t *testing.T, paint Paint) *image.RGBA {
	t.Helper()
	var p Pack
	e := mustAdd(t, &p, 1, Drawing{Em: rasterEm, Layers: []Layer{{Path: squarePath(0, 0, 32), Paint: paint}}})
	dst := image.NewRGBA(image.Rect(0, 0, 32, 32))
	if err := p.Rasterize(e, pixelView, [4]float32{1, 0, 0, 1}, dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

func withSpace(p Paint, s ColorSpace) Paint {
	g := *p.Gradient
	g.Space = s
	p.Gradient = &g
	return p
}

func withSpread(p Paint, s Spread) Paint {
	p.Gradient.Spread = s
	return p
}

func TestGradientValues(t *testing.T) {
	line := LinearPaint(Point{8, 0}, Point{24, 0}, ramp...)
	short := func() Paint { return LinearPaint(Point{0, 0}, Point{8, 0}, ramp...) }
	cases := []struct {
		name  string
		paint Paint
		x, y  int
		want  float64 // the gray value of the pixel, from 0 to 1
	}{
		{"linear", line, 12, 3, 4.5 / 16},
		{"pad before", line, 2, 3, 0},
		{"pad after", line, 30, 3, 1},
		{"repeat", withSpread(short(), Repeat), 12, 3, 4.5 / 8},
		{"reflect", withSpread(short(), Reflect), 12, 3, 3.5 / 8},
		{"radial", RadialPaint(Point{16, 16}, 0, Point{16, 16}, 16, ramp...), 24, 16, 8.53 / 16},
		{"sweep half", SweepPaint(Point{16, 16}, 0, 2*math.Pi, ramp...), 4, 16, 0.5},
		{"sweep from below", SweepPaint(Point{16, 16}, -math.Pi/2, 3*math.Pi/2, ramp...), 4, 16, 0.75},
		{"sweep backward", SweepPaint(Point{16, 16}, 2*math.Pi, 0, ramp...), 16, 28, 0.75},
		{"hard stop low", LinearPaint(Point{0, 0}, Point{32, 0}, Stop{0.5, black}, Stop{0.5, white}), 15, 3, 0},
		{"hard stop high", LinearPaint(Point{0, 0}, Point{32, 0}, Stop{0.5, black}, Stop{0.5, white}), 16, 3, 1},
		{"hard stop inside", LinearPaint(Point{0, 0}, Point{33, 0}, Stop{0.5, black}, Stop{0.5, white}), 16, 3, 0.5},
		{"repeat seam", withSpread(LinearPaint(Point{0, 0}, Point{8.5, 0}, ramp...), Repeat), 8, 3, 0.5},
		{"sweep seam", SweepPaint(Point{16, 16.5}, 0, 2*math.Pi, ramp...), 24, 16, 0.5},
		{"stripes under a pixel", withSpread(LinearPaint(Point{0, 0}, Point{0.25, 0}, ramp...), Repeat), 9, 3, 0.5},
		{"reflect stripes under a pixel", withSpread(LinearPaint(Point{0, 0}, Point{0.25, 0}, ramp...), Reflect), 9, 3, 0.5},
		{"linear light", withSpace(line, LinearSRGB), 12, 3, 0.567}, // sRGB of 4.5/16 in linear light
		{"no direction", LinearPaint(Point{8, 8}, Point{8, 8}, ramp...), 3, 3, 1},
		{"one stop", RadialPaint(Point{8, 8}, 0, Point{8, 8}, 8, Stop{0, white}), 3, 3, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := float64(gradientImage(t, c.paint).RGBAAt(c.x, c.y).R) / 255
			if math.Abs(got-c.want) > 0.02 {
				t.Errorf("gray = %.3f, want %.3f", got, c.want)
			}
		})
	}
}

// The definition of two circles is the oracle: the pixel is on the circle of its t.
func TestRadialGradientOnItsCircle(t *testing.T) {
	c0, c1 := Point{6, 16}, Point{22, 16}
	const r0, r1 = 1, 9
	img := gradientImage(t, RadialPaint(c0, r0, c1, r1, ramp...))
	transparent, painted := 0, 0
	for y := range 32 {
		for x := range 32 {
			c := img.RGBAAt(x, y)
			if c.A == 0 {
				transparent++
				continue
			}
			if c.A != 255 || c.R < 26 || c.R > 229 {
				continue // the filter mixes a border with transparent, and a value near a pad with the pad
			}
			painted++
			v := float64(c.R) / 255
			cx := float64(c0.X) + v*float64(c1.X-c0.X)
			dist := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-16)
			if r := r0 + v*(r1-r0); math.Abs(dist-r) > 0.1 {
				t.Fatalf("pixel %d,%d has t %.3f: distance %.2f, radius %.2f", x, y, v, dist, r)
			}
		}
	}
	if transparent == 0 || painted == 0 {
		t.Errorf("%d pixels are outside the cone and %d pixels are inside", transparent, painted)
	}
}

func TestGradientAlphaMix(t *testing.T) {
	red, clearBlue := [4]float32{1, 0, 0, 1}, [4]float32{0, 0, 1, 0}
	straight := LinearPaint(Point{0, 0}, Point{32, 0}, Stop{0, red}, Stop{1, clearBlue})
	premultiplied := LinearPaint(Point{0, 0}, Point{32, 0}, Stop{0, red}, Stop{1, clearBlue})
	premultiplied.Gradient.Premultiplied = true

	// The pixel at 15 is nearly half of the way. dst is premultiplied.
	a := gradientImage(t, straight).RGBAAt(15, 3)
	if a.B < 55 {
		t.Errorf("the straight mix has no blue: %v", a)
	}
	b := gradientImage(t, premultiplied).RGBAAt(15, 3)
	if b.B != 0 || b.R != b.A {
		t.Errorf("the premultiplied mix gives color to a transparent stop: %v", b)
	}
}

// The gradient is in the space of the path, so both transforms move it with the shape.
func TestGradientFollowsTransforms(t *testing.T) {
	scale := Affine{2, 0, 0, 2, 0, 0}
	shift := Affine{1, 0, 0, 1, 8, 0}
	var p Pack
	path := squarePath(0, 0, 16)
	e := mustAdd(t, &p, 1, Drawing{Em: rasterEm, Layers: []Layer{{
		Path: path, Transform: &scale, Paint: LinearPaint(Point{0, 0}, Point{16, 0}, ramp...),
	}}})
	moved := LinearPaint(Point{0, 0}, Point{8, 0}, ramp...)
	moved.Gradient.Transform = &shift
	f := mustAdd(t, &p, 2, Drawing{Em: rasterEm, Layers: []Layer{{Path: path, Paint: moved}}})
	cases := []struct {
		e    Entry
		x    int
		want float64
	}{{e, 24, 24.5 / 32}, {f, 12, 4.5 / 8}, {f, 4, 0}}
	for _, c := range cases {
		dst := image.NewRGBA(image.Rect(0, 0, 32, 32))
		if err := p.Rasterize(c.e, pixelView, white, dst); err != nil {
			t.Fatal(err)
		}
		if got := float64(dst.RGBAAt(c.x, 3).R) / 255; math.Abs(got-c.want) > 0.02 {
			t.Errorf("entry %v at x=%d: gray %.3f, want %.3f", c.e.Key, c.x, got, c.want)
		}
	}
}

func TestStrokeKeepsGradient(t *testing.T) {
	d, err := Drawing{Em: rasterEm, Layers: []Layer{{
		Path: squarePath(8, 8, 16), Paint: LinearPaint(Point{0, 0}, Point{32, 0}, ramp...),
	}}}.Stroke(Stroke{Width: 4})
	if err != nil {
		t.Fatal(err)
	}
	var p Pack
	e := mustAdd(t, &p, 1, d)
	dst := image.NewRGBA(image.Rect(0, 0, 32, 32))
	if err := p.Rasterize(e, pixelView, [4]float32{1, 0, 0, 1}, dst); err != nil {
		t.Fatal(err)
	}
	left, right := dst.RGBAAt(8, 16), dst.RGBAAt(24, 16)
	if left.A != 255 || right.A != 255 || left.R >= right.R || left.G != left.R {
		t.Errorf("the line has %v at the left and %v at the right", left, right)
	}
}

func TestInvalidGradients(t *testing.T) {
	singular := Affine{}
	flat := LinearPaint(Point{0, 0}, Point{1, 0}, ramp...)
	flat.Gradient.Transform = &singular
	cases := map[string]Paint{
		"no gradient":  {Kind: LinearGradient},
		"unknown kind": {Kind: SweepGradient + 1, Gradient: &Gradient{Stops: ramp}},
		"no stops":     LinearPaint(Point{0, 0}, Point{1, 0}),
		"falling":      LinearPaint(Point{0, 0}, Point{1, 0}, Stop{1, black}, Stop{0, white}),
		"negative":     RadialPaint(Point{0, 0}, -1, Point{1, 0}, 1, ramp...),
		"singular":     flat,
		"spread":       withSpread(LinearPaint(Point{0, 0}, Point{1, 0}, ramp...), Reflect+1),
	}
	for name, paint := range cases {
		var p Pack
		_, err := p.Add(Key{ID: 1}, Drawing{Em: rasterEm, Layers: []Layer{{Path: squarePath(0, 0, 8), Paint: paint}}})
		if !errors.Is(err, ErrInvalidPaint) {
			t.Errorf("%s: err = %v, want ErrInvalidPaint", name, err)
		}
		if p.Size() != 0 {
			t.Errorf("%s: the pack has %d bytes", name, p.Size())
		}
	}
}

// The edge of a circle has each direction. A test of two samples misses some of them.
func TestGradientEdgeIsSmoothInEachDirection(t *testing.T) {
	const radius = 10
	img := gradientImage(t, RadialPaint(Point{16, 16}, 0, Point{16, 16}, radius*2, Stop{0.5, black}, Stop{0.5, white}))
	checked := 0
	for y := range 32 {
		for x := range 32 {
			dist := math.Hypot(float64(x)+0.5-16, float64(y)+0.5-16)
			if math.Abs(dist-radius) > 0.1 {
				continue
			}
			checked++
			if v := img.RGBAAt(x, y).R; v == 0 || v == 255 {
				t.Errorf("pixel %d,%d on the edge has gray %d", x, y, v)
			}
		}
	}
	if checked < 8 {
		t.Errorf("the edge has %d pixels", checked)
	}
}

// The border of a cone is an edge to transparent, so it needs a partial alpha.
func TestRadialConeBorderIsSmooth(t *testing.T) {
	img := gradientImage(t, RadialPaint(Point{4, 16}, 0, Point{20, 16}, 8, ramp...))
	partial := 0
	for y := range 32 {
		for x := range 32 {
			if a := img.RGBAAt(x, y).A; a > 0 && a < 255 {
				partial++
			}
		}
	}
	if partial == 0 {
		t.Error("the border of the cone has no partial alpha")
	}
}
