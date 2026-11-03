package text

import (
	"testing"

	"github.com/go-text/typesetting-utils/opentype"
	"golang.org/x/image/font/sfnt"

	slug "github.com/dhannyell/go-slug"
)

func raleway(t *testing.T) *Font { return load(t, "common/Raleway-v4020-Regular.otf") }

func load(t *testing.T, name string) *Font {
	t.Helper()
	data, err := opentype.Files.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(data, 7)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// The shaper must change the advance of a pair: a plain sum of advances is the
// result that this module exists to replace.
func TestKerningMovesTheGlyph(t *testing.T) {
	f := raleway(t)
	av := f.Shape("AV", Options{})[0]
	a := f.Shape("A", Options{})[0]
	if !(av.Glyphs[1].X < a.Width) {
		t.Errorf("V is at %v, and the advance of A is %v", av.Glyphs[1].X, a.Width)
	}
}

// A ligature has a glyph that no rune maps to. The ID of the shaper must select
// the same glyph in the outline reader, or the pack draws the wrong shape.
func TestLigatureHasAnOutline(t *testing.T) {
	f := raleway(t)
	line := f.Shape("xffi", Options{})[0]
	if len(line.Glyphs) != 2 {
		t.Fatalf("got %d glyphs, want x and the ligature", len(line.Glyphs))
	}
	if got := line.Glyphs[1].Cluster; got != 1 {
		t.Errorf("the ligature has cluster %d, want 1", got)
	}

	x, err := f.outlines.GlyphIndex('x')
	if err != nil {
		t.Fatal(err)
	}
	if line.Glyphs[0].ID != uint32(x) {
		t.Errorf("x has ID %d in the shaper and %d in the outline reader", line.Glyphs[0].ID, x)
	}

	var pack slug.Pack
	e, err := f.Add(&pack, line.Glyphs[1])
	if err != nil {
		t.Fatal(err)
	}
	if e.Key != (slug.Key{Source: 7, ID: line.Glyphs[1].ID}) || e.LayerCount != 1 {
		t.Errorf("got entry %+v", e)
	}
	if _, err := f.outlines.Glyph(sfnt.GlyphIndex(line.Glyphs[1].ID)); err != nil {
		t.Error(err)
	}
}

// The glyphs of a line are in screen order, with X going up. A run of Hebrew
// inside Latin text must reverse its clusters only.
func TestBidiIsInScreenOrder(t *testing.T) {
	f := load(t, "common/DejaVuSans.ttf")
	line := f.Shape("ab אבג", Options{})[0]
	want := []int{0, 1, 2, 5, 4, 3}
	if len(line.Glyphs) != len(want) {
		t.Fatalf("got %d glyphs, want %d", len(line.Glyphs), len(want))
	}
	for i, g := range line.Glyphs {
		if g.Cluster != want[i] {
			t.Errorf("glyph %d has cluster %d, want %d", i, g.Cluster, want[i])
		}
		if i > 0 && !(g.X > line.Glyphs[i-1].X) {
			t.Errorf("glyph %d at %v does not follow %v", i, g.X, line.Glyphs[i-1].X)
		}
	}

	rtl := f.Shape("ab אבג", Options{RTL: true})[0]
	if got := rtl.Glyphs[0].Cluster; got != 5 {
		t.Errorf("in an RTL paragraph the first glyph has cluster %d, want the last Hebrew letter", got)
	}
}

// A newline crashes the line wrapper of the shaper, so Shape splits the text
// first. Clusters must still index the whole text.
func TestLinesAndClusters(t *testing.T) {
	f := raleway(t)
	lines := f.Shape("one\n\ntwo three", Options{Width: 2.5})
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want one, empty, two, three", len(lines))
	}
	if len(lines[1].Glyphs) != 0 {
		t.Errorf("the empty line has %d glyphs", len(lines[1].Glyphs))
	}
	if got := lines[2].Glyphs[0].Cluster; got != 5 {
		t.Errorf("two starts at cluster %d, want 5", got)
	}
	if got := lines[3].Glyphs[0].Cluster; got != 9 {
		t.Errorf("three starts at cluster %d, want 9", got)
	}
	for i, l := range lines {
		if l.Width > 2.5 {
			t.Errorf("line %d has width %v", i, l.Width)
		}
	}
}
