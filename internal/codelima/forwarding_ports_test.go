package codelima

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func TestForwardingPortsProjectionIncludesStoppedAndHeldMappings(t *testing.T) {
	h := newForwardingHarness(t, "ready")
	stopped := saveForwardingTestNode(t, h.service, "stopped")
	stopped.Ports = []string{"15432:5432"}
	if err := h.service.store.SaveNode(stopped, BootstrapState{}); err != nil {
		t.Fatal(err)
	}
	setForwardingObservationStatus(t, h.service, stopped.SandboxName, ObservationStopped)
	held := saveForwardingTestNode(t, h.service, "starting")
	held.Status, held.BootstrapCompleted, held.Ports = NodeStatusProvisioning, false, []string{"18081:8081"}
	if err := h.service.store.SaveNode(held, BootstrapState{}); err != nil {
		t.Fatal(err)
	}
	h.reconcile()
	snapshot := h.forwarder.portsSnapshot()
	if len(snapshot.Nodes) != 3 || len(snapshot.Rows) != 3 {
		t.Fatalf("projection omitted configured nodes: %+v", snapshot)
	}
	states := map[string]string{}
	for _, row := range snapshot.Rows {
		states[row.NodeSlug] = row.Status
	}
	if !reflect.DeepEqual(states, map[string]string{"ready": "live", "stopped": "stopped", "starting": "held"}) {
		t.Fatalf("states = %v", states)
	}
	h.forwarder.mu.Lock()
	h.forwarder.inventoryErr = "runtime unavailable"
	h.forwarder.mu.Unlock()
	if retained := h.forwarder.portsSnapshot(); retained.InventoryError == "" || len(retained.Rows) != 3 {
		t.Fatal("inventory failure appeared as an empty fleet")
	}
}

func TestForwardingPortsProcessNamesAreObservedAndOptional(t *testing.T) {
	data := []byte("codelima-listener LISTEN 0 4096 127.0.0.1:3000 0.0.0.0:* users:((\"node\",pid=12,fd=3))\n" +
		"codelima-listener LISTEN 0 4096 [::1]:5173 [::]:* users:((\"vite\",pid=14,fd=3))\n" +
		"codelima-listener LISTEN 0 4096 192.0.2.1:3000 0.0.0.0:* users:((\"unrelated\",pid=13,fd=3))\n" +
		"codelima-listener LISTEN 0 4096 0.0.0.0:8080 0.0.0.0:*\n")
	if got := parseListenerProcesses(data); !reflect.DeepEqual(got, map[int]string{3000: "node", 5173: "vite"}) {
		t.Fatalf("names = %v", got)
	}
	if got := parseListenerProcesses([]byte("ss: not found")); len(got) != 0 {
		t.Fatalf("invented labels: %v", got)
	}
}

func TestForwardingPolicyFirstClaimSurvivesRestart(t *testing.T) {
	service, factory, port, peers := newSharedPortForwardingFixture(t, "alpha", "beta")
	f, clock := newClockedTestDynamicForwarder(service, factory)
	f.listen = ephemeralForwardingListen
	f.policyPath = filepath.Join(t.TempDir(), "forwarding.json")
	t.Cleanup(func() { _ = f.Close() })
	peers["alpha"].setPorts(nil)
	f.reconcile(context.Background()) // beta is first, despite sorting after alpha.
	clock.advance(time.Second)
	peers["alpha"].setPorts([]int{port})
	f.reconcile(context.Background())
	if err := f.persistRoutingPolicy(); err != nil {
		t.Fatal(err)
	}
	next := newTestDynamicForwarder(service, factory)
	next.listen, next.policyPath = ephemeralForwardingListen, f.policyPath
	t.Cleanup(func() { _ = next.Close() })
	next.loadRoutingPolicy()
	next.reconcile(context.Background())
	if got := forwardingClaimant(t, next, port); got != "beta" {
		t.Fatalf("restart reordered first claim: %s", got)
	}
}

func TestForwardingPolicyWritesRequireCurrentDaemonOwnership(t *testing.T) {
	h := newForwardingHarness(t, "owned")
	h.reconcile()
	h.forwarder.policyPath = filepath.Join(t.TempDir(), "forwarding.json")
	host := newDaemonHost(h.service)
	host.forwarder = h.forwarder
	host.serving.Store(true)
	host.persistForwardingPolicy()
	if _, err := os.Stat(h.forwarder.policyPath); !os.IsNotExist(err) {
		t.Fatal("unowned daemon wrote policy")
	}
	host.activityEvents.owned.Store(true)
	host.updateGate.Lock()
	host.persistForwardingPolicy() // Must return without waiting behind handoff.
	host.updateGate.Unlock()
	if _, err := os.Stat(h.forwarder.policyPath); !os.IsNotExist(err) {
		t.Fatal("policy write crossed handoff barrier")
	}
	host.persistForwardingPolicy()
	before, err := os.ReadFile(h.forwarder.policyPath)
	if err != nil {
		t.Fatal(err)
	}
	host.replaced.Store(true)
	h.forwarder.mu.Lock()
	h.forwarder.routes = map[dynamicRouteKey]*dynamicForwardingRoute{}
	h.forwarder.mu.Unlock()
	host.persistForwardingPolicy()
	after, err := os.ReadFile(h.forwarder.policyPath)
	if err != nil || string(before) != string(after) {
		t.Fatal("replaced daemon overwrote successor preferences")
	}
}

func TestForwardingPortsPinUsesControlAndRejectsReplacedHost(t *testing.T) {
	if daemon.ClassifyMethod("ports.pin") != daemon.ClassControl || daemon.SeatArbitratedMethod("ports.pin") || daemon.ClassifyMethod("ports.list") != daemon.ClassQuery {
		t.Fatal("ports RPC ownership classification drifted")
	}
	h := newForwardingHarness(t, "pin")
	h.reconcile()
	host := newDaemonHost(h.service)
	host.forwarder = h.forwarder
	host.replaced.Store(true)
	raw, err := json.Marshal(daemon.PinPortParams{NodeID: h.node.ID, Port: h.port})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.handlePorts("ports.pin", raw); err == nil {
		t.Fatal("replaced host accepted a pin")
	}
}

func TestForwardingPortsDropsProcessLabelOnProbeFailure(t *testing.T) {
	h := newForwardingHarness(t, "process")
	h.reconcile()
	h.forwarder.nodes[h.node.ID].portProcesses = map[int]string{h.port: "node"}
	if got := h.forwarder.portsSnapshot().Rows[0].Process; got != "node" {
		t.Fatal(got)
	}
	h.forwarder.recordNodeFailure(h.node, os.ErrDeadlineExceeded)
	if got := h.forwarder.portsSnapshot().Rows[0].Process; got != "" {
		t.Fatalf("stale process remained: %s", got)
	}
}

func TestForwardingPortsPublishedTCPActivityTransitions(t *testing.T) {
	h := newForwardingHarness(t, "tcp-activity")
	node := h.node
	node.Ports = []string{fmt.Sprintf("%d:5432", h.port)}
	if err := h.service.store.SaveNode(node, BootstrapState{}); err != nil {
		t.Fatal(err)
	}
	events := newDaemonHost(h.service).activityEvents
	h.forwarder.activityObserver = events.observeForwarding
	h.reconcile()
	h.reconcile()
	if len(events.pending) != 2 {
		t.Fatalf("raw/HTTP endpoint duplicated Activity: %+v", events.pending)
	}
	h.peer.failScans(os.ErrDeadlineExceeded)
	h.reconcile()
	h.clock.advance(forwardingRouteGrace + time.Second)
	h.reconcile()
	h.peer.failScans(nil)
	h.clock.advance(forwardingBackoffMaximum + time.Second)
	h.reconcile()
	node.Status, node.BootstrapCompleted = NodeStatusFailed, false
	if err := h.service.store.SaveNode(node, BootstrapState{}); err != nil {
		t.Fatal(err)
	}
	h.reconcile()
	var texts []string
	for _, event := range events.pending {
		if event.NodeID != node.ID {
			t.Fatalf("lost endpoint identity: %+v", event)
		}
		texts = append(texts, event.Text)
	}
	joined := strings.Join(texts, "\n")
	for _, transition := range []string{"added", "degraded", "restored", "removed"} {
		if !strings.Contains(joined, fmt.Sprintf("route :%d %s", h.port, transition)) {
			t.Fatalf("missing %s: %s", transition, joined)
		}
	}
	if len(texts) != 6 {
		t.Fatalf("unexpected duplicate transitions: %v", texts)
	}
}
