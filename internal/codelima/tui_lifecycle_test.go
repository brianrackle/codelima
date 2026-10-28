package codelima

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/testutil"
	"go.rockorager.dev/vaxis"
)

func TestTUILifecycleCancelTargetsAdmittedStartAfterConfirmation(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	s.sandbox = outputOperationSandbox{SandboxClient: s.sandbox}
	home, host := testCLIOperationServer(t, s, Version)
	client, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	operations, err := host.nodeOperations()
	if err != nil {
		t.Fatal(err)
	}
	record, err := operations.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "start", Node: node.ID})
	if err != nil {
		t.Fatal(err)
	}
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		current, err := operations.Get(record.ID)
		return err == nil && current.CurrentPhase == "bootstrap"
	}, "startup reached blocked command")
	a, sessions := newTestManager(t, 80, 24)
	if err := a.applyReloadedNodes([]Node{node}, ""); err != nil {
		t.Fatal(err)
	}
	a.nodeDetail = true
	a.service = s.withIO(io.Discard, io.Discard)
	a.service.daemonClient = client
	a.startLifecycleLoad()
	a.handleKey(vaxis.Key{Keycode: 'x', Text: "x"})
	a.handleKey(vaxis.Key{Keycode: 's', Text: "s"})
	if a.overlay == nil {
		t.Fatal("startup cancellation omitted confirmation")
	}
	current, _ := operations.Get(record.ID)
	if current.State != "running" {
		t.Fatal("merely opening confirmation canceled startup", current.State)
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEsc})
	current, _ = operations.Get(record.ID)
	if current.State != "running" || a.lifecycle.queuedOperation != record.ID {
		t.Fatal("dismissing confirmation changed startup or attachment intent")
	}
	a.handleKey(vaxis.Key{Keycode: 's', Text: "s"})
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		current, err := operations.Get(record.ID)
		return err == nil && current.State == "canceled"
	}, "Starting cancellation stopped the VM")
	page, err := operations.ListPage("", 10)
	if err != nil || len(page.History) != 1 || page.History[0].ID != record.ID {
		t.Fatal("Starting cancellation submitted a competing operation", page, err)
	}
	node, err = s.NodeShow(context.Background(), node.ID)
	if err != nil || nodeIsRunning(node) || a.lifecycle.queuedOperation != "" || len(sessions.opened) != 0 {
		t.Fatal("cancellation left a VM or attachment intent", node, err)
	}
}

func TestTUILifecycleQueueFencesOlderReceiptAndRevokesOnFailure(t *testing.T) {
	a, sessions := newTestManager(t, 80, 24)
	a.nodeDetail = true
	id := a.state.selectedEntry().node.ID
	a.lifecycle = tuiLifecycleState{nodeID: id, loaded: true, serial: 3, operation: NodeOperation{ID: "old", NodeID: id, State: "succeeded"}}
	a.activity.page.Running = []NodeOperation{{ID: "new", NodeID: id, State: "running", Request: NodeOperationRequest{Kind: "start"}}}
	a.handleKey(vaxis.Key{Keycode: 'x', Text: "x"})
	a.finishQueuedLifecycleAttach()
	if a.lifecycle.queuedOperation != "new" || a.lifecycle.loaded || a.lifecycle.serial <= 3 {
		t.Fatal("stale latest receipt consumed the new attachment intent")
	}
	a.applyLifecycleData(tuiLifecycleDataEvent{nodeID: id, serial: 3, generation: a.managerGeneration, operation: NodeOperation{ID: "old", NodeID: id, State: "succeeded"}})
	if a.lifecycle.queuedOperation != "new" || len(sessions.opened) != 0 {
		t.Fatal("late response changed queued attachment")
	}
	a.applyLifecycleData(tuiLifecycleDataEvent{nodeID: id, serial: a.lifecycle.serial, generation: a.managerGeneration, operation: NodeOperation{ID: "new", NodeID: id, State: "failed"}})
	if a.lifecycle.queuedOperation != "" || len(sessions.opened) != 0 {
		t.Fatal("failed startup retained queued attachment")
	}
}

func TestTUILifecycleStartingShowsObservedPhasesAndOutputWithoutOpeningShell(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		a, sessions := newTestManager(t, size[0], size[1])
		a.nodeDetail = true
		a.applyKeyBindings(KeyBindingConfig{"node.logs": {"alt+l"}})
		if a.keyBindingError != "" {
			t.Fatal(a.keyBindingError)
		}
		node := a.state.selectedEntry().node
		started := time.Now().Add(-time.Minute)
		record := NodeOperation{ID: "start-id", NodeID: node.ID, State: "running", Request: NodeOperationRequest{Kind: "start"}, CurrentPhase: "boot", CreatedAt: started, Phases: initialNodeOperationPhases("start"), Output: []string{"BOOT-LINE"}}
		record.Phases[0].State = "succeeded"
		record.Phases[1].State, record.Phases[1].StartedAt = "running", &started
		a.activity.page.Running = []NodeOperation{record}
		a.lifecycle = tuiLifecycleState{nodeID: node.ID, loaded: true, operation: record}
		a.draw()
		screen := renderedScreenText(t, a.vx, size[0], size[1])
		for _, want := range []string{"Starting", "Boot", "Output", "BOOT-LINE", "cancel start", "Alt+l full logs"} {
			if !strings.Contains(screen, want) {
				t.Fatalf("missing %s at %v:\n%s", want, size, screen)
			}
		}
		if len(sessions.opened) != 0 {
			t.Fatal("viewing progress opened a shell")
		}
	}
}

func TestTUILifecycleCancelingDoesNotOfferQueuedAttachment(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		a, sessions := newTestManager(t, size[0], size[1])
		a.nodeDetail = true
		id := a.state.selectedEntry().node.ID
		a.lifecycle = tuiLifecycleState{nodeID: id, loaded: true, operation: NodeOperation{ID: "canceling", NodeID: id, State: "canceling", Request: NodeOperationRequest{Kind: "start"}}}
		a.draw()
		screen := renderedScreenText(t, a.vx, size[0], size[1])
		if !strings.Contains(screen, "Canceling start") || strings.Contains(screen, "attach when ready") {
			t.Fatal("canceling startup offered an unavailable attachment", screen)
		}
		a.handleKey(vaxis.Key{Keycode: 'x', Text: "x"})
		if a.lifecycle.queuedOperation != "" || len(sessions.opened) != 0 {
			t.Fatal("canceling startup accepted an attachment")
		}
	}
}

func TestTUILifecycleQueuedAttachRequiresMatchingReceiptAndReadiness(t *testing.T) {
	a, sessions := newTestManager(t, 80, 24)
	a.nodeDetail = true
	id := a.state.selectedEntry().node.ID
	cfg := DefaultConfig("")
	cfg.Shell.Mode = "embedded"
	a.preferences = &cfg
	a.lifecycle = tuiLifecycleState{nodeID: id, loaded: true, operation: NodeOperation{ID: "op", NodeID: id, State: "running", Request: NodeOperationRequest{Kind: "start"}}}
	a.handleKey(vaxis.Key{Keycode: 'x', Text: "x"})
	if a.lifecycle.queuedOperation != "op" || len(sessions.opened) != 0 {
		t.Fatal("queue opened early or lost identity")
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEsc})
	if a.lifecycle.queuedOperation != "" || !a.nodeDetail {
		t.Fatal("Escape did not cancel only the queued attachment")
	}
	a.handleKey(vaxis.Key{Keycode: 'x', Text: "x"})
	a.lifecycle.operation.State = "succeeded"
	a.finishQueuedLifecycleAttach()
	if len(sessions.opened) != 0 {
		t.Fatal("receipt completion alone opened an unready node")
	}
	nodes := slices.Clone(a.state.nodes)
	for i := range nodes {
		if nodes[i].ID == id {
			nodes[i].Status = NodeStatusRunning
			nodes[i].BootstrapCompleted = true
		}
	}
	if err := a.applyReloadedNodes(nodes, ""); err != nil {
		t.Fatal(err)
	}
	if len(sessions.opened) != 0 {
		t.Fatal("durable readiness without runtime observation opened a shell")
	}
	for i := range nodes {
		if nodes[i].ID == id {
			nodes[i].LastRuntimeObservation = &RuntimeObservation{Exists: true, Status: ObservationStopped}
		}
	}
	if err := a.applyReloadedNodes(nodes, ""); err != nil {
		t.Fatal(err)
	}
	if len(sessions.opened) != 0 {
		t.Fatal("stale running metadata overrode a stopped VM observation")
	}
	for i := range nodes {
		if nodes[i].ID == id {
			nodes[i].LastRuntimeObservation = &RuntimeObservation{Exists: true, Status: ObservationRunning}
		}
	}
	if err := a.applyReloadedNodes(nodes, ""); err != nil {
		t.Fatal(err)
	}
	if len(sessions.opened) != 1 || a.lifecycle.queuedOperation != "" {
		t.Fatal("ready receipt did not consume the queue exactly once", sessions.opened)
	}
	a.finishQueuedLifecycleAttach()
	if len(sessions.opened) != 1 {
		t.Fatal("queued attachment opened twice")
	}
}

func TestTUILifecycleNavigationRevokesQueuedAttachAndMetricsWaits(t *testing.T) {
	a, sessions := newTestManager(t, 80, 24)
	a.nodeDetail = true
	id := a.state.selectedEntry().node.ID
	a.lifecycle = tuiLifecycleState{nodeID: id, loaded: true, operation: NodeOperation{ID: "op", NodeID: id, State: "running", Request: NodeOperationRequest{Kind: "start"}}}
	a.handleKey(vaxis.Key{Keycode: 'x', Text: "x"})
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyTab})
	if a.detailTab == tuiDetailMetrics || a.metricsVisible() {
		t.Fatal("starting node exposed Metrics")
	}
	a.handleKey(vaxis.Key{Keycode: '2', Text: "2"})
	a.lifecycle.operation.State = "succeeded"
	a.finishQueuedLifecycleAttach()
	if a.lifecycle.queuedOperation != "" || len(sessions.opened) != 0 {
		t.Fatal("navigation retained an auto-attach intent")
	}
	if !strings.Contains(a.status, "attachment canceled") {
		t.Fatal("navigation left a stale queued-attachment status", a.status)
	}
}

func TestTUILifecycleRejectsLateDataAfterNavigation(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.nodeDetail = true
	id := a.state.selectedEntry().node.ID
	a.lifecycle = tuiLifecycleState{nodeID: id, serial: 3, inFlight: true}
	a.managerGeneration = 5
	late := tuiLifecycleDataEvent{nodeID: id, serial: 2, generation: 5, operation: NodeOperation{ID: "stale", NodeID: id}}
	a.applyLifecycleData(late)
	if a.lifecycle.operation.ID != "" || !a.lifecycle.inFlight {
		t.Fatal("stale reply replaced current load")
	}
	late.serial = 3
	late.generation = 4
	a.applyLifecycleData(late)
	if a.lifecycle.operation.ID != "" {
		t.Fatal("previous navigation replaced current operation")
	}
	late.generation = 5
	late.operation.ID = "current"
	a.applyLifecycleData(late)
	if a.lifecycle.operation.ID != "current" {
		t.Fatal("current receipt was rejected")
	}
	a.nodeDetail = false
	a.cancelHiddenLifecycleLoad()
	late.operation.ID = "after-exit"
	a.applyLifecycleData(late)
	if a.lifecycle.operation.ID == "after-exit" {
		t.Fatal("hidden view accepted a completion")
	}
}

func TestTUILifecycleFailureShowsRecordedCommandAndLiveVMSeparately(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		a, sessions := newTestManager(t, size[0], size[1])
		node := a.state.selectedEntry().node
		node.Status = NodeStatusFailed
		node.LastRuntimeObservation = &RuntimeObservation{Exists: true, Status: ObservationRunning}
		if err := a.applyReloadedNodes([]Node{node}, ""); err != nil {
			t.Fatal(err)
		}
		a.nodeDetail = true
		start := time.Now().Add(-time.Minute)
		end := time.Now()
		exit := 23
		duration := int64(1200)
		a.lifecycle = tuiLifecycleState{nodeID: node.ID, loaded: true, operation: NodeOperation{ID: "failed", NodeID: node.ID, State: "failed", CurrentPhase: "bootstrap", CreatedAt: start, FinishedAt: &end, Error: "FAILED-MARKER"}}
		stepStart := start.Add(time.Second)
		a.spec = tuiSpecState{nodeID: node.ID, loaded: true, snapshot: nodeSpecSnapshot{Node: node, Bootstrap: BootstrapState{Steps: []BootstrapStep{
			{Kind: "bootstrap", State: BootstrapStepSucceeded, Command: "first"},
			{Kind: "bootstrap", State: BootstrapStepFailed, Command: "printf FAILED-MARKER; exit 23", SourceKind: "environment", SourceID: "env-id", SourceName: "qa-env", AttemptID: "attempt-id", StartedAt: &stepStart, FinishedAt: &end, DurationMillis: &duration, ExitCode: &exit, Error: "FAILED-MARKER"},
		}}}}
		a.draw()
		screen := renderedScreenText(t, a.vx, size[0], size[1])
		for _, want := range []string{"Bootstrap failed", "step 2", "exit 23", "qa-env", "VM running", "FAILED-MARKER"} {
			if !strings.Contains(screen, want) {
				t.Fatalf("missing %s at %v:\n%s", want, size, screen)
			}
		}
		if len(sessions.opened) != 0 {
			t.Fatal("failure inspection opened a shell")
		}
		a.lifecycle.operation.CurrentPhase = "boot"
		a.lifecycle.operation.Error = "firmware unavailable"
		a.draw()
		screen = renderedScreenText(t, a.vx, size[0], size[1])
		if !strings.Contains(screen, "Boot failed") || strings.Contains(screen, "Bootstrap failed at step") {
			t.Fatal("stale bootstrap failure hid the latest boot failure", screen)
		}
	}
}

func TestTUILifecycleRetainsBootFailureAfterStatusRollbackAndInterruption(t *testing.T) {
	a, sessions := newTestManager(t, 80, 24)
	node := a.state.selectedEntry().node
	node.Status = NodeStatusStopped
	node.LastRuntimeObservation = &RuntimeObservation{Exists: true, Status: ObservationStopped}
	if err := a.applyReloadedNodes([]Node{node}, ""); err != nil {
		t.Fatal(err)
	}
	a.nodeDetail = true
	a.lifecycle = tuiLifecycleState{nodeID: node.ID, loaded: true, operation: NodeOperation{ID: "boot-failed", NodeID: node.ID, State: "failed", CurrentPhase: "boot", Error: "firmware unavailable"}}
	a.draw()
	screen := renderedScreenText(t, a.vx, 80, 24)
	for _, want := range []string{"Boot failed", "firmware unavailable", "VM stopped", "setup failed"} {
		if !strings.Contains(screen, want) {
			t.Fatal("boot failure disappeared after metadata rollback", want, screen)
		}
	}
	a.handleKey(vaxis.Key{Keycode: 'x', Text: "x"})
	if len(sessions.opened) != 0 {
		t.Fatal("failed boot opened a shell")
	}
	node.Status = NodeStatusProvisioning
	if err := a.applyReloadedNodes([]Node{node}, ""); err != nil {
		t.Fatal(err)
	}
	a.lifecycle.operation.State = "interrupted"
	a.draw()
	screen = renderedScreenText(t, a.vx, 80, 24)
	if a.nodeStarting() || !strings.Contains(screen, "Startup interrupted") {
		t.Fatal("interrupted receipt masqueraded as active startup", screen)
	}
}
