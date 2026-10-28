// Package slug prepares vector outlines for GPU rendering with the Slug
// algorithm.
//
// Coordinates use a Y-up convention. Units are free: font units for glyphs,
// viewBox units for icons.
package slug

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
	CurrentColor PaintKind = iota // the host supplies the color
	Solid                         // Paint.Color
)

// Paint is the color of a layer.
type Paint struct {
	Kind PaintKind
	// Color is straight (not premultiplied) RGB in sRGB; alpha is linear, 0 to 1.
	// Only Solid reads it.
	Color [4]float32
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
}
