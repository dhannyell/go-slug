# go-slug data format

A `Pack` holds three GPU arrays and one table. This document describes the
arrays, and the bytes of `Pack.Encode`. The current version is **2**. Version 2 adds the gradient
paint kinds. A reader of version 2 also reads version 1.

All coordinates are in em space, Y-up: drawing units divided by `Drawing.Em`. A
host gets a position in pixels when it multiplies a coordinate by the size of
the em in pixels. All words are 32 bits.

## GPU arrays

The addresses are linear. A host can upload each array as a storage buffer, or
as a texture with a fixed width `W`, where element `i` is at
`(i % W, i / W)`.

| Array | Element | Suggested texture format | Address unit |
|---|---|---|---|
| `Curves()` | texel: 4 `float32` | `rgba32float` | texel |
| `Bands()` | word: `uint32` | `r32uint` | word |
| `Layers()` | record: 24 words (`LayerWords`), `uint32` or `float32` bits | `rgba32uint`, 6 texels per record | record |

The arrays only grow. Existing elements never change. `Pack.TakeDirty` returns
the new elements as indices into the Go slices (`float32` or `uint32` values,
not texels or records).

### Curves

One quadratic Bézier uses two consecutive texels:

| Texel | x, y | z, w |
|---|---|---|
| `loc` | P0 | P1 |
| `loc + 1` | P2 | P1 of the next curve when it shares this point; otherwise 0, 0 |

When a curve starts at the end of the previous curve, the `loc + 1` texel of the
previous curve is the `loc` texel of the new curve. A reader must not assume
that curves are in any order or that they share texels.

### Bands

A layer has `nH` horizontal bands and `nV` vertical bands. All bands on one axis
have the same thickness. The data starts at the word `bandLoc` of the layer:

| Words after `bandLoc` | Content |
|---|---|
| `2 * nH` | horizontal band headers: `count`, `offset` |
| `2 * nV` | vertical band headers: `count`, `offset` |
| the rest | curve lists |

`Bands()` also holds the stops of gradients. A layer record has their address, and a
reader of band lists does not reach them.

The list of a band starts at the word `bandLoc + offset`. It has `count` words.
Each word is the `loc` texel of a curve.

- Horizontal bands cut the Y axis. They serve rays along X. Their lists are in
  descending order of the maximum X of P0, P1 and P2.
- Vertical bands cut the X axis. They serve rays along Y. Their lists are in
  descending order of the maximum Y of P0, P1 and P2.
- A curve with a constant Y is not in the horizontal bands. A curve with a
  constant X is not in the vertical bands.
- Two headers can point to the same list. A reader must not assume that lists
  are unique.

To find the bands of a coordinate `(x, y)`:

```text
h = clamp(int(y * scaleY + offsetY), 0, nH - 1)
v = clamp(int(x * scaleX + offsetX), 0, nV - 1)
```

### Layer record

| Word | Field | Content |
|---|---|---|
| 0 | `bandLoc` | first word of the band data |
| 1 | `bandCounts` | `nH` in bits 0-15, `nV` in bits 16-31 |
| 2 | `flags` | bits 0-7: fill rule (0 non-zero, 1 even-odd); bits 8-15: paint kind (0 current color, 1 solid, 2 linear gradient, 3 radial gradient, 4 sweep gradient); bits 16-23: blend, **reserved**, 0; bits 24-31: **reserved**, 0 |
| 3 | `clipRef` | **reserved**, 0 |
| 4-7 | banding (`float32`) | `scaleX`, `scaleY`, `offsetX`, `offsetY` |
| 8-11 | bounds (`float32`) | min x, min y, max x, max y |
| 12-15 | color (`float32`) | red, green, blue, alpha; straight RGB in sRGB, linear alpha; only a solid paint reads it |
| 16-21 | transform (`float32`) | **reserved**, the identity: 1, 0, 0, 1, 0, 0. The curves already include `Layer.Transform` |
| 22-23 | | **reserved**, 0 |

A layer with a gradient kind uses words 12 to 22 in a different way:

| Word | Field | Content |
|---|---|---|
| 12 | `stops` | the word in `Bands()` of the first stop |
| 13 | `info` | stop count in bits 0-15; spread in bits 16-23 (0 pad, 1 repeat, 2 reflect); bit 24: mix with premultiplied alpha; bit 25: the stop colors are in linear light, and the result goes back to sRGB |
| 14-15 | `a`, `b` (`float32`) | radial: `r0`, `r1`; sweep: `start`, `end`, in radians; linear: 0 |
| 16-21 | `toPaint` (`float32`) | the matrix a, b, c, d, e, f from em space to paint space. It includes `Layer.Transform` |
| 22 | `cx` (`float32`) | radial: 0 or 1; else 0 |

### Stops

One stop is 5 words in `Bands()`, as `float32` bits: offset, red, green, blue, alpha.
The color is straight RGB in sRGB, with linear alpha. The offsets do not fall. Colors
mix in sRGB.

### Gradient evaluation

A reader finds `p = toPaint * coord`, then `t` from the kind:

| Kind | `t` |
|---|---|
| linear | `p.x` |
| radial | the larger root of the circles with center `(t * cx, 0)` and radius `a + t * (b - a)` that has a radius of at least 0. If there is no such root, the color is transparent |
| sweep | the angle of `p` from `a` in the direction of `b`, from 0 to one turn, divided by `abs(b - a)` |

Then the spread changes `t`. Repeat and reflect use the range 0 to 1. Pad does not
change `t`. Below the first offset, the color is the first stop. Over the last offset,
it is the last stop. Between two stops, the color mixes in a straight line.

A reader can filter over a pixel, so an edge between two colors is smooth. `slug_paint`
and `Pack.Rasterize` take the mean of the stops over the `t` range of the pixel. The
width of the range is the length of the gradient of `t` over one pixel. The mean uses
premultiplied alpha. The border of a radial cone sets the alpha from the distance to
the border.

A writer does not write a gradient that has one stop or no direction. It writes a solid
paint with the color of the last stop.

A writer sets reserved fields to the given values. A reader ignores them.

## Encoded bytes

All values are little-endian.

```text
"SLUG"           4 bytes
version          uint32
section...       until the end of the data
```

Each section:

```text
id               uint32
size             uint32, in bytes, a multiple of 4
payload          size bytes
```

| ID | Section | Payload |
|---|---|---|
| 1 | curves | the `Curves()` array |
| 2 | bands | the `Bands()` array |
| 3 | layers | the `Layers()` array |
| 4 | entries | 8 words per entry: key source, key ID, first layer, layer count, then bounds as 4 `float32` (min x, min y, max x, max y) |
| 5 | stamps | optional; 9 words per stamp: key source, then the 32 bytes of `Pack.Stamp`, in ascending order of source |

Sections 1 to 4 must all be present, one time each. Section 5 is optional, and a
source has at most one stamp. A reader skips an unknown section ID, so an older
reader of version 2 skips section 5. A change that an old reader cannot ignore
needs a new version.

The bytes are not portable as a golden file. Some CPUs fuse float operations,
so a cubic outline can differ in the last bits between architectures. A decoded
pack is valid on every architecture.
