package terminalstate

import (
	"errors"
	"slices"
)

// CloneColors detaches application-owned defaults from mutable RPC/request
// buffers. Nil means the terminal's built-in defaults remain in effect.
func CloneColors(colors *Colors) *Colors {
	if colors == nil {
		return nil
	}
	copyRGB := func(value *uint32) *uint32 {
		if value == nil {
			return nil
		}
		copy := *value
		return &copy
	}
	return &Colors{
		Foreground: copyRGB(colors.Foreground), Background: copyRGB(colors.Background), Cursor: copyRGB(colors.Cursor),
		Palette: slices.Clone(colors.Palette), Theme: colors.Theme,
	}
}

func ValidateColors(colors *Colors) error {
	if colors == nil {
		return nil
	}
	if (len(colors.Palette) != 0 && len(colors.Palette) != 256) || colors.Theme < 0 || colors.Theme > 2 {
		return errors.New("invalid terminal color policy")
	}
	for _, value := range []*uint32{colors.Foreground, colors.Background, colors.Cursor} {
		if value != nil && *value > 0xffffff {
			return errors.New("terminal color exceeds 24-bit RGB")
		}
	}
	for _, value := range colors.Palette {
		if value > 0xffffff {
			return errors.New("terminal palette color exceeds 24-bit RGB")
		}
	}
	return nil
}
