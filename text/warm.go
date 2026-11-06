package text

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"time"

	slug "github.com/dhannyell/go-slug"
)

// ErrStalePack reports a Pack whose stamp for the source of a font is not the stamp
// of the font: its glyphs came from another font file, style or palette.
var ErrStalePack = errors.New("text: the pack has glyphs of another font")

// stampVersion changes when Add makes other drawings from the same font.
const stampVersion = 1

// Stamp returns a SHA-256 of the font data, the style, the palette and the version of
// the conversion. Bake writes it into a Pack. The first call hashes the font data.
func (f *Font) Stamp() [32]byte {
	if f.stamp != nil {
		return *f.stamp
	}
	base := f.parsed()
	if base.digest == nil {
		d := sha256.Sum256(base.data)
		base.digest = &d
	}
	h := sha256.New()
	h.Write([]byte("go-slug/text stamp"))
	h.Write(base.digest[:])
	s := f.style
	var b []byte
	for _, v := range []float32{s.Bold, s.Slant, s.Outline} {
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
	}
	b = binary.LittleEndian.AppendUint32(b, uint32(s.Join))
	b = binary.LittleEndian.AppendUint32(b, uint32(f.palette))
	b = binary.LittleEndian.AppendUint32(b, stampVersion)
	h.Write(b)
	f.stamp = new([32]byte)
	h.Sum(f.stamp[:0])
	return *f.stamp
}

// checkStamp returns ErrStalePack if p has a stamp for f that f did not make.
func (f *Font) checkStamp(p *slug.Pack) error {
	if s, ok := p.Stamp(f.source); ok && s != f.Stamp() {
		return ErrStalePack
	}
	return nil
}

// Warmer adds the glyphs of a text to a Pack over many frames, so a new glyph does not
// cost time in the frame that first draws it.
type Warmer struct {
	font *Font
	ids  []uint32 // the glyphs left, in order
}

// Warmer shapes str once and keeps its glyphs: each rune alone, and the shaped text,
// so ligatures and contextual forms enter too.
func (f *Font) Warmer(str string, o Options) *Warmer {
	w := &Warmer{font: f}
	seen := make(map[uint32]bool)
	add := func(id uint32) {
		if !seen[id] {
			seen[id] = true
			w.ids = append(w.ids, id)
		}
	}
	for _, r := range str {
		if id, ok := f.face.NominalGlyph(r); ok {
			add(uint32(id))
		}
	}
	for _, l := range f.Shape(str, o) {
		for _, g := range l.Glyphs {
			add(g.ID)
		}
	}
	return w
}

// Step adds glyphs to p until budget ends, and returns true when no glyph is left. It
// adds at least one glyph, so a budget of 0 adds one glyph for each call. Call it once
// a frame, or with a large budget in a loading screen.
//
// A glyph that fails to convert stays out of p, and the error tells which; the next
// glyphs continue. If p is full, Step stops with slug.ErrPackFull and keeps the glyph
// for the next call.
func (w *Warmer) Step(p *slug.Pack, budget time.Duration) (done bool, err error) {
	start := time.Now()
	var errs []error
	for len(w.ids) > 0 {
		_, err := w.font.Add(p, Glyph{ID: w.ids[0]})
		if errors.Is(err, slug.ErrPackFull) || errors.Is(err, ErrStalePack) {
			return false, errors.Join(append(errs, err)...)
		}
		if err != nil {
			errs = append(errs, err)
		}
		w.ids = w.ids[1:]
		if time.Since(start) >= budget {
			break
		}
	}
	return len(w.ids) == 0, errors.Join(errs...)
}

// Bake adds the glyphs of str to p, like a Warmer without a budget, and sets the stamp
// of the source of f. Encode the Pack to ship it, and Decode it at the start: Add then
// finds each baked glyph, and checks the stamp. A baked Pack is larger than the font,
// so it saves time at the start and costs bytes in a download.
func (f *Font) Bake(p *slug.Pack, str string, o Options) error {
	if err := f.checkStamp(p); err != nil {
		return err
	}
	_, err := f.Warmer(str, o).Step(p, math.MaxInt64)
	p.SetStamp(f.source, f.Stamp())
	return err
}
