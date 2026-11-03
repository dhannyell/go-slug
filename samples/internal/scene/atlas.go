package scene

import (
	"fmt"
	"image"
	"math"
	"slices"

	slug "github.com/dhannyell/go-slug"
)

// Method is a traditional way to draw a glyph: from a texture made in advance.
type Method int

const (
	// Bitmap stores the coverage of each glyph.
	Bitmap Method = iota
	// Distance stores a distance to the outline: a signed distance field with one
	// channel.
	Distance
)

func (m Method) String() string {
	if m == Distance {
		return "signed distance field"
	}
	return "bitmap atlas"
}

const (
	// atlasPadding is the free texels around a glyph. It is also the largest
	// distance that a field stores.
	atlasPadding = 4
	// AtlasLevels is the number of mipmap levels. After them, the padding is less
	// than one texel, and a glyph takes color from the next cell.
	AtlasLevels = 4
	// distanceSamples is the number of samples per texel along one axis, for the
	// outline of a distance field.
	distanceSamples = 8
)

// Atlas holds one image per glyph of a scene, in one channel.
type Atlas struct {
	Side        int
	Levels      [][]byte // the texels of each mipmap level, from the largest
	PixelsPerEm float32
	Bytes       int // the size of all levels

	cells map[uint32]image.Point
}

// Cell returns the texel of the lower corner of the bounds of e.
func (a *Atlas) Cell(e slug.Entry) image.Point { return a.cells[e.FirstLayer] }

// Padding returns the size of the free texels around a glyph, in em.
func (a *Atlas) Padding() float32 { return atlasPadding / a.PixelsPerEm }

// Plain tells that an item is a rectangle. An application draws it as geometry,
// and not from a texture.
func (it Item) Plain() bool { return it.Entry.Key.Source == sourceHall }

// PackBytes returns the size of the data that Slug uploads for the scene.
func (s *Scene) PackBytes() int {
	return 4 * (len(s.Pack.Curves()) + len(s.Pack.Bands()) + len(s.Pack.Layers()))
}

// Atlas builds a texture of at most budget bytes, with the largest glyphs that fit.
func (s *Scene) Atlas(method Method, budget int) (*Atlas, error) {
	var entries []slug.Entry
	seen := map[uint32]bool{}
	for _, it := range s.Items {
		if it.Plain() || it.Entry.LayerCount == 0 || seen[it.Entry.FirstLayer] {
			continue
		}
		if it.Entry.LayerCount != 1 {
			return nil, fmt.Errorf("scene: an atlas holds one layer per glyph, and %v has %d", it.Entry.Key, it.Entry.LayerCount)
		}
		seen[it.Entry.FirstLayer] = true
		entries = append(entries, it.Entry)
	}
	slices.SortFunc(entries, func(a, b slug.Entry) int {
		return int(math.Copysign(1, float64(b.Bounds.Max.Y-b.Bounds.Min.Y-a.Bounds.Max.Y+a.Bounds.Min.Y)))
	})

	// A side with this multiple has exact mipmap levels.
	const multiple = 1 << (AtlasLevels - 1)
	side := multiple
	for levelBytes(side+multiple) <= budget {
		side += multiple
	}
	lo, hi := float32(1), float32(side)
	if _, ok := place(entries, lo, side); !ok {
		return nil, fmt.Errorf("scene: %d glyphs do not fit in an atlas of %d texels", len(entries), side)
	}
	for hi-lo > 0.25 {
		mid := (lo + hi) / 2
		if _, ok := place(entries, mid, side); ok {
			lo = mid
		} else {
			hi = mid
		}
	}
	cells, _ := place(entries, lo, side)

	base := &image.Alpha{Pix: make([]byte, side*side), Stride: side, Rect: image.Rect(0, 0, side, side)}
	for _, e := range entries {
		var err error
		if method == Distance {
			err = s.drawDistance(e, lo, cells[e.FirstLayer], base)
		} else {
			err = s.Pack.Coverage(e.FirstLayer, cellView(e, lo, cells[e.FirstLayer], 1), base)
		}
		if err != nil {
			return nil, err
		}
	}
	a := &Atlas{Side: side, PixelsPerEm: lo, Bytes: levelBytes(side), cells: cells, Levels: [][]byte{base.Pix}}
	for range AtlasLevels - 1 {
		a.Levels = append(a.Levels, halve(a.Levels[len(a.Levels)-1], side>>(len(a.Levels)-1)))
	}
	return a, nil
}

func levelBytes(side int) int {
	total := 0
	for l := range AtlasLevels {
		total += (side >> l) * (side >> l)
	}
	return total
}

// place puts the glyphs in rows, and returns false when they do not fit. The glyphs
// come sorted by height.
func place(entries []slug.Entry, pixelsPerEm float32, side int) (map[uint32]image.Point, bool) {
	cells := make(map[uint32]image.Point, len(entries))
	x, y, row := 0, 0, 0
	for _, e := range entries {
		w, h := cellSize(e, pixelsPerEm)
		if x+w > side {
			x, y, row = 0, y+row, 0
		}
		if x+w > side || y+h > side {
			return nil, false
		}
		cells[e.FirstLayer] = image.Pt(x+atlasPadding, y+atlasPadding)
		x, row = x+w, max(row, h)
	}
	return cells, true
}

func cellSize(e slug.Entry, pixelsPerEm float32) (w, h int) {
	size := func(lo, hi float32) int { return int(math.Ceil(float64((hi-lo)*pixelsPerEm))) + 2*atlasPadding }
	return size(e.Bounds.Min.X, e.Bounds.Max.X), size(e.Bounds.Min.Y, e.Bounds.Max.Y)
}

// cellView maps the em space of e to the texels of its cell, with samples per texel.
func cellView(e slug.Entry, pixelsPerEm float32, cell image.Point, samples float32) slug.Affine {
	k := pixelsPerEm * samples
	return slug.Affine{k, 0, 0, k, float32(cell.X)*samples - e.Bounds.Min.X*k, float32(cell.Y)*samples - e.Bounds.Min.Y*k}
}

// drawDistance writes the distance field of e. A texel at 0.5 is on the outline,
// and a larger value is inside.
func (s *Scene) drawDistance(e slug.Entry, pixelsPerEm float32, cell image.Point, dst *image.Alpha) error {
	w, h := cellSize(e, pixelsPerEm)
	n := distanceSamples
	fine := image.NewAlpha(image.Rect(0, 0, w*n, h*n))
	view := cellView(e, pixelsPerEm, image.Pt(atlasPadding, atlasPadding), float32(n))
	if err := s.Pack.Coverage(e.FirstLayer, view, fine); err != nil {
		return err
	}
	inside := make([]bool, len(fine.Pix))
	for i, v := range fine.Pix {
		inside[i] = v >= 128
	}
	toInside := squaredDistances(inside, w*n, h*n, true)
	toOutside := squaredDistances(inside, w*n, h*n, false)
	signed := func(x, y int) float64 {
		i := y*w*n + x
		return math.Sqrt(toOutside[i]) - math.Sqrt(toInside[i])
	}
	for y := range h {
		for x := range w {
			// The center of a texel is between four samples.
			cx, cy := x*n+n/2, y*n+n/2
			d := (signed(cx-1, cy-1) + signed(cx, cy-1) + signed(cx-1, cy) + signed(cx, cy)) / 4
			v := 0.5 + d/float64(n)/(2*atlasPadding)
			dst.Pix[(cell.Y-atlasPadding+y)*dst.Stride+cell.X-atlasPadding+x] = uint8(min(max(v, 0), 1)*255 + 0.5)
		}
	}
	return nil
}

// squaredDistances returns, for each sample, the squared distance to the nearest
// sample where mask equals want. It is the transform of Felzenszwalb and
// Huttenlocher: one pass per axis, over the lower envelope of parabolas.
func squaredDistances(mask []bool, w, h int, want bool) []float64 {
	const far = 1e20
	out := make([]float64, w*h)
	for i, m := range mask {
		if m != want {
			out[i] = far
		}
	}
	n := max(w, h)
	f, d, z, v := make([]float64, n), make([]float64, n), make([]float64, n+1), make([]int, n)
	pass := func(count, length, stride, step int) {
		for c := range count {
			for i := range length {
				f[i] = out[c*stride+i*step]
			}
			k := 0
			v[0], z[0], z[1] = 0, -far, far
			for q := 1; q < length; q++ {
				var s float64
				for {
					p := v[k]
					s = (f[q] + float64(q*q) - f[p] - float64(p*p)) / float64(2*(q-p))
					if s > z[k] {
						break
					}
					k--
				}
				k++
				v[k], z[k], z[k+1] = q, s, far
			}
			k = 0
			for q := range length {
				for z[k+1] < float64(q) {
					k++
				}
				p := v[k]
				d[q] = float64((q-p)*(q-p)) + f[p]
			}
			for i := range length {
				out[c*stride+i*step] = d[i]
			}
		}
	}
	pass(h, w, w, 1)
	pass(w, h, 1, w)
	return out
}

// halve returns the next mipmap level, with a box filter.
func halve(pix []byte, side int) []byte {
	half := side / 2
	out := make([]byte, half*half)
	for y := range half {
		for x := range half {
			i := 2*y*side + 2*x
			sum := int(pix[i]) + int(pix[i+1]) + int(pix[i+side]) + int(pix[i+side+1])
			out[y*half+x] = uint8((sum + 2) / 4)
		}
	}
	return out
}
