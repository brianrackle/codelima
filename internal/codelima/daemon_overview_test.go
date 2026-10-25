package codelima

import (
	"net"
	"testing"
	"time"
)

func TestDaemonOverviewUsesObservedForwardingAndReclaimState(t *testing.T) {
	service, _ := newTestService(t)
	service.cfg.Daemon.VirtioFSReclaim = true
	h := &daemonHost{service: service}
	got := h.Overview()
	if got.Forwarding.Enabled || got.Reclaim.Supported || !got.Reclaim.Enabled || !got.UpdatedAt.IsZero() {
		t.Fatalf("invented runtime evidence: %+v", got)
	}
	now := time.Now().UTC()
	h.updatedAt = now
	h.virtioFSSupported = true
	h.virtioFSReclaimTicker = newVirtioFSReclaimTicker(nil, time.Minute)
	h.virtioFSReclaimTicker.state.LastRunAt = now
	h.virtioFSReclaimTicker.state.LastReclaimedNodes = 4
	h.virtioFSReclaimTicker.state.LastError = "one node failed"
	h.forwarder = &dynamicForwarder{
		prepared: true, interval: 2 * time.Second, lastPoll: now, inventoryErr: "list failed", lastErr: "port in use",
		servers: map[int]*dynamicPortServer{
			3000: {status: "serving", listeners: make([]net.Listener, 2)},
			4000: {status: "conflicted"},
		},
		routes: map[dynamicRouteKey]*dynamicForwardingRoute{{port: 3000}: {lapsed: true}, {port: 4000}: {}},
	}
	got = h.Overview()
	f, r := got.Forwarding, got.Reclaim
	if !got.UpdatedAt.Equal(now) || !f.Enabled || !f.Authorized || f.Listeners != 2 || f.Ports != 2 || f.DegradedPorts != 1 || f.LapsedRoutes != 1 || f.IntervalSeconds != 2 || !f.LastPollAt.Equal(now) || f.InventoryError != "list failed" || f.LastError != "port in use" {
		t.Fatalf("incorrect forwarding summary: %+v", got)
	}
	if !r.Supported || !r.Enabled || r.IntervalSeconds != 60 || !r.LastRunAt.Equal(now) || r.LastReclaimedNodes != 4 || r.LastError != "one node failed" {
		t.Fatalf("incorrect reclaim summary: %+v", r)
	}
	h.forwarder.routes = nil
	h.forwarder.servers = nil
	h.forwarder.inventoryErr = ""
	if next := h.Overview(); next.Forwarding.Listeners != 0 || next.Forwarding.DegradedPorts != 0 || next.Forwarding.LapsedRoutes != 0 {
		t.Fatalf("stale counts after recovery: %+v", next)
	}
}
