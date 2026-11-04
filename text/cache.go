package text

// ShapeCache keeps the results of Font.Shape. A program that draws the same text
// in each frame shapes that text one time. It is not safe for concurrent use.
type ShapeCache struct {
	limit         int // the entries of one generation
	current, last map[shapeKey][]Line
}

// shapeKey identifies one result.
type shapeKey struct {
	font *Font
	text string
	opt  Options
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

// rotate drops the results that no call used since the last rotation.
func (c *ShapeCache) rotate() {
	c.last = c.current
	c.current = make(map[shapeKey][]Line, c.limit)
}
