package codelima

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

type tuiTerminalMouseGesture struct {
	targetKey string
	startCol  int
	startRow  int
	dragged   bool
}

// tuiKeyBinding pairs a key matcher with the app action it triggers.
type tuiKeyBinding struct {
	match func(vaxis.Key) bool
	run   func(a *vaxisTUIApp) error
}

// tuiKeyBindings is THE table of terminal-scope shortcuts: handleKey
// dispatches from it and isTUITerminalPayloadKey derives payload-ness from
// the same entries, so the shortcut set and the payload predicate can never
// drift apart.
var tuiKeyBindings = []tuiKeyBinding{
	{match: isHostTerminalTabOpenKey, run: (*vaxisTUIApp).openHostTerminalTab},
	{match: isTerminalTabOpenKey, run: (*vaxisTUIApp).openTerminalTab},
	{match: isTerminalTabMoveNextKey, run: func(a *vaxisTUIApp) error { return a.moveTerminalTab(1) }},
	{match: isTerminalTabMovePreviousKey, run: func(a *vaxisTUIApp) error { return a.moveTerminalTab(-1) }},
	{match: isTerminalTabNextKey, run: func(a *vaxisTUIApp) error { return a.switchTerminalTab(1) }},
	{match: isTerminalTabPreviousKey, run: func(a *vaxisTUIApp) error { return a.switchTerminalTab(-1) }},
	{match: isTerminalTabCloseKey, run: (*vaxisTUIApp).closeTerminalTab},
	{match: isTerminalViewToggleKey, run: func(a *vaxisTUIApp) error { return a.state.toggleFocus() }},
}

// isTUITerminalPayloadKey reports whether a key is terminal input rather than
// a CodeLima shortcut: pasted input is always payload, and any key that
// matches no entry in tuiKeyBindings is payload. Payload input is redrawn
// only after the daemon publishes a fresh terminal snapshot; drawing
// immediately would repaint stale state.
func isTUITerminalPayloadKey(key vaxis.Key) bool {
	if key.EventType == vaxis.EventPaste {
		return true
	}
	for _, binding := range tuiKeyBindings {
		if binding.match(key) {
			return false
		}
	}
	return true
}

func (a *vaxisTUIApp) handleKey(key vaxis.Key) bool {
	// Pasted text is terminal payload, never a TUI shortcut. Vaxis emits one
	// EventPaste key per decoded input sequence between PasteStart/PasteEnd.
	if key.EventType == vaxis.EventPaste {
		if a.state.focus == tuiFocusTerminal {
			a.forwardTerminalEvent(key)
		}
		return false
	}

	for _, binding := range tuiKeyBindings {
		if !binding.match(key) {
			continue
		}
		if err := binding.run(a); err != nil {
			a.setStatus(slog.LevelError, err.Error())
			return false
		}
		a.clearStatus()
		a.syncSessionFocus()
		return false
	}

	// The info toggle is tree-scoped (in terminal focus a bare "i" is shell
	// payload), so it stays outside the terminal-scope binding table.
	if a.state.focus == tuiFocusTree && key.MatchString("i") {
		a.state.toggleTreePaneMode()
		a.clearStatus()
		a.syncSessionFocus()
		return false
	}

	if a.state.focus == tuiFocusTree && key.MatchString("m") {
		a.openMessagesView()
		return false
	}

	if a.state.focus == tuiFocusTerminal {
		a.forwardTerminalEvent(key)
		return false
	}

	if action, ok := a.matchAction(key); ok {
		if err := a.performAction(action); err != nil {
			a.setStatus(slog.LevelError, err.Error())
			return false
		}
		return false
	}

	var err error
	switch {
	case key.MatchString("q"), isQuitKey(key):
		return true
	case key.MatchString("Up"):
		err = a.state.moveSelection(-1)
	case key.MatchString("Down"):
		err = a.state.moveSelection(1)
	default:
		return false
	}

	if err == nil {
		err = a.ensureSelectedNodeTerminal()
	}
	if err != nil {
		a.setStatus(slog.LevelError, err.Error())
	} else {
		a.clearStatus()
	}
	a.syncSessionFocus()
	return false
}

func (a *vaxisTUIApp) matchAction(key vaxis.Key) (tuiActionSpec, bool) {
	if normalizedKeyModifiers(key.Modifiers) != 0 {
		return tuiActionSpec{}, false
	}

	pressed := []rune(strings.ToLower(key.Text))
	if len(pressed) == 0 {
		return tuiActionSpec{}, false
	}

	for _, action := range availableTUIActions(a.state.selectedEntry()) {
		if action.Hotkey == pressed[0] {
			return action, true
		}
	}

	return tuiActionSpec{}, false
}

func (a *vaxisTUIApp) actionResourceKeys(action tuiActionSpec, entry tuiTreeEntry) []string {
	switch action.ID {
	case tuiActionConfigurationManage:
		return []string{"configurations"}
	case tuiActionNodeCreate:
		return []string{"nodes"}
	case tuiActionNodeStart, tuiActionNodeStop, tuiActionNodeDelete:
		return []string{terminal.NodeTarget(entry.node.ID).String()}
	case tuiActionNodeClone:
		return []string{terminal.NodeTarget(entry.node.ID).String()}
	default:
		return nil
	}
}

func (a *vaxisTUIApp) ensureActionNotConflicting(action tuiActionSpec, entry tuiTreeEntry) error {
	if conflict := a.conflictingOperation(a.actionResourceKeys(action, entry)); conflict != nil {
		return fmt.Errorf("%s is already in progress", strings.ToLower(conflict.Title))
	}
	return nil
}

func (a *vaxisTUIApp) performAction(action tuiActionSpec) error {
	entry := a.state.selectedEntry()
	if err := a.ensureActionNotConflicting(action, entry); err != nil {
		return err
	}
	switch action.ID {
	case tuiActionConfigurationManage:
		return a.openConfigurationsMenu()
	case tuiActionEnvironmentConfigManage:
		return a.openEnvironmentConfigsMenu()
	case tuiActionNodeCreate:
		return a.openCreateNodeDialog()
	case tuiActionNodeStart:
		return a.startOperation(tuiOperationRequest{
			Title:         "Starting " + entry.node.Slug,
			DisplayStatus: "starting",
			ResourceKeys:  []string{terminal.NodeTarget(entry.node.ID).String()},
			EntryKeys:     []string{terminal.NodeTarget(entry.node.ID).String()},
			Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
				node, err := service.NodeStart(ctx, entry.node.ID)
				if err != nil {
					return tuiOperationResult{}, err
				}
				return tuiOperationResult{
					Status:       "started node " + node.Slug,
					PreferredKey: terminal.NodeTarget(node.ID).String(),
					ReloadData:   true,
				}, nil
			},
		})
	case tuiActionNodeStop:
		return a.startOperation(tuiOperationRequest{
			Title:         "Stopping " + entry.node.Slug,
			DisplayStatus: "stopping",
			ResourceKeys:  []string{terminal.NodeTarget(entry.node.ID).String()},
			EntryKeys:     []string{terminal.NodeTarget(entry.node.ID).String()},
			Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
				node, err := service.NodeStop(ctx, entry.node.ID)
				if err != nil {
					return tuiOperationResult{}, err
				}
				return tuiOperationResult{
					Status:       "stopped node " + node.Slug,
					PreferredKey: terminal.NodeTarget(node.ID).String(),
					CloseNodeID:  node.ID,
					ReloadData:   true,
				}, nil
			},
		})
	case tuiActionNodeDelete:
		a.openDeleteNodeDialog(entry.node)
	case tuiActionNodeClone:
		a.openCloneNodeDialog(entry.node)
	}

	return nil
}

func (a *vaxisTUIApp) handleMouse(mouse vaxis.Mouse) {
	if mouse.EventType == vaxis.EventPress && mouse.Button == vaxis.MouseLeftButton {
		if target, ok := a.linkTargetAt(mouse.Col, mouse.Row); ok {
			if err := a.openHyperlink(target); err != nil {
				a.setStatus(slog.LevelError, err.Error())
				return
			}
			a.setStatus(slog.LevelInfo, "opened "+target)
			return
		}
	}

	if a.treeContentRect.contains(mouse.Col, mouse.Row) && mouse.EventType == vaxis.EventPress && mouse.Button == vaxis.MouseLeftButton {
		a.cancelTerminalMouseGesture()
		a.state.focusTree()
		contentRow := mouse.Row - a.treeContentRect.row
		entryCapacity := tuiTreeEntryCapacity(a.treeContentRect.height)
		if contentRow < entryCapacity*tuiTreeEntryHeight {
			if err := a.state.selectTreeRow(contentRow/tuiTreeEntryHeight, entryCapacity); err != nil {
				a.setStatus(slog.LevelError, err.Error())
				return
			}
			if err := a.ensureSelectedNodeTerminal(); err != nil {
				a.setStatus(slog.LevelError, err.Error())
				return
			}
		}
		a.clearStatus()
		a.syncSessionFocus()
		return
	}

	if !a.terminalBodyRect.contains(mouse.Col, mouse.Row) {
		if mouse.EventType == vaxis.EventRelease && mouse.Button == vaxis.MouseLeftButton {
			a.cancelTerminalMouseGesture()
		}
		return
	}

	if a.state.focus != tuiFocusTerminal && a.state.treePaneMode != tuiTreePaneModeTerminal {
		return
	}

	entry := a.mouseTerminalEntry()
	if !entry.valid() {
		return
	}

	sessionKey := a.state.activeSessionKey()
	term, ok := a.sessions.SessionTerminal(sessionKey)
	if !ok {
		return
	}

	translated := a.terminalBodyRect.translateMouse(mouse)
	if mouse.Button == vaxis.MouseWheelUp || mouse.Button == vaxis.MouseWheelDown {
		a.forwardSessionEvent(sessionKey, translated)
		return
	}

	if !term.CapturesMouse() {
		if err := a.handleTerminalMouseGesture(sessionKey, mouse, translated); err != nil {
			a.setStatus(slog.LevelError, err.Error())
		}
		return
	}

	a.cancelTerminalMouseGesture()
	if mouse.EventType != vaxis.EventPress || mouse.Button != vaxis.MouseLeftButton {
		if a.state.focus == tuiFocusTerminal {
			a.forwardSessionEvent(sessionKey, translated)
		}
		return
	}

	if a.state.focus != tuiFocusTerminal {
		if err := a.state.focusTerminal(); err != nil {
			a.setStatus(slog.LevelError, err.Error())
			return
		}
		a.syncSessionFocus()
	}
	a.clearStatus()
	a.forwardSessionEvent(sessionKey, translated)
}

func (a *vaxisTUIApp) handleResize(event vaxis.Resize) {
	width := event.Cols
	height := event.Rows
	if (width <= 0 || height <= 0) && a.vx != nil {
		width, height = a.vx.Window().Size()
	}
	if width <= 0 || height <= 0 || a.sessions == nil || a.state == nil {
		return
	}

	focus := a.effectiveLayoutFocus()
	cols, rows := a.activeTerminalSize(width, height, focus)
	if cols <= 0 || rows <= 0 {
		return
	}
	a.sessions.SetPreferredTerminalSize(cols, rows)

	term, ok := a.sessions.SessionTerminal(a.state.activeSessionKey())
	if !ok || term == nil {
		return
	}
	term.Resize(cols, rows)
}

func (a *vaxisTUIApp) mouseTerminalEntry() tuiTreeEntry {
	if a.state.focus == tuiFocusTerminal {
		return a.state.activeTerminalEntry()
	}
	return a.state.selectedEntry()
}

func (a *vaxisTUIApp) handleTerminalMouseGesture(targetKey string, mouse vaxis.Mouse, translated vaxis.Mouse) error {
	switch mouse.EventType {
	case vaxis.EventPress:
		if mouse.Button == vaxis.MouseLeftButton {
			a.beginTerminalMouseGesture(targetKey, translated)
		}
		return nil
	case vaxis.EventMotion:
		a.updateTerminalMouseGesture(targetKey, translated)
		return nil
	case vaxis.EventRelease:
		if mouse.Button != vaxis.MouseLeftButton {
			return nil
		}
		if a.finishTerminalMouseGesture(targetKey, translated) {
			return nil
		}
		if target, ok := a.terminalLinkTargetAt(mouse); ok {
			if err := a.openHyperlink(target); err != nil {
				return err
			}
			a.setStatus(slog.LevelInfo, "opened "+target)
			return nil
		}
		if a.state.focus != tuiFocusTerminal {
			if err := a.state.focusTerminal(); err != nil {
				return err
			}
			a.syncSessionFocus()
		}
		a.clearStatus()
	}
	return nil
}

func (a *vaxisTUIApp) beginTerminalMouseGesture(targetKey string, mouse vaxis.Mouse) {
	a.terminalMouse = &tuiTerminalMouseGesture{
		targetKey: targetKey,
		startCol:  mouse.Col,
		startRow:  mouse.Row,
	}
}

func (a *vaxisTUIApp) updateTerminalMouseGesture(targetKey string, mouse vaxis.Mouse) {
	if a.terminalMouse == nil || a.terminalMouse.targetKey != targetKey {
		return
	}
	if mouse.Col != a.terminalMouse.startCol || mouse.Row != a.terminalMouse.startRow {
		a.terminalMouse.dragged = true
	}
}

func (a *vaxisTUIApp) finishTerminalMouseGesture(targetKey string, mouse vaxis.Mouse) bool {
	if a.terminalMouse == nil || a.terminalMouse.targetKey != targetKey {
		return false
	}
	if mouse.Col != a.terminalMouse.startCol || mouse.Row != a.terminalMouse.startRow {
		a.terminalMouse.dragged = true
	}
	dragged := a.terminalMouse.dragged
	a.terminalMouse = nil
	return dragged
}

func (a *vaxisTUIApp) cancelTerminalMouseGesture() {
	a.terminalMouse = nil
}

// US-layout macOS Option-layer glyphs: terminals that do not encode the
// Option/Alt modifier deliver the composed character the layout produces, so
// the shortcuts also match these glyphs.
const (
	macOptionShiftTGlyph = "ˇ" // Option-Shift-t
	macOptionTGlyph      = "†" // Option-t
	macOptionWGlyph      = "∑" // Option-w
)

func isTerminalViewToggleKey(key vaxis.Key) bool {
	if hasTerminalModifier(normalizedKeyModifiers(key.Modifiers), vaxis.ModShift) {
		return false
	}
	return keyMatchesTerminalModifier(key, '`') || key.Matches(vaxis.KeyF06)
}

func isHostTerminalTabOpenKey(key vaxis.Key) bool {
	if key.Matches('t', vaxis.ModAlt|vaxis.ModShift) ||
		key.Matches('t', vaxis.ModMeta|vaxis.ModShift) ||
		key.Matches('t', vaxis.ModAlt|vaxis.ModMeta|vaxis.ModShift) {
		return true
	}

	modifiers := normalizedKeyModifiers(key.Modifiers)
	if hasTerminalModifier(modifiers, 0) && (key.Text == "T" || key.Keycode == 'T') {
		return true
	}
	if modifiers != 0 && !hasTerminalModifier(modifiers, vaxis.ModShift) {
		return false
	}
	return keyIsGlyph(key, macOptionShiftTGlyph)
}

func isTerminalTabOpenKey(key vaxis.Key) bool {
	return keyMatchesOptionShortcut(key, 't', macOptionTGlyph)
}

func isTerminalTabNextKey(key vaxis.Key) bool {
	return keyMatchesTerminalModifier(key, vaxis.KeyRight) ||
		keyMatchesTerminalModifier(key, 'f')
}

func isTerminalTabPreviousKey(key vaxis.Key) bool {
	return keyMatchesTerminalModifier(key, vaxis.KeyLeft) ||
		keyMatchesTerminalModifier(key, 'b')
}

func isTerminalTabMoveNextKey(key vaxis.Key) bool {
	return keyMatchesShiftedTerminalModifier(key, vaxis.KeyRight) ||
		keyMatchesShiftedTerminalModifier(key, 'f')
}

func isTerminalTabMovePreviousKey(key vaxis.Key) bool {
	return keyMatchesShiftedTerminalModifier(key, vaxis.KeyLeft) ||
		keyMatchesShiftedTerminalModifier(key, 'b')
}

func isTerminalTabCloseKey(key vaxis.Key) bool {
	return keyMatchesOptionShortcut(key, 'w', macOptionWGlyph)
}

func keyMatchesOptionShortcut(key vaxis.Key, code rune, optionTexts ...string) bool {
	if keyMatchesTerminalModifier(key, code) {
		return true
	}

	modifiers := normalizedKeyModifiers(key.Modifiers)
	if modifiers != 0 && !hasTerminalModifier(modifiers, 0) {
		return false
	}
	for _, text := range optionTexts {
		if keyIsGlyph(key, text) {
			return true
		}
	}
	return false
}

// keyIsGlyph reports whether the key delivers the given single-glyph text,
// either as decoded text or as the raw keycode.
func keyIsGlyph(key vaxis.Key, glyph string) bool {
	if key.Text == glyph {
		return true
	}
	runes := []rune(glyph)
	return len(runes) == 1 && key.Keycode == runes[0]
}

func keyMatchesTerminalModifier(key vaxis.Key, code rune) bool {
	return key.Matches(code, vaxis.ModAlt) ||
		key.Matches(code, vaxis.ModMeta) ||
		key.Matches(code, vaxis.ModAlt|vaxis.ModMeta)
}

func keyMatchesShiftedTerminalModifier(key vaxis.Key, code rune) bool {
	return key.Matches(code, vaxis.ModAlt|vaxis.ModShift) ||
		key.Matches(code, vaxis.ModMeta|vaxis.ModShift) ||
		key.Matches(code, vaxis.ModAlt|vaxis.ModMeta|vaxis.ModShift)
}

func hasTerminalModifier(modifiers vaxis.ModifierMask, extra vaxis.ModifierMask) bool {
	terminalModifiers := []vaxis.ModifierMask{
		vaxis.ModAlt,
		vaxis.ModMeta,
		vaxis.ModAlt | vaxis.ModMeta,
	}
	for _, terminalModifier := range terminalModifiers {
		if modifiers == terminalModifier|extra {
			return true
		}
	}
	return false
}

func normalizedKeyModifiers(modifiers vaxis.ModifierMask) vaxis.ModifierMask {
	return modifiers &^ vaxis.ModCapsLock &^ vaxis.ModNumLock
}

func isQuitKey(key vaxis.Key) bool {
	return key.Matches('c', vaxis.ModCtrl)
}
