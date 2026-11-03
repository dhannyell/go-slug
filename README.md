# go-slug

A Go library that prepares vector outlines (font glyphs, icons and shapes)
for GPU rendering with the Slug algorithm. It has no graphics API dependency:
it produces data buffers and WGSL shader code that any WebGPU host can use.

> Status: early development. The API is not stable.

## Shaders

`slug.ReferenceWGSL` is a complete shader over three storage buffers: the three
arrays of a `Pack`, which [FORMAT.md](FORMAT.md) describes. `slug.CoreWGSL` holds
only the functions, for a shader of your own. It calls `slug_curve`, `slug_band`
and `slug_layer`, which you declare.

`Pack.Rasterize` is the CPU copy of the shader, and its oracle. The test in
[samples/internal/render](samples/internal/render) compares the two.

## Text

[text](text) is an optional module that shapes a string into glyphs, with kerning,
ligatures, right-to-left text and line breaks. It is separate because it depends
on [go-text/typesetting](https://github.com/go-text/typesetting), and the core
needs no shaper.

```go
f, err := text.Parse(fontData, 0)
for _, line := range f.Shape("office", text.Options{}) {
	for _, g := range line.Glyphs {
		entry, err := f.Add(&pack, g)
		// Draw entry with g.At(size), then move by the baseline of the line.
	}
}
```

`Shape` uses one font for all of the string. A font fallback is the work of the
caller.

## Samples

[samples](samples) is a separate module, so the library has no GPU dependency.
These commands run in the `samples` directory.

Draw the scenes in a window:

```bash
go run ./cmd/native
```

Build the browser host:

```bash
CGO_ENABLED=0 GOOS=js GOARCH=wasm go build -o web/app.wasm ./cmd/web && cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/
```

Serve it at `http://localhost:8092`:

```bash
go run ./cmd/serve
```

The two hosts show the same scenes and views. In a window, the keys select them.
In a browser, the address does, for example `/?scene=zoom&mode=debug`.

| Key | Address | Shows |
| --- | --- | --- |
| 1 | `scene=overview` | text, transforms, fill rules and layers |
| 2 | `scene=zoom` | a continuous zoom, up to 256 times |
| 3 | `scene=perspective` | a plane that turns in perspective |
| 4 | `scene=text` | ligatures, Arabic, mixed directions and line breaks |
| D | `mode=debug` | the curves in each band, the band borders and the control points |
| C | `mode=compare` | the GPU, the CPU, their difference and the pixels over the tolerance |

## Credits

The rendering algorithm is Slug, by Eric Lengyel:

- Eric Lengyel, "GPU-Centered Font Rendering Directly from Glyph Outlines",
  *Journal of Computer Graphics Techniques*, vol. 6, no. 2, 2017.
  <https://jcgt.org/published/0006/02/02/>
- Reference shaders: <https://github.com/EricLengyel/Slug>

The WGSL shaders and the CPU coverage evaluator in this repository derive from
the reference shaders. Slug shader code Copyright 2017 by Eric Lengyel, used
under the MIT License (see [LICENSES/Slug-MIT.txt](LICENSES/Slug-MIT.txt)).
The Slug patent has been dedicated to the public domain.

The `text` scene uses the Amiri font, under the SIL Open Font License (see
[samples/internal/scene/fonts/OFL.txt](samples/internal/scene/fonts/OFL.txt)).

## License

go-slug is available under the MIT License. See [LICENSE](LICENSE) and
[NOTICE](NOTICE).
