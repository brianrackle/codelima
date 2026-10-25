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

func TestTUIFleetPreviewAgentChecksRequirePerAgentEvidence(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	now := time.Now()
	node := &a.state.nodes[0]
	node.Status, node.BootstrapCompleted, node.AgentProfileName = NodeStatusRunning, true, "codex-cli"
	node.Environments, node.UpdatedAt = []string{"codex", "claude-code"}, now
	evidence := fleetNodeEvidence{NodeUpdatedAt: now, Start: &fleetLifecycleEvidence{State: "succeeded"}}
	a.fleetEvidence = tuiFleetEvidenceState{loaded: true, receivedAt: now, snapshot: fleetEvidenceSnapshot{Nodes: map[string]fleetNodeEvidence{node.ID: evidence}}}
	a.refreshFleet()
	a.fleet.selectID(node.ID)
	a.selectFleetNode()
	a.draw()
	screen := renderedScreenText(t, a.vx, 150, 38)
	if strings.Contains(screen, "checks passed") || !strings.Contains(screen, "codex ?  claude-code ?") {
		t.Fatalf("start receipt claimed agent validation:\n%s", screen)
	}
	p := &fleetPreviewEvidence{NodeID: node.ID, NodeUpdatedAt: now, AgentChecks: []fleetAgentCheck{
		{Name: "codex", State: BootstrapStepSucceeded, LoginUser: true},
		{Name: "claude-code", State: BootstrapStepSucceeded, LoginUser: true},
	}}
	a.fleetEvidence.snapshot.Preview = p
	for _, detail := range []bool{false, true} {
		a.nodeDetail = detail
		a.draw()
		screen = renderedScreenText(t, a.vx, 150, 38)
		if !strings.Contains(screen, "codex ✓  claude-code ✓") || !strings.Contains(screen, "validated as login user") {
			t.Fatalf("detail=%v missing independent results:\n%s", detail, screen)
		}
	}
	for _, invalid := range []string{"other node", "new revision", "read failure"} {
		p.NodeID, p.NodeUpdatedAt, p.AgentError = node.ID, now, ""
		switch invalid {
		case "other node":
			p.NodeID = "different-node"
		case "new revision":
			p.NodeUpdatedAt = now.Add(-time.Minute)
		case "read failure":
			p.AgentError = "unreadable"
		}
		a.draw()
		screen = renderedScreenText(t, a.vx, 150, 38)
		if strings.Contains(screen, "codex ✓") || !strings.Contains(screen, "checks unavailable") {
			t.Fatalf("%s evidence claimed success:\n%s", invalid, screen)
		}
	}
	p.NodeID, p.NodeUpdatedAt, p.AgentError = node.ID, now, ""
	a.fleetEvidence.receivedAt = now.Add(-6 * time.Second)
	a.draw()
	if !strings.Contains(renderedScreenText(t, a.vx, 150, 38), "stale · last recorded") {
		t.Fatal("stale results looked current")
	}
}

func TestTUIFleetAgentCheckStatesRemainExplicit(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	now := time.Now()
	node := &a.state.nodes[0]
	node.Status, node.AgentProfileName = NodeStatusRunning, "codex-cli"
	node.Environments = []string{"codex", "claude-code"}
	p := &fleetPreviewEvidence{NodeID: node.ID, NodeUpdatedAt: node.UpdatedAt, AgentChecks: []fleetAgentCheck{
		{Name: "codex", State: BootstrapStepSucceeded, LoginUser: true},
		{Name: "claude-code", State: BootstrapStepSucceeded, LoginUser: true},
	}}
	a.fleetEvidence = tuiFleetEvidenceState{loaded: true, receivedAt: now, snapshot: fleetEvidenceSnapshot{Preview: p}}
	a.refreshFleet()
	a.fleet.selectID(node.ID)
	a.selectFleetNode()
	a.nodeDetail = true
	a.draw()
	if !strings.Contains(renderedScreenText(t, a.vx, 80, 24), "codex ✓  claude-code ✓") {
		t.Fatal("compact Overview lost agent results")
	}
	for _, tc := range []struct {
		state            BootstrapStepState
		inherited, login bool
		want, note       string
	}{
		{BootstrapStepSucceeded, true, true, "claude-code ↳✓", "↳ inherited"},
		{BootstrapStepSucceeded, false, false, "claude-code check ✓", "configured profile check"},
		{BootstrapStepFailed, false, true, "claude-code failed", "recorded checks"},
		{BootstrapStepPending, false, true, "claude-code pending", "recorded checks"},
		{BootstrapStepRunning, false, true, "claude-code checking", "recorded checks"},
		{BootstrapStepCanceled, false, true, "claude-code canceled", "recorded checks"},
		{BootstrapStepInterrupted, false, true, "claude-code interrupted", "recorded checks"},
		{BootstrapStepUnrecorded, false, true, "claude-code ?", "recorded checks"},
	} {
		p.AgentChecks[1].State, p.AgentChecks[1].Inherited, p.AgentChecks[1].LoginUser = tc.state, tc.inherited, tc.login
		segments, note := a.fleetAgentCheckSegments(*node, tuiTheme{})
		var text strings.Builder
		for _, segment := range segments {
			text.WriteString(segment.Text)
		}
		if !strings.Contains(text.String(), tc.want) || !strings.Contains(note, tc.note) || strings.Contains(note, "validated as login user") {
			t.Fatalf("incorrect check presentation: %q %q", text.String(), note)
		}
		a.draw()
		if screen := renderedScreenText(t, a.vx, 80, 24); !strings.Contains(screen, tc.want) {
			t.Fatalf("compact Overview hid %q:\n%s", tc.want, screen)
		}
	}
	node.Status = NodeStatusStopped
	for _, tc := range []struct {
		state BootstrapStepState
		note  string
	}{{BootstrapStepPending, "checks pending"}, {BootstrapStepUnrecorded, "checks not recorded"}} {
		for i := range p.AgentChecks {
			p.AgentChecks[i].State, p.AgentChecks[i].Inherited = tc.state, false
		}
		_, note := a.fleetAgentCheckSegments(*node, tuiTheme{})
		if note != tc.note {
			t.Fatalf("unexecuted checks appeared historical: %q", note)
		}
	}
	p.AgentChecks = []fleetAgentCheck{{Name: strings.Repeat("x", 48), State: BootstrapStepFailed}}
	a.draw()
	if screen := renderedScreenText(t, a.vx, 80, 24); !strings.Contains(screen, "failed") || !strings.Contains(screen, "configured profile check") {
		t.Fatalf("long profile hid its result:\n%s", screen)
	}
}
