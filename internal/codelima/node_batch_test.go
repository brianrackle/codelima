package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/testutil"
)

type batchSandbox struct {
	SandboxClient
	entered chan Node
	release chan struct{}
	failID  string
}

func (s batchSandbox) Stop(ctx context.Context, node Node) error {
	s.entered <- node
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	if node.ID == s.failID {
		return errors.New("fixture stop failure")
	}
	return s.SandboxClient.Stop(ctx, node)
}

func batchFixture(t *testing.T, count int) (*Service, []Node, batchSandbox) {
	t.Helper()
	s, workspace := newTestService(t)
	var nodes []Node
	noAuth := false
	for i := range count {
		node, err := s.NodeCreate(context.Background(), NodeCreateInput{Directory: workspace, Slug: fmt.Sprintf("batch-%d", i), ImportHostAuth: &noAuth})
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, node)
	}
	fake := s.sandbox.(*fakeSandbox)
	fake.mu.Lock()
	for _, n := range nodes {
		fake.observations[n.SandboxName] = RuntimeObservation{Name: n.SandboxName, Exists: true, Status: ObservationRunning}
	}
	fake.mu.Unlock()
	gate := batchSandbox{SandboxClient: fake, entered: make(chan Node, count), release: make(chan struct{})}
	s.sandbox = gate
	return s, nodes, gate
}

func batchRequest(t *testing.T, nodes []Node) NodeOperationRequest {
	t.Helper()
	targets := make([]map[string]string, 0, len(nodes))
	for _, node := range nodes {
		targets = append(targets, map[string]string{"node_id": node.ID, "node_slug": node.Slug, "kind": "stop"})
	}
	data, err := json.Marshal(map[string]any{"id": newID(), "kind": "batch", "origin": "tui", "batch": targets})
	if err != nil {
		t.Fatal(err)
	}
	var request NodeOperationRequest
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func awaitBatchEntries(t *testing.T, gate batchSandbox, count int) {
	t.Helper()
	for range count {
		select {
		case <-gate.entered:
		case <-time.After(3 * time.Second):
			t.Fatal("batch did not run its targets in parallel")
		}
	}
}

func TestNodeBatchParallelPartialFailureAndRequesterExit(t *testing.T) {
	s, nodes, gate := batchFixture(t, 6)
	gate.failID = nodes[0].ID
	s.sandbox = gate
	ops := testNodeOperations(t, s)
	request := batchRequest(t, nodes)
	ctx, cancel := context.WithCancel(context.Background())
	record, err := ops.Submit(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	awaitBatchEntries(t, gate, 4)
	select {
	case <-gate.entered:
		t.Fatal("batch exceeded four simultaneous targets")
	default:
	}
	if _, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "stop", Node: nodes[5].ID}); err == nil {
		t.Fatal("queued target was not reserved")
	}
	// Caller-owned request data cannot change accepted membership.
	request.Batch[0].NodeID = "edited"
	close(gate.release)
	finished := waitNodeOperation(t, ops, record.ID, "failed")
	if finished.Batch.Succeeded != 5 || finished.Batch.Failed != 1 || len(finished.Batch.Members) != 6 {
		t.Fatalf("partial outcomes lost: %+v", finished.Batch)
	}
	for _, member := range finished.Batch.Members {
		child, err := ops.Get(member.OperationID)
		if err != nil || child.BatchID != record.ID || child.NodeID != member.NodeID || child.State != member.State {
			t.Fatal("child outcome not retained", child, member, err)
		}
	}
	if _, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "stop", Node: nodes[5].ID}); err != nil {
		t.Fatal("finished batch retained reservation", err)
	}
}

func TestNodeBatchCancelLeavesQueuedTargetsUntouched(t *testing.T) {
	s, nodes, gate := batchFixture(t, 6)
	ops := testNodeOperations(t, s)
	record, err := ops.Submit(context.Background(), batchRequest(t, nodes))
	if err != nil {
		t.Fatal(err)
	}
	awaitBatchEntries(t, gate, 4)
	if err := ops.Cancel(record.ID); err != nil {
		t.Fatal(err)
	}
	finished := waitNodeOperation(t, ops, record.ID, "canceled")
	if finished.Batch.Canceled != 6 {
		t.Fatalf("cancel lost target results: %+v", finished.Batch)
	}
	select {
	case <-gate.entered:
		t.Fatal("cancellation started a queued target")
	default:
	}
	for _, member := range finished.Batch.Members[4:] {
		if _, err := ops.Get(member.OperationID); !IsNotFound(err) {
			t.Fatal("queued target acquired execution receipt", err)
		}
	}
}

func TestNodeBatchAdmissionIsAtomicAndIdempotent(t *testing.T) {
	s, nodes, gate := batchFixture(t, 2)
	ops := testNodeOperations(t, s)
	for _, problem := range []string{"missing", "renamed", "duplicate", "unsupported", "empty"} {
		r := batchRequest(t, nodes)
		switch problem {
		case "missing":
			r.Batch[1].NodeID = newID()
		case "renamed":
			r.Batch[1].NodeSlug = "renamed"
		case "duplicate":
			r.Batch[1] = r.Batch[0]
		case "unsupported":
			r.Batch[1].Kind = "clone"
		case "empty":
			r.Batch = nil
		}
		if _, err := ops.Submit(context.Background(), r); err == nil {
			t.Fatal("accepted invalid batch", problem)
		}
	}
	if len(ops.List()) != 0 {
		t.Fatal("invalid admission ran a prefix")
	}
	r := batchRequest(t, nodes)
	record, err := ops.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	awaitBatchEntries(t, gate, 2)
	duplicate, err := ops.Submit(context.Background(), r)
	if err != nil || duplicate.ID != record.ID {
		t.Fatal("lost idempotency", duplicate, err)
	}
	r.Batch[0].Kind = "delete"
	if _, err := ops.Submit(context.Background(), r); err == nil {
		t.Fatal("reused ID changed accepted action")
	}
	close(gate.release)
	waitNodeOperation(t, ops, record.ID, "succeeded")
}

func TestNodeBatchRecoveryKeepsCompletedResultsWithoutReplayingQueuedWork(t *testing.T) {
	s, nodes, _ := batchFixture(t, 3)
	ops := testNodeOperations(t, s)
	r := batchRequest(t, nodes)
	batch, _, err := ops.prepareBatch(r)
	if err != nil {
		t.Fatal(err)
	}
	now := s.now()
	root := NodeOperation{ID: r.ID, Request: r, State: "running", CreatedAt: now, Batch: batch}
	child := NodeOperation{ID: batch.Members[0].OperationID, BatchID: r.ID, NodeID: nodes[0].ID, NodeSlug: nodes[0].Slug, Request: NodeOperationRequest{Kind: "stop"}, State: "succeeded", CreatedAt: now, FinishedAt: &now}
	if err := ops.persist(root); err != nil {
		t.Fatal(err)
	}
	if err := ops.persist(child); err != nil {
		t.Fatal(err)
	}
	if err := ops.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered := testNodeOperations(t, s)
	got, err := recovered.Get(root.ID)
	if err != nil || got.State != "interrupted" || got.Batch.Succeeded != 1 || got.Batch.Interrupted != 2 || recovered.Active() {
		t.Fatal("recovery lost outcomes or replayed work", got, err)
	}
	data, err := os.ReadFile(filepath.Join(recovered.dir, root.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored NodeOperation
	if err := json.Unmarshal(data, &stored); err != nil || stored.Batch.Interrupted != 2 {
		t.Fatal("recovery was not persisted", err)
	}
}

func TestNodeBatchChildCancellationDoesNotCancelSiblings(t *testing.T) {
	s, nodes, gate := batchFixture(t, 2)
	ops := testNodeOperations(t, s)
	r, err := ops.Submit(context.Background(), batchRequest(t, nodes))
	if err != nil {
		t.Fatal(err)
	}
	awaitBatchEntries(t, gate, 2)
	id := r.Batch.Members[0].OperationID
	if err := ops.Cancel(id); err != nil {
		t.Fatal(err)
	}
	testutil.WaitForCondition(t, 3*time.Second, func() bool { child, _ := ops.Get(id); return child.State == "canceled" }, "single child canceled")
	close(gate.release)
	finished := waitNodeOperation(t, ops, r.ID, "failed")
	if finished.Batch.Succeeded != 1 || finished.Batch.Canceled != 1 {
		t.Fatalf("child cancel affected siblings: %+v", finished.Batch)
	}
}

func TestNodeBatchWireAdmissionOutlivesRequesterAndListsProgress(t *testing.T) {
	s, nodes, gate := batchFixture(t, 6)
	home, _ := testCLIOperationServer(t, s, Version)
	connection, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	frontend := s.withIO(io.Discard, io.Discard)
	frontend.daemonClient = connection
	receipts := make(chan string, 1)
	frontend.operationReceipt = func(id string) { receipts <- id }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	targets := batchRequest(t, nodes).Batch
	go func() { _, err := frontend.NodeBatch(ctx, targets); done <- err }()
	var id string
	select {
	case id = <-receipts:
	case <-time.After(3 * time.Second):
		t.Fatal("batch was not admitted")
	}
	awaitBatchEntries(t, gate, 4)
	var page NodeOperationsPage
	if err := frontend.daemonClient.Call(context.Background(), "operation.list", nil, &page); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range page.Running {
		if record.ID != id {
			continue
		}
		found = true
		if record.Batch == nil || record.Batch.Total != 6 || record.Batch.Queued != 2 || len(record.Batch.Members) != 6 || record.Progress == nil || len(record.Request.Batch) != 0 {
			t.Fatal("bounded list lost queued targets or retained full request", record)
		}
	}
	if !found {
		t.Fatal("batch not visible in Activity's operation source")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("requester could not stop waiting", err)
	}
	_ = frontend.daemonClient.Close()
	close(gate.release)
	// A fresh connection must observe completion of targets that were queued
	// when the initiating connection disappeared.
	client, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	var record NodeOperation
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		return client.Call(context.Background(), "operation.get", map[string]string{"id": id}, &record) == nil && record.State == "succeeded"
	}, "queued targets completed after disconnect")
	if record.Batch.Succeeded != 6 {
		t.Fatal("lost queued work", record.Batch)
	}
	if activityOperationRecord(record, false).Action != activityOpenOperation {
		t.Fatal("batch outcome does not open its retained target list")
	}
}

func TestNodeBatchGlobalConcurrencyAndBoundedSummaryIsolation(t *testing.T) {
	s, nodes, gate := batchFixture(t, 20)
	ops := testNodeOperations(t, s)
	var roots []NodeOperation
	for start := 0; start < 20; start += 4 {
		r, err := ops.Submit(context.Background(), batchRequest(t, nodes[start:start+4]))
		if err != nil {
			t.Fatal(err)
		}
		roots = append(roots, r)
	}
	awaitBatchEntries(t, gate, 16)
	select {
	case <-gate.entered:
		t.Fatal("batches exceeded global lifecycle capacity")
	case <-time.After(75 * time.Millisecond):
	}
	for _, record := range ops.List() {
		if record.Batch == nil {
			continue
		}
		if len(record.Request.Batch) != 0 {
			t.Fatal("summary retained full request")
		}
		record.Batch.Members[0].NodeID = "mutated"
		stored, _ := ops.Get(record.ID)
		if stored.Batch.Members[0].NodeID == "mutated" {
			t.Fatal("summary exposes mutable batch storage")
		}
	}
	close(gate.release)
	for _, root := range roots {
		waitNodeOperation(t, ops, root.ID, "succeeded")
	}
	for _, record := range ops.List() {
		if record.Batch != nil && len(record.Batch.Members) > 0 {
			t.Fatal("history summaries retained complete target lists")
		}
	}
}

func TestNodeBatchFailedAdmissionDoesNotExecuteAnyTarget(t *testing.T) {
	s, nodes, gate := batchFixture(t, 2)
	ops := testNodeOperations(t, s)
	original := ops.dir
	ops.dir = filepath.Join(original, "not-a-directory")
	if err := os.WriteFile(ops.dir, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.Submit(context.Background(), batchRequest(t, nodes)); err == nil {
		t.Fatal("acknowledged admission without durable receipt")
	}
	ops.dir = original
	if len(ops.List()) != 0 {
		t.Fatal("failed admission retained a runnable prefix")
	}
	select {
	case <-gate.entered:
		t.Fatal("failed admission mutated a VM")
	default:
	}
}
