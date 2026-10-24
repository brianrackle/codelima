package codelima

import (
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func TestNodeMetricsCPUTrendBoundsValuesAndPreservesGaps(t *testing.T) {
	base := time.Now().UTC()
	var history nodeMetricsHistory
	cpu, memory := 50.0, uint64(42)
	for i := range 80 {
		history.appendPoint(daemon.NodeMetricPoint{SampledAt: base.Add(time.Duration(i) * time.Second), CPUUsagePercent: &cpu, MemoryUsedBytes: &memory})
	}
	trend := history.recentCPU(base.Add(79 * time.Second))
	if len(trend) != 60 || trend[0].MemoryUsedBytes != nil || !trend[0].SampledAt.Equal(base.Add(20*time.Second)) {
		t.Fatal("trend bounds/shape drifted")
	}
	*trend[0].CPUUsagePercent = 99
	if *history.recentCPU(base.Add(79 * time.Second))[0].CPUUsagePercent != 50 {
		t.Fatal("trend aliased the daemon ring")
	}
	merged := mergeCPUTrend(trend, nil, base.Add(80*time.Second), nil)
	if len(merged) != 60 || merged[59].CPUUsagePercent != nil {
		t.Fatal("gap was not preserved")
	}
}

func TestForwarderMetricsInventoryTrendHasEpochAndSurvivesUnavailableCPU(t *testing.T) {
	now := time.Now().UTC()
	f := &dynamicForwarder{now: func() time.Time { return now }}
	f.recordNodeUsage("node", forwardingGuestObservation{CPU: guestCPUCounters{Total: 100, Idle: 50}}, now.Add(-time.Second))
	f.recordNodeUsage("node", forwardingGuestObservation{CPU: guestCPUCounters{Total: 200, Idle: 75}}, now)
	now = now.Add(time.Second)
	f.clearNodeUsage("node")
	nodes := []Node{{ID: "node", LastRuntimeObservation: &RuntimeObservation{Status: ObservationRunning}}}
	f.addNodeUsage(nodes)
	usage := nodeUsageFromNode(nodes[0])
	if usage.MetricsEpoch == "" || len(usage.CPUHistory) != 3 || usage.CPUHistory[1].CPUUsagePercent == nil || usage.CPUHistory[2].CPUUsagePercent != nil {
		t.Fatalf("missing unavailable-sample trend: %+v", usage)
	}
	*usage.CPUHistory[1].CPUUsagePercent = 12
	if *f.metricsHistory["node"].recentCPU(now)[1].CPUUsagePercent != 75 {
		t.Fatal("inventory reader mutated daemon history")
	}
}

func TestTUIMetricsCPUTrendSurvivesPushAndStaleRefresh(t *testing.T) {
	app, _ := newTestManager(t, 150, 38)
	id := app.state.selectedEntry().node.ID
	base := time.Now().UTC()
	cpu := 50.0
	for i := range 5 {
		app.applyNodeUsage(nodeUsageEvent{NodeID: id, MetricsEpoch: "one", SampledAt: base.Add(time.Duration(i) * time.Second), CPUUsagePercent: &cpu})
	}
	if len(app.nodeUsage[id].CPUHistory) != 5 {
		t.Fatal("pushes did not accumulate a trend")
	}
	nodes := append([]Node(nil), app.state.nodes...)
	for i := range nodes {
		if nodes[i].ID == id {
			at := base
			nodes[i].LastRuntimeObservation = &RuntimeObservation{MetricsEpoch: "one", CPUUsagePercent: &cpu, CPUUsageSampledAt: &at}
		}
	}
	if err := app.applyReloadedNodes(nodes, ""); err != nil {
		t.Fatal(err)
	}
	if len(app.state.nodesByID[id].LastRuntimeObservation.CPUHistory) != 5 {
		t.Fatal("stale inventory erased recent push history")
	}
	app.applyNodeUsage(nodeUsageEvent{NodeID: id, MetricsEpoch: "two", SampledAt: base.Add(6 * time.Second), CPUUsagePercent: &cpu})
	if len(app.nodeUsage[id].CPUHistory) != 1 {
		t.Fatal("new daemon epoch retained old samples")
	}
}

func TestTUIMetricsCPUTrendDrawsTimeInsteadOfGauge(t *testing.T) {
	now := time.Now().UTC()
	low, high := 0.0, 100.0
	points := []daemon.NodeMetricPoint{{SampledAt: now.Add(-7500 * time.Millisecond), CPUUsagePercent: &low}, {SampledAt: now.Add(-500 * time.Millisecond), CPUUsagePercent: &high}}
	if got := tuiCPUSparkline(points, now, 8*time.Second, 8); got != "▁      █" {
		t.Fatalf("sparkline=%q", got)
	}
	app, _ := newTestManager(t, 150, 38)
	entry := app.state.selectedEntry()
	app.applyNodeUsage(nodeUsageEvent{NodeID: entry.node.ID, SampledAt: now, CPUUsagePercent: &high, CPUHistory: points})
	app.draw()
	if !strings.Contains(renderedScreenText(t, app.vx, 150, 38), "CPU · last 60s") {
		t.Fatal("preview history missing")
	}
}
