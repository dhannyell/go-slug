// Slug coverage functions, without bindings or entry points.
// Derived from the Slug reference shaders.
// Copyright 2017 by Eric Lengyel. Used under the MIT License;
// see LICENSES/Slug-MIT.txt.
//
// The host declares how the shader reads the three arrays of FORMAT.md:
//
//   fn slug_curve(texel: u32) -> vec4f  // Pack.Curves, one texel
//   fn slug_band(word: u32) -> u32      // Pack.Bands, one word
//   fn slug_layer(word: u32) -> u32     // Pack.Layers, one word
//
// Storage buffers and textures both fit these functions.

const SLUG_NEAR_ZERO = 1.0 / 65536.0;

// A root that cannot contribute: its coverage and its weight are 0.
const SLUG_NO_ROOT = -1e10;

const SLUG_EVEN_ODD = 1u;
const SLUG_SOLID = 1u;
const SLUG_LINEAR = 2u;
const SLUG_RADIAL = 3u;
const SLUG_SWEEP = 4u;
const SLUG_REPEAT = 1u;
const SLUG_REFLECT = 2u;
const SLUG_STOP_WORDS = 5u;
const SLUG_TAU = 6.283185307179586;

struct SlugLayer {
    band_loc: u32,
    n_h: u32,
    n_v: u32,
    rule: u32,
    kind: u32,
    scale: vec2f,
    offset: vec2f,
    bounds: vec4f, // min x, min y, max x, max y
    color: vec4f,  // straight RGB in sRGB, linear alpha
}

fn slug_layer_f32(word: u32) -> f32 {
    return bitcast<f32>(slug_layer(word));
}

// slug_layer_at decodes record i of Pack.Layers.
fn slug_layer_at(i: u32) -> SlugLayer {
    let r = i * 24u;
    let bands = slug_layer(r + 1u);
    let flags = slug_layer(r + 2u);
    return SlugLayer(
        slug_layer(r),
        bands & 0xFFFFu,
        bands >> 16u,
        flags & 0xFFu,
        (flags >> 8u) & 0xFFu,
        vec2f(slug_layer_f32(r + 4u), slug_layer_f32(r + 5u)),
        vec2f(slug_layer_f32(r + 6u), slug_layer_f32(r + 7u)),
        vec4f(slug_layer_f32(r + 8u), slug_layer_f32(r + 9u), slug_layer_f32(r + 10u), slug_layer_f32(r + 11u)),
        vec4f(slug_layer_f32(r + 12u), slug_layer_f32(r + 13u), slug_layer_f32(r + 14u), slug_layer_f32(r + 15u)),
    );
}

// slug_root_code returns which roots contribute: bit 0 and bit 8. It reads the
// sign bits, so -0 counts as negative.
fn slug_root_code(y1: f32, y2: f32, y3: f32) -> u32 {
    let i1 = bitcast<u32>(y1) >> 31u;
    let i2 = bitcast<u32>(y2) >> 30u;
    let i3 = bitcast<u32>(y3) >> 29u;
    var shift = (i2 & 2u) | (i1 & ~2u);
    shift = (i3 & 4u) | (shift & ~4u);
    return (0x2E74u >> shift) & 0x0101u;
}

// slug_solve_poly returns the x of the two crossings of y = 0. Imaginary roots
// become a double root at the extreme of the curve. WGSL does not define a
// division by zero, so this function does not divide by zero.
fn slug_solve_poly(p1: vec2f, p2: vec2f, p3: vec2f) -> vec2f {
    let a = p1 - p2 * 2.0 + p3;
    let b = p1 - p2;
    var t: vec2f;
    if abs(a.y) < SLUG_NEAR_ZERO {
        if b.y == 0.0 {
            return vec2f(SLUG_NO_ROOT);
        }
        t = vec2f(p1.y * 0.5 / b.y);
    } else {
        let d = sqrt(max(b.y * b.y - a.y * p1.y, 0.0));
        t = vec2f(b.y - d, b.y + d) / a.y;
    }
    return (a.x * t - b.x * 2.0) * t + p1.x;
}

// slug_ray walks the curve list of one band. It returns the coverage in x and
// the weight in y. A vertical ray is a horizontal ray with X and Y swapped. The
// swap reverses the orientation, so its coverage has the opposite sign.
fn slug_ray(l: SlugLayer, band: u32, coord: vec2f, vertical: bool, pixels_per_unit: f32) -> vec2f {
    let count = slug_band(l.band_loc + 2u * band);
    let list = l.band_loc + slug_band(l.band_loc + 2u * band + 1u);
    var cov = 0.0;
    var wgt = 0.0;
    for (var i = 0u; i < count; i++) {
        let loc = slug_band(list + i);
        let c0 = slug_curve(loc);
        let c1 = slug_curve(loc + 1u);
        var p1 = c0.xy - coord;
        var p2 = c0.zw - coord;
        var p3 = c1.xy - coord;
        if vertical {
            p1 = p1.yx;
            p2 = p2.yx;
            p3 = p3.yx;
        }

        // The list is in descending order: no later curve can reach this pixel.
        if max(max(p1.x, p2.x), p3.x) * pixels_per_unit < -0.5 {
            break;
        }
        let code = slug_root_code(p1.y, p2.y, p3.y);
        if code == 0u {
            continue;
        }
        let r = slug_solve_poly(p1, p2, p3) * pixels_per_unit;
        if (code & 1u) != 0u {
            cov += saturate(r.x + 0.5);
            wgt = max(wgt, saturate(1.0 - abs(r.x) * 2.0));
        }
        if code > 1u {
            cov -= saturate(r.y + 0.5);
            wgt = max(wgt, saturate(1.0 - abs(r.y) * 2.0));
        }
    }
    if vertical {
        cov = -cov;
    }
    return vec2f(cov, wgt);
}

// slug_combine joins the two rays and applies the fill rule.
fn slug_combine(x: vec2f, y: vec2f, rule: u32) -> f32 {
    let c = max(abs(x.x * x.y + y.x * y.y) / max(x.y + y.y, SLUG_NEAR_ZERO), min(abs(x.x), abs(y.x)));
    if rule == SLUG_EVEN_ODD {
        let half = c * 0.5;
        return 1.0 - abs(1.0 - (half - floor(half)) * 2.0);
    }
    return saturate(c);
}

// slug_coverage returns the coverage of one layer at coord, in em space.
// pixels_per_em is 1 / fwidth(coord), which only a fragment entry point can
// compute. The vertical bands come after the horizontal bands.
fn slug_coverage(l: SlugLayer, coord: vec2f, pixels_per_em: vec2f) -> f32 {
    let band = coord * l.scale + l.offset;
    let h = u32(clamp(i32(band.y), 0, i32(l.n_h) - 1));
    let v = u32(clamp(i32(band.x), 0, i32(l.n_v) - 1));
    let x = slug_ray(l, h, coord, false, pixels_per_em.x);
    let y = slug_ray(l, l.n_h + v, coord, true, pixels_per_em.y);
    return slug_combine(x, y, l.rule);
}

// slug_stop_color returns the color of a stop, or of the first word of its color.
fn slug_stop_color(word: u32) -> vec4f {
    return vec4f(
        bitcast<f32>(slug_band(word + 1u)),
        bitcast<f32>(slug_band(word + 2u)),
        bitcast<f32>(slug_band(word + 3u)),
        bitcast<f32>(slug_band(word + 4u)),
    );
}

fn slug_mix_stops(a: vec4f, b: vec4f, f: f32, premultiplied: bool) -> vec4f {
    if !premultiplied {
        return a + (b - a) * f;
    }
    let alpha = a.a + (b.a - a.a) * f;
    if alpha <= 0.0 {
        return vec4f(0.0);
    }
    let rgb = a.rgb * a.a + (b.rgb * b.a - a.rgb * a.a) * f;
    return vec4f(rgb / alpha, alpha);
}

fn slug_gradient_stop(first: u32, count: u32, t: f32, premultiplied: bool) -> vec4f {
    var lo = bitcast<f32>(slug_band(first));
    var lo_color = slug_stop_color(first);
    if t <= lo {
        return lo_color;
    }
    for (var i = 1u; i < count; i++) {
        let word = first + i * SLUG_STOP_WORDS;
        let hi = bitcast<f32>(slug_band(word));
        let hi_color = slug_stop_color(word);
        if t <= hi {
            // t is over lo, so the two offsets differ.
            return slug_mix_stops(lo_color, hi_color, (t - lo) / (hi - lo), premultiplied);
        }
        lo = hi;
        lo_color = hi_color;
    }
    return lo_color;
}

// slug_radial_t returns the larger t whose circle has a radius of at least 0 and
// passes through at, in x. y is 1 if that circle exists.
fn slug_radial_t(at: vec2f, r0: f32, r1: f32, cx: f32) -> vec2f {
    let dr = r1 - r0;
    let a = cx * cx - dr * dr;
    let b = at.x * cx + r0 * dr;
    let c = dot(at, at) - r0 * r0;
    var t1: f32;
    var t2: f32;
    if abs(a) < SLUG_NEAR_ZERO {
        if b == 0.0 {
            return vec2f(0.0);
        }
        t1 = c * 0.5 / b;
        t2 = t1;
    } else {
        let d = b * b - a * c;
        if d < 0.0 {
            return vec2f(0.0);
        }
        let s = sqrt(d);
        t1 = max((b + s) / a, (b - s) / a);
        t2 = min((b + s) / a, (b - s) / a);
    }
    if r0 + t1 * dr >= 0.0 {
        return vec2f(t1, 1.0);
    }
    if r0 + t2 * dr >= 0.0 {
        return vec2f(t2, 1.0);
    }
    return vec2f(0.0);
}

// slug_sweep_t measures the angle from start in the direction of end, up to one turn.
fn slug_sweep_t(at: vec2f, start: f32, end: f32) -> f32 {
    // WGSL does not define atan2(0, 0).
    var angle = -start;
    if at.x != 0.0 || at.y != 0.0 {
        angle = atan2(at.y, at.x) - start;
    }
    var span = end - start;
    if span < 0.0 {
        angle = -angle;
        span = -span;
    }
    angle -= SLUG_TAU * floor(angle / SLUG_TAU);
    return angle / span;
}

// slug_paint returns the straight color of layer i at coord, in em space. current is
// the color of a CurrentColor layer. A host that does not call it draws a gradient
// with its own color.
fn slug_paint(i: u32, l: SlugLayer, coord: vec2f, current: vec4f) -> vec4f {
    if l.kind == SLUG_SOLID {
        return l.color;
    }
    if l.kind < SLUG_LINEAR || l.kind > SLUG_SWEEP {
        return current;
    }
    let r = i * 24u;
    let first = slug_layer(r + 12u);
    let info = slug_layer(r + 13u);
    let at = vec2f(
        slug_layer_f32(r + 16u) * coord.x + slug_layer_f32(r + 18u) * coord.y + slug_layer_f32(r + 20u),
        slug_layer_f32(r + 17u) * coord.x + slug_layer_f32(r + 19u) * coord.y + slug_layer_f32(r + 21u),
    );
    let a = slug_layer_f32(r + 14u);
    let b = slug_layer_f32(r + 15u);
    var t = at.x;
    if l.kind == SLUG_RADIAL {
        let radial = slug_radial_t(at, a, b, slug_layer_f32(r + 22u));
        if radial.y == 0.0 {
            return vec4f(0.0);
        }
        t = radial.x;
    } else if l.kind == SLUG_SWEEP {
        t = slug_sweep_t(at, a, b);
    }
    let spread = (info >> 16u) & 0xFFu;
    if spread == SLUG_REPEAT {
        t -= floor(t);
    } else if spread == SLUG_REFLECT {
        t -= 2.0 * floor(t * 0.5);
        if t > 1.0 {
            t = 2.0 - t;
        }
    }
    return slug_gradient_stop(first, info & 0xFFFFu, t, ((info >> 24u) & 1u) != 0u);
}

// slug_dilate moves a corner of a layer box out by half of fwidth(coord): a
// coverage is not 0 only inside this reach. A dilation by half a pixel, as in
// the reference, cuts the edge of a rotated or sheared layer.
// corner has a component of -1 or 1 for each axis. m0, m1 and m3 are rows 0, 1
// and 3 of the matrix from em space to clip space. dim is the viewport size in
// pixels. The result is in em space.
fn slug_dilate(pos: vec2f, corner: vec2f, m0: vec4f, m1: vec4f, m3: vec4f, dim: vec2f) -> vec2f {
    // The Jacobian of the pixel position at pos.
    let w = dot(m3.xy, pos) + m3.w;
    let jx = (m0.xy * w - m3.xy * (dot(m0.xy, pos) + m0.w)) * dim.x * 0.5 / (w * w);
    let jy = (m1.xy * w - m3.xy * (dot(m1.xy, pos) + m1.w)) * dim.y * 0.5 / (w * w);
    let det = jx.x * jy.y - jx.y * jy.x;

    // A singular matrix has no size in pixels to dilate by.
    if det == 0.0 {
        return pos;
    }
    let reach = vec2f(abs(jy.y) + abs(jx.y), abs(jy.x) + abs(jx.x)) * 0.5 / abs(det);
    return pos + corner * reach;
}
