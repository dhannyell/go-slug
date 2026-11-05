//go:build cgo

package render

import (
	"fmt"
	"testing"

	"github.com/dhannyell/webgpu/wgpu"

	slug "github.com/dhannyell/go-slug"
	"github.com/dhannyell/go-slug/samples/internal/scene"
)

// TestGPUMatchesCPU is the conformance check of the shader: the CPU evaluator
// is the oracle. Each backend compiles the WGSL in its own way.
func TestGPUMatchesCPU(t *testing.T) {
	type size struct{ w, h int }
	full := []size{
		{scene.Width, scene.Height},
		{scene.Width * 2, scene.Height * 2},
		{scene.Width / 2, scene.Height / 2},
		{1000, 777},
	}
	// The perspective oracle draws pixel by pixel, so it uses fewer sizes.
	small := []size{{scene.Width, scene.Height}, {1000, 777}}
	cases := []struct {
		scene string
		times []float64
		sizes []size
	}{
		{"overview", []float64{0}, full},
		{"gradients", []float64{0}, full},
		{"text", []float64{0}, full},
		// A deep zoom tests the precision of the matrix path: the peak is at half
		// of the period.
		{"zoom", []float64{scene.ZoomPeriod / 4, scene.ZoomPeriod / 2}, small},
		// The turn is largest at 3 s, and at 9 s it has the other sign.
		{"perspective", []float64{0, 3, 9}, small},
		// The start has the floor at the lowest angle, and the half is near the cube.
		{"hall", []float64{0, 4, scene.HallPeriod / 4, scene.HallPeriod / 2}, small},
		// The peak has the largest scale, at a low angle.
		{"dive", []float64{0, scene.DivePeriod / 4, scene.DivePeriod / 2}, small},
	}

	backends := []struct {
		name string
		kind wgpu.BackendType
	}{{"vulkan", wgpu.BackendTypeVulkan}, {"d3d12", wgpu.BackendTypeD3D12}, {"metal", wgpu.BackendTypeMetal}}
	instance := wgpu.CreateInstance(nil)
	defer instance.Release()
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			adapter, err := instance.RequestAdapter(&wgpu.RequestAdapterOptions{BackendType: b.kind})
			if err != nil {
				t.Skip("no adapter:", err)
			}
			defer adapter.Release()
			device, err := adapter.RequestDevice(nil)
			if err != nil {
				t.Skip("no device:", err)
			}
			defer device.Release()
			for _, c := range cases {
				s, err := scene.New(c.scene)
				if err != nil {
					t.Fatal(err)
				}
				for _, at := range c.times {
					for _, sz := range c.sizes {
						d, err := Check(device, s, s.Frame(at, sz.w, sz.h), sz.w, sz.h)
						if err != nil {
							t.Fatal(err)
						}
						t.Logf("%s t=%v %dx%d: %v", c.scene, at, sz.w, sz.h, d)
						if d.Pixels > 0 {
							t.Errorf("%s t=%v %dx%d: %v", c.scene, at, sz.w, sz.h, d)
						}
					}
				}
			}
			// The first view, a large angle, the back page, and a deep zoom.
			s, err := scene.New("news")
			if err != nil {
				t.Fatal(err)
			}
			for _, o := range []scene.Orbit{*s.Orbit, {Yaw: 1.1, Pitch: 0.4}, {Yaw: 3.3, Pitch: -0.2}, {Yaw: 0.2, Zoom: 5, X: 2, Y: 3}} {
				*s.Orbit = o
				for _, sz := range small {
					d, err := Check(device, s, s.Frame(0, sz.w, sz.h), sz.w, sz.h)
					if err != nil {
						t.Fatal(err)
					}
					t.Logf("news %+v %dx%d: %v", o, sz.w, sz.h, d)
					if d.Pixels > 0 {
						t.Errorf("news %+v %dx%d: %v", o, sz.w, sz.h, d)
					}
				}
			}
		})
	}
}

// testDevice returns a device on the first backend that has one.
func testDevice(tb testing.TB) *wgpu.Device {
	tb.Helper()
	instance := wgpu.CreateInstance(nil)
	tb.Cleanup(instance.Release)
	adapter, err := instance.RequestAdapter(&wgpu.RequestAdapterOptions{PowerPreference: wgpu.PowerPreferenceHighPerformance})
	if err != nil {
		tb.Skip("no adapter:", err)
	}
	tb.Cleanup(adapter.Release)
	device, err := adapter.RequestDevice(nil)
	if err != nil {
		tb.Skip("no device:", err)
	}
	tb.Cleanup(device.Release)
	return device
}

// TestMethodsMatchSlugAtTheirScale keeps the versus view fair. One texel per pixel
// is the best case of a texture, so a difference here is a bug in the method, and
// not a limit of it. The anisotropic filter of Lavapipe moves a bitmap by up to 25
// steps here. A distance field cannot keep all of the coverage, so its limit is
// larger, but its outline must not move.
func TestMethodsMatchSlugAtTheirScale(t *testing.T) {
	device := testDevice(t)
	s, err := scene.New("dive")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		mode  Mode
		kind  scene.Method
		limit int
	}{{VersusBitmap, scene.Bitmap, 32}, {VersusDistance, scene.Distance, 110}}
	for _, c := range cases {
		atlas, err := s.Atlas(c.kind, s.PackBytes()*defaultBudget(c.kind))
		if err != nil {
			t.Fatal(err)
		}
		// The S of the word: a view that puts its texels on the pixels.
		first := 0
		for s.Items[first].Entry.Bounds.Max.Y-s.Items[first].Entry.Bounds.Min.Y < 0.5 || s.Items[first].Color != s.Items[len(s.Items)-1].Color {
			first++
		}
		it := s.Items[first]
		inverse, ok := it.Place.Invert()
		if !ok {
			t.Fatal("no inverse")
		}
		k := atlas.PixelsPerEm
		cell := inverse.Then(slug.Affine{k, 0, 0, k, 8 - it.Entry.Bounds.Min.X*k, 8 - it.Entry.Bounds.Min.Y*k})
		frame := []scene.Span{{First: first, Count: 1, View: scene.FromAffine(cell)}}
		w, h := int((it.Entry.Bounds.Max.X-it.Entry.Bounds.Min.X)*k)+16, int((it.Entry.Bounds.Max.Y-it.Entry.Bounds.Min.Y)*k)+16
		want, err := Capture(device, s, frame, w, h)
		if err != nil {
			t.Fatal(err)
		}
		got, err := capture(device, s, c.mode, frame, w, h)
		if err != nil {
			t.Fatal(err)
		}
		worst := 0
		for i := range got.Pix {
			worst = max(worst, abs(int(got.Pix[i])-int(want.Pix[i])))
		}
		t.Logf("%v at %.1f texels per em: max difference %d/255", c.kind, k, worst)
		if worst > c.limit {
			t.Errorf("%v: max difference %d/255, the limit is %d", c.kind, worst, c.limit)
		}
	}
}

// BenchmarkFrame measures the GPU cost of one frame of each method. Each pass draws
// the frame many times, so the work of the GPU is larger than the work of the CPU.
func BenchmarkFrame(b *testing.B) {
	device := testDevice(b)
	const w, h, repeats = 1920, 1080, 50
	const format = wgpu.TextureFormatRGBA8Unorm
	tex, err := device.TryCreateTexture(&wgpu.TextureDescriptor{
		Label: "bench", Usage: wgpu.TextureUsageRenderAttachment,
		Dimension: wgpu.TextureDimension2D, Size: wgpu.Extent3D{Width: w, Height: h, DepthOrArrayLayers: 1},
		Format: format, MipLevelCount: 1, SampleCount: 1,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer tex.Release()
	target, err := tex.TryCreateView(nil)
	if err != nil {
		b.Fatal(err)
	}
	defer target.Release()

	modes := []struct {
		name string
		mode Mode
	}{{"slug", Normal}, {"bitmap", VersusBitmap}, {"sdf", VersusDistance}}
	for _, c := range []struct {
		scene string
		at    float64
	}{{"hall", 0}, {"hall", scene.HallPeriod / 2}, {"dive", 0}, {"dive", scene.DivePeriod / 2}, {"news", 0}} {
		s, err := scene.New(c.scene)
		if err != nil {
			b.Fatal(err)
		}
		r, err := New(device, format, s)
		if err != nil {
			b.Fatal(err)
		}
		defer r.Release()
		frame := s.Frame(c.at, w, h)
		for _, m := range modes {
			once, err := r.drawing(m.mode)
			if err != nil {
				b.Fatal(err)
			}
			draw := func(pass *wgpu.RenderPassEncoder, frame []scene.Span) {
				for range repeats {
					once(pass, frame)
				}
			}
			b.Run(fmt.Sprintf("%s/t=%v/%s", c.scene, c.at, m.name), func(b *testing.B) {
				for b.Loop() {
					if err := r.encode(target, frame, w, h, draw, nil); err != nil {
						b.Fatal(err)
					}
					device.Poll(true, nil)
				}
				b.ReportMetric(float64(b.Elapsed().Microseconds())/float64(b.N)/repeats, "us/frame")
				b.ReportMetric(0, "ns/op")
			})
		}
	}
}
