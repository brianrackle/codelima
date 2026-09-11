//go:build cgo && (darwin || linux)

package ghostty

/*
#include "ghostty_bridge_compat.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/brianrackle/codelima/internal/terminalstate"

	"go.rockorager.dev/vaxis"
)

func (t *ghosttyTUITerminal) Interact(request TerminalInteractionRequest) (TerminalInteractionResult, error) {
	if err := validateTerminalInteraction(request); err != nil {
		return TerminalInteractionResult{}, err
	}
	reply := make(chan interactionOutcome, 1)
	select {
	case t.commands <- actorEnvelope{cmd: cmdInteract{Request: request, Reply: reply}}:
	case <-t.actorDone:
		return TerminalInteractionResult{}, errTerminalClosed
	}
	select {
	case result := <-reply:
		return result.Result, result.Err
	case <-t.actorDone:
		return TerminalInteractionResult{}, errTerminalClosed
	}
}

func (t *ghosttyTUITerminal) applyInteraction(request TerminalInteractionRequest) (TerminalInteractionResult, error) {
	if err := validateTerminalInteraction(request); err != nil {
		return TerminalInteractionResult{}, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var output TerminalInteractionResult
	if t.closed || t.term == nil {
		return output, errTerminalClosed
	}
	var result C.GhosttyResult
	var status C.GhosttyBridgeSearchStatus
	switch request.Action {
	case "colors":
		if err := t.applyColorsLocked(request.Colors); err != nil {
			return output, err
		}
		result = C.GHOSTTY_SUCCESS
	case "search":
		result = C.ghostty_bridge_terminal_search_start(t.term, (*C.uint8_t)(unsafe.Pointer(unsafe.StringData(request.Query))), C.size_t(len(request.Query)))
		if result == C.GHOSTTY_SUCCESS {
			result = C.ghostty_bridge_terminal_search_tick(t.term, 2, &status)
		}
	case "tick":
		result = C.ghostty_bridge_terminal_search_tick(t.term, 2, &status)
	case "next", "previous":
		result = C.ghostty_bridge_terminal_search_move(t.term, C.bool(request.Action == "next"), &status)
	case "copy":
		result = C.GHOSTTY_SUCCESS
	default:
		action := C.GHOSTTY_BRIDGE_SELECTION_PRESS
		switch request.Action {
		case "drag":
			action = C.GHOSTTY_BRIDGE_SELECTION_DRAG
		case "release":
			action = C.GHOSTTY_BRIDGE_SELECTION_RELEASE
		case "cancel":
			action = C.GHOSTTY_BRIDGE_SELECTION_CANCEL
		}
		behavior := C.GHOSTTY_SELECTION_GESTURE_BEHAVIOR_CELL
		switch request.Kind {
		case "word":
			behavior = C.GHOSTTY_SELECTION_GESTURE_BEHAVIOR_WORD
		case "line":
			behavior = C.GHOSTTY_SELECTION_GESTURE_BEHAVIOR_LINE
		case "output":
			behavior = C.GHOSTTY_SELECTION_GESTURE_BEHAVIOR_OUTPUT
		}
		result = C.ghostty_bridge_terminal_selection_event(t.term, C.GhosttyBridgeSelectionAction(action), C.int(request.Col), C.int(request.Row), C.GhosttySelectionGestureBehavior(behavior), C.bool(request.Rectangle))
	}
	if result != C.GHOSTTY_SUCCESS {
		return output, fmt.Errorf("terminal %s: native result %d", request.Action, int(result))
	}
	if request.Action == "copy" || request.Action == "release" {
		var data *C.uint8_t
		var length C.size_t
		result = C.ghostty_bridge_terminal_selection_format(t.term, C.size_t(terminalMaxPasteBytes), &data, &length) //nolint:gocritic // cgo-generated pointer-safety expression.
		if data != nil {
			defer C.ghostty_bridge_free(unsafe.Pointer(data))
		}
		// A plain click clears selection and releases without a drag range.
		// This is a normal gesture, not a failed clipboard write.
		if request.Action == "release" && result == C.GHOSTTY_NO_VALUE {
			result = C.GHOSTTY_SUCCESS
		}
		if result != C.GHOSTTY_SUCCESS {
			return output, fmt.Errorf("copy terminal selection: native result %d", int(result))
		}
		output.Text = C.GoStringN((*C.char)(unsafe.Pointer(data)), C.int(length))
	}
	output.Search = TerminalSearchStatus{Matches: uint64(status.total_matches), Selected: uint64(status.selected_index), HasSelected: bool(status.has_selected), CaughtUp: bool(status.caught_up)}
	if request.Action != "tick" && request.Action != "copy" {
		t.generation++
		t.invalidateLocked()
	}
	return output, nil
}

func (t *ghosttyTUITerminal) applyColorsLocked(colors *TerminalColors) error {
	return t.applyColorsPolicyLocked(colors, true)
}

func (t *ghosttyTUITerminal) applyColorsPolicyLocked(colors *TerminalColors, report bool) error {
	if colors == nil {
		return errors.New("invalid terminal default colors")
	}
	if err := terminalstate.ValidateColors(colors); err != nil {
		return err
	}
	color := func(value *uint32) *C.GhosttyColorRgb {
		if value == nil {
			return nil
		}
		return &C.GhosttyColorRgb{r: C.uint8_t(*value >> 16), g: C.uint8_t(*value >> 8), b: C.uint8_t(*value)}
	}
	var palette *C.GhosttyColorRgb
	var values [256]C.GhosttyColorRgb
	if len(colors.Palette) != 0 {
		for index, value := range colors.Palette {
			values[index] = *color(&value)
		}
		palette = &values[0]
	}
	result := C.ghostty_bridge_terminal_set_default_colors(t.term, color(colors.Foreground), color(colors.Background), color(colors.Cursor), palette)
	if result != C.GHOSTTY_SUCCESS {
		return fmt.Errorf("terminal default colors: native result %d", int(result))
	}
	t.setColorThemeModeLocked(vaxis.ColorThemeMode(colors.Theme))
	if report && t.getModeLocked(ghosttyModeColorScheme, false) {
		t.reportColorThemeModeLocked()
	}
	if err := t.checkNativeErrorLockedRaw("default colors"); err != nil {
		return err
	}
	t.defaultColors = terminalstate.CloneColors(colors)
	return nil
}
