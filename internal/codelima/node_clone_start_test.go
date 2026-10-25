package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/testutil"
)

func cloneStartInput(t *testing.T, source Node) NodeCloneInput {
	t.Helper()
	var input NodeCloneInput
	if err := json.Unmarshal([]byte(`{"node_slug":"clone-child","start":true}`), &input); err != nil {
		t.Fatal(err)
	}
	input.SourceNode = source.ID
	return input
}

func cloneStartSource(t *testing.T) (*Service, Node, *fakeSandbox) {
	t.Helper()
	s, workspace := newTestService(t)
	noAuth := false
	source, err := s.NodeCreate(context.Background(), NodeCreateInput{Directory: workspace, Slug: "clone-source", ImportHostAuth: &noAuth})
	if err != nil {
		t.Fatal(err)
	}
	source, err = s.NodeStart(context.Background(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	fake := s.sandbox.(*fakeSandbox)
	fake.calls = nil
	return s, source, fake
}

func TestNodeCloneStartRestoresSourceBeforeBootingChild(t *testing.T) {
	s, source, fake := cloneStartSource(t)
	child, err := s.NodeClone(context.Background(), cloneStartInput(t, source))
	if err != nil || !nodeIsRunning(child) || !child.BootstrapCompleted {
		t.Fatal("clone-and-start did not reach readiness", child, err)
	}
	want := []string{"stop " + source.SandboxName, "clone " + source.SandboxName + " " + child.SandboxName, "start " + source.SandboxName, "start " + child.SandboxName}
	var mutations []string
	for _, call := range fake.calls {
		if strings.HasPrefix(call, "start ") || strings.HasPrefix(call, "stop ") || strings.HasPrefix(call, "clone ") {
			mutations = append(mutations, call)
		}
	}
	if !slices.Equal(mutations, want) {
		t.Fatal("source restoration must precede child startup", mutations)
	}
}

type cloneStartSandbox struct {
	SandboxClient
	sourceID string
	block    string
	entered  chan Node
	release  chan struct{}
	fail     string
}

func (s cloneStartSandbox) wait(ctx context.Context, stage string, node Node) error {
	if s.block == stage {
		s.entered <- node
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.fail == stage {
		return errors.New("fixture " + stage + " failure")
	}
	return nil
}

func (s cloneStartSandbox) Clone(ctx context.Context, source, child Node) error {
	if err := s.wait(ctx, "copy", child); err != nil {
		return err
	}
	return s.SandboxClient.Clone(ctx, source, child)
}

func (s cloneStartSandbox) Start(ctx context.Context, node Node) error {
	stage := "child boot"
	if node.ID == s.sourceID {
		stage = "source restore"
	}
	// A partially started child must be stopped on explicit cancellation.
	if err := s.SandboxClient.Start(ctx, node); err != nil {
		return err
	}
	return s.wait(ctx, stage, node)
}

func awaitCloneStage(t *testing.T, gate cloneStartSandbox) Node {
	t.Helper()
	select {
	case node := <-gate.entered:
		return node
	case <-time.After(3 * time.Second):
		t.Fatal("clone did not reach " + gate.block)
		return Node{}
	}
}

func TestNodeOperationsCloneStartCancellationPreservesSource(t *testing.T) {
	for _, stage := range []string{"copy", "source restore", "child boot"} {
		t.Run(stage, func(t *testing.T) {
			s, source, fake := cloneStartSource(t)
			gate := cloneStartSandbox{SandboxClient: fake, sourceID: source.ID, block: stage, entered: make(chan Node, 1), release: make(chan struct{})}
			s.sandbox = gate
			ops := testNodeOperations(t, s)
			input := cloneStartInput(t, source)
			op, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "clone", Clone: &input})
			if err != nil {
				t.Fatal(err)
			}
			awaitCloneStage(t, gate)
			if _, err := ops.LatestStart(source.ID); err == nil {
				t.Fatal("clone was misreported as a new source startup")
			}
			if err := ops.Cancel(op.ID); err != nil {
				t.Fatal(err)
			}
			close(gate.release)
			waitNodeOperation(t, ops, op.ID, "canceled")
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.observations[source.SandboxName].Status != ObservationRunning {
				t.Fatal("cancellation stopped the restored source", fake.calls)
			}
			stops := 0
			for _, call := range fake.calls {
				if call == "stop "+source.SandboxName {
					stops++
				}
			}
			if stops != 1 || fake.observations["clone-child"].Status == ObservationRunning {
				t.Fatal("cancellation must stop only the started child", fake.calls)
			}
		})
	}
}

func TestNodeOperationsCloneStartFailuresRetainChild(t *testing.T) {
	for _, stage := range []string{"source restore", "child boot"} {
		t.Run(stage, func(t *testing.T) {
			s, source, fake := cloneStartSource(t)
			s.sandbox = cloneStartSandbox{SandboxClient: fake, sourceID: source.ID, fail: stage}
			ops := testNodeOperations(t, s)
			input := cloneStartInput(t, source)
			op, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "clone", Clone: &input})
			if err != nil {
				t.Fatal(err)
			}
			op = waitNodeOperation(t, ops, op.ID, "failed")
			child, err := s.store.NodeByIDOrSlug(input.NodeSlug)
			if err != nil || op.NodeID != child.ID || !strings.Contains(op.Error, "fixture "+stage+" failure") {
				t.Fatal("failure lost the committed child identity", op, child, err)
			}
			_, latestErr := ops.LatestStart(child.ID)
			if (latestErr == nil) != (stage == "child boot") {
				t.Fatal("startup evidence does not match the stage reached", latestErr)
			}
			if stage == "source restore" && containsCall(fake.calls, "start "+child.SandboxName) {
				t.Fatal("child booted after failed source restoration", fake.calls)
			}
		})
	}
}

func TestNodeOperationsCloneStartBindsAndReservesChild(t *testing.T) {
	s, source, fake := cloneStartSource(t)
	gate := cloneStartSandbox{SandboxClient: fake, sourceID: source.ID, block: "child boot", entered: make(chan Node, 1), release: make(chan struct{})}
	s.sandbox = gate
	ops := testNodeOperations(t, s)
	input := cloneStartInput(t, source)
	op, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "clone", Clone: &input})
	if err != nil {
		t.Fatal(err)
	}
	child := awaitCloneStage(t, gate)
	defer close(gate.release)
	for _, id := range []string{source.ID, child.ID, child.Slug} {
		if _, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "delete", Node: id}); err == nil {
			t.Fatal("clone/start lost its reservation", id)
		}
	}
	latest, err := ops.LatestStart(child.ID)
	if err != nil || latest.ID != op.ID || operationPhaseForTest(t, latest, "clone").State != "succeeded" || operationPhaseForTest(t, latest, "boot").State != "running" {
		t.Fatal("child startup was not bound to the original receipt", latest, err)
	}
	listed := ops.List()
	if len(listed) != 1 || !listed[0].startsNode() || listed[0].Request.Clone.SourceNode != "" || listed[0].Request.Clone.NodeSlug != "" || listed[0].NodeID != child.ID {
		t.Fatal("bounded summary lost child startup or leaked full inputs", listed)
	}
	if record := activityOperationRecord(listed[0], false); !strings.Contains(record.Text, "clone + start") {
		t.Fatal("Activity lost optional startup", record)
	}
	if evidence := summarizeFleetLifecycle(source.ID, listed); evidence.Start != nil {
		t.Fatal("clone startup was attributed to source", evidence.Start)
	}
	if evidence := summarizeFleetLifecycle(child.ID, listed); evidence.Start == nil || evidence.Start.ID != op.ID {
		t.Fatal("child startup missing from fleet evidence", evidence.Start)
	}
}

func TestNodeCloneStartDirectFailureRetainsCommittedChild(t *testing.T) {
	for _, stage := range []string{"source restore", "child boot"} {
		t.Run(stage, func(t *testing.T) {
			s, source, fake := cloneStartSource(t)
			s.sandbox = cloneStartSandbox{SandboxClient: fake, sourceID: source.ID, fail: stage}
			child, err := s.NodeClone(context.Background(), cloneStartInput(t, source))
			if err == nil || child.ID == "" || child.ID == source.ID {
				t.Fatal("direct failure lost child identity", child.ID, err)
			}
			stored, loadErr := s.store.NodeByID(child.ID)
			if loadErr != nil || stored.ID != child.ID || stage == "child boot" && stored.Status != child.Status {
				t.Fatal("direct failure did not return retained state", child.Status, stored.Status, loadErr)
			}
		})
	}
}

func TestNodeOperationsCloneStartSurvivesClientDisconnect(t *testing.T) {
	s, source, fake := cloneStartSource(t)
	gate := cloneStartSandbox{SandboxClient: fake, sourceID: source.ID, block: "copy", entered: make(chan Node, 1), release: make(chan struct{})}
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
	input := cloneStartInput(t, source)
	go func() { _, err := frontend.NodeClone(ctx, input); done <- err }()
	var id string
	select {
	case id = <-receipt:
	case <-time.After(3 * time.Second):
		t.Fatal("clone-and-start admission was not acknowledged")
	}
	awaitCloneStage(t, gate)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("observer did not stop waiting", err)
	}
	_ = client.Close()
	close(gate.release)
	observer, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = observer.Close() }()
	var record NodeOperation
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		return observer.Call(context.Background(), "operation.get", map[string]string{"id": id}, &record) == nil && record.State == "succeeded"
	}, "clone and child boot finish after requester disconnect")
	if record.NodeID == source.ID || record.Result == nil || record.Result.Status != NodeStatusRunning || operationPhaseForTest(t, record, "ready").State != "succeeded" {
		t.Fatal("one receipt did not retain child readiness", record)
	}
}

func TestNodeCloneCLIStartIsOptional(t *testing.T) {
	s, source, _ := cloneStartSource(t)
	for _, start := range []bool{false, true} {
		name := "clone-stopped"
		args := []string{"clone", source.ID, "--slug", name}
		if start {
			args = []string{"clone", source.ID, "--slug", "clone-started", "--start"}
		}
		result, err := dispatchNode(context.Background(), s, args)
		if err != nil {
			t.Fatal(err)
		}
		if nodeIsRunning(result.(Node)) != start {
			t.Fatal("clone CLI ignored optional start", result)
		}
	}
}
