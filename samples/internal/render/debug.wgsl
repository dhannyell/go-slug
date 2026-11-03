// A fragment entry point that shows the data of a layer, over slug_vs. It uses
// the same functions as slug_fs.
//
// The color of a band is the count of curves that a pixel tests: blue is few, red
// is many. Lines mark the band borders. A green dot is a point on a curve, and an
// orange dot is a control point. A pixel sees only the points of its two bands, so
// a control point outside these bands can lose a part of its dot.

const DEBUG_MANY_CURVES = 24.0;

fn debug_heat(curves: f32) -> vec3f {
    let v = saturate(curves / DEBUG_MANY_CURVES);
    return mix(vec3f(0.65, 0.78, 0.95), vec3f(0.95, 0.30, 0.20), v);
}

// debug_dots returns the color of a dot at coord in one band, with alpha 0 for no dot.
fn debug_dots(l: SlugLayer, band: u32, coord: vec2f, pixels_per_em: vec2f) -> vec4f {
    const RADIUS = 3.0; // in pixels
    let count = slug_band(l.band_loc + 2u * band);
    let list = l.band_loc + slug_band(l.band_loc + 2u * band + 1u);
    var dot = vec4f(0.0);
    for (var i = 0u; i < count; i++) {
        let loc = slug_band(list + i);
        let c0 = slug_curve(loc);
        let c1 = slug_curve(loc + 1u);
        if length((c0.zw - coord) * pixels_per_em) < RADIUS {
            dot = vec4f(0.95, 0.55, 0.05, 1.0);
        }
        if length((c0.xy - coord) * pixels_per_em) < RADIUS || length((c1.xy - coord) * pixels_per_em) < RADIUS {
            return vec4f(0.05, 0.60, 0.25, 1.0);
        }
    }
    return dot;
}

@fragment
fn slug_debug_fs(in: SlugVarying) -> @location(0) vec4f {
    let pixels_per_em = 1.0 / max(fwidth(in.coord), vec2f(1e-9));
    let l = slug_layer_at(in.layer);
    let band = in.coord * l.scale + l.offset;
    let h = u32(clamp(i32(band.y), 0, i32(l.n_h) - 1));
    let v = u32(clamp(i32(band.x), 0, i32(l.n_v) - 1));
    let curves = slug_band(l.band_loc + 2u * h) + slug_band(l.band_loc + 2u * (l.n_h + v));

    var color = debug_heat(f32(curves));

    // The distance in pixels to the nearest band border.
    let to_border = abs(fract(band + 0.5) - 0.5) / max(l.scale, vec2f(1e-9)) * pixels_per_em;
    if min(to_border.x, to_border.y) < 0.5 {
        color *= 0.75;
    }

    let cov = slug_coverage(l, in.coord, pixels_per_em);
    color = mix(color, vec3f(0.05, 0.06, 0.10), cov * 0.8);

    var dots = debug_dots(l, h, in.coord, pixels_per_em);
    if dots.a == 0.0 {
        dots = debug_dots(l, l.n_h + v, in.coord, pixels_per_em);
    }
    return vec4f(mix(color, dots.rgb, dots.a), 1.0);
}
