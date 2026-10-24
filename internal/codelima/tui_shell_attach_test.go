package codelima

import (
	"context"
	"testing"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

func TestTUIRawCreatedSessionAppearsOnceAcrossPushAndOpenReply(t *testing.T) {
	service, _ := newTestService(t)
	store := newTUISessionStore(context.Background(), service, nil)
	t.Cleanup(store.Close)
	state := daemon.TerminalState{TerminalID: "raw-created", TabID: "node:a#1", Target: "node:a", Kind: "node-host-shell", Label: "host"}
	event := tuiDaemonTerminalOpenedEvent{TargetKey: state.Target, ShellKind: terminal.NodeHostShell, State: state}
	first, err := store.installDaemonTab(event)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.applyOpenedDaemonTab(event)
	if err != nil || first != second || len(store.TargetSessionKeys(state.Target)) != 1 {
		t.Fatalf("created push/open reply duplicated or lost a tab: %q %q %v", first, second, err)
	}
}

func TestManagedGuestRuntimeCommandDoesNotAttachRecursively(t *testing.T) {
	service, workspace := newTestService(t)
	node, err := service.NodeCreate(context.Background(), NodeCreateInput{Slug: "runtime-shell", Directory: workspace})
	if err != nil {
		t.Fatal(err)
	}
	service.localTerminals = false
	_, err = dispatch(context.Background(), service, []string{"terminal", "run-node-shell", node.ID})
	if err != nil {
		t.Fatal(err)
	}
	if calls := recordedShellCalls(service.sandbox.(*fakeSandbox)); len(calls) == 0 || !calls[len(calls)-1].interactive {
		t.Fatalf("managed runtime did not invoke the direct interactive transport: %+v", calls)
	}
}
