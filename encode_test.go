package slug

import (
	"encoding/binary"
	"errors"
	"slices"
	"testing"
)

func encodedPack(t *testing.T) *Pack {
	t.Helper()
	var p Pack
	mustAdd(t, &p, 1, Drawing{Em: testEm, Layers: []Layer{{Path: squarePath(0, 0, 8)}}})
	mustAdd(t, &p, 2, Drawing{Em: testEm, Layers: []Layer{{Path: ringPath(0, 0, 50, 8)}}})
	mustAdd(t, &p, 3, Drawing{Em: testEm})
	return &p
}

func TestEncodeRoundTrip(t *testing.T) {
	p := encodedPack(t)

	got, err := Decode(p.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Curves(), p.Curves()) || !slices.Equal(got.Bands(), p.Bands()) ||
		!slices.Equal(got.Layers(), p.Layers()) || !slices.Equal(got.Entries(), p.Entries()) {
		t.Errorf("decoded pack differs from the original")
	}
	if e, ok := got.Lookup(Key{ID: 2}); !ok || e != p.Entries()[1] {
		t.Errorf("Lookup = %v, %v, want %v", e, ok, p.Entries()[1])
	}

	// The host of a decoded pack has nothing yet: all data is dirty.
	want := Range{0, len(p.Curves())}
	if d := got.TakeDirty(); d.Curves != want {
		t.Errorf("dirty curves = %v, want %v", d.Curves, want)
	}

	// A decoded pack accepts new drawings at the same offsets as the original.
	d := Drawing{Em: testEm, Layers: []Layer{{Path: squarePath(1, 1, 2)}}}
	if a, b := mustAdd(t, got, 4, d), mustAdd(t, p, 4, d); a != b {
		t.Errorf("Add after Decode = %v, want %v", a, b)
	}
	if !slices.Equal(got.Encode(), p.Encode()) {
		t.Errorf("encoded bytes differ after the same Add")
	}
}

// A later minor addition must not break old readers.
func TestDecodeSkipsUnknownSection(t *testing.T) {
	p := encodedPack(t)
	data := appendSection(p.Encode(), 99, []uint32{0xDEADBEEF})

	got, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Entries(), p.Entries()) {
		t.Errorf("entries differ")
	}
}

func TestDecodeRejectsBadData(t *testing.T) {
	section := func(id uint32, words ...uint32) []byte { return appendSection(nil, id, words) }
	header := binary.LittleEndian.AppendUint32([]byte(packMagic), packVersion)
	otherVersion := binary.LittleEndian.AppendUint32([]byte(packMagic), packVersion+1)
	oddSize := []byte{99, 0, 0, 0, 3, 0, 0, 0, 1, 2, 3}
	curves, bands, layers, entries := section(sectionCurves), section(sectionBands), section(sectionLayers), section(sectionEntries)
	good := encodedPack(t).Encode()

	withEntries := func(words ...uint32) []byte {
		return slices.Concat(header, curves, bands, layers, section(sectionEntries, words...))
	}

	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"bad magic", slices.Concat([]byte("NOPE"), good[4:])},
		{"other version", slices.Concat(otherVersion, good[8:])},
		{"cut payload", good[:len(good)-1]},
		{"cut section header", slices.Concat(good, []byte{1, 2, 3})},
		{"size not multiple of 4", slices.Concat(good, oddSize)},
		{"missing section", slices.Concat(header, curves, bands, layers)},
		{"repeated section", slices.Concat(good, bands)},
		{"partial texel", slices.Concat(header, section(sectionCurves, 1), bands, layers, entries)},
		{"partial layer", slices.Concat(header, curves, bands, section(sectionLayers, 1), entries)},
		{"partial entry", withEntries(1)},
		{"entry outside the layers", withEntries(0, 1, 0, 1, 0, 0, 0, 0)},
		{"entry range overflows", withEntries(0, 1, 0xFFFFFFFF, 1, 0, 0, 0, 0)},
		{"repeated key", withEntries(0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0)},
	}
	for _, c := range cases {
		if _, err := Decode(c.data); !errors.Is(err, ErrInvalidPack) {
			t.Errorf("%s: err = %v, want ErrInvalidPack", c.name, err)
		}
	}
}
