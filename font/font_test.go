package font

import (
	"image"
	"testing"

	slug "github.com/dhannyell/go-slug"
	"golang.org/x/image/font/gofont/goregular"
)

func TestGlyphO(t *testing.T) {
	g := glyph(t, 'o')

	// Y-up: the glyph sits on the baseline and rises above it.
	if g.Bounds.Max.Y <= 0 || g.Bounds.Min.Y > 0 || g.Bounds.Max.Y < -g.Bounds.Min.Y {
		t.Errorf("Bounds = %v, want Y-up above the baseline", g.Bounds)
	}
	if g.Advance <= 0 {
		t.Errorf("Advance = %v, want > 0", g.Advance)
	}

	// 'o' has two contours with opposite winding: the hole cancels the fill.
	areas := contourAreas(g.Drawing.Layers[0].Path)
	if len(areas) != 2 || areas[0]*areas[1] >= 0 {
		t.Errorf("contour areas = %v, want two with opposite signs", areas)
	}
}

// A real glyph must render: the middle row crosses the two sides of the 'o', and
// the center is the hole.
func TestGlyphORenders(t *testing.T) {
	g := glyph(t, 'o')
	var p slug.Pack
	e, err := p.Add(slug.Key{ID: 'o'}, g.Drawing)
	if err != nil {
		t.Fatal(err)
	}

	const pixels = 64 // size of the em
	b := e.Bounds
	view := slug.Affine{pixels, 0, 0, -pixels, -b.Min.X * pixels, b.Max.Y * pixels}
	dst := image.NewAlpha(image.Rect(0, 0, int((b.Max.X-b.Min.X)*pixels)+1, int((b.Max.Y-b.Min.Y)*pixels)+1))
	if err := p.Coverage(e.FirstLayer, view, dst); err != nil {
		t.Fatal(err)
	}

	size, row := dst.Bounds().Size(), dst.Bounds().Dy()/2
	runs, full := 0, false
	for x := range size.X {
		v := dst.AlphaAt(x, row).A
		if v >= 128 && (x == 0 || dst.AlphaAt(x-1, row).A < 128) {
			runs++
		}
		full = full || v == 255
	}
	if runs != 2 || !full {
		t.Errorf("middle row: %d filled runs, full pixel %v; want 2 runs with a full pixel", runs, full)
	}
	if got := dst.AlphaAt(size.X/2, row).A; got != 0 {
		t.Errorf("center: coverage %d, want 0", got)
	}
}

func TestGlyphSpace(t *testing.T) {
	g := glyph(t, ' ')
	if len(g.Drawing.Layers) != 0 || g.Advance <= 0 {
		t.Errorf("layers = %d, Advance = %v; want no layers and an advance",
			len(g.Drawing.Layers), g.Advance)
	}
}

// A conversion to the 16 bits of a font must not select another glyph.
func TestGlyphRejectsLargeID(t *testing.T) {
	f, err := Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Glyph(1 << 16); err == nil {
		t.Error("glyph 65536 has no error")
	}
}

// Real outlines must satisfy the Path contract and fit in a Pack.
func TestGlyphsEnterPack(t *testing.T) {
	var p slug.Pack
	for r := rune(' '); r <= '~'; r++ {
		g := glyph(t, r)
		e, err := p.Add(slug.Key{ID: uint32(r)}, g.Drawing)
		if err != nil {
			t.Fatalf("Add(%q): %v", r, err)
		}
		if want := uint32(len(g.Drawing.Layers)); e.LayerCount != want {
			t.Errorf("Add(%q): LayerCount = %d, want %d", r, e.LayerCount, want)
		}
	}
}

func glyph(t *testing.T, r rune) Glyph {
	t.Helper()
	f, err := Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	i, err := f.GlyphIndex(r)
	if err != nil || i == 0 {
		t.Fatalf("GlyphIndex(%q) = %d, %v", r, i, err)
	}
	g, err := f.Glyph(i)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// contourAreas returns the signed shoelace area of each contour, with the
// control points as polygon vertices. The sign gives the winding.
func contourAreas(p slug.Path) []float32 {
	var areas []float32
	var poly []slug.Point
	flush := func() {
		var a float32
		for i := range poly {
			q, r := poly[i], poly[(i+1)%len(poly)]
			a += q.X*r.Y - r.X*q.Y
		}
		if len(poly) > 0 {
			areas = append(areas, a/2)
		}
		poly = poly[:0]
	}
	i := 0
	for _, v := range p.Verbs {
		if v == slug.MoveTo {
			flush()
		}
		poly = append(poly, p.Points[i:i+v.PointCount()]...)
		i += v.PointCount()
	}
	flush()
	return areas
}
