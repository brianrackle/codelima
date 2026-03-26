package codelima

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/containerd/console"
)

type fakeTUIRunner struct {
	calls int
}

type fakeVaxisConsole struct {
	input bytes.Buffer
	size  console.WinSize
}

func (f *fakeTUIRunner) Run(_ context.Context, _ *Service) error {
	f.calls++
	return nil
}

func newFakeVaxisConsole(input string, width, height uint16) *fakeVaxisConsole {
	console := &fakeVaxisConsole{
		size: console.WinSize{
			Width:  width,
			Height: height,
		},
	}
	console.input.WriteString(input)
	return console
}

func (f *fakeVaxisConsole) Read(p []byte) (int, error) {
	if f.input.Len() == 0 {
		return 0, io.EOF
	}
	return f.input.Read(p)
}

func (f *fakeVaxisConsole) Write(p []byte) (int, error) {
	return len(p), nil
}

func (f *fakeVaxisConsole) Close() error {
	return nil
}

func (f *fakeVaxisConsole) Fd() uintptr {
	return 0
}

func (f *fakeVaxisConsole) Name() string {
	return "fake-vaxis-console"
}

func (f *fakeVaxisConsole) Resize(size console.WinSize) error {
	f.size = size
	return nil
}

func (f *fakeVaxisConsole) ResizeFrom(other console.Console) error {
	size, err := other.Size()
	if err != nil {
		return err
	}
	f.size = size
	return nil
}

func (f *fakeVaxisConsole) SetRaw() error {
	return nil
}

func (f *fakeVaxisConsole) DisableEcho() error {
	return nil
}

func (f *fakeVaxisConsole) Reset() error {
	return nil
}

func (f *fakeVaxisConsole) Size() (console.WinSize, error) {
	return f.size, nil
}

func newRenderTestVaxis(t *testing.T, width, height int) *vaxis.Vaxis {
	t.Helper()

	console := newFakeVaxisConsole("\x1b[?1;2c", uint16(width), uint16(height))
	vx, err := vaxis.New(vaxis.Options{
		WithConsole:  console,
		DisableMouse: true,
		NoSignals:    true,
	})
	if err != nil {
		t.Fatalf("vaxis.New() error = %v", err)
	}
	return vx
}

func renderedCellGrapheme(t *testing.T, vx *vaxis.Vaxis, col, row int) string {
	t.Helper()

	buf := reflect.ValueOf(vx).Elem().FieldByName("screenNext").Elem().FieldByName("buf")
	cell := buf.Index(row).Index(col)
	return cell.FieldByName("Character").FieldByName("Grapheme").String()
}

func renderedScreenText(t *testing.T, vx *vaxis.Vaxis, width, height int) string {
	t.Helper()

	var lines []string
	for row := range height {
		var line strings.Builder
		for col := range width {
			line.WriteString(renderedCellGrapheme(t, vx, col, row))
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return strings.Join(lines, "\n")
}

type fakeTUISessionManager struct {
	ensured map[string]int
}

type sharedFakeTUISessionManager struct {
	store   *tuiSessionStore
	ensured map[string]int
}

type fakeTUITerminal struct {
	snapshot      string
	termEnv       string
	capturesMouse bool
	events        []vaxis.Event
}

func newFakeTUISessionManager() *fakeTUISessionManager {
	return &fakeTUISessionManager{ensured: map[string]int{}}
}

func newSharedFakeTUISessionManager(store *tuiSessionStore) *sharedFakeTUISessionManager {
	return &sharedFakeTUISessionManager{
		store:   store,
		ensured: map[string]int{},
	}
}

func newFakeTUITerminal() *fakeTUITerminal {
	return &fakeTUITerminal{termEnv: tuiEmbeddedTermEnv}
}

func (f *fakeTUITerminal) Start(*exec.Cmd) error {
	return nil
}

func (f *fakeTUITerminal) Update(event vaxis.Event) {
	f.events = append(f.events, event)
}

func (f *fakeTUITerminal) Draw(win vaxis.Window) {
	for row, line := range strings.Split(f.snapshot, "\n") {
		win.Println(row, vaxis.Segment{Text: line})
	}
}

func (f *fakeTUITerminal) Close() {}

func (f *fakeTUITerminal) Focus() {}

func (f *fakeTUITerminal) Blur() {}

func (f *fakeTUITerminal) String() string {
	return f.snapshot
}

func (f *fakeTUITerminal) TermEnv() string {
	return f.termEnv
}

func (f *fakeTUITerminal) HyperlinkAt(int, int) (string, bool) {
	return "", false
}

func (f *fakeTUITerminal) CapturesMouse() bool {
	return f.capturesMouse
}

func (f *fakeTUISessionManager) HasSession(nodeID string) bool {
	return f.ensured[nodeID] > 0
}

func (f *fakeTUISessionManager) EnsureSession(node Node) error {
	f.ensured[node.ID]++
	return nil
}

func (f *sharedFakeTUISessionManager) HasSession(nodeID string) bool {
	return f.store.HasSession(nodeID)
}

func (f *sharedFakeTUISessionManager) EnsureSession(node Node) error {
	f.ensured[node.ID]++
	f.store.sessions[node.ID] = &tuiSession{
		node:     node,
		terminal: newFakeTUITerminal(),
	}
	return nil
}

func TestDispatchWithoutCommandRunsInjectedRunner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	runner := &fakeTUIRunner{}
	service.tui = runner

	if _, err := dispatch(ctx, service, nil); err != nil {
		t.Fatalf("dispatch() error = %v", err)
	}

	if runner.calls != 1 {
		t.Fatalf("expected runner to be called once, got %d", runner.calls)
	}
}

func TestDispatchExplicitTUICommandIsRejected(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)

	if _, err := dispatch(ctx, service, []string{"tui"}); err == nil {
		t.Fatalf("expected dispatch(tui) to fail")
	} else {
		var appErr *AppError
		if !As(err, &appErr) {
			t.Fatalf("expected AppError, got %T", err)
		}
		if appErr.Category != "InvalidArgument" {
			t.Fatalf("expected InvalidArgument, got %q", appErr.Category)
		}
	}
}

func TestTUIMutedStyleUsesBrighterSecondaryColor(t *testing.T) {
	t.Parallel()

	style := tuiMutedStyle()
	if style.Foreground != vaxis.ColorSilver {
		t.Fatalf("expected muted style foreground to be ColorSilver, got %v", style.Foreground)
	}
}

func TestNewTUITerminalUsesCompatibleTERM(t *testing.T) {
	t.Parallel()

	terminal := newTUITerminal("node-root", func(vaxis.Event) {})
	if terminal.TermEnv() != tuiEmbeddedTermEnv {
		t.Fatalf("expected embedded terminal TERM %q, got %q", tuiEmbeddedTermEnv, terminal.TermEnv())
	}
}

func TestNewGhosttyTUITerminalLoadsWhenLibraryInstalled(t *testing.T) {
	t.Parallel()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	if terminal.TermEnv() != tuiEmbeddedTermEnv {
		t.Fatalf("expected ghostty terminal TERM %q, got %q", tuiEmbeddedTermEnv, terminal.TermEnv())
	}
}

func TestTUIDrawOverlayClearsCoveredCells(t *testing.T) {
	t.Parallel()

	vx := newRenderTestVaxis(t, 20, 10)
	defer vx.Close()

	window := vx.Window()
	window.Fill(vaxis.Cell{Character: vaxis.Character{Grapheme: "x", Width: 1}})

	app := &vaxisTUIApp{}
	app.drawOverlay(window, 10, 5, func(overlay vaxis.Window) {
		overlay.Println(0, vaxis.Segment{Text: "A"})
	})

	if got := renderedCellGrapheme(t, vx, 6, 3); got != " " {
		t.Fatalf("expected overlay interior to be cleared to space, got %q", got)
	}
	if got := renderedCellGrapheme(t, vx, 5, 2); got != "A" {
		t.Fatalf("expected overlay draw callback to render text, got %q", got)
	}
	if got := renderedCellGrapheme(t, vx, 0, 0); got != "x" {
		t.Fatalf("expected cells outside overlay to remain unchanged, got %q", got)
	}
}

func TestTUIDrawOmitsRedundantTerminalChrome(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUITree(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	vx := newRenderTestVaxis(t, 100, 24)
	defer vx.Close()

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: newTUISessionStore(ctx, service, func(vaxis.Event) {}),
		vx:       vx,
	}
	app.sessions.sessions["node-root"] = &tuiSession{
		node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
		terminal: &fakeTUITerminal{
			snapshot: "shell prompt",
			termEnv:  tuiEmbeddedTermEnv,
		},
	}

	app.draw()

	rendered := renderedScreenText(t, vx, 100, 24)
	if !strings.Contains(rendered, "Project: root  Node: root-node  Mode: copy") {
		t.Fatalf("expected rendered TUI header to include the node workspace mode, got:\n%s", rendered)
	}
	for _, unexpected := range []string{
		"CodeLima TUI",
		"Layout:",
		"Shell-first layout",
		"Mouse: enabled",
		"Auto-switch on node selection",
		"Mouse click: select project or node",
		"Up/Down move, Left/Right collapse/expand",
		"Action hotkeys are shown in the right pane",
		"Terminal:",
		"Terminal focused:",
		"Tree focused:",
		"status: ",
		"workspace: ",
	} {
		if strings.Contains(rendered, unexpected) {
			t.Fatalf("expected rendered TUI not to contain %q, got:\n%s", unexpected, rendered)
		}
	}
}

func TestTUIDrawProjectHeaderOmitsNodeAndModeWhenProjectSelected(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUITree(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	selectTUIEntry(t, &vaxisTUIApp{state: state}, "project:project-root")

	vx := newRenderTestVaxis(t, 100, 24)
	defer vx.Close()

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: newTUISessionStore(ctx, service, func(vaxis.Event) {}),
		vx:       vx,
	}
	app.sessions.sessions["node-root"] = &tuiSession{
		node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
		terminal: &fakeTUITerminal{
			snapshot: "shell prompt",
			termEnv:  tuiEmbeddedTermEnv,
		},
	}

	app.draw()

	rendered := renderedScreenText(t, vx, 100, 24)
	if !strings.Contains(rendered, "Project: root") {
		t.Fatalf("expected rendered TUI header to include the project, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "Node:") || strings.Contains(rendered, "Mode:") {
		t.Fatalf("expected rendered TUI header to omit node and mode when a project is selected, got:\n%s", rendered)
	}
}

func TestTUIDrawTerminalUsesFullWidthWithoutSideBorders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUITree(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	vx := newRenderTestVaxis(t, 100, 24)
	defer vx.Close()

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: newTUISessionStore(ctx, service, func(vaxis.Event) {}),
		vx:       vx,
	}
	app.sessions.sessions["node-root"] = &tuiSession{
		node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
		terminal: &fakeTUITerminal{
			snapshot: "shell prompt",
			termEnv:  tuiEmbeddedTermEnv,
		},
	}

	app.draw()

	layout := layoutTUIBody(100, tuiFocusTree)
	if got := renderedCellGrapheme(t, vx, layout.termCol, 2); got != "s" {
		t.Fatalf("expected terminal content to start at the left edge of the pane, got %q", got)
	}
	if got := renderedCellGrapheme(t, vx, layout.termCol, 1); got != "─" {
		t.Fatalf("expected top terminal border at the pane edge, got %q", got)
	}
}

func TestRenderFooterUsesAvailableActionsForFocus(t *testing.T) {
	t.Parallel()

	got := renderFooter(tuiFocusTree, tuiTreeEntry{})
	if got != "[a] add project   [g] env configs   q quit" {
		t.Fatalf("expected empty-tree footer with global actions, got %q", got)
	}

	projectEntry := tuiTreeEntry{
		kind:    tuiTreeEntryProject,
		project: Project{Slug: "root"},
	}
	got = renderFooter(tuiFocusTree, projectEntry)
	if got != "Up/Down move   Left/Right collapse   [a] add project   [g] env configs   [n] create node   [u] update project   [x] delete project   q quit" {
		t.Fatalf("expected project footer with project actions, got %q", got)
	}
	if strings.Contains(got, "Use action hotkeys in the right pane") {
		t.Fatalf("expected project footer to omit generic action text, got %q", got)
	}

	runningNodeEntry := tuiTreeEntry{
		kind: tuiTreeEntryNode,
		node: Node{Slug: "root-node", Status: NodeStatusRunning},
	}
	got = renderFooter(tuiFocusTree, runningNodeEntry)
	if got != "Up/Down move   Left/Right collapse   Alt-` shell focus   [a] add project   [g] env configs   [s] stop node   [d] delete node   [c] clone node   q quit" {
		t.Fatalf("expected running-node footer with shell focus and node actions, got %q", got)
	}
	if strings.Contains(got, "drag copy") || strings.Contains(got, "wheel scroll") {
		t.Fatalf("expected running-node footer to omit mouse hints, got %q", got)
	}

	stoppedNodeEntry := tuiTreeEntry{
		kind: tuiTreeEntryNode,
		node: Node{Slug: "stopped-node", Status: NodeStatusStopped},
	}
	got = renderFooter(tuiFocusTree, stoppedNodeEntry)
	if got != "Up/Down move   Left/Right collapse   [a] add project   [g] env configs   [s] start node   [d] delete node   [c] clone node   q quit" {
		t.Fatalf("expected stopped-node footer without shell focus, got %q", got)
	}

	got = renderFooter(tuiFocusTerminal, runningNodeEntry)
	if got != "Alt-` tree focus   q quit" {
		t.Fatalf("expected terminal footer without mouse hints, got %q", got)
	}
	if strings.Contains(got, "drag copy") || strings.Contains(got, "wheel scroll") {
		t.Fatalf("expected terminal footer to omit mouse hints, got %q", got)
	}
}

func TestLayoutTUIBodySplitsTreeAndTerminalWhenCollapsed(t *testing.T) {
	t.Parallel()

	layout := layoutTUIBody(120, tuiFocusTree)
	if !layout.treeVisible {
		t.Fatalf("expected tree to stay visible when terminal is not expanded")
	}
	if layout.treeWidth != 40 {
		t.Fatalf("expected clamped tree width 40, got %d", layout.treeWidth)
	}
	if layout.termCol != 41 {
		t.Fatalf("expected terminal column 41, got %d", layout.termCol)
	}
	if layout.termWidth != 79 {
		t.Fatalf("expected terminal width 79, got %d", layout.termWidth)
	}
}

func TestLayoutTUIBodyHidesTreeWhenTerminalFocused(t *testing.T) {
	t.Parallel()

	layout := layoutTUIBody(120, tuiFocusTerminal)
	if layout.treeVisible {
		t.Fatalf("expected tree to be hidden when terminal is focused")
	}
	if layout.treeWidth != 0 {
		t.Fatalf("expected hidden tree width 0, got %d", layout.treeWidth)
	}
	if layout.termCol != 0 {
		t.Fatalf("expected terminal to start at column 0, got %d", layout.termCol)
	}
	if layout.termWidth != 120 {
		t.Fatalf("expected terminal to expand to full width, got %d", layout.termWidth)
	}
}

func TestTerminalViewToggleKeyMatchesAltBacktickOnly(t *testing.T) {
	t.Parallel()

	if !isTerminalViewToggleKey(vaxis.Key{Text: "`", Keycode: '`', Modifiers: vaxis.ModAlt}) {
		t.Fatalf("expected Alt+` to match the terminal view toggle key")
	}
	if isTerminalViewToggleKey(vaxis.Key{Text: "`", Keycode: '`', Modifiers: vaxis.ModSuper}) {
		t.Fatalf("expected Super+` not to match the terminal view toggle key")
	}
	if isTerminalViewToggleKey(vaxis.Key{Text: "`", Keycode: '`'}) {
		t.Fatalf("expected bare ` not to match the terminal view toggle key")
	}
}

func TestTUIStateAutoSwitchesAndReusesPerNodeSessions(t *testing.T) {
	t.Parallel()

	tree := testTUITree(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(tree, sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	if got := state.selectedEntry().node.Slug; got != "root-node" {
		t.Fatalf("expected initial selection to be root-node, got %q", got)
	}

	if state.activeNodeID != "node-root" {
		t.Fatalf("expected initial active node to be node-root, got %q", state.activeNodeID)
	}

	if sessions.ensured["node-root"] != 1 {
		t.Fatalf("expected root session to be created once, got %d", sessions.ensured["node-root"])
	}

	if err := state.moveSelection(1); err != nil {
		t.Fatalf("moveSelection(child project) error = %v", err)
	}

	if got := state.selectedEntry().project.Slug; got != "child" {
		t.Fatalf("expected project selection to move to child, got %q", got)
	}

	if state.activeNodeID != "node-root" {
		t.Fatalf("expected project selection to keep root terminal active, got %q", state.activeNodeID)
	}

	if sessions.ensured["node-child"] != 0 {
		t.Fatalf("expected child session to remain unopened, got %d", sessions.ensured["node-child"])
	}

	if err := state.moveSelection(1); err != nil {
		t.Fatalf("moveSelection(child node) error = %v", err)
	}

	if got := state.selectedEntry().node.Slug; got != "child-node" {
		t.Fatalf("expected node selection to move to child-node, got %q", got)
	}

	if state.activeNodeID != "node-child" {
		t.Fatalf("expected active node to switch to node-child, got %q", state.activeNodeID)
	}

	if sessions.ensured["node-child"] != 1 {
		t.Fatalf("expected child session to be created once, got %d", sessions.ensured["node-child"])
	}

	if err := state.moveSelection(-2); err != nil {
		t.Fatalf("moveSelection(root node) error = %v", err)
	}

	if got := state.selectedEntry().node.Slug; got != "root-node" {
		t.Fatalf("expected selection to return to root-node, got %q", got)
	}

	if sessions.ensured["node-root"] != 1 {
		t.Fatalf("expected root session to be reused, got %d creations", sessions.ensured["node-root"])
	}

	if err := state.focusTerminal(); err != nil {
		t.Fatalf("focusTerminal() error = %v", err)
	}

	if state.focus != tuiFocusTerminal {
		t.Fatalf("expected terminal focus, got %q", state.focus)
	}

	state.focusTree()
	if state.focus != tuiFocusTree {
		t.Fatalf("expected tree focus, got %q", state.focus)
	}
}

func TestTUIStateCollapseAndExpandProjectBranches(t *testing.T) {
	t.Parallel()

	tree := testTUITree(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(tree, sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	if err := state.moveSelection(-1); err != nil {
		t.Fatalf("moveSelection(root project) error = %v", err)
	}

	if got := state.selectedEntry().project.Slug; got != "root" {
		t.Fatalf("expected selection to move to root project, got %q", got)
	}

	state.collapseSelection()
	if len(state.entries) != 1 {
		t.Fatalf("expected collapsed tree to show only the root project, got %d entries", len(state.entries))
	}

	if got := state.selectedEntry().project.Slug; got != "root" {
		t.Fatalf("expected root project to remain selected after collapse, got %q", got)
	}

	state.expandSelection()
	if len(state.entries) != 4 {
		t.Fatalf("expected expanded tree to restore all entries, got %d", len(state.entries))
	}

	if got := state.entries[3].node.Slug; got != "child-node" {
		t.Fatalf("expected expanded tree to restore child-node, got %q", got)
	}
}

func TestTUIStateSelectsCreatedNodeWithoutOpeningShellSession(t *testing.T) {
	t.Parallel()

	tree := testTUITreeWithCreatedNode(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(tree, sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	if err := state.moveSelection(2); err != nil {
		t.Fatalf("moveSelection(created node) error = %v", err)
	}

	if got := state.selectedEntry().node.Slug; got != "created-node" {
		t.Fatalf("expected selection to move to created-node, got %q", got)
	}

	if state.activeNodeID != "node-created" {
		t.Fatalf("expected created node to become the selected active node, got %q", state.activeNodeID)
	}

	if sessions.ensured["node-created"] != 0 {
		t.Fatalf("expected created node not to open a shell session, got %d creations", sessions.ensured["node-created"])
	}

	if err := state.focusTerminal(); err == nil {
		t.Fatalf("expected focusTerminal() to fail for a node without a running shell session")
	}
}

func TestTUIHandleKeyAltBacktickTogglesFocusToTerminalAndHidesTree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUITree(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: newTUISessionStore(ctx, service, func(vaxis.Event) {}),
	}
	app.sessions.sessions["node-root"] = &tuiSession{
		node:     Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
		terminal: newFakeTUITerminal(),
	}

	quit, err := app.handleKey(vaxis.Key{Text: "`", Keycode: '`', Modifiers: vaxis.ModAlt})
	if err != nil {
		t.Fatalf("handleKey(Alt+`) error = %v", err)
	}
	if quit {
		t.Fatalf("expected Alt+` to toggle terminal view, not quit")
	}
	if app.state.focus != tuiFocusTerminal {
		t.Fatalf("expected Alt+` to focus the terminal, got %q", app.state.focus)
	}
	if layout := layoutTUIBody(120, app.state.focus); layout.treeVisible {
		t.Fatalf("expected terminal focus to hide the tree")
	}
}

func TestTUIHandleKeyAltBacktickTogglesFocusBackToTreeAndShowsTree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUITree(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	if err := state.focusTerminal(); err != nil {
		t.Fatalf("focusTerminal() error = %v", err)
	}

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: newTUISessionStore(ctx, service, func(vaxis.Event) {}),
	}
	app.sessions.sessions["node-root"] = &tuiSession{
		node:     Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
		terminal: newFakeTUITerminal(),
	}

	quit, err := app.handleKey(vaxis.Key{Text: "`", Keycode: '`', Modifiers: vaxis.ModAlt})
	if err != nil {
		t.Fatalf("handleKey(Alt+`) error = %v", err)
	}
	if quit {
		t.Fatalf("expected Alt+` to toggle terminal view, not quit")
	}
	if app.state.focus != tuiFocusTree {
		t.Fatalf("expected Alt+` to return focus to the tree, got %q", app.state.focus)
	}
	if layout := layoutTUIBody(120, app.state.focus); !layout.treeVisible {
		t.Fatalf("expected tree focus to show the tree")
	}
}

func TestTUIHandleKeyEnterNoLongerFocusesTerminal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUITree(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: newTUISessionStore(ctx, service, func(vaxis.Event) {}),
	}
	app.sessions.sessions["node-root"] = &tuiSession{
		node:     Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
		terminal: newFakeTUITerminal(),
	}

	quit, err := app.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
	if err != nil {
		t.Fatalf("handleKey(Enter) error = %v", err)
	}
	if quit {
		t.Fatalf("expected Enter not to quit")
	}
	if app.state.focus != tuiFocusTree {
		t.Fatalf("expected Enter to leave focus on the tree, got %q", app.state.focus)
	}
}

func TestTUIHandleKeyAltEnterNoLongerTogglesTerminalFocus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUITree(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: newTUISessionStore(ctx, service, func(vaxis.Event) {}),
	}
	app.sessions.sessions["node-root"] = &tuiSession{
		node:     Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
		terminal: newFakeTUITerminal(),
	}

	quit, err := app.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter, Modifiers: vaxis.ModAlt})
	if err != nil {
		t.Fatalf("handleKey(Alt+Enter) error = %v", err)
	}
	if quit {
		t.Fatalf("expected Alt+Enter to be ignored, not quit")
	}
	if app.state.focus != tuiFocusTree {
		t.Fatalf("expected Alt+Enter to leave focus on the tree, got %q", app.state.focus)
	}
}

func TestTUIHandleKeyTabNoLongerFocusesTerminal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUITree(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: newTUISessionStore(ctx, service, func(vaxis.Event) {}),
	}

	quit, err := app.handleKey(vaxis.Key{Keycode: vaxis.KeyTab})
	if err != nil {
		t.Fatalf("handleKey(Tab) error = %v", err)
	}
	if quit {
		t.Fatalf("expected Tab to be ignored, not quit")
	}
	if app.state.focus != tuiFocusTree {
		t.Fatalf("expected Tab to leave focus on the tree, got %q", app.state.focus)
	}
}

func TestTUIMouseMotionDoesNotFocusTerminal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUITree(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	app := &vaxisTUIApp{
		ctx:              ctx,
		service:          service,
		state:            state,
		sessions:         newTUISessionStore(ctx, service, func(vaxis.Event) {}),
		terminalBodyRect: tuiRect{col: 10, row: 5, width: 40, height: 10},
	}
	app.sessions.sessions["node-root"] = &tuiSession{
		node:     Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
		terminal: newFakeTUITerminal(),
	}

	if err := app.handleMouse(vaxis.Mouse{
		Col:       12,
		Row:       7,
		Button:    vaxis.MouseNoButton,
		EventType: vaxis.EventMotion,
	}); err != nil {
		t.Fatalf("handleMouse(motion) error = %v", err)
	}
	if app.state.focus != tuiFocusTree {
		t.Fatalf("expected mouse motion to keep tree focus, got %q", app.state.focus)
	}

	if err := app.handleMouse(vaxis.Mouse{
		Col:       12,
		Row:       7,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventPress,
	}); err != nil {
		t.Fatalf("handleMouse(press) error = %v", err)
	}
	if app.state.focus != tuiFocusTree {
		t.Fatalf("expected mouse press to keep tree focus until release, got %q", app.state.focus)
	}

	if err := app.handleMouse(vaxis.Mouse{
		Col:       12,
		Row:       7,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventRelease,
	}); err != nil {
		t.Fatalf("handleMouse(release) error = %v", err)
	}
	if app.state.focus != tuiFocusTerminal {
		t.Fatalf("expected mouse release to focus terminal, got %q", app.state.focus)
	}
}

func TestTUIMouseReleaseOpensTerminalHyperlinkWithoutDrag(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUITree(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	opened := ""
	app := &vaxisTUIApp{
		ctx:               ctx,
		service:           service,
		openLink:          func(target string) error { opened = target; return nil },
		screenHyperlinkAt: func(col, row int) (string, bool) { return "https://auth.openai.com/example", col == 12 && row == 7 },
		state:             state,
		sessions:          newTUISessionStore(ctx, service, func(vaxis.Event) {}),
		terminalBodyRect:  tuiRect{col: 10, row: 5, width: 40, height: 10},
	}
	app.sessions.sessions["node-root"] = &tuiSession{
		node:     Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
		terminal: newFakeTUITerminal(),
	}

	if err := app.handleMouse(vaxis.Mouse{
		Col:       12,
		Row:       7,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventPress,
	}); err != nil {
		t.Fatalf("handleMouse(link press) error = %v", err)
	}
	if opened != "" {
		t.Fatalf("expected press to defer opening terminal hyperlink, got %q", opened)
	}

	if err := app.handleMouse(vaxis.Mouse{
		Col:       12,
		Row:       7,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventRelease,
	}); err != nil {
		t.Fatalf("handleMouse(link release) error = %v", err)
	}

	if opened != "https://auth.openai.com/example" {
		t.Fatalf("expected terminal hyperlink to open, got %q", opened)
	}
	if app.status != "opened https://auth.openai.com/example" {
		t.Fatalf("expected open-link status, got %q", app.status)
	}
	if app.state.focus != tuiFocusTree {
		t.Fatalf("expected opening a terminal hyperlink to keep tree focus, got %q", app.state.focus)
	}
}

func TestTUIMouseDragCopiesTerminalSelectionWithoutShift(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUITree(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	terminal := newFakeTUITerminal()
	terminal.snapshot = "pwd\n/workspace/demo\n"

	var copied string
	app := &vaxisTUIApp{
		ctx:              ctx,
		service:          service,
		copySelection:    func(text string) error { copied = text; return nil },
		state:            state,
		sessions:         newTUISessionStore(ctx, service, func(vaxis.Event) {}),
		terminalBodyRect: tuiRect{col: 10, row: 5, width: 40, height: 10},
	}
	app.sessions.sessions["node-root"] = &tuiSession{
		node:     Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
		terminal: terminal,
	}

	if err := app.handleMouse(vaxis.Mouse{
		Col:       11,
		Row:       6,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventPress,
	}); err != nil {
		t.Fatalf("handleMouse(selection press) error = %v", err)
	}
	if err := app.handleMouse(vaxis.Mouse{
		Col:       14,
		Row:       6,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventMotion,
	}); err != nil {
		t.Fatalf("handleMouse(selection motion) error = %v", err)
	}
	if err := app.handleMouse(vaxis.Mouse{
		Col:       14,
		Row:       6,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventRelease,
	}); err != nil {
		t.Fatalf("handleMouse(selection release) error = %v", err)
	}

	if copied != "work" {
		t.Fatalf("expected copied selection, got %q", copied)
	}
	if app.status != "copied 4 bytes from root-node" {
		t.Fatalf("expected copy status, got %q", app.status)
	}
	if len(terminal.events) != 0 {
		t.Fatalf("expected local selection to avoid forwarding mouse events, got %d events", len(terminal.events))
	}
}

func TestTUIMouseDragForwardsToGuestWhenTerminalCapturesMouse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUITree(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	terminal := newFakeTUITerminal()
	terminal.capturesMouse = true
	terminal.snapshot = "pwd\n/workspace/demo\n"

	var copied string
	app := &vaxisTUIApp{
		ctx:              ctx,
		service:          service,
		copySelection:    func(text string) error { copied = text; return nil },
		state:            state,
		sessions:         newTUISessionStore(ctx, service, func(vaxis.Event) {}),
		terminalBodyRect: tuiRect{col: 10, row: 5, width: 40, height: 10},
	}
	app.sessions.sessions["node-root"] = &tuiSession{
		node:     Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
		terminal: terminal,
	}

	for _, eventType := range []vaxis.EventType{vaxis.EventPress, vaxis.EventMotion, vaxis.EventRelease} {
		if err := app.handleMouse(vaxis.Mouse{
			Col:       11,
			Row:       6,
			Button:    vaxis.MouseLeftButton,
			EventType: eventType,
		}); err != nil {
			t.Fatalf("handleMouse(%v) error = %v", eventType, err)
		}
	}

	if copied != "" {
		t.Fatalf("expected mouse-capturing terminal to skip local copy, got %q", copied)
	}
	if len(terminal.events) != 3 {
		t.Fatalf("expected guest mouse forwarding, got %d events", len(terminal.events))
	}
	if app.state.focus != tuiFocusTerminal {
		t.Fatalf("expected mouse press to focus terminal, got %q", app.state.focus)
	}
}

func TestTUIShiftDragCopiesTerminalSelectionWhenTerminalCapturesMouse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUITree(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	terminal := newFakeTUITerminal()
	terminal.capturesMouse = true
	terminal.snapshot = "pwd\n/workspace/demo\n"

	var copied string
	app := &vaxisTUIApp{
		ctx:              ctx,
		service:          service,
		copySelection:    func(text string) error { copied = text; return nil },
		state:            state,
		sessions:         newTUISessionStore(ctx, service, func(vaxis.Event) {}),
		terminalBodyRect: tuiRect{col: 10, row: 5, width: 40, height: 10},
	}
	app.sessions.sessions["node-root"] = &tuiSession{
		node:     Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
		terminal: terminal,
	}

	events := []vaxis.Mouse{
		{Col: 11, Row: 6, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress, Modifiers: vaxis.ModShift},
		{Col: 13, Row: 6, Button: vaxis.MouseLeftButton, EventType: vaxis.EventMotion, Modifiers: vaxis.ModShift},
		{Col: 13, Row: 6, Button: vaxis.MouseLeftButton, EventType: vaxis.EventRelease, Modifiers: vaxis.ModShift},
	}
	for _, event := range events {
		if err := app.handleMouse(event); err != nil {
			t.Fatalf("handleMouse(shift %+v) error = %v", event, err)
		}
	}

	if copied != "wor" {
		t.Fatalf("expected shift-drag to force local copy, got %q", copied)
	}
	if len(terminal.events) != 0 {
		t.Fatalf("expected shift-drag selection to avoid guest mouse forwarding, got %d events", len(terminal.events))
	}
}

func TestTUIShiftDragCopiesTerminalSelection(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	state, err := newTUIState(testTUITree(t), newFakeTUISessionManager())
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	var copied string
	app := &vaxisTUIApp{
		ctx:           ctx,
		service:       service,
		copySelection: func(text string) error { copied = text; return nil },
		state:         state,
		sessions:      newTUISessionStore(ctx, service, func(vaxis.Event) {}),
	}

	app.handleTerminalSelection("node-root", "root-node", "pwd\n/workspace/demo\n", vaxis.Mouse{
		Col:       1,
		Row:       1,
		EventType: vaxis.EventPress,
	})
	app.handleTerminalSelection("node-root", "root-node", "pwd\n/workspace/demo\n", vaxis.Mouse{
		Col:       4,
		Row:       1,
		EventType: vaxis.EventRelease,
	})

	if copied != "work" {
		t.Fatalf("expected copied selection, got %q", copied)
	}
	if app.status != "copied 4 bytes from root-node" {
		t.Fatalf("expected copy status, got %q", app.status)
	}
	if app.selection != nil {
		t.Fatalf("expected selection to clear after release")
	}
}

func TestTUIDialogCtrlLeftBracketCancels(t *testing.T) {
	t.Parallel()

	dialog := newTUIDialog(
		"Create Project",
		"Create",
		nil,
		[]tuiDialogField{newTUIInputField("slug", "Project Slug", "", false)},
		nil,
	)

	completed, cancelled, err := dialog.Update(vaxis.Key{
		Keycode:   '[',
		Modifiers: vaxis.ModCtrl,
	})
	if err != nil {
		t.Fatalf("dialog.Update(Ctrl+[) error = %v", err)
	}
	if completed {
		t.Fatalf("expected Ctrl+[ to cancel, not complete")
	}
	if !cancelled {
		t.Fatalf("expected Ctrl+[ to cancel the dialog")
	}
}

func TestTUIHandleEventEscapeClosesDialog(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())

	if err := app.performAction(tuiActionSpec{ID: tuiActionProjectCreate}); err != nil {
		t.Fatalf("performAction(create project) error = %v", err)
	}
	if app.dialog == nil {
		t.Fatalf("expected create project dialog to be open")
	}

	quit, err := app.handleEvent(vaxis.Key{Keycode: vaxis.KeyEsc})
	if err != nil {
		t.Fatalf("handleEvent(Esc) error = %v", err)
	}
	if quit {
		t.Fatalf("expected Esc to close the dialog, not quit the app")
	}
	if app.dialog != nil {
		t.Fatalf("expected Esc to close the dialog")
	}
}

func TestTUIHandleEventCtrlCQuitsWithDialogOpen(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())

	if err := app.performAction(tuiActionSpec{ID: tuiActionProjectCreate}); err != nil {
		t.Fatalf("performAction(create project) error = %v", err)
	}
	if app.dialog == nil {
		t.Fatalf("expected create project dialog to be open")
	}

	quit, err := app.handleEvent(vaxis.Key{
		Keycode:   'c',
		Modifiers: vaxis.ModCtrl,
	})
	if err != nil {
		t.Fatalf("handleEvent(Ctrl+c) error = %v", err)
	}
	if !quit {
		t.Fatalf("expected Ctrl+c to quit while a dialog is open")
	}
}

func TestTUITerminalClosedEventIsHandledWhileOperationActive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)

	state, err := newTUIState(testTUITree(t), tuiNoopSessionManager{})
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state.sessions = sessions
	state.focus = tuiFocusTerminal

	node := state.selectedEntry().node
	sessions.sessions[node.ID] = &tuiSession{
		node:     node,
		terminal: newFakeTUITerminal(),
	}

	app := &vaxisTUIApp{
		ctx:       ctx,
		service:   service,
		state:     state,
		sessions:  sessions,
		operation: &tuiOperationState{Title: "Cloning " + node.Slug},
	}

	quit, err := app.handleEvent(tuiTerminalClosedEvent{NodeID: node.ID})
	if err != nil {
		t.Fatalf("handleEvent(tuiTerminalClosedEvent) error = %v", err)
	}
	if quit {
		t.Fatalf("expected terminal close during operation to stay in app")
	}
	if sessions.HasSession(node.ID) {
		t.Fatalf("expected closed terminal session to be removed during operation")
	}
	if state.activeNodeID != "" {
		t.Fatalf("expected closed active node id to be cleared, got %q", state.activeNodeID)
	}
	if state.focus != tuiFocusTree {
		t.Fatalf("expected focus to return to tree after terminal close, got %q", state.focus)
	}
	if app.status != "shell exited for "+node.Slug {
		t.Fatalf("expected shell exit status, got %q", app.status)
	}
}

func TestTUISourceNodeSessionIsRecreatedAfterCloneClosesIt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)

	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	sessionManager := newSharedFakeTUISessionManager(sessions)

	state, err := newTUIState(testTUITree(t), sessionManager)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	state.focus = tuiFocusTerminal

	rootNode := state.selectedEntry().node
	if !sessions.HasSession(rootNode.ID) {
		t.Fatalf("expected initial source-node session to exist")
	}

	app := &vaxisTUIApp{
		ctx:       ctx,
		service:   service,
		state:     state,
		sessions:  sessions,
		operation: &tuiOperationState{Title: "Cloning " + rootNode.Slug},
	}

	quit, err := app.handleEvent(tuiTerminalClosedEvent{NodeID: rootNode.ID})
	if err != nil {
		t.Fatalf("handleEvent(tuiTerminalClosedEvent) error = %v", err)
	}
	if quit {
		t.Fatalf("expected terminal close during clone to stay in app")
	}
	if sessions.HasSession(rootNode.ID) {
		t.Fatalf("expected source-node session to be removed after shell exit")
	}

	if err := state.moveSelection(1); err != nil {
		t.Fatalf("moveSelection(to child) error = %v", err)
	}
	if err := state.moveSelection(-1); err != nil {
		t.Fatalf("moveSelection(back to source) error = %v", err)
	}

	if !sessions.HasSession(rootNode.ID) {
		t.Fatalf("expected source-node session to be recreated after reselection")
	}
	if sessionManager.ensured[rootNode.ID] < 2 {
		t.Fatalf("expected source-node session to be ensured again after reselection, got %d", sessionManager.ensured[rootNode.ID])
	}
}

func TestTUIAvailableActionsDependOnSelectedEntry(t *testing.T) {
	t.Parallel()

	emptyActions := availableTUIActions(tuiTreeEntry{})
	if got := actionIDs(emptyActions); got != "project.create,environment_config.manage" {
		t.Fatalf("unexpected empty action set: %s", got)
	}

	projectActions := availableTUIActions(tuiTreeEntry{
		kind:    tuiTreeEntryProject,
		project: Project{Slug: "root"},
	})

	if got := actionIDs(projectActions); got != "project.create,environment_config.manage,project.create_node,project.update,project.delete" {
		t.Fatalf("unexpected project action set: %s", got)
	}

	runningNodeActions := availableTUIActions(tuiTreeEntry{
		kind: tuiTreeEntryNode,
		node: Node{Slug: "root-node", Status: NodeStatusRunning},
	})

	if got := actionIDs(runningNodeActions); got != "project.create,environment_config.manage,node.stop,node.delete,node.clone" {
		t.Fatalf("unexpected running node action set: %s", got)
	}

	createdNodeActions := availableTUIActions(tuiTreeEntry{
		kind: tuiTreeEntryNode,
		node: Node{Slug: "created-node", Status: NodeStatusCreated},
	})

	if got := actionIDs(createdNodeActions); got != "project.create,environment_config.manage,node.start,node.delete,node.clone" {
		t.Fatalf("unexpected created node action set: %s", got)
	}
}

func TestTUIProjectActionsCreateUpdateAndDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	rootProject, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate(root) error = %v", err)
	}

	deleteWorkspace := filepath.Join(t.TempDir(), "delete-me")
	writeFile(t, filepath.Join(deleteWorkspace, "README.md"), "delete me\n")

	sessions := newFakeTUISessionManager()
	app := newTestTUIApp(t, ctx, service, sessions)
	selectTUIEntry(t, app, "project:"+rootProject.ID)

	if err := app.performAction(tuiActionSpec{ID: tuiActionProjectCreate}); err != nil {
		t.Fatalf("performAction(create project) error = %v", err)
	}
	if app.dialog == nil || app.dialog.Title != "Create Project" {
		t.Fatalf("expected create project dialog, got %#v", app.dialog)
	}
	submitTUIDialog(t, app, map[string]string{
		"slug":           "delete-me",
		"workspace_path": deleteWorkspace,
	})

	deleteProject, err := service.ProjectShow("delete-me")
	if err != nil {
		t.Fatalf("ProjectShow(delete-me) error = %v", err)
	}
	if got := app.state.selectedEntry(); got.kind != tuiTreeEntryProject || got.project.ID != deleteProject.ID {
		t.Fatalf("expected created project to become selected, got %#v", got)
	}

	selectTUIEntry(t, app, "project:"+rootProject.ID)
	if err := app.performAction(tuiActionSpec{ID: tuiActionProjectCreateNode}); err != nil {
		t.Fatalf("performAction(create node) error = %v", err)
	}
	if app.dialog == nil || app.dialog.Title != "Create Node" {
		t.Fatalf("expected create node dialog, got %#v", app.dialog)
	}
	if len(app.dialog.Description) != 1 || app.dialog.Description[0] != "Selected project: "+rootProject.Slug {
		t.Fatalf("expected create node dialog to keep only the selected project context, got %#v", app.dialog.Description)
	}
	if len(app.dialog.Fields) != 3 {
		t.Fatalf("expected create node dialog to prompt for slug, workspace mode, and an optional lima commands file, got %#v", app.dialog.Fields)
	}
	if err := app.dialog.Fields[1].Activate(); err != nil {
		t.Fatalf("workspace mode selector activate error = %v", err)
	}
	if app.selector == nil || app.selector.Title != "Workspace Mode" {
		t.Fatalf("expected workspace mode selector, got %#v", app.selector)
	}
	if len(app.selector.Description) != 0 {
		t.Fatalf("expected workspace mode selector to rely on option labels without extra description, got %#v", app.selector.Description)
	}
	if len(app.selector.Options) != 2 {
		t.Fatalf("expected two workspace mode options, got %#v", app.selector.Options)
	}
	app.selector = nil
	limaCommandsPath := filepath.Join(t.TempDir(), "tui-node-create-lima.yaml")
	if err := os.WriteFile(limaCommandsPath, []byte("start: \"{{binary}} start {{instance_name}} --tty=false\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(lima commands) error = %v", err)
	}
	submitTUIDialog(t, app, map[string]string{
		"slug":               "root-node",
		"workspace_mode":     WorkspaceModeMounted,
		"lima_commands_file": limaCommandsPath,
	})

	rootNode, err := service.NodeShow(ctx, "root-node")
	if err != nil {
		t.Fatalf("NodeShow(root-node) error = %v", err)
	}
	if got := nodeWorkspaceMode(rootNode); got != WorkspaceModeMounted {
		t.Fatalf("expected created node workspace mode mounted, got %q", got)
	}
	if rootNode.WorkspaceMountPath != workspace {
		t.Fatalf("expected mounted workspace path %q, got %q", workspace, rootNode.WorkspaceMountPath)
	}
	if got := strings.Join(rootNode.LimaCommands.Start, "|"); got != "{{binary}} start {{instance_name}} --tty=false" {
		t.Fatalf("expected create node dialog to load lima command overrides, got %q", got)
	}
	if nodeAutoStartsSession(rootNode) {
		t.Fatalf("expected newly created node to remain non-running, got status %q", rootNode.Status)
	}
	if got := app.state.selectedEntry(); got.kind != tuiTreeEntryNode || got.node.ID != rootNode.ID {
		t.Fatalf("expected created node to become selected, got %#v", got)
	}
	if sessions.ensured[rootNode.ID] != 0 {
		t.Fatalf("expected created node to stay shell-free, got %d session creations", sessions.ensured[rootNode.ID])
	}

	selectTUIEntry(t, app, "project:"+rootProject.ID)
	if err := app.performAction(tuiActionSpec{ID: tuiActionProjectUpdate}); err != nil {
		t.Fatalf("performAction(update project) error = %v", err)
	}
	if app.dialog == nil || app.dialog.Title != "Update Project" {
		t.Fatalf("expected update project dialog, got %#v", app.dialog)
	}
	submitTUIDialog(t, app, map[string]string{
		"slug":           "root-renamed",
		"workspace_path": workspace,
	})

	if got := app.state.selectedEntry(); got.kind != tuiTreeEntryProject || got.project.ID != rootProject.ID || got.project.Slug != "root-renamed" {
		t.Fatalf("expected updated project to remain selected, got %#v", got)
	}

	updatedProject, err := service.store.ProjectByID(rootProject.ID)
	if err != nil {
		t.Fatalf("ProjectByID(root) error = %v", err)
	}
	if updatedProject.Slug != "root-renamed" {
		t.Fatalf("expected updated project slug root-renamed, got %q", updatedProject.Slug)
	}

	selectTUIEntry(t, app, "project:"+deleteProject.ID)
	if err := app.performAction(tuiActionSpec{ID: tuiActionProjectDelete}); err != nil {
		t.Fatalf("performAction(delete project) error = %v", err)
	}
	if app.dialog == nil || app.dialog.Title != "Delete Project" {
		t.Fatalf("expected delete project dialog, got %#v", app.dialog)
	}
	submitTUIDialog(t, app, map[string]string{})

	deletedProject, err := service.store.ProjectByID(deleteProject.ID)
	if err != nil {
		t.Fatalf("ProjectByID(delete-me) error = %v", err)
	}
	if deletedProject.DeletedAt == nil {
		t.Fatalf("expected project %q to be marked deleted", deleteProject.Slug)
	}
	if index := app.state.findEntryByKey("project:" + deleteProject.ID); index >= 0 {
		t.Fatalf("expected deleted project to disappear from the visible tree, still found at %d", index)
	}
}

func TestTUIDrawProjectDetailsShowProjectFilePathAndManualEditGuidance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:              "root",
		WorkspacePath:     workspace,
		BootstrapCommands: []string{"./script/setup"},
	})
	if err != nil {
		t.Fatalf("ProjectCreate(root) error = %v", err)
	}

	tree, err := service.ProjectTree("", false)
	if err != nil {
		t.Fatalf("ProjectTree() error = %v", err)
	}

	state, err := newTUIState(tree, newFakeTUISessionManager())
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: newTUISessionStore(ctx, service, func(vaxis.Event) {}),
		vx:       newRenderTestVaxis(t, 220, 30),
	}
	defer app.vx.Close()

	selectTUIEntry(t, app, "project:"+project.ID)
	app.draw()

	rendered := renderedScreenText(t, app.vx, 220, 30)
	projectFilePath := service.store.projectPath(project.ID)
	expectedPathPrefix := "Project file: " + projectFilePath
	if len(expectedPathPrefix) > 80 {
		expectedPathPrefix = expectedPathPrefix[:80]
	}
	if !strings.Contains(rendered, expectedPathPrefix) {
		t.Fatalf("expected rendered project details to include the project file path, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "edit the project file directly for advanced settings such as Lima command overrides") {
		t.Fatalf("expected rendered project details to include manual edit guidance, got:\n%s", rendered)
	}
}

func TestTUIEnvironmentConfigCreationAndProjectAssignment(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate(root) error = %v", err)
	}

	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())

	if err := app.performAction(tuiActionSpec{ID: tuiActionEnvironmentConfigManage}); err != nil {
		t.Fatalf("performAction(environment config manage) error = %v", err)
	}
	if app.menu == nil || app.menu.Title != "Environment Configs" {
		t.Fatalf("expected environment config menu, got %#v", app.menu)
	}
	for _, line := range app.menu.Description {
		if strings.Contains(line, "Reusable environment command sets can be assigned to multiple projects.") {
			t.Fatalf("expected environment config menu description to omit redundant copy, got %#v", app.menu.Description)
		}
	}
	chooseTUIMenuEntry(t, app, "Create Config")
	if app.dialog == nil || app.dialog.Title != "Create Environment Config" {
		t.Fatalf("expected create environment config dialog, got %#v", app.dialog)
	}
	if len(app.dialog.Fields) != 1 || app.dialog.Fields[0].Key != "slug" {
		t.Fatalf("expected create environment config dialog to only prompt for slug, got %#v", app.dialog.Fields)
	}
	submitTUIDialog(t, app, map[string]string{"slug": "shared-dev"})
	if app.menu == nil || app.menu.Title != "Environment Config: shared-dev" {
		t.Fatalf("expected command menu after create, got %#v", app.menu)
	}
	for _, line := range app.menu.Description {
		if strings.Contains(line, "Commands in this config apply to new nodes for every project") {
			t.Fatalf("expected environment config command menu to omit redundant copy, got %#v", app.menu.Description)
		}
	}
	chooseTUIMenuEntry(t, app, "Add Bootstrap Command")
	if app.dialog == nil || app.dialog.Title != "Add Environment Config Bootstrap Command" {
		t.Fatalf("expected add command dialog after create, got %#v", app.dialog)
	}
	submitTUIDialog(t, app, map[string]string{"command": "./script/setup"})

	config, err := service.EnvironmentConfigShow("shared-dev")
	if err != nil {
		t.Fatalf("EnvironmentConfigShow(shared-dev) error = %v", err)
	}
	if got := strings.Join(config.BootstrapCommands, "|"); got != "./script/setup" {
		t.Fatalf("expected created config command, got %q", got)
	}

	selectTUIEntry(t, app, "project:"+project.ID)
	if err := app.performAction(tuiActionSpec{ID: tuiActionProjectUpdate}); err != nil {
		t.Fatalf("performAction(update project) error = %v", err)
	}
	if app.dialog == nil || app.dialog.Title != "Update Project" {
		t.Fatalf("expected update project dialog, got %#v", app.dialog)
	}
	app.dialog.FieldIndex = 2
	quit, err := app.handleEvent(vaxis.Key{Keycode: vaxis.KeyEnter})
	if err != nil {
		t.Fatalf("handleEvent(open selector) error = %v", err)
	}
	if quit {
		t.Fatalf("expected selector open to keep app running")
	}
	if app.selector == nil || app.selector.Title != "Select Environment Configs" {
		t.Fatalf("expected environment config selector, got %#v", app.selector)
	}
	chooseTUISelector(t, app, "shared-dev")
	quit, err = app.handleEvent(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	if err != nil {
		t.Fatalf("handleEvent(Ctrl+s) error = %v", err)
	}
	if quit {
		t.Fatalf("expected Ctrl+s submit to keep app running")
	}
	if app.dialog != nil {
		t.Fatalf("expected update project dialog to close after submit")
	}

	updated, err := service.ProjectShow(project.ID)
	if err != nil {
		t.Fatalf("ProjectShow(updated root) error = %v", err)
	}
	if got := strings.Join(updated.EnvironmentConfigs, "|"); got != "shared-dev" {
		t.Fatalf("expected project to reference shared-dev, got %q", got)
	}

	selectTUIEntry(t, app, "project:"+project.ID)
	if err := app.performAction(tuiActionSpec{ID: tuiActionProjectUpdate}); err != nil {
		t.Fatalf("performAction(update project clear configs) error = %v", err)
	}
	if app.dialog == nil || app.dialog.Title != "Update Project" {
		t.Fatalf("expected update project dialog, got %#v", app.dialog)
	}
	app.dialog.FieldIndex = 2
	quit, err = app.handleEvent(vaxis.Key{Keycode: vaxis.KeyEnter})
	if err != nil {
		t.Fatalf("handleEvent(open selector for clear) error = %v", err)
	}
	if quit {
		t.Fatalf("expected selector open to keep app running")
	}
	if app.selector == nil || app.selector.Title != "Select Environment Configs" {
		t.Fatalf("expected environment config selector, got %#v", app.selector)
	}
	chooseTUISelector(t, app)
	quit, err = app.handleEvent(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	if err != nil {
		t.Fatalf("handleEvent(Ctrl+s clear configs) error = %v", err)
	}
	if quit {
		t.Fatalf("expected Ctrl+s clear submit to keep app running")
	}

	updated, err = service.ProjectShow(project.ID)
	if err != nil {
		t.Fatalf("ProjectShow(cleared root) error = %v", err)
	}
	if len(updated.EnvironmentConfigs) != 0 {
		t.Fatalf("expected cleared environment config refs, got %v", updated.EnvironmentConfigs)
	}
}

func TestTUICreateProjectDialogUsesEnvironmentConfigSelector(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate(root) error = %v", err)
	}
	if _, err := service.EnvironmentConfigCreate(EnvironmentConfigCreateInput{
		Slug:              "shared-dev",
		BootstrapCommands: []string{"./script/setup"},
	}); err != nil {
		t.Fatalf("EnvironmentConfigCreate(shared-dev) error = %v", err)
	}

	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())
	selectTUIEntry(t, app, "project:"+project.ID)

	if err := app.performAction(tuiActionSpec{ID: tuiActionProjectCreate}); err != nil {
		t.Fatalf("performAction(create project) error = %v", err)
	}
	if app.dialog == nil || app.dialog.Title != "Create Project" {
		t.Fatalf("expected create project dialog, got %#v", app.dialog)
	}
	if app.dialog.Fields[2].Input != nil {
		t.Fatalf("expected environment config field to use selector, not text input")
	}

	app.dialog.FieldIndex = 2
	quit, err := app.handleEvent(vaxis.Key{Keycode: vaxis.KeyEnter})
	if err != nil {
		t.Fatalf("handleEvent(open create selector) error = %v", err)
	}
	if quit {
		t.Fatalf("expected selector open to keep app running")
	}
	if app.selector == nil || app.selector.Title != "Select Environment Configs" {
		t.Fatalf("expected environment config selector, got %#v", app.selector)
	}
	chooseTUISelector(t, app, "shared-dev")
	app.dialog.SetFieldValue("workspace_path", filepath.Join(t.TempDir(), "child-project"))

	values, err := app.dialog.Values()
	if err != nil {
		t.Fatalf("dialog.Values() error = %v", err)
	}
	if values["environment_configs"] != "shared-dev" {
		t.Fatalf("expected selected environment config to be stored, got %q", values["environment_configs"])
	}
}

func TestTUIManageEnvironmentConfigUsesSelector(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	if _, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	}); err != nil {
		t.Fatalf("ProjectCreate(root) error = %v", err)
	}
	if _, err := service.EnvironmentConfigCreate(EnvironmentConfigCreateInput{
		Slug:              "shared-dev",
		BootstrapCommands: []string{"./script/setup"},
	}); err != nil {
		t.Fatalf("EnvironmentConfigCreate(shared-dev) error = %v", err)
	}

	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())

	if err := app.performAction(tuiActionSpec{ID: tuiActionEnvironmentConfigManage}); err != nil {
		t.Fatalf("performAction(environment config manage) error = %v", err)
	}
	if app.menu == nil || app.menu.Title != "Environment Configs" {
		t.Fatalf("expected environment config menu, got %#v", app.menu)
	}
	chooseTUIMenuEntry(t, app, "Manage Config")
	if app.selector == nil || app.selector.Title != "Manage Environment Config" {
		t.Fatalf("expected manage config selector, got %#v", app.selector)
	}
	chooseTUISelector(t, app, "shared-dev")
	if app.menu == nil || app.menu.Title != "Environment Config: shared-dev" {
		t.Fatalf("expected environment config command menu, got %#v", app.menu)
	}
}

func TestTUIEnvironmentConfigCommandEditingReopensMenuAndSupportsReorder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	if _, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	}); err != nil {
		t.Fatalf("ProjectCreate(root) error = %v", err)
	}
	if _, err := service.EnvironmentConfigCreate(EnvironmentConfigCreateInput{
		Slug:              "shared-dev",
		BootstrapCommands: []string{"./script/setup", "direnv allow", "mise install"},
	}); err != nil {
		t.Fatalf("EnvironmentConfigCreate(shared-dev) error = %v", err)
	}

	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())
	if err := app.openManageEnvironmentConfigDialog("shared-dev"); err != nil {
		t.Fatalf("openManageEnvironmentConfigDialog(shared-dev) error = %v", err)
	}
	chooseTUISelector(t, app, "shared-dev")
	if app.menu == nil || app.menu.Title != "Environment Config: shared-dev" {
		t.Fatalf("expected environment config command menu, got %#v", app.menu)
	}

	chooseTUIMenuEntry(t, app, "Add Bootstrap Command")
	if app.dialog == nil || app.dialog.Title != "Add Environment Config Bootstrap Command" {
		t.Fatalf("expected add command dialog, got %#v", app.dialog)
	}
	submitTUIDialog(t, app, map[string]string{"command": "brew bundle"})
	if app.menu == nil || app.menu.Title != "Environment Config: shared-dev" {
		t.Fatalf("expected command menu to reopen after add, got %#v", app.menu)
	}

	chooseTUIMenuEntry(t, app, "Move Bootstrap Command")
	if app.selector == nil || app.selector.Title != "Move Environment Config Bootstrap Command" {
		t.Fatalf("expected move command selector, got %#v", app.selector)
	}
	chooseTUISelector(t, app, "4. brew bundle")
	if app.menu == nil || app.menu.Title != "Move Environment Config Bootstrap Command: brew bundle" {
		t.Fatalf("expected move direction menu, got %#v", app.menu)
	}
	chooseTUIMenuEntry(t, app, "Move Up")
	if app.menu == nil || app.menu.Title != "Environment Config: shared-dev" {
		t.Fatalf("expected command menu to reopen after reorder, got %#v", app.menu)
	}

	chooseTUIMenuEntry(t, app, "Remove Bootstrap Command")
	if app.selector == nil || app.selector.Title != "Remove Environment Config Bootstrap Commands" {
		t.Fatalf("expected remove command selector, got %#v", app.selector)
	}
	chooseTUISelector(t, app, "2. direnv allow", "3. brew bundle")
	if app.dialog == nil || app.dialog.Title != "Remove Environment Config Bootstrap Commands" {
		t.Fatalf("expected remove command confirmation dialog, got %#v", app.dialog)
	}
	submitTUIDialog(t, app, map[string]string{})
	if app.menu == nil || app.menu.Title != "Environment Config: shared-dev" {
		t.Fatalf("expected command menu to reopen after remove, got %#v", app.menu)
	}

	config, err := service.EnvironmentConfigShow("shared-dev")
	if err != nil {
		t.Fatalf("EnvironmentConfigShow(shared-dev) error = %v", err)
	}
	if got := strings.Join(config.BootstrapCommands, "|"); got != "./script/setup|mise install" {
		t.Fatalf("expected reordered and removed commands, got %q", got)
	}
}

func TestTUIAddProjectActionCreatesProjectFromEmptyTree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	workspace := filepath.Join(t.TempDir(), "first-project")
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())

	if got := actionIDs(availableTUIActions(app.state.selectedEntry())); got != "project.create,environment_config.manage" {
		t.Fatalf("unexpected empty-tree action set: %s", got)
	}

	if err := app.performAction(tuiActionSpec{ID: tuiActionProjectCreate}); err != nil {
		t.Fatalf("performAction(create project) error = %v", err)
	}
	if app.dialog == nil || app.dialog.Title != "Create Project" {
		t.Fatalf("expected create project dialog, got %#v", app.dialog)
	}
	submitTUIDialog(t, app, map[string]string{
		"slug":           "first-project",
		"workspace_path": workspace,
	})

	project, err := service.ProjectShow("first-project")
	if err != nil {
		t.Fatalf("ProjectShow(first-project) error = %v", err)
	}
	if got := app.state.selectedEntry(); got.kind != tuiTreeEntryProject || got.project.ID != project.ID {
		t.Fatalf("expected first project to become selected, got %#v", got)
	}
}

func TestTUICreateProjectDialogDefaultsStayBlankWithExistingProjectSelected(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "codelima-codex",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())
	selectTUIEntry(t, app, "project:"+project.ID)

	if err := app.performAction(tuiActionSpec{ID: tuiActionProjectCreate}); err != nil {
		t.Fatalf("performAction(create project) error = %v", err)
	}
	if app.dialog == nil || app.dialog.Title != "Create Project" {
		t.Fatalf("expected create project dialog, got %#v", app.dialog)
	}

	if got := app.dialog.Fields[0].rawValue(); got != "" {
		t.Fatalf("expected project slug default to stay blank, got %q", got)
	}
	if got := app.dialog.Fields[1].rawValue(); got != "" {
		t.Fatalf("expected workspace path default to stay blank, got %q", got)
	}
}

func TestTUINodeActionsStartStopCloneAndDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	rootNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "root-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate(root-node) error = %v", err)
	}

	sessions := newFakeTUISessionManager()
	app := newTestTUIApp(t, ctx, service, sessions)
	selectTUIEntry(t, app, "node:"+rootNode.ID)

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodeStart}); err != nil {
		t.Fatalf("performAction(start node) error = %v", err)
	}

	startedNode, err := service.NodeShow(ctx, rootNode.ID)
	if err != nil {
		t.Fatalf("NodeShow(started root-node) error = %v", err)
	}
	if startedNode.Status != NodeStatusRunning {
		t.Fatalf("expected running node status, got %q", startedNode.Status)
	}
	if sessions.ensured[rootNode.ID] != 1 {
		t.Fatalf("expected one shell session creation for running node, got %d", sessions.ensured[rootNode.ID])
	}
	if got := app.state.selectedEntry(); got.kind != tuiTreeEntryNode || got.node.ID != rootNode.ID || got.node.Status != NodeStatusRunning {
		t.Fatalf("expected running root node to remain selected, got %#v", got)
	}

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodeStop}); err != nil {
		t.Fatalf("performAction(stop node) error = %v", err)
	}

	stoppedNode, err := service.NodeShow(ctx, rootNode.ID)
	if err != nil {
		t.Fatalf("NodeShow(stopped root-node) error = %v", err)
	}
	if stoppedNode.Status != NodeStatusStopped {
		t.Fatalf("expected stopped node status, got %q", stoppedNode.Status)
	}
	if got := app.state.selectedEntry(); got.kind != tuiTreeEntryNode || got.node.ID != rootNode.ID || got.node.Status != NodeStatusStopped {
		t.Fatalf("expected stopped root node to remain selected, got %#v", got)
	}

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodeClone}); err != nil {
		t.Fatalf("performAction(clone node) error = %v", err)
	}
	if app.dialog == nil || app.dialog.Title != "Clone Node" {
		t.Fatalf("expected clone node dialog, got %#v", app.dialog)
	}
	limaCommandsPath := filepath.Join(t.TempDir(), "tui-node-clone-lima.yaml")
	if err := os.WriteFile(limaCommandsPath, []byte("clone: \"{{binary}} clone {{source_instance}} {{target_instance}} --tty=false\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(lima commands) error = %v", err)
	}
	submitTUIDialog(t, app, map[string]string{
		"node_slug":          "child-node",
		"lima_commands_file": limaCommandsPath,
	})

	childNode, err := service.NodeShow(ctx, "child-node")
	if err != nil {
		t.Fatalf("NodeShow(child-node) error = %v", err)
	}
	if childNode.ParentNodeID != rootNode.ID {
		t.Fatalf("expected cloned node parent id %q, got %q", rootNode.ID, childNode.ParentNodeID)
	}
	if got := strings.Join(childNode.LimaCommands.Clone, "|"); got != "{{binary}} clone {{source_instance}} {{target_instance}} --tty=false" {
		t.Fatalf("expected clone node dialog to load lima command overrides, got %q", got)
	}
	if got := app.state.selectedEntry(); got.kind != tuiTreeEntryNode || got.node.ID != childNode.ID {
		t.Fatalf("expected cloned child node to become selected, got %#v", got)
	}

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodeDelete}); err != nil {
		t.Fatalf("performAction(delete node) error = %v", err)
	}
	if app.dialog == nil || app.dialog.Title != "Delete Node" {
		t.Fatalf("expected delete node dialog, got %#v", app.dialog)
	}
	submitTUIDialog(t, app, map[string]string{})

	deletedChild, err := service.NodeShow(ctx, childNode.ID)
	if err != nil {
		t.Fatalf("NodeShow(deleted child-node) error = %v", err)
	}
	if deletedChild.Status != NodeStatusTerminated || deletedChild.DeletedAt == nil {
		t.Fatalf("expected deleted child node to be terminated, got status=%q deleted_at=%v", deletedChild.Status, deletedChild.DeletedAt)
	}
	if index := app.state.findEntryByKey("node:" + childNode.ID); index >= 0 {
		t.Fatalf("expected deleted node to disappear from the visible tree, still found at %d", index)
	}
}

func TestTUINodeCloneLeavesProviderStartedCloneStoppedUntilExplicitStart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	service.lima.(*fakeLima).cloneStatus = "running"
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	rootNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "root-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate(root-node) error = %v", err)
	}

	if _, err := service.NodeStart(ctx, rootNode.ID); err != nil {
		t.Fatalf("NodeStart(root-node) error = %v", err)
	}

	sessions := newFakeTUISessionManager()
	app := newTestTUIApp(t, ctx, service, sessions)
	selectTUIEntry(t, app, "node:"+rootNode.ID)

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodeClone}); err != nil {
		t.Fatalf("performAction(clone node) error = %v", err)
	}
	if app.dialog == nil || app.dialog.Title != "Clone Node" {
		t.Fatalf("expected clone node dialog, got %#v", app.dialog)
	}
	submitTUIDialog(t, app, map[string]string{
		"node_slug":          "child-node",
		"lima_commands_file": "",
	})

	childNode, err := service.NodeShow(ctx, "child-node")
	if err != nil {
		t.Fatalf("NodeShow(child-node) error = %v", err)
	}
	if childNode.Status != NodeStatusStopped {
		t.Fatalf("expected cloned child node to be stopped until explicitly started, got %q", childNode.Status)
	}
	if got := app.state.selectedEntry(); got.kind != tuiTreeEntryNode || got.node.ID != childNode.ID || got.node.Status != NodeStatusStopped {
		t.Fatalf("expected stopped cloned child node to become selected, got %#v", got)
	}
	if sessions.ensured[childNode.ID] != 0 {
		t.Fatalf("expected stopped cloned child node to avoid auto-opening a shell session, got %d session creations", sessions.ensured[childNode.ID])
	}
}

func testTUITree(t *testing.T) []ProjectTreeNode {
	t.Helper()

	rootWorkspace := filepath.Join(t.TempDir(), "root")
	childWorkspace := filepath.Join(t.TempDir(), "child")

	return []ProjectTreeNode{
		{
			Project: Project{
				ID:            "project-root",
				Slug:          "root",
				WorkspacePath: rootWorkspace,
			},
			Nodes: []Node{
				{
					ID:                 "node-root",
					Slug:               "root-node",
					GuestWorkspacePath: rootWorkspace,
					Status:             NodeStatusRunning,
				},
			},
			Children: []ProjectTreeNode{
				{
					Project: Project{
						ID:            "project-child",
						Slug:          "child",
						WorkspacePath: childWorkspace,
					},
					Nodes: []Node{
						{
							ID:                 "node-child",
							Slug:               "child-node",
							GuestWorkspacePath: childWorkspace,
							Status:             NodeStatusRunning,
						},
					},
				},
			},
		},
	}
}

func testTUITreeWithCreatedNode(t *testing.T) []ProjectTreeNode {
	t.Helper()

	rootWorkspace := filepath.Join(t.TempDir(), "root")
	childWorkspace := filepath.Join(t.TempDir(), "child")

	return []ProjectTreeNode{
		{
			Project: Project{
				ID:            "project-root",
				Slug:          "root",
				WorkspacePath: rootWorkspace,
			},
			Nodes: []Node{
				{
					ID:                 "node-root",
					Slug:               "root-node",
					GuestWorkspacePath: rootWorkspace,
					Status:             NodeStatusRunning,
				},
			},
			Children: []ProjectTreeNode{
				{
					Project: Project{
						ID:            "project-child",
						Slug:          "child",
						WorkspacePath: childWorkspace,
					},
					Nodes: []Node{
						{
							ID:                 "node-created",
							Slug:               "created-node",
							GuestWorkspacePath: childWorkspace,
							Status:             NodeStatusCreated,
						},
					},
				},
			},
		},
	}
}

func actionIDs(actions []tuiActionSpec) string {
	values := make([]string, 0, len(actions))
	for _, action := range actions {
		values = append(values, string(action.ID))
	}
	return strings.Join(values, ",")
}

func newTestTUIApp(t *testing.T, ctx context.Context, service *Service, sessions tuiSessionManager) *vaxisTUIApp {
	t.Helper()

	tree, err := service.ProjectTree("", false)
	if err != nil {
		t.Fatalf("ProjectTree() error = %v", err)
	}

	state, err := newTUIState(tree, sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: newTUISessionStore(ctx, service, func(vaxis.Event) {}),
	}

	return app
}

func selectTUIEntry(t *testing.T, app *vaxisTUIApp, key string) {
	t.Helper()

	index := app.state.findEntryByKey(key)
	if index < 0 {
		t.Fatalf("expected tree entry %q to exist", key)
	}
	if err := app.state.selectIndex(index); err != nil {
		t.Fatalf("selectIndex(%q) error = %v", key, err)
	}
}

func submitTUIDialog(t *testing.T, app *vaxisTUIApp, values map[string]string) {
	t.Helper()

	if app.dialog == nil {
		t.Fatalf("expected dialog to be open")
	}
	if err := app.dialog.OnSubmit(values); err != nil {
		t.Fatalf("dialog submit error = %v", err)
	}
	app.dialog = nil
}

func chooseTUIMenuEntry(t *testing.T, app *vaxisTUIApp, label string) {
	t.Helper()

	if app.menu == nil {
		t.Fatalf("expected menu to be open")
	}
	current := app.menu

	for _, entry := range app.menu.Entries {
		if entry.Label != label {
			continue
		}
		if err := entry.Action(); err != nil {
			t.Fatalf("menu action %q error = %v", label, err)
		}
		if app.menu == current {
			app.menu = nil
		}
		return
	}

	t.Fatalf("expected menu entry %q", label)
}

func chooseTUISelector(t *testing.T, app *vaxisTUIApp, values ...string) {
	t.Helper()

	if app.selector == nil {
		t.Fatalf("expected selector to be open")
	}

	if app.selector.Multi {
		app.selector.Selected = map[string]bool{}
	}

	for _, value := range values {
		found := false
		for index, option := range app.selector.Options {
			if option.Value != value && option.Label != value {
				continue
			}
			app.selector.Index = index
			if app.selector.Multi {
				app.selector.Selected[option.Value] = true
			}
			found = true
			break
		}
		if !found {
			t.Fatalf("expected selector option %q", value)
		}
	}

	if !app.selector.Multi && len(values) > 0 {
		for index, option := range app.selector.Options {
			if option.Value != values[0] && option.Label != values[0] {
				continue
			}
			app.selector.Index = index
			break
		}
	}

	completed, cancelled, err := app.selector.Update(vaxis.Key{Keycode: vaxis.KeyEnter})
	if err != nil {
		t.Fatalf("selector submit error = %v", err)
	}
	if !completed || cancelled {
		t.Fatalf("expected selector to complete, completed=%v cancelled=%v", completed, cancelled)
	}
	app.selector = nil
}
