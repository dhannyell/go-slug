//go:build cgo

// Command native draws the scenes in a desktop window, with wgpu-native and GLFW.
// The number keys select a scene, D shows the debug view, and C shows the
// comparison of the GPU with the CPU.
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

	// A renderer holds a pack on the GPU, so build each one once.
	renderers := map[string]*render.Renderer{}
	defer func() {
		for _, r := range renderers {
			r.Release()
		}
	}()
	selected, mode := scene.Names[0], render.Normal
	selectRenderer := func() (*render.Renderer, error) {
		r, ok := renderers[selected]
		if !ok {
			s, err := scene.New(selected)
			if err != nil {
				return nil, err
			}
			if r, err = render.New(device, format, s); err != nil {
				return nil, err
			}
			renderers[selected] = r
		}
		r.Mode = mode
		return r, nil
	}

	window.SetKeyCallback(func(_ *glfw.Window, key glfw.Key, _ int, action glfw.Action, _ glfw.ModifierKey) {
		if action != glfw.Press {
			return
		}
		if i := int(key - glfw.Key1); i >= 0 && i < len(scene.Names) {
			selected = scene.Names[i]
		}
		toggle := func(m render.Mode) {
			if mode == m {
				mode = render.Normal
			} else {
				mode = m
			}
		}
		switch key {
		case glfw.KeyD:
			toggle(render.Debug)
		case glfw.KeyC:
			toggle(render.Compare)
		}
	})

	width, height := 0, 0
	title := ""
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
		r, err := selectRenderer()
		if err != nil {
			return err
		}
		if err := r.Present(surface, glfw.GetTime(), w, h); err != nil {
			return err
		}

		next := fmt.Sprintf("go-slug samples: %s (keys 1-%d, D debug, C compare)", selected, len(scene.Names))
		if d, ok := r.Compared(); ok && mode == render.Compare {
			next = "go-slug samples: " + selected + ": GPU and CPU: " + d.String()
		}
		if next != title {
			title = next
			window.SetTitle(title)
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
