package codelima

import (
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func TestTUIFleetPreviewUsesSelectedObservedHistoryAndRuntime(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	now := time.Now()
	node := &a.state.nodes[0]
	node.Status, node.UpdatedAt = NodeStatusRunning, now.Add(-time.Hour)
	node.WorkspaceMode, node.GuestWorkspacePath = "mounted", node.DirectoryPath
	node.AgentProfileName, node.SandboxName = "codex", "actual-instance"
	node.AuthImportCompleted, node.ImportHostAuth = true, true
	cpu1, cpu2, old := 20.0, 60.0, 99.0
	used, total := uint64(3<<30), uint64(8<<30)
	node.LastRuntimeObservation = &RuntimeObservation{Exists: true, Status: ObservationRunning, VMType: "qemu", CPUUsagePercent: &cpu2, MemoryUsedBytes: &used, MemoryTotalBytes: &total, ResourceUsageSampledAt: &now}
	nested, writable := false, true
	p := &fleetPreviewEvidence{NodeID: node.ID, NodeUpdatedAt: node.UpdatedAt, Runtime: &fleetRuntimeEvidence{VMType: "qemu", MountType: "9p", Nested: &nested, WorkspaceWritable: &writable, WorkspacePath: node.DirectoryPath}, History: []daemon.NodeMetricPoint{
		{SampledAt: now.Add(-2 * time.Minute), CPUUsagePercent: &old},
		{SampledAt: now.Add(-2 * time.Second), CPUUsagePercent: &cpu1, MemoryUsedBytes: &used, MemoryTotalBytes: &total},
		{SampledAt: now.Add(-time.Second), CPUUsagePercent: &cpu2, MemoryUsedBytes: &used, MemoryTotalBytes: &total},
	}}
	a.fleetEvidence = tuiFleetEvidenceState{loaded: true, receivedAt: now, snapshot: fleetEvidenceSnapshot{Preview: p}}
	a.refreshFleet()
	a.fleet.selectID(node.ID)
	a.selectFleetNode()
	a.draw()
	screen := renderedScreenText(t, a.vx, 150, 38)
	for _, want := range []string{"avg 40%", "peak 60%", "MEMORY 3/8 GiB", "available 5 GiB", "qemu · 9p · nested off", "same path · rw", "actual-instance", "auth imported"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("missing %q:\n%s", want, screen)
		}
	}
	p.NodeID = "other-node"
	a.draw()
	screen = renderedScreenText(t, a.vx, 150, 38)
	if strings.Contains(screen, "avg 40%") || strings.Contains(screen, "nested off") {
		t.Fatalf("other node's preview leaked:\n%s", screen)
	}
}

func TestTUIFleetPreviewKeepsUnknownConfigurationAndInvalidMemoryExplicit(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	node := a.state.nodes[0]
	node.LastRuntimeObservation = &RuntimeObservation{VMType: "vz"}
	p := &fleetPreviewEvidence{NodeID: node.ID, NodeUpdatedAt: node.UpdatedAt, Runtime: &fleetRuntimeEvidence{}}
	a.fleetEvidence = tuiFleetEvidenceState{loaded: true, receivedAt: time.Now(), snapshot: fleetEvidenceSnapshot{Preview: p}}
	if got := a.fleetRuntimeLabel(node); got != "vz · mount ? · nested ?" {
		t.Fatal(got)
	}
	used, total := uint64(9), uint64(8)
	node.LastRuntimeObservation.MemoryUsedBytes, node.LastRuntimeObservation.MemoryTotalBytes = &used, &total
	if got := fleetPreviewMemory(node); got != "MEMORY —" {
		t.Fatal(got)
	}
	node.UpdatedAt = time.Now()
	if a.fleetPreviewFor(node) != nil {
		t.Fatal("older metadata revision accepted")
	}
}

func TestTUIFleetPreviewChecksRequireCurrentSuccessfulEvidence(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	now := time.Now()
	node := &a.state.nodes[0]
	node.Status, node.BootstrapCompleted, node.AgentProfileName = NodeStatusRunning, true, "codex"
	node.UpdatedAt = now
	evidence := fleetNodeEvidence{NodeUpdatedAt: now, Start: &fleetLifecycleEvidence{State: "succeeded"}}
	a.fleetEvidence = tuiFleetEvidenceState{loaded: true, receivedAt: now, snapshot: fleetEvidenceSnapshot{Nodes: map[string]fleetNodeEvidence{node.ID: evidence}}}
	a.refreshFleet()
	a.fleet.selectID(node.ID)
	a.selectFleetNode()
	a.draw()
	if !strings.Contains(renderedScreenText(t, a.vx, 150, 38), "codex · checks passed") {
		t.Fatal("matching successful checks missing")
	}
	for _, invalid := range []string{"stale", "new revision", "failed"} {
		a.fleetEvidence.receivedAt = now
		evidence.NodeUpdatedAt, evidence.Start.State = now, "succeeded"
		switch invalid {
		case "stale":
			a.fleetEvidence.receivedAt = now.Add(-6 * time.Second)
		case "new revision":
			evidence.NodeUpdatedAt = now.Add(-time.Minute)
		case "failed":
			evidence.Start.State = "failed"
		}
		a.fleetEvidence.snapshot.Nodes[node.ID] = evidence
		a.draw()
		if strings.Contains(renderedScreenText(t, a.vx, 150, 38), "checks passed") {
			t.Fatalf("%s evidence claimed successful checks", invalid)
		}
	}
}
