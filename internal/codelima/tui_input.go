package codelima

import (
	"context"
	"fmt"
	"strings"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/test_lima/internal/codelima/terminal"
)

type tuiTerminalMouseGesture struct {
	targetKey string
	startCol  int
	startRow  int
	dragged   bool
}

func (a *vaxisTUIApp) handleKey(key vaxis.Key) (bool, error) {
	if isHostTerminalTabOpenKey(key) {
		if err := a.openHostTerminalTab(); err != nil {
			a.status = err.Error()
			return false, nil
		}
		a.status = ""
		a.syncSessionFocus()
		return false, nil
	}

	if isTerminalTabOpenKey(key) {
		if err := a.openTerminalTab(); err != nil {
			a.status = err.Error()
			return false, nil
		}
		a.status = ""
		a.syncSessionFocus()
		return false, nil
	}

	if isTerminalTabNextKey(key) {
		if err := a.switchTerminalTab(1); err != nil {
			a.status = err.Error()
			return false, nil
		}
		a.status = ""
		a.syncSessionFocus()
		return false, nil
	}

	if isTerminalTabPreviousKey(key) {
		if err := a.switchTerminalTab(-1); err != nil {
			a.status = err.Error()
			return false, nil
		}
		a.status = ""
		a.syncSessionFocus()
		return false, nil
	}

	if isTerminalTabCloseKey(key) {
		if err := a.closeTerminalTab(); err != nil {
			a.status = err.Error()
			return false, nil
		}
		a.status = ""
		a.syncSessionFocus()
		return false, nil
	}

	if isTerminalViewToggleKey(key) {
		if err := a.state.toggleFocus(); err != nil {
			a.status = err.Error()
			return false, nil
		}
		a.status = ""
		a.syncSessionFocus()
		return false, nil
	}

	if a.state.focus == tuiFocusTree && key.MatchString("i") {
		if err := a.state.toggleTreePaneMode(); err != nil {
			a.status = err.Error()
			return false, nil
		}
		a.status = ""
		a.syncSessionFocus()
		return false, nil
	}

	if a.state.focus == tuiFocusTree && key.MatchString("m") {
		a.openMessagesView()
		return false, nil
	}

	if a.state.focus == tuiFocusTerminal {
		a.forwardTerminalEvent(key)
		return false, nil
	}

	if action, ok := a.matchAction(key); ok {
		if err := a.performAction(action); err != nil {
			a.status = err.Error()
			return false, nil
		}
		return false, nil
	}

	var err error
	switch {
	case key.MatchString("q"), isQuitKey(key):
		return true, nil
	case key.MatchString("Up"):
		err = a.state.moveSelection(-1)
	case key.MatchString("Down"):
		err = a.state.moveSelection(1)
	case key.MatchString("Left"):
		a.state.collapseSelection()
	case key.MatchString("Right"):
		a.state.expandSelection()
	default:
		return false, nil
	}

	if err != nil {
		a.status = err.Error()
	} else {
		a.status = ""
	}
	a.syncSessionFocus()
	return false, nil
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
	case tuiActionProjectCreate:
		return []string{"projects"}
	case tuiActionProjectCreateNode, tuiActionProjectUpdate, tuiActionProjectDelete:
		return []string{terminal.ProjectTarget(entry.project.ID).String()}
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
	case tuiActionProjectCreate:
		a.openCreateProjectDialog()
	case tuiActionProjectCreateNode:
		return a.openLegacyCreateNodeDialog(entry.project)
	case tuiActionProjectUpdate:
		a.openUpdateProjectDialog(entry.project)
	case tuiActionProjectDelete:
		a.openDeleteProjectDialog(entry.project)
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

func (a *vaxisTUIApp) handleMouse(mouse vaxis.Mouse) error {
	if mouse.EventType == vaxis.EventPress && mouse.Button == vaxis.MouseLeftButton {
		if target, ok := a.linkTargetAt(mouse.Col, mouse.Row); ok {
			if err := a.openHyperlink(target); err != nil {
				a.status = err.Error()
				return nil
			}
			a.status = "opened " + target
			return nil
		}
	}

	if a.treeContentRect.contains(mouse.Col, mouse.Row) && mouse.EventType == vaxis.EventPress && mouse.Button == vaxis.MouseLeftButton {
		a.cancelTerminalMouseGesture()
		a.state.focusTree()
		if err := a.state.selectTreeRow(mouse.Row-a.treeContentRect.row, a.treeContentRect.height); err != nil {
			a.status = err.Error()
			return nil
		}
		a.status = ""
		a.syncSessionFocus()
		return nil
	}

	if !a.terminalBodyRect.contains(mouse.Col, mouse.Row) {
		if mouse.EventType == vaxis.EventRelease && mouse.Button == vaxis.MouseLeftButton {
			a.cancelTerminalMouseGesture()
		}
		return nil
	}

	if a.state.focus != tuiFocusTerminal && a.state.treePaneMode != tuiTreePaneModeTerminal {
		return nil
	}

	entry := a.mouseTerminalEntry()
	if entry.kind != tuiTreeEntryProject && entry.kind != tuiTreeEntryNode {
		return nil
	}

	sessionKey := a.state.activeSessionKey()
	term, ok := a.sessions.SessionTerminal(sessionKey)
	if !ok {
		return nil
	}

	translated := a.terminalBodyRect.translateMouse(mouse)
	if mouse.Button == vaxis.MouseWheelUp || mouse.Button == vaxis.MouseWheelDown {
		a.forwardSessionEvent(sessionKey, translated)
		return nil
	}

	if !term.CapturesMouse() {
		if err := a.handleTerminalMouseGesture(sessionKey, mouse, translated); err != nil {
			a.status = err.Error()
		}
		return nil
	}

	a.cancelTerminalMouseGesture()
	if mouse.EventType != vaxis.EventPress || mouse.Button != vaxis.MouseLeftButton {
		if a.state.focus == tuiFocusTerminal {
			a.forwardSessionEvent(sessionKey, translated)
		}
		return nil
	}

	if a.state.focus != tuiFocusTerminal {
		if err := a.state.focusTerminal(); err != nil {
			a.status = err.Error()
			return nil
		}
		a.syncSessionFocus()
	}
	a.status = ""
	a.forwardSessionEvent(sessionKey, translated)
	return nil
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
			a.status = "opened " + target
			return nil
		}
		if a.state.focus != tuiFocusTerminal {
			if err := a.state.focusTerminal(); err != nil {
				return err
			}
			a.syncSessionFocus()
		}
		a.status = ""
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
	return key.Text == "ˇ" || key.Keycode == 'ˇ'
}

func isTerminalTabOpenKey(key vaxis.Key) bool {
	return keyMatchesOptionShortcut(key, 't', "†")
}

func isTerminalTabNextKey(key vaxis.Key) bool {
	return keyMatchesTerminalModifier(key, vaxis.KeyRight) ||
		keyMatchesTerminalModifier(key, 'f')
}

func isTerminalTabPreviousKey(key vaxis.Key) bool {
	return keyMatchesTerminalModifier(key, vaxis.KeyLeft) ||
		keyMatchesTerminalModifier(key, 'b')
}

func isTerminalTabCloseKey(key vaxis.Key) bool {
	return keyMatchesOptionShortcut(key, 'w', "∑")
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
		if key.Text == text {
			return true
		}
		runes := []rune(text)
		if len(runes) == 1 && key.Keycode == runes[0] {
			return true
		}
	}
	return false
}

func keyMatchesTerminalModifier(key vaxis.Key, code rune) bool {
	return key.Matches(code, vaxis.ModAlt) ||
		key.Matches(code, vaxis.ModMeta) ||
		key.Matches(code, vaxis.ModAlt|vaxis.ModMeta)
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
