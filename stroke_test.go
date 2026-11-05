package slug

import (
	"errors"
	"image"
	"math"
	"testing"
)

// strokeSamples returns points along one contour of p, closely enough to measure a
// distance. A fill closes an open contour, but a stroke does not.
func strokeSamples(p Path, open bool) []Point {
	curves, err := flattenPath(p, Identity(), 0.01, nil)
	if err != nil {
		panic(err)
	}
	if open {
		curves = curves[:len(curves)-1]
	}
	var out []Point
	for _, c := range curves {
		for i := range 65 {
			out = append(out, pointAt(c, float32(i)/64))
		}
	}
	return out
}

// strokeCoverage returns the alpha of each pixel of a filled path.
func strokeCoverage(t *testing.T, p Path, size int) *image.RGBA {
	t.Helper()
	var pack Pack
	e, err := pack.Add(Key{ID: 1}, Drawing{Layers: []Layer{{Path: p}}, Em: rasterEm})
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	if err := pack.Rasterize(e, pixelView, [4]float32{1, 1, 1, 1}, img); err != nil {
		t.Fatal(err)
	}
	return img
}

func closedSquare(lo, hi float32) Path {
	var p Path
	p.MoveTo(Point{lo, lo})
	p.LineTo(Point{hi, lo})
	p.LineTo(Point{hi, hi})
	p.LineTo(Point{lo, hi})
	p.Close()
	return p
}

// A round stroke is all the points near the path, so distance gives the truth at each
// pixel. A loop of the wrong winding shows here as a hole, and it shows only on a
// curve tighter than the width.
func TestRoundStrokeIsTheAreaNearThePath(t *testing.T) {
	var open, tight, zigzag, overlap, reversal Path
	open.MoveTo(Point{10, 10})
	open.CubeTo(Point{60, 110}, Point{70, -40}, Point{118, 90})

	tight.MoveTo(Point{20, 40})
	tight.QuadTo(Point{110, 44}, Point{30, 48})
	tight.LineTo(Point{30, 100})

	zigzag.MoveTo(Point{10, 20})
	zigzag.LineTo(Point{110, 30})
	zigzag.LineTo(Point{15, 45})
	zigzag.LineTo(Point{112, 60})
	zigzag.LineTo(Point{20, 80})
	zigzag.Close()

	overlap.MoveTo(Point{20, 20})
	overlap.CubeTo(Point{140, 120}, Point{-20, 120}, Point{100, 20})

	reversal.MoveTo(Point{20, 60})
	reversal.LineTo(Point{100, 60})
	reversal.LineTo(Point{40, 60})

	for name, c := range map[string]struct {
		path  Path
		width float32
		open  bool
	}{
		"open":     {open, 12, true},
		"circle":   {circlePath(64, 64, 40, 8, false), 10, false},
		"wide":     {circlePath(64, 64, 20, 8, false), 30, false},
		"reverse":  {circlePath(64, 64, 30, 8, true), 16, false},
		"dot":      {circlePath(64, 64, 8, 8, false), 40, false},
		"dot CW":   {circlePath(64, 64, 8, 8, true), 40, false},
		"tight":    {tight, 24, true},
		"zigzag":   {zigzag, 9, false},
		"overlap":  {overlap, 14, true},
		"reversal": {reversal, 20, true},
	} {
		t.Run(name, func(t *testing.T) {
			ring, err := c.path.Stroke(Stroke{Width: c.width, Cap: RoundCap, Join: RoundJoin})
			if err != nil {
				t.Fatal(err)
			}
			img := strokeCoverage(t, ring, 128)
			samples := strokeSamples(c.path, c.open)
			half := float64(c.width) / 2
			wrong := 0
			for y := range 128 {
				for x := range 128 {
					dist := math.Inf(1)
					for _, s := range samples {
						dist = min(dist, math.Hypot(float64(s.X)-float64(x)-0.5, float64(s.Y)-float64(y)-0.5))
					}
					alpha := img.Pix[img.PixOffset(x, y)+3]
					// One pixel of margin covers the edge, where coverage is partial.
					if (dist < half-1 && alpha != 255) || (dist > half+1 && alpha != 0) {
						wrong++
						if wrong <= 3 {
							t.Errorf("pixel %d,%d at distance %.2f has alpha %d", x, y, dist, alpha)
						}
					}
				}
			}
			if wrong > 3 {
				t.Errorf("%d wrong pixels", wrong)
			}
		})
	}
}

func TestStrokeCapsAndJoins(t *testing.T) {
	var line Path
	line.MoveTo(Point{40, 64})
	line.LineTo(Point{88, 64})
	for name, c := range map[string]struct {
		cap  Cap
		want Point // a point past the end of the line
		ink  bool
	}{
		"butt":   {ButtCap, Point{93, 64}, false},
		"square": {SquareCap, Point{93, 69}, true},
		"round":  {RoundCap, Point{93, 64}, true},
		"round2": {RoundCap, Point{93, 71}, false},
	} {
		t.Run(name, func(t *testing.T) {
			ring, err := line.Stroke(Stroke{Width: 16, Cap: c.cap})
			if err != nil {
				t.Fatal(err)
			}
			img := strokeCoverage(t, ring, 128)
			if got := img.Pix[img.PixOffset(int(c.want.X), int(c.want.Y))+3] != 0; got != c.ink {
				t.Errorf("ink at %v is %v", c.want, got)
			}
		})
	}

	corner := Point{94, 94} // near the outer corner of a square from 40 to 88, width 16
	for name, c := range map[string]struct {
		style Stroke
		ink   bool
	}{
		"miter":       {Stroke{Width: 16}, true},
		"over-limit":  {Stroke{Width: 16, MiterLimit: 1.1}, false},
		"bevel":       {Stroke{Width: 16, Join: BevelJoin}, false},
		"round joins": {Stroke{Width: 16, Join: RoundJoin}, false},
	} {
		t.Run(name, func(t *testing.T) {
			ring, err := closedSquare(40, 88).Stroke(c.style)
			if err != nil {
				t.Fatal(err)
			}
			img := strokeCoverage(t, ring, 128)
			if got := img.Pix[img.PixOffset(int(corner.X), int(corner.Y))+3] != 0; got != c.ink {
				t.Errorf("ink at the corner is %v", got)
			}
		})
	}
}

// A contour must not cancel its fill, in the two orientations that fonts use. The
// hole must get smaller, and must stay.
func TestEmboldenKeepsHoles(t *testing.T) {
	for _, outerCW := range []bool{false, true} {
		var p Path
		add := func(c Path) {
			p.Verbs = append(p.Verbs, c.Verbs...)
			p.Points = append(p.Points, c.Points...)
		}
		add(circlePath(64, 64, 50, 8, outerCW))
		add(circlePath(64, 64, 20, 8, !outerCW))

		bold, err := p.Embolden(6)
		if err != nil {
			t.Fatal(err)
		}
		img := strokeCoverage(t, bold, 128)
		for _, c := range []struct {
			name string
			x    int
			want bool
		}{
			{"the hole", 64, false},
			{"the new edge of the hole", 64 + 17, true},
			{"the old body", 64 + 35, true},
			{"the old edge", 64 + 50, true},
			{"the new edge", 64 + 54, true},
			{"the outside", 64 + 59, false},
		} {
			if got := img.Pix[img.PixOffset(c.x, 64)+3] == 255; got != c.want && (c.want || img.Pix[img.PixOffset(c.x, 64)+3] != 0) {
				t.Errorf("clockwise %v: %s has alpha %d", outerCW, c.name, img.Pix[img.PixOffset(c.x, 64)+3])
			}
		}
	}
}

func TestStrokeRejectsBadInput(t *testing.T) {
	p := closedSquare(0, 10)
	for name, err := range map[string]func() error{
		"zero width": func() error { _, err := p.Stroke(Stroke{}); return err },
		"NaN width": func() error {
			_, err := p.Stroke(Stroke{Width: float32(math.NaN())})
			return err
		},
		"bad path": func() error {
			_, err := Path{Verbs: []Verb{LineTo}, Points: []Point{{}}}.Stroke(Stroke{Width: 1})
			return err
		},
		"even-odd": func() error {
			_, err := Path{Rule: EvenOdd}.Embolden(1)
			return err
		},
	} {
		if !errors.Is(err(), ErrInvalidPath) {
			t.Errorf("%s: got %v", name, err())
		}
	}
}
