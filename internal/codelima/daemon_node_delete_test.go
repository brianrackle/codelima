package codelima

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"go.rockorager.dev/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func newDaemonDeleteFixture(t *testing.T) (*daemonHost, *Service, []Node, *fakeSandbox) {
	t.Helper()
	service, nodes, sandbox := batchFixture(t, 2)
	fake := sandbox.SandboxClient.(*fakeSandbox)
	service.sandbox = fake
	host := newDaemonHost(service)
	host.terminalFactory = func(string, func(vaxis.Event)) tuiTerminal { return newFakeHandoffTerminal() }
	t.Cleanup(func() { _ = host.Close() })
	return host, service, nodes, fake
}

func openDeleteFixtureTerminal(t *testing.T, host *daemonHost, node Node, kind string) daemon.TerminalState {
	t.Helper()
	state, err := host.open(context.Background(), terminalOpenParams{Target: "node:" + node.ID, Kind: kind}, "")
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestDaemonNodeDeleteClosesSessionsWithoutFrontend(t *testing.T) {
	for _, scenario := range []string{"single", "partial-batch", "runtime-failure", "event-failure", "session-failure"} {
		t.Run(scenario, func(t *testing.T) {
			host, service, nodes, fake := newDaemonDeleteFixture(t)
			for _, node := range nodes {
				openDeleteFixtureTerminal(t, host, node, "node-host-shell")
				openDeleteFixtureTerminal(t, host, node, "node-shell")
			}
			wantState := "succeeded"
			request := NodeOperationRequest{ID: newID(), Kind: "delete", Node: nodes[0].ID}
			switch scenario {
			case "partial-batch":
				service.sandbox = &deleteFailingSandbox{fakeSandbox: fake, failInstances: map[string]bool{nodes[1].SandboxName: true}}
				request.Kind, request.Node = "batch", ""
				for _, node := range nodes {
					request.Batch = append(request.Batch, NodeBatchTarget{NodeID: node.ID, NodeSlug: node.Slug, Kind: "delete"})
				}
				wantState = "failed"
			case "runtime-failure":
				service.sandbox = &deleteFailingSandbox{fakeSandbox: fake, failInstances: map[string]bool{nodes[0].SandboxName: true}}
				wantState = "failed"
			case "event-failure":
				path := service.store.nodeEventsPath(nodes[0].ID)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				wantState = "failed"
			case "session-failure":
				if err := os.Remove(host.session); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(host.session, 0o700); err != nil {
					t.Fatal(err)
				}
				wantState = "failed"
			}
			operations, err := host.nodeOperations()
			if err != nil {
				t.Fatal(err)
			}
			record, err := operations.Submit(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			result := waitNodeOperation(t, operations, record.ID, wantState)
			if scenario == "partial-batch" && (result.Batch.Succeeded != 1 || result.Batch.Failed != 1) {
				t.Fatalf("lost partial deletion outcomes: %+v", result.Batch)
			}
			wantTerminals := 2
			if scenario == "runtime-failure" {
				wantTerminals = 4
			}
			if got := host.TerminalCount(); got != wantTerminals {
				t.Fatalf("terminal count after %s = %d, want %d without any frontend", scenario, got, wantTerminals)
			}
			for _, state := range host.list() {
				if scenario != "runtime-failure" && state.Target == "node:"+nodes[0].ID {
					t.Fatalf("deleted node still owns terminal %+v", state)
				}
			}
			if scenario != "session-failure" {
				if got := len(readPersistedSession(t, host.session).Terminals); got != wantTerminals {
					t.Fatalf("persisted terminal count = %d, want %d", got, wantTerminals)
				}
			}
		})
	}
}

type deletionRacingTerminal struct {
	*fakeHandoffTerminal
	entered chan struct{}
	release chan struct{}
}

func (t *deletionRacingTerminal) Start(*exec.Cmd) error {
	close(t.entered)
	<-t.release
	return nil
}

func TestDaemonNodeDeleteFencesLateTerminalPublication(t *testing.T) {
	for _, useSlug := range []bool{false, true} {
		t.Run(map[bool]string{false: "id", true: "slug"}[useSlug], func(t *testing.T) {
			host, service, nodes, _ := newDaemonDeleteFixture(t)
			term := &deletionRacingTerminal{fakeHandoffTerminal: newFakeHandoffTerminal(), entered: make(chan struct{}), release: make(chan struct{})}
			release := sync.OnceFunc(func() { close(term.release) })
			t.Cleanup(release)
			host.terminalFactory = func(string, func(vaxis.Event)) tuiTerminal { return term }
			target := nodes[0].ID
			if useSlug {
				target = nodes[0].Slug
			}
			opened := make(chan error, 1)
			go func() {
				_, err := host.open(context.Background(), terminalOpenParams{Target: "node:" + target, Kind: "node-host-shell"}, "")
				opened <- err
			}()
			select {
			case <-term.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("terminal did not reach launch")
			}
			operations, err := host.nodeOperations()
			if err != nil {
				t.Fatal(err)
			}
			record, err := operations.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "delete", Node: nodes[0].ID})
			if err != nil {
				t.Fatal(err)
			}
			waitNodeOperation(t, operations, record.ID, "succeeded")
			if useSlug {
				noAuth := false
				replacement, err := service.NodeCreate(context.Background(), NodeCreateInput{Directory: nodes[0].DirectoryPath, Slug: nodes[0].Slug, ImportHostAuth: &noAuth})
				if err != nil || replacement.ID == nodes[0].ID {
					t.Fatalf("could not reuse deleted slug: %+v, %v", replacement, err)
				}
			}
			release()
			select {
			case err := <-opened:
				if err == nil {
					t.Error("published a terminal after its node was deleted")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("terminal open did not finish")
			}
			if host.TerminalCount() != 0 {
				t.Error("late open survived deletion")
			}
			waitForHandoff(t, time.Second, func() bool {
				term.mu.Lock()
				defer term.mu.Unlock()
				return term.closed
			}, "rejected terminal runtime teardown")
		})
	}
}

func TestDaemonNodeDeleteRetainsOwnershipThroughTerminalCleanup(t *testing.T) {
	host, service, nodes, fake := newDaemonDeleteFixture(t)
	openDeleteFixtureTerminal(t, host, nodes[0], "node-host-shell")
	fake.deleteGate = newFakeSandboxGate()
	release := sync.OnceFunc(func() { close(fake.deleteGate.release) })
	t.Cleanup(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	response := make(chan error, 1)
	go func() { _, err := host.runLegacyNodeOperation(ctx, "delete", nodes[0].ID); response <- err }()
	awaitFakeSandboxGate(t, fake.deleteGate, nodes[0].SandboxName)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	_, err = host.update(binary)
	var rpcErr *daemon.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Category != string(CategoryPreconditionFailed) {
		t.Fatalf("update admitted during deletion: %v", err)
	}
	cancel()
	select {
	case err := <-response:
		if err == nil {
			t.Fatal("disconnected caller got success before deletion")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("caller cancellation did not stop waiting")
	}
	// Simulate an update holding its write barrier while the runtime finishes.
	// The active receipt must prevent handoff until session cleanup commits.
	host.updateGate.Lock()
	locked := true
	defer func() {
		if locked {
			host.updateGate.Unlock()
		}
	}()
	release()
	waitForHandoff(t, time.Second, func() bool {
		node, err := service.store.NodeByID(nodes[0].ID)
		return err == nil && node.DeletedAt != nil
	}, "committed deletion")
	if !host.activeNodeOperations() {
		t.Error("released operation ownership before terminal cleanup")
	}
	host.updateGate.Unlock()
	locked = false
	operations, err := host.nodeOperations()
	if err != nil {
		t.Fatal(err)
	}
	records := operations.List()
	if len(records) != 1 {
		t.Fatalf("operations = %+v", records)
	}
	waitNodeOperation(t, operations, records[0].ID, "succeeded")
	if host.TerminalCount() != 0 {
		t.Fatal("disconnected deletion left its host terminal")
	}
}

func TestDaemonNodeDeleteRefusesTerminalRestore(t *testing.T) {
	host, service, nodes, _ := newDaemonDeleteFixture(t)
	openDeleteFixtureTerminal(t, host, nodes[0], "node-host-shell")
	// A crash may leave an older session snapshot beside committed deletion.
	if _, err := service.NodeDelete(context.Background(), nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	restored := newDaemonHost(service)
	restored.restore = "respawn"
	launched := false
	restored.terminalFactory = func(string, func(vaxis.Event)) tuiTerminal { launched = true; return newFakeHandoffTerminal() }
	t.Cleanup(func() { _ = restored.Close() })
	if err := restored.restoreSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if launched || restored.TerminalCount() != 0 {
		t.Fatal("restored a terminal for a deleted node")
	}
	_, err := restored.open(context.Background(), terminalOpenParams{Target: "node:" + nodes[0].ID, Kind: "node-host-shell"}, "")
	var rpcErr *daemon.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Category != string(CategoryNotFound) {
		t.Fatalf("open deleted target = %v, want NotFound", err)
	}
}

func TestDaemonNodeDeleteCanonicalTerminalOwnership(t *testing.T) {
	host, _, nodes, _ := newDaemonDeleteFixture(t)
	state, err := host.open(context.Background(), terminalOpenParams{Target: "node:" + nodes[0].Slug, Kind: "node-host-shell"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if state.Target != "node:"+nodes[0].ID {
		t.Fatalf("terminal retained a reusable slug: %+v", state)
	}
	if _, err := host.runLegacyNodeOperation(context.Background(), "delete", nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	if host.TerminalCount() != 0 {
		t.Fatal("deletion missed a terminal opened by slug")
	}
}
