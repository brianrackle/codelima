package codelima

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/widgets/term"
)

type fakeTUIRunner struct {
	calls int
}

func (f *fakeTUIRunner) Run(_ context.Context, _ *Service) error {
	f.calls++
	return nil
}

type fakeTUISessionManager struct {
	ensured map[string]int
}

func newFakeTUISessionManager() *fakeTUISessionManager {
	return &fakeTUISessionManager{ensured: map[string]int{}}
}

func (f *fakeTUISessionManager) HasSession(nodeID string) bool {
	return f.ensured[nodeID] > 0
}

func (f *fakeTUISessionManager) EnsureSession(node Node) error {
	f.ensured[node.ID]++
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
		terminal: term.New(),
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
	if app.state.focus != tuiFocusTerminal {
		t.Fatalf("expected mouse press to focus terminal, got %q", app.state.focus)
	}
}

func TestTUIMousePressOpensTerminalHyperlink(t *testing.T) {
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
		terminal: term.New(),
	}

	if err := app.handleMouse(vaxis.Mouse{
		Col:       12,
		Row:       7,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventPress,
	}); err != nil {
		t.Fatalf("handleMouse(link press) error = %v", err)
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

	if got := actionIDs(projectActions); got != "project.create,environment_config.manage,project.create_node,project.environment,project.update,project.delete" {
		t.Fatalf("unexpected project action set: %s", got)
	}

	runningNodeActions := availableTUIActions(tuiTreeEntry{
		kind: tuiTreeEntryNode,
		node: Node{Slug: "root-node", Status: NodeStatusRunning},
	})

	if got := actionIDs(runningNodeActions); got != "project.create,environment_config.manage,node.stop,node.delete,node.clone,node.patch" {
		t.Fatalf("unexpected running node action set: %s", got)
	}

	createdNodeActions := availableTUIActions(tuiTreeEntry{
		kind: tuiTreeEntryNode,
		node: Node{Slug: "created-node", Status: NodeStatusCreated},
	})

	if got := actionIDs(createdNodeActions); got != "project.create,environment_config.manage,node.start,node.delete,node.clone,node.patch" {
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
	submitTUIDialog(t, app, map[string]string{"slug": "root-node"})

	rootNode, err := service.NodeShow(ctx, "root-node")
	if err != nil {
		t.Fatalf("NodeShow(root-node) error = %v", err)
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

func TestTUIProjectEnvironmentActionsAddRemoveAndClear(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
		SetupCommands: []string{"./script/setup"},
	})
	if err != nil {
		t.Fatalf("ProjectCreate(root) error = %v", err)
	}

	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())
	selectTUIEntry(t, app, "project:"+project.ID)

	if err := app.performAction(tuiActionSpec{ID: tuiActionProjectEnvironment}); err != nil {
		t.Fatalf("performAction(project environment) error = %v", err)
	}
	if app.menu == nil || app.menu.Title != "Project Environment" {
		t.Fatalf("expected project environment menu, got %#v", app.menu)
	}
	chooseTUIMenuEntry(t, app, "Add Command")
	if app.dialog == nil || app.dialog.Title != "Add Environment Command" {
		t.Fatalf("expected add environment command dialog, got %#v", app.dialog)
	}
	submitTUIDialog(t, app, map[string]string{"command": "direnv allow"})

	updated, err := service.ProjectShow(project.ID)
	if err != nil {
		t.Fatalf("ProjectShow(updated root) error = %v", err)
	}
	if strings.Join(updated.SetupCommands, "|") != "./script/setup|direnv allow" {
		t.Fatalf("expected appended environment commands, got %v", updated.SetupCommands)
	}

	selectTUIEntry(t, app, "project:"+project.ID)
	if err := app.performAction(tuiActionSpec{ID: tuiActionProjectEnvironment}); err != nil {
		t.Fatalf("performAction(project environment remove) error = %v", err)
	}
	chooseTUIMenuEntry(t, app, "Remove Command")
	if app.dialog == nil || app.dialog.Title != "Remove Environment Command" {
		t.Fatalf("expected remove environment command dialog, got %#v", app.dialog)
	}
	submitTUIDialog(t, app, map[string]string{"index": "1"})

	updated, err = service.ProjectShow(project.ID)
	if err != nil {
		t.Fatalf("ProjectShow(after remove) error = %v", err)
	}
	if strings.Join(updated.SetupCommands, "|") != "direnv allow" {
		t.Fatalf("expected first environment command to be removed, got %v", updated.SetupCommands)
	}

	selectTUIEntry(t, app, "project:"+project.ID)
	if err := app.performAction(tuiActionSpec{ID: tuiActionProjectEnvironment}); err != nil {
		t.Fatalf("performAction(project environment clear) error = %v", err)
	}
	chooseTUIMenuEntry(t, app, "Clear Commands")
	if app.dialog == nil || app.dialog.Title != "Clear Environment Commands" {
		t.Fatalf("expected clear environment commands dialog, got %#v", app.dialog)
	}
	submitTUIDialog(t, app, map[string]string{})

	updated, err = service.ProjectShow(project.ID)
	if err != nil {
		t.Fatalf("ProjectShow(after clear) error = %v", err)
	}
	if len(updated.SetupCommands) != 0 {
		t.Fatalf("expected cleared environment commands, got %v", updated.SetupCommands)
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
	chooseTUIMenuEntry(t, app, "Create Config")
	if app.dialog == nil || app.dialog.Title != "Create Environment Config" {
		t.Fatalf("expected create environment config dialog, got %#v", app.dialog)
	}
	submitTUIDialog(t, app, map[string]string{
		"slug":            "shared-dev",
		"initial_command": "./script/setup",
	})

	config, err := service.EnvironmentConfigShow("shared-dev")
	if err != nil {
		t.Fatalf("EnvironmentConfigShow(shared-dev) error = %v", err)
	}
	if got := strings.Join(config.Commands, "|"); got != "./script/setup" {
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
	if err := app.performAction(tuiActionSpec{ID: tuiActionProjectEnvironment}); err != nil {
		t.Fatalf("performAction(project environment clear configs) error = %v", err)
	}
	if app.menu == nil || app.menu.Title != "Project Environment" {
		t.Fatalf("expected project environment menu, got %#v", app.menu)
	}
	chooseTUIMenuEntry(t, app, "Set Configs")
	if app.selector == nil || app.selector.Title != "Set Environment Configs" {
		t.Fatalf("expected set configs selector, got %#v", app.selector)
	}
	chooseTUISelector(t, app)

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
		Slug:     "shared-dev",
		Commands: []string{"./script/setup"},
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
		Slug:     "shared-dev",
		Commands: []string{"./script/setup"},
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

func TestTUINodePatchActionsProposeApproveApplyAndReject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "file.txt"), "root\n")

	rootProject, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate(root) error = %v", err)
	}

	childWorkspace := filepath.Join(t.TempDir(), "child")
	childProject, err := service.ProjectFork(ctx, ProjectForkInput{
		SourceProject: rootProject.ID,
		Slug:          "child",
		WorkspacePath: childWorkspace,
	})
	if err != nil {
		t.Fatalf("ProjectFork(child) error = %v", err)
	}

	rootNode, err := service.NodeCreate(ctx, NodeCreateInput{Project: rootProject.ID, Slug: "root-node"})
	if err != nil {
		t.Fatalf("NodeCreate(root-node) error = %v", err)
	}
	childNode, err := service.NodeCreate(ctx, NodeCreateInput{Project: childProject.ID, Slug: "child-node"})
	if err != nil {
		t.Fatalf("NodeCreate(child-node) error = %v", err)
	}

	writeFile(t, filepath.Join(childWorkspace, "file.txt"), "child change\n")

	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())
	selectTUIEntry(t, app, "node:"+childNode.ID)

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodePatch}); err != nil {
		t.Fatalf("performAction(patch menu) error = %v", err)
	}
	if app.menu == nil || app.menu.Title != "Patch Operations" {
		t.Fatalf("expected patch operations menu, got %#v", app.menu)
	}
	chooseTUIMenuEntry(t, app, "Propose Patch")
	if app.dialog == nil || app.dialog.Title != "Propose Patch" {
		t.Fatalf("expected propose patch dialog, got %#v", app.dialog)
	}
	submitTUIDialog(t, app, map[string]string{
		"target_project": rootProject.Slug,
		"target_node":    rootNode.Slug,
	})

	patches, err := service.PatchList("")
	if err != nil {
		t.Fatalf("PatchList() after propose error = %v", err)
	}
	if len(patches) != 1 {
		t.Fatalf("expected one patch after proposal, got %d", len(patches))
	}
	firstPatch := patches[0]
	if firstPatch.Status != PatchStatusSubmitted {
		t.Fatalf("expected submitted patch status, got %q", firstPatch.Status)
	}
	if firstPatch.SourceNodeID != childNode.ID || firstPatch.TargetNodeID != rootNode.ID {
		t.Fatalf("expected patch to track child->root nodes, got source=%q target=%q", firstPatch.SourceNodeID, firstPatch.TargetNodeID)
	}

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodePatch}); err != nil {
		t.Fatalf("performAction(patch menu approve) error = %v", err)
	}
	chooseTUIMenuEntry(t, app, "Approve Patch")
	submitTUIDialog(t, app, map[string]string{
		"patch_id": firstPatch.ID,
		"actor":    "tui-test",
		"note":     "ready",
	})

	approvedPatch, _, err := service.PatchShow(firstPatch.ID)
	if err != nil {
		t.Fatalf("PatchShow(approved) error = %v", err)
	}
	if approvedPatch.Status != PatchStatusApproved {
		t.Fatalf("expected approved patch status, got %q", approvedPatch.Status)
	}

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodePatch}); err != nil {
		t.Fatalf("performAction(patch menu apply) error = %v", err)
	}
	chooseTUIMenuEntry(t, app, "Apply Patch")
	submitTUIDialog(t, app, map[string]string{
		"patch_id": firstPatch.ID,
	})

	appliedPatch, _, err := service.PatchShow(firstPatch.ID)
	if err != nil {
		t.Fatalf("PatchShow(applied) error = %v", err)
	}
	if appliedPatch.Status != PatchStatusApplied {
		t.Fatalf("expected applied patch status, got %q", appliedPatch.Status)
	}

	rootContents, err := os.ReadFile(filepath.Join(workspace, "file.txt"))
	if err != nil {
		t.Fatalf("ReadFile(root file) error = %v", err)
	}
	if string(rootContents) != "child change\n" {
		t.Fatalf("expected root workspace to receive applied patch, got %q", string(rootContents))
	}

	writeFile(t, filepath.Join(childWorkspace, "file.txt"), "rejected change\n")

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodePatch}); err != nil {
		t.Fatalf("performAction(patch menu repropose) error = %v", err)
	}
	chooseTUIMenuEntry(t, app, "Propose Patch")
	submitTUIDialog(t, app, map[string]string{
		"target_project": rootProject.Slug,
		"target_node":    rootNode.Slug,
	})

	patches, err = service.PatchList("")
	if err != nil {
		t.Fatalf("PatchList() after repropose error = %v", err)
	}
	if len(patches) != 2 {
		t.Fatalf("expected two patches after reproposal, got %d", len(patches))
	}

	var secondPatch PatchProposal
	for _, patch := range patches {
		if patch.ID != firstPatch.ID {
			secondPatch = patch
			break
		}
	}
	if secondPatch.ID == "" {
		t.Fatalf("expected to find a second patch proposal")
	}

	if err := app.performAction(tuiActionSpec{ID: tuiActionNodePatch}); err != nil {
		t.Fatalf("performAction(patch menu reject) error = %v", err)
	}
	chooseTUIMenuEntry(t, app, "Reject Patch")
	submitTUIDialog(t, app, map[string]string{
		"patch_id": secondPatch.ID,
		"actor":    "tui-test",
		"note":     "not yet",
	})

	rejectedPatch, _, err := service.PatchShow(secondPatch.ID)
	if err != nil {
		t.Fatalf("PatchShow(rejected) error = %v", err)
	}
	if rejectedPatch.Status != PatchStatusRejected {
		t.Fatalf("expected rejected patch status, got %q", rejectedPatch.Status)
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
	if err := app.reloadPatches(); err != nil {
		t.Fatalf("reloadPatches() error = %v", err)
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

	for _, entry := range app.menu.Entries {
		if entry.Label != label {
			continue
		}
		if err := entry.Action(); err != nil {
			t.Fatalf("menu action %q error = %v", label, err)
		}
		app.menu = nil
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
