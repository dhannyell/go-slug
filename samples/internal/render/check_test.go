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
	s, err := scene.New()
	if err != nil {
		t.Fatal(err)
	}
	backends := []struct {
		name string
		kind wgpu.BackendType
	}{{"vulkan", wgpu.BackendTypeVulkan}, {"d3d12", wgpu.BackendTypeD3D12}, {"metal", wgpu.BackendTypeMetal}}
	sizes := []struct{ w, h int }{
		{scene.Width, scene.Height},
		{scene.Width * 2, scene.Height * 2},
		{scene.Width / 2, scene.Height / 2},
		{1000, 777},
	}
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
			for _, size := range sizes {
				d, err := Check(device, s, size.w, size.h)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("%dx%d: %v", size.w, size.h, d)
				if d.Pixels > 0 {
					t.Errorf("%dx%d: %v", size.w, size.h, d)
				}
			}
		})
	}
}
