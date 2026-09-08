package terminalstate

import (
	"errors"
	"unicode/utf8"
)

type InteractionRequest struct {
	Action    string  `json:"action"`
	Kind      string  `json:"kind,omitempty"`
	Col       int     `json:"col,omitempty"`
	Row       int     `json:"row,omitempty"`
	Rectangle bool    `json:"rectangle,omitempty"`
	Query     string  `json:"query,omitempty"`
	Colors    *Colors `json:"colors,omitempty"`
}

type Colors struct {
	Foreground *uint32  `json:"foreground,omitempty"`
	Background *uint32  `json:"background,omitempty"`
	Cursor     *uint32  `json:"cursor,omitempty"`
	Palette    []uint32 `json:"palette,omitempty"`
	Theme      int      `json:"theme,omitempty"`
}

type SearchStatus struct {
	Matches     uint64 `json:"matches"`
	Selected    uint64 `json:"selected"`
	HasSelected bool   `json:"has_selected"`
	CaughtUp    bool   `json:"caught_up"`
}

type InteractionResult struct {
	Text   string       `json:"text,omitempty"`
	Search SearchStatus `json:"search"`
}

func ValidateInteraction(request InteractionRequest) error {
	// Validate in Go's full-width integer domain before any native C cast.
	// Out-of-viewport drags may still clamp natively, but wire coordinates
	// cannot be negative or exceed the terminal's uint16 coordinate domain.
	if request.Col < 0 || request.Col > 65535 || request.Row < 0 || request.Row > 65535 {
		return errors.New("terminal interaction coordinates must be between 0 and 65535")
	}
	if err := ValidateColors(request.Colors); err != nil {
		return err
	}
	if len(request.Query) > 4096 || !utf8.ValidString(request.Query) {
		return errors.New("search query must be valid UTF-8 of at most 4096 bytes")
	}
	switch request.Action {
	case "press", "drag", "release", "cancel", "copy", "search", "tick", "next", "previous", "colors":
	default:
		return errors.New("invalid terminal interaction action")
	}
	switch request.Kind {
	case "", "cell", "word", "line", "output":
	default:
		return errors.New("invalid terminal selection kind")
	}
	return nil
}
