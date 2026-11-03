//go:build cgo || js

package render

import (
	_ "embed"
	"fmt"
	"slices"

	"github.com/dhannyell/webgpu/wgpu"

	slug "github.com/dhannyell/go-slug"
	"github.com/dhannyell/go-slug/samples/internal/scene"
)

//go:embed versus.wgsl
var versusWGSL string

// versusInstanceSize is the size of the attributes of VersusInstance.
const versusInstanceSize = 68

// method holds one traditional renderer on the GPU. It uses the texture filters
// that give the best image: mipmaps, and anisotropic filtering.
type method struct {
	kind      scene.Method
	atlas     *scene.Atlas
	pipeline  *wgpu.RenderPipeline
	line      *wgpu.RenderPipeline
	instances *wgpu.Buffer
	texture   *wgpu.Texture
	view      *wgpu.TextureView
	sampler   *wgpu.Sampler
	params    *wgpu.BindGroup
	images    *wgpu.BindGroup
	size      uint64
}

func (m *method) release() {
	m.images.Release()
	m.params.Release()
	m.sampler.Release()
	m.view.Release()
	m.texture.Release()
	m.instances.Release()
	m.line.Release()
	m.pipeline.Release()
}

// draw draws a frame with the traditional renderer. base is the index of the
// parameters of the first span.
func (m *method) draw(pass *wgpu.RenderPassEncoder, frame []scene.Span, base int) {
	pass.SetPipeline(m.pipeline)
	pass.SetVertexBuffer(0, m.instances, 0, m.size)
	pass.SetBindGroup(1, m.images, nil)
	for i, span := range frame {
		if span.Count == 0 {
			continue
		}
		pass.SetBindGroup(0, m.params, []uint32{uint32(base+i) * paramsAlign})
		pass.Draw(6, uint32(span.Count), 0, uint32(span.First))
	}
}

// method returns the traditional renderer of a mode, and builds it at the first use.
func (r *Renderer) method(mode Mode) (*method, error) {
	if m, ok := r.methods[mode]; ok {
		return m, nil
	}
	kind := scene.Bitmap
	if mode == VersusDistance {
		kind = scene.Distance
	}
	m, err := r.newMethod(kind)
	if err != nil {
		return nil, err
	}
	if r.methods == nil {
		r.methods = map[Mode]*method{}
	}
	r.methods[mode] = m
	return m, nil
}

// defaultBudget returns the size of the atlas of a method, as a multiple of the size
// of the pack. With 1, the strokes of a distance field are too thin for its texels.
func defaultBudget(kind scene.Method) int {
	if kind == scene.Distance {
		return 4
	}
	return 1
}

func (r *Renderer) newMethod(kind scene.Method) (*method, error) {
	budget := r.Budget
	if budget <= 0 {
		budget = defaultBudget(kind)
	}
	atlas, err := r.scene.Atlas(kind, r.scene.PackBytes()*budget)
	if err != nil {
		return nil, err
	}
	m := &method{kind: kind, atlas: atlas}

	module, err := r.device.TryCreateShaderModule(&wgpu.ShaderModuleDescriptor{
		Label: "versus", WGSLSource: &wgpu.ShaderSourceWGSL{Code: slug.ReferenceWGSL + versusWGSL},
	})
	if err != nil {
		return nil, fmt.Errorf("render: compile versus shader: %w", err)
	}
	defer module.Release()

	paramsLayout, err := r.device.TryCreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{
		Label: "versus params",
		Entries: []wgpu.BindGroupLayoutEntry{{
			Binding: 0, Visibility: wgpu.ShaderStageVertex,
			Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingTypeUniform, HasDynamicOffset: true, MinBindingSize: paramsSize},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("render: create versus layout: %w", err)
	}
	defer paramsLayout.Release()
	imagesLayout, err := r.device.TryCreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{
		Label: "versus images",
		Entries: []wgpu.BindGroupLayoutEntry{
			{Binding: 0, Visibility: wgpu.ShaderStageFragment, Texture: wgpu.TextureBindingLayout{
				SampleType: wgpu.TextureSampleTypeFloat, ViewDimension: wgpu.TextureViewDimension2D,
			}},
			{Binding: 1, Visibility: wgpu.ShaderStageFragment, Sampler: wgpu.SamplerBindingLayout{Type: wgpu.SamplerBindingTypeFiltering}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("render: create versus layout: %w", err)
	}
	defer imagesLayout.Release()
	layout, err := r.device.TryCreatePipelineLayout(&wgpu.PipelineLayoutDescriptor{
		Label: "versus", BindGroupLayouts: []*wgpu.BindGroupLayout{paramsLayout, imagesLayout},
	})
	if err != nil {
		return nil, fmt.Errorf("render: create versus pipeline layout: %w", err)
	}
	defer layout.Release()

	fragment := "versus_bitmap_fs"
	if kind == scene.Distance {
		fragment = "versus_distance_fs"
	}
	if m.pipeline, err = r.createVersusPipeline(module, layout, fragment); err != nil {
		return nil, err
	}
	m.line, err = r.device.TryCreateRenderPipeline(&wgpu.RenderPipelineDescriptor{
		Label:  "versus line",
		Vertex: wgpu.VertexState{Module: module, EntryPoint: "versus_line_vs"},
		Primitive: wgpu.PrimitiveState{
			Topology: wgpu.PrimitiveTopologyTriangleList, StripIndexFormat: wgpu.IndexFormatUndefined,
			FrontFace: wgpu.FrontFaceCCW, CullMode: wgpu.CullModeNone,
		},
		Multisample: wgpu.MultisampleState{Count: 1, Mask: 0xFFFFFFFF},
		Fragment: &wgpu.FragmentState{
			Module: module, EntryPoint: "versus_line_fs",
			Targets: []wgpu.ColorTargetState{{Format: r.format, WriteMask: wgpu.ColorWriteMaskAll}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("render: create versus line pipeline: %w", err)
	}

	data := versusInstances(r.scene, atlas)
	m.size = uint64(len(data))
	m.instances, err = r.device.TryCreateBuffer(&wgpu.BufferDescriptor{
		Label: "versus", Usage: wgpu.BufferUsageVertex | wgpu.BufferUsageCopyDst, Size: m.size,
	})
	if err != nil {
		return nil, fmt.Errorf("render: create versus buffer: %w", err)
	}
	if err := r.queue.TryWriteBuffer(m.instances, 0, data); err != nil {
		return nil, fmt.Errorf("render: upload versus instances: %w", err)
	}

	if err := m.uploadAtlas(r); err != nil {
		return nil, err
	}
	m.params, err = r.device.TryCreateBindGroup(&wgpu.BindGroupDescriptor{
		Label: "versus params", Layout: paramsLayout,
		Entries: []wgpu.BindGroupEntry{{Binding: 0, Buffer: r.params, Size: paramsSize}},
	})
	if err != nil {
		return nil, fmt.Errorf("render: create versus bind group: %w", err)
	}
	m.images, err = r.device.TryCreateBindGroup(&wgpu.BindGroupDescriptor{
		Label: "versus images", Layout: imagesLayout,
		Entries: []wgpu.BindGroupEntry{{Binding: 0, TextureView: m.view}, {Binding: 1, Sampler: m.sampler}},
	})
	if err != nil {
		return nil, fmt.Errorf("render: create versus bind group: %w", err)
	}
	return m, nil
}

func (r *Renderer) createVersusPipeline(module *wgpu.ShaderModule, layout *wgpu.PipelineLayout, fragment string) (*wgpu.RenderPipeline, error) {
	over := wgpu.BlendComponent{Operation: wgpu.BlendOperationAdd, SrcFactor: wgpu.BlendFactorOne, DstFactor: wgpu.BlendFactorOneMinusSrcAlpha}
	p, err := r.device.TryCreateRenderPipeline(&wgpu.RenderPipelineDescriptor{
		Label:  fragment,
		Layout: layout,
		Vertex: wgpu.VertexState{
			Module: module, EntryPoint: "versus_vs",
			Buffers: []wgpu.VertexBufferLayout{{
				ArrayStride: versusInstanceSize,
				StepMode:    wgpu.VertexStepModeInstance,
				Attributes: []wgpu.VertexAttribute{
					{Format: wgpu.VertexFormatFloat32x4, Offset: 0, ShaderLocation: 0},
					{Format: wgpu.VertexFormatFloat32x2, Offset: 16, ShaderLocation: 1},
					{Format: wgpu.VertexFormatFloat32x4, Offset: 24, ShaderLocation: 2},
					{Format: wgpu.VertexFormatFloat32x4, Offset: 40, ShaderLocation: 3},
					{Format: wgpu.VertexFormatFloat32x3, Offset: 56, ShaderLocation: 4},
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

// versusInstances returns one instance per item. An item without an outline has
// no color, so it draws nothing.
func versusInstances(s *scene.Scene, atlas *scene.Atlas) []byte {
	data := make([]byte, 0, len(s.Items)*versusInstanceSize)
	side := float32(atlas.Side)
	for _, it := range s.Items {
		m, c, b := it.Place, it.Color, it.Entry.Bounds
		var pad, ox, oy, scale float32
		switch {
		case it.Entry.LayerCount == 0:
			c = [4]float32{}
		case !it.Plain():
			cell := atlas.Cell(it.Entry)
			pad = atlas.Padding()
			scale = atlas.PixelsPerEm / side
			ox = float32(cell.X)/side - b.Min.X*scale
			oy = float32(cell.Y)/side - b.Min.Y*scale
		}
		data = floats(data,
			m[0], m[1], m[2], m[3], m[4], m[5],
			c[0], c[1], c[2], c[3],
			b.Min.X-pad, b.Min.Y-pad, b.Max.X+pad, b.Max.Y+pad,
			ox, oy, scale,
		)
	}
	return data
}

func (m *method) uploadAtlas(r *Renderer) error {
	var err error
	side := uint32(m.atlas.Side)
	m.texture, err = r.device.TryCreateTexture(&wgpu.TextureDescriptor{
		Label: "atlas", Usage: wgpu.TextureUsageTextureBinding | wgpu.TextureUsageCopyDst,
		Dimension: wgpu.TextureDimension2D, Size: wgpu.Extent3D{Width: side, Height: side, DepthOrArrayLayers: 1},
		Format: wgpu.TextureFormatR8Unorm, MipLevelCount: uint32(len(m.atlas.Levels)), SampleCount: 1,
	})
	if err != nil {
		return fmt.Errorf("render: create atlas: %w", err)
	}
	for level, pix := range m.atlas.Levels {
		s := side >> level
		err := r.queue.TryWriteTexture(
			&wgpu.TexelCopyTextureInfo{Texture: m.texture, MipLevel: uint32(level), Aspect: wgpu.TextureAspectAll},
			pix,
			&wgpu.TexelCopyBufferLayout{BytesPerRow: s, RowsPerImage: s},
			&wgpu.Extent3D{Width: s, Height: s, DepthOrArrayLayers: 1},
		)
		if err != nil {
			return fmt.Errorf("render: upload atlas: %w", err)
		}
	}
	if m.view, err = m.texture.TryCreateView(nil); err != nil {
		return fmt.Errorf("render: create atlas view: %w", err)
	}
	m.sampler, err = r.device.TryCreateSampler(&wgpu.SamplerDescriptor{
		Label:        "atlas",
		AddressModeU: wgpu.AddressModeClampToEdge, AddressModeV: wgpu.AddressModeClampToEdge, AddressModeW: wgpu.AddressModeClampToEdge,
		MagFilter: wgpu.FilterModeLinear, MinFilter: wgpu.FilterModeLinear, MipmapFilter: wgpu.MipmapFilterModeLinear,
		LodMaxClamp: float32(len(m.atlas.Levels) - 1), MaxAnisotropy: 16,
	})
	if err != nil {
		return fmt.Errorf("render: create atlas sampler: %w", err)
	}
	return nil
}

// drawing returns the function that draws the whole target in a mode.
func (r *Renderer) drawing(mode Mode) (func(*wgpu.RenderPassEncoder, []scene.Span), error) {
	if mode != VersusBitmap && mode != VersusDistance {
		return func(pass *wgpu.RenderPassEncoder, frame []scene.Span) {
			r.drawSpans(pass, r.pipeline, frame, 0)
		}, nil
	}
	m, err := r.method(mode)
	if err != nil {
		return nil, err
	}
	return func(pass *wgpu.RenderPassEncoder, frame []scene.Span) { m.draw(pass, frame, 0) }, nil
}

// presentVersus draws the same image twice: Slug on the left half of the target,
// the traditional method on the right half, and a line between them. A split of one
// image would show each detail on one side only.
func (r *Renderer) presentVersus(target *wgpu.TextureView, t float64, w, h int) error {
	m, err := r.method(r.Mode)
	if err != nil {
		return err
	}
	half := max(w/2, 1)
	left := r.scene.Frame(t, half, h)
	frame := slices.Clone(left)
	for _, span := range left {
		span.View = span.View.Then(scene.Projection{1, 0, float64(half), 0, 1, 0, 0, 0, 1})
		frame = append(frame, span)
	}
	uh := uint32(h)
	return r.encode(target, frame, w, h, func(pass *wgpu.RenderPassEncoder, frame []scene.Span) {
		pass.SetScissorRect(0, 0, uint32(half), uh)
		r.drawSpans(pass, r.pipeline, frame[:len(left)], 0)
		pass.SetScissorRect(uint32(half), 0, uint32(w-half), uh)
		m.draw(pass, frame[len(left):], len(left))
		pass.SetPipeline(m.line)
		pass.SetScissorRect(uint32(half-1), 0, min(2, uint32(w-half+1)), uh)
		pass.Draw(3, 1, 0, 0)
	}, nil)
}

// Versus describes the two sides of the versus view.
func (r *Renderer) Versus() (string, bool) {
	m, ok := r.methods[r.Mode]
	if !ok {
		return "", false
	}
	return fmt.Sprintf("Left: Slug, %d KB. Right: %v, %d KB, %.0f texels per em.",
		r.scene.PackBytes()/1024, m.kind, m.atlas.Bytes/1024, m.atlas.PixelsPerEm), true
}
