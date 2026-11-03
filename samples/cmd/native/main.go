//go:build cgo

// Command native draws the scene in a desktop window, with wgpu-native and GLFW.
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/dhannyell/webgpu/wgpu"
	"github.com/dhannyell/webgpu/wgpuglfw"
	"github.com/go-gl/glfw/v3.4/glfw"

	"github.com/dhannyell/go-slug/samples/internal/render"
	"github.com/dhannyell/go-slug/samples/internal/scene"
)

func init() {
	// GLFW needs the main thread.
	runtime.LockOSThread()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "native:", err)
		os.Exit(1)
	}
}

func run() error {
	if err := glfw.Init(); err != nil {
		return err
	}
	defer glfw.Terminate()
	glfw.WindowHint(glfw.ClientAPI, glfw.NoAPI)
	window, err := glfw.CreateWindow(scene.Width, scene.Height, "go-slug samples", nil, nil)
	if err != nil {
		return err
	}
	defer window.Destroy()

	instance := wgpu.CreateInstance(nil)
	defer instance.Release()
	surface := instance.CreateSurface(wgpuglfw.GetSurfaceDescriptor(window))
	defer surface.Release()
	adapter, err := instance.RequestAdapter(&wgpu.RequestAdapterOptions{
		CompatibleSurface: surface,
		PowerPreference:   wgpu.PowerPreferenceHighPerformance,
	})
	if err != nil {
		return fmt.Errorf("request adapter: %w", err)
	}
	defer adapter.Release()
	device, err := adapter.RequestDevice(nil)
	if err != nil {
		return fmt.Errorf("request device: %w", err)
	}
	defer device.Release()

	caps := surface.GetCapabilities(adapter)
	format, err := unormFormat(caps.Formats)
	if err != nil {
		return err
	}
	configure := func(w, h int) {
		surface.Configure(device, &wgpu.SurfaceConfiguration{
			Usage: wgpu.TextureUsageRenderAttachment, Format: format,
			Width: uint32(w), Height: uint32(h),
			PresentMode: wgpu.PresentModeFifo, AlphaMode: caps.AlphaModes[0],
		})
	}

	s, err := scene.New()
	if err != nil {
		return err
	}
	r, err := render.New(device, format, s)
	if err != nil {
		return err
	}
	defer r.Release()

	check, err := render.Check(device, s, scene.Width, scene.Height)
	if err != nil {
		return err
	}
	fmt.Println("GPU and CPU:", check)

	width, height := 0, 0
	for !window.ShouldClose() {
		glfw.PollEvents()
		w, h := window.GetFramebufferSize()
		if w == 0 || h == 0 {
			glfw.WaitEvents()
			continue
		}
		if w != width || h != height {
			width, height = w, h
			configure(w, h)
		}
		if err := r.Present(surface, w, h); err != nil {
			return err
		}
	}
	return nil
}

// unormFormat selects a format without an sRGB view: the shader blends in sRGB,
// as Pack.Rasterize does.
func unormFormat(formats []wgpu.TextureFormat) (wgpu.TextureFormat, error) {
	for _, f := range formats {
		if f == wgpu.TextureFormatBGRA8Unorm || f == wgpu.TextureFormatRGBA8Unorm {
			return f, nil
		}
	}
	return 0, errors.New("the surface has no BGRA8Unorm or RGBA8Unorm format")
}
