package codelima

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/testutil"
	"go.rockorager.dev/vaxis"
)

func TestNodeOperationsOutputDeliveryHandlesFastCompletionAndTailRotation(t *testing.T) {
	var buffer bytes.Buffer
	printer := nodeOperationPrinter{writer: &buffer}
	printer.write(NodeOperation{State: "succeeded", Output: []string{"complete"}, OutputPartial: "final"})
	printer.write(NodeOperation{State: "succeeded", OutputPartial: "final"})
	if buffer.String() != "complete\nfinal\n" {
		t.Fatal("fast receipt lost or duplicated completion output", buffer.String())
	}
	buffer.Reset()
	printer = nodeOperationPrinter{writer: &buffer}
	printer.write(NodeOperation{State: "running", OutputStart: 100, Output: []string{"first", "second"}, OutputPartial: "not-yet"})
	printer.write(NodeOperation{State: "running", OutputStart: 101, Output: []string{"second", "third"}})
	printer.write(NodeOperation{State: "failed", OutputStart: 102, Output: []string{"third"}, OutputPartial: "last"})
	if buffer.String() != "first\nsecond\nthird\nlast\n" {
		t.Fatal("polling duplicated lines or lost retained output", buffer.String())
	}
}

func TestNodeOperationsHistoryOrdersByObservedCompletion(t *testing.T) {
	s, _ := newTestService(t)
	operations := testNodeOperations(t, s)
	now := time.Now()
	earlier := now.Add(-time.Hour)
	long := NodeOperation{ID: newID(), State: "succeeded", CreatedAt: now.Add(-2 * time.Hour), FinishedAt: &now}
	short := NodeOperation{ID: newID(), State: "succeeded", CreatedAt: now.Add(-90 * time.Minute), FinishedAt: &earlier}
	operations.records[long.ID] = &runningNodeOperation{record: long}
	operations.records[short.ID] = &runningNodeOperation{record: short}
	page, err := operations.ListPage("", 1)
	if err != nil || len(page.History) != 1 || page.History[0].ID != long.ID {
		t.Fatal("a long operation's new completion was buried in older history", page, err)
	}
	*page.History[0].FinishedAt = earlier
	stored, err := operations.Get(long.ID)
	if err != nil || !stored.FinishedAt.Equal(now) {
		t.Fatal("summary exposed mutable completion storage", stored, err)
	}
	page, err = operations.ListPage(page.Next, 1)
	if err != nil || len(page.History) != 1 || page.History[0].ID != short.ID {
		t.Fatal("completion cursor lost older history", page, err)
	}
}

func TestNodeOperationsClientExitAndActivityCancellation(t *testing.T) {
	s, n := setupBootstrapStepsTest(t)
	s.sandbox = outputOperationSandbox{SandboxClient: s.sandbox}
	host := newDaemonHost(s)
	// Keep the Unix socket beneath its platform path limit; metadata can use
	// the ordinary per-test service root.
	home := newDaemonTestRoot(t, "op-")
	server := daemon.NewServer(daemon.Config{Home: home, Version: Version, Handler: host})
	host.broadcast = server.Broadcast
	serverCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(serverCtx) }()
	t.Cleanup(func() { stop(); <-done; _ = host.Close() })
	waitForDaemonPing(t, home)
	client, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	frontend := s.withIO(io.Discard, io.Discard)
	frontend.daemonClient = client
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := frontend.NodeStart(ctx, n.ID); result <- err }()
	var page NodeOperationsPage
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		err := client.Call(context.Background(), "operation.list", nil, &page)
		return err == nil && len(page.Running) == 1
	}, "client admitted durable start")
	id := page.Running[0].ID
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal("caller should stop waiting without canceling work", err)
	}
	_ = client.Close()
	observer, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = observer.Close() }()
	var record NodeOperation
	testutil.WaitForCondition(t, 4*time.Second, func() bool {
		err := observer.Call(context.Background(), "operation.get", map[string]string{"id": id}, &record)
		return err == nil && record.State == "running" && strings.Contains(strings.Join(record.Output, "\n"), "LIVE-OUT")
	}, "independent observer sees continuing work and live output")
	a, _ := newTestManager(t, 80, 24)
	a.view = tuiViewActivity
	a.service = s.withIO(io.Discard, io.Discard)
	a.service.daemonClient = observer
	a.startActivityLoad(true)
	a.activity.selected = id
	a.handleKey(vaxis.Key{Keycode: 'K', Text: "K"})
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		err := observer.Call(context.Background(), "operation.get", map[string]string{"id": id}, &record)
		return err == nil && record.State == "canceled"
	}, "Activity cancellation finished")
	a.startActivityLoad(true)
	if len(a.activity.page.Running) != 0 || len(a.activity.page.History) != 1 || a.activity.selected != id {
		t.Fatal("Activity did not move the selected operation into history")
	}
	node, err := s.NodeShow(context.Background(), n.ID)
	if err != nil || nodeIsRunning(node) {
		t.Fatal("Activity cancel left VM running", err)
	}
}

type gatedOperationSandbox struct {
	SandboxClient
	release <-chan struct{}
}

type outputOperationSandbox struct{ SandboxClient }

type unconfirmedStopSandbox struct{ SandboxClient }

func (s unconfirmedStopSandbox) Stop(context.Context, Node) error { return nil }

func TestNodeOperationsCancellationCannotClaimAnUnconfirmedStop(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	s.sandbox = outputOperationSandbox{SandboxClient: unconfirmedStopSandbox{SandboxClient: s.sandbox}}
	operations := testNodeOperations(t, s)
	operation, err := operations.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "start", Node: node.ID})
	if err != nil {
		t.Fatal(err)
	}
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		b, err := s.store.LoadBootstrapState(node.ID)
		return err == nil && len(b.Steps) > 0 && b.Steps[0].State == BootstrapStepRunning
	}, "running bootstrap")
	if err := operations.Cancel(operation.ID); err != nil {
		t.Fatal(err)
	}
	result := waitNodeOperation(t, operations, operation.ID, "failed")
	if !strings.Contains(result.Error, "did not confirm") {
		t.Fatal("unconfirmed stop was hidden", result)
	}
}

func (s outputOperationSandbox) Shell(ctx context.Context, _ Node, _ guestIdentity, _ []string, _ string, _ bool, streams ShellStreams) error {
	_, _ = fmt.Fprint(streams.Stdout, "LIVE-OUT\npartial")
	_, _ = fmt.Fprint(streams.Stderr, "-ERR\n")
	<-ctx.Done()
	return ctx.Err()
}

func TestNodeOperationsRetainLiveOutputAcrossCancellation(t *testing.T) {
	s, n := setupBootstrapStepsTest(t)
	s.sandbox = outputOperationSandbox{SandboxClient: s.sandbox}
	ops := testNodeOperations(t, s)
	op, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "start", Node: n.ID})
	if err != nil {
		t.Fatal(err)
	}
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		op, err := ops.Get(op.ID)
		return err == nil && strings.Contains(strings.Join(op.Output, "\n"), "partial-ERR")
	}, "live stdout/stderr in durable receipt")
	if err := ops.Cancel(op.ID); err != nil {
		t.Fatal(err)
	}
	result := waitNodeOperation(t, ops, op.ID, "canceled")
	if !strings.Contains(strings.Join(result.Output, "\n"), "LIVE-OUT") {
		t.Fatal("cancellation discarded live output")
	}
	var stored NodeOperation
	if err := readJSONFile(filepath.Join(s.cfg.MetadataRoot, "_operations", op.ID+".json"), &stored); err != nil || len(stored.Output) != len(result.Output) {
		t.Fatal("output was not persisted", err)
	}
}

func TestNodeOperationsOutputTailBoundsAndPartialUnicode(t *testing.T) {
	w := &nodeOperationOutput{}
	for i := range 300 {
		if _, err := fmt.Fprintf(w, "%03d %s\n", i, strings.Repeat("line", 100)); err != nil {
			t.Fatal(err)
		}
	}
	text := strings.Repeat("界", 5000) + "FINAL"
	if _, err := w.Write([]byte(text[:17])); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(text[17:])); err != nil {
		t.Fatal(err)
	}
	partial := w.partialText()
	if !strings.HasSuffix(partial, "FINAL") || !utf8.ValidString(partial) || len(partial) > operationOutputLineBytes {
		t.Fatalf("invalid partial tail: length=%d suffix=%q", len(partial), partial[max(0, len(partial)-12):])
	}
	bytes := len(partial)
	for _, line := range w.lines {
		bytes += len(line)
	}
	if len(w.lines)+1 > operationOutputLines || bytes > operationOutputBytes || w.next != 300 {
		t.Fatalf("tail unbounded or lost sequence: lines=%d bytes=%d next=%d", len(w.lines), bytes, w.next)
	}
}

func TestNodeOperationsRPCAdmissionOutlivesConnectionAndBlocksUpdate(t *testing.T) {
	s, n := setupBootstrapStepsTest(t)
	s.sandbox = gatedOperationSandbox{SandboxClient: s.sandbox, release: make(chan struct{})}
	h := newDaemonHost(s)
	t.Cleanup(func() { _ = h.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	request := NodeOperationRequest{ID: newID(), Kind: "start", Node: n.ID}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.Handle(ctx, daemon.ClientContext{}, "operation.submit", raw)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	op, ok := result.(NodeOperation)
	if !ok || op.ID != request.ID {
		t.Fatalf("admission receipt: %#v", result)
	}
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		b, err := s.store.LoadBootstrapState(n.ID)
		return err == nil && len(b.Steps) > 0 && b.Steps[0].State == BootstrapStepRunning
	}, "RPC operation survived caller")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.update(executable); err == nil || !strings.Contains(err.Error(), "lifecycle operations") {
		t.Fatal("live update did not protect active lifecycle work", err)
	}
	raw, _ = json.Marshal(map[string]string{"id": op.ID})
	if _, err := h.Handle(context.Background(), daemon.ClientContext{}, "operation.cancel", raw); err != nil {
		t.Fatal(err)
	}
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		result, err := h.Handle(context.Background(), daemon.ClientContext{}, "operation.get", raw)
		return err == nil && result.(NodeOperation).State == "canceled"
	}, "RPC cancellation finished")
}

func TestNodeOperationsPagesFitProtocolAndRetainEveryRecord(t *testing.T) {
	s, _ := setupBootstrapStepsTest(t)
	ops := testNodeOperations(t, s)
	for range 250 {
		id := newID()
		ops.records[id] = &runningNodeOperation{record: NodeOperation{ID: id, State: "failed", CreatedAt: s.now(), NodeSlug: strings.Repeat("wide-", 500), Error: strings.Repeat("failure", 1000)}}
	}
	seen := make(map[string]bool)
	cursor := ""
	for {
		page, err := ops.ListPage(cursor, 100)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(page)
		if err != nil || len(encoded) >= daemon.MaxMessageSize/2 {
			t.Fatalf("oversized history page: %d, %v", len(encoded), err)
		}
		for _, record := range page.History {
			if seen[record.ID] {
				t.Fatal("history cursor repeated a record")
			}
			seen[record.ID] = true
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if len(seen) != 250 {
		t.Fatalf("history dropped records: %d", len(seen))
	}
	if _, err := ops.ListPage("expired", 20); err == nil {
		t.Fatal("expired cursor silently restarted history")
	}
	if _, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "start", Node: strings.Repeat("x", 65<<10)}); err == nil {
		t.Fatal("oversized admission accepted")
	}
}

func (s gatedOperationSandbox) Shell(ctx context.Context, node Node, identity guestIdentity, command []string, directory string, interactive bool, streams ShellStreams) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.release:
		return s.SandboxClient.Shell(ctx, node, identity, command, directory, interactive, streams)
	}
}

func testNodeOperations(t *testing.T, service *Service) *nodeOperations {
	t.Helper()
	operations, err := newNodeOperations(service)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := operations.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return operations
}

func waitNodeOperation(t *testing.T, operations *nodeOperations, id, state string) NodeOperation {
	t.Helper()
	var result NodeOperation
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		var err error
		result, err = operations.Get(id)
		return err == nil && result.State == state
	}, "operation "+state)
	return result
}

func TestNodeOperationsOutliveRequesterAndRejectNodeConflicts(t *testing.T) {
	s, n := setupBootstrapStepsTest(t)
	release := make(chan struct{})
	s.sandbox = gatedOperationSandbox{SandboxClient: s.sandbox, release: release}
	ops := testNodeOperations(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	request := NodeOperationRequest{ID: newID(), Kind: "start", Node: n.ID}
	op, err := ops.Submit(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		b, err := s.store.LoadBootstrapState(n.ID)
		return err == nil && len(b.Steps) > 0 && b.Steps[0].State == BootstrapStepRunning
	}, "detached bootstrap reached command")
	if duplicate, err := ops.Submit(context.Background(), request); err != nil || duplicate.ID != op.ID {
		t.Fatalf("duplicate admission: %+v %v", duplicate, err)
	}
	if _, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "stop", Node: n.Slug}); err == nil {
		t.Fatal("same node admitted a conflicting operation through its slug")
	}
	request.Kind = "stop"
	if _, err := ops.Submit(context.Background(), request); err == nil {
		t.Fatal("idempotency key accepted a different request")
	}
	close(release)
	finished := waitNodeOperation(t, ops, op.ID, "succeeded")
	if finished.NodeID != n.ID || finished.Result == nil || finished.Result.Status != NodeStatusRunning || finished.FinishedAt == nil {
		t.Fatalf("missing completion: %+v", finished)
	}
	info, err := os.Stat(filepath.Join(s.cfg.MetadataRoot, "_operations", op.ID+".json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("operation permissions: %v, %v", info, err)
	}
}

func TestNodeOperationsCancelStartStopsVMAndKeepsIncompleteBootstrap(t *testing.T) {
	s, n := setupBootstrapStepsTest(t)
	s.sandbox = gatedOperationSandbox{SandboxClient: s.sandbox, release: make(chan struct{})}
	ops := testNodeOperations(t, s)
	op, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "start", Node: n.ID})
	if err != nil {
		t.Fatal(err)
	}
	testutil.WaitForCondition(t, 3*time.Second, func() bool {
		b, err := s.store.LoadBootstrapState(n.ID)
		return err == nil && len(b.Steps) > 0 && b.Steps[0].State == BootstrapStepRunning
	}, "running step before cancel")
	if err := ops.Cancel(op.ID); err != nil {
		t.Fatal(err)
	}
	waitNodeOperation(t, ops, op.ID, "canceled")
	node, err := s.NodeShow(context.Background(), n.ID)
	if err != nil || nodeIsRunning(node) || node.BootstrapCompleted {
		t.Fatalf("cancel did not stop incomplete node: %+v, %v", node, err)
	}
	if err := ops.Cancel(op.ID); err != nil {
		t.Fatal("cancel was not idempotent", err)
	}
}

func TestNodeOperationsRecoverHistoryWithoutReplayingWork(t *testing.T) {
	s, _ := setupBootstrapStepsTest(t)
	dir := filepath.Join(s.cfg.MetadataRoot, "_operations")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := now.Add(-8 * 24 * time.Hour)
	running := NodeOperation{ID: newID(), State: "running", CreatedAt: now, Request: NodeOperationRequest{Kind: "start", Node: "missing"}}
	finished := NodeOperation{ID: newID(), State: "failed", CreatedAt: old, FinishedAt: &old}
	for _, record := range []NodeOperation{running, finished} {
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, record.ID+".json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ops := testNodeOperations(t, s)
	got, err := ops.Get(running.ID)
	if err != nil || got.State != "interrupted" || got.Error == "" || got.FinishedAt != nil {
		t.Fatalf("recovery invented a finish/replayed work: %+v, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, finished.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("expired history remains: %v", err)
	}
	if records := ops.List(); len(records) != 1 || records[0].ID != running.ID {
		t.Fatalf("history: %+v", records)
	}
}

func TestNodeOperationsLifecycleAndDeleteResponseReplay(t *testing.T) {
	s, dir := newTestService(t)
	ops := testNodeOperations(t, s)
	create := NodeOperationRequest{ID: newID(), Kind: "create", Create: &NodeCreateInput{Slug: "operation-created", Directory: dir}}
	created, err := ops.Submit(context.Background(), create)
	if err != nil {
		t.Fatal(err)
	}
	created = waitNodeOperation(t, ops, created.ID, "succeeded")
	start, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "start", Node: created.NodeID})
	if err != nil {
		t.Fatal(err)
	}
	waitNodeOperation(t, ops, start.ID, "succeeded")
	clone, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "clone", Clone: &NodeCloneInput{SourceNode: created.NodeID, NodeSlug: "operation-clone"}})
	if err != nil {
		t.Fatal(err)
	}
	clone = waitNodeOperation(t, ops, clone.ID, "succeeded")
	if clone.NodeID == created.NodeID || clone.NodeSlug != "operation-clone" {
		t.Fatalf("clone result lost child identity: %+v", clone)
	}
	stop, err := ops.Submit(context.Background(), NodeOperationRequest{ID: newID(), Kind: "stop", Node: created.NodeID})
	if err != nil {
		t.Fatal(err)
	}
	waitNodeOperation(t, ops, stop.ID, "succeeded")
	remove := NodeOperationRequest{ID: newID(), Kind: "delete", Node: clone.NodeSlug}
	deleted, err := ops.Submit(context.Background(), remove)
	if err != nil {
		t.Fatal(err)
	}
	waitNodeOperation(t, ops, deleted.ID, "succeeded")
	// Remove metadata too: replay must use the original admission receipt.
	if err := os.RemoveAll(filepath.Join(s.cfg.MetadataRoot, "nodes", clone.NodeID)); err != nil {
		t.Fatal(err)
	}
	if replay, err := ops.Submit(context.Background(), remove); err != nil || replay.ID != deleted.ID || replay.State != "succeeded" {
		t.Fatalf("delete response replay: %+v, %v", replay, err)
	}
	create.Create.Slug = "mutated-by-caller"
	loaded, err := ops.Get(created.ID)
	if err != nil || loaded.Request.Create.Slug != "operation-created" {
		t.Fatal("manager retained mutable request storage", err)
	}
	loaded.Request.Create.Slug = "mutated-query"
	loaded, err = ops.Get(created.ID)
	if err != nil || loaded.Request.Create.Slug != "operation-created" {
		t.Fatal("query exposed mutable operation storage", err)
	}
}
