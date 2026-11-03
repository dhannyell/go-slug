//go:build js && wasm

// Command web builds to app.wasm: it draws a scene in a browser tab, with WebGPU.
// The address selects what the tab shows: ?scene=zoom selects a scene, and
// &mode=debug, &mode=compare, &mode=versus or &mode=versus-sdf selects a view. In a
// versus view, &budget=16 gives the atlas 16 times the size of the pack.
package main

import (
	"strconv"
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

// query returns a parameter of the address, or fallback.
func query(name, fallback string) string {
	params := js.Global().Get("URLSearchParams").New(js.Global().Get("location").Get("search"))
	if v := params.Call("get", name); !v.IsNull() {
		return v.String()
	}
	return fallback
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

	s, err := scene.New(query("scene", scene.Names[0]))
	if err != nil {
		return err.Error()
	}
	r, err := render.New(device, format, s)
	if err != nil {
		return err.Error()
	}
	switch query("mode", "") {
	case "debug":
		r.Mode = render.Debug
	case "compare":
		r.Mode = render.Compare
	case "versus":
		r.Mode = render.VersusBitmap
	case "versus-sdf":
		r.Mode = render.VersusDistance
	}
	if b, err := strconv.Atoi(query("budget", "0")); err == nil {
		r.Budget = b
	}

	hint := ""
	if r.Orbited() {
		hint = "Drag to turn. Use the wheel to zoom."
		status(hint)
		style := canvas.Get("style")
		style.Set("cursor", "grab")
		style.Set("touchAction", "none")
		canvas.Call("addEventListener", "pointerdown", js.FuncOf(func(_ js.Value, args []js.Value) any {
			canvas.Call("setPointerCapture", args[0].Get("pointerId"))
			return nil
		}))
		canvas.Call("addEventListener", "pointermove", js.FuncOf(func(_ js.Value, args []js.Value) any {
			if e := args[0]; e.Get("buttons").Int()&1 != 0 {
				r.Drag(e.Get("movementX").Float(), e.Get("movementY").Float())
			}
			return nil
		}))
		// Only a listener that is not passive can stop the page from a scroll.
		options := js.Global().Get("Object").New()
		options.Set("passive", false)
		canvas.Call("addEventListener", "wheel", js.FuncOf(func(_ js.Value, args []js.Value) any {
			e := args[0]
			e.Call("preventDefault")
			dpr := js.Global().Get("devicePixelRatio").Float()
			r.Wheel(-e.Get("deltaY").Float()/100, e.Get("offsetX").Float()*dpr, e.Get("offsetY").Float()*dpr, width, height)
			return nil
		}), options)
	}

	// The frame runs on this goroutine, not inside the callback: a callback
	// must not block the event loop.
	frames := make(chan float64, 1)
	raf := js.FuncOf(func(_ js.Value, args []js.Value) any {
		frames <- args[0].Float() / 1000
		return nil
	})
	js.Global().Call("requestAnimationFrame", raf)
	shown := false
	for now := range frames {
		if w, h := size(); w != width || h != height {
			width, height = w, h
			canvas.Set("width", w)
			canvas.Set("height", h)
			configure(w, h)
		}
		if err := r.Present(surface, now, width, height); err != nil {
			return err.Error()
		}
		if d, ok := r.Compared(); ok && !shown {
			shown = true
			status("GPU and CPU: " + d.String())
		}
		if v, ok := r.Versus(); ok && !shown {
			shown = true
			status(v + " " + hint)
		}
		js.Global().Call("requestAnimationFrame", raf)
	}
	return ""
}
