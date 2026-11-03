
// The traditional side of the versus view: a quad per glyph that reads a texture,
// and a plain rectangle per panel. It uses the matrices and the dilation of Slug.

@group(1) @binding(0) var versus_atlas: texture_2d<f32>;
@group(1) @binding(1) var versus_sampler: sampler;

struct VersusInstance {
    @location(0) linear: vec4f,
    @location(1) translate: vec2f,
    @location(2) color: vec4f,
    @location(3) bounds: vec4f, // in em space, with the padding of the cell
    @location(4) uv: vec3f,     // origin and scale; a scale of 0 is a plain rectangle
}

struct VersusVarying {
    @builtin(position) position: vec4f,
    @location(0) coord: vec2f,
    @location(1) color: vec4f,
    @location(2) @interpolate(flat) bounds: vec4f,
    @location(3) @interpolate(flat) uv: vec3f,
}

@vertex
fn versus_vs(@builtin(vertex_index) vertex: u32, in: VersusInstance) -> VersusVarying {
    const CORNERS = array(
        vec2f(-1.0, -1.0), vec2f(1.0, -1.0), vec2f(1.0, 1.0),
        vec2f(-1.0, -1.0), vec2f(1.0, 1.0), vec2f(-1.0, 1.0),
    );
    let corner = CORNERS[vertex];
    let lo = in.bounds.xy;
    let hi = in.bounds.zw;
    let pos = (lo + hi) * 0.5 + (hi - lo) * 0.5 * corner;

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
    var out: VersusVarying;
    out.position = vec4f(
        dot(rows[0].xy, p) + rows[0].w,
        dot(rows[1].xy, p) + rows[1].w,
        dot(rows[2].xy, p) + rows[2].w,
        dot(rows[3].xy, p) + rows[3].w,
    );
    out.coord = p;
    out.color = in.color;
    out.bounds = in.bounds;
    out.uv = in.uv;
    return out;
}

// versus_finish adds the color. A plain rectangle has an analytic edge, so the
// traditional side does not lose at the panels.
fn versus_finish(in: VersusVarying, coverage: f32, inside: vec2f) -> vec4f {
    var cov = coverage;
    if in.uv.z == 0.0 {
        cov = inside.x * inside.y;
    } else if inside.x <= 0.0 || inside.y <= 0.0 {
        // The dilation can leave the cell. There, the texture has the next glyph.
        cov = 0.0;
    }
    let a = in.color.a * cov;
    return vec4f(in.color.rgb * a, a);
}

// versus_bicubic reads the atlas with a cubic B-spline, in 4 bilinear reads. The
// outline of a bilinear distance field has a corner at each texel. The derivatives
// come from uv because the read positions jump at each texel.
fn versus_bicubic(uv: vec2f) -> f32 {
    let size = vec2f(textureDimensions(versus_atlas));
    let p = uv * size - 0.5;
    let f = fract(p);
    let i = p - f;
    let w0 = (1.0 - f) * (1.0 - f) * (1.0 - f) / 6.0;
    let w1 = (4.0 - 6.0 * f * f + 3.0 * f * f * f) / 6.0;
    let w3 = f * f * f / 6.0;
    let g0 = w0 + w1;
    let lo = (i + w1 / g0 - 0.5) / size;
    let hi = (i + w3 / (1.0 - g0) + 1.5) / size;
    let dx = dpdx(uv);
    let dy = dpdy(uv);
    let top = mix(
        textureSampleGrad(versus_atlas, versus_sampler, hi, dx, dy).r,
        textureSampleGrad(versus_atlas, versus_sampler, vec2f(lo.x, hi.y), dx, dy).r,
        g0.x,
    );
    let bottom = mix(
        textureSampleGrad(versus_atlas, versus_sampler, vec2f(hi.x, lo.y), dx, dy).r,
        textureSampleGrad(versus_atlas, versus_sampler, lo, dx, dy).r,
        g0.x,
    );
    return mix(top, bottom, g0.y);
}

fn versus_inside(in: VersusVarying) -> vec2f {
    let d = min(in.coord - in.bounds.xy, in.bounds.zw - in.coord);
    return saturate(d / max(fwidth(in.coord), vec2f(1e-9)) + 0.5);
}

@fragment
fn versus_bitmap_fs(in: VersusVarying) -> @location(0) vec4f {
    let texel = textureSample(versus_atlas, versus_sampler, in.uv.xy + in.coord * in.uv.z).r;
    return versus_finish(in, texel, versus_inside(in));
}

@fragment
fn versus_distance_fs(in: VersusVarying) -> @location(0) vec4f {
    let d = versus_bicubic(in.uv.xy + in.coord * in.uv.z);
    let width = max(fwidth(d), 1e-5);
    return versus_finish(in, saturate((d - 0.5) / width + 0.5), versus_inside(in));
}

@vertex
fn versus_line_vs(@builtin(vertex_index) vertex: u32) -> @builtin(position) vec4f {
    const CORNERS = array(vec2f(-1.0, -1.0), vec2f(3.0, -1.0), vec2f(-1.0, 3.0));
    return vec4f(CORNERS[vertex], 0.0, 1.0);
}

@fragment
fn versus_line_fs() -> @location(0) vec4f {
    return vec4f(0.62, 0.13, 0.20, 1.0);
}
