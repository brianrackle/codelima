package codelima

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/containerd/console"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

type fakeTUIRunner struct {
	calls         int
	workspaceRoot string
}

type fakeVaxisConsole struct {
	input bytes.Buffer
	size  console.WinSize
}

func (f *fakeTUIRunner) Run(_ context.Context, _ *Service, workspaceRoot string) error {
	f.calls++
	f.workspaceRoot = workspaceRoot
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

func decodedVaxisInputKey(t *testing.T, input string) vaxis.Key {
	t.Helper()

	console := newFakeVaxisConsole("\x1b[?1;2c"+input, 80, 24)
	vx, err := vaxis.New(vaxis.Options{
		WithConsole:  console,
		DisableMouse: true,
		NoSignals:    true,
	})
	if err != nil {
		t.Fatalf("vaxis.New() error = %v", err)
	}
	defer vx.Close()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-vx.Events():
			key, ok := event.(vaxis.Key)
			if ok {
				return key
			}
		case <-deadline:
			t.Fatalf("timed out waiting for decoded key input %q", input)
		}
	}
}

func renderedCellGrapheme(t *testing.T, vx *vaxis.Vaxis, col, row int) string {
	t.Helper()

	buf := reflect.ValueOf(vx).Elem().FieldByName("screenNext").Elem().FieldByName("buf")
	cell := buf.Index(row).Index(col)
	return cell.FieldByName("Character").FieldByName("Grapheme").String()
}

func renderedCellStyle(t *testing.T, vx *vaxis.Vaxis, col, row int) vaxis.Style {
	t.Helper()

	buf := reflect.ValueOf(vx).Elem().FieldByName("screenNext").Elem().FieldByName("buf")
	cell := buf.Index(row).Index(col)
	style := cell.FieldByName("Style")
	return vaxis.Style{
		Hyperlink:       style.FieldByName("Hyperlink").String(),
		HyperlinkParams: style.FieldByName("HyperlinkParams").String(),
		Foreground:      vaxis.Color(style.FieldByName("Foreground").Uint()),
		Background:      vaxis.Color(style.FieldByName("Background").Uint()),
		UnderlineColor:  vaxis.Color(style.FieldByName("UnderlineColor").Uint()),
		UnderlineStyle:  vaxis.UnderlineStyle(style.FieldByName("UnderlineStyle").Uint()),
		Attribute:       vaxis.AttributeMask(style.FieldByName("Attribute").Uint()),
	}
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
	opened      map[string]int
	tabCounters map[string]int
	openTabs    map[string][]string
}

type sharedFakeTUISessionManager struct {
	store  *tuiSessionStore
	opened map[string]int
}

type fakeTUITerminal struct {
	snapshot      string
	termEnv       string
	capturesMouse bool
	events        []vaxis.Event
	drawCalls     int
	startCmd      *exec.Cmd
	startErr      error
	startCols     int
	startRows     int
	focusCalls    int
	blurCalls     int
	closeCalls    int
}

func newFakeTUISessionManager() *fakeTUISessionManager {
	return &fakeTUISessionManager{
		opened:      map[string]int{},
		tabCounters: map[string]int{},
		openTabs:    map[string][]string{},
	}
}

func newSharedFakeTUISessionManager(store *tuiSessionStore) *sharedFakeTUISessionManager {
	return &sharedFakeTUISessionManager{
		store:  store,
		opened: map[string]int{},
	}
}

func newFakeTUITerminal() *fakeTUITerminal {
	return &fakeTUITerminal{termEnv: tuiEmbeddedTermEnv}
}

func (f *fakeTUITerminal) Start(cmd *exec.Cmd) error {
	f.startCmd = cmd
	return f.startErr
}

func (f *fakeTUITerminal) Resize(width, height int) {
	f.startCols = width
	f.startRows = height
}

func (f *fakeTUITerminal) Update(event vaxis.Event) {
	f.events = append(f.events, event)
}

func (f *fakeTUITerminal) Draw(win vaxis.Window) {
	f.drawCalls++
	for row, line := range strings.Split(f.snapshot, "\n") {
		win.Println(row, vaxis.Segment{Text: line})
	}
}

func (f *fakeTUITerminal) Close() { f.closeCalls++ }

func (f *fakeTUITerminal) Focus() { f.focusCalls++ }

func (f *fakeTUITerminal) Blur() { f.blurCalls++ }

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

func nodeTargetKey(nodeID string) string {
	return "node:" + nodeID
}

// putTestSession registers a terminal tab for the target in the store and
// returns the allocated session key.
func putTestSession(store *tuiSessionStore, targetKey string, session *tuiSession, term tuiTerminal) string {
	session.target = targetKey
	session.key = store.nextSessionKey(targetKey)
	if term != nil {
		session.terminalID = store.registry.Allocate(term).ID
	}
	store.putSession(session)
	return session.key
}

// targetTestSession returns the first open tab session for the target.
func targetTestSession(t *testing.T, store *tuiSessionStore, targetKey string) *tuiSession {
	t.Helper()

	keys := store.TargetSessionKeys(targetKey)
	if len(keys) == 0 {
		t.Fatalf("expected an open terminal tab for %q", targetKey)
	}
	return store.sessions[keys[0]]
}

// targetTestTerminal resolves the live terminal backend for the target's first
// open tab through the runtime registry, asserting it is the *fakeTUITerminal
// tests inject.
func targetTestTerminal(t *testing.T, store *tuiSessionStore, targetKey string) *fakeTUITerminal {
	t.Helper()

	session := targetTestSession(t, store, targetKey)
	term, ok := store.terminalFor(session)
	if !ok {
		t.Fatalf("expected a live terminal for %q", targetKey)
	}
	fake, ok := term.(*fakeTUITerminal)
	if !ok {
		t.Fatalf("expected fake terminal for %q, got %T", targetKey, term)
	}
	return fake
}

func sessionTestTerminal(t *testing.T, store *tuiSessionStore, sessionKey string) *fakeTUITerminal {
	t.Helper()
	term, ok := store.SessionTerminal(sessionKey)
	if !ok {
		t.Fatalf("expected a live terminal for session %q", sessionKey)
	}
	fake, ok := term.(*fakeTUITerminal)
	if !ok {
		t.Fatalf("expected fake terminal for session %q, got %T", sessionKey, term)
	}
	return fake
}

func (f *fakeTUISessionManager) HasSession(sessionKey string) bool {
	for _, keys := range f.openTabs {
		for _, key := range keys {
			if key == sessionKey {
				return true
			}
		}
	}
	return false
}

func (f *fakeTUISessionManager) TargetSessionKeys(targetKey string) []string {
	return append([]string(nil), f.openTabs[targetKey]...)
}

func (f *fakeTUISessionManager) openTab(targetKey string) (string, error) {
	f.opened[targetKey]++
	f.tabCounters[targetKey]++
	key := formatSessionKey(targetKey, f.tabCounters[targetKey])
	f.openTabs[targetKey] = append(f.openTabs[targetKey], key)
	return key, nil
}

func (f *fakeTUISessionManager) OpenNodeTab(node Node) (string, error) {
	return f.openTab(nodeTargetKey(node.ID))
}

func (f *fakeTUISessionManager) OpenNodeHostTab(node Node) (string, error) {
	return f.openTab(nodeTargetKey(node.ID))
}

func (f *sharedFakeTUISessionManager) HasSession(sessionKey string) bool {
	return f.store.HasSession(sessionKey)
}

func (f *sharedFakeTUISessionManager) TargetSessionKeys(targetKey string) []string {
	return f.store.TargetSessionKeys(targetKey)
}

func (f *sharedFakeTUISessionManager) OpenNodeTab(node Node) (string, error) {
	targetKey := nodeTargetKey(node.ID)
	f.opened[targetKey]++
	key := f.store.nextSessionKey(targetKey)
	f.store.putSession(&tuiSession{
		key:        key,
		target:     targetKey,
		shellKind:  terminal.NodeShell,
		label:      node.Slug,
		node:       node,
		terminalID: f.store.registry.Allocate(newFakeTUITerminal()).ID,
	})
	return key, nil
}

func (f *sharedFakeTUISessionManager) OpenNodeHostTab(node Node) (string, error) {
	targetKey := nodeTargetKey(node.ID)
	f.opened[targetKey]++
	key := f.store.nextSessionKey(targetKey)
	f.store.putSession(&tuiSession{
		key:        key,
		target:     targetKey,
		shellKind:  terminal.NodeHostShell,
		label:      node.Slug + " host",
		node:       node,
		terminalID: f.store.registry.Allocate(newFakeTUITerminal()).ID,
	})
	return key, nil
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
	if runner.workspaceRoot != "" {
		t.Fatalf("expected default TUI launch to have no workspace root, got %q", runner.workspaceRoot)
	}
}

func TestDispatchPathRunsInjectedRunnerWithWorkspaceRoot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	runner := &fakeTUIRunner{}
	service.tui = runner
	workspaceRoot := t.TempDir()
	canonicalRoot, err := canonicalPath(workspaceRoot)
	if err != nil {
		t.Fatalf("canonicalPath() error = %v", err)
	}

	if _, err := dispatch(ctx, service, []string{workspaceRoot}); err != nil {
		t.Fatalf("dispatch(path) error = %v", err)
	}

	if runner.calls != 1 {
		t.Fatalf("expected runner to be called once, got %d", runner.calls)
	}
	if runner.workspaceRoot != canonicalRoot {
		t.Fatalf("expected scoped TUI root %q, got %q", canonicalRoot, runner.workspaceRoot)
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
		if !errors.As(err, &appErr) {
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

func TestTUISelectedStyleMatchesTreeHighlight(t *testing.T) {
	t.Parallel()

	style := tuiSelectedStyle()
	if style.Foreground != vaxis.ColorWhite {
		t.Fatalf("expected selected style foreground to be ColorWhite, got %v", style.Foreground)
	}
	if style.Background != vaxis.ColorBlue {
		t.Fatalf("expected selected style background to be ColorBlue, got %v", style.Background)
	}
	if style.Attribute != vaxis.AttrBold {
		t.Fatalf("expected selected style to use bold text, got %v", style.Attribute)
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

func TestTUIDrawTransientRightPaneContentUsesSplitLayout(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		title     string
		configure func(*vaxisTUIApp)
	}{
		{
			name:  "dialog",
			title: "Create Node",
			configure: func(app *vaxisTUIApp) {
				app.overlay = newTUIDialog(
					"Create Node",
					"Create",
					nil,
					[]tuiDialogField{newTUIInputField("slug", "Node Slug", "", true)},
					nil,
				)
			},
		},
		{
			name:  "menu",
			title: "Environments",
			configure: func(app *vaxisTUIApp) {
				app.overlay = &tuiMenu{
					Title:   "Environments",
					Entries: []tuiMenuEntry{{Key: 'c', Label: "Create Environment"}},
				}
			},
		},
		{
			name:  "selector",
			title: "Select Environments",
			configure: func(app *vaxisTUIApp) {
				app.overlay = newTUISelector(
					"Select Environments",
					nil,
					[]tuiSelectorOption{{Label: "codex", Value: "codex"}},
					nil,
					false,
					nil,
				)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			service, _ := newTestService(t)
			sessions := newFakeTUISessionManager()
			state, err := newTUIState(testTUINodes(t), sessions)
			if err != nil {
				t.Fatalf("newTUIState() error = %v", err)
			}
			if err := state.focusTerminal(); err != nil {
				t.Fatalf("focusTerminal() error = %v", err)
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
			tc.configure(app)

			app.draw()

			layout := layoutTUIBody(100, tuiFocusTree)
			if got := renderedCellGrapheme(t, vx, layout.termCol+1, 2); got != string(tc.title[0]) {
				t.Fatalf("expected %s title to start in the right pane, got %q", tc.name, got)
			}

			rendered := renderedScreenText(t, vx, 100, 24)
			if !strings.Contains(rendered, "Nodes") {
				t.Fatalf("expected %s to keep the tree visible, got:\n%s", tc.name, rendered)
			}
		})
	}
}

func TestTUIDrawBackgroundOperationKeepsSelectedNodeInPrimaryPane(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUINodesWithCreatedNode(t), sessions)
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
		operations: map[string]*tuiOperationState{
			"op-node-start": {
				ID:            "op-node-start",
				Title:         "Starting created-node",
				DisplayStatus: "starting",
				EntryKeys:     []string{"node:node-created"},
				ResourceKeys:  []string{"node:node-created"},
				Lines:         []string{"waiting for command output..."},
			},
		},
		operationOrder: []string{"op-node-start"},
	}
	selectTUIEntry(t, app, "node:node-created")

	app.draw()

	rendered := renderedScreenText(t, vx, 100, 24)
	if !strings.Contains(rendered, "created-node") {
		t.Fatalf("expected tree to render the background operation status, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Status: starting") {
		t.Fatalf("expected details pane to render the background operation status, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Background tasks") || !strings.Contains(rendered, "Starting created-node") {
		t.Fatalf("expected details pane to summarize the background task, got:\n%s", rendered)
	}
}

func TestTUISyncSessionFocusBlursHiddenTerminalWhenDialogOpen(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)

	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	state.terminalTarget = nodeTargetKey("node-root")
	state.focus = tuiFocusTerminal

	fakeTerminal := newFakeTUITerminal()
	putTestSession(sessions, nodeTargetKey("node-root"), &tuiSession{
		shellKind: terminal.NodeShell,
		label:     "root-node",
		node:      Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
	}, fakeTerminal)

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: sessions,
		overlay: newTUIDialog(
			"Create Node",
			"Create",
			nil,
			[]tuiDialogField{newTUIInputField("slug", "Node Slug", "", true)},
			nil,
		),
	}

	app.syncSessionFocus()

	if fakeTerminal.focusCalls != 0 {
		t.Fatalf("expected hidden terminal not to be focused, got %d focus calls", fakeTerminal.focusCalls)
	}
	if fakeTerminal.blurCalls != 1 {
		t.Fatalf("expected hidden terminal to be blurred once, got %d blur calls", fakeTerminal.blurCalls)
	}
}

func TestTUIDrawOmitsRedundantTerminalChrome(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUINodes(t), sessions)
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
	putTestSession(app.sessions, nodeTargetKey("node-root"), &tuiSession{
		shellKind: terminal.NodeShell,
		label:     "root-node",
		node:      Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
	}, &fakeTUITerminal{
		snapshot: "shell prompt",
		termEnv:  tuiEmbeddedTermEnv,
	})

	app.draw()

	rendered := renderedScreenText(t, vx, 100, 24)
	if !strings.Contains(rendered, "Node: root-node") || !strings.Contains(rendered, "Mode: copy") {
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

func TestTUIDrawTerminalUsesFullWidthWithoutSideBorders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	state.treePaneMode = tuiTreePaneModeTerminal

	vx := newRenderTestVaxis(t, 100, 24)
	defer vx.Close()

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: sessions,
		vx:       vx,
	}
	putTestSession(app.sessions, nodeTargetKey("node-root"), &tuiSession{
		shellKind: terminal.NodeShell,
		label:     "root-node",
		node:      Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
	}, &fakeTUITerminal{
		snapshot: "shell prompt",
		termEnv:  tuiEmbeddedTermEnv,
	})

	app.draw()

	layout := layoutTUIBody(100, tuiFocusTree)
	if got := renderedCellGrapheme(t, vx, layout.termCol, 2); got != "s" {
		t.Fatalf("expected terminal content to start at the left edge of the pane, got %q", got)
	}
	if got := renderedCellGrapheme(t, vx, layout.termCol, 1); got != " " {
		t.Fatalf("expected titled pane chrome at the top edge, got %q", got)
	}
}

func TestTUIDrawRendersPaneTabsInBorder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUINodes(t), sessions)
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

	app.draw()
	rendered := renderedScreenText(t, vx, 100, 24)
	if !strings.Contains(rendered, "Info [Terminal]") {
		t.Fatalf("expected a running node to render terminal-mode tabs by default, got:\n%s", rendered)
	}

	state.toggleTreePaneMode()
	app.draw()

	rendered = renderedScreenText(t, vx, 100, 24)
	if !strings.Contains(rendered, "[Info] Terminal") {
		t.Fatalf("expected info-mode tabs after toggling, got:\n%s", rendered)
	}
}

func TestRenderFooterUsesAvailableActionsForFocus(t *testing.T) {
	t.Parallel()

	got := renderFooter(tuiFocusTree, tuiTreePaneModeTerminal, tuiTreeEntry{})
	if got != "[n] create node   [a] configurations   [g] environments   q quit" {
		t.Fatalf("expected empty-list footer with global actions, got %q", got)
	}

	runningNodeEntry := tuiTreeEntry{
		node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
	}
	got = renderFooter(tuiFocusTree, tuiTreePaneModeInfo, runningNodeEntry)
	if got != "Up/Down move   "+terminalViewToggleFooterHint+" shell focus   "+terminalTabOpenFooterHint+" new tab   "+infoViewToggleFooterHint+" terminal   "+hostTerminalTabOpenFooterHint+" host tab   [n] create node   [a] configurations   [g] environments   [s] stop node   [d] delete node   [c] clone node   q quit" {
		t.Fatalf("expected running-node footer with shell focus and node actions, got %q", got)
	}
	if strings.Contains(got, "drag copy") || strings.Contains(got, "wheel scroll") {
		t.Fatalf("expected running-node footer to omit mouse hints, got %q", got)
	}
	if strings.Contains(got, "Left/Right collapse") {
		t.Fatalf("expected the flat node list footer to omit collapse hints, got %q", got)
	}

	got = renderFooter(tuiFocusTree, tuiTreePaneModeTerminal, runningNodeEntry)
	if got != "Up/Down move   "+terminalViewToggleFooterHint+" shell focus   "+terminalTabOpenFooterHint+" new tab   "+infoViewToggleFooterHint+" info   "+terminalTabPrevFooterHint+"/"+terminalTabNextFooterHint+" switch tab   "+terminalTabCloseFooterHint+" close tab   "+hostTerminalTabOpenFooterHint+" host tab   [n] create node   [a] configurations   [g] environments   [s] stop node   [d] delete node   [c] clone node   q quit" {
		t.Fatalf("expected running-node terminal-pane footer with tab management hints, got %q", got)
	}

	stoppedNodeEntry := tuiTreeEntry{
		node: Node{ID: "node-stopped", Slug: "stopped-node", Status: NodeStatusStopped},
	}
	got = renderFooter(tuiFocusTree, tuiTreePaneModeInfo, stoppedNodeEntry)
	if got != "Up/Down move   "+infoViewToggleFooterHint+" terminal   "+hostTerminalTabOpenFooterHint+" host tab   [n] create node   [a] configurations   [g] environments   [s] start node   [d] delete node   [c] clone node   q quit" {
		t.Fatalf("expected stopped-node footer without shell focus, got %q", got)
	}

	got = renderFooter(tuiFocusTerminal, tuiTreePaneModeTerminal, runningNodeEntry)
	if got != terminalViewToggleFooterHint+" tree focus   "+terminalTabOpenFooterHint+" new tab   "+terminalTabPrevFooterHint+"/"+terminalTabNextFooterHint+" switch tab   "+terminalTabCloseFooterHint+" close tab   "+hostTerminalTabOpenFooterHint+" host tab   q quit" {
		t.Fatalf("expected terminal footer without mouse hints, got %q", got)
	}
	if strings.Contains(got, "drag copy") || strings.Contains(got, "wheel scroll") {
		t.Fatalf("expected terminal footer to omit mouse hints, got %q", got)
	}
}

func TestCurrentPaneTabsHighlightActiveMode(t *testing.T) {
	t.Parallel()

	app := &vaxisTUIApp{
		state: &tuiState{
			focus:        tuiFocusTree,
			treePaneMode: tuiTreePaneModeInfo,
		},
	}

	nodeEntry := tuiTreeEntry{node: Node{ID: "node-root"}}
	if got := app.currentPaneTabs(nodeEntry); got != "[Info] Terminal" {
		t.Fatalf("expected node info pane tabs %q, got %q", "[Info] Terminal", got)
	}

	app.state.treePaneMode = tuiTreePaneModeTerminal
	if got := app.currentPaneTabs(nodeEntry); got != "Info [Terminal]" {
		t.Fatalf("expected node terminal pane tabs %q, got %q", "Info [Terminal]", got)
	}

	if got := app.currentPaneTabs(tuiTreeEntry{}); got != "" {
		t.Fatalf("expected empty selection to render no pane tabs, got %q", got)
	}
}

func TestCurrentPaneTabSegmentsHighlightActiveMode(t *testing.T) {
	t.Parallel()

	activeStyle := tuiSelectedStyle()
	inactiveStyle := tuiMutedStyle()
	app := &vaxisTUIApp{
		state: &tuiState{
			focus:        tuiFocusTree,
			treePaneMode: tuiTreePaneModeInfo,
		},
	}

	nodeEntry := tuiTreeEntry{node: Node{ID: "node-root"}}
	got := app.currentPaneTabSegments(nodeEntry, activeStyle, inactiveStyle)
	if len(got) != 2 {
		t.Fatalf("expected 2 info-mode tab segments, got %d", len(got))
	}
	if got[0].Text != "[Info]" || !reflect.DeepEqual(got[0].Style, activeStyle) {
		t.Fatalf("expected highlighted info segment, got %#v", got[0])
	}
	if got[1].Text != " Terminal" || !reflect.DeepEqual(got[1].Style, inactiveStyle) {
		t.Fatalf("expected muted terminal segment, got %#v", got[1])
	}

	app.state.treePaneMode = tuiTreePaneModeTerminal
	got = app.currentPaneTabSegments(nodeEntry, activeStyle, inactiveStyle)
	if len(got) != 2 {
		t.Fatalf("expected 2 terminal-mode tab segments, got %d", len(got))
	}
	if got[0].Text != "Info " || !reflect.DeepEqual(got[0].Style, inactiveStyle) {
		t.Fatalf("expected muted info segment, got %#v", got[0])
	}
	if got[1].Text != "[Terminal]" || !reflect.DeepEqual(got[1].Style, activeStyle) {
		t.Fatalf("expected highlighted terminal segment, got %#v", got[1])
	}

	if got := app.currentPaneTabSegments(tuiTreeEntry{}, activeStyle, inactiveStyle); len(got) != 0 {
		t.Fatalf("expected empty selection to render no tab segments, got %#v", got)
	}
}

func TestCurrentPaneTabSegmentsShowOpenTerminalTabs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: sessions,
	}
	if err := app.openTerminalTab(); err != nil {
		t.Fatalf("openTerminalTab(root) error = %v", err)
	}
	selectTUIEntry(t, app, nodeTargetKey("node-child"))
	if err := app.openTerminalTab(); err != nil {
		t.Fatalf("openTerminalTab(child) error = %v", err)
	}
	if err := app.openTerminalTab(); err != nil {
		t.Fatalf("openTerminalTab(child second tab) error = %v", err)
	}

	// Tabs are scoped to the focused tree item: only the child's tabs show.
	activeStyle := tuiSelectedStyle()
	inactiveStyle := tuiMutedStyle()
	got := app.currentPaneTabSegments(app.state.selectedEntry(), activeStyle, inactiveStyle)
	text := segmentsText(got)
	if !strings.Contains(text, "child-node 1") || !strings.Contains(text, "[child-node 2]") {
		t.Fatalf("expected the focused node's tabs in pane border, got %q", text)
	}
	if strings.Contains(text, "root-node") {
		t.Fatalf("expected other nodes' tabs to stay hidden, got %q", text)
	}

	selectTUIEntry(t, app, nodeTargetKey("node-root"))
	got = app.currentPaneTabSegments(app.state.selectedEntry(), activeStyle, inactiveStyle)
	text = segmentsText(got)
	if !strings.Contains(text, "[root-node]") {
		t.Fatalf("expected the root node tab when the root node is focused, got %q", text)
	}
	if strings.Contains(text, "child-node") {
		t.Fatalf("expected child tabs to stay hidden while root is focused, got %q", text)
	}
}

func segmentsText(segments []vaxis.Segment) string {
	var text strings.Builder
	for _, segment := range segments {
		text.WriteString(segment.Text)
	}
	return text.String()
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

func TestTUITerminalPaneSizeUsesSinglePaneGeometry(t *testing.T) {
	t.Parallel()

	cols, rows := tuiTerminalPaneBodySize(120, 24, tuiFocusTerminal)
	if cols != 120 || rows != 20 {
		t.Fatalf("expected full terminal size 120x20, got %dx%d", cols, rows)
	}

	cols, rows = tuiTerminalPaneBodySize(120, 24, tuiFocusTree)
	if cols != 79 || rows != 20 {
		t.Fatalf("expected right-pane terminal size 79x20, got %dx%d", cols, rows)
	}
}

func TestTerminalViewToggleKeyMatchesAltBacktickOrF6(t *testing.T) {
	t.Parallel()

	if !isTerminalViewToggleKey(vaxis.Key{Text: "`", Keycode: '`', Modifiers: vaxis.ModAlt}) {
		t.Fatalf("expected Alt+` to match the terminal view toggle key")
	}
	if !isTerminalViewToggleKey(vaxis.Key{Text: "`", Keycode: '`', Modifiers: vaxis.ModMeta}) {
		t.Fatalf("expected Meta+` to match the terminal view toggle key")
	}
	if !isTerminalViewToggleKey(vaxis.Key{Keycode: vaxis.KeyF06}) {
		t.Fatalf("expected F6 to match the terminal view toggle key")
	}
	if isTerminalViewToggleKey(vaxis.Key{Text: "~", Keycode: '`', ShiftedCode: '~', Modifiers: vaxis.ModAlt | vaxis.ModShift}) {
		t.Fatalf("expected Option+Shift+` not to toggle terminal focus")
	}
	if isTerminalViewToggleKey(vaxis.Key{Text: "`", Keycode: '`', Modifiers: vaxis.ModSuper}) {
		t.Fatalf("expected Super+` not to match the terminal view toggle key")
	}
	if isTerminalViewToggleKey(vaxis.Key{Keycode: vaxis.KeyF05}) {
		t.Fatalf("expected F5 not to match the terminal view toggle key")
	}
	if isTerminalViewToggleKey(vaxis.Key{Text: "`", Keycode: '`'}) {
		t.Fatalf("expected bare ` not to match the terminal view toggle key")
	}
}

func TestHostTerminalTabOpenKeyMatchesOptionShiftT(t *testing.T) {
	t.Parallel()

	if !isHostTerminalTabOpenKey(vaxis.Key{Text: "T", Keycode: 't', ShiftedCode: 'T', Modifiers: vaxis.ModAlt | vaxis.ModShift}) {
		t.Fatalf("expected Option-as-Alt+Shift+t to open a host terminal tab")
	}
	if !isHostTerminalTabOpenKey(vaxis.Key{Text: "T", Keycode: 't', ShiftedCode: 'T', Modifiers: vaxis.ModMeta | vaxis.ModShift}) {
		t.Fatalf("expected Option-as-Meta+Shift+t to open a host terminal tab")
	}
	if !isHostTerminalTabOpenKey(vaxis.Key{Text: "ˇ", Keycode: 'ˇ'}) {
		t.Fatalf("expected macOS Option+Shift+t text to open a host terminal tab")
	}
	if isHostTerminalTabOpenKey(vaxis.Key{Text: "t", Keycode: 't', Modifiers: vaxis.ModAlt}) {
		t.Fatalf("expected Option+t without Shift to remain the guest terminal shortcut")
	}
	if isHostTerminalTabOpenKey(vaxis.Key{Text: "T", Keycode: 't', ShiftedCode: 'T', Modifiers: vaxis.ModShift}) {
		t.Fatalf("expected Shift+t without Option not to open a host terminal tab")
	}
	if isHostTerminalTabOpenKey(vaxis.Key{Text: "~", Keycode: '`', ShiftedCode: '~', Modifiers: vaxis.ModAlt | vaxis.ModShift}) {
		t.Fatalf("expected Option+Shift+` not to open a host terminal tab")
	}
}

func TestTUIActionHotkeysIgnoreModifiedKeys(t *testing.T) {
	t.Parallel()

	app := &vaxisTUIApp{
		state: &tuiState{
			entries: []tuiTreeEntry{{
				node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
			}},
			selection: 0,
		},
	}

	if _, ok := app.matchAction(vaxis.Key{Text: "d", Keycode: 'd', Modifiers: vaxis.ModAlt}); ok {
		t.Fatalf("expected Alt+d not to match the plain delete action")
	}
	action, ok := app.matchAction(vaxis.Key{Text: "d", Keycode: 'd'})
	if !ok || action.ID != tuiActionNodeDelete {
		t.Fatalf("expected bare d to match delete action, got %#v ok=%v", action, ok)
	}
}

func TestTerminalTabKeysMatchOptionShortcuts(t *testing.T) {
	t.Parallel()

	if !isTerminalTabOpenKey(vaxis.Key{Text: "t", Keycode: 't', Modifiers: vaxis.ModAlt}) {
		t.Fatalf("expected Option-as-Alt+t to open a terminal tab")
	}
	if !isTerminalTabOpenKey(vaxis.Key{Text: "t", Keycode: 't', Modifiers: vaxis.ModMeta}) {
		t.Fatalf("expected Option-as-Meta+t to open a terminal tab")
	}
	if !isTerminalTabOpenKey(vaxis.Key{Text: "†", Keycode: '†'}) {
		t.Fatalf("expected macOS Option+t text to open a terminal tab")
	}
	if !isTerminalTabNextKey(vaxis.Key{Keycode: vaxis.KeyRight, Modifiers: vaxis.ModMeta}) {
		t.Fatalf("expected Meta+Right to switch to the next terminal tab")
	}
	if !isTerminalTabNextKey(vaxis.Key{Text: "f", Keycode: 'f', Modifiers: vaxis.ModAlt}) {
		t.Fatalf("expected Option+Right encoded as Alt+f to switch to the next terminal tab")
	}
	if !isTerminalTabPreviousKey(vaxis.Key{Keycode: vaxis.KeyLeft, Modifiers: vaxis.ModMeta}) {
		t.Fatalf("expected Meta+Left to switch to the previous terminal tab")
	}
	if !isTerminalTabPreviousKey(vaxis.Key{Text: "b", Keycode: 'b', Modifiers: vaxis.ModAlt}) {
		t.Fatalf("expected Option+Left encoded as Alt+b to switch to the previous terminal tab")
	}
	if !isTerminalTabCloseKey(vaxis.Key{Text: "w", Keycode: 'w', Modifiers: vaxis.ModMeta}) {
		t.Fatalf("expected Meta+w to close the active terminal tab")
	}
	if !isTerminalTabCloseKey(vaxis.Key{Text: "∑", Keycode: '∑'}) {
		t.Fatalf("expected macOS Option+w text to close the active terminal tab")
	}
	if isTerminalTabOpenKey(vaxis.Key{Text: "∑", Keycode: '∑'}) {
		t.Fatalf("expected unrelated macOS Option text not to match terminal tab open")
	}
}

func TestTerminalTabKeysRejectRemovedFallbackShortcuts(t *testing.T) {
	t.Parallel()

	if isTerminalTabOpenKey(vaxis.Key{Keycode: vaxis.KeyF07}) {
		t.Fatalf("expected F7 not to open a terminal tab")
	}
	if isTerminalTabOpenKey(vaxis.Key{Text: "t", Keycode: 't'}) {
		t.Fatalf("expected bare t not to open a terminal tab")
	}
	if isTerminalTabNextKey(vaxis.Key{Keycode: vaxis.KeyF08}) {
		t.Fatalf("expected F8 not to switch terminal tabs")
	}
	if isTerminalTabPreviousKey(vaxis.Key{Keycode: vaxis.KeyF08, Modifiers: vaxis.ModShift}) {
		t.Fatalf("expected Shift+F8 not to switch terminal tabs")
	}
	if isTerminalTabCloseKey(vaxis.Key{Keycode: vaxis.KeyF09}) {
		t.Fatalf("expected F9 not to close terminal tabs")
	}
	if isTerminalTabCloseKey(vaxis.Key{Text: "w", Keycode: 'w'}) {
		t.Fatalf("expected bare w not to close terminal tabs")
	}
}

func TestTerminalTabKeysMatchDecodedOptionInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		match func(vaxis.Key) bool
	}{
		{name: "escape prefixed option t opens tab", input: "\x1bt", match: isTerminalTabOpenKey},
		{name: "macos option t text opens tab", input: "†", match: isTerminalTabOpenKey},
		{name: "escape prefixed option shift t opens host tab", input: "\x1bT", match: isHostTerminalTabOpenKey},
		{name: "macos option shift t text opens host tab", input: "ˇ", match: isHostTerminalTabOpenKey},
		{name: "escape prefixed option right switches next tab", input: "\x1bf", match: isTerminalTabNextKey},
		{name: "escape prefixed option left switches previous tab", input: "\x1bb", match: isTerminalTabPreviousKey},
		{name: "escape prefixed option w closes tab", input: "\x1bw", match: isTerminalTabCloseKey},
		{name: "macos option w text closes tab", input: "∑", match: isTerminalTabCloseKey},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := decodedVaxisInputKey(t, test.input)
			if !test.match(key) {
				t.Fatalf("expected decoded input %q to match shortcut, got %#v", test.input, key)
			}
		})
	}
}

func TestTUITerminalTabKeybindOpensTabFromDecodedOpenShortcuts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "escape prefixed option t", input: "\x1bt"},
		{name: "macos option t text", input: "†"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			service, _ := newTestService(t)
			sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
			sessionManager := newSharedFakeTUISessionManager(sessions)
			state, err := newTUIState(testTUINodes(t), sessionManager)
			if err != nil {
				t.Fatalf("newTUIState() error = %v", err)
			}
			app := &vaxisTUIApp{
				ctx:      ctx,
				service:  service,
				state:    state,
				sessions: sessions,
			}

			key := decodedVaxisInputKey(t, test.input)
			quit := app.handleKey(key)
			if quit {
				t.Fatalf("expected decoded %s to keep app running", test.name)
			}
			if got := app.state.activeTerminalTargetKey(); got != nodeTargetKey("node-root") {
				t.Fatalf("expected decoded %s to target the root node, got %q", test.name, got)
			}
			if got := strings.Join(sessions.TargetSessionKeys(nodeTargetKey("node-root")), ","); got != nodeTargetKey("node-root")+"#1" {
				t.Fatalf("expected decoded %s to open the root node terminal tab, got %q", test.name, got)
			}
			if app.state.focus != tuiFocusTree {
				t.Fatalf("expected decoded %s to keep tree focus after opening root tab, got %q", test.name, app.state.focus)
			}
			if app.state.treePaneMode != tuiTreePaneModeTerminal {
				t.Fatalf("expected decoded %s to show terminal pane after opening root tab, got %q", test.name, app.state.treePaneMode)
			}

			selectTUIEntry(t, app, nodeTargetKey("node-child"))
			quit = app.handleKey(key)
			if quit {
				t.Fatalf("expected decoded %s child to keep app running", test.name)
			}
			if got := app.state.activeTerminalTargetKey(); got != nodeTargetKey("node-child") {
				t.Fatalf("expected decoded %s to target the child node, got %q", test.name, got)
			}
			if got := strings.Join(sessions.TargetSessionKeys(nodeTargetKey("node-child")), ","); got != nodeTargetKey("node-child")+"#1" {
				t.Fatalf("expected decoded %s to open the child node terminal tab, got %q", test.name, got)
			}
			if app.state.focus != tuiFocusTree {
				t.Fatalf("expected decoded %s to keep tree focus after opening child tab, got %q", test.name, app.state.focus)
			}
		})
	}
}

func TestTUIStateSelectionNeverOpensTerminalTabs(t *testing.T) {
	t.Parallel()

	tree := testTUINodes(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(tree, sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	if got := state.selectedEntry().node.Slug; got != "root-node" {
		t.Fatalf("expected initial selection to be root-node, got %q", got)
	}
	if got := state.activeTerminalTargetKey(); got != nodeTargetKey("node-root") {
		t.Fatalf("expected initial terminal target to follow selection, got %q", got)
	}

	state.toggleTreePaneMode()
	if state.treePaneMode != tuiTreePaneModeInfo {
		t.Fatalf("expected info pane mode after toggling from the running-node default, got %q", state.treePaneMode)
	}

	// Visit every entry after an explicit pane-mode choice; nothing may open a
	// session or recompute the sticky mode from the selected entry.
	for _, delta := range []int{1, -1, 1, -1} {
		if err := state.moveSelection(delta); err != nil {
			t.Fatalf("moveSelection(%d) error = %v", delta, err)
		}
	}
	if len(sessions.opened) != 0 {
		t.Fatalf("expected visiting tree entries to open no terminal tabs, got %v", sessions.opened)
	}

	// Explicitly focusing the terminal opens the first tab and reuses it.
	if err := state.focusTerminal(); err != nil {
		t.Fatalf("focusTerminal() error = %v", err)
	}
	if state.focus != tuiFocusTerminal {
		t.Fatalf("expected terminal focus, got %q", state.focus)
	}
	if sessions.opened[nodeTargetKey("node-root")] != 1 {
		t.Fatalf("expected focusing the terminal to open one root tab, got %d", sessions.opened[nodeTargetKey("node-root")])
	}

	state.focusTree()
	if state.focus != tuiFocusTree {
		t.Fatalf("expected tree focus, got %q", state.focus)
	}
	if err := state.focusTerminal(); err != nil {
		t.Fatalf("focusTerminal() reuse error = %v", err)
	}
	if sessions.opened[nodeTargetKey("node-root")] != 1 {
		t.Fatalf("expected refocusing to reuse the root tab, got %d opens", sessions.opened[nodeTargetKey("node-root")])
	}
}

func TestTUIStateOpenInitialTerminalTab(t *testing.T) {
	t.Parallel()

	tree := testTUINodes(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(tree, sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	if err := state.openInitialTerminalTab(); err != nil {
		t.Fatalf("openInitialTerminalTab() error = %v", err)
	}

	targetKey := nodeTargetKey("node-root")
	if got := sessions.opened[targetKey]; got != 1 {
		t.Fatalf("expected startup to open one default root tab, got %d", got)
	}
	if got := state.activeSessionKey(); got != targetKey+"#1" {
		t.Fatalf("expected startup tab to become active, got %q", got)
	}
	if state.focus != tuiFocusTree {
		t.Fatalf("expected startup to keep tree focus, got %q", state.focus)
	}
	if state.treePaneMode != tuiTreePaneModeTerminal {
		t.Fatalf("expected a running node to default to terminal pane mode, got %q", state.treePaneMode)
	}

	if err := state.openInitialTerminalTab(); err != nil {
		t.Fatalf("openInitialTerminalTab() reuse error = %v", err)
	}
	if got := sessions.opened[targetKey]; got != 1 {
		t.Fatalf("expected startup default tab to be created once, got %d opens", got)
	}
}

func TestTUIStateDefaultsStoppedNodeToInfoWithoutOpeningTerminal(t *testing.T) {
	t.Parallel()

	nodes := []Node{{
		ID:     "node-stopped",
		Slug:   "stopped-node",
		Status: NodeStatusStopped,
	}}
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(nodes, sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	if state.treePaneMode != tuiTreePaneModeInfo {
		t.Fatalf("expected a stopped node to default to info pane mode, got %q", state.treePaneMode)
	}
	if err := state.openInitialTerminalTab(); err != nil {
		t.Fatalf("openInitialTerminalTab() error = %v", err)
	}
	if len(sessions.opened) != 0 {
		t.Fatalf("expected a stopped node to open no terminal tabs, got %v", sessions.opened)
	}
}

func TestTUISessionStoreUsesPreferredTerminalSizeForNewSessions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	sessions.SetPreferredTerminalSize(79, 20)

	var terminal *fakeTUITerminal
	sessions.newTerminal = func(string, func(vaxis.Event)) tuiTerminal {
		terminal = newFakeTUITerminal()
		return terminal
	}

	if _, err := sessions.OpenNodeTab(Node{ID: "node-root", Slug: "root-node"}); err != nil {
		t.Fatalf("OpenNodeTab() error = %v", err)
	}
	if terminal == nil {
		t.Fatal("expected OpenNodeTab() to allocate a terminal")
	}
	if terminal.startCols != 79 || terminal.startRows != 20 {
		t.Fatalf("expected session terminal to start at 79x20, got %dx%d", terminal.startCols, terminal.startRows)
	}
}

func TestResolveCodelimaExecutablePathResolvesBuildCompatibilitySymlink(t *testing.T) {
	root := t.TempDir()
	platformBinDir := filepath.Join(root, "bin", "linux-aarch64")
	if err := os.MkdirAll(platformBinDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	platformBinary := filepath.Join(platformBinDir, "codelima")
	if err := os.WriteFile(platformBinary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	compatBinary := filepath.Join(root, "bin", "codelima")
	if err := os.Symlink(filepath.Join("linux-aarch64", "codelima"), compatBinary); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	expectedBinary, err := filepath.EvalSymlinks(platformBinary)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}

	if got := resolveCodelimaExecutablePath(compatBinary); got != expectedBinary {
		t.Fatalf("expected compatibility symlink to resolve to %q, got %q", expectedBinary, got)
	}
}

func TestTUISessionStoreStartsNodeShellWithServiceResolvedExecutable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})

	// The codelima executable for a node shell is now resolved by the Service
	// (os.Executable + resolveCodelimaExecutablePath), not cached on the store.
	selfExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	wantExe := resolveCodelimaExecutablePath(selfExe)

	var terminal *fakeTUITerminal
	sessions.newTerminal = func(string, func(vaxis.Event)) tuiTerminal {
		terminal = newFakeTUITerminal()
		return terminal
	}

	if _, err := sessions.OpenNodeTab(Node{ID: "node-root", Slug: "root-node"}); err != nil {
		t.Fatalf("OpenNodeTab() error = %v", err)
	}
	if terminal == nil {
		t.Fatal("expected OpenNodeTab() to allocate a terminal")
	}
	if terminal.startCmd == nil {
		t.Fatal("expected OpenNodeTab() to start the terminal command")
	}

	wantArgs := []string{
		wantExe,
		"--home",
		service.cfg.MetadataRoot,
		"shell",
		"node-root",
	}
	if !reflect.DeepEqual(terminal.startCmd.Args, wantArgs) {
		t.Fatalf("expected node shell command %#v, got %#v", wantArgs, terminal.startCmd.Args)
	}
	if terminal.startCmd.Path != wantExe {
		t.Fatalf("expected node shell executable %q, got %q", wantExe, terminal.startCmd.Path)
	}
}

func TestTUISessionStoreExplainsNodeShellExecFormatError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})

	sessions.newTerminal = func(string, func(vaxis.Event)) tuiTerminal {
		terminal := newFakeTUITerminal()
		terminal.startErr = &os.PathError{
			Op:   "fork/exec",
			Path: filepath.Join(t.TempDir(), "bin", "linux-aarch64", "codelima"),
			Err:  syscall.ENOEXEC,
		}
		return terminal
	}

	_, err := sessions.OpenNodeTab(Node{ID: "node-root", Slug: "root-node"})
	if err == nil {
		t.Fatal("expected OpenNodeTab() error")
	}
	for _, want := range []string{"not compatible with this platform", "run make build on this platform", "restart codelima"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error %q to contain %q", err.Error(), want)
		}
	}
}

func TestTUISessionStoreStartsNodeHostShellInDirectory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	sessions.SetPreferredTerminalSize(91, 27)

	var fakeTerm *fakeTUITerminal
	sessions.newTerminal = func(string, func(vaxis.Event)) tuiTerminal {
		fakeTerm = newFakeTUITerminal()
		return fakeTerm
	}

	node := Node{ID: "node-root", Slug: "root-node", DirectoryPath: workspace}
	if _, err := sessions.OpenNodeHostTab(node); err != nil {
		t.Fatalf("OpenNodeHostTab() error = %v", err)
	}
	if fakeTerm == nil {
		t.Fatal("expected OpenNodeHostTab() to allocate a terminal")
	}
	if fakeTerm.startCmd == nil {
		t.Fatal("expected OpenNodeHostTab() to start the terminal command")
	}
	if fakeTerm.startCmd.Dir != workspace {
		t.Fatalf("expected host shell dir %q, got %q", workspace, fakeTerm.startCmd.Dir)
	}
	if got, want := strings.Join(fakeTerm.startCmd.Args, " "), strings.Join(interactiveShellLaunchCommand(), " "); got != want {
		t.Fatalf("expected host shell command %q, got %q", want, got)
	}
	if fakeTerm.startCols != 91 || fakeTerm.startRows != 27 {
		t.Fatalf("expected host terminal to start at 91x27, got %dx%d", fakeTerm.startCols, fakeTerm.startRows)
	}
}

func TestTUIDrawUpdatesPreferredTerminalSizeForFutureSessions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	state, err := newTUIState(testTUINodes(t), nil)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	vx := newRenderTestVaxis(t, 100, 24)
	defer vx.Close()

	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: sessions,
		vx:       vx,
	}

	app.draw()

	wantCols, wantRows := tuiEmbeddedTerminalSize(100, 24, tuiFocusTree)
	if sessions.preferredCols != wantCols || sessions.preferredRows != wantRows {
		t.Fatalf("expected draw() to record terminal size %dx%d, got %dx%d", wantCols, wantRows, sessions.preferredCols, sessions.preferredRows)
	}
}

func TestTUIHandleResizeResizesActiveTerminalBeforeRedraw(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	if err := state.focusTerminal(); err != nil {
		t.Fatalf("focusTerminal() error = %v", err)
	}

	terminal := targetTestTerminal(t, sessions, nodeTargetKey("node-root"))

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: sessions,
	}
	app.handleResize(vaxis.Resize{Cols: 120, Rows: 30})

	wantCols, wantRows := tuiEmbeddedTerminalSize(120, 30, tuiFocusTerminal)
	if terminal.startCols != wantCols || terminal.startRows != wantRows {
		t.Fatalf("expected active terminal resize to %dx%d, got %dx%d", wantCols, wantRows, terminal.startCols, terminal.startRows)
	}
}

func TestVaxisTUITerminalPreservesInitialOutputWhenStartedAtPaneSize(t *testing.T) {
	t.Parallel()

	terminal := newTUIVaxisTerminal("node-root", func(vaxis.Event) {})
	terminal.Resize(12, 3)

	cmd := exec.Command("sh", "-lc", "printf prompt; sleep 1")
	if err := terminal.Start(cmd); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer terminal.Close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(terminal.String(), "prompt") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(terminal.String(), "prompt") {
		t.Fatalf("expected terminal buffer to contain initial output, got %q", terminal.String())
	}

	vx := newRenderTestVaxis(t, 12, 3)
	defer vx.Close()

	terminal.Draw(vx.Window())

	rendered := renderedScreenText(t, vx, 12, 3)
	if !strings.Contains(rendered, "prompt") {
		t.Fatalf("expected rendered terminal to preserve initial output, got:\n%s", rendered)
	}
}

func TestTUIRefreshTickReloadsNodes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)

	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())
	if len(app.state.entries) != 0 {
		t.Fatalf("expected empty initial node list, got %d entries", len(app.state.entries))
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Slug:      "root-node",
		Directory: workspace,
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	quit, err := app.handleEvent(tuiRefreshTickEvent{})
	if err != nil {
		t.Fatalf("handleEvent(refresh tick) error = %v", err)
	}
	if quit {
		t.Fatalf("expected refresh tick to keep app running")
	}
	if app.refreshInFlight {
		t.Fatalf("expected synchronous test refresh to finish")
	}
	if index := app.state.findEntryByKey(nodeTargetKey(node.ID)); index < 0 {
		t.Fatalf("expected refresh tick to add node entry to the list")
	}
}

func TestTUIRefreshTickPreservesDirectoryRootScope(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	scopeRoot := filepath.Join(t.TempDir(), "scope")
	scopedDirectory := filepath.Join(scopeRoot, "scoped")
	writeFile(t, filepath.Join(scopedDirectory, "README.md"), "scoped\n")

	outsideDirectory := filepath.Join(t.TempDir(), "outside")
	writeFile(t, filepath.Join(outsideDirectory, "README.md"), "outside\n")
	outsideNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Slug:      "outside-node",
		Directory: outsideDirectory,
	})
	if err != nil {
		t.Fatalf("NodeCreate(outside-node) error = %v", err)
	}

	nodes, err := service.NodeListByDirectoryRoot(ctx, scopeRoot, false)
	if err != nil {
		t.Fatalf("NodeListByDirectoryRoot() error = %v", err)
	}
	sessions := newTUISessionStore(ctx, service, nil)
	defer sessions.Close()
	state, err := newTUIState(nodes, newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	app := &vaxisTUIApp{
		ctx:               ctx,
		service:           service,
		treeWorkspaceRoot: scopeRoot,
		state:             state,
		sessions:          sessions,
	}
	if index := app.state.findEntryByKey(nodeTargetKey(outsideNode.ID)); index >= 0 {
		t.Fatalf("expected outside node to be hidden, found at %d", index)
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Slug:      "scoped-node",
		Directory: scopedDirectory,
	})
	if err != nil {
		t.Fatalf("NodeCreate(scoped-node) error = %v", err)
	}

	quit, err := app.handleEvent(tuiRefreshTickEvent{})
	if err != nil {
		t.Fatalf("handleEvent(refresh tick) error = %v", err)
	}
	if quit {
		t.Fatalf("expected refresh tick to keep app running")
	}
	if index := app.state.findEntryByKey(nodeTargetKey(node.ID)); index < 0 {
		t.Fatalf("expected scoped refresh to add node entry")
	}
	if index := app.state.findEntryByKey(nodeTargetKey(outsideNode.ID)); index >= 0 {
		t.Fatalf("expected scoped refresh to keep outside node hidden, found at %d", index)
	}
}

func TestTUIStateSelectsCreatedNodeWithoutOpeningShellSession(t *testing.T) {
	t.Parallel()

	nodes := testTUINodesWithCreatedNode(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(nodes, sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	if err := state.moveSelection(1); err != nil {
		t.Fatalf("moveSelection(created node) error = %v", err)
	}

	if got := state.selectedEntry().node.Slug; got != "created-node" {
		t.Fatalf("expected selection to move to created-node, got %q", got)
	}

	if got := state.activeTerminalTargetKey(); got != nodeTargetKey("node-created") {
		t.Fatalf("expected created node to become the selected terminal target, got %q", got)
	}

	if sessions.opened[nodeTargetKey("node-created")] != 0 {
		t.Fatalf("expected created node not to open a shell session, got %d creations", sessions.opened[nodeTargetKey("node-created")])
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
	state, err := newTUIState(testTUINodes(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: newTUISessionStore(ctx, service, func(vaxis.Event) {}),
	}
	putTestSession(app.sessions, nodeTargetKey("node-root"), &tuiSession{
		node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
	}, newFakeTUITerminal())

	quit := app.handleKey(vaxis.Key{Text: "`", Keycode: '`', Modifiers: vaxis.ModAlt})
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
	state, err := newTUIState(testTUINodes(t), sessions)
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
	putTestSession(app.sessions, nodeTargetKey("node-root"), &tuiSession{
		node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
	}, newFakeTUITerminal())

	quit := app.handleKey(vaxis.Key{Text: "`", Keycode: '`', Modifiers: vaxis.ModAlt})
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

func TestTUIHandleKeyF6TogglesFocusToTerminalAndHidesTree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	state.treePaneMode = tuiTreePaneModeTerminal

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: sessions,
	}
	putTestSession(app.sessions, nodeTargetKey("node-root"), &tuiSession{
		node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
	}, newFakeTUITerminal())

	quit := app.handleKey(vaxis.Key{Keycode: vaxis.KeyF06})
	if quit {
		t.Fatalf("expected F6 to toggle terminal view, not quit")
	}
	if app.state.focus != tuiFocusTerminal {
		t.Fatalf("expected F6 to focus the terminal, got %q", app.state.focus)
	}
	if layout := layoutTUIBody(120, app.state.focus); layout.treeVisible {
		t.Fatalf("expected terminal focus to hide the tree")
	}
}

func TestTUIHandleKeyF6TogglesFocusBackToTreeAndShowsTree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUINodes(t), sessions)
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
	putTestSession(app.sessions, nodeTargetKey("node-root"), &tuiSession{
		node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
	}, newFakeTUITerminal())

	quit := app.handleKey(vaxis.Key{Keycode: vaxis.KeyF06})
	if quit {
		t.Fatalf("expected F6 to toggle terminal view, not quit")
	}
	if app.state.focus != tuiFocusTree {
		t.Fatalf("expected F6 to return focus to the tree, got %q", app.state.focus)
	}
	if layout := layoutTUIBody(120, app.state.focus); !layout.treeVisible {
		t.Fatalf("expected tree focus to show the tree")
	}
}

func TestTUIHandleKeyOptionShiftTOpensFreshHostTerminalTabs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	sessionManager := newSharedFakeTUISessionManager(sessions)
	state, err := newTUIState(testTUINodes(t), sessionManager)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: sessions,
	}

	hostKey := vaxis.Key{Text: "T", Keycode: 't', ShiftedCode: 'T', Modifiers: vaxis.ModAlt | vaxis.ModShift}
	quit := app.handleKey(hostKey)
	if quit {
		t.Fatalf("expected Option+Shift+t to open a host tab, not quit")
	}
	if app.state.focus != tuiFocusTree {
		t.Fatalf("expected Option+Shift+t to preserve tree focus like Option+t, got %q", app.state.focus)
	}
	if app.state.treePaneMode != tuiTreePaneModeTerminal {
		t.Fatalf("expected Option+Shift+t to show the terminal pane, got %q", app.state.treePaneMode)
	}
	if app.state.selectedEntry().key() != nodeTargetKey("node-root") {
		t.Fatalf("expected host toggle to keep the node selected, got %q", app.state.selectedEntry().key())
	}
	if got := app.state.activeTerminalTargetKey(); got != nodeTargetKey("node-root") {
		t.Fatalf("expected host shell to remain attached to the node target, got %q", got)
	}
	if sessionManager.opened[nodeTargetKey("node-root")] != 1 {
		t.Fatalf("expected one host node tab without an implicit guest tab, got %d", sessionManager.opened[nodeTargetKey("node-root")])
	}
	firstHostSession := app.state.activeSessionKey()
	if session, ok := sessions.Session(firstHostSession); !ok || session.shellKind != terminal.NodeHostShell {
		t.Fatalf("expected active session %q to be a host tab, got %#v", firstHostSession, session)
	}

	quit = app.handleKey(hostKey)
	if quit {
		t.Fatalf("expected Option+Shift+t to open another host tab, not quit")
	}
	if sessionManager.opened[nodeTargetKey("node-root")] != 2 {
		t.Fatalf("expected repeated Option+Shift+t to open a fresh host tab, got %d tabs", sessionManager.opened[nodeTargetKey("node-root")])
	}
	if got := app.state.activeSessionKey(); got == firstHostSession {
		t.Fatalf("expected the second host tab to become active, still on %q", got)
	}
}

func TestTUIRefreshPreservesActiveHostTerminalTab(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	sessionManager := newSharedFakeTUISessionManager(sessions)
	state, err := newTUIState(testTUINodes(t), sessionManager)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: sessions,
	}

	hostKey := vaxis.Key{Text: "T", Keycode: 't', ShiftedCode: 'T', Modifiers: vaxis.ModAlt | vaxis.ModShift}
	app.handleKey(hostKey)
	hostSession := app.state.activeSessionKey()
	if got := app.state.activeTerminalTargetKey(); got != nodeTargetKey("node-root") {
		t.Fatalf("expected host terminal to use the node target before refresh, got %q", got)
	}

	if err := app.applyReloadedNodes(testTUINodes(t), ""); err != nil {
		t.Fatalf("applyReloadedNodes() error = %v", err)
	}
	if got := app.state.selectedEntry().key(); got != nodeTargetKey("node-root") {
		t.Fatalf("expected refresh to preserve selected node, got %q", got)
	}
	if got := app.state.activeSessionKey(); got != hostSession {
		t.Fatalf("expected refresh to preserve active host session %q, got %q", hostSession, got)
	}
}

func TestTUIRefreshPreservesDaemonTabsOutsideEachProcessScope(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	nodes := []Node{
		{ID: "node-window-one", Slug: "window-one", SandboxName: "window-one", DirectoryPath: filepath.Join(workspace, "one"), Status: NodeStatusRunning},
		{ID: "node-window-two", Slug: "window-two", SandboxName: "window-two", DirectoryPath: filepath.Join(workspace, "two"), Status: NodeStatusRunning},
	}
	for _, node := range nodes {
		if err := service.store.SaveNode(node, BootstrapState{}); err != nil {
			t.Fatalf("SaveNode(%q) error = %v", node.ID, err)
		}
	}

	for windowIndex, visibleNode := range nodes {
		t.Run(visibleNode.Slug, func(t *testing.T) {
			store := newTUISessionStore(ctx, service, func(vaxis.Event) {})
			state, err := newTUIState([]Node{visibleNode}, newSharedFakeTUISessionManager(store))
			if err != nil {
				t.Fatalf("newTUIState() error = %v", err)
			}
			app := &vaxisTUIApp{ctx: ctx, service: service, state: state, sessions: store}

			terminals := make([]*fakeTUITerminal, 0, 4)
			for _, node := range nodes {
				for range 2 {
					fake := newFakeTUITerminal()
					putTestSession(store, nodeTargetKey(node.ID), &tuiSession{node: node}, fake)
					terminals = append(terminals, fake)
				}
			}

			if err := app.applyReloadedNodes([]Node{nodes[windowIndex]}, ""); err != nil {
				t.Fatalf("applyReloadedNodes() error = %v", err)
			}
			for _, node := range nodes {
				if got := len(store.TargetSessionKeys(nodeTargetKey(node.ID))); got != 2 {
					t.Fatalf("tabs for %s after scoped refresh = %d, want 2", node.Slug, got)
				}
			}
			for index, terminal := range terminals {
				if terminal.closeCalls != 0 {
					t.Fatalf("terminal %d Close() calls = %d, want 0", index, terminal.closeCalls)
				}
			}
		})
	}
}

func TestTUIRefreshClosesTabsForConfirmedDeletedNode(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	deletedAt := time.Now().UTC()
	node := Node{
		ID:            "node-deleted-in-another-process",
		Slug:          "deleted-in-another-process",
		SandboxName:   "deleted-in-another-process",
		DirectoryPath: workspace,
		Status:        NodeStatusTerminated,
		DeletedAt:     &deletedAt,
	}
	if err := service.store.SaveNode(node, BootstrapState{}); err != nil {
		t.Fatalf("SaveNode() error = %v", err)
	}

	store := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(nil, newSharedFakeTUISessionManager(store))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	app := &vaxisTUIApp{ctx: ctx, service: service, state: state, sessions: store}
	fake := newFakeTUITerminal()
	key := putTestSession(store, nodeTargetKey(node.ID), &tuiSession{node: node}, fake)

	if err := app.applyReloadedNodes(nil, ""); err != nil {
		t.Fatalf("applyReloadedNodes() error = %v", err)
	}
	if store.HasSession(key) {
		t.Fatalf("confirmed deleted node session %q was retained", key)
	}
	if fake.closeCalls != 1 {
		t.Fatalf("terminal Close() calls = %d, want 1", fake.closeCalls)
	}
}

func TestTUIHostTerminalTabUsesNormalSwitchAndCloseBehavior(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	if err := state.focusTerminal(); err != nil {
		t.Fatalf("focusTerminal() error = %v", err)
	}
	guestSession := state.activeSessionKey()
	app := &vaxisTUIApp{ctx: ctx, service: service, state: state, sessions: sessions}

	hostKey := vaxis.Key{Text: "T", Keycode: 't', ShiftedCode: 'T', Modifiers: vaxis.ModAlt | vaxis.ModShift}
	app.handleKey(hostKey)
	hostSession := state.activeSessionKey()
	if hostSession == guestSession || !app.hostTerminalTabActive() {
		t.Fatalf("expected a distinct active host tab, guest=%q host=%q", guestSession, hostSession)
	}

	app.handleKey(vaxis.Key{Keycode: vaxis.KeyLeft, Modifiers: vaxis.ModAlt})
	if got := state.activeSessionKey(); got != guestSession || app.hostTerminalTabActive() {
		t.Fatalf("expected normal tab switching to activate guest %q, got %q", guestSession, got)
	}

	app.handleKey(vaxis.Key{Keycode: vaxis.KeyRight, Modifiers: vaxis.ModAlt})
	if got := state.activeSessionKey(); got != hostSession || !app.hostTerminalTabActive() {
		t.Fatalf("expected normal tab switching to reactivate host %q, got %q", hostSession, got)
	}

	app.handleKey(vaxis.Key{Text: "w", Keycode: 'w', Modifiers: vaxis.ModAlt})
	if got := state.activeSessionKey(); got != guestSession || app.hostTerminalTabActive() {
		t.Fatalf("expected closing host tab to activate guest %q, got %q", guestSession, got)
	}
}

func TestTUIDrawActiveHostTerminalTabRendersRedHeader(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	vx := newRenderTestVaxis(t, 100, 24)
	defer vx.Close()
	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: sessions,
		vx:       vx,
	}

	hostKey := vaxis.Key{Text: "T", Keycode: 't', ShiftedCode: 'T', Modifiers: vaxis.ModAlt | vaxis.ModShift}
	app.handleKey(hostKey)
	app.draw()

	rendered := renderedScreenText(t, vx, 100, 24)
	if strings.Contains(rendered, "HOST TERMINAL") {
		t.Fatalf("expected host-terminal indicator to use the existing header, got:\n%s", rendered)
	}
	if got := renderedCellStyle(t, vx, 0, 0).Foreground; got != vaxis.ColorRed {
		t.Fatalf("expected header to be red while a host terminal tab is active, got %#v", got)
	}
	if got := app.terminalBodyRect.row; got != 2 {
		t.Fatalf("expected terminal body to start below the normal top bar, got row %d", got)
	}
}

func TestTUIForwardTerminalEventPreservesPastedNewlines(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
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
		sessions: sessions,
	}
	targetKey := nodeTargetKey("node-root")
	terminal := targetTestTerminal(t, sessions, targetKey)
	beforeSessions := len(sessions.TargetSessionKeys(targetKey))
	if quit := app.handleKey(vaxis.Key{Text: "†", Keycode: '†', EventType: vaxis.EventPaste}); quit {
		t.Fatalf("handleKey(pasted Option+t glyph) = (%v, %v), want (false, nil)", quit, err)
	}
	if got := len(sessions.TargetSessionKeys(targetKey)); got != beforeSessions {
		t.Fatalf("pasted text triggered a terminal-tab keybinding: session count = %d, want %d", got, beforeSessions)
	}
	if len(terminal.events) != 1 {
		t.Fatalf("pasted Option+t glyph forwarded %d events, want one", len(terminal.events))
	}
	terminal.events = nil

	app.forwardTerminalEvent(vaxis.Key{
		Text:      "one\ntwo\r\nthree",
		Keycode:   '\n',
		EventType: vaxis.EventPaste,
	})

	if len(terminal.events) != 1 {
		t.Fatalf("expected pasted key to be forwarded once, got %d events", len(terminal.events))
	}
	key, ok := terminal.events[0].(vaxis.Key)
	if !ok {
		t.Fatalf("expected forwarded event to remain a key, got %T", terminal.events[0])
	}
	if key.Text != "one\ntwo\nthree" {
		t.Fatalf("expected pasted newlines to remain line feeds, got %q", key.Text)
	}
}

func TestTUIHandleTerminalKeyWaitsForFreshSnapshotBeforeRedraw(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	if err := state.focusTerminal(); err != nil {
		t.Fatalf("focusTerminal() error = %v", err)
	}

	vx := newRenderTestVaxis(t, 100, 24)
	defer vx.Close()
	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: sessions,
		vx:       vx,
	}
	terminal := targetTestTerminal(t, sessions, nodeTargetKey("node-root"))

	quit, err := app.handleEvent(vaxis.Key{Text: "a", Keycode: 'a'})
	if err != nil || quit {
		t.Fatalf("handleEvent(terminal key) = (%v, %v), want (false, nil)", quit, err)
	}
	if len(terminal.events) != 1 {
		t.Fatalf("terminal received %d events, want one", len(terminal.events))
	}
	if terminal.drawCalls != 0 {
		t.Fatalf("terminal was redrawn %d times with a stale snapshot, want none", terminal.drawCalls)
	}
}

func TestTUITerminalTabKeybindsOpenSwitchAndCloseTabs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	sessionManager := newSharedFakeTUISessionManager(sessions)
	state, err := newTUIState(testTUINodes(t), sessionManager)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: sessions,
	}

	openKey := vaxis.Key{Text: "t", Keycode: 't', Modifiers: vaxis.ModAlt}
	rootTarget := nodeTargetKey("node-root")
	childTarget := nodeTargetKey("node-child")

	quit := app.handleKey(openKey)
	if quit {
		t.Fatalf("expected Option+t to keep app running")
	}
	if got := app.state.activeSessionKey(); got != rootTarget+"#1" {
		t.Fatalf("expected first root tab to be active, got %q", got)
	}
	if app.state.focus != tuiFocusTree {
		t.Fatalf("expected Option+t to keep tree focus after opening root tab, got %q", app.state.focus)
	}
	if app.state.treePaneMode != tuiTreePaneModeTerminal {
		t.Fatalf("expected Option+t to show the terminal pane after opening root tab, got %q", app.state.treePaneMode)
	}

	// A second open creates a fresh tab for the same node and activates it.
	app.handleKey(openKey)
	if got := strings.Join(sessions.TargetSessionKeys(rootTarget), ","); got != rootTarget+"#1,"+rootTarget+"#2" {
		t.Fatalf("expected two root tabs in order, got %q", got)
	}
	if got := app.state.activeSessionKey(); got != rootTarget+"#2" {
		t.Fatalf("expected second root tab to be active, got %q", got)
	}

	// Option-based switching cycles within the focused node's tabs.
	app.handleKey(vaxis.Key{Keycode: vaxis.KeyLeft, Modifiers: vaxis.ModAlt})
	if got := app.state.activeSessionKey(); got != rootTarget+"#1" {
		t.Fatalf("expected Option+Left to switch to the first root tab, got %q", got)
	}
	app.handleKey(vaxis.Key{Keycode: vaxis.KeyRight, Modifiers: vaxis.ModAlt})
	if got := app.state.activeSessionKey(); got != rootTarget+"#2" {
		t.Fatalf("expected Option+Right to switch back to the second root tab, got %q", got)
	}

	// Tabs are scoped per node: the child node starts with none.
	selectTUIEntry(t, app, childTarget)
	if got := app.state.activeSessionKey(); got != "" {
		t.Fatalf("expected child node to have no active tab, got %q", got)
	}
	app.handleKey(vaxis.Key{Keycode: vaxis.KeyRight, Modifiers: vaxis.ModAlt})
	if app.status == "" {
		t.Fatalf("expected switching tabs without any open tab to report an error status")
	}

	app.handleKey(openKey)
	if got := app.state.activeSessionKey(); got != childTarget+"#1" {
		t.Fatalf("expected child tab to be active, got %q", got)
	}

	// Returning to the root node restores its remembered active tab.
	selectTUIEntry(t, app, rootTarget)
	if got := app.state.activeSessionKey(); got != rootTarget+"#2" {
		t.Fatalf("expected root node to remember its active tab, got %q", got)
	}

	// Close walks through the focused node's tabs without touching others.
	closeKey := vaxis.Key{Text: "w", Keycode: 'w', Modifiers: vaxis.ModAlt}
	app.handleKey(closeKey)
	if got := app.state.activeSessionKey(); got != rootTarget+"#1" {
		t.Fatalf("expected close to activate the remaining root tab, got %q", got)
	}
	app.handleKey(closeKey)
	if got := len(sessions.TargetSessionKeys(rootTarget)); got != 0 {
		t.Fatalf("expected all root tabs to be closed, got %d", got)
	}
	if !sessions.HasSession(childTarget + "#1") {
		t.Fatalf("expected closing root tabs to leave the child tab open")
	}
	if app.state.focus != tuiFocusTree {
		t.Fatalf("expected tree focus after closing tabs, got %q", app.state.focus)
	}
}

func TestNextActiveTerminalTabAfterCloseUsesAdjacentTab(t *testing.T) {
	t.Parallel()

	keys := []string{
		nodeTargetKey("node-root") + "#1",
		nodeTargetKey("node-root") + "#2",
		nodeTargetKey("node-root") + "#3",
	}

	tests := []struct {
		name       string
		keys       []string
		closingKey string
		want       string
	}{
		{name: "first tab", keys: keys, closingKey: keys[0], want: keys[1]},
		{name: "middle tab", keys: keys, closingKey: keys[1], want: keys[2]},
		{name: "last tab", keys: keys, closingKey: keys[2], want: keys[1]},
		{name: "only tab", keys: keys[:1], closingKey: keys[0], want: ""},
		{name: "missing tab", keys: keys, closingKey: nodeTargetKey("node-root") + "#4", want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := nextActiveTerminalTabAfterClose(test.keys, test.closingKey); got != test.want {
				t.Fatalf("nextActiveTerminalTabAfterClose() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTUIFocusedTerminalReceivesModifiedDKeys(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	sessionManager := newSharedFakeTUISessionManager(sessions)
	state, err := newTUIState(testTUINodes(t), sessionManager)
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
		sessions: sessions,
	}
	terminal := targetTestTerminal(t, sessions, nodeTargetKey("node-root"))

	quit := app.handleKey(vaxis.Key{Text: "d", Keycode: 'd', Modifiers: vaxis.ModAlt})
	if quit {
		t.Fatalf("expected Alt+d to keep app running")
	}
	if app.state.focus != tuiFocusTerminal {
		t.Fatalf("expected Alt+d to leave terminal focus active, got %q", app.state.focus)
	}

	quit = app.handleKey(vaxis.Key{Text: "D", Keycode: 'd', ShiftedCode: 'D', Modifiers: vaxis.ModAlt | vaxis.ModShift})
	if quit {
		t.Fatalf("expected Alt+Shift+d to keep app running")
	}

	quit = app.handleKey(vaxis.Key{Text: "t", Keycode: 't'})
	if quit {
		t.Fatalf("expected t to keep app running")
	}

	if len(terminal.events) != 3 {
		t.Fatalf("expected modified d keys and bare t to be forwarded to the terminal, got %d events", len(terminal.events))
	}
	if terminal.startCols != 0 || terminal.startRows != 0 {
		t.Fatalf("expected forwarded keys not to resize terminal, got %dx%d", terminal.startCols, terminal.startRows)
	}
}

func TestTUIMouseClickPreservesActiveHostTerminalTab(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	sessionManager := newSharedFakeTUISessionManager(sessions)
	state, err := newTUIState(testTUINodes(t), sessionManager)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	if err := state.focusTerminal(); err != nil {
		t.Fatalf("focusTerminal() error = %v", err)
	}
	if got := state.activeTerminalTargetKey(); got != nodeTargetKey("node-root") {
		t.Fatalf("expected initial node terminal focus, got %q", got)
	}

	app := &vaxisTUIApp{
		ctx:              ctx,
		service:          service,
		state:            state,
		sessions:         sessions,
		terminalBodyRect: tuiRect{col: 10, row: 5, width: 40, height: 10},
		status:           "stale status",
	}

	hostKey := vaxis.Key{Text: "T", Keycode: 't', ShiftedCode: 'T', Modifiers: vaxis.ModAlt | vaxis.ModShift}
	quit := app.handleKey(hostKey)
	if quit {
		t.Fatalf("expected Option+Shift+t to open a host tab, not quit")
	}
	hostSession := app.state.activeSessionKey()
	if got := app.state.activeTerminalTargetKey(); got != nodeTargetKey("node-root") {
		t.Fatalf("expected node host shell to retain the node target, got %q", got)
	}

	for _, eventType := range []vaxis.EventType{vaxis.EventPress, vaxis.EventRelease} {
		app.handleMouse(vaxis.Mouse{
			Col:       12,
			Row:       7,
			Button:    vaxis.MouseLeftButton,
			EventType: eventType,
		})
	}

	if got := app.state.activeSessionKey(); got != hostSession {
		t.Fatalf("expected terminal click to preserve host session %q, got %q", hostSession, got)
	}
	if app.status != "" {
		t.Fatalf("expected terminal click to clear stale status, got %q", app.status)
	}

}

func TestTUIMouseCaptureUsesActiveHostTerminalTab(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	sessionManager := newSharedFakeTUISessionManager(sessions)
	state, err := newTUIState(testTUINodes(t), sessionManager)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	if err := state.focusTerminal(); err != nil {
		t.Fatalf("focusTerminal() error = %v", err)
	}
	guestSession := state.activeSessionKey()

	app := &vaxisTUIApp{
		ctx:              ctx,
		service:          service,
		state:            state,
		sessions:         sessions,
		terminalBodyRect: tuiRect{col: 10, row: 5, width: 40, height: 10},
	}

	hostKey := vaxis.Key{Text: "T", Keycode: 't', ShiftedCode: 'T', Modifiers: vaxis.ModAlt | vaxis.ModShift}
	app.handleKey(hostKey)

	hostSession := app.state.activeSessionKey()
	hostTerminal := sessionTestTerminal(t, sessions, hostSession)
	hostTerminal.capturesMouse = true
	guestTerminal := sessionTestTerminal(t, sessions, guestSession)
	guestTerminal.capturesMouse = true

	app.handleMouse(vaxis.Mouse{
		Col:       12,
		Row:       7,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventPress,
	})

	if got := app.state.activeSessionKey(); got != hostSession {
		t.Fatalf("expected captured mouse press to preserve host session %q, got %q", hostSession, got)
	}
	if len(hostTerminal.events) != 1 {
		t.Fatalf("expected captured mouse press to reach node host terminal, got %d events", len(hostTerminal.events))
	}
	if len(guestTerminal.events) != 0 {
		t.Fatalf("expected captured mouse press not to reach guest terminal, got %d events", len(guestTerminal.events))
	}
}

func TestTUIHandleKeyITogglesStickyTreePaneMode(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUINodes(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: newTUISessionStore(ctx, service, func(vaxis.Event) {}),
	}

	if state.treePaneMode != tuiTreePaneModeTerminal {
		t.Fatalf("expected terminal pane mode by default for the running node, got %q", state.treePaneMode)
	}

	quit := app.handleKey(vaxis.Key{Text: "i", Keycode: 'i'})
	if quit {
		t.Fatalf("expected i to toggle the pane mode, not quit")
	}
	if state.treePaneMode != tuiTreePaneModeInfo {
		t.Fatalf("expected info pane mode after i, got %q", state.treePaneMode)
	}

	if err := state.moveSelection(1); err != nil {
		t.Fatalf("moveSelection(child node) error = %v", err)
	}
	if got := state.selectedEntry(); got.node.ID != "node-child" {
		t.Fatalf("expected child node selection after moving in info mode, got %#v", got)
	}
	if state.treePaneMode != tuiTreePaneModeInfo {
		t.Fatalf("expected info pane mode to stay sticky across selection changes, got %q", state.treePaneMode)
	}

	quit = app.handleKey(vaxis.Key{Text: "i", Keycode: 'i'})
	if quit {
		t.Fatalf("expected second i to keep the app running")
	}
	if state.treePaneMode != tuiTreePaneModeTerminal {
		t.Fatalf("expected terminal pane mode after toggling back, got %q", state.treePaneMode)
	}
	if len(sessions.opened) != 0 {
		t.Fatalf("expected pane mode toggling and selection moves to open no terminal tabs, got %v", sessions.opened)
	}
}

func TestTUIHandleKeyEnterNoLongerFocusesTerminal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newFakeTUISessionManager()
	state, err := newTUIState(testTUINodes(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: newTUISessionStore(ctx, service, func(vaxis.Event) {}),
	}
	putTestSession(app.sessions, nodeTargetKey("node-root"), &tuiSession{
		node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
	}, newFakeTUITerminal())

	quit := app.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
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
	state, err := newTUIState(testTUINodes(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: newTUISessionStore(ctx, service, func(vaxis.Event) {}),
	}
	putTestSession(app.sessions, nodeTargetKey("node-root"), &tuiSession{
		node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
	}, newFakeTUITerminal())

	quit := app.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter, Modifiers: vaxis.ModAlt})
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
	state, err := newTUIState(testTUINodes(t), sessions)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: newTUISessionStore(ctx, service, func(vaxis.Event) {}),
	}

	quit := app.handleKey(vaxis.Key{Keycode: vaxis.KeyTab})
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
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	state.treePaneMode = tuiTreePaneModeTerminal

	app := &vaxisTUIApp{
		ctx:              ctx,
		service:          service,
		state:            state,
		sessions:         sessions,
		terminalBodyRect: tuiRect{col: 10, row: 5, width: 40, height: 10},
	}
	putTestSession(app.sessions, nodeTargetKey("node-root"), &tuiSession{
		node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
	}, newFakeTUITerminal())

	app.handleMouse(vaxis.Mouse{
		Col:       12,
		Row:       7,
		Button:    vaxis.MouseNoButton,
		EventType: vaxis.EventMotion,
	})
	if app.state.focus != tuiFocusTree {
		t.Fatalf("expected mouse motion to keep tree focus, got %q", app.state.focus)
	}

	app.handleMouse(vaxis.Mouse{
		Col:       12,
		Row:       7,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventPress,
	})
	if app.state.focus != tuiFocusTree {
		t.Fatalf("expected mouse press to keep tree focus until release, got %q", app.state.focus)
	}

	app.handleMouse(vaxis.Mouse{
		Col:       12,
		Row:       7,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventRelease,
	})
	if app.state.focus != tuiFocusTerminal {
		t.Fatalf("expected mouse release to focus terminal, got %q", app.state.focus)
	}
}

func TestTUIMouseReleaseOpensTerminalHyperlinkWithoutDrag(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	state.treePaneMode = tuiTreePaneModeTerminal

	opened := ""
	app := &vaxisTUIApp{
		ctx:               ctx,
		service:           service,
		openLink:          func(target string) error { opened = target; return nil },
		screenHyperlinkAt: func(col, row int) (string, bool) { return "https://auth.openai.com/example", col == 12 && row == 7 },
		state:             state,
		sessions:          sessions,
		terminalBodyRect:  tuiRect{col: 10, row: 5, width: 40, height: 10},
	}
	putTestSession(app.sessions, nodeTargetKey("node-root"), &tuiSession{
		node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
	}, newFakeTUITerminal())

	app.handleMouse(vaxis.Mouse{
		Col:       12,
		Row:       7,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventPress,
	})
	if opened != "" {
		t.Fatalf("expected press to defer opening terminal hyperlink, got %q", opened)
	}

	app.handleMouse(vaxis.Mouse{
		Col:       12,
		Row:       7,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventRelease,
	})

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

func TestTUIMouseDragDoesNotOpenTerminalHyperlinkWithoutGuestCapture(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	state.treePaneMode = tuiTreePaneModeTerminal

	terminal := newFakeTUITerminal()

	opened := ""
	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		openLink: func(target string) error { opened = target; return nil },
		screenHyperlinkAt: func(col, row int) (string, bool) {
			return "https://example.com/drag", col >= 11 && col <= 14 && row == 6
		},
		state:            state,
		sessions:         sessions,
		terminalBodyRect: tuiRect{col: 10, row: 5, width: 40, height: 10},
	}
	putTestSession(app.sessions, nodeTargetKey("node-root"), &tuiSession{
		node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
	}, terminal)

	app.handleMouse(vaxis.Mouse{
		Col:       11,
		Row:       6,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventPress,
	})
	app.handleMouse(vaxis.Mouse{
		Col:       14,
		Row:       6,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventMotion,
	})
	app.handleMouse(vaxis.Mouse{
		Col:       14,
		Row:       6,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventRelease,
	})

	if opened != "" {
		t.Fatalf("expected drag to avoid opening a terminal hyperlink, got %q", opened)
	}
	if app.state.focus != tuiFocusTree {
		t.Fatalf("expected drag without guest mouse capture to keep tree focus, got %q", app.state.focus)
	}
	if len(terminal.events) != 0 {
		t.Fatalf("expected drag without guest mouse capture to avoid forwarding mouse events, got %d events", len(terminal.events))
	}
	if app.terminalMouse != nil {
		t.Fatalf("expected terminal mouse gesture to clear after release")
	}
}

func TestTUIMouseDragForwardsToGuestWhenTerminalCapturesMouse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	state.treePaneMode = tuiTreePaneModeTerminal

	terminal := newFakeTUITerminal()
	terminal.capturesMouse = true

	app := &vaxisTUIApp{
		ctx:              ctx,
		service:          service,
		state:            state,
		sessions:         sessions,
		terminalBodyRect: tuiRect{col: 10, row: 5, width: 40, height: 10},
	}
	putTestSession(app.sessions, nodeTargetKey("node-root"), &tuiSession{
		node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
	}, terminal)

	for _, eventType := range []vaxis.EventType{vaxis.EventPress, vaxis.EventMotion, vaxis.EventRelease} {
		app.handleMouse(vaxis.Mouse{
			Col:       11,
			Row:       6,
			Button:    vaxis.MouseLeftButton,
			EventType: eventType,
		})
	}

	if len(terminal.events) != 3 {
		t.Fatalf("expected guest mouse forwarding, got %d events", len(terminal.events))
	}
	if app.state.focus != tuiFocusTerminal {
		t.Fatalf("expected mouse press to focus terminal, got %q", app.state.focus)
	}
}

func TestTUIShiftDragForwardsToGuestWhenTerminalCapturesMouse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	state.treePaneMode = tuiTreePaneModeTerminal

	terminal := newFakeTUITerminal()
	terminal.capturesMouse = true

	app := &vaxisTUIApp{
		ctx:              ctx,
		service:          service,
		state:            state,
		sessions:         sessions,
		terminalBodyRect: tuiRect{col: 10, row: 5, width: 40, height: 10},
	}
	putTestSession(app.sessions, nodeTargetKey("node-root"), &tuiSession{
		node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
	}, terminal)

	events := []vaxis.Mouse{
		{Col: 11, Row: 6, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress, Modifiers: vaxis.ModShift},
		{Col: 13, Row: 6, Button: vaxis.MouseLeftButton, EventType: vaxis.EventMotion, Modifiers: vaxis.ModShift},
		{Col: 13, Row: 6, Button: vaxis.MouseLeftButton, EventType: vaxis.EventRelease, Modifiers: vaxis.ModShift},
	}
	for _, event := range events {
		app.handleMouse(event)
	}

	if len(terminal.events) != 3 {
		t.Fatalf("expected shift-drag to reach the guest when it captures mouse, got %d events", len(terminal.events))
	}
}

func TestTUIDialogCtrlLeftBracketCancels(t *testing.T) {
	t.Parallel()

	var submitted bool
	dialog := newTUIDialog(
		"Create Node",
		"Create",
		nil,
		[]tuiDialogField{newTUIInputField("slug", "Node Slug", "", false)},
		func(map[string]string) error {
			submitted = true
			return nil
		},
	)

	done, err := dialog.Update(vaxis.Key{
		Keycode:   '[',
		Modifiers: vaxis.ModCtrl,
	})
	if err != nil {
		t.Fatalf("dialog.Update(Ctrl+[) error = %v", err)
	}
	if !done {
		t.Fatalf("expected Ctrl+[ to dismiss the dialog")
	}
	if submitted {
		t.Fatalf("expected Ctrl+[ to cancel, not submit")
	}
}

func TestTUIDialogLeftAndRightMoveCursorInEveryTextField(t *testing.T) {
	t.Parallel()

	dialog := newTUIDialog(
		"Update Configuration",
		"Update",
		nil,
		[]tuiDialogField{
			newTUIInputField("slug", "Configuration Slug", "small", true),
			newTUIInputField("image", "Image", "ubuntu", true),
			newTUIInputField("vcpus", "vCPUs", "1234", true),
		},
		nil,
	)

	for index := range dialog.Fields {
		dialog.FieldIndex = index
		input := dialog.Fields[index].Input
		end := input.CursorPosition()

		if _, err := dialog.Update(vaxis.Key{Keycode: vaxis.KeyLeft}); err != nil {
			t.Fatalf("field %q Left error = %v", dialog.Fields[index].Key, err)
		}
		if got := input.CursorPosition(); got != end-1 {
			t.Fatalf("field %q cursor after Left = %d, want %d", dialog.Fields[index].Key, got, end-1)
		}

		if _, err := dialog.Update(vaxis.Key{Keycode: vaxis.KeyRight}); err != nil {
			t.Fatalf("field %q Right error = %v", dialog.Fields[index].Key, err)
		}
		if got := input.CursorPosition(); got != end {
			t.Fatalf("field %q cursor after Right = %d, want %d", dialog.Fields[index].Key, got, end)
		}
	}
}

func TestTUIDialogRightStillActivatesSelectorFields(t *testing.T) {
	t.Parallel()

	activated := false
	dialog := newTUIDialog(
		"Update Configuration",
		"Update",
		nil,
		[]tuiDialogField{newTUIValueSelectorField("environment", "Environment", "dev", true, nil, func() error {
			activated = true
			return nil
		})},
		nil,
	)

	if _, err := dialog.Update(vaxis.Key{Keycode: vaxis.KeyRight}); err != nil {
		t.Fatalf("selector Right error = %v", err)
	}
	if !activated {
		t.Fatal("Right did not activate selector field")
	}
}

func TestTUIHandleEventEscapeClosesDialog(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodeCreate}); err != nil {
		t.Fatalf("performAction(create node) error = %v", err)
	}
	if app.activeDialog() == nil {
		t.Fatalf("expected create node dialog to be open")
	}

	quit, err := app.handleEvent(vaxis.Key{Keycode: vaxis.KeyEsc})
	if err != nil {
		t.Fatalf("handleEvent(Esc) error = %v", err)
	}
	if quit {
		t.Fatalf("expected Esc to close the dialog, not quit the app")
	}
	if app.activeDialog() != nil {
		t.Fatalf("expected Esc to close the dialog")
	}
}

func TestTUIHandleEventCtrlCQuitsWithDialogOpen(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodeCreate}); err != nil {
		t.Fatalf("performAction(create node) error = %v", err)
	}
	if app.activeDialog() == nil {
		t.Fatalf("expected create node dialog to be open")
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

func TestTUIHandleClipboardEventPushesHostClipboard(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())

	var copied string
	app.clipboardPush = func(text string) error {
		copied = text
		return nil
	}

	quit, err := app.handleEvent(tuiClipboardEvent{
		TargetKey: nodeTargetKey("node-root"),
		Text:      "vm clipboard",
	})
	if err != nil {
		t.Fatalf("handleEvent(tuiClipboardEvent) error = %v", err)
	}
	if quit {
		t.Fatalf("expected clipboard event to keep app running")
	}
	if copied != "vm clipboard" {
		t.Fatalf("expected VM clipboard payload to be pushed to host clipboard, got %q", copied)
	}
	if app.status != "synced VM clipboard to host clipboard" {
		t.Fatalf("expected clipboard sync status, got %q", app.status)
	}
}

func TestTUITerminalClosedEventIsHandledWhileOperationActive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)

	state, err := newTUIState(testTUINodes(t), tuiNoopSessionManager{})
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state.sessions = sessions
	state.focus = tuiFocusTerminal

	node := state.selectedEntry().node
	state.terminalTarget = nodeTargetKey(node.ID)
	sessionKey := putTestSession(sessions, nodeTargetKey(node.ID), &tuiSession{
		shellKind: terminal.NodeShell,
		label:     node.Slug,
		node:      node,
	}, newFakeTUITerminal())

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: sessions,
		operations: map[string]*tuiOperationState{
			"op-clone": {ID: "op-clone", Title: "Cloning " + node.Slug, ResourceKeys: []string{"node:" + node.ID}},
		},
		operationOrder: []string{"op-clone"},
	}

	quit, err := app.handleEvent(tuiTerminalClosedEvent{SessionKey: sessionKey})
	if err != nil {
		t.Fatalf("handleEvent(tuiTerminalClosedEvent) error = %v", err)
	}
	if quit {
		t.Fatalf("expected terminal close during operation to stay in app")
	}
	if sessions.HasSession(sessionKey) {
		t.Fatalf("expected closed terminal session to be removed during operation")
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

	state, err := newTUIState(testTUINodes(t), sessionManager)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	state.toggleTreePaneMode()
	if err := state.focusTerminal(); err != nil {
		t.Fatalf("focusTerminal() error = %v", err)
	}

	rootNode := state.selectedEntry().node
	sessionKeys := sessions.TargetSessionKeys(nodeTargetKey(rootNode.ID))
	if len(sessionKeys) != 1 {
		t.Fatalf("expected initial source-node session to exist, got %v", sessionKeys)
	}

	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: sessions,
		operations: map[string]*tuiOperationState{
			"op-clone": {ID: "op-clone", Title: "Cloning " + rootNode.Slug, ResourceKeys: []string{"node:" + rootNode.ID}},
		},
		operationOrder: []string{"op-clone"},
	}

	quit, err := app.handleEvent(tuiTerminalClosedEvent{SessionKey: sessionKeys[0]})
	if err != nil {
		t.Fatalf("handleEvent(tuiTerminalClosedEvent) error = %v", err)
	}
	if quit {
		t.Fatalf("expected terminal close during clone to stay in app")
	}
	if got := sessions.TargetSessionKeys(nodeTargetKey(rootNode.ID)); len(got) != 0 {
		t.Fatalf("expected source-node session to be removed after shell exit, got %v", got)
	}
	if state.focus != tuiFocusTree {
		t.Fatalf("expected focus to return to the tree after the source-node shell exited, got %q", state.focus)
	}

	// Explicitly refocusing the source node opens a fresh tab.
	if err := state.focusTerminal(); err != nil {
		t.Fatalf("focusTerminal() after clone close error = %v", err)
	}
	if got := sessions.TargetSessionKeys(nodeTargetKey(rootNode.ID)); len(got) != 1 {
		t.Fatalf("expected source-node session to be recreated after refocusing, got %v", got)
	}
	if sessionManager.opened[nodeTargetKey(rootNode.ID)] < 2 {
		t.Fatalf("expected source-node session to be opened again after refocusing, got %d", sessionManager.opened[nodeTargetKey(rootNode.ID)])
	}
}

func TestTUIAvailableActionsDependOnSelectedEntry(t *testing.T) {
	t.Parallel()

	emptyActions := availableTUIActions(tuiTreeEntry{})
	if got := actionIDs(emptyActions); got != "node.create,configuration.manage,environment_config.manage" {
		t.Fatalf("unexpected empty action set: %s", got)
	}

	runningNodeActions := availableTUIActions(tuiTreeEntry{
		node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning},
	})

	if got := actionIDs(runningNodeActions); got != "node.create,configuration.manage,environment_config.manage,node.stop,node.delete,node.clone" {
		t.Fatalf("unexpected running node action set: %s", got)
	}

	createdNodeActions := availableTUIActions(tuiTreeEntry{
		node: Node{ID: "node-created", Slug: "created-node", Status: NodeStatusCreated},
	})

	if got := actionIDs(createdNodeActions); got != "node.create,configuration.manage,environment_config.manage,node.start,node.delete,node.clone" {
		t.Fatalf("unexpected created node action set: %s", got)
	}
}

func TestTUIConfigurationMenuCreatesReusableConfiguration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	if err := service.ensureReadyForWrite(context.Background()); err != nil {
		t.Fatalf("ensureReadyForWrite() error = %v", err)
	}
	state, err := newTUIState(nil, newFakeTUISessionManager())
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	app := &vaxisTUIApp{
		ctx:      ctx,
		service:  service,
		state:    state,
		sessions: newTUISessionStore(ctx, service, noopPostEvent),
	}

	if err := app.performAction(tuiActionSpec{ID: tuiActionConfigurationManage}); err != nil {
		t.Fatalf("performAction(configuration manage) error = %v", err)
	}
	if app.activeMenu() == nil || app.activeMenu().Title != "Configurations" {
		t.Fatalf("expected configurations menu, got %#v", app.activeMenu())
	}
	chooseTUIMenuEntry(t, app, "Create Configuration")
	if app.activeDialog() == nil || app.activeDialog().Title != "Create Configuration" {
		t.Fatalf("expected create configuration dialog, got %#v", app.activeDialog())
	}
	submitTUIDialog(t, app, map[string]string{"slug": "qa-recipe"})

	created, err := service.ConfigurationShow(context.Background(), "qa-recipe")
	if err != nil {
		t.Fatalf("ConfigurationShow(qa-recipe) error = %v", err)
	}
	if created.VCPUs != DefaultVCPUs || created.MemoryMiB != DefaultMemoryMiB || created.DiskMiB != DefaultDiskMiB {
		t.Fatalf("expected new configuration to copy default resources, got %#v", created)
	}
	if app.activeMenu() == nil || app.activeMenu().Title != "Configuration: qa-recipe" {
		t.Fatalf("expected created configuration menu, got %#v", app.activeMenu())
	}
}

func TestTUIConfigurationSelectorOrdersBuiltInSizes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	if err := service.ensureReadyForWrite(ctx); err != nil {
		t.Fatalf("ensureReadyForWrite() error = %v", err)
	}
	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())

	if err := app.openConfigurationSelector("", func(string) error { return nil }); err != nil {
		t.Fatalf("openConfigurationSelector() error = %v", err)
	}
	selector := app.activeSelector()
	if selector == nil {
		t.Fatal("expected configuration selector")
	}
	values := make([]string, 0, len(selector.Options))
	labels := make([]string, 0, len(selector.Options))
	for _, option := range selector.Options {
		values = append(values, option.Value)
		labels = append(labels, option.Label)
	}
	if got := strings.Join(values, ","); got != "xsmall,small,medium,large,xlarge" {
		t.Fatalf("configuration selector order = %q", got)
	}
	wantLabels := []string{
		"xsmall (1 vCPU, 1 GiB RAM, 10 GiB disk)",
		"small (2 vCPU, 4 GiB RAM, 25 GiB disk)",
		"medium (4 vCPU, 8 GiB RAM, 50 GiB disk)",
		"large (6 vCPU, 16 GiB RAM, 75 GiB disk)",
		"xlarge (8 vCPU, 32 GiB RAM, 100 GiB disk)",
	}
	if got := strings.Join(labels, "\n"); got != strings.Join(wantLabels, "\n") {
		t.Fatalf("configuration selector labels =\n%s\nwant:\n%s", got, strings.Join(wantLabels, "\n"))
	}
}

func TestTUIConfigurationSelectorLabelPreservesPartialGiBValues(t *testing.T) {
	t.Parallel()

	configuration := Configuration{
		Slug:      "custom",
		VCPUs:     3,
		MemoryMiB: 1536,
		DiskMiB:   25 * 1024,
	}
	if got, want := configurationSelectorLabel(configuration), "custom (3 vCPU, 1536 MiB RAM, 25 GiB disk)"; got != want {
		t.Fatalf("configurationSelectorLabel() = %q, want %q", got, want)
	}
}

func TestTUIManageEnvironmentConfigUsesSelector(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)

	if _, err := service.EnvironmentConfigCreate(context.Background(), EnvironmentConfigCreateInput{
		Slug:              "shared-dev",
		BootstrapCommands: []string{"./script/setup"},
	}); err != nil {
		t.Fatalf("EnvironmentConfigCreate(shared-dev) error = %v", err)
	}

	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())

	if err := app.performAction(tuiActionSpec{ID: tuiActionEnvironmentConfigManage}); err != nil {
		t.Fatalf("performAction(environment config manage) error = %v", err)
	}
	if app.activeMenu() == nil || app.activeMenu().Title != "Environments" {
		t.Fatalf("expected environments menu, got %#v", app.activeMenu())
	}
	chooseTUIMenuEntry(t, app, "Manage Environment")
	if app.activeSelector() == nil || app.activeSelector().Title != "Manage Environment" {
		t.Fatalf("expected manage environment selector, got %#v", app.activeSelector())
	}
	chooseTUISelector(t, app, "shared-dev")
	if app.activeMenu() == nil || app.activeMenu().Title != "Environment: shared-dev" {
		t.Fatalf("expected environment command menu, got %#v", app.activeMenu())
	}
}

func TestTUICreateEnvironmentUsesEnvironmentTerminology(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())

	if err := app.openEnvironmentConfigsMenu(); err != nil {
		t.Fatalf("openEnvironmentConfigsMenu() error = %v", err)
	}
	chooseTUIMenuEntry(t, app, "Create Environment")
	dialog := app.activeDialog()
	if dialog == nil || dialog.Title != "Create Environment" {
		t.Fatalf("expected create environment dialog, got %#v", dialog)
	}
	if len(dialog.Fields) != 1 || dialog.Fields[0].Label != "Environment Slug" {
		t.Fatalf("expected environment slug field, got %#v", dialog.Fields)
	}
}

func TestTUIEnvironmentConfigCommandEditingReopensMenuAndSupportsReorder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)

	if _, err := service.EnvironmentConfigCreate(context.Background(), EnvironmentConfigCreateInput{
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
	if app.activeMenu() == nil || app.activeMenu().Title != "Environment: shared-dev" {
		t.Fatalf("expected environment command menu, got %#v", app.activeMenu())
	}

	chooseTUIMenuEntry(t, app, "Add Bootstrap Command")
	if app.activeDialog() == nil || app.activeDialog().Title != "Add Environment Bootstrap Command" {
		t.Fatalf("expected add command dialog, got %#v", app.activeDialog())
	}
	submitTUIDialog(t, app, map[string]string{"command": "brew bundle"})
	if app.activeMenu() == nil || app.activeMenu().Title != "Environment: shared-dev" {
		t.Fatalf("expected command menu to reopen after add, got %#v", app.activeMenu())
	}

	chooseTUIMenuEntry(t, app, "Move Bootstrap Command")
	if app.activeSelector() == nil || app.activeSelector().Title != "Move Environment Bootstrap Command" {
		t.Fatalf("expected move command selector, got %#v", app.activeSelector())
	}
	chooseTUISelector(t, app, "4. brew bundle")
	if app.activeMenu() == nil || app.activeMenu().Title != "Move Environment Bootstrap Command: brew bundle" {
		t.Fatalf("expected move direction menu, got %#v", app.activeMenu())
	}
	chooseTUIMenuEntry(t, app, "Move Up")
	if app.activeMenu() == nil || app.activeMenu().Title != "Environment: shared-dev" {
		t.Fatalf("expected command menu to reopen after reorder, got %#v", app.activeMenu())
	}

	chooseTUIMenuEntry(t, app, "Remove Bootstrap Command")
	if app.activeSelector() == nil || app.activeSelector().Title != "Remove Environment Bootstrap Commands" {
		t.Fatalf("expected remove command selector, got %#v", app.activeSelector())
	}
	chooseTUISelector(t, app, "2. direnv allow", "3. brew bundle")
	if app.activeDialog() == nil || app.activeDialog().Title != "Remove Environment Bootstrap Commands" {
		t.Fatalf("expected remove command confirmation dialog, got %#v", app.activeDialog())
	}
	submitTUIDialog(t, app, map[string]string{})
	if app.activeMenu() == nil || app.activeMenu().Title != "Environment: shared-dev" {
		t.Fatalf("expected command menu to reopen after remove, got %#v", app.activeMenu())
	}

	config, err := service.EnvironmentConfigShow(context.Background(), "shared-dev")
	if err != nil {
		t.Fatalf("EnvironmentConfigShow(shared-dev) error = %v", err)
	}
	if got := strings.Join(config.BootstrapCommands, "|"); got != "./script/setup|mise install" {
		t.Fatalf("expected reordered and removed commands, got %q", got)
	}
}

func TestTUINodeActionsStartStopCloneAndDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	rootNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Slug:      "root-node",
		Directory: workspace,
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
	if sessions.opened[nodeTargetKey(rootNode.ID)] != 0 {
		t.Fatalf("expected info mode to avoid opening a node shell session automatically, got %d", sessions.opened[nodeTargetKey(rootNode.ID)])
	}
	if got := app.state.selectedEntry(); got.node.ID != rootNode.ID || got.node.Status != NodeStatusRunning {
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
	if got := app.state.selectedEntry(); got.node.ID != rootNode.ID || got.node.Status != NodeStatusStopped {
		t.Fatalf("expected stopped root node to remain selected, got %#v", got)
	}

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodeClone}); err != nil {
		t.Fatalf("performAction(clone node) error = %v", err)
	}
	if app.activeDialog() == nil || app.activeDialog().Title != "Clone Node" {
		t.Fatalf("expected clone node dialog, got %#v", app.activeDialog())
	}
	if len(app.activeDialog().Fields) != 1 || app.activeDialog().Fields[0].Key != "node_slug" {
		t.Fatalf("expected clone node dialog to prompt only for the cloned slug, got %#v", app.activeDialog().Fields)
	}
	submitTUIDialog(t, app, map[string]string{
		"node_slug": "child-node",
	})

	childNode, err := service.NodeShow(ctx, "child-node")
	if err != nil {
		t.Fatalf("NodeShow(child-node) error = %v", err)
	}
	if childNode.ParentNodeID != rootNode.ID {
		t.Fatalf("expected cloned node parent id %q, got %q", rootNode.ID, childNode.ParentNodeID)
	}
	if got := app.state.selectedEntry(); got.node.ID != childNode.ID {
		t.Fatalf("expected cloned child node to become selected, got %#v", got)
	}

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodeDelete}); err != nil {
		t.Fatalf("performAction(delete node) error = %v", err)
	}
	if app.activeDialog() == nil || app.activeDialog().Title != "Delete Node" {
		t.Fatalf("expected delete node dialog, got %#v", app.activeDialog())
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
	service.sandbox.(*fakeSandbox).cloneStatus = "running"
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	rootNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Slug:      "root-node",
		Directory: workspace,
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
	if app.activeDialog() == nil || app.activeDialog().Title != "Clone Node" {
		t.Fatalf("expected clone node dialog, got %#v", app.activeDialog())
	}
	submitTUIDialog(t, app, map[string]string{
		"node_slug": "child-node",
	})

	childNode, err := service.NodeShow(ctx, "child-node")
	if err != nil {
		t.Fatalf("NodeShow(child-node) error = %v", err)
	}
	if childNode.Status != NodeStatusStopped {
		t.Fatalf("expected cloned child node to be stopped until explicitly started, got %q", childNode.Status)
	}
	if got := app.state.selectedEntry(); got.node.ID != childNode.ID || got.node.Status != NodeStatusStopped {
		t.Fatalf("expected stopped cloned child node to become selected, got %#v", got)
	}
	if sessions.opened[nodeTargetKey(childNode.ID)] != 0 {
		t.Fatalf("expected stopped cloned child node to avoid auto-opening a shell session, got %d session creations", sessions.opened[nodeTargetKey(childNode.ID)])
	}
}

func TestTUIBackgroundNodeOperationDoesNotBlockNavigationOrTerminalFocus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	rootNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Slug:      "root-node",
		Directory: workspace,
	})
	if err != nil {
		t.Fatalf("NodeCreate(root-node) error = %v", err)
	}
	childNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Slug:      "child-node",
		Directory: workspace,
	})
	if err != nil {
		t.Fatalf("NodeCreate(child-node) error = %v", err)
	}
	if _, err := service.NodeStart(ctx, childNode.ID); err != nil {
		t.Fatalf("NodeStart(child-node) error = %v", err)
	}

	fake := service.sandbox.(*fakeSandbox)
	startGate := newFakeSandboxGate()
	fake.startGate = startGate

	app, events := newAsyncTestTUIApp(t, ctx, service)
	selectTUIEntry(t, app, "node:"+rootNode.ID)

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodeStart}); err != nil {
		t.Fatalf("performAction(start root-node) error = %v", err)
	}
	awaitFakeSandboxGate(t, startGate, rootNode.SandboxName)

	app.handleKey(vaxis.Key{Keycode: vaxis.KeyUp})
	if got := app.state.selectedEntry().node.ID; got != childNode.ID {
		t.Fatalf("expected selection to move to child-node, got %q", got)
	}

	quit := app.handleKey(vaxis.Key{Text: "`", Keycode: '`', Modifiers: vaxis.ModAlt})
	if quit {
		t.Fatalf("expected Alt+` to keep the app running")
	}
	if app.state.focus != tuiFocusTerminal {
		t.Fatalf("expected terminal focus while background work is active, got %q", app.state.focus)
	}

	drainAsyncTUIEvents(t, app, events)
	close(startGate.release)
	awaitAsyncTUIState(t, app, events, func() bool {
		return len(app.operations) == 0
	}, "background start operation to finish")
	if got := app.state.selectedEntry().node.ID; got != childNode.ID {
		t.Fatalf("expected selection to stay on child-node after background completion, got %q", got)
	}
	if app.state.focus != tuiFocusTerminal {
		t.Fatalf("expected terminal focus to stay on child-node after background completion, got %q", app.state.focus)
	}

	startedNode, err := service.NodeShow(ctx, rootNode.ID)
	if err != nil {
		t.Fatalf("NodeShow(root-node) error = %v", err)
	}
	if startedNode.Status != NodeStatusRunning {
		t.Fatalf("expected root-node to finish running, got %q", startedNode.Status)
	}
}

func TestTUIBackgroundOperationsAllowNonConflictingActionsAndRejectConflicts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	rootNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Slug:      "root-node",
		Directory: workspace,
	})
	if err != nil {
		t.Fatalf("NodeCreate(root-node) error = %v", err)
	}
	childNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Slug:      "child-node",
		Directory: workspace,
	})
	if err != nil {
		t.Fatalf("NodeCreate(child-node) error = %v", err)
	}

	fake := service.sandbox.(*fakeSandbox)
	startGate := newFakeSandboxGate()
	fake.startGate = startGate

	app, events := newAsyncTestTUIApp(t, ctx, service)
	selectTUIEntry(t, app, "node:"+rootNode.ID)

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodeStart}); err != nil {
		t.Fatalf("performAction(start root-node) error = %v", err)
	}
	awaitFakeSandboxGate(t, startGate, rootNode.SandboxName)

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodeDelete}); err == nil {
		t.Fatalf("expected conflicting delete to be rejected while start is active")
	}

	selectTUIEntry(t, app, "node:"+childNode.ID)
	if err := app.performAction(tuiActionSpec{ID: tuiActionNodeStart}); err != nil {
		t.Fatalf("performAction(start child-node) error = %v", err)
	}
	if len(app.operations) != 2 {
		t.Fatalf("expected two background operations, got %d", len(app.operations))
	}

	drainAsyncTUIEvents(t, app, events)
	close(startGate.release)
	awaitAsyncTUIState(t, app, events, func() bool {
		return len(app.operations) == 0
	}, "background start operations to finish")

	rootNode, err = service.NodeShow(ctx, rootNode.ID)
	if err != nil {
		t.Fatalf("NodeShow(root-node) error = %v", err)
	}
	if rootNode.Status != NodeStatusRunning {
		t.Fatalf("expected root-node to be running, got %q", rootNode.Status)
	}
	childNode, err = service.NodeShow(ctx, childNode.ID)
	if err != nil {
		t.Fatalf("NodeShow(child-node) error = %v", err)
	}
	if childNode.Status != NodeStatusRunning {
		t.Fatalf("expected child-node to be running, got %q", childNode.Status)
	}
}

func TestTUIFlatNodeListHasNoProjectRowsAndUsesRelativeDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	child := filepath.Join(root, "child")
	state, err := newTUIState([]Node{
		{ID: "root", Slug: "root-node", ConfigurationSlug: "default", DirectoryPath: root, Status: NodeStatusStopped},
		{ID: "child", Slug: "child-node", ConfigurationSlug: "large", DirectoryPath: child, Status: NodeStatusRunning},
	}, newFakeTUISessionManager())
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	if len(state.entries) != 2 {
		t.Fatalf("expected two flat node entries, got %#v", state.entries)
	}
	for _, entry := range state.entries {
		if !entry.valid() {
			t.Fatalf("expected node-only rows, got %#v", entry)
		}
	}

	app := &vaxisTUIApp{state: state, treeWorkspaceRoot: root}
	if got := app.treeEntryLabel(state.entries[0]); !strings.Contains(got, "[default]  .") || strings.Contains(got, root) {
		t.Fatalf("expected root-relative node label, got %q", got)
	}
	if got := app.treeEntryLabel(state.entries[1]); !strings.Contains(got, "[large]  child") || strings.Contains(got, root) {
		t.Fatalf("expected child-relative node label, got %q", got)
	}
}

func testTUINodes(t *testing.T) []Node {
	t.Helper()

	rootWorkspace := filepath.Join(t.TempDir(), "root")
	childWorkspace := filepath.Join(t.TempDir(), "child")

	return []Node{
		{
			ID:                 "node-root",
			Slug:               "root-node",
			GuestWorkspacePath: rootWorkspace,
			Status:             NodeStatusRunning,
		},
		{
			ID:                 "node-child",
			Slug:               "child-node",
			GuestWorkspacePath: childWorkspace,
			Status:             NodeStatusRunning,
		},
	}
}

func testTUINodesWithCreatedNode(t *testing.T) []Node {
	t.Helper()

	rootWorkspace := filepath.Join(t.TempDir(), "root")
	childWorkspace := filepath.Join(t.TempDir(), "child")

	return []Node{
		{
			ID:                 "node-root",
			Slug:               "root-node",
			GuestWorkspacePath: rootWorkspace,
			Status:             NodeStatusRunning,
		},
		{
			ID:                 "node-created",
			Slug:               "created-node",
			GuestWorkspacePath: childWorkspace,
			Status:             NodeStatusCreated,
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

	nodes, err := loadTUINodes(ctx, service, "")
	if err != nil {
		t.Fatalf("loadTUINodes() error = %v", err)
	}

	state, err := newTUIState(nodes, sessions)
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

func newAsyncTestTUIApp(t *testing.T, ctx context.Context, service *Service) (*vaxisTUIApp, chan vaxis.Event) {
	t.Helper()

	events := make(chan vaxis.Event, 64)
	postEvent := func(event vaxis.Event) {
		events <- event
	}
	sessions := newTUISessionStore(ctx, service, postEvent)
	nodes, err := loadTUINodes(ctx, service, "")
	if err != nil {
		t.Fatalf("loadTUINodes() error = %v", err)
	}
	state, err := newTUIState(nodes, newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	app := &vaxisTUIApp{
		ctx:        ctx,
		service:    service,
		state:      state,
		sessions:   sessions,
		postEvent:  postEvent,
		operations: map[string]*tuiOperationState{},
	}

	return app, events
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

	dialog := app.activeDialog()
	if dialog == nil {
		t.Fatalf("expected dialog to be open")
	}
	if err := dialog.OnSubmit(values); err != nil {
		t.Fatalf("dialog submit error = %v", err)
	}
	// OnSubmit may have replaced the overlay (e.g. reopening a menu); only
	// dismiss when the submitted dialog is still showing.
	if app.activeDialog() == dialog {
		app.overlay = nil
	}
}

func chooseTUIMenuEntry(t *testing.T, app *vaxisTUIApp, label string) {
	t.Helper()

	current := app.activeMenu()
	if current == nil {
		t.Fatalf("expected menu to be open")
	}

	for _, entry := range current.Entries {
		if entry.Label != label {
			continue
		}
		if err := entry.Action(); err != nil {
			t.Fatalf("menu action %q error = %v", label, err)
		}
		if app.activeMenu() == current {
			app.overlay = nil
		}
		return
	}

	t.Fatalf("expected menu entry %q", label)
}

func awaitFakeSandboxGate(t *testing.T, gate *fakeSandboxGate, want string) {
	t.Helper()

	select {
	case got := <-gate.entered:
		if got != want {
			t.Fatalf("expected fake sandbox gate for %q, got %q", want, got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for fake sandbox gate %q", want)
	}
}

func drainAsyncTUIEvents(t *testing.T, app *vaxisTUIApp, events <-chan vaxis.Event) {
	t.Helper()

	for {
		select {
		case event := <-events:
			quit, err := app.handleEvent(event)
			if err != nil {
				t.Fatalf("handleEvent(async) error = %v", err)
			}
			if quit {
				t.Fatalf("expected async event handling to keep the app running")
			}
		default:
			return
		}
	}
}

func awaitAsyncTUIState(t *testing.T, app *vaxisTUIApp, events <-chan vaxis.Event, condition func() bool, description string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		drainAsyncTUIEvents(t, app, events)
		if condition() {
			return
		}
		select {
		case event := <-events:
			quit, err := app.handleEvent(event)
			if err != nil {
				t.Fatalf("handleEvent(async wait) error = %v", err)
			}
			if quit {
				t.Fatalf("expected async wait for %s to keep the app running", description)
			}
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for %s", description)
}

func chooseTUISelector(t *testing.T, app *vaxisTUIApp, values ...string) {
	t.Helper()

	selector := app.activeSelector()
	if selector == nil {
		t.Fatalf("expected selector to be open")
	}

	if selector.Multi {
		selector.Selected = map[string]bool{}
	}

	for _, value := range values {
		found := false
		for index, option := range selector.Options {
			if option.Value != value && option.Label != value {
				continue
			}
			selector.Index = index
			if selector.Multi {
				selector.Selected[option.Value] = true
			}
			found = true
			break
		}
		if !found {
			t.Fatalf("expected selector option %q", value)
		}
	}

	if !selector.Multi && len(values) > 0 {
		for index, option := range selector.Options {
			if option.Value != values[0] && option.Label != values[0] {
				continue
			}
			selector.Index = index
			break
		}
	}

	done, err := selector.Update(vaxis.Key{Keycode: vaxis.KeyEnter})
	if err != nil {
		t.Fatalf("selector submit error = %v", err)
	}
	if !done {
		t.Fatalf("expected selector to complete")
	}
	// OnSubmit may have replaced the overlay (opening a menu or dialog); only
	// dismiss when the submitted selector is still showing.
	if app.activeSelector() == selector {
		app.overlay = overlayParent(selector)
	}
}

// TestRunClosesSessionsOnContextCancel pins down the Ctrl+C / SIGTERM cleanup
// contract: when the context wired into the TUI event loop is cancelled, the
// loop returns and every open terminal session is drained via Close() (which,
// per Track 0.1, group-kills the VM shells). The guarantee must live in the
// loop seam (serve) as a defer, not in happy-path code, so an abrupt cancel
// cannot skip it.
func TestRunClosesSessionsOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, _ := newTestService(t)
	app, events := newAsyncTestTUIApp(t, ctx, service)

	nodeTerminal := newFakeTUITerminal()
	putTestSession(app.sessions, nodeTargetKey("node-1"), &tuiSession{
		shellKind: terminal.NodeShell,
	}, nodeTerminal)
	hostTerminal := newFakeTUITerminal()
	putTestSession(app.sessions, nodeTargetKey("node-2"), &tuiSession{
		shellKind: terminal.NodeHostShell,
	}, hostTerminal)

	done := make(chan error, 1)
	go func() {
		done <- app.serve(events)
	}()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("serve() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("serve() did not return after context cancellation")
	}

	if nodeTerminal.closeCalls != 1 {
		t.Fatalf("node terminal Close() calls = %d, want 1", nodeTerminal.closeCalls)
	}
	if hostTerminal.closeCalls != 1 {
		t.Fatalf("host terminal Close() calls = %d, want 1", hostTerminal.closeCalls)
	}
	if len(app.sessions.sessions) != 0 {
		t.Fatalf("expected all sessions drained, got %d", len(app.sessions.sessions))
	}
}
