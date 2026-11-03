//go:build cgo || js

// Package render draws a scene with the reference shader. The native host and
// the browser host use the same code.
package render

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/dhannyell/webgpu/wgpu"

	slug "github.com/dhannyell/go-slug"
	"github.com/dhannyell/go-slug/samples/internal/scene"
)

const (
	paramsSize   = 80 // mat4x4f, vec2f, and the padding of the uniform struct
	instanceSize = 44 // the attributes of SlugInstance
)

// Renderer holds the GPU copy of one scene.
type Renderer struct {
	device    *wgpu.Device
	queue     *wgpu.Queue
	scene     *scene.Scene
	pipeline  *wgpu.RenderPipeline
	params    *wgpu.Buffer
	instances *wgpu.Buffer
	bindGroup *wgpu.BindGroup
	count     uint32 // of instances
	buffers   []*wgpu.Buffer
}

// New uploads s and builds a pipeline for targets of format.
func New(device *wgpu.Device, format wgpu.TextureFormat, s *scene.Scene) (*Renderer, error) {
	r := &Renderer{device: device, queue: device.GetQueue(), scene: s}

	module, err := device.TryCreateShaderModule(&wgpu.ShaderModuleDescriptor{
		Label: "slug", WGSLSource: &wgpu.ShaderSourceWGSL{Code: slug.ReferenceWGSL},
	})
	if err != nil {
		return nil, fmt.Errorf("render: compile shader: %w", err)
	}
	defer module.Release()

	over := wgpu.BlendComponent{Operation: wgpu.BlendOperationAdd, SrcFactor: wgpu.BlendFactorOne, DstFactor: wgpu.BlendFactorOneMinusSrcAlpha}
	r.pipeline, err = device.TryCreateRenderPipeline(&wgpu.RenderPipelineDescriptor{
		Label: "slug",
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
			Module: module, EntryPoint: "slug_fs",
			Targets: []wgpu.ColorTargetState{{
				Format:    format,
				Blend:     &wgpu.BlendState{Color: over, Alpha: over},
				WriteMask: wgpu.ColorWriteMaskAll,
			}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("render: create pipeline: %w", err)
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

	layout := r.pipeline.GetBindGroupLayout(0)
	defer layout.Release()
	r.bindGroup, err = device.TryCreateBindGroup(&wgpu.BindGroupDescriptor{Label: "slug", Layout: layout, Entries: entries})
	if err != nil {
		return nil, fmt.Errorf("render: create bind group: %w", err)
	}
	return r, nil
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
	r.bindGroup.Release()
	r.pipeline.Release()
	for _, b := range r.buffers {
		b.Release()
	}
}

// upload writes the parameters and the instances for a target of w by h pixels.
func (r *Renderer) upload(w, h int) error {
	fw, fh := float32(w), float32(h)
	params := floats(make([]byte, 0, paramsSize),
		2/fw, 0, 0, 0,
		0, -2/fh, 0, 0,
		0, 0, 1, 0,
		-1, 1, 0, 1,
		fw, fh, 0, 0,
	)
	if err := r.queue.TryWriteBuffer(r.params, 0, params); err != nil {
		return fmt.Errorf("render: upload params: %w", err)
	}

	fit := scene.Fit(w, h)
	data := make([]byte, 0, int(r.count)*instanceSize)
	for _, it := range r.scene.Items {
		m := it.Place.Then(fit)
		for i := range it.Entry.LayerCount {
			data = floats(data, m[0], m[1], m[2], m[3], m[4], m[5], it.Color[0], it.Color[1], it.Color[2], it.Color[3])
			data = binary.LittleEndian.AppendUint32(data, it.Entry.FirstLayer+i)
		}
	}
	if err := r.queue.TryWriteBuffer(r.instances, 0, data); err != nil {
		return fmt.Errorf("render: upload instances: %w", err)
	}
	return nil
}

// submit clears view and draws the scene for a target of w by h pixels. after
// records more commands in the same encoder.
func (r *Renderer) submit(view *wgpu.TextureView, w, h int, after func(*wgpu.CommandEncoder) error) error {
	if err := r.upload(w, h); err != nil {
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
			View: view, LoadOp: wgpu.LoadOpClear, StoreOp: wgpu.StoreOpStore,
			ClearValue: wgpu.Color{R: float64(bg.R) / 255, G: float64(bg.G) / 255, B: float64(bg.B) / 255, A: float64(bg.A) / 255},
		}},
	})
	if err != nil {
		return fmt.Errorf("render: begin pass: %w", err)
	}
	pass.SetPipeline(r.pipeline)
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

// Present draws one frame on the surface. It draws nothing when the surface has
// no texture, for example during a resize.
func (r *Renderer) Present(surface *wgpu.Surface, w, h int) error {
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
	view, err := tex.TryCreateView(nil)
	if err != nil {
		return fmt.Errorf("render: create view: %w", err)
	}
	defer view.Release()
	if err := r.submit(view, w, h, nil); err != nil {
		return err
	}
	surface.Present()
	return nil
}
