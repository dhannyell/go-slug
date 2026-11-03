package scene

import "math"

// vec3 is a point or a direction in the world: x to the right, y up, z forward.
type vec3 [3]float64

func (a vec3) add(b vec3) vec3      { return vec3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func (a vec3) sub(b vec3) vec3      { return vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func (a vec3) scale(k float64) vec3 { return vec3{a[0] * k, a[1] * k, a[2] * k} }
func (a vec3) dot(b vec3) float64   { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func (a vec3) unit() vec3           { return a.scale(1 / math.Sqrt(a.dot(a))) }

func (a vec3) cross(b vec3) vec3 {
	return vec3{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

// camera is a pinhole camera. focal is in target pixels.
type camera struct {
	eye, right, up, forward vec3
	focal                   float64
}

// lookAt returns a camera at eye that looks at target. fov is the vertical angle
// of a target of the given height, in radians.
func lookAt(eye, target vec3, fov float64, height int) camera {
	forward := target.sub(eye).unit()
	right := vec3{0, 1, 0}.cross(forward).unit()
	return camera{
		eye: eye, right: right, up: forward.cross(right), forward: forward,
		focal: float64(height) / 2 / math.Tan(fov/2),
	}
}

// depth returns the distance from the camera plane to p. It is the w of p.
func (c camera) depth(p vec3) float64 { return p.sub(c.eye).dot(c.forward) }

// plane returns the projection of the points origin + x*u + y*v. A plane through
// a pinhole camera is a homography, so a 3x3 matrix is exact.
func (c camera) plane(origin, u, v vec3, w, h int) Projection {
	cx, cy := float64(w)/2, float64(h)/2
	column := func(a vec3) [3]float64 {
		depth := a.dot(c.forward)
		return [3]float64{c.focal*a.dot(c.right) + cx*depth, -c.focal*a.dot(c.up) + cy*depth, depth}
	}
	x, y, o := column(u), column(v), column(origin.sub(c.eye))
	return Projection{x[0], y[0], o[0], x[1], y[1], o[1], x[2], y[2], o[2]}
}
