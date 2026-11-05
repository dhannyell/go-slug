// Package slug prepares vector outlines for GPU rendering with the Slug
// algorithm.
//
// Coordinates use a Y-up convention. Units are free: font units for glyphs,
// viewBox units for icons.
package slug

import "math"

// Point is a position in drawing units.
type Point struct{ X, Y float32 }

// Rect is an axis-aligned box. Min is the lower-left corner.
type Rect struct{ Min, Max Point }

// Affine is a 2x3 matrix {a, b, c, d, e, f}:
//
//	x' = a*x + c*y + e
//	y' = b*x + d*y + f
//
// A zero Affine is the zero matrix: it maps every point to the origin.
type Affine [6]float32

// Identity returns the identity matrix.
func Identity() Affine {
	return Affine{1, 0, 0, 1, 0, 0}
}

// Apply transforms p.
func (m Affine) Apply(p Point) Point {
	return Point{
		X: m[0]*p.X + m[2]*p.Y + m[4],
		Y: m[1]*p.X + m[3]*p.Y + m[5],
	}
}

// Then returns a matrix that applies m first and next second.
func (m Affine) Then(next Affine) Affine {
	a1, b1, c1, d1, e1, f1 := m.unpack()
	a2, b2, c2, d2, e2, f2 := next.unpack()

	// next × m: m is on the right because it applies first.
	return Affine{
		a2*a1 + c2*b1,
		b2*a1 + d2*b1,
		a2*c1 + c2*d1,
		b2*c1 + d2*d1,
		a2*e1 + c2*f1 + e2,
		b2*e1 + d2*f1 + f2,
	}
}

// Invert returns the inverse of m. It returns false when the inverse is not
// finite, which includes a singular m.
func (m Affine) Invert() (Affine, bool) {
	// float64: the determinant of a small scale underflows float32.
	a, b, c, d := float64(m[0]), float64(m[1]), float64(m[2]), float64(m[3])
	e, f := float64(m[4]), float64(m[5])
	det := a*d - b*c
	inv := Affine{
		float32(d / det),
		float32(-b / det),
		float32(-c / det),
		float32(a / det),
		float32((c*f - d*e) / det),
		float32((b*e - a*f) / det),
	}
	for _, v := range inv {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return Affine{}, false
		}
	}
	return inv, true
}

func (m Affine) unpack() (a, b, c, d, e, f float32) {
	return m[0], m[1], m[2], m[3], m[4], m[5]
}

// Verb is one path command.
type Verb uint8

const (
	MoveTo Verb = iota // 1 point
	LineTo             // 1 point
	QuadTo             // 2 points: control, end
	CubeTo             // 3 points: control 1, control 2, end
	Close              // 0 points
)

// PointCount returns how many points v reads from Path.Points, or -1 for an
// unknown verb.
func (v Verb) PointCount() int {
	switch v {
	case MoveTo, LineTo:
		return 1
	case QuadTo:
		return 2
	case CubeTo:
		return 3
	case Close:
		return 0
	default:
		return -1
	}
}

// FillRule selects which areas a path fills.
type FillRule uint8

const (
	NonZero FillRule = iota // the default; TrueType and CFF use it
	EvenOdd
)

// Path is a sequence of contours. Each verb reads its points
// from Points in order.
//
// Rules for every producer; normalization rejects a path that breaks them:
//   - Each contour starts with MoveTo. After Close, the next drawing verb
//     needs a new MoveTo.
//   - A contour without Close is closed implicitly with a line back to its
//     MoveTo point. Filling needs closed contours.
//   - A MoveTo directly after another MoveTo starts an empty contour, which
//     draws nothing.
//   - len(Points) equals the sum of PointCount over Verbs. An unknown verb
//     is an error.
type Path struct {
	Verbs  []Verb
	Points []Point
	Rule   FillRule
}

// PaintKind selects how a layer gets its color.
type PaintKind uint8

const (
	CurrentColor   PaintKind = iota // the host supplies the color
	Solid                           // Paint.Color
	LinearGradient                  // Paint.Gradient: along a line
	RadialGradient                  // Paint.Gradient: between two circles
	SweepGradient                   // Paint.Gradient: around a point
)

// Paint is the color of a layer.
type Paint struct {
	Kind PaintKind
	// Color is straight (not premultiplied) RGB in sRGB; alpha is linear, 0 to 1.
	// Only Solid reads it.
	Color [4]float32
	// Gradient is the gradient of a gradient kind.
	Gradient *Gradient
}

// Spread selects the color outside the range of a gradient.
type Spread uint8

const (
	Pad     Spread = iota // the color of the nearest end
	Repeat                // the range 0 to 1 again
	Reflect               // the range 0 to 1 again, in the opposite direction at each step
)

// Stop is one color of a gradient.
type Stop struct {
	// Offset is the position along the gradient. Each stop must have an offset of at
	// least the offset of the previous stop. Two equal offsets make a hard edge.
	Offset float32
	// Color is like Paint.Color.
	Color [4]float32
}

// ColorSpace is the space in which the stops of a gradient mix.
type ColorSpace uint8

const (
	SRGB       ColorSpace = iota // the default; SVG and CSS mix here
	LinearSRGB                   // linear light; COLR fonts mix here
)

// Gradient is a color that changes over a layer. Use LinearPaint, RadialPaint or
// SweepPaint to make one. A gradient without a direction, such as a
// line of length 0, draws the color of its last stop.
type Gradient struct {
	Stops  []Stop
	Spread Spread
	// Premultiplied mixes the stops with RGB multiplied by alpha, so a transparent
	// stop gives no color to its neighbor.
	Premultiplied bool
	// Space is the space in which the stops mix. The result is sRGB in both spaces.
	Space ColorSpace
	// Transform maps the space of the fields below to drawing units, before
	// Layer.Transform. nil means identity.
	Transform *Affine

	P0, P1     Point   // linear: the line; radial: the centers; sweep: P0 is the center
	R0, R1     float32 // radial: the radii at offset 0 and offset 1
	Start, End float32 // sweep: the angles at offset 0 and offset 1, in radians, from +X to +Y
}

// LinearPaint returns a gradient from p0, at offset 0, to p1, at offset 1.
func LinearPaint(p0, p1 Point, stops ...Stop) Paint {
	return Paint{Kind: LinearGradient, Gradient: &Gradient{Stops: stops, P0: p0, P1: p1}}
}

// RadialPaint returns a gradient from one circle, at offset 0, to another circle, at
// offset 1. Outside the circles of a cone, the layer is transparent.
func RadialPaint(c0 Point, r0 float32, c1 Point, r1 float32, stops ...Stop) Paint {
	return Paint{Kind: RadialGradient, Gradient: &Gradient{Stops: stops, P0: c0, P1: c1, R0: r0, R1: r1}}
}

// SweepPaint returns a gradient around center, from the angle start, at offset 0, to
// the angle end, at offset 1.
func SweepPaint(center Point, start, end float32, stops ...Stop) Paint {
	return Paint{Kind: SweepGradient, Gradient: &Gradient{Stops: stops, P0: center, Start: start, End: end}}
}

// Layer is one filled path with its paint.
type Layer struct {
	Path      Path
	Paint     Paint
	Transform *Affine // nil means identity
}

// Drawing is one complete shape: a glyph, an icon or an emoji.
// Layers draw in order; later layers go on top.
type Drawing struct {
	Layers []Layer
	// Em is the design size in drawing units: units per em for a glyph, the
	// viewBox size for an icon. Pack.Add divides coordinates by it.
	Em float32
}
