//go:build cgo || js

// Package render draws a scene with the reference shader. The native host and
// the browser host use the same code.
package render

import (
	_ "embed"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/dhannyell/webgpu/wgpu"

	slug "github.com/dhannyell/go-slug"
	"github.com/dhannyell/go-slug/samples/internal/scene"
)

//go:embed debug.wgsl
var debugWGSL string

const (
	paramsSize   = 80 // mat4x4f, vec2f, and the padding of the uniform struct
	instanceSize = 44 // the attributes of SlugInstance
)

// Mode selects what Present shows.
type Mode int

const (
	// Normal draws the scene with the reference shader.
	Normal Mode = iota
	// Debug shows the bands, the cost of each band, and the curve points.
	Debug
	// Compare shows the GPU image, the CPU image, and their difference.
	Compare
)

// Renderer holds the GPU copy of one scene.
type Renderer struct {
	Mode Mode

	device    *wgpu.Device
	queue     *wgpu.Queue
	format    wgpu.TextureFormat
	scene     *scene.Scene
	pipeline  *wgpu.RenderPipeline
	debug     *wgpu.RenderPipeline
	params    *wgpu.Buffer
	instances *wgpu.Buffer
	bindGroup *wgpu.BindGroup
	count     uint32 // of instances
	buffers   []*wgpu.Buffer
	compare   *comparison
}

// New uploads s and builds the pipelines for targets of format.
func New(device *wgpu.Device, format wgpu.TextureFormat, s *scene.Scene) (*Renderer, error) {
	r := &Renderer{device: device, queue: device.GetQueue(), format: format, scene: s}

	module, err := device.TryCreateShaderModule(&wgpu.ShaderModuleDescriptor{
		Label: "slug", WGSLSource: &wgpu.ShaderSourceWGSL{Code: slug.ReferenceWGSL + debugWGSL},
	})
	if err != nil {
		return nil, fmt.Errorf("render: compile shader: %w", err)
	}
	defer module.Release()

	// The two pipelines have one layout, so they use one bind group.
	layout, err := r.layout()
	if err != nil {
		return nil, err
	}
	defer layout.bind.Release()
	defer layout.pipeline.Release()

	if r.pipeline, err = r.createPipeline(module, "slug_fs", layout.pipeline); err != nil {
		return nil, err
	}
	if r.debug, err = r.createPipeline(module, "slug_debug_fs", layout.pipeline); err != nil {
		return nil, err
	}

	for _, it := range s.Items {
		r.count += it.Entry.LayerCount
	}
	if r.params, err = r.buffer("params", wgpu.BufferUsageUniform, paramsSize); err != nil {
		return nil, err
	}
	if r.instances, err = r.buffer("instances", wgpu.BufferUsageVertex, uint64(r.count)*instanceSize); err != nil {
		return nil, err
	}
	data := make([]byte, 0, int(r.count)*instanceSize)
	for _, it := range s.Items {
		m := it.Place
		for i := range it.Entry.LayerCount {
			data = floats(data, m[0], m[1], m[2], m[3], m[4], m[5], it.Color[0], it.Color[1], it.Color[2], it.Color[3])
			data = binary.LittleEndian.AppendUint32(data, it.Entry.FirstLayer+i)
		}
	}
	if err := r.queue.TryWriteBuffer(r.instances, 0, data); err != nil {
		return nil, fmt.Errorf("render: upload instances: %w", err)
	}

	entries := []wgpu.BindGroupEntry{{Binding: 0, Buffer: r.params, Size: paramsSize}}
	for i, data := range [][]byte{floats(nil, s.Pack.Curves()...), words(s.Pack.Bands()), words(s.Pack.Layers())} {
		b, err := r.buffer("pack", wgpu.BufferUsageStorage, uint64(len(data)))
		if err != nil {
			return nil, err
		}
		if err := r.queue.TryWriteBuffer(b, 0, data); err != nil {
			return nil, fmt.Errorf("render: upload pack: %w", err)
		}
		entries = append(entries, wgpu.BindGroupEntry{Binding: uint32(i + 1), Buffer: b, Size: uint64(len(data))})
	}

	r.bindGroup, err = device.TryCreateBindGroup(&wgpu.BindGroupDescriptor{Label: "slug", Layout: layout.bind, Entries: entries})
	if err != nil {
		return nil, fmt.Errorf("render: create bind group: %w", err)
	}
	return r, nil
}

type layouts struct {
	bind     *wgpu.BindGroupLayout
	pipeline *wgpu.PipelineLayout
}

// layout declares the bindings of slug_reference.wgsl.
func (r *Renderer) layout() (layouts, error) {
	storage := func(binding uint32) wgpu.BindGroupLayoutEntry {
		return wgpu.BindGroupLayoutEntry{
			Binding: binding, Visibility: wgpu.ShaderStageVertex | wgpu.ShaderStageFragment,
			Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingTypeReadOnlyStorage},
		}
	}
	bind, err := r.device.TryCreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{
		Label: "slug",
		Entries: []wgpu.BindGroupLayoutEntry{
			{
				Binding: 0, Visibility: wgpu.ShaderStageVertex,
				Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingTypeUniform, MinBindingSize: paramsSize},
			},
			storage(1), storage(2), storage(3),
		},
	})
	if err != nil {
		return layouts{}, fmt.Errorf("render: create bind group layout: %w", err)
	}
	pipeline, err := r.device.TryCreatePipelineLayout(&wgpu.PipelineLayoutDescriptor{
		Label: "slug", BindGroupLayouts: []*wgpu.BindGroupLayout{bind},
	})
	if err != nil {
		return layouts{}, fmt.Errorf("render: create pipeline layout: %w", err)
	}
	return layouts{bind, pipeline}, nil
}

func (r *Renderer) createPipeline(module *wgpu.ShaderModule, fragment string, layout *wgpu.PipelineLayout) (*wgpu.RenderPipeline, error) {
	over := wgpu.BlendComponent{Operation: wgpu.BlendOperationAdd, SrcFactor: wgpu.BlendFactorOne, DstFactor: wgpu.BlendFactorOneMinusSrcAlpha}
	p, err := r.device.TryCreateRenderPipeline(&wgpu.RenderPipelineDescriptor{
		Label:  fragment,
		Layout: layout,
		Vertex: wgpu.VertexState{
			Module: module, EntryPoint: "slug_vs",
			Buffers: []wgpu.VertexBufferLayout{{
				ArrayStride: instanceSize,
				StepMode:    wgpu.VertexStepModeInstance,
				Attributes: []wgpu.VertexAttribute{
					{Format: wgpu.VertexFormatFloat32x4, Offset: 0, ShaderLocation: 0},
					{Format: wgpu.VertexFormatFloat32x2, Offset: 16, ShaderLocation: 1},
					{Format: wgpu.VertexFormatFloat32x4, Offset: 24, ShaderLocation: 2},
					{Format: wgpu.VertexFormatUint32, Offset: 40, ShaderLocation: 3},
				},
			}},
		},
		Primitive: wgpu.PrimitiveState{
			Topology: wgpu.PrimitiveTopologyTriangleList, StripIndexFormat: wgpu.IndexFormatUndefined,
			FrontFace: wgpu.FrontFaceCCW, CullMode: wgpu.CullModeNone,
		},
		Multisample: wgpu.MultisampleState{Count: 1, Mask: 0xFFFFFFFF},
		Fragment: &wgpu.FragmentState{
			Module: module, EntryPoint: fragment,
			Targets: []wgpu.ColorTargetState{{
				Format:    r.format,
				Blend:     &wgpu.BlendState{Color: over, Alpha: over},
				WriteMask: wgpu.ColorWriteMaskAll,
			}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("render: create pipeline %s: %w", fragment, err)
	}
	return p, nil
}

func (r *Renderer) buffer(label string, usage wgpu.BufferUsage, size uint64) (*wgpu.Buffer, error) {
	b, err := r.device.TryCreateBuffer(&wgpu.BufferDescriptor{Label: label, Usage: usage | wgpu.BufferUsageCopyDst, Size: size})
	if err != nil {
		return nil, fmt.Errorf("render: create buffer %s: %w", label, err)
	}
	r.buffers = append(r.buffers, b)
	return b, nil
}

func words(v []uint32) []byte {
	out := make([]byte, 0, len(v)*4)
	for _, w := range v {
		out = binary.LittleEndian.AppendUint32(out, w)
	}
	return out
}

func floats(b []byte, v ...float32) []byte {
	for _, f := range v {
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(f))
	}
	return b
}

// Release frees the GPU objects of r.
func (r *Renderer) Release() {
	r.compare.release()
	r.bindGroup.Release()
	r.pipeline.Release()
	r.debug.Release()
	for _, b := range r.buffers {
		b.Release()
	}
}

// upload writes the parameters for a target of w by h pixels. The shader maps
// world space to clip space with one matrix, and here world space is the design.
func (r *Renderer) upload(view scene.Projection, w, h int) error {
	fw, fh := float32(w), float32(h)
	sx, sy := 2/float64(w), -2/float64(h)
	// Clip x is 2x/w - 1, and clip y is 1 - 2y/h, both before the division by w.
	clipX := [3]float64{sx*view[0] - view[6], sx*view[1] - view[7], sx*view[2] - view[8]}
	clipY := [3]float64{sy*view[3] + view[6], sy*view[4] + view[7], sy*view[5] + view[8]}
	// The columns of a mat4x4f. The design has z = 0.
	params := floats(make([]byte, 0, paramsSize),
		float32(clipX[0]), float32(clipY[0]), 0, float32(view[6]),
		float32(clipX[1]), float32(clipY[1]), 0, float32(view[7]),
		0, 0, 1, 0,
		float32(clipX[2]), float32(clipY[2]), 0, float32(view[8]),
		fw, fh, 0, 0,
	)
	if err := r.queue.TryWriteBuffer(r.params, 0, params); err != nil {
		return fmt.Errorf("render: upload params: %w", err)
	}
	return nil
}

// submit clears target and draws the scene through view. after records more
// commands in the same encoder.
func (r *Renderer) submit(target *wgpu.TextureView, pipeline *wgpu.RenderPipeline, view scene.Projection, w, h int, after func(*wgpu.CommandEncoder) error) error {
	if err := r.upload(view, w, h); err != nil {
		return err
	}
	encoder, err := r.device.TryCreateCommandEncoder(&wgpu.CommandEncoderDescriptor{Label: "slug"})
	if err != nil {
		return fmt.Errorf("render: create encoder: %w", err)
	}
	defer encoder.Release()

	bg := scene.Background
	pass, err := encoder.TryBeginRenderPass(&wgpu.RenderPassDescriptor{
		Label: "slug",
		ColorAttachments: []wgpu.RenderPassColorAttachment{{
			View: target, LoadOp: wgpu.LoadOpClear, StoreOp: wgpu.StoreOpStore,
			ClearValue: wgpu.Color{R: float64(bg.R) / 255, G: float64(bg.G) / 255, B: float64(bg.B) / 255, A: float64(bg.A) / 255},
		}},
	})
	if err != nil {
		return fmt.Errorf("render: begin pass: %w", err)
	}
	pass.SetPipeline(pipeline)
	pass.SetBindGroup(0, r.bindGroup, nil)
	pass.SetVertexBuffer(0, r.instances, 0, uint64(r.count)*instanceSize)
	pass.Draw(6, r.count, 0, 0)
	pass.End()
	pass.Release()

	if after != nil {
		if err := after(encoder); err != nil {
			return err
		}
	}
	cmd, err := encoder.TryFinish(&wgpu.CommandBufferDescriptor{Label: "slug"})
	if err != nil {
		return fmt.Errorf("render: finish encoder: %w", err)
	}
	defer cmd.Release()
	r.queue.Submit(cmd)
	return nil
}

// Present draws one frame at t seconds on the surface. It draws nothing when the
// surface has no texture, for example during a resize.
func (r *Renderer) Present(surface *wgpu.Surface, t float64, w, h int) error {
	st, err := surface.TryGetCurrentTexture()
	if err != nil {
		return nil
	}
	tex, ok := st.Get()
	if !ok {
		if tex != nil {
			tex.Release()
		}
		return nil
	}
	defer tex.Release()
	target, err := tex.TryCreateView(nil)
	if err != nil {
		return fmt.Errorf("render: create view: %w", err)
	}
	defer target.Release()

	switch r.Mode {
	case Compare:
		err = r.presentCompare(target, t, w, h)
	case Debug:
		err = r.submit(target, r.debug, r.scene.View(t, w, h), w, h, nil)
	default:
		err = r.submit(target, r.pipeline, r.scene.View(t, w, h), w, h, nil)
	}
	if err != nil {
		return err
	}
	surface.Present()
	return nil
}
