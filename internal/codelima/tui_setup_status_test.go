package codelima

import (
	"slices"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func TestTUIFleetSeparatesVMStatusAndSetupAtEveryWidth(t *testing.T) {
	for _, width := range []int{80, 100, 120, 150} {
		a, _ := newTestManager(t, width, 38)
		node := a.state.nodes[0]
		node.Status, node.BootstrapCompleted = NodeStatusFailed, false
		node.LastRuntimeObservation = &RuntimeObservation{Exists: true, Status: ObservationRunning}
		if err := a.applyReloadedNodes([]Node{node}, ""); err != nil {
			t.Fatal(err)
		}
		a.draw()
		record, ok := a.fleetSelected()
		if !ok || record.Status != "running" || record.Setup != "failed" {
			t.Fatalf("width %d: record = %+v", width, record)
		}
		for _, field := range []tuiFleetSort{tuiFleetSortStatus, tuiFleetSortSetup} {
			if !slices.ContainsFunc(tuiFleetColumns(width-4, width < 100, true), func(c tuiFleetColumn) bool { return c.field == field }) {
				t.Fatalf("width %d: missing column %s", width, field)
			}
		}
		screen := renderedScreenText(t, a.vx, width, 38)
		for _, want := range []string{"STATUS", "SETUP", "running", "failed"} {
			if !strings.Contains(screen, want) {
				t.Fatalf("width %d: missing %q:\n%s", width, want, screen)
			}
		}
		if !strings.Contains(a.managerFooter(), "r retry") {
			t.Fatal("running VM lost failed-setup retry")
		}
		for _, runtime := range []ObservationStatus{ObservationStopped, ObservationRunning} {
			node.LastRuntimeObservation.Status = runtime
			if err := a.applyReloadedNodes([]Node{node}, ""); err != nil {
				t.Fatal(err)
			}
			a.draw()
			record, _ = a.fleetSelected()
			if record.Status != string(runtime) || record.Setup != "failed" {
				t.Fatalf("VM transition changed setup failure: %+v", record)
			}
		}
		a.nodeDetail, a.detailTab = true, tuiDetailOverview
		a.draw()
		screen = renderedScreenText(t, a.vx, width, 38)
		if !strings.Contains(screen, "setup") || !strings.Contains(screen, "failed") || !strings.Contains(screen, "running") {
			t.Fatalf("Overview missing independent states at %d:\n%s", width, screen)
		}
	}
}

func TestTUIFleetSetupFilterAndSort(t *testing.T) {
	records := []tuiFleetRecord{
		{Node: Node{ID: "a"}, Status: "running", Setup: "ready"},
		{Node: Node{ID: "b"}, Status: "running", Setup: "failed"},
		{Node: Node{ID: "c"}, Status: "stopped", Setup: "failed"},
	}
	fleet := newTUIFleet("/work")
	fleet.replace(records)
	fleet.setFilter("status:running setup:failed")
	if got := fleetIDs(fleet.visible); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("independent filters = %v", got)
	}
	fleet.setFilter("")
	fleet.sort = tuiFleetSortSetup
	fleet.rebuild()
	if got := fleetIDs(fleet.visible); !slices.Equal(got, []string{"b", "c", "a"}) {
		t.Fatalf("setup sort = %v", got)
	}
}

func TestTUIFleetSetupDoesNotPromoteBootstrappingGuestShell(t *testing.T) {
	a, sessions := newTestManager(t, 80, 24)
	node := a.state.nodes[0]
	node.Status, node.BootstrapCompleted = NodeStatusProvisioning, false
	node.LastRuntimeObservation = &RuntimeObservation{Exists: true, Status: ObservationRunning}
	if err := a.applyReloadedNodes([]Node{node}, ""); err != nil {
		t.Fatal(err)
	}
	record, _ := a.fleetSelected()
	if record.Status != "running" || record.Setup != "provisioning" {
		t.Fatalf("bootstrapping record = %+v", record)
	}
	if nodeGuestShellReady(record.Node) {
		t.Fatal("VM status bypassed the setup readiness gate")
	}
	a.handleKey(vaxis.Key{Keycode: 'x', Text: "x"})
	if len(sessions.opened) != 0 {
		t.Fatal("bootstrapping VM opened an unfinished guest shell")
	}
}
