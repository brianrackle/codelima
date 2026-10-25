package codelima

import (
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func (h *daemonHost) Overview() daemon.RuntimeOverview {
	h.mu.RLock()
	updatedAt := h.updatedAt
	h.mu.RUnlock()
	result := daemon.RuntimeOverview{
		UpdatedAt: updatedAt,
		Reclaim: daemon.ReclaimOverview{
			Enabled: h.service.cfg.Daemon.VirtioFSReclaim, Supported: h.virtioFSSupported,
		},
	}
	if h.forwarder != nil {
		result.Forwarding = h.forwarder.overview()
	}
	if h.virtioFSReclaimTicker != nil {
		state := h.virtioFSReclaimTicker.Snapshot()
		result.Reclaim.IntervalSeconds = state.IntervalSeconds
		result.Reclaim.LastRunAt = state.LastRunAt
		result.Reclaim.NextRunAt = state.NextRunAt
		result.Reclaim.LastReclaimedNodes = state.LastReclaimedNodes
		result.Reclaim.LastError = state.LastError
	}
	return result
}

func (f *dynamicForwarder) overview() daemon.ForwardingOverview {
	f.mu.RLock()
	defer f.mu.RUnlock()
	result := daemon.ForwardingOverview{
		Enabled: true, Authorized: f.prepared, Ports: len(f.servers), LastPollAt: f.lastPoll,
		LastError: f.lastErr, InventoryError: f.inventoryErr,
		IntervalSeconds: int(durationOrDefault(f.interval, forwardingPollInterval) / time.Second),
	}
	for _, server := range f.servers {
		result.Listeners += len(server.listeners)
		if server.status != "serving" {
			result.DegradedPorts++
		}
	}
	for _, route := range f.routes {
		if route.lapsed {
			result.LapsedRoutes++
		}
	}
	return result
}
