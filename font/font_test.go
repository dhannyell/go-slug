package font

import (
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

func TestGlyphSpace(t *testing.T) {
	g := glyph(t, ' ')
	if len(g.Drawing.Layers) != 0 || g.Advance <= 0 {
		t.Errorf("layers = %d, Advance = %v; want no layers and an advance",
			len(g.Drawing.Layers), g.Advance)
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
