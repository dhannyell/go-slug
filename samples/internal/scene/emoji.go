package scene

import (
	_ "embed"

	"github.com/dhannyell/go-slug/text"
)

// emojiFont is 16 glyphs of Noto Color Emoji, kept small for the wasm demo.
//
//go:embed fonts/NotoColorEmoji-Subset.ttf
var emojiFont []byte

// emoji draws the color glyphs of a COLR font at three sizes.
func (s *Scene) emoji() error {
	f, err := text.Parse(emojiFont, sourceEmoji)
	if err != nil {
		return err
	}
	rows := []struct {
		str        string
		size, x, y float32
	}{
		{"😀🌈🔥🎨🌍🍉🦊🐙", 88, 40, 120},
		{"🌸⭐💎🚀🍩🎈🌻🐳", 88, 40, 240},
		{"😀🌈🔥🎨🌍🍉🦊🐙🌸⭐💎🚀🍩🎈🌻🐳", 32, 40, 320},
		{"🦊🐙🌻", 150, 40, 500},
	}
	for _, r := range rows {
		if err := s.line(f, r.str, text.Options{}, r.size, r.x, r.y, ink); err != nil {
			return err
		}
	}
	return nil
}
