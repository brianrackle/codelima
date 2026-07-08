package codelima

import (
	"context"
	"strings"
	"testing"

	"git.sr.ht/~rockorager/vaxis"
)

// This file pins today's OBSERVABLE terminal-identity behavior before the
// TargetKey migration (IMPROVEMENT_PLAN Part E, Track 1 PR1). Every expectation
// here is written with literal strings ("project:<id>", "node:<id>",
// "<target>#<n>") so it is an independent oracle: it must stay green,
// unmodified, both before and after the migration. If any assertion in this
// file has to change to pass, semantics changed and the migration is wrong.

func noopPostEvent(vaxis.Event) {}

// withFakeSessionTerminals swaps the terminal factory so real
// tuiSessionStore.Open*Tab paths allocate in-memory fakes instead of spawning
// PTY-backed shells. Restores on cleanup. Tests using it must not be parallel:
// newSessionTUITerminal is a package-global.
func withFakeSessionTerminals(t *testing.T) {
	t.Helper()
	original := newSessionTUITerminal
	newSessionTUITerminal = func(string, func(vaxis.Event)) tuiTerminal {
		return newFakeTUITerminal()
	}
	t.Cleanup(func() { newSessionTUITerminal = original })
}

// TestCharacterizeSessionKeyFormatAndMonotonicCounter pins that each opened tab
// is keyed "<targetKey>#<n>" with a per-target counter that only ever
// increments and never decrements or reuses a number when a tab is closed.
func TestCharacterizeSessionKeyFormatAndMonotonicCounter(t *testing.T) {
	withFakeSessionTerminals(t)

	ctx := context.Background()
	service, _ := newTestService(t)
	store := newTUISessionStore(ctx, service, noopPostEvent)

	node := Node{ID: "n1", Slug: "n1", Status: NodeStatusRunning}

	k1, err := store.OpenNodeTab(node)
	if err != nil {
		t.Fatalf("OpenNodeTab() #1 error = %v", err)
	}
	if k1 != "node:n1#1" {
		t.Fatalf("expected first tab key node:n1#1, got %q", k1)
	}

	k2, err := store.OpenNodeTab(node)
	if err != nil {
		t.Fatalf("OpenNodeTab() #2 error = %v", err)
	}
	if k2 != "node:n1#2" {
		t.Fatalf("expected second tab key node:n1#2, got %q", k2)
	}

	k3, err := store.OpenNodeTab(node)
	if err != nil {
		t.Fatalf("OpenNodeTab() #3 error = %v", err)
	}
	if k3 != "node:n1#3" {
		t.Fatalf("expected third tab key node:n1#3, got %q", k3)
	}

	// Closing the middle tab must not decrement or free the counter.
	store.CloseSession(k2)
	k4, err := store.OpenNodeTab(node)
	if err != nil {
		t.Fatalf("OpenNodeTab() after close error = %v", err)
	}
	if k4 != "node:n1#4" {
		t.Fatalf("expected counter to keep climbing to node:n1#4, got %q", k4)
	}
}

// TestCharacterizeTabOrderingAcrossOpenCloseReopen pins that TargetSessionKeys
// reports a target's tabs in open order, that closing removes only the closed
// tab, and that reopening appends the new tab at the end.
func TestCharacterizeTabOrderingAcrossOpenCloseReopen(t *testing.T) {
	withFakeSessionTerminals(t)

	ctx := context.Background()
	service, _ := newTestService(t)
	store := newTUISessionStore(ctx, service, noopPostEvent)

	node := Node{ID: "n1", Slug: "n1", Status: NodeStatusRunning}

	k1, _ := store.OpenNodeTab(node)
	k2, _ := store.OpenNodeTab(node)
	k3, _ := store.OpenNodeTab(node)

	if got := strings.Join(store.TargetSessionKeys("node:n1"), ","); got != k1+","+k2+","+k3 {
		t.Fatalf("expected open order %q, got %q", k1+","+k2+","+k3, got)
	}

	store.CloseSession(k2)
	if got := strings.Join(store.TargetSessionKeys("node:n1"), ","); got != k1+","+k3 {
		t.Fatalf("expected middle tab removed, got %q", got)
	}

	k4, _ := store.OpenNodeTab(node)
	if got := strings.Join(store.TargetSessionKeys("node:n1"), ","); got != k1+","+k3+","+k4 {
		t.Fatalf("expected reopened tab appended last, got %q", got)
	}
}

// TestCharacterizeTargetKeyStringFormsProduced pins the exact "project:<id>" /
// "node:<id>" strings produced by the tree-entry key builder and by the session
// store's open paths (session.target and the returned session key prefix).
func TestCharacterizeTargetKeyStringFormsProduced(t *testing.T) {
	withFakeSessionTerminals(t)

	if got := (tuiTreeEntry{kind: tuiTreeEntryProject, project: Project{ID: "p1"}}).key(); got != "project:p1" {
		t.Fatalf("expected project entry key project:p1, got %q", got)
	}
	if got := (tuiTreeEntry{kind: tuiTreeEntryNode, node: Node{ID: "n1"}}).key(); got != "node:n1" {
		t.Fatalf("expected node entry key node:n1, got %q", got)
	}
	// The empty/unknown entry renders as the empty-string sentinel.
	if got := (tuiTreeEntry{}).key(); got != "" {
		t.Fatalf("expected empty entry key to be empty, got %q", got)
	}

	ctx := context.Background()
	service, _ := newTestService(t)
	store := newTUISessionStore(ctx, service, noopPostEvent)

	nodeKey, err := store.OpenNodeTab(Node{ID: "n1", Slug: "n1", Status: NodeStatusRunning})
	if err != nil {
		t.Fatalf("OpenNodeTab() error = %v", err)
	}
	if nodeKey != "node:n1#1" {
		t.Fatalf("expected node session key node:n1#1, got %q", nodeKey)
	}
	if session, ok := store.Session(nodeKey); !ok || session.target != "node:n1" {
		t.Fatalf("expected node session target node:n1, got %+v (ok=%v)", session, ok)
	}

	projectKey, err := store.OpenProjectTab(Project{ID: "p1", Slug: "p1", WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenProjectTab() error = %v", err)
	}
	if projectKey != "project:p1#1" {
		t.Fatalf("expected project session key project:p1#1, got %q", projectKey)
	}
	if session, ok := store.Session(projectKey); !ok || session.target != "project:p1" {
		t.Fatalf("expected project session target project:p1, got %+v (ok=%v)", session, ok)
	}
}

// TestCharacterizeTargetKeyConsumption pins how the string target keys are
// parsed back into projects/nodes: entryForKey resolves both the "project:" and
// "node:" prefixes (including via the by-ID fallback when the entry is not
// currently visible), rejects unknown/empty keys, and activeProject/activeNode
// resolve from the focused terminal target string.
func TestCharacterizeTargetKeyConsumption(t *testing.T) {
	t.Parallel()

	state, err := newTUIState(testTUITree(t), nil)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	if entry, ok := state.entryForKey("project:project-root"); !ok || entry.kind != tuiTreeEntryProject || entry.project.ID != "project-root" {
		t.Fatalf("expected project:project-root to resolve to project-root, got %+v (ok=%v)", entry, ok)
	}
	if entry, ok := state.entryForKey("node:node-root"); !ok || entry.kind != tuiTreeEntryNode || entry.node.ID != "node-root" {
		t.Fatalf("expected node:node-root to resolve to node-root, got %+v (ok=%v)", entry, ok)
	}
	if _, ok := state.entryForKey("bogus"); ok {
		t.Fatalf("expected malformed key to resolve to nothing")
	}
	if _, ok := state.entryForKey(""); ok {
		t.Fatalf("expected empty key to resolve to nothing")
	}

	// Collapse the root project so node-root is no longer a visible entry; the
	// by-ID fallback in entryForKey must still parse "node:node-root".
	state.expanded["project-root"] = false
	state.rebuildEntries()
	if state.findEntryByKey("node:node-root") >= 0 {
		t.Fatalf("expected node-root to be hidden while its project is collapsed")
	}
	if entry, ok := state.entryForKey("node:node-root"); !ok || entry.kind != tuiTreeEntryNode || entry.node.ID != "node-root" {
		t.Fatalf("expected collapsed node:node-root to resolve via by-ID fallback, got %+v (ok=%v)", entry, ok)
	}

	// activeProject/activeNode resolve from the focused terminal target string
	// when the selection itself is not the resolving kind.
	state.selection = -1
	state.focus = tuiFocusTerminal
	state.terminalTarget = "node:node-child"
	if node, ok := state.activeNode(); !ok || node.ID != "node-child" {
		t.Fatalf("expected terminalTarget node:node-child to resolve activeNode, got %+v (ok=%v)", node, ok)
	}
	state.terminalTarget = "project:project-child"
	if project, ok := state.activeProject(); !ok || project.ID != "project-child" {
		t.Fatalf("expected terminalTarget project:project-child to resolve activeProject, got %+v (ok=%v)", project, ok)
	}
}

// TestCharacterizeActiveTabFallbackToFirstKey pins targetActiveSessionKey's
// fallback: it returns the recorded active tab when that tab is still open,
// otherwise it falls back to the target's first open tab.
func TestCharacterizeActiveTabFallbackToFirstKey(t *testing.T) {
	withFakeSessionTerminals(t)

	ctx := context.Background()
	service, _ := newTestService(t)
	store := newTUISessionStore(ctx, service, noopPostEvent)
	state, err := newTUIState(testTUITree(t), newSharedFakeTUISessionManager(store))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	node := Node{ID: "n1", Slug: "n1", Status: NodeStatusRunning}
	k1, _ := store.OpenNodeTab(node)
	k2, _ := store.OpenNodeTab(node)

	// Recorded active tab is honored while it is open.
	state.setActiveTab("node:n1", k2)
	if got := state.targetActiveSessionKey("node:n1"); got != k2 {
		t.Fatalf("expected recorded active tab %q, got %q", k2, got)
	}

	// When the recorded active tab is gone, fall back to the first open tab.
	store.CloseSession(k2)
	if got := state.targetActiveSessionKey("node:n1"); got != k1 {
		t.Fatalf("expected fallback to first open tab %q, got %q", k1, got)
	}

	// With no open tabs, there is no active tab.
	store.CloseSession(k1)
	if got := state.targetActiveSessionKey("node:n1"); got != "" {
		t.Fatalf("expected empty active tab when none are open, got %q", got)
	}
}

// TestCharacterizeSessionErrorLifecycleKeyedByTarget pins that an open failure
// records an error keyed by TARGET (not session key), that the error is
// retrievable via the target, that closing the target's tabs clears it, and
// that a subsequent successful open also clears it.
func TestCharacterizeSessionErrorLifecycleKeyedByTarget(t *testing.T) {
	withFakeSessionTerminals(t)

	ctx := context.Background()
	service, _ := newTestService(t)
	store := newTUISessionStore(ctx, service, noopPostEvent)

	// An empty workspace path fails before any tab is created.
	if _, err := store.OpenProjectTab(Project{ID: "p1", Slug: "p1", WorkspacePath: ""}); err == nil {
		t.Fatalf("expected OpenProjectTab with empty workspace to fail")
	}
	if store.SessionError("project:p1") == nil {
		t.Fatalf("expected an error recorded under the target key project:p1")
	}
	// The error is keyed by target, so it is not addressable as a session key.
	if store.SessionError("project:p1#1") != nil {
		t.Fatalf("expected no error recorded under a session key")
	}

	// Closing the target's tabs clears the recorded error.
	store.CloseTargetSessions("project:p1")
	if store.SessionError("project:p1") != nil {
		t.Fatalf("expected CloseTargetSessions to clear the recorded error")
	}

	// Re-arm the error, then a successful open must clear it.
	if _, err := store.OpenProjectTab(Project{ID: "p1", Slug: "p1", WorkspacePath: ""}); err == nil {
		t.Fatalf("expected second failing OpenProjectTab to fail")
	}
	if store.SessionError("project:p1") == nil {
		t.Fatalf("expected error re-armed under project:p1")
	}
	if _, err := store.OpenProjectTab(Project{ID: "p1", Slug: "p1", WorkspacePath: t.TempDir()}); err != nil {
		t.Fatalf("expected successful OpenProjectTab, got %v", err)
	}
	if store.SessionError("project:p1") != nil {
		t.Fatalf("expected a successful open to clear the recorded error")
	}
}

// TestCharacterizeSessionErrorClearedOnReloadWhenTargetRemoved pins that a
// reload prunes a target-keyed session error when that target no longer exists
// in the reloaded tree.
func TestCharacterizeSessionErrorClearedOnReloadWhenTargetRemoved(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	store := newTUISessionStore(ctx, service, noopPostEvent)
	state, err := newTUIState(testTUITree(t), newSharedFakeTUISessionManager(store))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	app := &vaxisTUIApp{ctx: ctx, service: service, state: state, sessions: store}

	// Record an error for a target that exists in the current tree.
	if _, err := store.OpenProjectTab(Project{ID: "project-root", Slug: "root", WorkspacePath: ""}); err == nil {
		t.Fatalf("expected OpenProjectTab with empty workspace to fail")
	}
	if store.SessionError("project:project-root") == nil {
		t.Fatalf("expected error recorded for project:project-root")
	}

	// Reloading to a tree without that project prunes the stale error.
	if err := app.applyReloadedTree([]ProjectTreeNode{}, ""); err != nil {
		t.Fatalf("applyReloadedTree() error = %v", err)
	}
	if store.SessionError("project:project-root") != nil {
		t.Fatalf("expected reload to prune the error for the removed target")
	}
}
