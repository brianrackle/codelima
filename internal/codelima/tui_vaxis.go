package codelima

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	terminal *term.Model
}

type tuiSessionStore struct {
	ctx            context.Context
	service        *Service
	postEvent      func(vaxis.Event)
	sessions       map[string]*tuiSession
	nodeByTerminal map[*term.Model]string
}

func newTUISessionStore(ctx context.Context, service *Service, postEvent func(vaxis.Event)) *tuiSessionStore {
	return &tuiSessionStore{
		ctx:            ctx,
		service:        service,
		postEvent:      postEvent,
		sessions:       map[string]*tuiSession{},
		nodeByTerminal: map[*term.Model]string{},
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

	terminal := term.New()
	terminal.Attach(s.postEvent)
	if err := terminal.Start(command); err != nil {
		return err
	}

	session := &tuiSession{node: node, terminal: terminal}
	s.sessions[node.ID] = session
	s.nodeByTerminal[terminal] = node.ID
	return nil
}

func (s *tuiSessionStore) Session(nodeID string) (*tuiSession, bool) {
	session, ok := s.sessions[nodeID]
	return session, ok
}

func (s *tuiSessionStore) RemoveClosed(terminal *term.Model) (*tuiSession, string, bool) {
	nodeID, ok := s.nodeByTerminal[terminal]
	if !ok {
		return nil, "", false
	}

	session := s.sessions[nodeID]
	delete(s.nodeByTerminal, terminal)
	delete(s.sessions, nodeID)
	return session, nodeID, true
}

func (s *tuiSessionStore) Close() {
	for nodeID, session := range s.sessions {
		session.terminal.Close()
		delete(s.nodeByTerminal, session.terminal)
		delete(s.sessions, nodeID)
	}
}

func (s *tuiSessionStore) CloseNode(nodeID string) {
	session, ok := s.sessions[nodeID]
	if !ok {
		return
	}

	delete(s.nodeByTerminal, session.terminal)
	delete(s.sessions, nodeID)
	session.terminal.Close()
}

type tuiRect struct {
	col    int
	row    int
	width  int
	height int
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

type vaxisTUIApp struct {
	ctx              context.Context
	service          *Service
	vx               *vaxis.Vaxis
	state            *tuiState
	sessions         *tuiSessionStore
	patches          []PatchProposal
	dialog           *tuiDialog
	menu             *tuiMenu
	status           string
	treeContentRect  tuiRect
	terminalBodyRect tuiRect
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
		ctx:      ctx,
		service:  service,
		vx:       vx,
		state:    state,
		sessions: sessions,
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
	if key, ok := event.(vaxis.Key); ok && isQuitKey(key) && (a.dialog != nil || a.menu != nil) {
		return true, nil
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
	case term.EventClosed:
		a.handleTerminalClosed(event)
		a.draw()
	case term.EventPanic:
		a.status = error(event).Error()
		a.draw()
	case vaxis.QuitEvent:
		return true, nil
	}

	return false, nil
}

func (a *vaxisTUIApp) handleKey(key vaxis.Key) (bool, error) {
	if a.state.focus == tuiFocusTerminal {
		if isTreeEscapeKey(key) {
			a.state.focusTree()
			a.syncSessionFocus()
			return false, nil
		}
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
	case key.MatchString("Enter"), key.MatchString("Tab"):
		err = a.state.focusTerminal()
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
	case tuiActionProjectCreateNode:
		a.openCreateNodeDialog(entry.project)
	case tuiActionProjectUpdate:
		a.openUpdateProjectDialog(entry.project)
	case tuiActionProjectDelete:
		a.openDeleteProjectDialog(entry.project)
	case tuiActionNodeStart:
		node, err := a.service.NodeStart(a.ctx, entry.node.ID)
		if err != nil {
			return err
		}
		a.status = "started node " + node.Slug
		return a.reloadData("node:" + node.ID)
	case tuiActionNodeStop:
		node, err := a.service.NodeStop(a.ctx, entry.node.ID)
		if err != nil {
			return err
		}
		a.sessions.CloseNode(node.ID)
		a.status = "stopped node " + node.Slug
		return a.reloadData("node:" + node.ID)
	case tuiActionNodeDelete:
		a.openDeleteNodeDialog(entry.node)
	case tuiActionNodeClone:
		a.openCloneNodeDialog(entry.node, entry.project)
	case tuiActionNodePatch:
		a.openPatchMenu(entry.node, entry.project)
	}

	return nil
}

func (a *vaxisTUIApp) handleMouse(mouse vaxis.Mouse) error {
	if a.treeContentRect.contains(mouse.Col, mouse.Row) && mouse.EventType == vaxis.EventPress && mouse.Button == vaxis.MouseLeftButton {
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

	if mouse.EventType != vaxis.EventPress || mouse.Button != vaxis.MouseLeftButton {
		if a.state.focus == tuiFocusTerminal {
			a.forwardTerminalEvent(a.terminalBodyRect.translateMouse(mouse))
		}
		return nil
	}

	if err := a.state.focusTerminal(); err != nil {
		a.status = err.Error()
		return nil
	}
	a.status = ""
	a.syncSessionFocus()
	a.forwardTerminalEvent(a.terminalBodyRect.translateMouse(mouse))
	return nil
}

func (a *vaxisTUIApp) handleTerminalClosed(event term.EventClosed) {
	session, nodeID, ok := a.sessions.RemoveClosed(event.Term)
	if !ok {
		return
	}

	if a.state.activeNodeID == nodeID {
		a.state.activeNodeID = ""
		if a.state.focus == tuiFocusTerminal {
			a.state.focusTree()
		}
	}

	message := fmt.Sprintf("shell exited for %s", session.node.Slug)
	if event.Error != nil {
		message = fmt.Sprintf("%s: %s", message, event.Error)
	}
	a.status = message
	a.syncSessionFocus()
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

func (a *vaxisTUIApp) openCreateProjectDialog() {
	defaultSlug := ""
	defaultWorkspace := ""
	description := []string{
		"Create a top-level project rooted at a host workspace.",
		"Use node clone when you want a child project copied from an existing VM.",
	}

	if project, ok := a.state.activeProject(); ok {
		defaultSlug = project.Slug + "-new"
		defaultWorkspace = filepath.Join(filepath.Dir(project.WorkspacePath), project.Slug+"-new")
	}

	a.dialog = newTUIDialog(
		"Create Project",
		"Create",
		description,
		[]tuiDialogField{
			newTUIInputField("slug", "Project Slug", defaultSlug, false),
			newTUIInputField("workspace_path", "Workspace Path", defaultWorkspace, true),
		},
		func(values map[string]string) error {
			project, err := a.service.ProjectCreate(a.ctx, ProjectCreateInput{
				Slug:          values["slug"],
				WorkspacePath: values["workspace_path"],
			})
			if err != nil {
				return err
			}
			a.status = "created project " + project.Slug
			return a.reloadData("project:" + project.ID)
		},
	)
}

func (a *vaxisTUIApp) openCreateNodeDialog(project Project) {
	a.dialog = newTUIDialog(
		"Create Node",
		"Create",
		[]string{
			"Selected project: " + project.Slug,
			"Uses the project's existing runtime, agent, and setup defaults.",
		},
		[]tuiDialogField{
			newTUIInputField("slug", "Node Slug", project.Slug+"-node", true),
		},
		func(values map[string]string) error {
			node, err := a.service.NodeCreate(a.ctx, NodeCreateInput{
				Project: project.ID,
				Slug:    values["slug"],
			})
			if err != nil {
				return err
			}
			a.status = "created node " + node.Slug
			return a.reloadData("node:" + node.ID)
		},
	)
}

func (a *vaxisTUIApp) openUpdateProjectDialog(project Project) {
	a.dialog = newTUIDialog(
		"Update Project",
		"Update",
		[]string{"Update the selected project slug or workspace path."},
		[]tuiDialogField{
			newTUIInputField("slug", "Project Slug", project.Slug, true),
			newTUIInputField("workspace_path", "Workspace Path", project.WorkspacePath, true),
		},
		func(values map[string]string) error {
			slug := values["slug"]
			workspacePath := values["workspace_path"]
			updated, err := a.service.ProjectUpdate(project.ID, ProjectUpdateInput{
				Slug:          &slug,
				WorkspacePath: &workspacePath,
			})
			if err != nil {
				return err
			}
			a.status = "updated project " + updated.Slug
			return a.reloadData("project:" + updated.ID)
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
			deleted, err := a.service.NodeDelete(a.ctx, node.ID)
			if err != nil {
				return err
			}
			a.sessions.CloseNode(deleted.ID)
			a.status = "deleted node " + deleted.Slug
			return a.reloadData("")
		},
	)
}

func (a *vaxisTUIApp) openCloneNodeDialog(node Node, project Project) {
	defaultWorkspace := filepath.Join(filepath.Dir(project.WorkspacePath), node.Slug+"-clone")
	a.dialog = newTUIDialog(
		"Clone Node",
		"Clone",
		[]string{
			"Clone the selected node into a child project and child node.",
			"Workspace path must point at an empty or missing host directory.",
		},
		[]tuiDialogField{
			newTUIInputField("project_slug", "Child Project Slug", project.Slug+"-child", true),
			newTUIInputField("node_slug", "Child Node Slug", node.Slug+"-clone", true),
			newTUIInputField("workspace_path", "Child Workspace Path", defaultWorkspace, true),
		},
		func(values map[string]string) error {
			childProject, childNode, err := a.service.NodeClone(a.ctx, NodeCloneInput{
				SourceNode:    node.ID,
				ProjectSlug:   values["project_slug"],
				NodeSlug:      values["node_slug"],
				WorkspacePath: values["workspace_path"],
			})
			if err != nil {
				return err
			}
			a.status = "cloned node " + node.Slug + " to " + childNode.Slug + " in " + childProject.Slug
			return a.reloadData("node:" + childNode.ID)
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

	session, ok := a.sessions.Session(a.state.activeNodeID)
	if !ok {
		return
	}

	session.terminal.Update(event)
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
	mutedStyle := vaxis.Style{Foreground: vaxis.ColorGray}
	errorStyle := vaxis.Style{Foreground: vaxis.ColorRed, Attribute: vaxis.AttrBold}

	projectSlug := "none"
	if project, ok := a.state.activeProject(); ok {
		projectSlug = project.Slug
	}

	selectedNode := "none"
	if node, ok := a.state.activeNode(); ok {
		selectedNode = node.Slug
	}

	window.Println(0,
		vaxis.Segment{Text: "CodeLima TUI", Style: headerStyle},
		vaxis.Segment{Text: "  Project: " + projectSlug},
		vaxis.Segment{Text: "  Selected Node: " + selectedNode},
		vaxis.Segment{Text: "  Focus: " + string(a.state.focus)},
	)

	window.Println(1,
		vaxis.Segment{Text: "Shell-first layout  Mouse: enabled  Auto-switch on node selection", Style: mutedStyle},
	)

	bodyTop := 2
	bodyHeight := height - bodyTop - 1
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
	termWidth := width - treeWidth - 1

	treeOuter := window.New(0, bodyTop, treeWidth, bodyHeight)
	treeInner := border.All(treeOuter, mutedStyle)
	termOuter := window.New(treeWidth+1, bodyTop, termWidth, bodyHeight)
	termInner := border.All(termOuter, mutedStyle)

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

	entry := a.state.selectedEntry()
	terminalTitle, terminalSubtitle := a.panelHeader(entry)
	termInner.Println(0, vaxis.Segment{Text: terminalTitle, Style: headerStyle})
	if a.status != "" {
		termInner.Println(1, vaxis.Segment{Text: a.status, Style: errorStyle})
	} else {
		termInner.Println(1, vaxis.Segment{Text: terminalSubtitle, Style: mutedStyle})
	}
	termInner.Println(2, vaxis.Segment{Text: renderActionHints(availableTUIActions(entry)), Style: mutedStyle})

	termInnerWidth, termInnerHeight := termInner.Size()
	termBody := termInner.New(0, 3, termInnerWidth, termInnerHeight-3)
	termOriginCol, termOriginRow := termBody.Origin()
	a.terminalBodyRect = tuiRect{col: termOriginCol, row: termOriginRow, width: termInnerWidth, height: termInnerHeight - 3}

	if entry.kind == tuiTreeEntryNode && a.sessions.HasSession(entry.node.ID) {
		if session, ok := a.sessions.Session(entry.node.ID); ok {
			session.terminal.Draw(termBody)
		} else {
			termBody.Println(0, vaxis.Segment{Text: "Shell session is not running. Select the node again or press Enter to reopen.", Style: mutedStyle})
		}
	} else {
		a.drawDetails(termBody, entry, headerStyle, mutedStyle)
	}

	footer := renderFooter(a.state.focus, entry)
	if a.state.focus == tuiFocusTerminal {
		footer = "Terminal focused: all input passes through to the shell until Alt-` returns to the tree"
	}
	window.Println(height-1, vaxis.Segment{Text: footer, Style: mutedStyle})

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

	a.vx.Render()
}

func (a *vaxisTUIApp) panelHeader(entry tuiTreeEntry) (title string, subtitle string) {
	switch entry.kind {
	case tuiTreeEntryProject:
		subtitle = entry.project.WorkspacePath
		if subtitle == "" {
			subtitle = "workspace path unavailable"
		}
		return "Project: " + entry.project.Slug, subtitle
	case tuiTreeEntryNode:
		subtitle = "status: " + nodeVMStatus(entry.node)
		if workspace := nodeWorkspacePath(entry.node); workspace != "" {
			subtitle += "  workspace: " + workspace
		}
		if a.sessions.HasSession(entry.node.ID) {
			return "Terminal: " + entry.node.Slug, subtitle
		}
		return "Node: " + entry.node.Slug, subtitle
	default:
		return "Terminal", "Select a project or node in the tree."
	}
}

func (a *vaxisTUIApp) drawDetails(win vaxis.Window, entry tuiTreeEntry, headerStyle, mutedStyle vaxis.Style) {
	row := 0
	switch entry.kind {
	case tuiTreeEntryProject:
		win.Println(row, vaxis.Segment{Text: "Project controls", Style: headerStyle})
		row++
		win.Println(row, vaxis.Segment{Text: "Slug: " + entry.project.Slug})
		row++
		win.Println(row, vaxis.Segment{Text: "Workspace: " + entry.project.WorkspacePath})
		row += 2
		win.Println(row, vaxis.Segment{Text: "Create nodes, update the project binding, or delete the project from the tree view.", Style: mutedStyle})
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
			win.Println(row, vaxis.Segment{Text: "Workspace: " + workspace})
			row++
		}
		row++
		if nodeAutoStartsSession(entry.node) {
			win.Println(row, vaxis.Segment{Text: "Node is running. Press Tab or Enter to focus its shell session.", Style: mutedStyle})
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
	draw(win.New(col, row, width, height))
}

func renderActionHints(actions []tuiActionSpec) string {
	if len(actions) == 0 {
		return "Tree: no actions available"
	}

	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		parts = append(parts, "["+string(action.Hotkey)+"] "+action.Label)
	}
	return "Tree: " + strings.Join(parts, "  ")
}

func renderFooter(focus tuiFocus, entry tuiTreeEntry) string {
	if focus == tuiFocusTerminal {
		return "Terminal focused: all input passes through to the shell until Alt-` returns to the tree"
	}
	if entry.kind == "" {
		return "Press [a] to add a project   q quit"
	}
	if entry.kind == tuiTreeEntryNode {
		return "Up/Down move   Left/Right collapse   Tab/Enter shell   Alt-` focus tree   q quit"
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

func isTreeEscapeKey(key vaxis.Key) bool {
	return key.Modifiers&vaxis.ModAlt != 0 && (key.Text == "`" || key.Keycode == '`')
}

func isQuitKey(key vaxis.Key) bool {
	return key.Matches('c', vaxis.ModCtrl)
}
