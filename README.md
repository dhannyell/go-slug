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

## Samples

[samples](samples) is a separate module, so the library has no GPU dependency.
These commands run in the `samples` directory.

Draw the scene in a window:

```bash
go run ./cmd/native
```

Build the browser host:

```bash
GOOS=js GOARCH=wasm go build -o web/app.wasm ./cmd/web && cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/
```

Serve it at `http://localhost:8092`. The address `/?check` shows the comparison
of the GPU with the CPU.

```bash
go run ./cmd/serve
```

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

## License

go-slug is available under the MIT License. See [LICENSE](LICENSE) and
[NOTICE](NOTICE).
