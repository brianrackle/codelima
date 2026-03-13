package codelima

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
	service          *Service
	vx               *vaxis.Vaxis
	state            *tuiState
	sessions         *tuiSessionStore
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
		service:  service,
		vx:       vx,
		state:    state,
		sessions: sessions,
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

	var err error
	switch {
	case key.MatchString("q"), key.MatchString("Ctrl+c"):
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
		"Mouse click: select node",
		"Up/Down move, Left/Right collapse/expand",
		"Enter or Tab: focus terminal, Alt-`: tree",
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

	activeNode, hasActiveNode := a.state.activeNode()
	terminalTitle := "Terminal"
	terminalSubtitle := "Select a node to start a shell."
	if hasActiveNode {
		terminalTitle = "Terminal: " + activeNode.Slug
		terminalSubtitle = nodeWorkspacePath(activeNode)
		if terminalSubtitle == "" {
			terminalSubtitle = "workspace path unavailable"
		}
	}
	termInner.Println(0, vaxis.Segment{Text: terminalTitle, Style: headerStyle})
	if a.status != "" {
		termInner.Println(1, vaxis.Segment{Text: a.status, Style: errorStyle})
	} else {
		termInner.Println(1, vaxis.Segment{Text: terminalSubtitle, Style: mutedStyle})
	}

	termInnerWidth, termInnerHeight := termInner.Size()
	termBody := termInner.New(0, 2, termInnerWidth, termInnerHeight-2)
	termOriginCol, termOriginRow := termBody.Origin()
	a.terminalBodyRect = tuiRect{col: termOriginCol, row: termOriginRow, width: termInnerWidth, height: termInnerHeight - 2}

	if hasActiveNode {
		if session, ok := a.sessions.Session(activeNode.ID); ok {
			session.terminal.Draw(termBody)
		} else {
			termBody.Println(0, vaxis.Segment{Text: "Shell session is not running. Select the node again or press Enter to reopen.", Style: mutedStyle})
		}
	} else {
		termBody.Println(0, vaxis.Segment{Text: "Select a node in the tree to create a preserved shell session.", Style: mutedStyle})
	}

	footer := "Auto-switch on selection   Tab focus shell   Alt-` focus tree   q quit"
	if a.state.focus == tuiFocusTerminal {
		footer = "Terminal focused: all input passes through to the shell until Alt-` returns to the tree"
	}
	window.Println(height-1, vaxis.Segment{Text: footer, Style: mutedStyle})
	a.vx.Render()
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
