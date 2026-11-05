package text

import (
	"encoding/binary"
	"math"
)

// ShapeCache keeps the results of Font.Shape. A program that draws the same text
// in each frame shapes that text one time. It is not safe for concurrent use.
type ShapeCache struct {
	limit         int // the entries of one generation
	current, last map[shapeKey][]Line

	families map[*Family]uint32 // the numbers that a key of spans uses for its families
	key      []byte             // the buffer of a key of spans
}

// shapeKey identifies one result.
type shapeKey struct {
	font   *Font
	family *Family
	text   string // the text, or the encoded spans if family is set
	opt    Options
}

// NewShapeCache returns a cache that holds at most maxEntries results. It panics if
// maxEntries is less than 2. A value below the count of labels of a frame shapes
// them again in each frame. The limit counts results and not bytes, so long
// paragraphs use more memory than short labels.
func NewShapeCache(maxEntries int) *ShapeCache {
	if maxEntries < 2 {
		panic("text: NewShapeCache needs at least 2 entries")
	}
	return &ShapeCache{
		limit:   maxEntries / 2,
		current: map[shapeKey][]Line{},
		last:    map[shapeKey][]Line{},
	}
}

// Shape returns the same lines as f.Shape(s, o). The cache and all of its callers
// share the result, so the caller must not change it.
func (c *ShapeCache) Shape(f *Font, s string, o Options) []Line {
	key := shapeKey{font: f, text: s, opt: o}
	if lines, ok := c.current[key]; ok {
		return lines
	}
	lines, ok := c.last[key]
	if !ok {
		lines = f.Shape(s, o)
	}
	if len(c.current) >= c.limit {
		c.rotate()
	}
	c.current[key] = lines
	return lines
}

// ShapeSpans returns the same lines as fam.ShapeSpans(spans, o), under the same rules
// as Shape. The cache keeps a reference to each Family of a Span.
func (c *ShapeCache) ShapeSpans(fam *Family, spans []Span, o Options) []Line {
	c.key = c.encode(c.key[:0], spans)
	// The conversions in these two lookups do not allocate.
	if lines, ok := c.current[shapeKey{family: fam, text: string(c.key), opt: o}]; ok {
		return lines
	}
	lines, ok := c.last[shapeKey{family: fam, text: string(c.key), opt: o}]
	if !ok {
		lines = fam.ShapeSpans(spans, o)
	}
	if len(c.current) >= c.limit {
		c.rotate()
	}
	c.current[shapeKey{family: fam, text: string(c.key), opt: o}] = lines
	return lines
}

// encode appends a value that is unique for spans. The length of each text keeps
// "ab"+"c" apart from "a"+"bc".
func (c *ShapeCache) encode(key []byte, spans []Span) []byte {
	for _, s := range spans {
		key = binary.AppendUvarint(key, uint64(len(s.Text)))
		key = append(key, s.Text...)
		key = binary.AppendUvarint(key, uint64(c.number(s.Family)))
		key = binary.AppendUvarint(key, uint64(math.Float32bits(s.Scale)))
	}
	return key
}

// number returns a number for fam. nil is 0.
func (c *ShapeCache) number(fam *Family) uint32 {
	if fam == nil {
		return 0
	}
	n, ok := c.families[fam]
	if !ok {
		if c.families == nil {
			c.families = map[*Family]uint32{}
		}
		n = uint32(len(c.families) + 1)
		c.families[fam] = n
	}
	return n
}

// rotate drops the results that no call used since the last rotation.
func (c *ShapeCache) rotate() {
	c.last = c.current
	c.current = make(map[shapeKey][]Line, c.limit)
}
