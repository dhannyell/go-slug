//go:build cgo

package render

import (
	"testing"

	"github.com/dhannyell/webgpu/wgpu"

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
		{"text", []float64{0}, full},
		// A deep zoom tests the precision of the matrix path: the peak is at half
		// of the period.
		{"zoom", []float64{scene.ZoomPeriod / 4, scene.ZoomPeriod / 2}, small},
		// The turn is largest at 3 s, and at 9 s it has the other sign.
		{"perspective", []float64{0, 3, 9}, small},
		// The start has the floor at the lowest angle, and the half is near the cube.
		{"hall", []float64{0, 4, scene.HallPeriod / 4, scene.HallPeriod / 2}, small},
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
		})
	}
}
