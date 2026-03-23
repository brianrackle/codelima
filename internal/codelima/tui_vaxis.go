package codelima

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/widgets/border"
	"git.sr.ht/~rockorager/vaxis/widgets/term"
)

type vaxisTUIRunner struct{}

func newTUIRunner() TUIRunner {
	return &vaxisTUIRunner{}
}

type tuiSession struct {
	node     Node
	terminal tuiTerminal
}

type tuiSessionStore struct {
	ctx       context.Context
	service   *Service
	postEvent func(vaxis.Event)
	sessions  map[string]*tuiSession
}

func newTUISessionStore(ctx context.Context, service *Service, postEvent func(vaxis.Event)) *tuiSessionStore {
	return &tuiSessionStore{
		ctx:       ctx,
		service:   service,
		postEvent: postEvent,
		sessions:  map[string]*tuiSession{},
	}
}

func (s *tuiSessionStore) HasSession(nodeID string) bool {
	_, ok := s.sessions[nodeID]
	return ok
}

func (s *tuiSessionStore) EnsureSession(node Node) error {
	if _, ok := s.sessions[node.ID]; ok {
		return nil
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve codelima executable: %w", err)
	}

	command := exec.CommandContext(s.ctx, executable, "--home", s.service.cfg.MetadataRoot, "shell", node.ID)
	command.Env = os.Environ()

	terminal := newTUITerminal(node.ID, s.postEvent)
	if err := terminal.Start(command); err != nil {
		return err
	}

	session := &tuiSession{node: node, terminal: terminal}
	s.sessions[node.ID] = session
	return nil
}

func (s *tuiSessionStore) Session(nodeID string) (*tuiSession, bool) {
	session, ok := s.sessions[nodeID]
	return session, ok
}

func (s *tuiSessionStore) RemoveNode(nodeID string) (*tuiSession, bool) {
	session := s.sessions[nodeID]
	if session == nil {
		return nil, false
	}
	delete(s.sessions, nodeID)
	return session, true
}

func (s *tuiSessionStore) Close() {
	for nodeID, session := range s.sessions {
		session.terminal.Close()
		delete(s.sessions, nodeID)
	}
}

func (s *tuiSessionStore) CloseNode(nodeID string) {
	session, ok := s.sessions[nodeID]
	if !ok {
		return
	}

	delete(s.sessions, nodeID)
	session.terminal.Close()
}

type tuiRect struct {
	col    int
	row    int
	width  int
	height int
}

type tuiBodyLayout struct {
	treeVisible bool
	treeWidth   int
	termCol     int
	termWidth   int
}

func (r tuiRect) contains(col, row int) bool {
	if r.width <= 0 || r.height <= 0 {
		return false
	}

	return col >= r.col && col < r.col+r.width && row >= r.row && row < r.row+r.height
}

func (r tuiRect) translateMouse(mouse vaxis.Mouse) vaxis.Mouse {
	mouse.Col -= r.col
	mouse.Row -= r.row
	return mouse
}

func layoutTUIBody(width int, focus tuiFocus) tuiBodyLayout {
	if focus == tuiFocusTerminal {
		return tuiBodyLayout{
			treeVisible: false,
			treeWidth:   0,
			termCol:     0,
			termWidth:   width,
		}
	}

	treeWidth := width / 3
	if treeWidth < 28 {
		treeWidth = 28
	}
	if treeWidth > 40 {
		treeWidth = 40
	}
	if treeWidth > width-24 {
		treeWidth = width - 24
	}

	return tuiBodyLayout{
		treeVisible: true,
		treeWidth:   treeWidth,
		termCol:     treeWidth + 1,
		termWidth:   width - treeWidth - 1,
	}
}

type vaxisTUIApp struct {
	ctx               context.Context
	service           *Service
	vx                *vaxis.Vaxis
	copySelection     func(string) error
	openLink          func(string) error
	screenHyperlinkAt func(int, int) (string, bool)
	state             *tuiState
	sessions          *tuiSessionStore
	patches           []PatchProposal
	progress          *tuiProgressWriter
	operation         *tuiOperationState
	linkRegions       []tuiLinkRegion
	selection         *tuiTerminalSelection
	dialog            *tuiDialog
	menu              *tuiMenu
	selector          *tuiSelector
	status            string
	treeContentRect   tuiRect
	terminalBodyRect  tuiRect
}

func (r *vaxisTUIRunner) Run(ctx context.Context, service *Service) error {
	tree, err := service.ProjectTree("", false)
	if err != nil {
		return err
	}

	vx, err := vaxis.New(vaxis.Options{})
	if err != nil {
		return err
	}
	defer vx.Close()

	sessions := newTUISessionStore(ctx, service, vx.PostEvent)
	defer sessions.Close()

	state, err := newTUIState(tree, sessions)
	if err != nil {
		return err
	}

	app := &vaxisTUIApp{
		ctx:           ctx,
		service:       service,
		vx:            vx,
		copySelection: func(text string) error { return copyTextToClipboard(text, vx.ClipboardPush) },
		state:         state,
		sessions:      sessions,
		progress:      newTUIProgressWriter(vx.PostEvent),
	}
	if execLima, ok := service.lima.(*ExecLimaClient); ok {
		stdout := execLima.Stdout
		stderr := execLima.Stderr
		execLima.Stdout = app.progress
		execLima.Stderr = app.progress
		defer func() {
			app.progress.Flush()
			execLima.Stdout = stdout
			execLima.Stderr = stderr
		}()
	}
	if err := app.reloadPatches(); err != nil {
		return err
	}
	app.syncSessionFocus()
	app.draw()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-vx.Events():
			if !ok {
				return nil
			}
			quit, err := app.handleEvent(event)
			if err != nil {
				return err
			}
			if quit {
				return nil
			}
		}
	}
}

func (a *vaxisTUIApp) handleEvent(event vaxis.Event) (bool, error) {
	switch event := event.(type) {
	case tuiOperationProgressEvent:
		a.appendOperationLine(event.Line)
		a.draw()
		return false, nil
	case tuiOperationCompleteEvent:
		a.finishOperation(event)
		a.draw()
		return false, nil
	case tuiTerminalClosedEvent:
		a.handleTerminalClosed(event)
		a.draw()
		return false, nil
	case tuiTerminalErrorEvent:
		a.status = event.Err.Error()
		a.draw()
		return false, nil
	}

	if key, ok := event.(vaxis.Key); ok && isQuitKey(key) && (a.dialog != nil || a.menu != nil || a.selector != nil) {
		return true, nil
	}

	if a.operation != nil {
		if key, ok := event.(vaxis.Key); ok && (key.MatchString("q") || isQuitKey(key)) {
			return true, nil
		}
		return false, nil
	}

	if a.selector != nil {
		completed, cancelled, err := a.selector.Update(event)
		if err != nil {
			a.status = err.Error()
		}
		if completed || cancelled || err != nil {
			a.selector = nil
		}
		a.draw()
		return false, nil
	}

	if a.dialog != nil {
		completed, cancelled, err := a.dialog.Update(event)
		if err != nil {
			return false, err
		}
		if completed || cancelled {
			a.dialog = nil
		}
		a.draw()
		return false, nil
	}

	if a.menu != nil {
		completed, cancelled, err := a.menu.Update(event)
		if err != nil {
			a.status = err.Error()
		}
		if completed || cancelled || err != nil {
			a.menu = nil
		}
		a.draw()
		return false, nil
	}

	switch event := event.(type) {
	case vaxis.Key:
		quit, err := a.handleKey(event)
		a.draw()
		return quit, err
	case vaxis.Mouse:
		err := a.handleMouse(event)
		a.draw()
		return false, err
	case vaxis.PasteStartEvent:
		a.forwardTerminalEvent(event)
		a.draw()
	case vaxis.PasteEndEvent:
		a.forwardTerminalEvent(event)
		a.draw()
	case vaxis.ColorThemeUpdate:
		a.forwardTerminalEvent(event)
		a.draw()
	case vaxis.Resize:
		a.draw()
	case vaxis.Redraw:
		a.draw()
	case vaxis.SyncFunc:
		event()
		a.draw()
	case term.EventNotify:
		a.vx.Notify(event.Title, event.Body)
	case vaxis.QuitEvent:
		return true, nil
	}

	return false, nil
}

func (a *vaxisTUIApp) handleKey(key vaxis.Key) (bool, error) {
	if isTerminalViewToggleKey(key) {
		if err := a.state.toggleFocus(); err != nil {
			a.status = err.Error()
			return false, nil
		}
		a.status = ""
		a.syncSessionFocus()
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

func (a *vaxisTUIApp) performAction(action tuiActionSpec) error {
	entry := a.state.selectedEntry()
	switch action.ID {
	case tuiActionProjectCreate:
		a.openCreateProjectDialog()
	case tuiActionEnvironmentConfigManage:
		return a.openEnvironmentConfigsMenu()
	case tuiActionProjectCreateNode:
		a.openCreateNodeDialog(entry.project)
	case tuiActionProjectEnvironment:
		a.openProjectEnvironmentMenu(entry.project)
	case tuiActionProjectUpdate:
		a.openUpdateProjectDialog(entry.project)
	case tuiActionProjectDelete:
		a.openDeleteProjectDialog(entry.project)
	case tuiActionNodeStart:
		return a.startOperation("Starting "+entry.node.Slug, func(ctx context.Context) (tuiOperationResult, error) {
			node, err := a.service.NodeStart(ctx, entry.node.ID)
			if err != nil {
				return tuiOperationResult{}, err
			}
			return tuiOperationResult{
				Status:       "started node " + node.Slug,
				PreferredKey: "node:" + node.ID,
				ReloadData:   true,
			}, nil
		})
	case tuiActionNodeStop:
		return a.startOperation("Stopping "+entry.node.Slug, func(ctx context.Context) (tuiOperationResult, error) {
			node, err := a.service.NodeStop(ctx, entry.node.ID)
			if err != nil {
				return tuiOperationResult{}, err
			}
			return tuiOperationResult{
				Status:       "stopped node " + node.Slug,
				PreferredKey: "node:" + node.ID,
				CloseNodeID:  node.ID,
				ReloadData:   true,
			}, nil
		})
	case tuiActionNodeDelete:
		a.openDeleteNodeDialog(entry.node)
	case tuiActionNodeClone:
		a.openCloneNodeDialog(entry.node, entry.project)
	case tuiActionNodePatch:
		a.openPatchMenu(entry.node, entry.project)
	}

	return nil
}

func commaSeparatedValues(values []string) string {
	return strings.Join(values, ",")
}

func parseCommaSeparatedValues(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}

	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		values = append(values, part)
	}

	return values
}

func environmentConfigSelectionSummary(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func (a *vaxisTUIApp) environmentConfigSelectorOptions() ([]tuiSelectorOption, error) {
	configs, err := a.service.EnvironmentConfigList(false)
	if err != nil {
		return nil, err
	}

	options := make([]tuiSelectorOption, 0, len(configs))
	for _, config := range configs {
		label := config.Slug
		if len(config.Commands) > 0 {
			label = fmt.Sprintf("%s (%d commands)", config.Slug, len(config.Commands))
		}
		options = append(options, tuiSelectorOption{
			Label: label,
			Value: config.Slug,
		})
	}
	return options, nil
}

func (a *vaxisTUIApp) openEnvironmentConfigSelector(title string, description []string, current []string, multi bool, onSubmit func(values []string) error) error {
	options, err := a.environmentConfigSelectorOptions()
	if err != nil {
		return err
	}
	if !multi && len(options) == 0 {
		return fmt.Errorf("no environment configs configured")
	}
	if multi && len(options) == 0 {
		description = append(description, "No reusable environment configs configured. Press Enter to keep none assigned.")
	}
	a.selector = newTUISelector(title, description, options, current, multi, onSubmit)
	return nil
}

func commandSelectorOptions(commands []string) []tuiSelectorOption {
	options := make([]tuiSelectorOption, 0, len(commands))
	for index, command := range commands {
		options = append(options, tuiSelectorOption{
			Label: fmt.Sprintf("%d. %s", index+1, command),
			Value: strconv.Itoa(index),
		})
	}
	return options
}

func parseSelectorIndices(values []string, length int) ([]int, error) {
	indices := make([]int, 0, len(values))
	seen := map[int]bool{}
	for _, value := range values {
		index, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("invalid command selection")
		}
		if index < 0 || index >= length {
			return nil, fmt.Errorf("selected command is out of range")
		}
		if seen[index] {
			continue
		}
		indices = append(indices, index)
		seen[index] = true
	}
	sort.Ints(indices)
	return indices, nil
}

func removeCommandsByIndex(commands []string, indices []int) []string {
	if len(indices) == 0 {
		return append([]string(nil), commands...)
	}

	filtered := make([]string, 0, len(commands)-len(indices))
	selected := map[int]bool{}
	for _, index := range indices {
		selected[index] = true
	}
	for index, command := range commands {
		if selected[index] {
			continue
		}
		filtered = append(filtered, command)
	}
	return filtered
}

func moveCommand(commands []string, index int, delta int) []string {
	target := index + delta
	if index < 0 || index >= len(commands) || target < 0 || target >= len(commands) {
		return append([]string(nil), commands...)
	}

	moved := append([]string(nil), commands...)
	moved[index], moved[target] = moved[target], moved[index]
	return moved
}

func (a *vaxisTUIApp) reloadProjectAndOpenEnvironmentMenu(projectID string) error {
	if err := a.reloadData("project:" + projectID); err != nil {
		return err
	}
	project, err := a.service.ProjectShow(projectID)
	if err != nil {
		return err
	}
	a.openProjectEnvironmentMenu(project)
	return nil
}

func (a *vaxisTUIApp) reopenEnvironmentConfigCommandMenu(configID string) error {
	config, err := a.service.EnvironmentConfigShow(configID)
	if err != nil {
		return err
	}
	a.openEnvironmentConfigCommandMenu(config)
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
		a.selection = nil
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
		return nil
	}

	entry := a.state.selectedEntry()
	if entry.kind != tuiTreeEntryNode {
		return nil
	}

	session, ok := a.sessions.Session(entry.node.ID)
	if !ok {
		return nil
	}

	translated := a.terminalBodyRect.translateMouse(mouse)
	if a.usesLocalTerminalSelection(session.terminal, mouse, entry.node.ID) {
		if err := a.handleTerminalMouseSelection(entry.node.ID, entry.node.Slug, session.terminal.String(), mouse, translated); err != nil {
			a.status = err.Error()
		}
		return nil
	}

	if mouse.Button == vaxis.MouseWheelUp || mouse.Button == vaxis.MouseWheelDown {
		a.forwardSessionEvent(entry.node.ID, translated)
		return nil
	}

	if mouse.EventType != vaxis.EventPress || mouse.Button != vaxis.MouseLeftButton {
		if a.state.focus == tuiFocusTerminal {
			a.forwardSessionEvent(entry.node.ID, translated)
		}
		return nil
	}

	a.selection = nil
	if err := a.state.focusTerminal(); err != nil {
		a.status = err.Error()
		return nil
	}
	a.status = ""
	a.syncSessionFocus()
	a.forwardSessionEvent(entry.node.ID, translated)
	return nil
}

func (a *vaxisTUIApp) usesLocalTerminalSelection(terminal tuiTerminal, mouse vaxis.Mouse, nodeID string) bool {
	if terminal == nil {
		return false
	}
	if mouse.Button == vaxis.MouseLeftButton {
		return mouse.Modifiers&vaxis.ModShift != 0 || !terminal.CapturesMouse()
	}
	if mouse.Button != vaxis.MouseNoButton || mouse.EventType == vaxis.EventPress || a.selection == nil || a.selection.nodeID != nodeID {
		return false
	}
	return mouse.Modifiers&vaxis.ModShift != 0 || !terminal.CapturesMouse()
}

func (a *vaxisTUIApp) handleTerminalMouseSelection(nodeID string, nodeSlug string, snapshot string, mouse vaxis.Mouse, translated vaxis.Mouse) error {
	switch mouse.EventType {
	case vaxis.EventPress:
		a.beginTerminalSelection(nodeID, translated)
		return nil
	case vaxis.EventMotion:
		a.updateTerminalSelection(nodeID, translated)
		return nil
	case vaxis.EventRelease:
		dragged := a.finishTerminalSelection(nodeID, nodeSlug, snapshot, translated)
		if dragged || mouse.Modifiers&vaxis.ModShift != 0 {
			return nil
		}
		if target, ok := a.terminalLinkTargetAt(mouse); ok {
			if err := a.openHyperlink(target); err != nil {
				return err
			}
			a.status = "opened " + target
			return nil
		}
		if err := a.state.focusTerminal(); err != nil {
			return err
		}
		a.status = ""
		a.syncSessionFocus()
	}
	return nil
}

func (a *vaxisTUIApp) handleTerminalClosed(event tuiTerminalClosedEvent) {
	session, ok := a.sessions.RemoveNode(event.NodeID)
	if !ok {
		return
	}

	if a.state.activeNodeID == event.NodeID {
		a.state.activeNodeID = ""
		if a.state.focus == tuiFocusTerminal {
			a.state.focusTree()
		}
	}

	message := fmt.Sprintf("shell exited for %s", session.node.Slug)
	if event.Err != nil {
		message = fmt.Sprintf("%s: %s", message, event.Err)
	}
	a.status = message
	a.syncSessionFocus()
}

func (a *vaxisTUIApp) openHyperlink(target string) error {
	if a.openLink != nil {
		return a.openLink(target)
	}
	return openHyperlink(target)
}

func (a *vaxisTUIApp) renderedHyperlinkAt(col, row int) (string, bool) {
	if a.screenHyperlinkAt != nil {
		return a.screenHyperlinkAt(col, row)
	}
	return renderedHyperlinkAt(a.vx, col, row)
}

func (a *vaxisTUIApp) terminalLinkTargetAt(mouse vaxis.Mouse) (string, bool) {
	if !a.terminalBodyRect.contains(mouse.Col, mouse.Row) {
		return "", false
	}

	entry := a.state.selectedEntry()
	if entry.kind != tuiTreeEntryNode {
		return "", false
	}
	if !a.sessions.HasSession(entry.node.ID) {
		return "", false
	}

	if session, ok := a.sessions.Session(entry.node.ID); ok {
		localMouse := a.terminalBodyRect.translateMouse(mouse)
		if target, ok := session.terminal.HyperlinkAt(localMouse.Col, localMouse.Row); ok {
			return target, true
		}
	}

	return a.renderedHyperlinkAt(mouse.Col, mouse.Row)
}

func (a *vaxisTUIApp) reloadPatches() error {
	patches, err := a.service.PatchList("")
	if err != nil {
		return err
	}
	a.patches = patches
	return nil
}

func (a *vaxisTUIApp) reloadData(preferredKey string) error {
	tree, err := a.service.ProjectTree("", false)
	if err != nil {
		return err
	}
	if err := a.state.replaceTree(tree, preferredKey); err != nil {
		return err
	}
	if err := a.reloadPatches(); err != nil {
		return err
	}

	var orphans []string
	for nodeID := range a.sessions.sessions {
		if _, ok := a.state.nodesByID[nodeID]; !ok {
			orphans = append(orphans, nodeID)
		}
	}
	for _, nodeID := range orphans {
		a.sessions.CloseNode(nodeID)
	}

	a.syncSessionFocus()
	return nil
}

func (a *vaxisTUIApp) startOperation(title string, run func(context.Context) (tuiOperationResult, error)) error {
	if a.operation != nil {
		return fmt.Errorf("wait for %s to finish", strings.ToLower(a.operation.Title))
	}

	if a.vx == nil {
		result, err := run(a.ctx)
		if err != nil {
			return err
		}
		return a.applyOperationResult(result)
	}

	a.operation = &tuiOperationState{
		Title: title,
		Lines: []string{"waiting for command output..."},
	}
	go func() {
		result, err := run(a.ctx)
		if a.progress != nil {
			a.progress.Flush()
		}
		a.vx.PostEvent(tuiOperationCompleteEvent{Result: result, Err: err})
	}()
	return nil
}

func (a *vaxisTUIApp) applyOperationResult(result tuiOperationResult) error {
	if result.CloseNodeID != "" {
		a.sessions.CloseNode(result.CloseNodeID)
	}
	if result.ReloadData {
		if err := a.reloadData(result.PreferredKey); err != nil {
			return err
		}
	} else if result.ReloadPatches {
		if err := a.reloadPatches(); err != nil {
			return err
		}
	}
	if result.Status != "" {
		a.status = result.Status
	}
	return nil
}

func (a *vaxisTUIApp) appendOperationLine(line string) {
	if a.operation == nil || strings.TrimSpace(line) == "" {
		return
	}

	a.operation.Lines = append(a.operation.Lines, line)
	if len(a.operation.Lines) > 200 {
		a.operation.Lines = a.operation.Lines[len(a.operation.Lines)-200:]
	}
}

func (a *vaxisTUIApp) finishOperation(event tuiOperationCompleteEvent) {
	a.operation = nil
	if event.Err != nil {
		a.status = event.Err.Error()
		return
	}
	if err := a.applyOperationResult(event.Result); err != nil {
		a.status = err.Error()
	}
}

func (a *vaxisTUIApp) openCreateProjectDialog() {
	description := []string{
		"Create a top-level project rooted at a host workspace.",
		"Use project fork when you want a child project copied from an existing workspace snapshot.",
		"Use the Environment Configs field to choose shared defaults for future nodes from the selector.",
	}

	dialog := newTUIDialog(
		"Create Project",
		"Create",
		description,
		[]tuiDialogField{
			newTUIInputField("slug", "Project Slug", "", false),
			newTUIInputField("workspace_path", "Workspace Path", "", true),
			newTUISelectorField("environment_configs", "Environment Configs", "", false, nil),
		},
		func(values map[string]string) error {
			title := "Creating project"
			if values["slug"] != "" {
				title += " " + values["slug"]
			}
			return a.startOperation(title, func(ctx context.Context) (tuiOperationResult, error) {
				project, err := a.service.ProjectCreate(ctx, ProjectCreateInput{
					Slug:               values["slug"],
					WorkspacePath:      values["workspace_path"],
					EnvironmentConfigs: parseCommaSeparatedValues(values["environment_configs"]),
				})
				if err != nil {
					return tuiOperationResult{}, err
				}
				return tuiOperationResult{
					Status:       "created project " + project.Slug,
					PreferredKey: "project:" + project.ID,
					ReloadData:   true,
				}, nil
			})
		},
	)
	dialog.Fields[2].Value = commaSeparatedValues([]string{})
	dialog.Fields[2].Display = func(value string) string {
		return environmentConfigSelectionSummary(parseCommaSeparatedValues(value))
	}
	dialog.Fields[2].Activate = func() error {
		return a.openEnvironmentConfigSelector(
			"Select Environment Configs",
			[]string{"Choose reusable environment configs to assign shared defaults for future nodes in this project."},
			parseCommaSeparatedValues(dialog.Fields[2].Value),
			true,
			func(values []string) error {
				dialog.SetFieldValue("environment_configs", commaSeparatedValues(values))
				return nil
			},
		)
	}
	a.dialog = dialog
}

func (a *vaxisTUIApp) openCreateNodeDialog(project Project) {
	a.dialog = newTUIDialog(
		"Create Node",
		"Create",
		[]string{
			"Selected project: " + project.Slug,
			"Uses the project's existing runtime, agent, and environment command defaults.",
		},
		[]tuiDialogField{
			newTUIInputField("slug", "Node Slug", project.Slug+"-node", true),
		},
		func(values map[string]string) error {
			return a.startOperation("Creating node "+values["slug"], func(ctx context.Context) (tuiOperationResult, error) {
				node, err := a.service.NodeCreate(ctx, NodeCreateInput{
					Project: project.ID,
					Slug:    values["slug"],
				})
				if err != nil {
					return tuiOperationResult{}, err
				}
				return tuiOperationResult{
					Status:       "created node " + node.Slug,
					PreferredKey: "node:" + node.ID,
					ReloadData:   true,
				}, nil
			})
		},
	)
}

func (a *vaxisTUIApp) openProjectEnvironmentMenu(project Project) {
	description := []string{
		"Commands run the first time a node for project " + project.Slug + " is bootstrapped.",
	}
	if len(project.EnvironmentConfigs) == 0 {
		description = append(description, "Assigned configs: none.")
	} else {
		description = append(description, "Assigned configs: "+commaSeparatedValues(project.EnvironmentConfigs))
	}
	if len(project.SetupCommands) == 0 {
		description = append(description, "No environment commands configured.")
	} else {
		description = append(description, "Configured commands:")
		for index, command := range project.SetupCommands {
			description = append(description, fmt.Sprintf("%d. %s", index+1, command))
		}
	}

	a.menu = &tuiMenu{
		Title:       "Project Environment",
		Description: description,
		Entries: []tuiMenuEntry{
			{Key: 'g', Label: "Set Configs", Action: func() error { return a.openSetProjectEnvironmentConfigsDialog(project) }},
			{Key: 'l', Label: "Clear Configs", Action: func() error {
				return a.startOperation("Clearing configs for "+project.Slug, func(context.Context) (tuiOperationResult, error) {
					updated, err := a.service.ProjectUpdate(project.ID, ProjectUpdateInput{ClearEnvironmentConfigs: true})
					if err != nil {
						return tuiOperationResult{}, err
					}
					return tuiOperationResult{
						Status:       "cleared environment configs for " + updated.Slug,
						PreferredKey: "project:" + updated.ID,
						ReloadData:   true,
					}, nil
				})
			}},
			{Key: 'a', Label: "Add Command", Action: func() error { a.openAddProjectEnvironmentCommandDialog(project); return nil }},
			{Key: 'r', Label: "Remove Command", Action: func() error { return a.openRemoveProjectEnvironmentCommandDialog(project) }},
			{Key: 'm', Label: "Move Command", Action: func() error { return a.openMoveProjectEnvironmentCommandDialog(project) }},
			{Key: 'c', Label: "Clear Commands", Action: func() error { a.openClearProjectEnvironmentCommandsDialog(project); return nil }},
		},
	}
}

func (a *vaxisTUIApp) openSetProjectEnvironmentConfigsDialog(project Project) error {
	return a.openEnvironmentConfigSelector(
		"Set Environment Configs",
		[]string{"Choose reusable environment configs to assign shared defaults to this project."},
		project.EnvironmentConfigs,
		true,
		func(values []string) error {
			return a.startOperation("Updating configs for "+project.Slug, func(context.Context) (tuiOperationResult, error) {
				updated, err := a.service.ProjectUpdate(project.ID, ProjectUpdateInput{
					EnvironmentConfigs: values,
				})
				if err != nil {
					return tuiOperationResult{}, err
				}
				return tuiOperationResult{
					Status:       "updated environment configs for " + updated.Slug,
					PreferredKey: "project:" + updated.ID,
					ReloadData:   true,
				}, nil
			})
		},
	)
}

func (a *vaxisTUIApp) openAddProjectEnvironmentCommandDialog(project Project) {
	a.dialog = newTUIDialog(
		"Add Environment Command",
		"Add",
		[]string{"Add a command to the project's environment bootstrap list."},
		[]tuiDialogField{
			newTUIInputField("command", "Command", "", true),
		},
		func(values map[string]string) error {
			commands := append(append([]string(nil), project.SetupCommands...), values["command"])
			updated, err := a.service.ProjectUpdate(project.ID, ProjectUpdateInput{SetupCommands: commands})
			if err != nil {
				return err
			}
			a.status = "updated environment for " + updated.Slug
			return a.reloadProjectAndOpenEnvironmentMenu(updated.ID)
		},
	)
}

func (a *vaxisTUIApp) openRemoveProjectEnvironmentCommandDialog(project Project) error {
	if len(project.SetupCommands) == 0 {
		return fmt.Errorf("project %s has no environment commands", project.Slug)
	}

	a.selector = newTUISelector(
		"Remove Environment Commands",
		[]string{"Choose one or more project environment commands to remove."},
		commandSelectorOptions(project.SetupCommands),
		nil,
		true,
		func(values []string) error {
			indices, err := parseSelectorIndices(values, len(project.SetupCommands))
			if err != nil {
				return err
			}
			if len(indices) == 0 {
				return fmt.Errorf("select at least one command to remove")
			}

			description := []string{"Remove the selected project environment commands?"}
			for _, index := range indices {
				description = append(description, fmt.Sprintf("%d. %s", index+1, project.SetupCommands[index]))
			}

			a.dialog = newTUIDialog(
				"Remove Environment Commands",
				"Remove",
				description,
				nil,
				func(map[string]string) error {
					commands := removeCommandsByIndex(project.SetupCommands, indices)
					updated, err := a.service.ProjectUpdate(project.ID, ProjectUpdateInput{SetupCommands: commands})
					if err != nil {
						return err
					}
					a.status = "updated environment for " + updated.Slug
					return a.reloadProjectAndOpenEnvironmentMenu(updated.ID)
				},
			)
			return nil
		},
	)
	return nil
}

func (a *vaxisTUIApp) openMoveProjectEnvironmentCommandDialog(project Project) error {
	if len(project.SetupCommands) < 2 {
		return fmt.Errorf("project %s needs at least two environment commands to change order", project.Slug)
	}

	a.selector = newTUISelector(
		"Move Environment Command",
		[]string{"Choose a project environment command to move up or down."},
		commandSelectorOptions(project.SetupCommands),
		nil,
		false,
		func(values []string) error {
			indices, err := parseSelectorIndices(values, len(project.SetupCommands))
			if err != nil {
				return err
			}
			if len(indices) != 1 {
				return fmt.Errorf("select a single command to move")
			}
			index := indices[0]
			command := project.SetupCommands[index]

			entries := []tuiMenuEntry{}
			if index > 0 {
				entries = append(entries, tuiMenuEntry{Key: 'u', Label: "Move Up", Action: func() error {
					updated, err := a.service.ProjectUpdate(project.ID, ProjectUpdateInput{SetupCommands: moveCommand(project.SetupCommands, index, -1)})
					if err != nil {
						return err
					}
					a.status = "updated environment for " + updated.Slug
					return a.reloadProjectAndOpenEnvironmentMenu(updated.ID)
				}})
			}
			if index < len(project.SetupCommands)-1 {
				entries = append(entries, tuiMenuEntry{Key: 'd', Label: "Move Down", Action: func() error {
					updated, err := a.service.ProjectUpdate(project.ID, ProjectUpdateInput{SetupCommands: moveCommand(project.SetupCommands, index, 1)})
					if err != nil {
						return err
					}
					a.status = "updated environment for " + updated.Slug
					return a.reloadProjectAndOpenEnvironmentMenu(updated.ID)
				}})
			}

			a.menu = &tuiMenu{
				Title:       "Move Environment Command: " + command,
				Description: []string{"Choose how to reposition the selected project environment command."},
				Entries:     entries,
			}
			return nil
		},
	)
	return nil
}

func (a *vaxisTUIApp) openClearProjectEnvironmentCommandsDialog(project Project) {
	a.dialog = newTUIDialog(
		"Clear Environment Commands",
		"Clear",
		[]string{"Remove all environment commands from project " + project.Slug + "."},
		nil,
		func(_ map[string]string) error {
			updated, err := a.service.ProjectUpdate(project.ID, ProjectUpdateInput{ClearSetup: true})
			if err != nil {
				return err
			}
			a.status = "cleared environment for " + updated.Slug
			return a.reloadProjectAndOpenEnvironmentMenu(updated.ID)
		},
	)
}

func (a *vaxisTUIApp) openUpdateProjectDialog(project Project) {
	dialog := newTUIDialog(
		"Update Project",
		"Update",
		[]string{
			"Update the selected project slug, workspace path, and assigned environment configs.",
			"Use [e] to manage direct project-specific environment commands.",
		},
		[]tuiDialogField{
			newTUIInputField("slug", "Project Slug", project.Slug, true),
			newTUIInputField("workspace_path", "Workspace Path", project.WorkspacePath, true),
			newTUISelectorField("environment_configs", "Environment Configs", commaSeparatedValues(project.EnvironmentConfigs), false, nil),
		},
		func(values map[string]string) error {
			slug := values["slug"]
			workspacePath := values["workspace_path"]
			return a.startOperation("Saving project "+project.Slug, func(context.Context) (tuiOperationResult, error) {
				updated, err := a.service.ProjectUpdate(project.ID, ProjectUpdateInput{
					Slug:               &slug,
					WorkspacePath:      &workspacePath,
					EnvironmentConfigs: parseCommaSeparatedValues(values["environment_configs"]),
				})
				if err != nil {
					return tuiOperationResult{}, err
				}
				return tuiOperationResult{
					Status:       "updated project " + updated.Slug,
					PreferredKey: "project:" + updated.ID,
					ReloadData:   true,
				}, nil
			})
		},
	)
	dialog.Fields[2].Display = func(value string) string {
		return environmentConfigSelectionSummary(parseCommaSeparatedValues(value))
	}
	dialog.Fields[2].Activate = func() error {
		return a.openEnvironmentConfigSelector(
			"Select Environment Configs",
			[]string{"Choose reusable environment configs to keep assigned to this project."},
			parseCommaSeparatedValues(dialog.Fields[2].Value),
			true,
			func(values []string) error {
				dialog.SetFieldValue("environment_configs", commaSeparatedValues(values))
				return nil
			},
		)
	}
	a.dialog = dialog
}

func (a *vaxisTUIApp) openEnvironmentConfigsMenu() error {
	configs, err := a.service.EnvironmentConfigList(false)
	if err != nil {
		return err
	}

	description := []string{
		"Reusable environment command sets can be assigned to multiple projects.",
	}
	if len(configs) == 0 {
		description = append(description, "No environment configs configured.")
	} else {
		description = append(description, "Configured defaults:")
		for _, config := range configs {
			description = append(description, fmt.Sprintf("- %s (%d commands)", config.Slug, len(config.Commands)))
		}
	}

	entries := []tuiMenuEntry{
		{Key: 'c', Label: "Create Config", Action: func() error { a.openCreateEnvironmentConfigDialog(); return nil }},
	}
	if len(configs) > 0 {
		entries = append(entries, tuiMenuEntry{Key: 'm', Label: "Manage Config", Action: func() error { return a.openManageEnvironmentConfigDialog(configs[0].Slug) }})
	}

	a.menu = &tuiMenu{
		Title:       "Environment Configs",
		Description: description,
		Entries:     entries,
	}

	return nil
}

func (a *vaxisTUIApp) openCreateEnvironmentConfigDialog() {
	a.dialog = newTUIDialog(
		"Create Environment Config",
		"Create",
		[]string{
			"Create a reusable environment config for project bootstrap commands.",
			"Create the config first, then add or reorder as many commands as you need from the command editor.",
		},
		[]tuiDialogField{
			newTUIInputField("slug", "Config Slug", "", true),
		},
		func(values map[string]string) error {
			config, err := a.service.EnvironmentConfigCreate(EnvironmentConfigCreateInput{
				Slug: values["slug"],
			})
			if err != nil {
				return err
			}
			a.status = "created environment config " + config.Slug
			a.openEnvironmentConfigCommandMenu(config)
			return nil
		},
	)
}

func (a *vaxisTUIApp) openManageEnvironmentConfigDialog(defaultSlug string) error {
	selected := []string{}
	if strings.TrimSpace(defaultSlug) != "" {
		selected = append(selected, defaultSlug)
	}
	return a.openEnvironmentConfigSelector(
		"Manage Environment Config",
		[]string{"Choose an environment config to edit its commands or delete it."},
		selected,
		false,
		func(values []string) error {
			if len(values) == 0 {
				return fmt.Errorf("select an environment config")
			}
			config, err := a.service.EnvironmentConfigShow(values[0])
			if err != nil {
				return err
			}
			a.openEnvironmentConfigCommandMenu(config)
			return nil
		},
	)
}

func (a *vaxisTUIApp) openEnvironmentConfigCommandMenu(config EnvironmentConfig) {
	description := []string{
		"Commands in this config apply to new nodes for every project that references " + config.Slug + ".",
	}
	if len(config.Commands) == 0 {
		description = append(description, "No environment commands configured.")
	} else {
		description = append(description, "Configured commands:")
		for index, command := range config.Commands {
			description = append(description, fmt.Sprintf("%d. %s", index+1, command))
		}
	}

	a.menu = &tuiMenu{
		Title:       "Environment Config: " + config.Slug,
		Description: description,
		Entries: []tuiMenuEntry{
			{Key: 'a', Label: "Add Command", Action: func() error { a.openAddEnvironmentConfigCommandDialog(config); return nil }},
			{Key: 'r', Label: "Remove Command", Action: func() error { return a.openRemoveEnvironmentConfigCommandDialog(config) }},
			{Key: 'm', Label: "Move Command", Action: func() error { return a.openMoveEnvironmentConfigCommandDialog(config) }},
			{Key: 'c', Label: "Clear Commands", Action: func() error { a.openClearEnvironmentConfigCommandsDialog(config); return nil }},
			{Key: 'd', Label: "Delete Config", Action: func() error { a.openDeleteEnvironmentConfigDialog(config); return nil }},
		},
	}
}

func (a *vaxisTUIApp) openAddEnvironmentConfigCommandDialog(config EnvironmentConfig) {
	a.dialog = newTUIDialog(
		"Add Environment Config Command",
		"Add",
		[]string{"Add a command to the reusable environment config."},
		[]tuiDialogField{
			newTUIInputField("command", "Command", "", true),
		},
		func(values map[string]string) error {
			commands := append(append([]string(nil), config.Commands...), values["command"])
			updated, err := a.service.EnvironmentConfigUpdate(config.ID, EnvironmentConfigUpdateInput{Commands: commands})
			if err != nil {
				return err
			}
			a.status = "updated environment config " + updated.Slug
			return a.reopenEnvironmentConfigCommandMenu(updated.ID)
		},
	)
}

func (a *vaxisTUIApp) openRemoveEnvironmentConfigCommandDialog(config EnvironmentConfig) error {
	if len(config.Commands) == 0 {
		return fmt.Errorf("environment config %s has no commands", config.Slug)
	}

	a.selector = newTUISelector(
		"Remove Environment Config Commands",
		[]string{"Choose one or more reusable environment config commands to remove."},
		commandSelectorOptions(config.Commands),
		nil,
		true,
		func(values []string) error {
			indices, err := parseSelectorIndices(values, len(config.Commands))
			if err != nil {
				return err
			}
			if len(indices) == 0 {
				return fmt.Errorf("select at least one command to remove")
			}

			description := []string{"Remove the selected reusable environment config commands?"}
			for _, index := range indices {
				description = append(description, fmt.Sprintf("%d. %s", index+1, config.Commands[index]))
			}

			a.dialog = newTUIDialog(
				"Remove Environment Config Commands",
				"Remove",
				description,
				nil,
				func(map[string]string) error {
					updated, err := a.service.EnvironmentConfigUpdate(config.ID, EnvironmentConfigUpdateInput{
						Commands: removeCommandsByIndex(config.Commands, indices),
					})
					if err != nil {
						return err
					}
					a.status = "updated environment config " + updated.Slug
					return a.reopenEnvironmentConfigCommandMenu(updated.ID)
				},
			)
			return nil
		},
	)

	return nil
}

func (a *vaxisTUIApp) openMoveEnvironmentConfigCommandDialog(config EnvironmentConfig) error {
	if len(config.Commands) < 2 {
		return fmt.Errorf("environment config %s needs at least two commands to change order", config.Slug)
	}

	a.selector = newTUISelector(
		"Move Environment Config Command",
		[]string{"Choose a reusable environment config command to move up or down."},
		commandSelectorOptions(config.Commands),
		nil,
		false,
		func(values []string) error {
			indices, err := parseSelectorIndices(values, len(config.Commands))
			if err != nil {
				return err
			}
			if len(indices) != 1 {
				return fmt.Errorf("select a single command to move")
			}
			index := indices[0]
			command := config.Commands[index]

			entries := []tuiMenuEntry{}
			if index > 0 {
				entries = append(entries, tuiMenuEntry{Key: 'u', Label: "Move Up", Action: func() error {
					updated, err := a.service.EnvironmentConfigUpdate(config.ID, EnvironmentConfigUpdateInput{
						Commands: moveCommand(config.Commands, index, -1),
					})
					if err != nil {
						return err
					}
					a.status = "updated environment config " + updated.Slug
					return a.reopenEnvironmentConfigCommandMenu(updated.ID)
				}})
			}
			if index < len(config.Commands)-1 {
				entries = append(entries, tuiMenuEntry{Key: 'd', Label: "Move Down", Action: func() error {
					updated, err := a.service.EnvironmentConfigUpdate(config.ID, EnvironmentConfigUpdateInput{
						Commands: moveCommand(config.Commands, index, 1),
					})
					if err != nil {
						return err
					}
					a.status = "updated environment config " + updated.Slug
					return a.reopenEnvironmentConfigCommandMenu(updated.ID)
				}})
			}

			a.menu = &tuiMenu{
				Title:       "Move Environment Config Command: " + command,
				Description: []string{"Choose how to reposition the selected reusable environment config command."},
				Entries:     entries,
			}
			return nil
		},
	)
	return nil
}

func (a *vaxisTUIApp) openClearEnvironmentConfigCommandsDialog(config EnvironmentConfig) {
	a.dialog = newTUIDialog(
		"Clear Environment Config Commands",
		"Clear",
		[]string{"Remove all commands from environment config " + config.Slug + "."},
		nil,
		func(_ map[string]string) error {
			updated, err := a.service.EnvironmentConfigUpdate(config.ID, EnvironmentConfigUpdateInput{ClearCommands: true})
			if err != nil {
				return err
			}
			a.status = "cleared environment config " + updated.Slug
			return a.reopenEnvironmentConfigCommandMenu(updated.ID)
		},
	)
}

func (a *vaxisTUIApp) openDeleteEnvironmentConfigDialog(config EnvironmentConfig) {
	selectedKey := a.state.selectedEntry().key()
	a.dialog = newTUIDialog(
		"Delete Environment Config",
		"Delete",
		[]string{"Delete reusable environment config " + config.Slug + "."},
		nil,
		func(_ map[string]string) error {
			deleted, err := a.service.EnvironmentConfigDelete(config.ID)
			if err != nil {
				return err
			}
			a.status = "deleted environment config " + deleted.Slug
			return a.reloadData(selectedKey)
		},
	)
}

func (a *vaxisTUIApp) openDeleteProjectDialog(project Project) {
	a.dialog = newTUIDialog(
		"Delete Project",
		"Delete",
		[]string{
			"Delete project " + project.Slug + ".",
			"This only succeeds if the project has no live nodes or child projects.",
		},
		nil,
		func(_ map[string]string) error {
			deleted, err := a.service.ProjectDelete(project.ID)
			if err != nil {
				return err
			}
			a.status = "deleted project " + deleted.Slug
			return a.reloadData("")
		},
	)
}

func (a *vaxisTUIApp) openDeleteNodeDialog(node Node) {
	a.dialog = newTUIDialog(
		"Delete Node",
		"Delete",
		[]string{
			"Delete node " + node.Slug + ".",
			"The associated Lima instance will be terminated.",
		},
		nil,
		func(_ map[string]string) error {
			return a.startOperation("Deleting node "+node.Slug, func(ctx context.Context) (tuiOperationResult, error) {
				deleted, err := a.service.NodeDelete(ctx, node.ID)
				if err != nil {
					return tuiOperationResult{}, err
				}
				return tuiOperationResult{
					Status:      "deleted node " + deleted.Slug,
					CloseNodeID: deleted.ID,
					ReloadData:  true,
				}, nil
			})
		},
	)
}

func (a *vaxisTUIApp) openCloneNodeDialog(node Node, project Project) {
	a.dialog = newTUIDialog(
		"Clone Node",
		"Clone",
		[]string{
			"Clone the selected node into another node in the same project.",
			"The cloned VM keeps the same guest workspace path and bootstrap state as the source.",
		},
		[]tuiDialogField{
			newTUIInputField("node_slug", "Cloned Node Slug", node.Slug+"-clone", true),
		},
		func(values map[string]string) error {
			return a.startOperation("Cloning node "+node.Slug, func(ctx context.Context) (tuiOperationResult, error) {
				childNode, err := a.service.NodeClone(ctx, NodeCloneInput{
					SourceNode: node.ID,
					NodeSlug:   values["node_slug"],
				})
				if err != nil {
					return tuiOperationResult{}, err
				}
				return tuiOperationResult{
					Status:       "cloned node " + node.Slug + " to " + childNode.Slug + " in " + project.Slug,
					PreferredKey: "node:" + childNode.ID,
					ReloadData:   true,
				}, nil
			})
		},
	)
}

func (a *vaxisTUIApp) openPatchMenu(node Node, project Project) {
	a.menu = &tuiMenu{
		Title: "Patch Operations",
		Description: []string{
			"Selected node: " + node.Slug,
			"Selected project: " + project.Slug,
		},
		Entries: []tuiMenuEntry{
			{Key: 'p', Label: "Propose Patch", Action: func() error { a.openPatchProposeDialog(node, project); return nil }},
			{Key: 'a', Label: "Approve Patch", Action: func() error { a.openPatchApproveDialog(project); return nil }},
			{Key: 'y', Label: "Apply Patch", Action: func() error { a.openPatchApplyDialog(project); return nil }},
			{Key: 'r', Label: "Reject Patch", Action: func() error { a.openPatchRejectDialog(project); return nil }},
		},
	}
}

func (a *vaxisTUIApp) openPatchProposeDialog(node Node, project Project) {
	a.dialog = newTUIDialog(
		"Propose Patch",
		"Propose",
		[]string{
			"Source project is " + project.Slug + " and source node is " + node.Slug + ".",
			"Enter the direct lineage neighbor project to target.",
		},
		[]tuiDialogField{
			newTUIInputField("target_project", "Target Project Slug", "", true),
			newTUIInputField("target_node", "Target Node Slug", "", false),
		},
		func(values map[string]string) error {
			proposal, err := a.service.PatchPropose(a.ctx, PatchProposeInput{
				SourceProject: project.ID,
				SourceNode:    node.ID,
				TargetProject: values["target_project"],
				TargetNode:    values["target_node"],
			})
			if err != nil {
				return err
			}
			a.status = "proposed patch " + proposal.ID
			return a.reloadData("node:" + node.ID)
		},
	)
}

func (a *vaxisTUIApp) openPatchApproveDialog(project Project) {
	a.dialog = newTUIDialog(
		"Approve Patch",
		"Approve",
		[]string{"Approve a submitted patch related to project " + project.Slug + "."},
		[]tuiDialogField{
			newTUIInputField("patch_id", "Patch ID", "", true),
			newTUIInputField("actor", "Actor", "tui", true),
			newTUIInputField("note", "Note", "", false),
		},
		func(values map[string]string) error {
			proposal, err := a.service.PatchApprove(values["patch_id"], values["actor"], values["note"])
			if err != nil {
				return err
			}
			a.status = "approved patch " + proposal.ID
			return a.reloadPatches()
		},
	)
}

func (a *vaxisTUIApp) openPatchApplyDialog(project Project) {
	a.dialog = newTUIDialog(
		"Apply Patch",
		"Apply",
		[]string{"Apply an approved patch related to project " + project.Slug + "."},
		[]tuiDialogField{
			newTUIInputField("patch_id", "Patch ID", "", true),
		},
		func(values map[string]string) error {
			proposal, err := a.service.PatchApply(a.ctx, values["patch_id"])
			if err != nil {
				return err
			}
			a.status = "applied patch " + proposal.ID
			return a.reloadPatches()
		},
	)
}

func (a *vaxisTUIApp) openPatchRejectDialog(project Project) {
	a.dialog = newTUIDialog(
		"Reject Patch",
		"Reject",
		[]string{"Reject a submitted or approved patch related to project " + project.Slug + "."},
		[]tuiDialogField{
			newTUIInputField("patch_id", "Patch ID", "", true),
			newTUIInputField("actor", "Actor", "tui", true),
			newTUIInputField("note", "Note", "", false),
		},
		func(values map[string]string) error {
			proposal, err := a.service.PatchReject(values["patch_id"], values["actor"], values["note"])
			if err != nil {
				return err
			}
			a.status = "rejected patch " + proposal.ID
			return a.reloadPatches()
		},
	)
}

func (a *vaxisTUIApp) forwardTerminalEvent(event vaxis.Event) {
	if a.state.focus != tuiFocusTerminal {
		return
	}

	a.forwardSessionEvent(a.state.activeNodeID, event)
}

func (a *vaxisTUIApp) forwardSessionEvent(nodeID string, event vaxis.Event) {
	session, ok := a.sessions.Session(nodeID)
	if !ok {
		return
	}
	session.terminal.Update(event)
}

func (a *vaxisTUIApp) handleTerminalSelection(nodeID string, nodeSlug string, snapshot string, mouse vaxis.Mouse) {
	switch mouse.EventType {
	case vaxis.EventPress:
		a.beginTerminalSelection(nodeID, mouse)
	case vaxis.EventMotion:
		a.updateTerminalSelection(nodeID, mouse)
	case vaxis.EventRelease:
		a.finishTerminalSelection(nodeID, nodeSlug, snapshot, mouse)
	}
}

func (a *vaxisTUIApp) beginTerminalSelection(nodeID string, mouse vaxis.Mouse) {
	point := tuiPoint{col: mouse.Col, row: mouse.Row}
	a.selection = &tuiTerminalSelection{
		nodeID: nodeID,
		start:  point,
		end:    point,
	}
}

func (a *vaxisTUIApp) updateTerminalSelection(nodeID string, mouse vaxis.Mouse) {
	if a.selection == nil || a.selection.nodeID != nodeID {
		return
	}

	point := tuiPoint{col: mouse.Col, row: mouse.Row}
	if point != a.selection.start {
		a.selection.dragged = true
	}
	a.selection.end = point
}

func (a *vaxisTUIApp) finishTerminalSelection(nodeID string, nodeSlug string, snapshot string, mouse vaxis.Mouse) bool {
	if a.selection == nil || a.selection.nodeID != nodeID {
		return false
	}

	point := tuiPoint{col: mouse.Col, row: mouse.Row}
	if point != a.selection.start {
		a.selection.dragged = true
	}
	a.selection.end = point
	selection := *a.selection
	a.selection = nil

	if !selection.dragged {
		return false
	}

	text := extractTerminalSelection(snapshot, selection)
	if strings.TrimSpace(text) == "" {
		return true
	}

	copySelection := a.copySelection
	if copySelection == nil {
		copySelection = func(text string) error {
			if a.vx != nil {
				return copyTextToClipboard(text, a.vx.ClipboardPush)
			}
			return copyTextToClipboard(text, nil)
		}
	}
	if err := copySelection(text); err != nil {
		a.status = err.Error()
		return true
	}
	a.status = fmt.Sprintf("copied %d bytes from %s", len(text), nodeSlug)
	return true
}

func (a *vaxisTUIApp) syncSessionFocus() {
	for nodeID, session := range a.sessions.sessions {
		if nodeID == a.state.activeNodeID && a.state.focus == tuiFocusTerminal {
			session.terminal.Focus()
			continue
		}
		session.terminal.Blur()
	}
}

func (a *vaxisTUIApp) linkTargetAt(col, row int) (string, bool) {
	for _, region := range a.linkRegions {
		if region.rect.contains(col, row) {
			return region.target, true
		}
	}
	return "", false
}

func (a *vaxisTUIApp) printLinkifiedLine(win vaxis.Window, row int, text string, style vaxis.Style) {
	segments := linkifiedSegments(text, style)
	win.PrintTruncate(row, segments...)

	originCol, originRow := win.Origin()
	width, _ := win.Size()
	col := 0
	for _, segment := range segments {
		segmentWidth := renderedTextWidth(a.vx, segment.Text)
		if segment.Style.Hyperlink != "" && segmentWidth > 0 && col < width {
			visibleWidth := segmentWidth
			if col+visibleWidth > width {
				visibleWidth = width - col
			}
			if visibleWidth > 0 {
				a.linkRegions = append(a.linkRegions, tuiLinkRegion{
					rect: tuiRect{
						col:    originCol + col,
						row:    originRow + row,
						width:  visibleWidth,
						height: 1,
					},
					target: segment.Style.Hyperlink,
				})
			}
		}
		col += segmentWidth
		if col >= width {
			break
		}
	}
}

func (a *vaxisTUIApp) drawTerminalSelection(win vaxis.Window, snapshot string, style vaxis.Style) {
	if a.selection == nil {
		return
	}

	lines := strings.Split(snapshot, "\n")
	start, end := normalizedSelection(*a.selection)
	if len(lines) == 0 || start.row >= len(lines) {
		return
	}
	if end.row >= len(lines) {
		end.row = len(lines) - 1
	}

	for row := start.row; row <= end.row; row++ {
		line := []rune(lines[row])
		if len(line) == 0 {
			continue
		}
		from := 0
		to := len(line)
		if row == start.row {
			from = clampInt(start.col, 0, len(line)-1)
		}
		if row == end.row {
			to = clampInt(end.col+1, 0, len(line))
		}
		if from > to {
			from, to = to, from
		}
		for col := from; col < to; col++ {
			win.SetCell(col, row, vaxis.Cell{
				Character: vaxis.Character{
					Grapheme: string(line[col]),
					Width:    1,
				},
				Style: style,
			})
		}
	}
}

func (a *vaxisTUIApp) drawOperationOverlay(win vaxis.Window, headerStyle, mutedStyle vaxis.Style) {
	if a.operation == nil {
		return
	}

	body := border.All(win, mutedStyle)
	body.Println(0, vaxis.Segment{Text: a.operation.Title, Style: headerStyle})
	body.Println(1, vaxis.Segment{Text: "Streaming output from Lima and guest bootstrap commands...", Style: mutedStyle})

	_, bodyHeight := body.Size()
	maxLines := bodyHeight - 4
	if maxLines < 1 {
		maxLines = 1
	}
	lines := a.operation.Lines
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	for index, line := range lines {
		a.printLinkifiedLine(body, index+2, line, mutedStyle)
	}
}

func (a *vaxisTUIApp) draw() {
	if a.vx == nil {
		return
	}

	window := a.vx.Window()
	window.Clear()
	a.vx.HideCursor()

	width, height := window.Size()
	a.treeContentRect = tuiRect{}
	a.terminalBodyRect = tuiRect{}
	a.linkRegions = nil
	if width < 60 || height < 14 {
		window.Println(0, vaxis.Segment{Text: "CodeLima TUI requires at least 60x14 terminal cells."})
		window.Println(1, vaxis.Segment{Text: fmt.Sprintf("Current size: %dx%d", width, height)})
		a.vx.Render()
		return
	}

	headerStyle := vaxis.Style{Attribute: vaxis.AttrBold}
	selectedStyle := vaxis.Style{
		Foreground: vaxis.ColorWhite,
		Background: vaxis.ColorBlue,
		Attribute:  vaxis.AttrBold,
	}
	mutedStyle := tuiMutedStyle()
	errorStyle := vaxis.Style{Foreground: vaxis.ColorRed, Attribute: vaxis.AttrBold}
	selectionStyle := vaxis.Style{
		Foreground: vaxis.ColorBlack,
		Background: vaxis.ColorWhite,
	}

	projectSlug := "none"
	if project, ok := a.state.activeProject(); ok {
		projectSlug = project.Slug
	}

	selectedNode := "none"
	if node, ok := a.state.activeNode(); ok {
		selectedNode = node.Slug
	}

	window.Println(0,
		vaxis.Segment{Text: "Project: " + projectSlug, Style: headerStyle},
		vaxis.Segment{Text: "  Node: " + selectedNode},
	)

	bodyTop := 1
	bodyHeight := height - bodyTop - 1
	layout := layoutTUIBody(width, a.state.focus)
	termOuter := window.New(layout.termCol, bodyTop, layout.termWidth, bodyHeight)
	termBody := drawTerminalPane(termOuter, mutedStyle)

	if layout.treeVisible {
		treeOuter := window.New(0, bodyTop, layout.treeWidth, bodyHeight)
		treeInner := border.All(treeOuter, mutedStyle)

		treeInner.Println(0, vaxis.Segment{Text: "Projects / Nodes", Style: headerStyle})
		helpLines := []string{
			"Mouse click: select project or node",
			"Up/Down move, Left/Right collapse/expand",
			"Action hotkeys are shown in the right pane",
		}

		treeInnerWidth, treeInnerHeight := treeInner.Size()
		treeContentHeight := treeInnerHeight - 1 - len(helpLines)
		if treeContentHeight < 0 {
			treeContentHeight = 0
		}
		treeContent := treeInner.New(0, 1, treeInnerWidth, treeContentHeight)
		treeOriginCol, treeOriginRow := treeContent.Origin()
		a.treeContentRect = tuiRect{col: treeOriginCol, row: treeOriginRow, width: treeInnerWidth, height: treeContentHeight}

		for row, entry := range a.state.visibleEntries(treeContentHeight) {
			index := a.state.viewportStart(treeContentHeight) + row
			style := mutedStyle
			if index == a.state.selection {
				style = selectedStyle
			}

			label := tuiEntryLabel(entry)
			treeContent.Println(row, vaxis.Segment{Text: label, Style: style})
		}

		for index, line := range helpLines {
			treeInner.Println(treeInnerHeight-len(helpLines)+index, vaxis.Segment{Text: line, Style: mutedStyle})
		}
	}

	entry := a.state.selectedEntry()
	termInnerWidth, termInnerHeight := termBody.Size()
	termOriginCol, termOriginRow := termBody.Origin()
	a.terminalBodyRect = tuiRect{col: termOriginCol, row: termOriginRow, width: termInnerWidth, height: termInnerHeight}

	if entry.kind == tuiTreeEntryNode && a.sessions.HasSession(entry.node.ID) {
		if session, ok := a.sessions.Session(entry.node.ID); ok {
			session.terminal.Draw(termBody)
			if a.selection != nil && a.selection.nodeID == entry.node.ID {
				a.drawTerminalSelection(termBody, session.terminal.String(), selectionStyle)
			}
		} else {
			termBody.Println(0, vaxis.Segment{Text: "Shell session is not running. Select the node again or press Alt-` to reopen.", Style: mutedStyle})
		}
	} else {
		a.drawDetails(termBody, entry, headerStyle, mutedStyle)
	}

	footer := renderFooter(a.state.focus, entry)
	footerStyle := mutedStyle
	if a.status != "" {
		footer = a.status
		footerStyle = errorStyle
	}
	window.Println(height-1, vaxis.Segment{Text: footer, Style: footerStyle})

	if a.menu != nil {
		a.drawOverlay(window, 56, 11, func(overlay vaxis.Window) {
			a.menu.Draw(overlay, headerStyle, mutedStyle)
		})
	}
	if a.dialog != nil {
		dialogHeight := 8 + len(a.dialog.Description) + len(a.dialog.Fields)*3
		a.drawOverlay(window, 72, dialogHeight, func(overlay vaxis.Window) {
			a.dialog.Draw(overlay, headerStyle, mutedStyle, errorStyle)
		})
	}
	if a.selector != nil {
		a.drawOverlay(window, 72, a.selector.Height(), func(overlay vaxis.Window) {
			a.selector.Draw(overlay, headerStyle, mutedStyle)
		})
	}
	if a.operation != nil {
		a.drawOverlay(window, 92, 14, func(overlay vaxis.Window) {
			a.drawOperationOverlay(overlay, headerStyle, mutedStyle)
		})
	}

	a.vx.Render()
}

func tuiMutedStyle() vaxis.Style {
	return vaxis.Style{Foreground: vaxis.ColorSilver}
}

func (a *vaxisTUIApp) drawDetails(win vaxis.Window, entry tuiTreeEntry, headerStyle, mutedStyle vaxis.Style) {
	row := 0
	switch entry.kind {
	case tuiTreeEntryProject:
		win.Println(row, vaxis.Segment{Text: "Project controls", Style: headerStyle})
		row++
		win.Println(row, vaxis.Segment{Text: "Slug: " + entry.project.Slug})
		row++
		a.printLinkifiedLine(win, row, "Workspace: "+entry.project.WorkspacePath, vaxis.Style{})
		row += 2
		if len(entry.project.EnvironmentConfigs) == 0 {
			win.Println(row, vaxis.Segment{Text: "Environment configs: none", Style: mutedStyle})
			row++
		} else {
			win.Println(row, vaxis.Segment{Text: "Environment configs: " + commaSeparatedValues(entry.project.EnvironmentConfigs), Style: mutedStyle})
			row++
		}
		if len(entry.project.SetupCommands) == 0 {
			win.Println(row, vaxis.Segment{Text: "Environment commands: none", Style: mutedStyle})
			row++
		} else {
			win.Println(row, vaxis.Segment{Text: fmt.Sprintf("Environment commands: %d", len(entry.project.SetupCommands)), Style: mutedStyle})
			row++
			for index, command := range entry.project.SetupCommands {
				if index >= 3 {
					win.Println(row, vaxis.Segment{Text: fmt.Sprintf("... %d more", len(entry.project.SetupCommands)-index), Style: mutedStyle})
					row++
					break
				}
				win.Println(row, vaxis.Segment{Text: fmt.Sprintf("%d. %s", index+1, command), Style: mutedStyle})
				row++
			}
		}
		row++
		win.Println(row, vaxis.Segment{Text: "Create nodes, assign reusable environment configs, manage direct environment commands, update the project binding, or delete the project from the tree view.", Style: mutedStyle})
		row += 2
		for _, patch := range a.relatedPatches(entry.project.ID) {
			win.Println(row, vaxis.Segment{Text: patchSummary(patch), Style: mutedStyle})
			row++
		}
	case tuiTreeEntryNode:
		win.Println(row, vaxis.Segment{Text: "Node controls", Style: headerStyle})
		row++
		win.Println(row, vaxis.Segment{Text: "Slug: " + entry.node.Slug})
		row++
		win.Println(row, vaxis.Segment{Text: "Status: " + nodeVMStatus(entry.node)})
		row++
		if workspace := nodeWorkspacePath(entry.node); workspace != "" {
			a.printLinkifiedLine(win, row, "Workspace: "+workspace, vaxis.Style{})
			row++
		}
		row++
		if nodeAutoStartsSession(entry.node) {
			win.Println(row, vaxis.Segment{Text: "Node is running. Press Alt-` to focus its terminal session.", Style: mutedStyle})
			row += 2
		} else {
			win.Println(row, vaxis.Segment{Text: "Start the node before focusing its terminal session.", Style: mutedStyle})
			row += 2
		}
		for _, patch := range a.relatedPatches(entry.project.ID) {
			win.Println(row, vaxis.Segment{Text: patchSummary(patch), Style: mutedStyle})
			row++
		}
	default:
		win.Println(0, vaxis.Segment{Text: "Press [a] to create a project or select a project or node in the tree.", Style: mutedStyle})
	}
}

func (a *vaxisTUIApp) drawOverlay(win vaxis.Window, width int, height int, draw func(vaxis.Window)) {
	winWidth, winHeight := win.Size()
	if width > winWidth-4 {
		width = winWidth - 4
	}
	if height > winHeight-4 {
		height = winHeight - 4
	}
	if width < 10 || height < 4 {
		return
	}

	col := (winWidth - width) / 2
	row := (winHeight - height) / 2
	overlay := win.New(col, row, width, height)
	overlay.Fill(vaxis.Cell{Character: vaxis.Character{Grapheme: " ", Width: 1}})
	draw(overlay)
}

func drawTerminalPane(win vaxis.Window, style vaxis.Style) vaxis.Window {
	width, height := win.Size()
	if width <= 0 || height <= 0 {
		return win
	}

	borderCell := vaxis.Cell{
		Character: vaxis.Character{Grapheme: "─", Width: 1},
		Style:     style,
	}
	for col := range width {
		win.SetCell(col, 0, borderCell)
		if height > 1 {
			win.SetCell(col, height-1, borderCell)
		}
	}

	if height <= 2 {
		return win.New(0, 0, width, height)
	}
	return win.New(0, 1, width, height-2)
}

func renderFooter(focus tuiFocus, entry tuiTreeEntry) string {
	if focus == tuiFocusTerminal {
		return "Alt-` tree focus   drag copy   Shift-drag force local copy   q quit"
	}
	if entry.kind == "" {
		return "Press [a] to add a project   q quit"
	}
	if entry.kind == tuiTreeEntryNode {
		return "Up/Down move   Left/Right collapse   Alt-` shell focus   wheel scroll   drag copy   q quit"
	}
	return "Up/Down move   Left/Right collapse   Use action hotkeys in the right pane   q quit"
}

func (a *vaxisTUIApp) relatedPatches(projectID string) []PatchProposal {
	if projectID == "" {
		return nil
	}

	related := make([]PatchProposal, 0, 4)
	for index := len(a.patches) - 1; index >= 0 && len(related) < 4; index-- {
		patch := a.patches[index]
		if patch.SourceProjectID != projectID && patch.TargetProjectID != projectID {
			continue
		}
		related = append(related, patch)
	}
	return related
}

func patchSummary(patch PatchProposal) string {
	return patch.ID + "  " + patch.Status + "  " + patch.Direction
}

func tuiEntryLabel(entry tuiTreeEntry) string {
	indent := strings.Repeat("  ", entry.depth)
	switch entry.kind {
	case tuiTreeEntryProject:
		marker := "▶"
		if entry.expanded {
			marker = "▼"
		}
		if !entry.hasChildren {
			marker = "•"
		}
		return indent + marker + " " + entry.project.Slug
	case tuiTreeEntryNode:
		status := strings.ToUpper(nodeVMStatus(entry.node))
		return indent + "• " + entry.node.Slug + "  " + status
	default:
		return ""
	}
}

func isTerminalViewToggleKey(key vaxis.Key) bool {
	return key.Matches('`', vaxis.ModAlt)
}

func isQuitKey(key vaxis.Key) bool {
	return key.Matches('c', vaxis.ModCtrl)
}
