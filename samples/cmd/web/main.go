//go:build js && wasm

// Command web builds to app.wasm: it draws the scene in a browser tab, with
// WebGPU. The address ?check compares the GPU with the CPU and shows the result.
package main

import (
	"strings"
	"syscall/js"

	"github.com/dhannyell/webgpu/wgpu"

	"github.com/dhannyell/go-slug/samples/internal/render"
	"github.com/dhannyell/go-slug/samples/internal/scene"
)

func main() {
	if err := run(); err != "" {
		status("Error: " + err)
	}
	select {}
}

func status(text string) {
	println(text)
	js.Global().Get("document").Call("getElementById", "status").Set("textContent", text)
}

func run() string {
	document := js.Global().Get("document")
	canvas := document.Call("getElementById", "canvas")

	size := func() (int, int) {
		dpr := js.Global().Get("devicePixelRatio").Float()
		w := int(canvas.Get("clientWidth").Float() * dpr)
		h := int(canvas.Get("clientHeight").Float() * dpr)
		return max(w, 1), max(h, 1)
	}
	width, height := size()
	canvas.Set("width", width)
	canvas.Set("height", height)

	instance := wgpu.CreateInstance(nil)
	if instance == nil {
		return "this browser has no WebGPU"
	}
	surface := instance.CreateSurface(&wgpu.SurfaceDescriptor{Canvas: canvas})
	adapter, err := instance.RequestAdapter(&wgpu.RequestAdapterOptions{CompatibleSurface: surface})
	if err != nil {
		return "request adapter: " + err.Error()
	}
	device, err := adapter.RequestDevice(nil)
	if err != nil {
		return "request device: " + err.Error()
	}

	// The preferred format of a browser has no sRGB view, so the shader blends
	// in sRGB, as Pack.Rasterize does.
	format := surface.GetCapabilities(adapter).Formats[0]
	configure := func(w, h int) {
		surface.Configure(device, &wgpu.SurfaceConfiguration{
			Usage: wgpu.TextureUsageRenderAttachment, Format: format,
			Width: uint32(w), Height: uint32(h),
			PresentMode: wgpu.PresentModeFifo, AlphaMode: wgpu.CompositeAlphaModeOpaque,
		})
	}
	configure(width, height)

	s, err := scene.New()
	if err != nil {
		return err.Error()
	}
	r, err := render.New(device, format, s)
	if err != nil {
		return err.Error()
	}

	if strings.Contains(js.Global().Get("location").Get("search").String(), "check") {
		d, err := render.Check(device, s, scene.Width, scene.Height)
		if err != nil {
			return err.Error()
		}
		status("GPU and CPU: " + d.String())
	}

	// The frame runs on this goroutine, not inside the callback: a callback
	// must not block the event loop.
	frames := make(chan struct{}, 1)
	raf := js.FuncOf(func(js.Value, []js.Value) any {
		frames <- struct{}{}
		return nil
	})
	js.Global().Call("requestAnimationFrame", raf)
	for range frames {
		if w, h := size(); w != width || h != height {
			width, height = w, h
			canvas.Set("width", w)
			canvas.Set("height", h)
			configure(w, h)
		}
		if err := r.Present(surface, width, height); err != nil {
			return err.Error()
		}
		js.Global().Call("requestAnimationFrame", raf)
	}
	return ""
}
