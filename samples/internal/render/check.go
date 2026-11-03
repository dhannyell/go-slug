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
// sample. Slug coverage jumps where the line of an edge passes a corner, because
// a ray sees a curve only inside the span of the curve. A sample on that line can
// go to either side, so the oracle of such a pixel is a range.
const noise = 1.0 / 512

// Diff is the result of a comparison of the two renderers.
type Diff struct {
	Max    int // the largest difference of a channel from the oracle
	Pixels int // the pixels over Tolerance
	Total  int // the pixels compared
}

func (d Diff) String() string {
	return fmt.Sprintf("max difference %d/255, %d of %d pixels over %d", d.Max, d.Pixels, d.Total, Tolerance)
}

// Check draws s with the GPU and with the CPU, and compares the two images.
func Check(device *wgpu.Device, s *scene.Scene, w, h int) (Diff, error) {
	got, err := Capture(device, s, w, h)
	if err != nil {
		return Diff{}, err
	}
	want, err := s.Rasterize(w, h, 0, 0)
	if err != nil {
		return Diff{}, err
	}

	// Most pixels match the first image, so draw the moved images only if needed.
	var outliers []int
	for i := 0; i < len(want.Pix); i += 4 {
		for c := range 4 {
			if abs(int(got.Pix[i+c])-int(want.Pix[i+c])) > Tolerance {
				outliers = append(outliers, i)
				break
			}
		}
	}
	oracle := []*image.RGBA{want}
	if len(outliers) > 0 {
		for _, o := range [][2]float32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {-1, -1}, {1, -1}, {-1, 1}} {
			moved, err := s.Rasterize(w, h, o[0]*noise, o[1]*noise)
			if err != nil {
				return Diff{}, err
			}
			oracle = append(oracle, moved)
		}
	}

	d := Diff{Total: w * h}
	for _, i := range outliers {
		worst := 0
		for c := range 4 {
			lo, hi := 255, 0
			for _, img := range oracle {
				lo, hi = min(lo, int(img.Pix[i+c])), max(hi, int(img.Pix[i+c]))
			}
			v := int(got.Pix[i+c])
			worst = max(worst, lo-v, v-hi)
		}
		d.Max = max(d.Max, worst)
		if worst > Tolerance {
			d.Pixels++
		}
	}
	return d, nil
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Capture draws s into a texture and reads the texture back.
func Capture(device *wgpu.Device, s *scene.Scene, w, h int) (*image.RGBA, error) {
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
	view, err := tex.TryCreateView(nil)
	if err != nil {
		return nil, fmt.Errorf("render: create capture view: %w", err)
	}
	defer view.Release()

	// A texture copy needs rows aligned to 256 bytes.
	row := (w*4 + 255) / 256 * 256
	staging, err := device.TryCreateBuffer(&wgpu.BufferDescriptor{
		Label: "capture", Usage: wgpu.BufferUsageMapRead | wgpu.BufferUsageCopyDst, Size: uint64(row * h),
	})
	if err != nil {
		return nil, fmt.Errorf("render: create capture buffer: %w", err)
	}
	defer staging.Release()

	err = r.submit(view, w, h, func(encoder *wgpu.CommandEncoder) error {
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
