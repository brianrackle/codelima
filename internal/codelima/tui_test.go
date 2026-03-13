package codelima

import (
	"context"
	"path/filepath"
	"testing"
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

func TestDispatchTUIRunsInjectedRunner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	runner := &fakeTUIRunner{}
	service.tui = runner

	if _, err := dispatch(ctx, service, []string{"tui"}); err != nil {
		t.Fatalf("dispatch(tui) error = %v", err)
	}

	if runner.calls != 1 {
		t.Fatalf("expected runner to be called once, got %d", runner.calls)
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
							Status:             NodeStatusCreated,
						},
					},
				},
			},
		},
	}
}
