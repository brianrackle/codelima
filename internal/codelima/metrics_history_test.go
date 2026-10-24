package codelima

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func TestNodeMetricsRPCResolvesIdentityAndValidatesRequests(t *testing.T) {
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "metric-node")
	host := newDaemonHost(service)
	now := time.Now().UTC()
	host.forwarder = &dynamicForwarder{now: func() time.Time { return now }}
	host.forwarder.recordNodeUsage(node.ID, forwardingGuestObservation{Memory: guestResourceUsage{UsedBytes: 1, TotalBytes: 2}}, now)
	raw, _ := json.Marshal(daemon.NodeMetricsParams{Node: node.Slug})
	result, err := host.Handle(context.Background(), daemon.ClientContext{}, "node.metrics", raw)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := result.(daemon.NodeMetricsSnapshot)
	if !ok || snapshot.NodeID != node.ID || len(snapshot.History) != 1 {
		t.Fatalf("unexpected response: %+v", result)
	}
	for _, invalid := range []string{`{}`, `{"node":"missing"}`, `{"node":2}`, `{"node":"metric-node","after":"bad-time"}`} {
		if _, err := host.Handle(context.Background(), daemon.ClientContext{}, "node.metrics", json.RawMessage(invalid)); err == nil {
			t.Errorf("accepted %s", invalid)
		}
	}
}

func TestNodeMetricsHistoryBoundsRetentionAndKeepsGaps(t *testing.T) {
	base := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	var history nodeMetricsHistory
	for i := 0; i < 4000; i++ {
		cpu := float64(i % 100)
		history.append(daemon.NodeUsageEvent{NodeID: "node", SampledAt: base.Add(time.Duration(i) * time.Second), CPUUsagePercent: &cpu})
	}
	points := history.after(base.Add(3999*time.Second), time.Time{})
	if len(points) != maxNodeMetricSamples || !points[len(points)-1].SampledAt.Equal(base.Add(3999*time.Second)) {
		t.Fatalf("retention len=%d", len(points))
	}
	history.append(daemon.NodeUsageEvent{NodeID: "node", SampledAt: base.Add(4000 * time.Second)})
	points = history.after(base.Add(4000*time.Second), base.Add(3999*time.Second))
	if len(points) != 1 || points[0].CPUUsagePercent != nil {
		t.Fatalf("missing sample should remain a gap: %+v", points)
	}
	if got := history.after(base.Add(8000*time.Second), time.Time{}); len(got) != 0 {
		t.Fatal("expired samples remained visible")
	}
}

func TestNodeMetricsHistoryIgnoresOldSamplesAndOwnsValues(t *testing.T) {
	now := time.Now().UTC()
	cpu := 25.0
	var history nodeMetricsHistory
	history.append(daemon.NodeUsageEvent{SampledAt: now, CPUUsagePercent: &cpu})
	cpu = 99
	history.append(daemon.NodeUsageEvent{SampledAt: now.Add(-time.Second), CPUUsagePercent: &cpu})
	points := history.after(now, time.Time{})
	if len(points) != 1 || *points[0].CPUUsagePercent != 25 {
		t.Fatalf("history aliased or accepted stale data: %+v", points)
	}
	*points[0].CPUUsagePercent = 77
	if *history.after(now, time.Time{})[0].CPUUsagePercent != 25 {
		t.Fatal("reader mutated stored history")
	}
	history.append(daemon.NodeUsageEvent{SampledAt: now, CPUUsagePercent: &cpu})
	if got := history.after(now, time.Time{}); len(got) != 1 || *got[0].CPUUsagePercent != 99 {
		t.Fatal("same timestamp did not replace sample")
	}
}

func TestNodeMetricsHistoryFitsProtocolFrame(t *testing.T) {
	now := time.Now().UTC()
	maxBytes := ^uint64(0)
	cpu := 99.99999999999999
	var history nodeMetricsHistory
	for i := 0; i < maxNodeMetricSamples; i++ {
		history.append(daemon.NodeUsageEvent{SampledAt: now.Add(time.Duration(i) * time.Second), CPUUsagePercent: &cpu, MemoryUsedBytes: &maxBytes, MemoryTotalBytes: &maxBytes, DiskUsedBytes: &maxBytes, DiskTotalBytes: &maxBytes})
	}
	encoded, err := json.Marshal(history.after(now.Add(time.Hour), time.Time{}))
	if err != nil || len(encoded) > daemon.MaxMessageSize-65536 {
		t.Fatalf("history payload %d exceeds frame allowance: %v", len(encoded), err)
	}
	processes := daemon.NodeProcessSnapshot{}
	for i := range maxProcessRows {
		processes.Rows = append(processes.Rows, daemon.NodeProcess{PID: i + 1, User: strings.Repeat("&", 32), Command: strings.Repeat("&", 160), CPUPercent: &cpu, RSSBytes: maxBytes})
	}
	details := daemon.NodeUsageDetails{}
	for i := range 256 {
		details.CPUCores = append(details.CPUCores, daemon.CPUCoreUsage{Index: i, UsagePercent: &cpu})
	}
	snapshot := daemon.NodeMetricsSnapshot{NodeID: "node", Epoch: "epoch", History: history.after(now.Add(time.Hour), time.Time{}), Latest: &daemon.NodeUsageEvent{Details: &details}, Processes: &processes}
	encoded, err = json.Marshal(snapshot)
	if err != nil || len(encoded) > daemon.MaxMessageSize-1024 {
		t.Fatalf("full metrics payload %d exceeds frame allowance: %v", len(encoded), err)
	}
}

func TestForwarderMetricsRetainsHistoryAfterCurrentSampleClears(t *testing.T) {
	now := time.Now().UTC()
	f := &dynamicForwarder{now: func() time.Time { return now }}
	f.recordNodeUsage("node", forwardingGuestObservation{CPU: guestCPUCounters{Total: 100, Idle: 25}, Memory: guestResourceUsage{UsedBytes: 1, TotalBytes: 2}}, now)
	now = now.Add(time.Second)
	f.recordNodeUsage("node", forwardingGuestObservation{CPU: guestCPUCounters{Total: 200, Idle: 75}, Memory: guestResourceUsage{UsedBytes: 1, TotalBytes: 2}}, now)
	first := f.metricsSnapshot("node", "", time.Time{})
	if first.Epoch == "" || len(first.History) != 2 || first.History[1].CPUUsagePercent == nil || *first.History[1].CPUUsagePercent != 50 {
		t.Fatalf("missing daemon history: %+v", first)
	}
	now = now.Add(time.Second)
	f.clearNodeUsage("node")
	cleared := f.metricsSnapshot("node", first.Epoch, first.History[1].SampledAt)
	if len(cleared.History) != 1 || cleared.History[0].MemoryUsedBytes != nil {
		t.Fatalf("clearing must append a gap: %+v", cleared)
	}
	reset := f.metricsSnapshot("node", "old-epoch", now)
	if len(reset.History) != 3 {
		t.Fatal("an old daemon epoch suppressed the new history")
	}
}
