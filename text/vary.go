package text

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"

	gofont "github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/font/opentype/tables"
	"github.com/go-text/typesetting/shaping"
)

// Axis is one design axis of a variable font, such as the weight "wght".
type Axis struct {
	Tag               string
	Min, Default, Max float32
}

// Variation sets one axis of a variable font to Value, in the units of the axis.
type Variation struct {
	Tag   string // four letters, such as "wght"
	Value float32
}

// Axes returns the axes of a variable font. It is nil for a font without axes.
func (f *Font) Axes() []Axis {
	return f.parsed().axes
}

// readAxes reads the fvar table. A font without the table has no axes.
func readAxes(data []byte) []Axis {
	ld, err := ot.NewLoader(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	raw, err := ld.RawTable(ot.MustNewTag("fvar"))
	if err != nil {
		return nil
	}
	fvar, _, err := tables.ParseFvar(raw)
	if err != nil {
		return nil
	}
	var axes []Axis
	for _, a := range fvar.FvarRecords.Axis {
		axes = append(axes, Axis{Tag: a.Tag.String(), Min: a.Minimum, Default: a.Default, Max: a.Maximum})
	}
	return axes
}

// Vary returns a font with the outlines, advances, metrics and colors at the values of
// v. An axis that v does not set has its default value, and the font clamps each value
// to its axis. Vary returns an error for a tag that is not an axis of the font.
//
// source is the Key.Source of the new font in a Pack. It must differ from the source
// of f, because the two have different outlines for one ID. Vary panics if it does
// not. The new font keeps the style and the palette of f.
func (f *Font) Vary(v []Variation, source uint32) (*Font, error) {
	vars := make([]gofont.Variation, len(v))
	for i, x := range v {
		if !f.hasAxis(x.Tag) {
			return nil, fmt.Errorf("text: the font has no axis %q", x.Tag)
		}
		vars[i] = gofont.Variation{Tag: ot.MustNewTag(x.Tag), Value: x.Value}
	}
	if source == f.source {
		panic("text: Vary needs a different source")
	}
	g := f.variant(source, f.style, f.palette)
	g.face.SetVariations(vars)
	return g, nil
}

func (f *Font) hasAxis(tag string) bool {
	for _, a := range f.Axes() {
		if a.Tag == tag {
			return true
		}
	}
	return false
}

// Features turns OpenType features of the font on or off, for example small capitals or
// old-style figures. The zero value keeps the defaults of the font. Make one with
// ParseFeatures.
type Features struct {
	packed string // a tag and a value for each feature, 8 bytes each; a string keeps Options comparable
}

// ParseFeatures reads features separated by commas. "smcp" or "+smcp" turns small
// capitals on, "liga=0" or "-liga" turns ligatures off, and "salt=2" selects the second
// alternate. A tag has 1 to 4 characters. A later feature replaces an earlier one with
// the same tag.
func ParseFeatures(s string) (Features, error) {
	var b []byte
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		f, err := parseFeature(part)
		if err != nil {
			return Features{}, fmt.Errorf("text: feature %q: %w", part, err)
		}
		b = binary.LittleEndian.AppendUint32(b, uint32(f.Tag))
		b = binary.LittleEndian.AppendUint32(b, f.Value)
	}
	return Features{packed: string(b)}, nil
}

func parseFeature(s string) (shaping.FontFeature, error) {
	value, sign := uint32(1), s[0] == '+' || s[0] == '-'
	if sign {
		if s[0] == '-' {
			value = 0
		}
		s = s[1:]
	}
	tag, v, hasValue := strings.Cut(s, "=")
	tag = strings.TrimSpace(tag)
	if hasValue {
		if sign {
			return shaping.FontFeature{}, errors.New("a sign and a value")
		}
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 32)
		if err != nil {
			return shaping.FontFeature{}, errors.New("the value is not a whole number")
		}
		value = uint32(n)
	}
	if len(tag) < 1 || len(tag) > 4 || strings.IndexFunc(tag, func(r rune) bool { return r < '!' || r > '~' }) >= 0 {
		return shaping.FontFeature{}, errors.New("a tag has 1 to 4 characters")
	}
	// A short tag ends with spaces.
	return shaping.FontFeature{Tag: ot.MustNewTag(tag + "   "[:4-len(tag)]), Value: value}, nil
}

// list returns the features for the shaper.
func (fs Features) list() []shaping.FontFeature {
	if fs.packed == "" {
		return nil
	}
	out := make([]shaping.FontFeature, len(fs.packed)/8)
	for i := range out {
		r := fs.packed[8*i:]
		out[i] = shaping.FontFeature{
			Tag:   ot.Tag(binary.LittleEndian.Uint32([]byte(r[:4]))),
			Value: binary.LittleEndian.Uint32([]byte(r[4:8])),
		}
	}
	return out
}

// String returns the features in the syntax of ParseFeatures.
func (fs Features) String() string {
	var parts []string
	for _, f := range fs.list() {
		parts = append(parts, fmt.Sprintf("%s=%d", f.Tag, f.Value))
	}
	return strings.Join(parts, ",")
}
