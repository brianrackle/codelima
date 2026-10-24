package codelima

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/hostinfo"
)

func TestActivityNativeRoutesRecordTransitionsAndOwnershipOnce(t *testing.T) {
	s, _ := newTestService(t)
	h := newDaemonHost(s)
	e := h.activityEvents
	a := activityRoute{NodeID: newID(), NodeSlug: "alpha", Port: 1455, Serving: true}
	b := activityRoute{NodeID: newID(), NodeSlug: "beta", Port: 1455, Serving: true}
	view := forwardingActivitySnapshot{Routes: map[dynamicRouteKey]activityRoute{{node: "alpha", port: 1455}: a}, Owners: map[int]activityRoute{1455: a}}
	e.observeForwarding(view)
	e.observeForwarding(view)
	if len(e.pending) != 2 {
		t.Fatal("unchanged routing generated extra records", e.pending)
	}
	a.Lapsed = true
	view.Routes[dynamicRouteKey{node: "alpha", port: 1455}] = a
	e.observeForwarding(view)
	a.Lapsed = false
	view.Routes[dynamicRouteKey{node: "alpha", port: 1455}] = a
	view.Routes[dynamicRouteKey{node: "beta", port: 1455}] = b
	view.Owners[1455] = b
	e.observeForwarding(view)
	delete(view.Routes, dynamicRouteKey{node: "alpha", port: 1455})
	e.observeForwarding(view)
	var texts []string
	for _, request := range e.pending {
		texts = append(texts, request.Text)
		if request.NodeID == "" {
			t.Fatal("route message lost its node identity", request)
		}
	}
	joined := strings.Join(texts, "\n")
	for _, want := range []string{"route :1455 added", "route :1455 degraded", "route :1455 restored", "route :1455 removed", "now owns localhost:1455 · was alpha"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
	if len(e.pending) != 7 {
		t.Fatal("unexpected transition count", texts)
	}
}

func TestActivityNativeCapacityUsesObservedGlobalAllocationAndUnknownRecovery(t *testing.T) {
	s, _ := newTestService(t)
	e := newDaemonHost(s).activityEvents
	memory := uint64(2 << 30)
	capacity := hostinfo.Snapshot{CPUs: 2, MemoryBytes: &memory}
	nodes := []Node{{ID: newID(), VCPUs: 3, MemoryMiB: 3072, Status: NodeStatusFailed, LastRuntimeObservation: &RuntimeObservation{Exists: true, Status: ObservationRunning}}}
	e.observeCapacity(capacity, nodes)
	e.observeCapacity(capacity, nodes)
	if len(e.pending) != 2 || e.pending[0].Level != slog.LevelWarn || !strings.Contains(e.pending[0].Text, "3 of 2 allocated") {
		t.Fatal("failed-but-running VM allocation was missed or repeated", e.pending)
	}
	nodes[0].LastRuntimeObservation = nil
	e.observeCapacity(capacity, nodes)
	if len(e.pending) != 2 {
		t.Fatal("unknown VM invented capacity recovery", e.pending)
	}
	nodes[0].LastRuntimeObservation = &RuntimeObservation{Exists: true, Status: ObservationBroken}
	e.observeCapacity(capacity, nodes)
	if len(e.pending) != 2 {
		t.Fatal("broken runtime invented released resources", e.pending)
	}
	nodes[0].LastRuntimeObservation = &RuntimeObservation{Exists: true, Status: ObservationStopped}
	e.observeCapacity(capacity, nodes)
	if len(e.pending) != 4 || e.pending[2].Level != slog.LevelInfo || !strings.Contains(e.pending[2].Text, "within capacity") {
		t.Fatal("observed stop did not recover capacity", e.pending)
	}
}

func TestActivityNativePersistenceRequiresOwnershipAndRetriesSameID(t *testing.T) {
	s, _ := newTestService(t)
	h := newDaemonHost(s)
	e := h.activityEvents
	e.enqueue(activityMessageRequest{ID: newID(), Source: "host", Level: slog.LevelWarn, Text: "capacity warning"})
	e.flush()
	if _, err := os.Stat(filepath.Join(s.cfg.MetadataRoot, "_activity")); !os.IsNotExist(err) {
		t.Fatal("producer wrote before server ownership", err)
	}
	h.markServing()
	e.owned.Store(true)
	h.updateGate.Lock()
	e.flush()
	h.updateGate.Unlock()
	if len(e.pending) != 1 {
		t.Fatal("producer crossed the update barrier")
	}
	j, err := h.activityJournal()
	if err != nil {
		t.Fatal(err)
	}
	id := e.pending[0].ID
	blocked := filepath.Join(j.dir, id+".json")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	e.flush()
	if len(e.pending) != 1 || e.pending[0].ID != id {
		t.Fatal("failed persistence lost its retry identity", e.pending)
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	e.flush()
	records, through := j.Snapshot()
	if len(e.pending) != 0 || len(records) != 1 || records[0].ID != id || through != 1 {
		t.Fatal("retry duplicated or lost the native message", records, through)
	}
	h.replaced.Store(true)
	e.enqueue(activityMessageRequest{ID: newID(), Source: "host", Text: "after ownership ended"})
	e.flush()
	if records, _ := j.Snapshot(); len(records) != 1 {
		t.Fatal("retired daemon continued writing", records)
	}
}

func TestActivityNativeQueueBoundsLossAndReportsItAfterRecovery(t *testing.T) {
	s, _ := newTestService(t)
	h := newDaemonHost(s)
	e := h.activityEvents
	for range nativeActivityQueueLimit + 3 {
		e.enqueue(activityMessageRequest{ID: newID(), Source: "ports", Text: "route changed"})
	}
	if len(e.pending) != nativeActivityQueueLimit || e.dropped != 3 {
		t.Fatal("native queue is unbounded or hid overflow", len(e.pending), e.dropped)
	}
	h.markServing()
	e.owned.Store(true)
	for range 10 {
		e.flush()
	}
	j, err := h.activityJournal()
	if err != nil {
		t.Fatal(err)
	}
	records, _ := j.Snapshot()
	if len(records) != nativeActivityQueueLimit+1 || !strings.Contains(records[0].Text, "3 native Activity events") {
		t.Fatal("queue loss was not reported after recovery", len(records), records[0])
	}
}

func TestActivityNativeWorkerStopsWithServerContext(t *testing.T) {
	s, _ := newTestService(t)
	h := newDaemonHost(s)
	h.markServing()
	ctx, cancel := context.WithCancel(context.Background())
	h.startActivityEvents(ctx)
	cancel()
	done := make(chan struct{})
	go func() { h.activityEvents.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("native event worker did not stop")
	}
}

func TestActivityNativeForwarderPublishesAfterUnlockWithoutATUI(t *testing.T) {
	s, _ := newTestService(t)
	node := saveForwardingTestNode(t, s, "sandbox-name")
	node.Slug = "display-name"
	if err := s.store.SaveNode(node, BootstrapState{}); err != nil {
		t.Fatal(err)
	}
	port := reserveDualLoopbackPort(t)
	peer := &controllableForwardingPeer{ports: []int{port}}
	f := newTestDynamicForwarder(s, &fakeForwardingPeerFactory{peers: map[string]*controllableForwardingPeer{node.SandboxName: peer}})
	defer func() { _ = f.Close() }()
	h := newDaemonHost(s)
	f.activityObserver = func(view forwardingActivitySnapshot) {
		if !f.mu.TryLock() {
			t.Error("activity callback ran while forwarding was locked")
			return
		}
		f.mu.Unlock()
		h.activityEvents.observeForwarding(view)
	}
	f.reconcile(context.Background())
	f.reconcile(context.Background())
	peer.mu.Lock()
	peer.ports = nil
	peer.mu.Unlock()
	f.reconcile(context.Background())
	h.markServing()
	h.activityEvents.owned.Store(true)
	h.activityEvents.flush()
	j, err := h.activityJournal()
	if err != nil {
		t.Fatal(err)
	}
	records, _ := j.Snapshot()
	if len(records) != 4 {
		t.Fatal("route/owner transitions were missed or repeated", records)
	}
	for _, record := range records {
		if record.NodeID != node.ID || record.NodeSlug != node.Slug {
			t.Fatal("native transition did not retain node identity and display slug", record)
		}
	}
}
