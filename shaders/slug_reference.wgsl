// A complete shader over slug_core.wgsl. It draws one instanced quad per layer.
// Derived from the Slug reference shaders.
// Copyright 2017 by Eric Lengyel. Used under the MIT License;
// see LICENSES/Slug-MIT.txt.
//
// Draw 6 vertices per instance, as a triangle list. The result is premultiplied:
// blend it with One and OneMinusSrcAlpha into a target without an sRGB view, so
// the blend happens in sRGB, as Pack.Rasterize does.

struct SlugParams {
    clip_from_world: mat4x4f,
    viewport: vec2f, // in pixels
}

@group(0) @binding(0) var<uniform> slug_params: SlugParams;
@group(0) @binding(1) var<storage, read> slug_curves: array<vec4f>;
@group(0) @binding(2) var<storage, read> slug_bands: array<u32>;
@group(0) @binding(3) var<storage, read> slug_layers: array<u32>;

fn slug_curve(texel: u32) -> vec4f { return slug_curves[texel]; }
fn slug_band(word: u32) -> u32 { return slug_bands[word]; }
fn slug_layer(word: u32) -> u32 { return slug_layers[word]; }

struct SlugInstance {
    @location(0) linear: vec4f,    // Affine a, b, c, d: em space to world space
    @location(1) translate: vec2f, // Affine e, f
    @location(2) color: vec4f,     // straight RGB in sRGB, for a CurrentColor layer
    @location(3) layer: u32,
}

struct SlugVarying {
    @builtin(position) position: vec4f,
    @location(0) coord: vec2f,
    @location(1) color: vec4f,
    @location(2) @interpolate(flat) layer: u32,
}

@vertex
fn slug_vs(@builtin(vertex_index) vertex: u32, in: SlugInstance) -> SlugVarying {
    const CORNERS = array(
        vec2f(-1.0, -1.0), vec2f(1.0, -1.0), vec2f(1.0, 1.0),
        vec2f(-1.0, -1.0), vec2f(1.0, 1.0), vec2f(-1.0, 1.0),
    );
    let corner = CORNERS[vertex];
    let l = slug_layer_at(in.layer);
    let lo = l.bounds.xy;
    let hi = l.bounds.zw;
    let pos = (lo + hi) * 0.5 + (hi - lo) * 0.5 * corner;

    // Rows of the matrix from em space to clip space. The layer has z = 0.
    let world = transpose(slug_params.clip_from_world);
    var rows: array<vec4f, 4>;
    for (var i = 0u; i < 4u; i++) {
        let r = world[i];
        rows[i] = vec4f(
            r.x * in.linear.x + r.y * in.linear.y,
            r.x * in.linear.z + r.y * in.linear.w,
            0.0,
            r.x * in.translate.x + r.y * in.translate.y + r.w,
        );
    }

    let p = slug_dilate(pos, corner, rows[0], rows[1], rows[3], slug_params.viewport);
    var out: SlugVarying;
    out.position = vec4f(
        dot(rows[0].xy, p) + rows[0].w,
        dot(rows[1].xy, p) + rows[1].w,
        dot(rows[2].xy, p) + rows[2].w,
        dot(rows[3].xy, p) + rows[3].w,
    );
    out.coord = p;
    out.color = in.color;
    out.layer = in.layer;
    return out;
}

@fragment
fn slug_fs(in: SlugVarying) -> @location(0) vec4f {
    // At a very high zoom, f32 cannot show a change of coord in one pixel.
    let pixels_per_em = 1.0 / max(fwidth(in.coord), vec2f(1e-9));
    let l = slug_layer_at(in.layer);
    let color = slug_paint(in.layer, l, in.coord, in.color);
    let a = color.a * slug_coverage(l, in.coord, pixels_per_em);
    return vec4f(color.rgb * a, a);
}
