# go-slug

A Go library that prepares vector outlines (font glyphs, icons and shapes)
for GPU rendering with the Slug algorithm. It has no graphics API dependency:
it produces data buffers and WGSL shader code that any WebGPU host can use.

> Status: early development. The API is not stable.

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
