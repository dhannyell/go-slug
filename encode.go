package slug

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// ErrInvalidPack reports data that Decode cannot read.
var ErrInvalidPack = errors.New("slug: invalid pack data")

const (
	packMagic   = "SLUG"
	packVersion = 1

	entryWords = 8
)

// Section IDs. Decode skips an unknown ID, so a later minor addition does not
// need a new version.
const (
	sectionCurves uint32 = iota + 1
	sectionBands
	sectionLayers
	sectionEntries
)

// Encode returns the pack as bytes. FORMAT.md describes the bytes.
func (p *Pack) Encode() []byte {
	curves := make([]uint32, len(p.curves))
	for i, v := range p.curves {
		curves[i] = math.Float32bits(v)
	}
	entries := make([]uint32, 0, len(p.entries)*entryWords)
	for _, e := range p.entries {
		f := math.Float32bits
		entries = append(entries, e.Key.Source, e.Key.ID, e.FirstLayer, e.LayerCount,
			f(e.Bounds.Min.X), f(e.Bounds.Min.Y), f(e.Bounds.Max.X), f(e.Bounds.Max.Y))
	}

	out := append([]byte(nil), packMagic...)
	out = binary.LittleEndian.AppendUint32(out, packVersion)
	out = appendSection(out, sectionCurves, curves)
	out = appendSection(out, sectionBands, p.bands)
	out = appendSection(out, sectionLayers, p.layers)
	return appendSection(out, sectionEntries, entries)
}

func appendSection(out []byte, id uint32, words []uint32) []byte {
	out = binary.LittleEndian.AppendUint32(out, id)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(words)*4))
	for _, w := range words {
		out = binary.LittleEndian.AppendUint32(out, w)
	}
	return out
}

// Decode reads the bytes of Encode. It checks the structure and the entries.
// It does not check the addresses inside the band data.
func Decode(data []byte) (*Pack, error) {
	if len(data) < 8 || string(data[:4]) != packMagic {
		return nil, fmt.Errorf("%w: no header", ErrInvalidPack)
	}
	if v := binary.LittleEndian.Uint32(data[4:]); v != packVersion {
		return nil, fmt.Errorf("%w: version %d", ErrInvalidPack, v)
	}

	sections := make(map[uint32][]uint32)
	rest := data[8:]
	for len(rest) > 0 {
		if len(rest) < 8 {
			return nil, fmt.Errorf("%w: cut section header", ErrInvalidPack)
		}
		id, size := binary.LittleEndian.Uint32(rest), binary.LittleEndian.Uint32(rest[4:])
		rest = rest[8:]
		if size%4 != 0 || uint64(size) > uint64(len(rest)) {
			return nil, fmt.Errorf("%w: section %d has bad size %d", ErrInvalidPack, id, size)
		}
		if id >= sectionCurves && id <= sectionEntries {
			if _, seen := sections[id]; seen {
				return nil, fmt.Errorf("%w: section %d repeats", ErrInvalidPack, id)
			}
			sections[id] = wordsOf(rest[:size])
		}
		rest = rest[size:]
	}
	for id := sectionCurves; id <= sectionEntries; id++ {
		if _, ok := sections[id]; !ok {
			return nil, fmt.Errorf("%w: section %d is missing", ErrInvalidPack, id)
		}
	}

	curves, layers, entries := sections[sectionCurves], sections[sectionLayers], sections[sectionEntries]
	if len(curves)%4 != 0 || len(layers)%LayerWords != 0 || len(entries)%entryWords != 0 {
		return nil, fmt.Errorf("%w: section size is not a whole record", ErrInvalidPack)
	}

	p := &Pack{
		curves: make([]float32, len(curves)),
		bands:  sections[sectionBands],
		layers: layers,
	}
	for i, w := range curves {
		p.curves[i] = math.Float32frombits(w)
	}

	f := math.Float32frombits
	for i := 0; i < len(entries); i += entryWords {
		w := entries[i : i+entryWords]
		e := Entry{
			Key:        Key{w[0], w[1]},
			FirstLayer: w[2],
			LayerCount: w[3],
			Bounds:     Rect{Point{f(w[4]), f(w[5])}, Point{f(w[6]), f(w[7])}},
		}
		if uint64(e.FirstLayer)+uint64(e.LayerCount) > uint64(len(layers)/LayerWords) {
			return nil, fmt.Errorf("%w: entry %v is outside the layers", ErrInvalidPack, e.Key)
		}
		if _, dup := p.Lookup(e.Key); dup {
			return nil, fmt.Errorf("%w: entry %v repeats", ErrInvalidPack, e.Key)
		}
		p.register(e)
	}
	return p, nil
}

func wordsOf(b []byte) []uint32 {
	words := make([]uint32, len(b)/4)
	for i := range words {
		words[i] = binary.LittleEndian.Uint32(b[i*4:])
	}
	return words
}
