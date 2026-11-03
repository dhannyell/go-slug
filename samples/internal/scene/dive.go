package scene

import (
	"math"

	slug "github.com/dhannyell/go-slug"
	"github.com/dhannyell/go-slug/font"
	"golang.org/x/image/font/gofont/goregular"
)

// DivePeriod is the time of one dive and back. The camera is nearest to the word
// at half of it.
const DivePeriod = 20.0

// dive lays one large word on the floor. The camera goes from a view of the whole
// word to one corner of a letter, at a low angle.
func (s *Scene) dive() error {
	f, err := font.Parse(goregular.TTF)
	if err != nil {
		return err
	}
	const width, depth = 2600.0, 1450.0
	if err := s.text(f, "Curves stay curves at any distance from the camera.", 92, slug.Affine{1, 0, 0, 1, 40, 160}, accent); err != nil {
		return err
	}
	first := len(s.Items)
	if err := s.text(f, "SLUG", 1000, slug.Affine{1, 0, 0, 1, 40, depth - 100}, ink); err != nil {
		return err
	}
	// The foot of the L is on the baseline, so nothing is nearer to the camera.
	l := s.Items[first+1]
	foot := l.Place.Apply(slug.Point{X: l.Entry.Bounds.Max.X, Y: l.Entry.Bounds.Min.Y})

	origin, u, v := vec3{-width / 2, 0, depth}, vec3{1, 0, 0}, vec3{0, 0, -1}
	target := origin.add(u.scale(float64(foot.X))).add(v.scale(float64(foot.Y)))
	center := origin.add(u.scale(width / 2)).add(v.scale(depth / 2))

	s.Animated, s.Versus = true, true
	s.frame = func(t float64, w, h int) []Span {
		return []Span{{0, len(s.Items), diveCamera(t, h, center, target).plane(origin, u, v, w, h)}}
	}
	return nil
}

// diveCamera moves the eye with a constant rate of zoom, as the zoom scene does.
// At the peak, the eye is in front of the baseline, so no glyph reaches the camera
// plane.
func diveCamera(t float64, height int, center, target vec3) camera {
	far, near := vec3{420, 1900, -1500}, vec3{36, 70, -210}
	walk := 0.5 - 0.5*math.Cos(2*math.Pi*t/DivePeriod)
	var offset vec3
	for i := range offset {
		offset[i] = far[i] * math.Pow(near[i]/far[i], walk)
	}
	look := center.add(target.sub(center).scale(walk))
	return lookAt(target.add(offset), look, math.Pi/3, height)
}
