// Command slugbake writes the glyphs of a font into an encoded slug.Pack. A program
// decodes the file at the start and skips the work of Add for each baked glyph.
//
//	slugbake -font Inter.ttf -source 1 -runes 20-7E,A0-FF -text menu.txt -o ui.slug
//
// The file is larger than the font. Bake when the time at the start costs more than
// the bytes, for example on a desktop; on the web, a Warmer at run time is often
// better. Run slugbake again with -in to add another font to the same file.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	slug "github.com/dhannyell/go-slug"
	"github.com/dhannyell/go-slug/text"
)

func main() {
	fontPath := flag.String("font", "", "the TrueType or OpenType font (required)")
	source := flag.Uint("source", 0, "the Key.Source of the font in the program")
	runes := flag.String("runes", "", "hexadecimal code point ranges, such as 20-7E,A0-FF")
	textPath := flag.String("text", "", "a UTF-8 file with the text to bake, shaped as one paragraph for each line")
	bold := flag.Float64("bold", 0, "Style.Bold, in em")
	slant := flag.Float64("slant", 0, "Style.Slant")
	outline := flag.Float64("outline", 0, "Style.Outline, in em")
	palette := flag.Int("palette", 0, "the color palette of a COLR font")
	in := flag.String("in", "", "an encoded Pack to add to")
	out := flag.String("o", "", "the output file (required)")
	flag.Parse()
	if *fontPath == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*fontPath, uint32(*source), *runes, *textPath,
		text.Style{Bold: float32(*bold), Slant: float32(*slant), Outline: float32(*outline)},
		*palette, *in, *out); err != nil {
		fmt.Fprintln(os.Stderr, "slugbake:", err)
		os.Exit(1)
	}
}

func run(fontPath string, source uint32, runes, textPath string, style text.Style, palette int, in, out string) error {
	data, err := os.ReadFile(fontPath)
	if err != nil {
		return err
	}
	f, err := font(data, source, style, palette)
	if err != nil {
		return err
	}

	str, err := runeText(runes)
	if err != nil {
		return err
	}
	if textPath != "" {
		b, err := os.ReadFile(textPath)
		if err != nil {
			return err
		}
		str += "\n" + string(b)
	}

	p := new(slug.Pack)
	if in != "" {
		b, err := os.ReadFile(in)
		if err != nil {
			return err
		}
		if p, err = slug.Decode(b); err != nil {
			return err
		}
	}
	before := len(p.Entries())
	if err := f.Bake(p, str, text.Options{}); err != nil {
		// A glyph that fails stays out of the file; the program adds it at run time.
		fmt.Fprintln(os.Stderr, "slugbake: warning:", err)
	}
	encoded := p.Encode()
	if err := os.WriteFile(out, encoded, 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: %d new glyphs, %d in all, %d bytes\n", out, len(p.Entries())-before, len(p.Entries()), len(encoded))
	return nil
}

// font returns the font that the program uses at run time. Its stamp includes the
// style and the palette, so the program must make the same variant.
func font(data []byte, source uint32, style text.Style, palette int) (*text.Font, error) {
	if palette == 0 && style == (text.Style{}) {
		return text.Parse(data, source)
	}
	// Each variant needs a source that differs from its font; only the last one is in
	// the file.
	f, err := text.Parse(data, source+1)
	if err != nil {
		return nil, err
	}
	if palette != 0 {
		if f, err = f.Palette(palette, source+2); err != nil {
			return nil, err
		}
	}
	return f.Styled(style, source), nil
}

// runeText returns one of each rune in ranges such as "20-7E,A0-FF".
func runeText(ranges string) (string, error) {
	var b strings.Builder
	for part := range strings.SplitSeq(ranges, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, _ := strings.Cut(part, "-")
		if hi == "" {
			hi = lo
		}
		a, err := strconv.ParseUint(lo, 16, 32)
		if err != nil {
			return "", fmt.Errorf("range %q: %w", part, err)
		}
		z, err := strconv.ParseUint(hi, 16, 32)
		if err != nil || z < a || z > 0x10FFFF {
			return "", fmt.Errorf("range %q is not valid", part)
		}
		for r := a; r <= z; r++ {
			b.WriteRune(rune(r))
			b.WriteByte(' ') // shapes each rune alone
		}
	}
	return b.String(), nil
}
