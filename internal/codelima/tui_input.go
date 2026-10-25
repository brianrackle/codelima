package codelima

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.rockorager.dev/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

type tuiTerminalMouseGesture struct {
	targetKey string
	startCol  int
	startRow  int
	dragged   bool
	clicks    int
}

type tuiTerminalClick struct {
	target          string
	col, row, count int
	at              time.Time
}

// tuiKeyBinding pairs a key matcher with the app action it triggers.
type tuiKeyBinding struct {
	match     func(vaxis.Key) bool
	run       func(a *vaxisTUIApp) error
	pressOnly bool
}

// tuiKeyBindings is THE table of terminal-scope shortcuts: handleKey
// dispatches from it and isTUITerminalPayloadKey derives payload-ness from
// the same entries, so the shortcut set and the payload predicate can never
// drift apart.
var tuiKeyBindings = []tuiKeyBinding{
	{match: func(key vaxis.Key) bool { return key.Matches(vaxis.KeyF06) }, run: func(a *vaxisTUIApp) error {
		if a.state.focus == tuiFocusTerminal {
			a.state.focusTree()
			a.nodeDetail, a.detailTab = true, tuiDetailShell
			return nil
		}
		return a.openEmbeddedShell(false)
	}, pressOnly: true},
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

func (a *vaxisTUIApp) dispatchKey(key vaxis.Key) bool {
	if a.overlay != nil {
		if key.EventType != vaxis.EventPaste && isQuitKey(key) {
			return tuiKeyActivates(key, true)
		}
		a.updateOverlay(key)
		return false
	}
	if a.createIntent != nil && key.Matches(vaxis.KeyEsc) && tuiKeyActivates(key, true) {
		a.createIntent = nil
		a.setFooterStatus(slog.LevelInfo, "Queued navigation/attachment canceled; creation continues")
		return false
	}
	if a.search != nil {
		a.handleSearchKey(key)
		return false
	}
	if a.handleLogsSearchKey(key) || a.handleActivitySearchKey(key) || a.handlePortsSearchKey(key) {
		return false
	}
	// Pasted text is terminal payload, never a TUI shortcut. Vaxis emits one
	// EventPaste key per decoded input sequence between PasteStart/PasteEnd.
	if key.EventType == vaxis.EventPaste {
		if a.state.focus == tuiFocusTerminal {
			a.forwardTerminalEvent(key)
		} else if a.filterInput != nil {
			return a.dispatchManagerKey(key)
		}
		return false
	}
	for _, binding := range tuiKeyBindings {
		if !binding.match(key) {
			continue
		}
		// Vaxis matches identity independently of press/repeat/release.
		// Consume the whole shortcut lifecycle even when its action runs
		// only on press, including after the action changes tabs or focus.
		if !tuiKeyActivates(key, binding.pressOnly) {
			return false
		}
		if binding.pressOnly {
			a.claimShortcutKey(key)
		}
		if err := binding.run(a); err != nil {
			a.setStatus(slog.LevelError, err.Error())
			return false
		}
		a.clearStatus()
		a.syncSessionFocus()
		return false
	}

	if a.state.focus == tuiFocusTerminal {
		a.forwardTerminalEvent(key)
		return false
	}
	return a.dispatchManagerKey(key)
}

func (a *vaxisTUIApp) matchAction(key vaxis.Key) (tuiActionSpec, bool) {
	if !tuiKeyActivates(key, true) || normalizedKeyModifiers(key.Modifiers) != 0 {
		return tuiActionSpec{}, false
	}

	for _, action := range availableTUIActions(a.state.selectedEntry()) {
		if key.Matches(action.Hotkey) || strings.EqualFold(key.Text, string(action.Hotkey)) {
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
	case tuiActionNodeStart, tuiActionNodeRetry, tuiActionNodeRestart, tuiActionNodeStop, tuiActionNodeDelete:
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
	return a.performEntryAction(action, a.state.selectedEntry())
}

// The action owner also accepts an explicit entry for identity-bound callers
// such as the palette. Confirmation callbacks retain that entry's node ID.
func (a *vaxisTUIApp) performEntryAction(action tuiActionSpec, entry tuiTreeEntry) error {
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
	case tuiActionNodeStart, tuiActionNodeRetry:
		return a.startOperation(tuiOperationRequest{
			Title:         "Starting " + entry.node.Slug,
			DisplayStatus: "starting",
			ResourceKeys:  []string{terminal.NodeTarget(entry.node.ID).String()},
			EntryKeys:     []string{terminal.NodeTarget(entry.node.ID).String()},
			Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
				node, err := runTUINodeAction(ctx, service, action.ID, entry.node.ID)
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
	case tuiActionNodeRestart:
		if !entry.valid() {
			return fmt.Errorf("select a node first")
		}
		if a.confirmNodeStop(entry.node) {
			a.overlay = newTUIDialog("Restart "+entry.node.Slug, "Restart", []string{
				"Restart stops the VM and ends its guest sessions.",
				"It then starts the VM. Completed bootstrap steps are kept.",
				"Failed setup is rerun; use Retry to resume it.",
			}, nil, func(map[string]string) error { return a.restartNode(entry.node) })
			return nil
		}
		return a.restartNode(entry.node)
	case tuiActionNodeStop:
		if a.confirmNodeStop(entry.node) {
			a.overlay = newTUIDialog("Stop "+entry.node.Slug, "Stop", []string{"Sessions may have running commands. Stopping ends their guest processes."}, nil, func(map[string]string) error { return a.stopNode(entry.node) })
			return nil
		}
		return a.stopNode(entry.node)
	case tuiActionNodeDelete:
		a.openDeleteNodeDialog(entry.node)
	case tuiActionNodeClone:
		a.openCloneNodeDialog(entry.node)
	}

	return nil
}

func (a *vaxisTUIApp) stopNode(node Node) error {
	return a.startOperation(tuiOperationRequest{
		Title: "Stopping " + node.Slug, DisplayStatus: "stopping",
		ResourceKeys: []string{terminal.NodeTarget(node.ID).String()}, EntryKeys: []string{terminal.NodeTarget(node.ID).String()},
		Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
			stopped, err := service.NodeStop(ctx, node.ID)
			return tuiOperationResult{Status: "stopped node " + stopped.Slug, PreferredKey: terminal.NodeTarget(stopped.ID).String(), CloseNodeID: stopped.ID, ReloadData: true}, err
		},
	})
}

func (a *vaxisTUIApp) restartNode(node Node) error {
	key := terminal.NodeTarget(node.ID).String()
	return a.startOperation(tuiOperationRequest{
		Title: "Restarting " + node.Slug, DisplayStatus: "starting",
		ResourceKeys: []string{key}, EntryKeys: []string{key},
		Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
			restarted, err := service.NodeRestart(ctx, node.ID)
			return tuiOperationResult{Status: "restarted node " + restarted.Slug, PreferredKey: key, ReloadData: true}, err
		},
	})
}

func (a *vaxisTUIApp) confirmNodeStop(node Node) bool {
	switch a.managerSettings().ConfirmStop {
	case "never":
		return false
	case "always":
		return true
	}
	if a.sessions == nil {
		return false
	}
	for _, key := range a.sessions.TargetSessionKeys(terminal.NodeTarget(node.ID).String()) {
		// Prompt state comes from shell integration. If a shell does not report
		// it, treat its activity as unknown and ask before ending its process.
		if !a.terminalMetadata(key).CursorAtPrompt {
			return true
		}
	}
	return false
}

func (a *vaxisTUIApp) handleMouse(mouse vaxis.Mouse) {
	defer a.cancelHiddenMetricsLoad()
	defer a.cancelHiddenLogsLoad()
	defer a.cancelHiddenSpecLoad()
	if a.overlay != nil {
		a.updateOverlay(mouse)
		return
	}
	if mouse.EventType == vaxis.EventPress {
		a.createIntent = nil
	}
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

	if mouse.EventType == vaxis.EventPress && mouse.Button == vaxis.MouseLeftButton {
		for i, rect := range a.viewRegions {
			if rect.contains(mouse.Col, mouse.Row) {
				a.switchView(tuiView(i))
				return
			}
		}
	}
	if a.handleEmbeddedShellMouse(mouse) || a.handleDetailNavigationMouse(mouse) || a.handleEmptyNodesMouse(mouse) || a.handleOverviewMouse(mouse) || a.handleLogsMouse(mouse) || a.handleSpecMouse(mouse) || a.handleActivityMouse(mouse) || a.handlePortsMouse(mouse) || a.handleRecipeMouse(mouse) || a.handleDoctorMouse(mouse) || a.handleSystemDaemonMouse(mouse) {
		return
	}
	if a.view == tuiViewSystem && mouse.EventType == vaxis.EventPress {
		for panel, rect := range a.systemRects {
			if !rect.contains(mouse.Col, mouse.Row) {
				continue
			}
			a.systemPanel = panel
			switch mouse.Button {
			case vaxis.MouseWheelUp:
				a.moveManagerSelection(-1)
			case vaxis.MouseWheelDown:
				a.moveManagerSelection(1)
			case vaxis.MouseLeftButton:
				if panel == tuiSystemSettings {
					start, visible := a.managerSettingsViewport(rect.height - 2)
					index := start + mouse.Row - rect.row - 3
					if index >= start && index < min(len(a.managerSettingRows()), start+visible) {
						a.settingIndex = index
						if err := a.editManagerSetting(); err != nil {
							a.setStatus(slog.LevelError, err.Error())
						}
					}
				}
			}
			return
		}
	}
	if a.metricsVisible() && mouse.EventType == vaxis.EventPress {
		switch mouse.Button {
		case vaxis.MouseWheelUp:
			a.moveMetricProcess(-1)
			return
		case vaxis.MouseWheelDown:
			a.moveMetricProcess(1)
			return
		case vaxis.MouseLeftButton:
			m := &a.metrics
			if m.processes != nil && m.processRect.contains(mouse.Col, mouse.Row) {
				index := m.processOffset + mouse.Row - m.processRect.row
				if index < len(m.processes.Rows) {
					m.processPID = m.processes.Rows[index].PID
				}
				return
			}
		}
	}
	if a.treeContentRect.contains(mouse.Col, mouse.Row) {
		if mouse.EventType != vaxis.EventPress {
			return
		}
		a.refreshFleet()
		switch mouse.Button {
		case vaxis.MouseWheelUp:
			a.fleet.move(-1)
		case vaxis.MouseWheelDown:
			a.fleet.move(1)
		case vaxis.MouseLeftButton:
			index := mouse.Row - a.treeContentRect.row
			if index < len(a.fleetMouseRows) && a.fleetMouseRows[index].NodeID != "" {
				a.fleet.selectID(a.fleetMouseRows[index].NodeID)
			}
		default:
			return
		}
		a.selectFleetNode()
		a.state.focusTree()
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
	if mouse.Modifiers&vaxis.ModShift != 0 {
		// Shift preserves the outer terminal's selection bypass, including when
		// the embedded application captures ordinary mouse input.
		a.cancelTerminalMouseGesture()
		return
	}
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
	if event.Cols > 0 && event.Rows > 0 && event.XPixel >= event.Cols && event.YPixel >= event.Rows {
		a.cellPixelWidth, a.cellPixelHeight = event.XPixel/event.Cols, event.YPixel/event.Rows
	}
	width := event.Cols
	height := event.Rows
	if (width <= 0 || height <= 0) && a.vx != nil {
		width, height = a.vx.Window().Size()
	}
	if width <= 0 || height <= 0 {
		return
	}
	if a.vx != nil {
		// Vaxis requires the application to resize its drawing buffers. Do
		// this before layout or Draw can reassert the old pane geometry.
		event.Cols, event.Rows = width, height
		a.vx.Resize(event)
	}
	if a.sessions == nil || a.state == nil {
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
	if terminal, ok := term.(*daemonTUITerminal); ok {
		terminal.resizePixels(cols, rows, a.cellPixelWidth, a.cellPixelHeight)
	} else {
		term.Resize(cols, rows)
	}
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
			if a.supportsTerminalInspection(targetKey) {
				clicks := 1
				last := a.lastTerminalClick
				if last.target == targetKey && last.col == translated.Col && last.row == translated.Row && time.Since(last.at) < 400*time.Millisecond {
					clicks = last.count%4 + 1
				}
				a.lastTerminalClick = tuiTerminalClick{target: targetKey, col: translated.Col, row: translated.Row, count: clicks, at: time.Now()}
				a.terminalMouse.clicks = clicks
				kinds := []string{"cell", "word", "line", "output"}
				return a.queueTerminalInteraction(targetKey, TerminalInteractionRequest{Action: "press", Kind: kinds[clicks-1], Col: translated.Col, Row: translated.Row}, false, 0)
			}
		}
		return nil
	case vaxis.EventMotion:
		a.updateTerminalMouseGesture(targetKey, translated)
		if a.terminalMouse != nil && a.supportsTerminalInspection(targetKey) {
			return a.queueTerminalInteraction(targetKey, TerminalInteractionRequest{Action: "drag", Col: translated.Col, Row: translated.Row}, false, 0)
		}
		return nil
	case vaxis.EventRelease:
		if mouse.Button != vaxis.MouseLeftButton {
			return nil
		}
		multiple := a.terminalMouse != nil && a.terminalMouse.clicks > 1
		dragged := a.finishTerminalMouseGesture(targetKey, translated)
		if a.supportsTerminalInspection(targetKey) {
			if err := a.queueTerminalInteraction(targetKey, TerminalInteractionRequest{Action: "release", Col: translated.Col, Row: translated.Row}, dragged || multiple, 0); err != nil {
				return err
			}
		}
		if dragged || multiple {
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
	if a.terminalMouse != nil && a.supportsTerminalInspection(a.terminalMouse.targetKey) {
		_ = a.queueTerminalInteraction(a.terminalMouse.targetKey, TerminalInteractionRequest{Action: "cancel"}, false, 0)
	}
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
