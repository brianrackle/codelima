package codelima

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func requestFleetEvidence(t *testing.T, host *daemonHost, request fleetEvidenceRequest) fleetEvidenceSnapshot {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := host.Handle(context.Background(), daemon.ClientContext{}, "node.fleet_evidence", raw)
	if err != nil {
		t.Fatal(err)
	}
	return result.(fleetEvidenceSnapshot)
}

func TestFleetEvidenceRecoversInterruptedReceiptWithoutInventingFinishOrIgnoringLiveClaim(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	bootstrap, err := s.store.LoadBootstrapState(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	node.Status, node.LifecycleState = NodeStatusProvisioning, NodeStatusProvisioning
	node.UpdatedAt = s.now()
	if err := s.store.SaveNode(node, bootstrap); err != nil {
		t.Fatal(err)
	}
	record := NodeOperation{ID: newID(), NodeID: node.ID, State: "running", CreatedAt: node.UpdatedAt.Add(-time.Minute), CurrentPhase: "bootstrap", Request: NodeOperationRequest{Kind: "start"}}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.cfg.MetadataRoot, "_operations")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, record.ID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	host := newDaemonHost(s)
	t.Cleanup(func() { _ = host.Close() })
	evidence := requestFleetEvidence(t, host, fleetEvidenceRequest{Nodes: []string{node.ID}}).Nodes[node.ID]
	if evidence.Start == nil || evidence.Start.State != "interrupted" || evidence.Start.FinishedAt != nil || evidence.ClaimState != "unclaimed" || !evidence.NodeUpdatedAt.Equal(node.UpdatedAt) {
		t.Fatalf("incorrect recovered evidence: %+v", evidence)
	}
	token, ok, err := tryAcquireNodeOperation(s.cfg.MetadataRoot, node.ID)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	defer token.release()
	evidence = requestFleetEvidence(t, host, fleetEvidenceRequest{Nodes: []string{node.ID}}).Nodes[node.ID]
	if evidence.ClaimState != "active" {
		t.Fatalf("live claim lost: %+v", evidence)
	}
}

func TestFleetEvidenceKeepsFailureAndRecentNodeHistoryIndependent(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	host := newDaemonHost(s)
	t.Cleanup(func() { _ = host.Close() })
	ops, err := host.nodeOperations()
	if err != nil {
		t.Fatal(err)
	}
	now := s.now()
	started, finished := now.Add(-time.Minute), now.Add(-40*time.Second)
	duration, exitCode := int64(6000), 23
	bootstrap, err := s.store.LoadBootstrapState(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap.Steps = []BootstrapStep{{Kind: "bootstrap", State: BootstrapStepSucceeded},
		{Kind: "bootstrap", State: BootstrapStepFailed, SourceName: "tools", StartedAt: &started, FinishedAt: &finished,
			DurationMillis: &duration, ExitCode: &exitCode, ExecutionNodeID: node.ID, Command: "DO-NOT-SERIALIZE"},
		{Kind: "validation", State: BootstrapStepPending}}
	node.Status = NodeStatusFailed
	if err := s.store.SaveNode(node, bootstrap); err != nil {
		t.Fatal(err)
	}
	start := NodeOperation{ID: newID(), NodeID: node.ID, State: "failed", CreatedAt: now.Add(-2 * time.Minute), FinishedAt: &finished,
		CurrentPhase: "bootstrap", Request: NodeOperationRequest{Kind: "start"}, Output: []string{"PRIVATE-OUTPUT"},
		Phases: []NodeOperationPhase{{ID: "bootstrap", Label: "Bootstrap", State: "failed"}}}
	stop := NodeOperation{ID: newID(), NodeID: node.ID, State: "succeeded", CreatedAt: now.Add(-30 * time.Second), FinishedAt: &now, Request: NodeOperationRequest{Kind: "stop"}}
	ops.records[start.ID], ops.records[stop.ID] = &runningNodeOperation{record: start}, &runningNodeOperation{record: stop}
	journal, err := host.activityJournal()
	if err != nil {
		t.Fatal(err)
	}
	_, err = journal.Append(activityMessageRequest{ID: newID(), NodeID: node.ID, Source: "ports", Text: "selected route added"})
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		_, err = journal.Append(activityMessageRequest{ID: newID(), NodeID: newID(), Source: "ports", Text: "unrelated node"})
		if err != nil {
			t.Fatal(err)
		}
	}
	page := requestFleetEvidence(t, host, fleetEvidenceRequest{Nodes: []string{node.ID}, RecentNode: node.ID})
	evidence := page.Nodes[node.ID]
	if evidence.Start == nil || evidence.Start.ID != start.ID || evidence.Step == nil || evidence.Step.Index != 2 || evidence.Step.Total != 2 || evidence.Step.DurationMillis == nil || *evidence.Step.DurationMillis != 6000 {
		t.Fatalf("failure lost to later stop or step total included validation: %+v", evidence)
	}
	if evidence.StoppedAt == nil || !evidence.StoppedAt.Equal(now) {
		t.Fatal("confirmed stop time missing")
	}
	if len(page.Recent) != 3 || page.RecentError != "" {
		t.Fatalf("bad recent projection: %+v", page)
	}
	for _, event := range page.Recent {
		if event.NodeID != node.ID {
			t.Fatal("another node leaked into recent history")
		}
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "DO-NOT-SERIALIZE") || strings.Contains(string(encoded), "PRIVATE-OUTPUT") {
		t.Fatal("evidence exposed command or output bodies")
	}
	newer := NodeOperation{ID: newID(), NodeID: node.ID, State: "failed", CreatedAt: now, CurrentPhase: "boot", Request: NodeOperationRequest{Kind: "start"}}
	ops.records[newer.ID] = &runningNodeOperation{record: newer}
	page = requestFleetEvidence(t, host, fleetEvidenceRequest{Nodes: []string{node.ID}})
	if page.Nodes[node.ID].Step != nil || page.Nodes[node.ID].StoppedAt != nil || page.Nodes[node.ID].Start.ID != newer.ID {
		t.Fatal("new boot failure blamed an old step or stop")
	}
}

func TestFleetEvidenceRejectsUnboundedAndInvalidRequests(t *testing.T) {
	s, _ := newTestService(t)
	host := newDaemonHost(s)
	t.Cleanup(func() { _ = host.Close() })
	tooMany := make([]string, fleetEvidenceNodeLimit+1)
	for i := range tooMany {
		tooMany[i] = newID()
	}
	for _, request := range []fleetEvidenceRequest{{Nodes: tooMany}, {Nodes: []string{"../other"}}, {Nodes: []string{newID()}, RecentNode: newID()}} {
		raw, _ := json.Marshal(request)
		_, err := host.Handle(context.Background(), daemon.ClientContext{}, "node.fleet_evidence", raw)
		if err == nil {
			t.Fatal("invalid evidence request accepted")
		}
	}
	if daemon.ClassifyMethod("node.fleet_evidence") != daemon.ClassQuery {
		t.Fatal("evidence read entered mutation delivery lane")
	}
}

func TestFleetEvidenceBoundsResponseAndIsolatesMetadataFailures(t *testing.T) {
	s, directory := newTestService(t)
	host := newDaemonHost(s)
	t.Cleanup(func() { _ = host.Close() })
	ops, err := host.nodeOperations()
	if err != nil {
		t.Fatal(err)
	}
	now := s.now()
	request := fleetEvidenceRequest{}
	for range fleetEvidenceNodeLimit {
		id := newID()
		request.Nodes = append(request.Nodes, id)
		node := Node{ID: id, Slug: id, SandboxName: id, DirectoryPath: directory, Status: NodeStatusFailed}
		step := BootstrapStep{Kind: "bootstrap", SourceName: strings.Repeat("source", 1000), State: BootstrapStepFailed, StartedAt: &now, ExecutionNodeID: id, Command: strings.Repeat("private-command", 1000)}
		if err := s.store.SaveNode(node, BootstrapState{Steps: []BootstrapStep{step}}); err != nil {
			t.Fatal(err)
		}
		op := NodeOperation{ID: newID(), NodeID: id, State: "failed", CurrentPhase: "bootstrap", CreatedAt: now, FinishedAt: &now, Error: strings.Repeat("error", 2000), Request: NodeOperationRequest{Kind: "start"}, Phases: []NodeOperationPhase{{ID: "bootstrap", Label: "Bootstrap", Detail: strings.Repeat("detail", 2000), State: "failed"}}}
		ops.records[op.ID] = &runningNodeOperation{record: op}
	}
	fake := s.sandbox.(*fakeSandbox)
	fake.mu.Lock()
	calls, listCalls, shellCalls := len(fake.calls), fake.listCalls, len(fake.shellCalls)
	fake.mu.Unlock()
	page := requestFleetEvidence(t, host, request)
	data, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Nodes) != fleetEvidenceNodeLimit || len(data) >= daemon.MaxMessageSize/2 || strings.Contains(string(data), "private-command") {
		t.Fatalf("unbounded projection: %d nodes, %d bytes", len(page.Nodes), len(data))
	}
	fake.mu.Lock()
	touched := len(fake.calls) != calls || fake.listCalls != listCalls || len(fake.shellCalls) != shellCalls
	fake.mu.Unlock()
	if touched {
		t.Fatal("evidence query prepared or probed the runtime")
	}
	if err := os.WriteFile(s.store.nodeBootstrapPath(request.Nodes[0]), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.cfg.MetadataRoot, "_activity"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.cfg.MetadataRoot, "_activity", newID()+".json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	request.RecentNode = request.Nodes[1]
	page = requestFleetEvidence(t, host, request)
	if page.Nodes[request.Nodes[0]].Error == "" || page.Nodes[request.Nodes[1]].Step == nil || page.RecentError == "" {
		t.Fatalf("one corrupt source hid unrelated evidence: %+v", page.Nodes[request.Nodes[1]])
	}
}

func TestFleetEvidenceStepRequiresMatchingAttemptNodeAndPhase(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Minute)
	nodeID := newID()
	bootstrap := BootstrapState{Steps: []BootstrapStep{
		{Kind: "bootstrap", State: BootstrapStepFailed, StartedAt: &old, ExecutionNodeID: nodeID},
		{Kind: "bootstrap", State: BootstrapStepFailed, StartedAt: &now, ExecutionNodeID: newID()},
		{Kind: "validation", State: BootstrapStepFailed, StartedAt: &now, ExecutionNodeID: nodeID},
	}}
	latest := &fleetLifecycleEvidence{Phase: "bootstrap", CreatedAt: now}
	if got := summarizeFleetStep(nodeID, bootstrap, latest); got != nil {
		t.Fatalf("wrong attempt/node/phase attributed: %+v", got)
	}
	latest.Phase = "validation"
	if got := summarizeFleetStep(nodeID, bootstrap, latest); got == nil || got.Kind != "validation" || got.Total != 1 {
		t.Fatalf("validation step missing: %+v", got)
	}
}
