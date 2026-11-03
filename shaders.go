package slug

import _ "embed"

// CoreWGSL holds the coverage functions, without bindings or entry points. Put
// it in a shader of your own, and declare slug_curve, slug_band and slug_layer
// there. WGSL does not need a declaration before its first use.
//
//go:embed shaders/slug_core.wgsl
var CoreWGSL string

//go:embed shaders/slug_reference.wgsl
var referenceWGSL string

// ReferenceWGSL is a complete shader, which includes CoreWGSL. Its entry points
// are slug_vs and slug_fs.
var ReferenceWGSL = CoreWGSL + referenceWGSL
