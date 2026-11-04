package text

import (
	"slices"
	"testing"

	"github.com/go-text/typesetting-utils/opentype"

	slug "github.com/dhannyell/go-slug"
)

func raleway(t testing.TB) *Font { return load(t, "common/Raleway-v4020-Regular.otf") }

func load(t testing.TB, name string) *Font {
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
	if line.Glyphs[0].ID != x {
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
	if _, err := f.outlines.Glyph(line.Glyphs[1].ID); err != nil {
		t.Error(err)
	}
}

// An empty line has no run to read, so its height must come from the font. A wrong
// value would move all the lines after it.
func TestEmptyLineHasTheHeightOfTheFont(t *testing.T) {
	f := raleway(t)
	lines := f.Shape("one\n\ntwo", Options{})
	if lines[0].Height() <= 0 {
		t.Fatalf("the height is %v", lines[0].Height())
	}
	if lines[1].Metrics != lines[0].Metrics {
		t.Errorf("the empty line has %+v, and the line with text has %+v", lines[1].Metrics, lines[0].Metrics)
	}
	if got := f.Metrics(); got != lines[0].Metrics {
		t.Errorf("the font has %+v, and the line has %+v", got, lines[0].Metrics)
	}
}

// A caret goes to an X, and a pointer at this X must return a rune with a caret at
// the same X. Where two directions meet, two runes share one X, so the rune can differ.
func TestCaretAndPointerAgree(t *testing.T) {
	for _, c := range []struct {
		name, font, text string
		o                Options
	}{
		{"ligature", "common/Raleway-v4020-Regular.otf", "xffi", Options{}},
		{"mixed", "common/DejaVuSans.ttf", "ab אבג", Options{}},
		{"rtl paragraph", "common/DejaVuSans.ttf", "ab אבג", Options{RTL: true}},
		{"aligned", "common/DejaVuSans.ttf", "ab אבג", Options{Width: 20, Align: AlignCenter}},
	} {
		t.Run(c.name, func(t *testing.T) {
			line := load(t, c.font).Shape(c.text, c.o)[0]
			// The loop compares only X, so a wrong X at End agrees with itself.
			if got := line.ClusterAt(line.XOf(line.End)); got != line.End {
				t.Errorf("the caret at End returns rune %d, want %d", got, line.End)
			}
			for i := line.Start; i <= line.End; i++ {
				x := line.XOf(i)
				if got := line.ClusterAt(x); line.XOf(got) != x {
					t.Errorf("rune %d has its caret at %v, and this X returns rune %d at %v", i, x, got, line.XOf(got))
				}
			}
		})
	}
}

// A caret inside a grapheme splits a letter from its accent, or an emoji into parts.
// The font composes the accent into one glyph, and has no glyph for the emoji, so the
// grapheme is one cluster in the first case and can be many in the second.
func TestCaretDoesNotStopInAGrapheme(t *testing.T) {
	f := load(t, "common/DejaVuSans.ttf")
	for _, c := range []struct {
		name, text string
		stops      []int
	}{
		{"accent", "aéb", []int{0, 1, 3, 4}},
		{"emoji", "a\U0001F468‍\U0001F469‍\U0001F467b", []int{0, 1, 6, 7}},
	} {
		t.Run(c.name, func(t *testing.T) {
			line := f.Shape(c.text, Options{})[0]
			for i := c.stops[1]; i < c.stops[2]; i++ {
				if got, want := line.XOf(i), line.XOf(c.stops[1]); got != want {
					t.Errorf("rune %d has its caret at %v, want the grapheme start at %v", i, got, want)
				}
			}
			for i := range 200 {
				x := -0.5 + (line.Width+1)*float32(i)/199
				if got := line.ClusterAt(x); !slices.Contains(c.stops, got) {
					t.Fatalf("X %v returns rune %d, want one of %v", x, got, c.stops)
				}
			}
			if !(line.XOf(c.stops[1]) < line.XOf(c.stops[2])) {
				t.Errorf("the grapheme has carets at %v and %v", line.XOf(c.stops[1]), line.XOf(c.stops[2]))
			}
		})
	}
}

// The carets of the font replace the equal division. These two values differ from the
// middle of the glyph, so a test on the middle cannot pass here.
func TestCaretUsesTheCaretsOfTheFont(t *testing.T) {
	for _, c := range []struct {
		name, font, text string
		caret            float32
		o                Options
	}{
		{"LTR", "toys/GDEFCaretList3.ttf", "fi", 679.0 / 2048, Options{}},
		{"RTL", "common/NotoSansArabic.ttf", "لا", 0.328, Options{RTL: true}},
		{"aligned", "toys/GDEFCaretList3.ttf", "fi", 679.0 / 2048, Options{Width: 20, Align: AlignEnd}},
	} {
		t.Run(c.name, func(t *testing.T) {
			line := load(t, c.font).Shape(c.text, c.o)[0]
			if len(line.Glyphs) != 1 {
				t.Fatalf("got %d glyphs, want one ligature", len(line.Glyphs))
			}
			g := line.Glyphs[0]
			if got, want := line.XOf(1), g.X+c.caret; got != want {
				t.Errorf("the caret is at %v, want %v", got, want)
			}
			if got := line.ClusterAt(line.XOf(1)); got != 1 {
				t.Errorf("the caret X returns rune %d, want 1", got)
			}
		})
	}
}

// The table lists carets from left to right, and RTL text reads them in reverse. A
// count that differs from the graphemes must not shift the text to the wrong caret.
func TestStopXReadsTheCaretsInTextOrder(t *testing.T) {
	carets := []float32{0.2, 0.7}
	for _, c := range []struct {
		name          string
		cluster       cluster
		count         int
		first, second float32
	}{
		{"LTR", cluster{right: 1, carets: carets}, 3, 0.2, 0.7},
		{"RTL", cluster{right: 1, rtl: true, carets: carets}, 3, 0.7, 0.2},
		{"count differs", cluster{right: 1, carets: carets}, 4, 0.25, 0.5},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cluster.stopX(1, c.count); got != c.first {
				t.Errorf("caret 1 is at %v, want %v", got, c.first)
			}
			if got := c.cluster.stopX(2, c.count); got != c.second {
				t.Errorf("caret 2 is at %v, want %v", got, c.second)
			}
		})
	}
}

// A caret inside a ligature must fall inside its glyph, and move with the text.
func TestCaretDividesALigature(t *testing.T) {
	line := raleway(t).Shape("xffi", Options{})[0]
	g := line.Glyphs[1]
	if g.Runes != 3 {
		t.Fatalf("the ligature has %d runes, want 3", g.Runes)
	}
	a, b := line.XOf(2), line.XOf(3)
	if !(g.X < a && a < b && b < g.X+g.Advance) {
		t.Errorf("carets at %v and %v, in a glyph from %v to %v", a, b, g.X, g.X+g.Advance)
	}
}

func TestAlignMovesTheLine(t *testing.T) {
	f := raleway(t)
	const box = 20
	plain := f.Shape("one two", Options{})[0]
	for _, c := range []struct {
		name string
		o    Options
		want float32
	}{
		{"start", Options{Width: box}, 0},
		{"center", Options{Width: box, Align: AlignCenter}, (box - plain.Width) / 2},
		{"end", Options{Width: box, Align: AlignEnd}, box - plain.Width},
		{"start of RTL", Options{Width: box, RTL: true}, box - plain.Width},
		{"end of RTL", Options{Width: box, RTL: true, Align: AlignEnd}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			line := f.Shape("one two", c.o)[0]
			if line.Left != c.want || line.Glyphs[0].X != c.want+plain.Glyphs[0].X {
				t.Errorf("Left is %v and the first glyph is at %v, want %v", line.Left, line.Glyphs[0].X, c.want)
			}
		})
	}

	// Without a width, the lines align in the widest line.
	lines := f.Shape("one two\none", Options{Align: AlignEnd})
	if want := lines[0].Width - lines[1].Width; lines[1].Left != want {
		t.Errorf("the short line starts at %v, want %v", lines[1].Left, want)
	}
}

// The clusters of an ellipsis index the ellipsis. They must change to the removed
// text, or a pointer on the ellipsis selects the start of the text.
func TestMaxLinesAddsAnEllipsis(t *testing.T) {
	f := raleway(t)
	ellipsis, err := f.outlines.GlyphIndex('…')
	if err != nil {
		t.Fatal(err)
	}
	const s = "one two three four five"

	lines := f.Shape(s, Options{Width: 5, MaxLines: 2, Ellipsis: "…"})
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	last := lines[1].Glyphs[len(lines[1].Glyphs)-1]
	if last.ID != ellipsis {
		t.Fatalf("the last glyph is %d, want the ellipsis %d", last.ID, ellipsis)
	}
	if last.Cluster <= lines[1].Start || last.Runes == 0 {
		t.Errorf("the ellipsis has cluster %d and %d runes, on a line from %d", last.Cluster, last.Runes, lines[1].Start)
	}
	if lines[1].End != len([]rune(s)) {
		t.Errorf("the line ends at %d, want the end of the text", lines[1].End)
	}
	if lines[1].Width > 5 {
		t.Errorf("the line has width %v", lines[1].Width)
	}

	// A later paragraph is also removed text.
	lines = f.Shape("one\n\ntwo", Options{MaxLines: 2, Ellipsis: "…"})
	if len(lines) != 2 || len(lines[1].Glyphs) != 1 || lines[1].Glyphs[0].ID != ellipsis {
		t.Errorf("got lines %+v, want one, then the ellipsis", lines)
	}

	// All the text fits, so no ellipsis appears.
	lines = f.Shape("one", Options{Width: 5, MaxLines: 2, Ellipsis: "…"})
	if got := lines[0].Glyphs[len(lines[0].Glyphs)-1].ID; len(lines) != 1 || got == ellipsis {
		t.Errorf("got %d lines that end in glyph %d", len(lines), got)
	}

	// No Ellipsis cuts the text without a mark.
	lines = f.Shape(s, Options{Width: 5, MaxLines: 1})
	if got := lines[0].Glyphs[len(lines[0].Glyphs)-1].ID; len(lines) != 1 || got == ellipsis {
		t.Errorf("got %d lines that end in glyph %d", len(lines), got)
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

// A hit must give the result of Shape, without a second layout.
func TestShapeCacheReturnsTheSameLines(t *testing.T) {
	f := raleway(t)
	c := NewShapeCache(8)
	first := c.Shape(f, "office", Options{})
	if want := f.Shape("office", Options{}); len(first) != 1 || !slices.Equal(first[0].Glyphs, want[0].Glyphs) {
		t.Fatalf("lines: got %v, want %v", first, want)
	}
	if second := c.Shape(f, "office", Options{}); &second[0] != &first[0] {
		t.Error("the second call did not use the cache")
	}
}

// A key without the font or the options gives the layout of other text, with no error.
func TestShapeCacheKeyHasFontAndOptions(t *testing.T) {
	f := raleway(t)
	c := NewShapeCache(8)
	first := c.Shape(f, "office", Options{})
	if other := c.Shape(f, "office", Options{RTL: true}); &other[0] == &first[0] {
		t.Error("other options used the same result")
	}
	if other := c.Shape(raleway(t), "office", Options{}); &other[0] == &first[0] {
		t.Error("another font used the same result")
	}
}

// A used result survives a rotation, and an unused result does not.
func TestShapeCacheDropsOldEntries(t *testing.T) {
	f := raleway(t)
	c := NewShapeCache(4) // two results in each generation
	used := c.Shape(f, "used", Options{})
	unused := c.Shape(f, "unused", Options{})
	c.Shape(f, "first rotation", Options{})
	if again := c.Shape(f, "used", Options{}); &again[0] != &used[0] {
		t.Fatal("the first rotation dropped a result")
	}
	c.Shape(f, "second rotation", Options{})
	if again := c.Shape(f, "used", Options{}); &again[0] != &used[0] {
		t.Error("the second rotation dropped a used result")
	}
	if again := c.Shape(f, "unused", Options{}); &again[0] == &unused[0] {
		t.Error("the second rotation kept an unused result")
	}
}

// A frame reads the cache one time for each label.
func TestShapeCacheHitDoesNotAllocate(t *testing.T) {
	f := raleway(t)
	c := NewShapeCache(8)
	c.Shape(f, "office", Options{})
	if allocs := testing.AllocsPerRun(100, func() { c.Shape(f, "office", Options{}) }); allocs != 0 {
		t.Errorf("allocs: %v", allocs)
	}
}

func BenchmarkShapeCache(b *testing.B) {
	f := raleway(b)
	const label = "Hit points: 1234 of 5678"
	b.Run("no-cache", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			f.Shape(label, Options{})
		}
	})
	b.Run("hit", func(b *testing.B) {
		c := NewShapeCache(8)
		c.Shape(f, label, Options{})
		b.ReportAllocs()
		for range b.N {
			c.Shape(f, label, Options{})
		}
	})
}
