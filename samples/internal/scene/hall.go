package scene

import (
	"math"
	"slices"

	slug "github.com/dhannyell/go-slug"
	"github.com/dhannyell/go-slug/font"
	"golang.org/x/image/font/gofont/goregular"
)

// HallPeriod is the time of one walk along the hall and back. The camera is
// nearest to the cube at half of it.
const HallPeriod = 24.0

const (
	// nearDepth is the smallest depth of a drawn plane. The shader divides by the
	// w of a vertex, so a plane must not reach the camera plane.
	nearDepth = 60

	// edgeCos is the smallest cosine between a turning face and the camera. A plane
	// near its edge has a large pixel size in em space, so coverage leaks past its
	// corners along the edge.
	edgeCos = 0.03

	cubeHalf = 160.0
)

var (
	cubeCenter = vec3{0, 200, 1750}
	paper      = [4]float32{0.90, 0.86, 0.76, 1}
	blue       = [4]float32{0.13, 0.36, 0.62, 1}
)

// flat is a part of the hall that one matrix draws. Its items use plane
// coordinates, from the corner at origin: x along u and y along v, with y down
// when the plane is a wall. A front face has u x v toward the camera.
type flat struct {
	first, count int
	w, h         float64 // the size of the plane
	ground       bool    // nothing is under it, so it draws first
	at           func(t float64) (origin, u, v vec3)
}

// hall builds a corridor: lines of text on the floor, signs on the walls, and a
// turning cube at the end.
func (s *Scene) hall() error {
	f, err := font.Parse(goregular.TTF)
	if err != nil {
		return err
	}
	var flats []flat
	group := func(fl flat, add func() error) error {
		fl.first = len(s.Items)
		err := add()
		fl.count = len(s.Items) - fl.first
		flats = append(flats, fl)
		return err
	}

	floor := []string{
		"Slug reads the outline, not a bitmap.",
		"A low angle needs no mipmap level.",
		"The edge stays sharp near the camera.",
		"Each plane is a homography of the text.",
		"The floor goes on to the horizon.",
		"Letters get small, and stay letters.",
		"The coverage comes from the curves.",
		"This line is far away from the camera.",
		"You can read this line from the cube.",
	}
	for i, line := range floor {
		const size, depth = 60, 80
		z := 50 + float64(i)*340
		err := group(flat{w: 960, h: depth, ground: true, at: fixed(vec3{-480, 0, z + depth}, vec3{1, 0, 0}, vec3{0, 0, -1})}, func() error {
			return s.text(f, line, size, slug.Affine{1, 0, 0, 1, 0, size}, ink)
		})
		if err != nil {
			return err
		}
	}

	signs := []struct {
		word, caption, small string
		z                    float64
	}{
		{"Near", "One em can fill the image.", "The shader reads the curves in each pixel.", 250},
		{"Far", "One em can be a few pixels.", "Small text keeps its shape at this size.", 800},
		{"Turn", "A plane in perspective is exact.", "The two axes have their own derivative.", 1350},
	}
	const signW, signH = 300, 180
	for i, sign := range signs {
		for _, side := range []float64{-1, 1} {
			// From inside the hall, text on the left wall reads forward, and text on
			// the right wall reads back to the camera.
			corner, along := vec3{-600, 300, sign.z}, vec3{0, 0, 1}
			if side > 0 {
				corner, along = vec3{600, 300, sign.z + signW}, vec3{0, 0, -1}
			}
			c := paper
			if (i%2 == 0) == (side > 0) {
				c = [4]float32{0.82, 0.86, 0.90, 1}
			}
			err := group(flat{w: signW, h: signH, at: fixed(corner, along, vec3{0, -1, 0})}, func() error {
				return s.panel(f, sign.word, sign.caption, sign.small, signW, signH, c, ink, 72, 24)
			})
			if err != nil {
				return err
			}
		}
	}

	faces := []struct {
		yaw, pitch float64
		word       string
		c          [4]float32
	}{
		{0, 0, "Slug", accent},
		{math.Pi / 2, 0, "curves", blue},
		{math.Pi, 0, "bands", accent},
		{-math.Pi / 2, 0, "paths", blue},
		{0, math.Pi / 2, "glyphs", ink},
		{0, -math.Pi / 2, "layers", ink},
	}
	for _, face := range faces {
		yaw, pitch := face.yaw, face.pitch
		at := func(t float64) (origin, u, v vec3) {
			turn := func(p vec3) vec3 { return rotateY(rotateX(rotateY(rotateX(p, pitch), yaw), 0.35), 0.5*t) }
			return cubeCenter.add(turn(vec3{-cubeHalf, cubeHalf, -cubeHalf})), turn(vec3{1, 0, 0}), turn(vec3{0, -1, 0})
		}
		err := group(flat{w: 2 * cubeHalf, h: 2 * cubeHalf, at: at}, func() error {
			return s.panel(f, face.word, "The GPU reads the outline.", "A cube is six planes.", 2*cubeHalf, 2*cubeHalf, face.c, paper, 80, 22)
		})
		if err != nil {
			return err
		}
	}

	s.Animated = true
	s.frame = func(t float64, w, h int) []Span { return hallFrame(flats, t, w, h) }
	return nil
}

// panel adds an opaque rectangle with a word and two smaller lines over it.
func (s *Scene) panel(f *font.Font, word, caption, small string, w, h float32, back, fore [4]float32, wordSize, captionSize float32) error {
	rect := slug.Drawing{Em: 1, Layers: []slug.Layer{{Path: slug.Path{
		Verbs:  []slug.Verb{slug.MoveTo, slug.LineTo, slug.LineTo, slug.LineTo, slug.Close},
		Points: []slug.Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 1}, {X: 0, Y: 1}},
	}}}}
	if err := s.add(slug.Key{Source: sourceHall}, rect, slug.Affine{w, 0, 0, -h, 0, h}, back); err != nil {
		return err
	}
	y := h * 0.5
	if err := s.text(f, word, wordSize, slug.Affine{1, 0, 0, 1, 16, y}, fore); err != nil {
		return err
	}
	y += captionSize * 1.3
	if err := s.text(f, caption, captionSize*0.72, slug.Affine{1, 0, 0, 1, 16, y}, fore); err != nil {
		return err
	}
	y += captionSize
	return s.text(f, small, captionSize*0.5, slug.Affine{1, 0, 0, 1, 16, y}, fore)
}

func fixed(origin, u, v vec3) func(float64) (vec3, vec3, vec3) {
	return func(float64) (vec3, vec3, vec3) { return origin, u, v }
}

func rotateX(p vec3, a float64) vec3 {
	sin, cos := math.Sincos(a)
	return vec3{p[0], cos*p[1] - sin*p[2], sin*p[1] + cos*p[2]}
}

func rotateY(p vec3, a float64) vec3 {
	sin, cos := math.Sincos(a)
	return vec3{cos*p[0] + sin*p[2], p[1], -sin*p[0] + cos*p[2]}
}

// hallCamera walks along the hall, and looks at the cube. The nearest corner of
// the cube stays further than nearDepth.
func hallCamera(t float64, height int) camera {
	phase := 2 * math.Pi * t / HallPeriod
	walk := 0.5 - 0.5*math.Cos(phase)
	eye := vec3{80 * math.Sin(phase), 110 + 60*walk, -150 + 1530*walk}
	return lookAt(eye, cubeCenter, math.Pi/3, height)
}

// hallFrame returns the planes in the order from back to front. Blending needs
// that order: a depth buffer would let an antialiased edge hide what is behind it.
func hallFrame(flats []flat, t float64, w, h int) []Span {
	cam := hallCamera(t, h)
	type drawn struct {
		span     Span
		ground   bool
		distance float64
	}
	var planes []drawn
	for _, fl := range flats {
		origin, u, v := fl.at(t)
		center := origin.add(u.scale(fl.w / 2)).add(v.scale(fl.h / 2))
		facing := u.cross(v).dot(cam.eye.sub(center).unit())
		if facing <= 0 || (!fl.ground && facing < edgeCos) {
			continue
		}
		near := math.Inf(1)
		for _, c := range []vec3{origin, origin.add(u.scale(fl.w)), origin.add(v.scale(fl.h)), origin.add(u.scale(fl.w)).add(v.scale(fl.h))} {
			near = min(near, cam.depth(c))
		}
		if near < nearDepth {
			continue
		}
		d := center.sub(cam.eye)
		planes = append(planes, drawn{Span{fl.first, fl.count, cam.plane(origin, u, v, w, h)}, fl.ground, d.dot(d)})
	}
	slices.SortStableFunc(planes, func(a, b drawn) int {
		switch {
		case a.ground != b.ground:
			if a.ground {
				return -1
			}
			return 1
		case a.distance > b.distance:
			return -1
		case a.distance < b.distance:
			return 1
		}
		return 0
	})
	spans := make([]Span, len(planes))
	for i, p := range planes {
		spans[i] = p.span
	}
	return spans
}
