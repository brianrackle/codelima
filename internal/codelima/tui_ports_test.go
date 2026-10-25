package codelima

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"go.rockorager.dev/vaxis"
)

func portsUIFixture(t *testing.T, width, height int) *vaxisTUIApp {
	t.Helper()
	a, _ := newTestManager(t, width, height)
	a.view, a.ports.loaded = tuiViewPorts, true
	a.ports.snapshot = daemon.PortsSnapshot{Enabled: true, Authorized: true, IntervalSeconds: 1, Listeners: 2,
		Rows: []daemon.PortRoute{{ID: "alpha", NodeID: a.state.nodes[0].ID, NodeSlug: "alpha", Port: 3000, GuestPort: 3000, Kind: "http", Process: "node", Address: "http://alpha.localhost:3000", Status: "live", OwnerID: a.state.nodes[0].ID, OwnerSlug: "alpha", OwnerReason: "first active"},
			{ID: "beta", NodeID: a.state.nodes[1].ID, NodeSlug: "beta", Port: 3000, GuestPort: 3000, Kind: "http", Address: "http://beta.localhost:3000", Status: "retrying", OwnerID: a.state.nodes[0].ID, OwnerSlug: "alpha", OwnerReason: "first active"}},
		Nodes: []daemon.PortNode{{ID: a.state.nodes[0].ID, Slug: "alpha", Status: "running", Ready: true}, {ID: a.state.nodes[1].ID, Slug: "beta", Status: "running", Ready: true}}}
	a.reconcilePortSelection()
	return a
}

func TestTUIPortsResponsiveLayoutAndMouseSelection(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		a := portsUIFixture(t, size[0], size[1])
		a.draw()
		text := renderedScreenText(t, a.vx, size[0], size[1])
		for _, want := range []string{"PORT", "NODE", "PROCESS", "STATUS", "alpha", "beta", "502", "[o Open]", "[p Pin]", "[Enter Node]"} {
			if !strings.Contains(text, want) {
				t.Errorf("missing %q at %v:\n%s", want, size, text)
			}
		}
		if size[0] >= 120 && !strings.Contains(text, "BARE localhost:PORT") {
			t.Fatal("wide table omitted ownership column")
		}
		row := a.ports.mouseRows[1]
		a.handlePortsMouse(vaxis.Mouse{Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress, Col: row.rect.col + 1, Row: row.rect.row + row.rect.height - 1})
		if a.ports.selected != "beta" {
			t.Fatal("click on route continuation did not select it")
		}
	}
}

func TestTUIPortsFilterOwnsNavigationKeysAndPreservesIdentity(t *testing.T) {
	a := portsUIFixture(t, 80, 24)
	a.ports.selected = "beta"
	a.handleKey(vaxis.Key{Keycode: '/', Text: "/"})
	for _, r := range "3000" {
		a.handleKey(vaxis.Key{Keycode: r, Text: string(r)})
	}
	if a.view != tuiViewPorts || a.ports.query != "3000" || a.ports.selected != "beta" {
		t.Fatal("filter input changed view or identity")
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEsc})
	// Pasted key-shaped text belongs to the filter, including global numbers.
	a.handleKey(vaxis.Key{Keycode: '/', Text: "/"})
	a.handleKey(vaxis.Key{Keycode: 'p', Text: "p3", EventType: vaxis.EventPaste})
	_, _ = a.handleEvent(vaxis.PasteEndEvent{})
	if a.ports.query != "p3" || a.ports.pinning || a.view != tuiViewPorts {
		t.Fatal("paste escaped the Ports filter")
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEsc})
	if a.ports.input != nil || a.ports.query != "" {
		t.Fatal("Escape did not restore the previous filter")
	}
	old := a.ports.snapshot
	a.applyPortsData(tuiPortsEvent{generation: a.ports.generation - 1})
	if len(a.ports.snapshot.Rows) != len(old.Rows) {
		t.Fatal("stale response cleared routes")
	}
	a.applyPortsData(tuiPortsEvent{generation: a.ports.generation, err: errors.New("disconnected")})
	if a.ports.selected != "beta" || len(a.ports.snapshot.Rows) != 2 {
		t.Fatal("failed refresh discarded last observation")
	}
}

func TestTUIPortsActionsUseSelectedRouteAndDoNotOpenShell(t *testing.T) {
	a := portsUIFixture(t, 80, 24)
	var copied, opened string
	a.clipboardPush = func(text string) error { copied = text; return nil }
	a.openLink = func(text string) error { opened = text; return nil }
	a.handleKey(vaxis.Key{Keycode: 'y', Text: "y"})
	a.handleKey(vaxis.Key{Keycode: 'o', Text: "o"})
	if copied != a.ports.snapshot.Rows[0].Address || opened != copied {
		t.Fatalf("wrong route: %s %s", copied, opened)
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
	if a.view != tuiViewNodes || !a.nodeDetail || a.detailTab != tuiDetailOverview || a.state.selectedEntry().node.ID != a.ports.snapshot.Rows[0].NodeID {
		t.Fatal("route did not navigate to its exact node")
	}
}

func TestTUIPortsFullErrorsRemainReadableWithoutRoutes(t *testing.T) {
	a := portsUIFixture(t, 80, 24)
	a.ports.snapshot.Rows = nil
	a.ports.snapshot.PolicyError = "read routing preferences: unexpected EOF at the end of the saved file"
	a.ports.snapshot.InventoryError = "runtime listing failed: the configured Lima executable is unavailable"
	a.ports.err = errors.New("daemon request failed: disconnected before reply")
	joined := strings.Join(a.portDetailLines(true), "\n")
	for _, want := range []string{a.ports.snapshot.PolicyError, a.ports.snapshot.InventoryError, a.ports.err.Error(), "alpha"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing complete details %q: %s", want, joined)
		}
	}
}

func TestTUIPortsFailureReadinessCopyHonorsLegacyOwnership(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		for _, managed := range []bool{false, true} {
			a, _ := newTestManager(t, size[0], size[1])
			node := a.state.selectedEntry().node
			node.Status, node.BootstrapCompleted, node.DaemonPortForwarding = NodeStatusFailed, false, managed
			node.Ports = []string{"15432:5432"}
			node.LastRuntimeObservation = &RuntimeObservation{Exists: true, Status: ObservationRunning}
			if err := a.applyReloadedNodes([]Node{node}, ""); err != nil {
				t.Fatal(err)
			}
			a.nodeDetail = true
			a.draw()
			screen := renderedScreenText(t, a.vx, size[0], size[1])
			want, absent := "legacy ports", "ports held until ready"
			if managed {
				want, absent = absent, want
			}
			if !strings.Contains(screen, want) || strings.Contains(screen, absent) {
				t.Fatalf("ownership copy at %v managed=%v:\n%s", size, managed, screen)
			}
		}
	}
}

func TestTUIPortsWideNodeSummaryOverflowRemainsReadable(t *testing.T) {
	a := portsUIFixture(t, 150, 38)
	for port := 10001; port <= 10006; port++ {
		a.ports.snapshot.Rows = append(a.ports.snapshot.Rows, daemon.PortRoute{ID: fmt.Sprint(port), NodeID: a.ports.snapshot.Nodes[0].ID, NodeSlug: "alpha", Kind: "tcp", Port: port, Status: "stopped"})
	}
	a.draw()
	screen := renderedScreenText(t, a.vx, 150, 38)
	if !strings.Contains(screen, "10005 (stopped)") || !strings.Contains(screen, "006 (stopped)") {
		t.Fatal("wide By node overflow was not preserved in complete route details", screen)
	}
}
