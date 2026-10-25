package codelima

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/hostinfo"
	"go.rockorager.dev/vaxis"
)

func TestTUIFleetCompactHeaderRetainsCapacityAndNavigation(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	memory := uint64(16 << 30)
	a.host = tuiHostSummary{AllocationsKnown: true, AllocatedCPUs: 6, AllocatedMemoryBytes: 8 << 30,
		Capacity: hostinfo.Snapshot{CPUs: 4, MemoryBytes: &memory, ObservedAt: time.Now()}}
	for view := tuiViewNodes; view <= tuiViewSystem; view++ {
		a.view = view
		a.draw()
		line := strings.Split(renderedScreenText(t, a.vx, 80, 24), "\n")[0]
		for _, want := range []string{"6/4", "8/16G", "○"} {
			if !strings.Contains(line, want) {
				t.Fatalf("compact header lost %q: %s", want, line)
			}
		}
		if len(a.viewRegions) != 5 {
			t.Fatal("not all views have click targets")
		}
		for i, rect := range a.viewRegions {
			if rect.width < 3 || rect.col+rect.width > 55 || (i > 0 && rect.col < a.viewRegions[i-1].col+a.viewRegions[i-1].width) {
				t.Fatalf("clipped or overlapping view %d: %+v", i, rect)
			}
		}
	}
}

func TestTUIFleetWideResourcesUseObservedBytes(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	used, total := uint64(3<<29), uint64(4<<30)
	r := tuiFleetRecord{Node: Node{LastRuntimeObservation: &RuntimeObservation{MemoryUsedBytes: &used, MemoryTotalBytes: &total}}}
	wide, _ := a.fleetCell(r, tuiFleetSortMemory, false, 21, newTUITheme(true))
	compact, _ := a.fleetCell(r, tuiFleetSortMemory, true, 6, newTUITheme(true))
	if !strings.Contains(wide, "1.5/4G") || compact != "38%" {
		t.Fatalf("wrong observed values: wide=%q compact=%q", wide, compact)
	}
	r.Node.LastRuntimeObservation.MemoryTotalBytes = nil
	missing, _ := a.fleetCell(r, tuiFleetSortMemory, false, 21, newTUITheme(true))
	if missing != "—" {
		t.Fatalf("invented total: %q", missing)
	}
}

func TestTUIFleetMarkedSummaryIncludesHiddenTargetsAndGlobalAllocation(t *testing.T) {
	for _, size := range [][2]int{{150, 38}, {80, 24}} {
		a, _ := newTestManager(t, size[0], size[1])
		for i := range a.state.nodes {
			a.state.nodes[i].VCPUs = 2
			a.state.nodes[i].MemoryMiB = 4096
			a.state.nodes[i].LastRuntimeObservation = &RuntimeObservation{Exists: true, Status: ObservationRunning}
		}
		a.refreshFleet()
		outside := Node{ID: "outside", VCPUs: 6, MemoryMiB: 8192, LastRuntimeObservation: &RuntimeObservation{Exists: true, Status: ObservationRunning}}
		memory := uint64(32 << 30)
		a.host = summarizeTUIHost(hostinfo.Snapshot{CPUs: 16, MemoryBytes: &memory}, append(slices.Clone(a.state.nodes), outside))
		for _, node := range a.state.nodes {
			a.fleet.marks[node.ID] = true
		}
		a.fleet.setFilter(a.state.nodes[0].Slug)
		a.draw()
		screen := renderedScreenText(t, a.vx, size[0], size[1])
		for _, want := range []string{"2 marked", "1 hidden", "4 vCPU", "8 GiB"} {
			if !strings.Contains(screen, want) {
				t.Fatalf("missing %q at %v:\n%s", want, size, screen)
			}
		}
		if strings.Contains(screen, "preview ·") {
			t.Fatal("single preview hides batch impact")
		}
		if size[0] > 100 && (!strings.Contains(screen, "6/16 vCPU") || !strings.Contains(screen, "8/32 GiB")) {
			t.Fatalf("after-stop allocation ignored nodes outside scope:\n%s", screen)
		}
		// No selected row still leaves all marked targets actionable.
		a.fleet.setFilter("no-matches")
		a.draw()
		if !strings.Contains(renderedScreenText(t, a.vx, size[0], size[1]), "2 hidden") {
			t.Fatal("filter hid batch impact")
		}
	}
}

func TestTUIFleetRoutesJoinByIdentityAndRejectUnavailableOpens(t *testing.T) {
	a := portsUIFixture(t, 150, 38)
	a.view = tuiViewNodes
	a.ports.snapshot.LastPollAt = time.Now()
	a.ports.snapshot.Rows[0].GuestPort = 5173
	a.refreshFleet()
	a.fleet.setFilter("port:5173")
	if len(a.fleet.visible) != 1 || a.fleet.visible[0].Node.ID != a.state.nodes[0].ID {
		t.Fatal("discovered route missing from fleet filter")
	}
	a.selectFleetNode()
	var opened string
	a.openLink = func(s string) error { opened = s; return nil }
	a.handleKey(vaxis.Key{Keycode: 'o', Text: "o"})
	if opened != a.ports.snapshot.Rows[0].Address {
		t.Fatalf("opened %q", opened)
	}
	a.ports.snapshot.Rows[0].Kind = "tcp"
	opened = ""
	a.handleKey(vaxis.Key{Keycode: 'o', Text: "o"})
	if opened != "" {
		t.Fatal("TCP route opened in browser")
	}
	a.ports.snapshot.Rows[0].Kind = "http"
	for _, state := range []string{"retrying", "held", "stopped", "conflicted"} {
		opened = ""
		a.ports.snapshot.Rows[0].Status = state
		a.handleKey(vaxis.Key{Keycode: 'o', Text: "o"})
		if opened != "" {
			t.Fatalf("opened %s route: %s", state, opened)
		}
	}
	a.ports.snapshot.Rows[0].Status = "live"
	for _, failure := range []string{"refresh", "disconnect", "old", "inventory", "not-ready"} {
		opened = ""
		a.ports.err, a.daemonDisconnected = nil, false
		a.ports.snapshot.LastPollAt, a.ports.snapshot.InventoryError = time.Now(), ""
		a.ports.snapshot.Nodes[0].Ready = true
		switch failure {
		case "refresh":
			a.ports.err = errors.New("timeout")
		case "disconnect":
			a.daemonDisconnected = true
		case "old":
			a.ports.snapshot.LastPollAt = time.Now().Add(-time.Hour)
		case "inventory":
			a.ports.snapshot.InventoryError = "unavailable"
		case "not-ready":
			a.ports.snapshot.Nodes[0].Ready = false
		}
		a.handleKey(vaxis.Key{Keycode: 'o', Text: "o"})
		if opened != "" {
			t.Fatalf("opened with %s: %s", failure, opened)
		}
	}
}

func TestTUIFleetRouteUsesSelectedNodeWhenBareOwnerDiffers(t *testing.T) {
	a := portsUIFixture(t, 150, 38)
	a.view, a.ports.snapshot.LastPollAt = tuiViewNodes, time.Now()
	a.ports.snapshot.Rows[1].Status = "live"
	a.refreshFleet()
	a.fleet.selectID(a.state.nodes[1].ID)
	a.selectFleetNode()
	// Marking another node must not retarget this single-node browser action.
	a.fleet.marks[a.state.nodes[0].ID] = true
	var opened string
	a.openLink = func(s string) error { opened = s; return nil }
	a.handleKey(vaxis.Key{Keycode: 'o', Text: "o"})
	if opened != a.ports.snapshot.Rows[1].Address {
		t.Fatalf("opened owner or marked node instead: %q", opened)
	}
}

func TestTUIFleetSummarySurvivesFullCompactViewport(t *testing.T) {
	layout := layoutTUIFleet(80, 24, 200)
	if layout.Summary.row >= 21 || layout.Summary.row <= layout.Table.row+layout.Table.height {
		t.Fatalf("summary overlaps table, filter or status: %+v", layout)
	}
}

func TestTUIFleetColumnsFitEverySupportedWidth(t *testing.T) {
	for width := 80; width <= 180; width++ {
		for _, flat := range []bool{false, true} {
			columns := tuiFleetColumns(width-4, width < 100, flat)
			for _, column := range columns {
				if column.width < 2 || column.col+column.width > width-4 {
					t.Fatalf("column clipped at %d flat=%v: %+v", width, flat, column)
				}
			}
		}
	}
}

func TestTUIFleetMissingObservationsStayUnknown(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	a.fleet.marks[a.state.nodes[0].ID] = true
	if !strings.Contains(a.markedImpact().summary(), "allocation unavailable") {
		t.Fatal("invented global allocation")
	}
	a.host = summarizeTUIHost(hostinfo.Snapshot{}, a.state.nodes)
	if !strings.Contains(a.markedImpact().summary(), "known release") || !strings.Contains(a.host.compactAllocation(), "+?") {
		t.Fatal("unknown VM observation became zero allocation")
	}
	a.refreshFleet()
	record := a.fleet.records[0]
	if _, known := tuiFleetMetric(record, tuiFleetSortPorts); known {
		t.Fatal("unavailable port inventory became measured zero")
	}
	text, _ := a.fleetCell(record, tuiFleetSortPorts, false, 12, newTUITheme(true))
	if text != "?" {
		t.Fatalf("missing unavailable marker: %q", text)
	}
}

func TestTUIFleetRecordedPhaseAppearsAtBothSizes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		a, _ := newTestManager(t, size[0], size[1])
		a.activity.page.Running = []NodeOperation{{NodeID: a.state.nodes[0].ID, State: "running", Request: NodeOperationRequest{Kind: "start"},
			Progress: &NodeOperationProgress{Phase: "Boot VM", Completed: 2, Total: 6, Detail: "waiting for SSH"}}}
		a.draw()
		screen := renderedScreenText(t, a.vx, size[0], size[1])
		if !strings.Contains(screen, "Boot VM") || !strings.Contains(screen, "2/6 phases") {
			t.Fatalf("missing recorded phase at %v:\n%s", size, screen)
		}
	}
}

func TestTUIFleetRouteChoiceUsesPortOrder(t *testing.T) {
	a := portsUIFixture(t, 150, 38)
	a.view, a.ports.snapshot.LastPollAt = tuiViewNodes, time.Now()
	earlier := a.ports.snapshot.Rows[0]
	earlier.ID, earlier.Port, earlier.GuestPort, earlier.Address = "earlier", 2000, 2000, "http://alpha.localhost:2000"
	a.ports.snapshot.Rows = append(a.ports.snapshot.Rows, earlier)
	a.refreshFleet()
	a.fleet.selectID(earlier.NodeID)
	a.selectFleetNode()
	var opened string
	a.openLink = func(s string) error { opened = s; return nil }
	a.handleKey(vaxis.Key{Keycode: 'o', Text: "o"})
	if opened != earlier.Address {
		t.Fatalf("cache order changed first-port action: %q", opened)
	}
}
