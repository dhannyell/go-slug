package scene

import slug "github.com/dhannyell/go-slug"

// Projection is a 3x3 matrix, by rows, from design pixels to target pixels. Its
// last row is the w of a perspective.
type Projection [9]float64

// FromAffine returns the projection of an affine matrix.
func FromAffine(m slug.Affine) Projection {
	return Projection{
		float64(m[0]), float64(m[2]), float64(m[4]),
		float64(m[1]), float64(m[3]), float64(m[5]),
		0, 0, 1,
	}
}

// Then returns the projection that applies p, and then q.
func (p Projection) Then(q Projection) Projection {
	var out Projection
	for r := range 3 {
		for c := range 3 {
			out[r*3+c] = q[r*3]*p[c] + q[r*3+1]*p[3+c] + q[r*3+2]*p[6+c]
		}
	}
	return out
}

// Moved returns p with its result moved by dx and dy, in target pixels.
func (p Projection) Moved(dx, dy float64) Projection {
	return p.Then(Projection{1, 0, dx, 0, 1, dy, 0, 0, 1})
}

// Apply returns the image of the point x, y.
func (p Projection) Apply(x, y float64) (float64, float64) {
	w := p[6]*x + p[7]*y + p[8]
	return (p[0]*x + p[1]*y + p[2]) / w, (p[3]*x + p[4]*y + p[5]) / w
}

// Invert returns the inverse of p. The caller must give a p with an inverse.
func (p Projection) Invert() Projection {
	a, b, c, d, e, f, g, h, i := p[0], p[1], p[2], p[3], p[4], p[5], p[6], p[7], p[8]
	det := a*(e*i-f*h) - b*(d*i-f*g) + c*(d*h-e*g)
	return Projection{
		(e*i - f*h) / det, (c*h - b*i) / det, (b*f - c*e) / det,
		(f*g - d*i) / det, (a*i - c*g) / det, (c*d - a*f) / det,
		(d*h - e*g) / det, (b*g - a*h) / det, (a*e - b*d) / det,
	}
}

// IsAffine reports whether p has no perspective.
func (p Projection) IsAffine() bool { return p[6] == 0 && p[7] == 0 && p[8] == 1 }

// Tangent returns the affine matrix that equals p, to the first order, at the
// point x, y. An affine p has one tangent for all points.
func (p Projection) Tangent(x, y float64) slug.Affine {
	w := p[6]*x + p[7]*y + p[8]
	u, v := p.Apply(x, y)
	a, c := (p[0]-u*p[6])/w, (p[1]-u*p[7])/w
	b, d := (p[3]-v*p[6])/w, (p[4]-v*p[7])/w
	return slug.Affine{float32(a), float32(b), float32(c), float32(d), float32(u - a*x - c*y), float32(v - b*x - d*y)}
}
