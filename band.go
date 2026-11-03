package slug

import (
	"cmp"
	"math"
	"slices"
)

// maxBands limits the bands on one axis.
const maxBands = 16

// band lists indices into the curves of a shape, in descending key order.
type band []uint32

// bandSet is the acceleration grid of one shape.
type bandSet struct {
	H []band // cut along Y; used by rays along X
	V []band // cut along X; used by rays along Y
}

// span is one curve as seen by one axis.
type span struct {
	lo, hi float32 // extent on the axis that the bands cut
	key    float32 // max control coordinate on the axis of the ray
	index  uint32
}

// buildBands makes the bands of curves. A curve also enters every band
// within overlap of its extent.
func buildBands(curves []Curve, bounds Rect, overlap float32) bandSet {
	h, v := axisSpans(curves)
	return bandSet{
		H: fillBands(h, bounds.Min.Y, bounds.Max.Y, overlap),
		V: fillBands(v, bounds.Min.X, bounds.Max.X, overlap),
	}
}

// axisSpans returns the spans for horizontal and vertical bands. A curve that
// is flat on an axis has no span there: a ray parallel to it never crosses it.
func axisSpans(curves []Curve) (h, v []span) {
	for i, c := range curves {
		b := c.Bounds()
		if b.Min.Y != b.Max.Y {
			h = append(h, span{b.Min.Y, b.Max.Y, max(c.P0.X, c.P1.X, c.P2.X), uint32(i)})
		}
		if b.Min.X != b.Max.X {
			v = append(v, span{b.Min.X, b.Max.X, max(c.P0.Y, c.P1.Y, c.P2.Y), uint32(i)})
		}
	}
	return h, v
}

// fillBands sorts spans and distributes them into bands. The sort carries into
// every band, and its stability keeps the output the same on every run.
func fillBands(spans []span, lo, hi, overlap float32) []band {
	slices.SortStableFunc(spans, func(a, b span) int {
		return cmp.Compare(b.key, a.key)
	})

	n := bandCount(spans, lo, hi, overlap)
	bands := make([]band, n)
	for _, s := range spans {
		first, last := bandRange(s, lo, hi, overlap, n)
		for b := range last - first + 1 {
			bands[first+b] = append(bands[first+b], s.index)
		}
	}
	return bands
}

// bandCount returns the n that gives the smallest maximum of curves in one
// band; the shader cost follows the fullest band. A tie selects the smaller n.
func bandCount(spans []span, lo, hi, overlap float32) int {
	bestN, bestWorst := 1, len(spans)
	for i := range maxBands {
		n := i + 1
		var counts [maxBands]int
		worst := 0
		for _, s := range spans {
			first, last := bandRange(s, lo, hi, overlap, n)
			for b := range last - first + 1 {
				counts[first+b]++
				worst = max(worst, counts[first+b])
			}
		}
		if worst < bestWorst {
			bestN, bestWorst = n, worst
		}
	}
	return bestN
}

// bandRange returns the first and last band that s touches.
func bandRange(s span, lo, hi, overlap float32, n int) (first, last int) {
	return bandIndex(s.lo-overlap, lo, hi, n), bandIndex(s.hi+overlap, lo, hi, n)
}

// bandIndex returns the band that holds v. It uses float64 because hi-lo can
// overflow float32.
func bandIndex(v, lo, hi float32, n int) int {
	t := (float64(v) - float64(lo)) / (float64(hi) - float64(lo))
	return min(max(int(math.Floor(t*float64(n))), 0), n-1)
}
