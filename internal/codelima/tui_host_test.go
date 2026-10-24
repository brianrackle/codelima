package codelima

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/hostinfo"
	"go.rockorager.dev/vaxis"
)

func TestTUIHostAllocationIncludesFailedVMStillRunning(t *testing.T) {
	up := &RuntimeObservation{Exists: true, Status: ObservationRunning}
	nodes := []Node{
		{ID: "running", Slug: "running", VCPUs: 4, MemoryMiB: 8192, Status: NodeStatusRunning, LastRuntimeObservation: up},
		{ID: "failed", Slug: "failed", VCPUs: 6, MemoryMiB: 16384, Status: NodeStatusFailed, LastRuntimeObservation: up},
		{ID: "starting", Slug: "starting", VCPUs: 2, MemoryMiB: 4096, Status: NodeStatusProvisioning, LastRuntimeObservation: &RuntimeObservation{Exists: true, Status: ObservationInstalling}},
		{ID: "stopped", Slug: "stopped", VCPUs: 8, MemoryMiB: 32768, Status: NodeStatusStopped, LastRuntimeObservation: &RuntimeObservation{Exists: true, Status: ObservationStopped}},
		{ID: "unknown", Slug: "unknown", VCPUs: 8, MemoryMiB: 32768, Status: NodeStatusRunning},
	}
	host := summarizeTUIHost(hostinfo.Snapshot{CPUs: 8}, nodes)
	if host.AllocatedCPUs != 12 || host.AllocatedMemoryBytes != 28<<30 || len(host.Nodes) != 3 || host.UnknownNodes != 1 {
		t.Fatalf("allocation must use observed VM state, not lifecycle status: %+v", host)
	}
}

func TestTUIHostAllocationDoesNotShrinkWithDirectoryScope(t *testing.T) {
	service, workspace := newTestService(t)
	for _, slug := range []string{"inside", "outside"} {
		node := saveForwardingTestNode(t, service, slug)
		node.DirectoryPath = workspace
		if slug == "outside" {
			node.DirectoryPath = t.TempDir()
		}
		node.VCPUs, node.MemoryMiB = 2, 1024
		if err := service.store.SaveNode(node, BootstrapState{}); err != nil {
			t.Fatal(err)
		}
		service.sandbox.(*fakeSandbox).observations[node.SandboxName] = RuntimeObservation{Name: node.SandboxName, Exists: true, Status: ObservationRunning}
	}
	event := loadTUIRefresh(context.Background(), service, workspace)
	if event.Err != nil {
		t.Fatal(event.Err)
	}
	if len(event.Nodes) != 1 || event.Nodes[0].Slug != "inside" {
		t.Fatalf("scope=%+v", event.Nodes)
	}
	if event.Host == nil || event.Host.AllocatedCPUs != 4 || len(event.Host.Nodes) != 2 {
		t.Fatalf("scope hid capacity allocated elsewhere: %+v", event.Host)
	}
}

func TestTUIHostSummaryRespectsRefreshGeneration(t *testing.T) {
	app, _ := newTestManager(t, 150, 38)
	app.refreshGeneration = 4
	app.host = tuiHostSummary{Capacity: hostinfo.Snapshot{Model: "current"}}
	app.finishDataRefresh(tuiRefreshCompleteEvent{Generation: 3, Host: &tuiHostSummary{Capacity: hostinfo.Snapshot{Model: "stale"}}})
	if app.host.Capacity.Model != "current" {
		t.Fatal("stale refresh replaced host capacity")
	}
}

func TestTUIHostSettingsMouseUsesScrolledRows(t *testing.T) {
	app, _ := newTestManager(t, 150, 34)
	app.view = tuiViewSystem
	app.systemPanel = tuiSystemSettings
	app.settingIndex = len(app.managerSettingRows()) - 1
	app.draw()
	rect := app.systemRects[tuiSystemSettings]
	start, _ := app.managerSettingsViewport(rect.height - 2)
	app.handleMouse(vaxis.Mouse{Col: rect.col + 5, Row: rect.row + 3, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
	if app.settingIndex != start {
		t.Fatalf("mouse selected %d, want first visible row %d", app.settingIndex, start)
	}
}

func TestTUIHostCapacityAndSystemPanelsRemainReachable(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 30}, {150, 38}} {
		app, _ := newTestManager(t, size[0], size[1])
		memory, free, disk := uint64(8<<30), uint64(64<<30), uint64(128<<30)
		app.host = tuiHostSummary{Capacity: hostinfo.Snapshot{Model: "Test CPU", CPUs: 4, MemoryBytes: &memory, DiskAvailableBytes: &free, DiskTotalBytes: &disk, ObservedAt: time.Now()}, AllocationsKnown: true, AllocatedCPUs: 6, AllocatedMemoryBytes: 4 << 30}
		app.draw()
		screen := renderedScreenText(t, app.vx, size[0], size[1])
		if size[0] >= 140 && (!strings.Contains(screen, "Test CPU") || !strings.Contains(screen, "6/4")) {
			t.Fatalf("missing real host summary:\n%s", screen)
		}
		app.view = tuiViewSystem
		for _, title := range []string{"Doctor", "Daemon", "Host capacity", "Settings"} {
			app.draw()
			screen = renderedScreenText(t, app.vx, size[0], size[1])
			if !strings.Contains(screen, title) {
				t.Fatalf("panel %s missing at %v:\n%s", title, size, screen)
			}
			if title == "Host capacity" && !strings.Contains(screen, "overcommitted by 2") {
				t.Fatalf("missing allocation warning:\n%s", screen)
			}
			app.handleKey(vaxis.Key{Keycode: vaxis.KeyTab})
		}
		if app.systemPanel != 0 {
			t.Fatal("Tab did not wrap panels")
		}
		app.handleKey(vaxis.Key{Keycode: vaxis.KeyTab, Modifiers: vaxis.ModShift})
		if app.systemPanel != tuiSystemSettings {
			t.Fatal("Shift-Tab did not reverse panels")
		}
	}
}
