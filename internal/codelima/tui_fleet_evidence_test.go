package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"go.rockorager.dev/vaxis"
)

func TestTUIFleetEvidenceOverviewRequestsOnlySelectedNodeAtEveryWidth(t *testing.T) {
	for _, width := range []int{80, 150} {
		a, _ := newTestManager(t, width, 24)
		a.nodeDetail, a.detailTab = true, tuiDetailOverview
		a.fleet.marks[a.fleet.selectedID] = true
		request := a.fleetEvidenceRequest()
		if !a.fleetEvidenceVisible() || request.RecentNode != a.fleet.selectedID || len(request.Nodes) != 1 || request.Nodes[0] != a.fleet.selectedID {
			t.Fatalf("Overview at %d did not request its selected preview: %+v", width, request)
		}
		a.fleetEvidence.serial = 4
		a.applyFleetEvidence(tuiFleetEvidenceEvent{serial: 4, request: request, snapshot: fleetEvidenceSnapshot{RecentNode: request.RecentNode}})
		if !a.fleetEvidence.loaded || a.fleetEvidenceStale() {
			t.Fatal("Overview response was discarded")
		}
		a.detailTab = tuiDetailMetrics
		if a.fleetEvidenceVisible() || len(a.fleetEvidenceRequest().Nodes) != 0 {
			t.Fatal("hidden preview kept requesting evidence")
		}
		a.applyFleetEvidence(tuiFleetEvidenceEvent{serial: 4, request: request, err: errors.New("late failure")})
		if a.fleetEvidence.err != nil {
			t.Fatal("hidden Overview accepted a late response")
		}
	}
}

func TestTUIFleetEvidenceFencesSelectionAndKeepsRecentIndependent(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	a.draw()
	request := a.fleetEvidenceRequest()
	a.fleetEvidence.serial = 3
	old := request.RecentNode
	for _, r := range a.fleet.records {
		if r.Node.ID != old {
			a.fleet.selectID(r.Node.ID)
			break
		}
	}
	a.selectFleetNode()
	a.applyFleetEvidence(tuiFleetEvidenceEvent{serial: 3, request: request, snapshot: fleetEvidenceSnapshot{RecentNode: old}})
	if a.fleetEvidence.loaded {
		t.Fatal("late selection response installed")
	}
	request = a.fleetEvidenceRequest()
	a.activity.page.History = []ActivityRecord{{NodeID: request.RecentNode, Text: "older Activity page"}}
	e := tuiFleetEvidenceEvent{serial: 3, request: request, snapshot: fleetEvidenceSnapshot{RecentNode: request.RecentNode, Recent: []ActivityRecord{
		{NodeID: request.RecentNode, Time: time.Now(), Text: "latest selected event"}, {NodeID: old, Text: "wrong node event"}}}}
	a.applyFleetEvidence(e)
	a.draw()
	screen := renderedScreenText(t, a.vx, 150, 38)
	if !strings.Contains(screen, "latest selected event") || strings.Contains(screen, "older Activity page") || strings.Contains(screen, "wrong node event") {
		t.Fatalf("mixed history:\n%s", screen)
	}
	a.applyFleetEvidence(tuiFleetEvidenceEvent{serial: 3, request: request, err: errors.New("offline")})
	a.draw()
	screen = renderedScreenText(t, a.vx, 150, 38)
	if !strings.Contains(screen, "Recent · stale") || !strings.Contains(screen, "latest selected event") {
		t.Fatalf("lost retained history warning:\n%s", screen)
	}
}

func TestTUIFleetEvidenceShowsFailedStepWithoutAttributingItToNewStart(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		a, _ := newTestManager(t, size[0], size[1])
		node := &a.state.nodes[0]
		node.Status = NodeStatusFailed
		code, duration := 23, int64(6000)
		a.fleetEvidence = tuiFleetEvidenceState{loaded: true, receivedAt: time.Now(), snapshot: fleetEvidenceSnapshot{Nodes: map[string]fleetNodeEvidence{
			node.ID: {Start: &fleetLifecycleEvidence{ID: "old", State: "failed", Phase: "bootstrap"}, Step: &fleetStepEvidence{Kind: "bootstrap", Index: 2, Total: 3, Source: "tools", State: BootstrapStepFailed, ExitCode: &code, DurationMillis: &duration}},
		}}}
		a.draw()
		screen := renderedScreenText(t, a.vx, size[0], size[1])
		if !strings.Contains(screen, "bootstrap 2/3") || size[0] > 100 && (!strings.Contains(screen, "exit 23") || !strings.Contains(screen, "6s")) {
			t.Fatalf("missing retained failure at %v:\n%s", size, screen)
		}
		a.activity.page.Running = []NodeOperation{{NodeID: node.ID, ID: "new", State: "running", Request: NodeOperationRequest{Kind: "start"}, Progress: &NodeOperationProgress{Phase: "Boot", Completed: 1, Total: 7}}}
		a.draw()
		screen = renderedScreenText(t, a.vx, size[0], size[1])
		if strings.Contains(screen, "bootstrap 2/3") || !strings.Contains(screen, "Boot 1/7 phases") {
			t.Fatalf("new attempt blamed old failure:\n%s", screen)
		}
	}
}

func TestTUIFleetEvidenceInterruptedReceiptSupersedesItsStaleInflightStatus(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	node := a.state.nodes[0]
	now := time.Now()
	node.Status, node.UpdatedAt = NodeStatusProvisioning, now.Add(-time.Minute)
	a.fleetEvidence = tuiFleetEvidenceState{loaded: true, receivedAt: now, snapshot: fleetEvidenceSnapshot{Nodes: map[string]fleetNodeEvidence{
		node.ID: {NodeUpdatedAt: node.UpdatedAt, ClaimState: "unclaimed", Start: &fleetLifecycleEvidence{ID: "interrupted", State: "interrupted", Phase: "bootstrap"}},
	}}}
	if got := a.fleetStatus(node); got != "failed" {
		t.Fatalf("interrupted start still shown as %s", got)
	}
	evidence := a.fleetEvidence.snapshot.Nodes[node.ID]
	evidence.ClaimState = "active"
	a.fleetEvidence.snapshot.Nodes[node.ID] = evidence
	if got := a.fleetStatus(node); got != "starting" {
		t.Fatalf("retained receipt overrode a live direct lifecycle claim: %s", got)
	}
	evidence.ClaimState = "unclaimed"
	a.fleetEvidence.snapshot.Nodes[node.ID] = evidence
	node.UpdatedAt = now.Add(time.Second)
	if got := a.fleetStatus(node); got != "starting" {
		t.Fatalf("older receipt overrode a new lifecycle attempt: %s", got)
	}
	node.UpdatedAt = now.Add(-time.Minute)
	a.activity.page.Running = []NodeOperation{{ID: "new-start", NodeID: node.ID, State: "running", Request: NodeOperationRequest{Kind: "start"}}}
	if got := a.fleetStatus(node); got != "starting" {
		t.Fatalf("older receipt overrode a new running ID: %s", got)
	}
}

type fleetEvidenceBlockingHost struct {
	*daemonHost
	gate    chan struct{}
	entered chan fleetEvidenceRequest
}

func (h *fleetEvidenceBlockingHost) Handle(ctx context.Context, client daemon.ClientContext, method string, raw json.RawMessage) (any, error) {
	if method == "node.fleet_evidence" {
		var request fleetEvidenceRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		h.entered <- request
		select {
		case <-h.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return h.daemonHost.Handle(ctx, client, method, raw)
}

func TestTUIFleetEvidenceReadCancelsOnNavigationWithoutLosingCache(t *testing.T) {
	s, _ := newHandoffTestService(t)
	h := &fleetEvidenceBlockingHost{daemonHost: newDaemonHost(s), gate: make(chan struct{}), entered: make(chan fleetEvidenceRequest, 4)}
	var once sync.Once
	release := func() { once.Do(func() { close(h.gate) }) }
	ctx, cancel := context.WithCancel(context.Background())
	server := daemon.NewServer(daemon.Config{Home: s.cfg.MetadataRoot, Version: Version, Handler: h})
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() { release(); cancel(); <-done })
	waitForDaemonPing(t, s.cfg.MetadataRoot)
	client, err := daemonclient.Dial(ctx, daemonclient.Options{Home: s.cfg.MetadataRoot, Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	s.daemonClient = client
	a, _ := newTestManager(t, 150, 38)
	a.service = s
	for i := range a.state.nodes {
		a.state.nodes[i].ID = newID()
	}
	a.refreshFleet()
	a.fleet.selectedID = a.state.nodes[0].ID
	a.draw()
	events := make(chan vaxis.Event, 4)
	a.postEvent = func(e vaxis.Event) { events <- e }
	a.fleetEvidence = tuiFleetEvidenceState{loaded: true, snapshot: fleetEvidenceSnapshot{RecentNode: a.fleet.selectedID, Recent: []ActivityRecord{{Text: "retained"}}}}
	a.startFleetEvidenceLoad(true)
	select {
	case <-h.entered:
	case <-time.After(time.Second):
		t.Fatal("read did not reach daemon")
	}
	serial := a.fleetEvidence.serial
	a.startFleetEvidenceLoad(true)
	if serial != a.fleetEvidence.serial {
		t.Fatal("duplicate in-flight read")
	}
	a.view = tuiViewTemplates
	a.startFleetEvidenceLoad(false)
	select {
	case raw := <-events:
		e := raw.(tuiFleetEvidenceEvent)
		if e.err == nil {
			t.Fatal("navigation failed to cancel")
		}
		a.applyFleetEvidence(e)
	case <-time.After(time.Second):
		t.Fatal("cancellation blocked event loop")
	}
	if a.fleetEvidence.loading || len(a.fleetEvidence.snapshot.Recent) != 1 {
		t.Fatal("navigation lost retained cache")
	}
	release()
	a.view = tuiViewNodes
	a.startFleetEvidenceLoad(true)
	select {
	case raw := <-events:
		a.applyFleetEvidence(raw.(tuiFleetEvidenceEvent))
	case <-time.After(time.Second):
		t.Fatal("fresh read did not recover")
	}
	if a.fleetEvidence.err != nil || a.fleetEvidence.loading || len(a.fleetEvidence.snapshot.Nodes) != 2 {
		t.Fatalf("recovery failed: %+v", a.fleetEvidence)
	}
}
