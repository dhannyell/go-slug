//go:build cgo || js

package render

import (
	"fmt"
	"image"
	"image/draw"

	"github.com/dhannyell/webgpu/wgpu"

	"github.com/dhannyell/go-slug/samples/internal/scene"
)

const blitWGSL = `
@group(0) @binding(0) var image: texture_2d<f32>;

@vertex
fn blit_vs(@builtin(vertex_index) vertex: u32) -> @builtin(position) vec4f {
    const CORNERS = array(vec2f(-1.0, -1.0), vec2f(3.0, -1.0), vec2f(-1.0, 3.0));
    return vec4f(CORNERS[vertex], 0.0, 1.0);
}

@fragment
fn blit_fs(@builtin(position) position: vec4f) -> @location(0) vec4f {
    return textureLoad(image, vec2i(position.xy), 0);
}
`

// diffGain makes a small difference visible: 16 steps of a channel fill the range.
const diffGain = 16

// comparison holds one composed image, for one size of the target.
type comparison struct {
	w, h      int
	pipeline  *wgpu.RenderPipeline
	texture   *wgpu.Texture
	view      *wgpu.TextureView
	bindGroup *wgpu.BindGroup
	Diff      Diff
}

func (c *comparison) release() {
	if c == nil {
		return
	}
	c.bindGroup.Release()
	c.view.Release()
	c.texture.Release()
	c.pipeline.Release()
}

// Compared returns the result of the last comparison on the screen.
func (r *Renderer) Compared() (Diff, bool) {
	if r.compare == nil {
		return Diff{}, false
	}
	return r.compare.Diff, true
}

// presentCompare shows the comparison at the first t of this size. The check
// reads the GPU back and draws on the CPU, so it does not run in each frame.
func (r *Renderer) presentCompare(target *wgpu.TextureView, t float64, w, h int) error {
	if c := r.compare; c == nil || c.w != w || c.h != h {
		next, err := r.newComparison(t, w, h)
		if err != nil {
			return err
		}
		r.compare.release()
		r.compare = next
	}
	c := r.compare

	encoder, err := r.device.TryCreateCommandEncoder(&wgpu.CommandEncoderDescriptor{Label: "compare"})
	if err != nil {
		return fmt.Errorf("render: create encoder: %w", err)
	}
	defer encoder.Release()
	pass, err := encoder.TryBeginRenderPass(&wgpu.RenderPassDescriptor{
		Label: "compare",
		ColorAttachments: []wgpu.RenderPassColorAttachment{{
			View: target, LoadOp: wgpu.LoadOpClear, StoreOp: wgpu.StoreOpStore,
		}},
	})
	if err != nil {
		return fmt.Errorf("render: begin pass: %w", err)
	}
	pass.SetPipeline(c.pipeline)
	pass.SetBindGroup(0, c.bindGroup, nil)
	pass.Draw(3, 1, 0, 0)
	pass.End()
	pass.Release()
	cmd, err := encoder.TryFinish(&wgpu.CommandBufferDescriptor{Label: "compare"})
	if err != nil {
		return fmt.Errorf("render: finish encoder: %w", err)
	}
	defer cmd.Release()
	r.queue.Submit(cmd)
	return nil
}

func (r *Renderer) newComparison(t float64, w, h int) (*comparison, error) {
	pw, ph := max(w/2, 1), max(h/2, 1)
	d, got, want, over, err := check(r.device, r.scene, r.scene.Frame(t, pw, ph), pw, ph)
	if err != nil {
		return nil, err
	}
	img, err := compose(got, want, over, w, h)
	if err != nil {
		return nil, err
	}

	module, err := r.device.TryCreateShaderModule(&wgpu.ShaderModuleDescriptor{
		Label: "blit", WGSLSource: &wgpu.ShaderSourceWGSL{Code: blitWGSL},
	})
	if err != nil {
		return nil, fmt.Errorf("render: compile blit shader: %w", err)
	}
	defer module.Release()
	c := &comparison{w: w, h: h, Diff: d}
	c.pipeline, err = r.device.TryCreateRenderPipeline(&wgpu.RenderPipelineDescriptor{
		Label:  "blit",
		Vertex: wgpu.VertexState{Module: module, EntryPoint: "blit_vs"},
		Primitive: wgpu.PrimitiveState{
			Topology: wgpu.PrimitiveTopologyTriangleList, StripIndexFormat: wgpu.IndexFormatUndefined,
			FrontFace: wgpu.FrontFaceCCW, CullMode: wgpu.CullModeNone,
		},
		Multisample: wgpu.MultisampleState{Count: 1, Mask: 0xFFFFFFFF},
		Fragment: &wgpu.FragmentState{
			Module: module, EntryPoint: "blit_fs",
			Targets: []wgpu.ColorTargetState{{Format: r.format, WriteMask: wgpu.ColorWriteMaskAll}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("render: create blit pipeline: %w", err)
	}

	size := wgpu.Extent3D{Width: uint32(w), Height: uint32(h), DepthOrArrayLayers: 1}
	c.texture, err = r.device.TryCreateTexture(&wgpu.TextureDescriptor{
		Label: "compare", Usage: wgpu.TextureUsageTextureBinding | wgpu.TextureUsageCopyDst,
		Dimension: wgpu.TextureDimension2D, Size: size, Format: wgpu.TextureFormatRGBA8Unorm, MipLevelCount: 1, SampleCount: 1,
	})
	if err != nil {
		return nil, fmt.Errorf("render: create compare texture: %w", err)
	}
	err = r.queue.TryWriteTexture(
		&wgpu.TexelCopyTextureInfo{Texture: c.texture, Aspect: wgpu.TextureAspectAll},
		img.Pix,
		&wgpu.TexelCopyBufferLayout{BytesPerRow: uint32(img.Stride), RowsPerImage: uint32(h)},
		&size,
	)
	if err != nil {
		return nil, fmt.Errorf("render: upload compare texture: %w", err)
	}
	if c.view, err = c.texture.TryCreateView(nil); err != nil {
		return nil, fmt.Errorf("render: create compare view: %w", err)
	}
	layout := c.pipeline.GetBindGroupLayout(0)
	defer layout.Release()
	c.bindGroup, err = r.device.TryCreateBindGroup(&wgpu.BindGroupDescriptor{
		Label: "compare", Layout: layout, Entries: []wgpu.BindGroupEntry{{Binding: 0, TextureView: c.view}},
	})
	if err != nil {
		return nil, fmt.Errorf("render: create compare bind group: %w", err)
	}
	return c, nil
}

// compose joins four panels of w/2 by h/2 pixels: the GPU, the CPU, the difference
// with gain, and the pixels that fail the check.
func compose(got, want *image.RGBA, over []bool, w, h int) (*image.RGBA, error) {
	pw, ph := got.Rect.Dx(), got.Rect.Dy()
	diff := image.NewRGBA(got.Rect)
	fail := image.NewRGBA(got.Rect)
	for i := 0; i < len(diff.Pix); i += 4 {
		for c := range 3 {
			diff.Pix[i+c] = uint8(min(abs(int(got.Pix[i+c])-int(want.Pix[i+c]))*diffGain, 255))
		}
		diff.Pix[i+3] = 255
		v := [4]uint8{255, 255, 255, 255}
		if over[i/4] {
			v = [4]uint8{220, 20, 40, 255}
		}
		copy(fail.Pix[i:i+4], v[:])
	}

	out := image.NewRGBA(image.Rect(0, 0, w, h))
	panels := []struct {
		img   *image.RGBA
		x, y  int
		label string
	}{
		{got, 0, 0, "GPU"},
		{want, pw, 0, "CPU"},
		{diff, 0, ph, fmt.Sprintf("|GPU - CPU| x %d", diffGain)},
		{fail, pw, ph, fmt.Sprintf("over the tolerance of %d, after the range", Tolerance)},
	}
	for _, p := range panels {
		r := image.Rect(p.x, p.y, p.x+pw, p.y+ph)
		draw.Draw(out, r, p.img, image.Point{}, draw.Src)
		if err := scene.Label(out, float32(p.x+8), float32(p.y+24), p.label); err != nil {
			return nil, err
		}
	}
	return out, nil
}
