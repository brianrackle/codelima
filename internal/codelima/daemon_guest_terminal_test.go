package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

// newGuestShellGateHost builds a daemon host over a real store, with a terminal
// factory that spawns nothing: every assertion here is about the precondition
// the handler checks before it reaches a runtime.
func newGuestShellGateHost(t *testing.T) (*daemonHost, *Service, Node) {
	t.Helper()

	service, workspace := newHandoffTestService(t)
	now := time.Now().UTC()
	node := Node{
		ID:            newID(),
		Slug:          "gate-node",
		SandboxName:   "gate-node",
		DirectoryPath: workspace,
		Status:        NodeStatusProvisioning,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := service.store.SaveNode(node, BootstrapState{}); err != nil {
		t.Fatalf("SaveNode() error = %v", err)
	}

	host := newDaemonHost(service)
	host.terminalFactory = func(string, func(vaxis.Event)) tuiTerminal { return newFakeHandoffTerminal() }
	t.Cleanup(func() { _ = host.Close() })
	return host, service, node
}

// completeNodeStart persists what NodeStart writes when bootstrap and
// validation are done. Running is deliberately not a durable value — node.yaml
// keeps only in-flight and terminal lifecycle states — so what the record
// carries afterwards is the absence of an in-flight claim.
func completeNodeStart(t *testing.T, service *Service, node Node) {
	t.Helper()
	node.Status = NodeStatusRunning
	node.LifecycleState = ""
	node.BootstrapCompleted = true
	node.UpdatedAt = time.Now().UTC()
	if err := service.store.SaveNode(node, BootstrapState{}); err != nil {
		t.Fatalf("SaveNode(running) error = %v", err)
	}
}

// A guest shell opened while the start is still bootstrapping lands in a booted
// VM that has no agents, no npm, and no imported credentials. The daemon is the
// only gate any non-TUI client passes, so it refuses with a reason rather than
// handing out that shell.
func TestDaemonTerminalOpenRefusesGuestShellWhileNodeIsBootstrapping(t *testing.T) {
	host, _, node := newGuestShellGateHost(t)

	_, err := host.open(context.Background(), terminalOpenParams{
		Target: "node:" + node.ID,
		Kind:   "node-shell",
		Cols:   80,
		Rows:   24,
	}, "")
	if err == nil {
		t.Fatal("terminal.open served a guest shell for a bootstrapping node")
	}
	var rpcErr *daemon.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("terminal.open error = %T %v, want a typed daemon error", err, err)
	}
	if rpcErr.Category != string(CategoryPreconditionFailed) || rpcErr.Code != ExitPreconditionFailed {
		t.Fatalf("terminal.open error = %#v, want a PreconditionFailed rejection", rpcErr)
	}
	if !strings.Contains(rpcErr.Message, node.Slug) || !strings.Contains(rpcErr.Message, "still bootstrapping") {
		t.Fatalf("terminal.open message = %q, want it to name the node and the reason", rpcErr.Message)
	}
	if host.TerminalCount() != 0 {
		t.Fatalf("refused open left %d terminals behind", host.TerminalCount())
	}
}

// The host shell is a shell on this machine rooted at the node directory. It
// never needed the guest, so the bootstrap gate must not reach it.
func TestDaemonTerminalOpenServesHostShellWhileNodeIsBootstrapping(t *testing.T) {
	host, _, node := newGuestShellGateHost(t)

	state, err := host.open(context.Background(), terminalOpenParams{
		Target: "node:" + node.ID,
		Kind:   "node-host-shell",
		Cols:   80,
		Rows:   24,
	}, "")
	if err != nil {
		t.Fatalf("terminal.open(node-host-shell) during bootstrap error = %v", err)
	}
	if state.Kind != terminal.NodeHostShell.String() {
		t.Fatalf("opened terminal kind = %q, want %q", state.Kind, terminal.NodeHostShell.String())
	}
}

// The gate closes only for the bootstrap window: once the start has cleared the
// node's in-flight claim, the same request is served.
func TestDaemonTerminalOpenServesGuestShellAfterBootstrapCompletes(t *testing.T) {
	host, service, node := newGuestShellGateHost(t)
	completeNodeStart(t, service, node)

	state, err := host.open(context.Background(), terminalOpenParams{
		Target: "node:" + node.ID,
		Kind:   "node-shell",
		Cols:   80,
		Rows:   24,
	}, "")
	if err != nil {
		t.Fatalf("terminal.open(node-shell) after bootstrap error = %v", err)
	}
	if state.Kind != terminal.NodeShell.String() {
		t.Fatalf("opened terminal kind = %q, want %q", state.Kind, terminal.NodeShell.String())
	}
	if state.Label != node.Slug {
		t.Fatalf("opened terminal label = %q, want %q", state.Label, node.Slug)
	}
}

// Restore after a daemon restart replays persisted tabs through the same open
// path. A started node's record carries no in-flight claim, so its guest tabs
// must come back exactly as they did before the gate existed.
func TestDaemonRestoreReopensGuestTabsForStartedNodes(t *testing.T) {
	host, service, node := newGuestShellGateHost(t)
	completeNodeStart(t, service, node)

	session := daemon.Session{
		Version: daemon.SessionVersion,
		Terminals: []daemon.TerminalState{{
			TerminalID: "term_restored",
			TabID:      "node:" + node.ID + "#term_restored",
			Target:     "node:" + node.ID,
			Kind:       terminal.NodeShell.String(),
			Label:      node.Slug,
			Cols:       80,
			Rows:       24,
			CreatedAt:  time.Now().UTC(),
		}},
	}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(host.session, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := host.restoreSession(context.Background()); err != nil {
		t.Fatalf("restoreSession() error = %v", err)
	}
	states := host.list()
	if len(states) != 1 {
		t.Fatalf("restored terminals = %#v, want the persisted guest tab", states)
	}
	if states[0].TerminalID != "term_restored" || states[0].Kind != terminal.NodeShell.String() {
		t.Fatalf("restored terminal = %#v, want the persisted guest tab identity", states[0])
	}
}
