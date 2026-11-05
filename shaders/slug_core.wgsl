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

// slug_segment_mean returns the premultiplied mean of a segment from f0 to f1. A straight
// mix has a product of two lines, so its mean has a term for the curve.
fn slug_segment_mean(a: vec4f, b: vec4f, f0: f32, f1: f32, premultiplied: bool) -> vec4f {
    let f = (f0 + f1) * 0.5;
    let alpha = a.a + (b.a - a.a) * f;
    if premultiplied {
        return vec4f(a.rgb * a.a + (b.rgb * b.a - a.rgb * a.a) * f, alpha);
    }
    let curve = (b.rgb - a.rgb) * (b.a - a.a) * (f1 - f0) * (f1 - f0) / 12.0;
    return vec4f((a.rgb + (b.rgb - a.rgb) * f) * alpha + curve, alpha);
}

fn slug_premultiply(c: vec4f) -> vec4f {
    return vec4f(c.rgb * c.a, c.a);
}

// slug_stops returns the premultiplied integral of the stops from a to b. Each segment
// adds only its own part, so a small range keeps its precision.
fn slug_stops(first: u32, info: u32, a: f32, b: f32) -> vec4f {
    let premultiplied = ((info >> 24u) & 1u) != 0u;
    var lo = bitcast<f32>(slug_band(first));
    var lo_color = slug_stop_color(first);
    var sum = vec4f(0.0);
    if a < lo {
        sum += slug_premultiply(lo_color) * (min(b, lo) - a);
    }
    for (var i = 1u; i < (info & 0xFFFFu); i++) {
        let word = first + i * SLUG_STOP_WORDS;
        let hi = bitcast<f32>(slug_band(word));
        let hi_color = slug_stop_color(word);
        let x0 = max(a, lo);
        let x1 = min(b, hi);
        if x1 > x0 {
            // x1 is over x0, so the two offsets differ.
            let f0 = (x0 - lo) / (hi - lo);
            let f1 = (x1 - lo) / (hi - lo);
            sum += slug_segment_mean(lo_color, hi_color, f0, f1, premultiplied) * (x1 - x0);
        }
        lo = hi;
        lo_color = hi_color;
    }
    if b > lo {
        sum += slug_premultiply(lo_color) * (b - max(a, lo));
    }
    return sum;
}

// slug_unit returns the integral of the part from a to b of the unit that starts at n.
fn slug_unit(first: u32, info: u32, n: f32, a: f32, b: f32) -> vec4f {
    if ((info >> 16u) & 0xFFu) == SLUG_REFLECT && n - 2.0 * floor(n * 0.5) != 0.0 {
        return slug_stops(first, info, 1.0 - b, 1.0 - a);
    }
    return slug_stops(first, info, a, b);
}

// slug_spread returns the premultiplied integral of the gradient from lo to hi, with spread.
fn slug_spread(first: u32, info: u32, lo: f32, hi: f32) -> vec4f {
    let spread = (info >> 16u) & 0xFFu;
    if spread != SLUG_REPEAT && spread != SLUG_REFLECT {
        return slug_stops(first, info, lo, hi);
    }
    let n0 = floor(lo);
    let n1 = floor(hi);
    if n0 == n1 {
        return slug_unit(first, info, n0, lo - n0, hi - n0);
    }
    var sum = slug_unit(first, info, n0, lo - n0, 1.0) + slug_unit(first, info, n1, 0.0, hi - n1);
    if n1 - n0 > 1.0 {
        sum += slug_stops(first, info, 0.0, 1.0) * (n1 - n0 - 1.0);
    }
    return sum;
}

// slug_radial_pixel returns the t of a pixel, the width of its t range, and the part of
// the pixel inside the cone. The t is the larger t whose circle has a radius of at least
// 0 and passes through at. du and dv are the change of at for one pixel.
fn slug_radial_pixel(at: vec2f, du: vec2f, dv: vec2f, r0: f32, r1: f32, cx: f32) -> vec3f {
    let dr = r1 - r0;
    let a = cx * cx - dr * dr;
    var b = at.x * cx + r0 * dr;
    let c = dot(at, at) - r0 * r0;
    let db = length(vec2f(du.x, dv.x) * cx);

    var t: f32;
    var limit: f32; // the largest width, near the border of the cone
    var cover = 1.0;
    if abs(a) < SLUG_NEAR_ZERO {
        // One root, and the border of the cone is the line b = 0.
        let dir = select(1.0, -1.0, dr < 0.0);
        let side = b * dir;
        cover = saturate(0.5 + side / db);
        if cover <= 0.0 {
            return vec3f(0.0);
        }
        if side < 1e-6 {
            b = 1e-6 * dir;
        }
        t = c * 0.5 / b;
        limit = 1e30;
    } else {
        var d = b * b - a * c;
        // The change of d for one pixel.
        let g = length(2.0 * vec2f(b * cx * du.x - a * dot(at, du), b * cx * dv.x - a * dot(at, dv)));
        if a > 0.0 {
            // Nested circles fill the plane, so only a cone has a border.
            if g > 0.0 {
                cover = saturate(0.5 + d / g);
            } else if d < 0.0 {
                cover = 0.0;
            }
            if cover <= 0.0 {
                return vec3f(0.0);
            }
        }
        d = max(d, 0.0);
        let s = sqrt(d);
        let t1 = max((b + s) / a, (b - s) / a);
        let t2 = min((b + s) / a, (b - s) / a);
        t = t1;
        if r0 + t1 * dr < 0.0 {
            if r0 + t2 * dr < 0.0 {
                return vec3f(0.0);
            }
            t = t2;
        }
        limit = (sqrt(d + g * 0.5) - sqrt(max(d - g * 0.5, 0.0)) + db) / abs(a);
    }

    // The gradient of t is (at - center) / (b - a*t). It has no limit at the border.
    var w = limit;
    let slope = abs(b - a * t);
    if slope > 0.0 {
        let q = vec2f(at.x - t * cx, at.y);
        w = min(limit, length(vec2f(dot(q, du), dot(q, dv))) / slope);
    }
    return vec3f(t, w, cover);
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

// slug_paint returns the straight color of layer i at coord, in em space, filtered over
// one pixel. pixels_per_em is the value of slug_coverage. current is the color of a
// CurrentColor layer. A host that does not call it draws a gradient with its own color.
//
// The color is the mean of the stops over the t range of the pixel, in closed form. A
// jump of color then has no steps, and the cost does not depend on the jump.
fn slug_paint(i: u32, l: SlugLayer, coord: vec2f, pixels_per_em: vec2f, current: vec4f) -> vec4f {
    if l.kind == SLUG_SOLID {
        return l.color;
    }
    if l.kind < SLUG_LINEAR || l.kind > SLUG_SWEEP {
        return current;
    }
    let r = i * 24u;
    let first = slug_layer(r + 12u);
    let info = slug_layer(r + 13u);
    let a = slug_layer_f32(r + 14u);
    let b = slug_layer_f32(r + 15u);
    let cx = slug_layer_f32(r + 22u);
    let m0 = vec3f(slug_layer_f32(r + 16u), slug_layer_f32(r + 18u), slug_layer_f32(r + 20u));
    let m1 = vec3f(slug_layer_f32(r + 17u), slug_layer_f32(r + 19u), slug_layer_f32(r + 21u));

    let at = vec2f(dot(m0, vec3f(coord, 1.0)), dot(m1, vec3f(coord, 1.0)));
    let ems = 1.0 / pixels_per_em;
    // The change of at for one pixel in x, and for one pixel in y.
    let du = vec2f(m0.x, m1.x) * ems.x;
    let dv = vec2f(m0.y, m1.y) * ems.y;

    var t = at.x;
    var w = length(vec2f(du.x, dv.x));
    var period = 0.0;
    var cover = 1.0;
    if l.kind == SLUG_RADIAL {
        let p = slug_radial_pixel(at, du, dv, a, b, cx);
        t = p.x;
        w = p.y;
        cover = p.z;
        if cover <= 0.0 {
            return vec4f(0.0);
        }
    } else if l.kind == SLUG_SWEEP {
        let span = abs(b - a);
        t = slug_sweep_t(at, a, b);
        period = SLUG_TAU / span;
        w = period;
        let r2 = dot(at, at);
        if r2 > 0.0 {
            w = length(vec2f(at.x * du.y - at.y * du.x, at.x * dv.y - at.y * dv.x)) / r2 / span;
        }
    }

    // The range keeps a width at each scale of t.
    w = max(w, abs(t) / 2097152.0 + 1e-20);
    var lo = t - w * 0.5;
    var hi = t + w * 0.5;
    var sum: vec4f;
    if period > 0.0 && w >= period {
        lo = 0.0;
        hi = period;
        sum = slug_spread(first, info, lo, hi);
    } else if period > 0.0 && lo < 0.0 { // the seam of a sweep
        sum = slug_spread(first, info, lo + period, period) + slug_spread(first, info, 0.0, hi);
    } else if period > 0.0 && hi > period {
        sum = slug_spread(first, info, lo, period) + slug_spread(first, info, 0.0, hi - period);
    } else {
        sum = slug_spread(first, info, lo, hi);
    }
    if sum.a <= 0.0 {
        return vec4f(0.0);
    }
    var rgb = sum.rgb / sum.a;
    if ((info >> 25u) & 1u) != 0u {
        rgb = slug_to_srgb(rgb);
    }
    return vec4f(rgb, sum.a / (hi - lo) * cover);
}

// slug_to_srgb is the transfer function of sRGB, from linear light.
fn slug_to_srgb(v: vec3f) -> vec3f {
    let low = v * 12.92;
    let high = 1.055 * pow(max(v, vec3f(0.0031308)), vec3f(1.0 / 2.4)) - 0.055;
    return select(high, low, v <= vec3f(0.0031308));
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
