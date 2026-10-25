package codelima

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func TestForwardingPolicyPinSurvivesRestartAndDegradation(t *testing.T) {
	service, factory, port, peers := newSharedPortForwardingFixture(t, "alpha", "beta")
	f, clock := newClockedTestDynamicForwarder(service, factory)
	f.listen = ephemeralForwardingListen
	t.Cleanup(func() { _ = f.Close() })
	f.policyPath = filepath.Join(t.TempDir(), "forwarding.json")
	f.loadRoutingPolicy()
	f.reconcile(context.Background())
	beta, err := service.store.NodeBySandboxName("beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.pinBarePort(beta.ID, port); err != nil {
		t.Fatal(err)
	}
	if got := forwardingClaimant(t, f, port); got != "beta" {
		t.Fatalf("pinned owner = %s", got)
	}
	peers["beta"].failScans(os.ErrDeadlineExceeded)
	f.reconcile(context.Background())
	clock.advance(forwardingRouteGrace + time.Second)
	f.reconcile(context.Background())
	if got := forwardingClaimant(t, f, port); got != "beta" {
		t.Fatalf("degradation discarded pin: %s", got)
	}
	// A restart rediscovers live routes but retains the user's explicit owner.
	next := newTestDynamicForwarder(service, factory)
	next.listen = ephemeralForwardingListen
	next.policyPath = f.policyPath
	next.loadRoutingPolicy()
	t.Cleanup(func() { _ = next.Close() })
	peers["beta"].failScans(nil)
	next.reconcile(context.Background())
	if got := forwardingClaimant(t, next, port); got != "beta" {
		t.Fatalf("restart discarded pin: %s", got)
	}
	peers["beta"].setPorts(nil)
	next.reconcile(context.Background())
	if got := forwardingClaimant(t, next, port); got != "alpha" {
		t.Fatalf("stopped listener did not release pin: %s", got)
	}
	if err := next.persistRoutingPolicy(); err != nil {
		t.Fatal(err)
	}
	last := newTestDynamicForwarder(service, factory)
	last.policyPath = f.policyPath
	last.loadRoutingPolicy()
	if len(last.pins) != 0 {
		t.Fatal("released pin remained on disk")
	}
}

func TestForwardingPolicyWriteFailureDoesNotChangeOwner(t *testing.T) {
	service, factory, port, _ := newSharedPortForwardingFixture(t, "alpha", "beta")
	f := newTestDynamicForwarder(service, factory)
	f.listen = ephemeralForwardingListen
	t.Cleanup(func() { _ = f.Close() })
	f.reconcile(context.Background())
	f.policyPath = t.TempDir() // A directory cannot be atomically replaced with a file.
	beta, err := service.store.NodeBySandboxName("beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.pinBarePort(beta.ID, port); err == nil {
		t.Fatal("pin acknowledged despite persistence failure")
	}
	if got := forwardingClaimant(t, f, port); got != "alpha" {
		t.Fatalf("failed save changed owner: %s", got)
	}
}

func TestForwardingPolicyCorruptionIsRetained(t *testing.T) {
	f := newTestDynamicForwarder(nil, nil)
	f.policyPath = filepath.Join(t.TempDir(), "forwarding.json")
	if err := os.WriteFile(f.policyPath, []byte("broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.loadRoutingPolicy()
	if err := f.persistRoutingPolicy(); err == nil {
		t.Fatal("corrupt policy was silently replaced")
	}
	data, err := os.ReadFile(f.policyPath)
	if err != nil || string(data) != "broken\n" {
		t.Fatalf("corrupt evidence changed: %q %v", data, err)
	}
}

func TestForwardingPolicyHandoffKeepsFinalClaimsUntilRollback(t *testing.T) {
	for _, change := range []string{"listener disappears", "node deleted"} {
		t.Run(change, func(t *testing.T) {
			h := newForwardingHarness(t, "handoff")
			h.reconcile()
			f := h.forwarder
			f.policyPath = filepath.Join(t.TempDir(), "forwarding.json")
			if err := f.pinBarePort(h.node.ID, h.port); err != nil {
				t.Fatal(err)
			}
			if err := f.prepareRoutingHandoff(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(f.policyPath)
			if err != nil {
				t.Fatal(err)
			}
			apply := func() {
				if change == "listener disappears" {
					f.applyScanResult(h.node, nil, nil, time.Now())
				} else {
					f.syncMembership(nil, time.Now())
				}
			}
			apply()
			if f.pins[h.port] != h.node.ID || len(f.routes) != 1 {
				t.Fatal("discovery changed ownership after the final handoff save")
			}
			if err := f.persistRoutingPolicy(); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(f.policyPath)
			if err != nil || string(after) != string(before) {
				t.Fatal("handoff policy changed after preparation")
			}
			f.resumeRoutingAfterHandoff()
			apply()
			if len(f.pins) != 0 || len(f.routes) != 0 {
				t.Fatal("rollback did not resume ownership reconciliation")
			}
		})
	}
}

func TestForwardingPolicyFailedLiveUpdateResumesDiscovery(t *testing.T) {
	routes := newForwardingHarness(t, "handoff")
	routes.reconcile()
	f := routes.forwarder
	f.policyPath = filepath.Join(t.TempDir(), "forwarding.json")
	if err := f.pinBarePort(routes.node.ID, routes.port); err != nil {
		t.Fatal(err)
	}
	handoff := newUpdateHandoffHarness(t)
	handoff.host.forwarder = f
	done := handoff.startUpdate()
	peer, token := handoff.dialSuccessor(t)
	handoff.receiveHandoff(t, peer, token)
	f.applyScanResult(routes.node, nil, nil, time.Now())
	if f.pins[routes.port] != routes.node.ID {
		t.Fatal("update did not freeze claims before sending the handoff")
	}
	if err := peer.WriteJSON(daemon.HandoffMessage{Type: "failed", Error: "injected import failure"}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("update ignored the import failure")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("update did not roll back")
	}
	f.applyScanResult(routes.node, nil, nil, time.Now())
	if len(f.pins) != 0 || len(f.routes) != 0 {
		t.Fatal("failed update left discovery paused")
	}
}

func TestForwardingPolicyCallbackOrderSurvivesRestartButRediscoveryIsNew(t *testing.T) {
	service, factory, _, peers := newSharedPortForwardingFixture(t, "alpha", "beta")
	f, clock := newClockedTestDynamicForwarder(service, factory)
	f.listen = ephemeralForwardingListen
	f.policyPath = filepath.Join(t.TempDir(), "forwarding.json")
	t.Cleanup(func() { _ = f.Close() })
	peers["alpha"].setPorts(nil)
	peers["beta"].setPorts([]int{codexLoginCallbackPort})
	f.reconcile(context.Background())
	clock.advance(time.Second)
	peers["alpha"].setPorts([]int{codexLoginCallbackPort})
	f.reconcile(context.Background())
	if err := f.persistRoutingPolicy(); err != nil {
		t.Fatal(err)
	}
	next, nextClock := newClockedTestDynamicForwarder(service, factory)
	next.listen, next.policyPath = ephemeralForwardingListen, f.policyPath
	nextClock.advance(time.Minute)
	t.Cleanup(func() { _ = next.Close() })
	next.loadRoutingPolicy()
	next.reconcile(context.Background())
	if got := forwardingClaimant(t, next, codexLoginCallbackPort); got != "alpha" {
		t.Fatalf("restart reordered callback claim: %s", got)
	}
	peers["beta"].setPorts(nil)
	next.reconcile(context.Background())
	nextClock.advance(time.Second)
	peers["beta"].setPorts([]int{codexLoginCallbackPort})
	next.reconcile(context.Background())
	if got := forwardingClaimant(t, next, codexLoginCallbackPort); got != "beta" {
		t.Fatalf("genuine rediscovery retained old callback priority: %s", got)
	}
}

func TestForwardingPolicyDeletedRestoredNodeReleasesPin(t *testing.T) {
	h := newForwardingHarness(t, "deleted")
	h.reconcile()
	h.forwarder.policyPath = filepath.Join(t.TempDir(), "forwarding.json")
	if err := h.forwarder.pinBarePort(h.node.ID, h.port); err != nil {
		t.Fatal(err)
	}
	next := newTestDynamicForwarder(h.service, nil)
	next.policyPath = h.forwarder.policyPath
	next.loadRoutingPolicy()
	if len(next.pins) != 1 {
		t.Fatal("fixture did not restore pin")
	}
	next.syncMembership(nil, time.Now())
	if len(next.pins) != 0 || len(next.restored) != 0 {
		t.Fatal("deleted node retained ownership")
	}
	if err := next.persistRoutingPolicy(); err != nil {
		t.Fatal(err)
	}
}

func TestForwardingPolicyCloseRejectsLateWrites(t *testing.T) {
	h := newForwardingHarness(t, "closing")
	h.reconcile()
	h.forwarder.policyPath = filepath.Join(t.TempDir(), "forwarding.json")
	if err := h.forwarder.pinBarePort(h.node.ID, h.port); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(h.forwarder.policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.forwarder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.forwarder.pinBarePort(h.node.ID, h.port); err == nil {
		t.Fatal("closed forwarder accepted a pin")
	}
	if err := h.forwarder.persistRoutingPolicy(); err != nil {
		t.Fatal(err)
	}
	if err := h.forwarder.prepareRoutingHandoff(); err == nil {
		t.Fatal("closed forwarder admitted a late handoff writer")
	}
	after, err := os.ReadFile(h.forwarder.policyPath)
	if err != nil || string(before) != string(after) {
		t.Fatal("late shutdown write erased durable claims")
	}
}

func TestForwardingPolicyRestartDoesNotRedirectUnreachablePinnedGuest(t *testing.T) {
	service, factory, port, peers := newSharedPortForwardingFixture(t, "alpha", "beta")
	f := newTestDynamicForwarder(service, factory)
	f.listen = ephemeralForwardingListen
	f.policyPath = filepath.Join(t.TempDir(), "forwarding.json")
	t.Cleanup(func() { _ = f.Close() })
	f.reconcile(context.Background())
	beta, err := service.store.NodeBySandboxName("beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.pinBarePort(beta.ID, port); err != nil {
		t.Fatal(err)
	}
	peers["beta"].failScans(os.ErrDeadlineExceeded)
	next, clock := newClockedTestDynamicForwarder(service, factory)
	next.listen, next.policyPath = ephemeralForwardingListen, f.policyPath
	t.Cleanup(func() { _ = next.Close() })
	next.loadRoutingPolicy()
	next.reconcile(context.Background())
	if got := forwardingClaimant(t, next, port); got != "beta" {
		t.Fatalf("unreachable pinned guest redirected to %s", got)
	}
	handler := &dynamicForwardingHandler{forwarder: next, port: port}
	assertForwardingStatus(t, handler, "localhost:8080", http.StatusBadGateway)
	assertResponse := func(host, want string) {
		t.Helper()
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "http://"+host, nil)
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != want {
			t.Fatalf("%s: %d %q", host, response.Code, response.Body.String())
		}
	}
	assertResponse("alpha.localhost:8080", "alpha")
	peers["beta"].failScans(nil)
	clock.advance(2 * time.Second)
	next.reconcile(context.Background())
	assertResponse("localhost:8080", "beta")
}
