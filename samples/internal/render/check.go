//go:build cgo || js

package render

import (
	"fmt"
	"image"

	"github.com/dhannyell/webgpu/wgpu"

	"github.com/dhannyell/go-slug/samples/internal/scene"
)

// Tolerance is the largest difference between the GPU and the CPU, in steps of
// a channel, that counts as equal. The two sides differ by rounding, and by the
// fused multiply-add that a GPU can use.
const Tolerance = 6

// noise is the distance in pixels that the interpolation of a GPU can move a
// sample, and noiseStep is the step of the search inside it. Slug coverage jumps
// where the line of an edge passes a corner, because a ray sees a curve only inside
// the span of the curve. A sample near that line can go to either side, and the
// coverage changes quickly next to the jump, so a few distances can miss the value.
const (
	noise       = 1.0 / 512
	noiseStep   = 1.0 / 65536
	searchLimit = 64
)

// Diff is the result of a comparison of the two renderers.
type Diff struct {
	Max    int         // the largest difference of a channel from the oracle
	Pixels int         // the pixels over Tolerance
	Total  int         // the pixels compared
	Worst  image.Point // the pixel of Max
}

func (d Diff) String() string {
	return fmt.Sprintf("max difference %d/255 at %v, %d of %d pixels over %d", d.Max, d.Worst, d.Pixels, d.Total, Tolerance)
}

// Check draws s through view with the GPU and with the CPU, and compares the two
// images.
func Check(device *wgpu.Device, s *scene.Scene, view scene.Projection, w, h int) (Diff, error) {
	d, _, _, _, err := check(device, s, view, w, h)
	return d, err
}

// check returns the images of Check, and a mask of the pixels over Tolerance.
func check(device *wgpu.Device, s *scene.Scene, view scene.Projection, w, h int) (d Diff, got, want *image.RGBA, over []bool, err error) {
	got, err = Capture(device, s, view, w, h)
	if err != nil {
		return Diff{}, nil, nil, nil, err
	}
	want, err = s.Rasterize(w, h, view)
	if err != nil {
		return Diff{}, nil, nil, nil, err
	}

	d = Diff{Total: w * h}
	over = make([]bool, w*h)
	for i := 0; i < len(want.Pix); i += 4 {
		x, y := i/4%w, i/4/w
		v := [4]uint8(got.Pix[i : i+4])
		first := [4]uint8(want.Pix[i : i+4])
		worst := distance(v, first, first)
		// A broken shader fails in many pixels, and the search of a failed pixel
		// is slow. After searchLimit failures, the first image is the oracle.
		if worst > Tolerance && d.Pixels < searchLimit {
			if worst, err = search(s, view, x, y, v, first); err != nil {
				return Diff{}, nil, nil, nil, err
			}
		}
		if worst > d.Max {
			d.Max, d.Worst = worst, image.Pt(x, y)
		}
		if worst > Tolerance {
			d.Pixels++
			over[i/4] = true
		}
	}
	return d, got, want, over, nil
}

// search moves the view around one pixel, and returns the distance from v to the
// range of the colors that the CPU draws. It stops when the range reaches v.
func search(s *scene.Scene, view scene.Projection, x, y int, v, first [4]uint8) (int, error) {
	items := s.Near(view, x, y)
	lo, hi := first, first
	worst := distance(v, lo, hi)
	for _, o := range [][2]float64{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {-1, -1}, {1, -1}, {-1, 1}} {
		for n := noiseStep; n <= noise; n += noiseStep {
			c, err := s.Pixel(items, view.Moved(o[0]*n, o[1]*n), x, y)
			if err != nil {
				return 0, err
			}
			for k, ch := range [4]uint8{c.R, c.G, c.B, c.A} {
				lo[k], hi[k] = min(lo[k], ch), max(hi[k], ch)
			}
			if worst = distance(v, lo, hi); worst <= Tolerance {
				return worst, nil
			}
		}
	}
	return worst, nil
}

// distance returns how far v is outside the range lo to hi, in the worst channel.
func distance(v, lo, hi [4]uint8) int {
	worst := 0
	for c := range 4 {
		worst = max(worst, int(lo[c])-int(v[c]), int(v[c])-int(hi[c]))
	}
	return worst
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Capture draws s into a texture and reads the texture back.
func Capture(device *wgpu.Device, s *scene.Scene, view scene.Projection, w, h int) (*image.RGBA, error) {
	const format = wgpu.TextureFormatRGBA8Unorm
	r, err := New(device, format, s)
	if err != nil {
		return nil, err
	}
	defer r.Release()

	size := wgpu.Extent3D{Width: uint32(w), Height: uint32(h), DepthOrArrayLayers: 1}
	tex, err := device.TryCreateTexture(&wgpu.TextureDescriptor{
		Label: "capture", Usage: wgpu.TextureUsageRenderAttachment | wgpu.TextureUsageCopySrc,
		Dimension: wgpu.TextureDimension2D, Size: size, Format: format, MipLevelCount: 1, SampleCount: 1,
	})
	if err != nil {
		return nil, fmt.Errorf("render: create capture texture: %w", err)
	}
	defer tex.Release()
	target, err := tex.TryCreateView(nil)
	if err != nil {
		return nil, fmt.Errorf("render: create capture view: %w", err)
	}
	defer target.Release()

	// A texture copy needs rows aligned to 256 bytes.
	row := (w*4 + 255) / 256 * 256
	staging, err := device.TryCreateBuffer(&wgpu.BufferDescriptor{
		Label: "capture", Usage: wgpu.BufferUsageMapRead | wgpu.BufferUsageCopyDst, Size: uint64(row * h),
	})
	if err != nil {
		return nil, fmt.Errorf("render: create capture buffer: %w", err)
	}
	defer staging.Release()

	err = r.submit(target, r.pipeline, view, w, h, func(encoder *wgpu.CommandEncoder) error {
		return encoder.TryCopyTextureToBuffer(
			&wgpu.TexelCopyTextureInfo{Texture: tex, Aspect: wgpu.TextureAspectAll},
			&wgpu.TexelCopyBufferInfo{Buffer: staging, Layout: wgpu.TexelCopyBufferLayout{BytesPerRow: uint32(row), RowsPerImage: uint32(h)}},
			&size,
		)
	})
	if err != nil {
		return nil, err
	}

	var status wgpu.MapAsyncStatus
	done := false
	err = staging.TryMapAsync(wgpu.MapModeRead, 0, uint64(row*h), func(s wgpu.MapAsyncStatus) {
		status, done = s, true
	})
	if err != nil {
		return nil, fmt.Errorf("render: map capture buffer: %w", err)
	}
	// The browser backend completes the map before TryMapAsync returns.
	for !done {
		device.Poll(true, nil)
	}
	if status != wgpu.MapAsyncStatusSuccess {
		return nil, fmt.Errorf("render: map capture buffer: status %d", status)
	}
	mapped := staging.GetMappedRange(0, uint(row*h))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		copy(img.Pix[y*img.Stride:][:w*4], mapped[y*row:])
	}
	if err := staging.TryUnmap(); err != nil {
		return nil, fmt.Errorf("render: unmap capture buffer: %w", err)
	}
	return img, nil
}
