package slug

import (
	"errors"
	"math"
	"slices"
	"testing"
)

const testEm = 256

// ringPath is a closed contour of quadratics.
func ringPath(cx, cy, r float64, count int) Path {
	at := func(i, radius float64) Point {
		a := i * 2 * math.Pi / float64(count)
		return Point{float32(cx + radius*math.Cos(a)), float32(cy + radius*math.Sin(a))}
	}
	var p Path
	p.MoveTo(at(0, r))
	for i := range count {
		end := at(float64(i+1), r)
		if i == count-1 {
			end = p.Points[0]
		}
		p.QuadTo(at(float64(i)+0.5, r*1.02), end)
	}
	p.Close()
	return p
}

func squarePath(x, y, size float32) Path {
	var p Path
	p.MoveTo(Point{x, y})
	p.LineTo(Point{x + size, y})
	p.LineTo(Point{x + size, y + size})
	p.LineTo(Point{x, y + size})
	p.Close()
	return p
}

// combPath has teeth that span the full height: many vertical bands, one
// horizontal band.
func combPath(teeth int) Path {
	var p Path
	p.MoveTo(Point{0, 0})
	for i := range teeth {
		p.LineTo(Point{float32(2*i + 1), 100})
		p.LineTo(Point{float32(2*i + 2), 0})
	}
	p.Close()
	return p
}

func mustAdd(t *testing.T, p *Pack, id uint32, d Drawing) Entry {
	t.Helper()
	e, err := p.Add(Key{ID: id}, d)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// A full Pack must keep no part of the drawing: a later Add would write its layers
// after the remains, and the upload ranges would be wrong.
func TestAddToAFullPackChangesNothing(t *testing.T) {
	first := Drawing{Em: testEm, Layers: []Layer{{Path: squarePath(0, 0, 100)}}}
	second := Drawing{Em: testEm, Layers: []Layer{{Path: ringPath(128, 128, 90, 8)}, {Path: squarePath(0, 0, 50)}}}

	var want Pack
	mustAdd(t, &want, 1, first)
	mustAdd(t, &want, 2, second)

	var p Pack
	mustAdd(t, &p, 1, first)
	p.MaxBytes = p.Size()
	size := p.Size()
	if _, err := p.Add(Key{ID: 2}, second); !errors.Is(err, ErrPackFull) {
		t.Fatalf("got error %v, want ErrPackFull", err)
	}
	if p.Size() != size {
		t.Errorf("size is %d after the failure, want %d", p.Size(), size)
	}
	if _, ok := p.Lookup(Key{ID: 2}); ok || len(p.Entries()) != 1 {
		t.Errorf("the failed drawing has an entry")
	}

	p.MaxBytes = 0
	mustAdd(t, &p, 2, second)
	if !slices.Equal(p.Curves(), want.Curves()) || !slices.Equal(p.Bands(), want.Bands()) || !slices.Equal(p.Layers(), want.Layers()) {
		t.Errorf("the failed Add left data in the pack")
	}
}

// After Reset the host writes from index 0. The data must equal a new Pack, or the
// old buffers would keep wrong addresses.
func TestResetStartsANewGeneration(t *testing.T) {
	d := Drawing{Em: testEm, Layers: []Layer{{Path: ringPath(128, 128, 90, 8)}}}

	var p Pack
	mustAdd(t, &p, 1, Drawing{Em: testEm, Layers: []Layer{{Path: squarePath(0, 0, 100)}}})
	mustAdd(t, &p, 2, d)
	p.TakeDirty()

	p.Reset()
	if p.Generation() != 1 {
		t.Errorf("generation is %d, want 1", p.Generation())
	}
	if p.Size() != 0 || len(p.Entries()) != 0 {
		t.Errorf("size %d and %d entries after Reset", p.Size(), len(p.Entries()))
	}
	if _, ok := p.Lookup(Key{ID: 1}); ok {
		t.Errorf("an old key is still in the pack")
	}

	got := mustAdd(t, &p, 2, d)
	var fresh Pack
	want := mustAdd(t, &fresh, 2, d)
	if got != want {
		t.Errorf("got entry %+v, want %+v", got, want)
	}
	if !slices.Equal(p.Curves(), fresh.Curves()) || !slices.Equal(p.Bands(), fresh.Bands()) || !slices.Equal(p.Layers(), fresh.Layers()) {
		t.Errorf("the data differs from a new Pack")
	}
	if dirty := p.TakeDirty(); dirty != fresh.TakeDirty() {
		t.Errorf("got dirty ranges %+v after Reset", dirty)
	}
}

// emShapes returns the shapes of d in the space of the pack data.
func emShapes(t *testing.T, d Drawing) []shape {
	t.Helper()
	shapes := mustNormalize(t, d)
	for i := range shapes {
		if err := toEm(&shapes[i], d.Em); err != nil {
			t.Fatal(err)
		}
	}
	return shapes
}

// layerView reads a layer record the way the shader reads it.
type layerView struct {
	p   *Pack
	rec []uint32
}

func viewLayer(p *Pack, i uint32) layerView {
	return layerView{p, p.Layers()[i*LayerWords:][:LayerWords]}
}

func (v layerView) float(i int) float32 { return math.Float32frombits(v.rec[i]) }

// bandCurves walks one band: header, list, then curve texels.
func (v layerView) bandCurves(vertical bool, coord float32) []curve {
	nH, nV := int(v.rec[1]&0xFFFF), int(v.rec[1]>>16)
	var idx int
	if vertical {
		idx = min(max(int(coord*v.float(4)+v.float(6)), 0), nV-1) + nH
	} else {
		idx = min(max(int(coord*v.float(5)+v.float(7)), 0), nH-1)
	}
	base := v.rec[0]
	count, offset := v.p.Bands()[base+uint32(2*idx)], v.p.Bands()[base+uint32(2*idx)+1]

	var out []curve
	for _, loc := range v.p.Bands()[base+offset:][:count] {
		c := v.p.Curves()[loc*4:][:8]
		out = append(out, curve{Point{c[0], c[1]}, Point{c[2], c[3]}, Point{c[4], c[5]}})
	}
	return out
}

func bandKey(c curve, vertical bool) float32 {
	if vertical {
		return max(c.P0.Y, c.P1.Y, c.P2.Y)
	}
	return max(c.P0.X, c.P1.X, c.P2.X)
}

// The data contract with the shader: from a layer record and a coordinate, the
// walk must reach every curve at that coordinate, with its exact points, in
// descending key order. Sampling uses the float32 math of the shader.
func TestPackDataMatchesShaderWalk(t *testing.T) {
	move := Affine{1, 0, 0, 1, 40, 0}
	d := Drawing{Em: testEm, Layers: []Layer{
		{Path: ringPath(128, 128, 100, 24)},
		{Path: squarePath(10, 20, 30), Transform: &move},
		{Path: combPath(12)},
	}}

	var p Pack
	// Data before d: the addresses must not start at zero.
	mustAdd(t, &p, 1, Drawing{Em: testEm, Layers: []Layer{{Path: squarePath(0, 0, 8)}}})
	e := mustAdd(t, &p, 2, d)
	shapes := emShapes(t, d)

	want := union(union(shapes[0].Bounds, shapes[1].Bounds), shapes[2].Bounds)
	if e.Bounds != want {
		t.Errorf("Entry.Bounds = %v, want %v", e.Bounds, want)
	}
	// Without this, a swap of the two axes stays hidden.
	if comb := viewLayer(&p, e.FirstLayer+2); comb.rec[1]&0xFFFF == comb.rec[1]>>16 {
		t.Fatalf("comb has the same band count on both axes: %#x", comb.rec[1])
	}

	for li, s := range shapes {
		v := viewLayer(&p, e.FirstLayer+uint32(li))
		for _, vertical := range []bool{false, true} {
			lo, hi := s.Bounds.Min.Y, s.Bounds.Max.Y
			if vertical {
				lo, hi = s.Bounds.Min.X, s.Bounds.Max.X
			}
			for i := range 2001 {
				coord := lo + (hi-lo)*float32(i)/2000
				list := v.bandCurves(vertical, coord)

				for _, c := range s.Curves {
					b := c.bounds()
					eLo, eHi := b.Min.Y, b.Max.Y
					if vertical {
						eLo, eHi = b.Min.X, b.Max.X
					}
					if eLo < eHi && eLo <= coord && coord <= eHi && !slices.Contains(list, c) {
						t.Fatalf("layer %d vertical=%v coord %v: curve %v is not in the band", li, vertical, coord, c)
					}
				}
				for j := 1; j < len(list); j++ {
					if bandKey(list[j-1], vertical) < bandKey(list[j], vertical) {
						t.Fatalf("layer %d vertical=%v coord %v: list is not descending", li, vertical, coord)
					}
				}
			}
		}
	}
}

// The band overlap protects the shader from float errors at band edges: a
// coordinate within 1/1024 em of a curve still reaches the curve.
func TestPackBandOverlap(t *testing.T) {
	const overlap = 1.0 / 1024

	// This ring is small in the em: the overlap is wide against its bands.
	d := Drawing{Em: 4096, Layers: []Layer{{Path: ringPath(128, 128, 100, 24)}}}
	var p Pack
	mustAdd(t, &p, 1, d)
	v := viewLayer(&p, 0)
	s := emShapes(t, d)[0]

	for _, c := range s.Curves {
		b := c.bounds()
		for _, coord := range []float32{b.Min.Y - overlap/2, b.Max.Y + overlap/2} {
			if b.Min.Y < b.Max.Y && !slices.Contains(v.bandCurves(false, coord), c) {
				t.Errorf("Y %v: curve %v is not in the band", coord, c)
			}
		}
		for _, coord := range []float32{b.Min.X - overlap/2, b.Max.X + overlap/2} {
			if b.Min.X < b.Max.X && !slices.Contains(v.bandCurves(true, coord), c) {
				t.Errorf("X %v: curve %v is not in the band", coord, c)
			}
		}
	}
}

func TestPackStoresEmSpace(t *testing.T) {
	var p Pack
	e := mustAdd(t, &p, 1, Drawing{Em: testEm, Layers: []Layer{{Path: squarePath(64, 128, 32)}}})

	if got := p.Curves()[:2]; got[0] != 0.25 || got[1] != 0.5 {
		t.Errorf("first point = %v, want 0.25, 0.5", got)
	}
	if want := (Rect{Point{0.25, 0.5}, Point{0.375, 0.625}}); e.Bounds != want {
		t.Errorf("Entry.Bounds = %v, want %v", e.Bounds, want)
	}
}

// A font with another units per em must give the same data for the same outline.
// The scale is a power of two, so the float math stays exact.
func TestPackDataDoesNotDependOnEm(t *testing.T) {
	data := func(size, em float32) *Pack {
		var path Path
		path.MoveTo(Point{0, 0})
		path.CubeTo(Point{size * 1.2, size * 1.6}, Point{-size / 5, size * 1.6}, Point{size, 0})
		var p Pack
		mustAdd(t, &p, 1, Drawing{Em: em, Layers: []Layer{{Path: path}}})
		return &p
	}
	small, large := data(100, 256), data(1600, 4096)
	if !slices.Equal(small.Curves(), large.Curves()) || !slices.Equal(small.Bands(), large.Bands()) ||
		!slices.Equal(small.Layers(), large.Layers()) {
		t.Errorf("data at Em 256 differs from data at Em 4096")
	}
}

func TestPackLayerRecordFields(t *testing.T) {
	color := [4]float32{0.25, 0.5, 0.75, 1}

	path := squarePath(1, 2, 3)
	var p Pack
	mustAdd(t, &p, 1, Drawing{Em: testEm, Layers: []Layer{{Path: path, Paint: Paint{Kind: Solid, Color: color}}}})
	v := viewLayer(&p, 0)

	if want := uint32(NonZero) | uint32(Solid)<<8; v.rec[2] != want {
		t.Errorf("flags = %#x, want %#x", v.rec[2], want)
	}
	path.Rule = EvenOdd
	mustAdd(t, &p, 2, Drawing{Em: testEm, Layers: []Layer{{Path: path}}})
	if got, want := viewLayer(&p, 1).rec[2], uint32(EvenOdd)|uint32(CurrentColor)<<8; got != want {
		t.Errorf("flags = %#x, want %#x", got, want)
	}
	bounds := [4]float32{v.float(8), v.float(9), v.float(10), v.float(11)}
	if want := [4]float32{1.0 / testEm, 2.0 / testEm, 4.0 / testEm, 5.0 / testEm}; bounds != want {
		t.Errorf("bounds = %v, want %v", bounds, want)
	}
	if got := [4]float32{v.float(12), v.float(13), v.float(14), v.float(15)}; got != color {
		t.Errorf("color = %v, want %v", got, color)
	}
	transform := [6]float32{v.float(16), v.float(17), v.float(18), v.float(19), v.float(20), v.float(21)}
	if want := [6]float32{1, 0, 0, 1, 0, 0}; transform != want {
		t.Errorf("reserved transform = %v, want the identity", transform)
	}
}

// A chained contour needs one texel per curve plus one; each break adds one.
func TestPackCurvesShareEndpoints(t *testing.T) {
	var two Path
	for _, x := range []float32{0, 10} {
		two.MoveTo(Point{x, 0})
		two.LineTo(Point{x + 4, 0})
		two.LineTo(Point{x, 4})
		two.Close()
	}
	var p Pack
	mustAdd(t, &p, 1, Drawing{Em: testEm, Layers: []Layer{{Path: two}}})

	// Two contours of 3 curves: (3+1) * 2 texels.
	if got := len(p.Curves()) / 4; got != 8 {
		t.Errorf("got %d texels, want 8", got)
	}
}

// A host uploads each range one time, so old data and entries must not move.
func TestPackAddKeepsOldData(t *testing.T) {
	var p Pack
	a := Drawing{Em: testEm, Layers: []Layer{{Path: squarePath(0, 0, 8)}}}
	b := Drawing{Em: testEm, Layers: []Layer{{Path: ringPath(0, 0, 50, 8)}}}

	first := mustAdd(t, &p, 1, a)
	curves := slices.Clone(p.Curves())
	bands := slices.Clone(p.Bands())
	layers := slices.Clone(p.Layers())

	if again := mustAdd(t, &p, 1, b); again != first {
		t.Errorf("second Add = %v, want the first entry %v", again, first)
	}
	if len(p.Curves()) != len(curves) {
		t.Errorf("second Add of a key wrote data")
	}

	second := mustAdd(t, &p, 2, b)
	if second.FirstLayer != first.LayerCount {
		t.Errorf("FirstLayer = %d, want %d", second.FirstLayer, first.LayerCount)
	}
	if !slices.Equal(p.Curves()[:len(curves)], curves) ||
		!slices.Equal(p.Bands()[:len(bands)], bands) ||
		!slices.Equal(p.Layers()[:len(layers)], layers) {
		t.Errorf("the second drawing changed old data")
	}
}

func TestPackTakeDirty(t *testing.T) {
	var p Pack
	mustAdd(t, &p, 1, Drawing{Em: testEm, Layers: []Layer{{Path: squarePath(0, 0, 8)}}})
	nC, nB, nL := len(p.Curves()), len(p.Bands()), len(p.Layers())

	want := Dirty{Range{0, nC}, Range{0, nB}, Range{0, nL}}
	if got := p.TakeDirty(); got != want {
		t.Errorf("first TakeDirty = %v, want %v", got, want)
	}
	want = Dirty{Range{nC, nC}, Range{nB, nB}, Range{nL, nL}}
	if got := p.TakeDirty(); got != want {
		t.Errorf("TakeDirty with no Add = %v, want %v", got, want)
	}

	mustAdd(t, &p, 2, Drawing{Em: testEm, Layers: []Layer{{Path: squarePath(0, 0, 9)}}})
	want = Dirty{Range{nC, len(p.Curves())}, Range{nB, len(p.Bands())}, Range{nL, len(p.Layers())}}
	if got := p.TakeDirty(); got != want {
		t.Errorf("TakeDirty after Add = %v, want %v", got, want)
	}
}

func TestPackAddFailureChangesNothing(t *testing.T) {
	good := Layer{Path: squarePath(0, 0, 8)}
	broken := Layer{Path: Path{Verbs: []Verb{LineTo}, Points: []Point{{1, 1}}}}

	var p Pack
	// The good layer comes first: the broken one must not leave it behind.
	_, err := p.Add(Key{ID: 1}, Drawing{Em: testEm, Layers: []Layer{good, broken}})
	if !errors.Is(err, ErrInvalidPath) {
		t.Errorf("broken path: err = %v, want ErrInvalidPath", err)
	}
	if _, err := p.Add(Key{ID: 1}, Drawing{Layers: []Layer{good}}); err == nil {
		t.Errorf("zero Em: err = nil")
	}
	// The coordinates are finite, but they overflow after the division by Em.
	huge := Layer{Path: squarePath(0, 0, 1e10)}
	_, err = p.Add(Key{ID: 1}, Drawing{Em: 1e-30, Layers: []Layer{good, huge}})
	if !errors.Is(err, ErrInvalidPath) {
		t.Errorf("overflow: err = %v, want ErrInvalidPath", err)
	}
	if len(p.Curves()) != 0 || len(p.Bands()) != 0 || len(p.Layers()) != 0 {
		t.Errorf("a failed Add wrote data")
	}
	if _, ok := p.Lookup(Key{ID: 1}); ok {
		t.Errorf("a failed Add registered the key")
	}
}

// A space glyph has an entry, so a host does not ask for it again.
func TestPackDrawingWithoutCurves(t *testing.T) {
	var p Pack
	e := mustAdd(t, &p, 1, Drawing{Em: testEm})
	if e.LayerCount != 0 {
		t.Errorf("LayerCount = %d, want 0", e.LayerCount)
	}
	if _, ok := p.Lookup(Key{ID: 1}); !ok {
		t.Errorf("Lookup did not find the entry")
	}
}

// The stops are in the bands, so a failed Add must also remove them.
func TestPackFullRemovesGradientStops(t *testing.T) {
	d := Drawing{Em: rasterEm, Layers: []Layer{{Path: squarePath(0, 0, 8), Paint: LinearPaint(Point{0, 0}, Point{8, 0}, ramp...)}}}
	var size Pack
	mustAdd(t, &size, 1, d)
	p := Pack{MaxBytes: size.Size() - 1}
	if _, err := p.Add(Key{ID: 1}, d); !errors.Is(err, ErrPackFull) {
		t.Fatalf("err = %v, want ErrPackFull", err)
	}
	if p.Size() != 0 {
		t.Errorf("the pack has %d bytes", p.Size())
	}
}
