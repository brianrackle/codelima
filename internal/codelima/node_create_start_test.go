package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/testutil"
	"go.rockorager.dev/vaxis"
)

type createStartSandbox struct {
	SandboxClient
	entered  chan Node
	release  chan struct{}
	startErr error
}

func (s createStartSandbox) Start(ctx context.Context, node Node) error {
	if err := s.SandboxClient.Start(ctx, node); err != nil {
		return err
	}
	s.entered <- node
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.release:
		return s.startErr
	}
}

func TestNodeOperationsCreateStartSurvivesClientDisconnect(t *testing.T) {
	s, _ := newTestService(t)
	gate := createStartSandbox{SandboxClient: s.sandbox, entered: make(chan Node, 1), release: make(chan struct{})}
	s.sandbox = gate
	home, _ := testCLIOperationServer(t, s, Version)
	client, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	frontend := s.withIO(io.Discard, io.Discard)
	frontend.daemonClient = client
	receipt := make(chan string, 1)
	frontend.operationReceipt = func(id string) { receipt <- id }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	input := createStartInput(t)
	go func() { _, err := frontend.NodeCreate(ctx, input); done <- err }()
	var id string
	select {
	case id = <-receipt:
	case <-time.After(3 * time.Second):
		t.Fatal("create-and-start admission was not acknowledged")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("caller did not stop waiting", err)
	}
	_ = client.Close()
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect prevented first boot")
	}
	observer, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = observer.Close() }()
	var running NodeOperation
	if err := observer.Call(context.Background(), "operation.get", map[string]string{"id": id}, &running); err != nil || running.NodeID == "" || !running.Request.startsNode() || running.State != "running" {
		t.Fatal("independent frontend cannot inspect continuing startup", running, err)
	}
	close(gate.release)
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		var record NodeOperation
		return observer.Call(context.Background(), "operation.get", map[string]string{"id": id}, &record) == nil && record.State == "succeeded"
	}, "create-and-start finishes after requester disconnect")
}

func TestNodeOperationsCreateStartFailureRetainsCreatedIdentity(t *testing.T) {
	for _, failedStage := range []string{"create", "start"} {
		t.Run(failedStage, func(t *testing.T) {
			s, _ := newTestService(t)
			gate := createStartSandbox{SandboxClient: s.sandbox, entered: make(chan Node, 1), release: make(chan struct{})}
			if failedStage == "create" {
				s.sandbox.(*fakeSandbox).createErr = errors.New("fixture create failure")
			} else {
				gate.startErr = errors.New("fixture boot failure")
			}
			close(gate.release)
			s.sandbox = gate
			ops := testNodeOperations(t, s)
			input := createStartInput(t)
			op, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "create", Create: &input})
			if err != nil {
				t.Fatal(err)
			}
			record := waitNodeOperation(t, ops, op.ID, "failed")
			if failedStage == "create" {
				if record.NodeID != "" || len(gate.entered) != 0 {
					t.Fatal("failed creation invented a node or started a VM", record)
				}
			} else {
				if record.NodeID == "" || !strings.Contains(record.Error, "fixture boot failure") {
					t.Fatal("failed first boot lost node identity", record)
				}
				if latest, err := ops.LatestStart(record.NodeID); err != nil || latest.ID != op.ID {
					t.Fatal("failure is unavailable to node detail", latest, err)
				}
			}
		})
	}
}

func TestNodeOperationsCreateStartReservesSlugBeforeIdentityPublication(t *testing.T) {
	s, _ := newTestService(t)
	input := createStartInput(t)
	input.Start = false
	node, err := s.NodeCreate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	ops := testNodeOperations(t, s)
	// Reproduce the boundary after the service committed its new metadata but
	// before the operation manager published the new UUID in its receipt.
	id := newID()
	ops.records[id] = &runningNodeOperation{record: NodeOperation{ID: id, State: "running", NodeSlug: node.Slug, Request: NodeOperationRequest{Kind: "create"}}, keys: []string{"slug:" + node.Slug}, cancel: func(error) {}}
	if _, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "stop", Node: node.ID}); err == nil {
		t.Fatal("existing-node admission bypassed the unbound creation's slug reservation")
	}
	delete(ops.records, id)
}

func createStartInput(t *testing.T) NodeCreateInput {
	t.Helper()
	var input NodeCreateInput
	if err := json.Unmarshal([]byte(`{"slug":"create-start","start":true,"import_host_auth":false}`), &input); err != nil {
		t.Fatal(err)
	}
	input.Directory = t.TempDir()
	return input
}

func TestNodeOperationsCreateStartBindsOneDurableReceipt(t *testing.T) {
	s, _ := newTestService(t)
	gate := createStartSandbox{SandboxClient: s.sandbox, entered: make(chan Node, 1), release: make(chan struct{})}
	s.sandbox = gate
	ops := testNodeOperations(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := createStartInput(t)
	request := NodeOperationRequest{ID: newID(), Kind: "create", Create: &input}
	op, err := ops.Submit(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	cancel() // Admission belongs to the daemon, including the subsequent start.
	var node Node
	select {
	case node = <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("accepted create-and-start never reached boot")
	}
	record, err := ops.Get(op.ID)
	if err != nil || record.NodeID != node.ID || record.State != "running" || record.CurrentPhase != "boot" || len(record.Phases) != 8 {
		t.Fatal("creation and boot did not share an identified receipt", record, err)
	}
	var stored NodeOperation
	if err := readJSONFile(filepath.Join(s.cfg.MetadataRoot, "_operations", op.ID+".json"), &stored); err != nil || stored.NodeID != node.ID {
		t.Fatal("new node identity was not persisted before boot", stored, err)
	}
	if latest, err := ops.LatestStart(node.ID); err != nil || latest.ID != op.ID {
		t.Fatal("startup details cannot find the create-and-start receipt", latest, err)
	}
	listed := ops.List()
	if len(listed) != 1 || !listed[0].Request.startsNode() || listed[0].Request.Create.Directory != "" {
		t.Fatal("Activity summary lost startup intent or copied the entire create input", listed)
	}
	for _, ref := range []string{node.ID, node.Slug} {
		if _, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "stop", Node: ref}); err == nil {
			t.Fatal("another operation entered between creation and boot", ref)
		}
	}
	if repeated, err := ops.Submit(context.Background(), request); err != nil || repeated.ID != op.ID {
		t.Fatal("retry admitted a second create-and-start", repeated, err)
	}
	close(gate.release)
	finished := waitNodeOperation(t, ops, op.ID, "succeeded")
	for _, phase := range finished.Phases {
		if phase.State != "succeeded" && phase.State != "skipped" {
			t.Fatal("successful create-and-start left a phase unfinished", phase)
		}
	}
	node, err = s.NodeShow(context.Background(), node.ID)
	if err != nil || !nodeIsRunning(node) || !node.BootstrapCompleted {
		t.Fatal("receipt succeeded without a ready node", node, err)
	}
}

func TestNodeOperationsCreateStartCancelStopsCreatedVM(t *testing.T) {
	s, _ := newTestService(t)
	gate := createStartSandbox{SandboxClient: s.sandbox, entered: make(chan Node, 1), release: make(chan struct{})}
	s.sandbox = gate
	ops := testNodeOperations(t, s)
	input := createStartInput(t)
	op, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "create", Create: &input})
	if err != nil {
		t.Fatal(err)
	}
	var node Node
	select {
	case node = <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("create-and-start never reached boot")
	}
	if err := ops.Cancel(op.ID); err != nil {
		t.Fatal(err)
	}
	finished := waitNodeOperation(t, ops, op.ID, "canceled")
	if finished.NodeID != node.ID || finished.Result == nil || finished.Result.Status != NodeStatusStopped {
		t.Fatal("cancellation lost the created node or failed to stop it", finished)
	}
	node, err = s.NodeShow(context.Background(), node.ID)
	if err != nil || nodeIsRunning(node) {
		t.Fatal("canceled creation left a running VM", node, err)
	}
}

func TestNodeCreateCLIStartsOnlyWhenRequested(t *testing.T) {
	s, _ := newTestService(t)
	sandbox := s.sandbox.(*fakeSandbox)
	created, err := dispatchNode(context.Background(), s, []string{"create", "--slug", "started", "--start", "--no-import-auth"})
	if err != nil {
		t.Fatal(err)
	}
	if node := created.(Node); !nodeIsRunning(node) || !node.BootstrapCompleted {
		t.Fatal("--start returned before readiness", node)
	}
	created, err = dispatchNode(context.Background(), s, []string{"create", "--slug", "created-only", "--no-import-auth"})
	if err != nil {
		t.Fatal(err)
	}
	if node := created.(Node); node.Status != NodeStatusCreated {
		t.Fatal("ordinary create started a VM", node)
	}
	sandbox.mu.Lock()
	defer sandbox.mu.Unlock()
	starts := 0
	for _, call := range sandbox.calls {
		if strings.HasPrefix(call, "start ") {
			starts++
		}
	}
	if starts != 1 {
		t.Fatal("unexpected start count", starts)
	}
}

func TestTUICreateStartReceiptUsesStartupAttachmentGate(t *testing.T) {
	a := testActivityApp(t, 80, 24)
	a.view, a.nodeDetail = tuiViewNodes, true
	node := a.state.selectedEntry().node
	record := NodeOperation{ID: "create-start", NodeID: node.ID, State: "running", CurrentPhase: "boot", Request: NodeOperationRequest{Kind: "create", Create: &NodeCreateInput{Start: true}}}
	a.activity.page.Running = []NodeOperation{record}
	a.lifecycle = tuiLifecycleState{nodeID: node.ID, loaded: true, operation: record}
	if !a.nodeStarting() {
		t.Fatal("create-and-start was treated as an ordinary ready node")
	}
	a.handleKey(vaxis.Key{Keycode: 'x', Text: "x"})
	if a.lifecycle.queuedOperation != record.ID {
		t.Fatal("attachment did not bind to the create-and-start receipt")
	}
	record.State = "failed"
	a.applyLifecycleData(tuiLifecycleDataEvent{nodeID: node.ID, serial: a.lifecycle.serial, generation: a.managerGeneration, operation: record})
	if a.lifecycle.queuedOperation != "" || !a.nodeLifecycleFailed() {
		t.Fatal("failed create-and-start retained attachment intent or hid its failure")
	}
}
