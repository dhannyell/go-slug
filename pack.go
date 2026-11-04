package slug

import (
	"errors"
	"fmt"
	"math"
)

// LayerWords is the size of one layer record in Pack.Layers.
const LayerWords = 24

const (
	bandOverlap   = 1.0 / 1024 // in em space; keeps float errors from missing a band
	cubicTolPerEm = 1.0 / 4096 // about 1/4 pixel for a glyph 1000 pixels tall
)

// Key identifies a drawing in a Pack. Source separates the origins of IDs, for
// example two fonts. ID is the glyph index for a font.
type Key struct {
	Source, ID uint32
}

// Entry tells where one drawing is in a Pack. A drawing without curves, such
// as a space glyph, has no layers.
type Entry struct {
	Key        Key
	FirstLayer uint32 // index of a record, not of a word
	LayerCount uint32
	Bounds     Rect // in em space
}

// Range is a half-open range of slice indices.
type Range struct {
	Start, End int
}

// Dirty tells which elements of the Pack slices a host must upload.
type Dirty struct {
	Curves, Bands, Layers Range
}

// ErrPackFull reports a drawing that does not fit in Pack.MaxBytes.
var ErrPackFull = errors.New("slug: the pack is full")

// Pack holds the GPU data of many drawings. FORMAT.md describes the data.
// Coordinates are in em space: drawing units divided by Drawing.Em.
//
// A Pack is an arena. The data and the Entry of a drawing do not change until
// Reset, so a host can upload new data without touching old data. The zero value
// is an empty Pack. A Pack is not safe for concurrent use.
type Pack struct {
	// MaxBytes limits Size. 0 is no limit. A host can use it to make buffers of a
	// fixed size. Decode does not restore it.
	MaxBytes int

	curves []float32
	bands  []uint32
	layers []uint32

	entries []Entry
	index   map[Key]int

	clean      Dirty // End fields only: how much the host has
	generation uint32
}

// Add puts d into the pack and returns its Entry. If key is already in the
// pack, Add ignores d and returns the existing Entry. If Add returns an error,
// the pack does not change.
//
// Add returns ErrPackFull when d does not fit in MaxBytes. A drawing larger than
// MaxBytes also fails after Reset.
func (p *Pack) Add(key Key, d Drawing) (Entry, error) {
	if e, ok := p.Lookup(key); ok {
		return e, nil
	}
	if !(d.Em > 0) {
		return Entry{}, fmt.Errorf("slug: Drawing.Em %v is not positive", d.Em)
	}
	shapes, err := normalize(d, d.Em*cubicTolPerEm)
	if err != nil {
		return Entry{}, err
	}
	for i := range shapes {
		if err := toEm(&shapes[i], d.Em); err != nil {
			return Entry{}, err
		}
	}

	e := Entry{
		Key:        key,
		FirstLayer: uint32(len(p.layers) / LayerWords),
		LayerCount: uint32(len(shapes)),
	}
	curves, bands, layers := len(p.curves), len(p.bands), len(p.layers)
	for i, s := range shapes {
		if i == 0 {
			e.Bounds = s.Bounds
		} else {
			e.Bounds = union(e.Bounds, s.Bounds)
		}
		p.appendShape(s)
	}
	if p.MaxBytes > 0 && p.Size() > p.MaxBytes {
		size := p.Size()
		// appendShape writes only after these lengths, so this removes all of d.
		p.curves, p.bands, p.layers = p.curves[:curves], p.bands[:bands], p.layers[:layers]
		return Entry{}, fmt.Errorf("%w: needs %d bytes, and the limit is %d", ErrPackFull, size, p.MaxBytes)
	}
	p.register(e)
	return e, nil
}

// Reset empties the pack and keeps its capacity. Each Entry from before Reset
// becomes invalid: an old Entry draws the wrong shape, without an error. The next
// TakeDirty starts at index 0, so the host writes over the old data.
func (p *Pack) Reset() {
	p.curves, p.bands, p.layers = p.curves[:0], p.bands[:0], p.layers[:0]
	p.entries = p.entries[:0]
	clear(p.index)
	p.clean = Dirty{}
	p.generation++
}

// Size returns the bytes of the GPU data: Curves, Bands and Layers.
func (p *Pack) Size() int {
	return 4 * (len(p.curves) + len(p.bands) + len(p.layers))
}

// Generation returns the number of calls to Reset. A host that keeps an Entry
// compares this value to know when the Entry becomes invalid.
func (p *Pack) Generation() uint32 { return p.generation }

// toEm divides the coordinates of s by em. Equal values stay equal, so shared
// endpoints and flat curves stay valid.
func toEm(s *shape, em float32) error {
	for i, c := range s.Curves {
		s.Curves[i] = curve{
			Point{c.P0.X / em, c.P0.Y / em},
			Point{c.P1.X / em, c.P1.Y / em},
			Point{c.P2.X / em, c.P2.Y / em},
		}
	}
	s.Bounds, _ = boundsOf(s.Curves)
	if !finite(s.Bounds.Min) || !finite(s.Bounds.Max) {
		return fmt.Errorf("%w: coordinates overflow at Em %v", ErrInvalidPath, em)
	}
	return nil
}

// Lookup returns the Entry of key.
func (p *Pack) Lookup(key Key) (Entry, bool) {
	i, ok := p.index[key]
	if !ok {
		return Entry{}, false
	}
	return p.entries[i], true
}

// Entries returns all entries in the order of Add. Do not change the slice.
func (p *Pack) Entries() []Entry { return p.entries }

// Curves returns the curve data: four values per texel. Do not change the slice.
func (p *Pack) Curves() []float32 { return p.curves }

// Bands returns the band data. Do not change the slice.
func (p *Pack) Bands() []uint32 { return p.bands }

// Layers returns the layer records, LayerWords each. Do not change the slice.
func (p *Pack) Layers() []uint32 { return p.layers }

// TakeDirty returns the data added since the last call. A decoded Pack starts
// with all of its data dirty.
func (p *Pack) TakeDirty() Dirty {
	d := Dirty{
		Curves: Range{p.clean.Curves.End, len(p.curves)},
		Bands:  Range{p.clean.Bands.End, len(p.bands)},
		Layers: Range{p.clean.Layers.End, len(p.layers)},
	}
	p.clean = d
	return d
}

func (p *Pack) register(e Entry) {
	if p.index == nil {
		p.index = make(map[Key]int)
	}
	p.index[e.Key] = len(p.entries)
	p.entries = append(p.entries, e)
}

func (p *Pack) appendShape(s shape) {
	locs := p.appendCurves(s.Curves)
	set := buildBands(s.Curves, s.Bounds, bandOverlap)
	bandLoc := p.appendBands(set, locs)

	scaleX, offsetX := bandFit(s.Bounds.Min.X, s.Bounds.Max.X, len(set.V))
	scaleY, offsetY := bandFit(s.Bounds.Min.Y, s.Bounds.Max.Y, len(set.H))
	f := math.Float32bits
	c := s.Paint.Color
	p.layers = append(p.layers,
		bandLoc,
		uint32(len(set.H))|uint32(len(set.V))<<16,
		uint32(s.Rule)|uint32(s.Paint.Kind)<<8,
		0, // clip reference: reserved
		f(scaleX), f(scaleY), f(offsetX), f(offsetY),
		f(s.Bounds.Min.X), f(s.Bounds.Min.Y), f(s.Bounds.Max.X), f(s.Bounds.Max.Y),
		f(c[0]), f(c[1]), f(c[2]), f(c[3]),
		f(1), f(0), f(0), f(1), f(0), f(0), 0, 0, // transform: reserved identity
	)
}

// appendCurves writes curves and returns the texel of each. A curve that starts
// at the end of the previous curve shares its texel.
func (p *Pack) appendCurves(curves []curve) []uint32 {
	locs := make([]uint32, len(curves))
	for i, c := range curves {
		if i > 0 && c.P0 == curves[i-1].P2 {
			n := len(p.curves)
			p.curves[n-2], p.curves[n-1] = c.P1.X, c.P1.Y
		} else {
			p.curves = append(p.curves, c.P0.X, c.P0.Y, c.P1.X, c.P1.Y)
		}
		locs[i] = uint32(len(p.curves)/4 - 1)
		p.curves = append(p.curves, c.P2.X, c.P2.Y, 0, 0)
	}
	return locs
}

// appendBands writes the band headers and lists, and returns the first word.
func (p *Pack) appendBands(set bandSet, locs []uint32) uint32 {
	start := len(p.bands)
	p.bands = append(p.bands, make([]uint32, 2*(len(set.H)+len(set.V)))...)

	header := start
	for _, bands := range [][]band{set.H, set.V} {
		for _, b := range bands {
			p.bands[header] = uint32(len(b))
			p.bands[header+1] = uint32(len(p.bands) - start)
			header += 2
			for _, i := range b {
				p.bands = append(p.bands, locs[i])
			}
		}
	}
	return uint32(start)
}

// bandFit returns the scale and offset that map a coordinate to a band index.
// A flat axis has one band: scale 0 selects it for every coordinate.
func bandFit(lo, hi float32, n int) (scale, offset float32) {
	if hi <= lo {
		return 0, 0
	}
	s := float64(n) / (float64(hi) - float64(lo))
	return float32(s), float32(-float64(lo) * s)
}
