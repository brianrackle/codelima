package codelima

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"go.rockorager.dev/vaxis"
)

func TestTUIMetricsProcessSelectionSortAndCopy(t *testing.T) {
	app, _ := newTestManager(t, 150, 38)
	app.nodeDetail, app.detailTab = true, tuiDetailMetrics
	app.metrics.nodeID = app.state.selectedEntry().node.ID
	low, high := 10.0, 80.0
	app.metrics.processes = &daemon.NodeProcessSnapshot{Rows: []daemon.NodeProcess{{PID: 20, CPUPercent: &high, RSSBytes: 10, Command: "worker"}, {PID: 10, CPUPercent: &low, RSSBytes: 100, Command: "server"}}}
	app.ensureMetricProcessSelection()
	app.handleKey(vaxis.Key{Keycode: vaxis.KeyDown})
	if app.metrics.processPID != 10 {
		t.Fatal("arrow did not select next process")
	}
	app.handleKey(vaxis.Key{Keycode: 'p', Text: "p"})
	if app.metrics.processes.Rows[0].PID != 10 || app.metrics.processPID != 10 {
		t.Fatal("sort lost process identity")
	}
	copied := ""
	app.clipboardPush = func(value string) error { copied = value; return nil }
	app.handleKey(vaxis.Key{Keycode: 'y', Text: "y"})
	if copied != "10" {
		t.Fatalf("copied %q instead of selected PID", copied)
	}
	app.draw()
	for _, want := range []string{"Processes", "memory", "worker", "server"} {
		if !strings.Contains(renderedScreenText(t, app.vx, 150, 38), want) {
			t.Fatalf("process view missing %s", want)
		}
	}
}

func TestTUIMetricsNavigationAndPauseCancelSampling(t *testing.T) {
	for _, key := range []vaxis.Key{{Keycode: vaxis.KeyEsc}, {Keycode: ' ', Text: " "}, {Keycode: '2', Text: "2"}} {
		app, _ := newTestManager(t, 80, 24)
		app.nodeDetail, app.detailTab = true, tuiDetailMetrics
		ctx, cancel := context.WithCancel(context.Background())
		app.metrics.cancel = cancel
		app.handleKey(key)
		if ctx.Err() == nil {
			t.Fatalf("%+v left sampling active", key)
		}
	}
}

func TestTUIMetricsPausedNodeDoesNotLeakIntoNewSelection(t *testing.T) {
	app, _ := newTestManager(t, 80, 24)
	app.nodeDetail, app.detailTab = true, tuiDetailMetrics
	app.metrics = tuiMetricsState{nodeID: "another-node", paused: true, processPID: 42, processes: &daemon.NodeProcessSnapshot{}}
	app.startMetricsLoad()
	if app.metrics.nodeID != app.state.selectedEntry().node.ID || app.metrics.paused || app.metrics.processPID != 0 || app.metrics.processes != nil {
		t.Fatal("previous node metrics leaked into new selection")
	}
}

func TestTUIMetricsMouseUsesVisibleProcessRows(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		app, _ := newTestManager(t, size[0], size[1])
		app.nodeDetail, app.detailTab = true, tuiDetailMetrics
		app.metrics.processes = &daemon.NodeProcessSnapshot{Rows: []daemon.NodeProcess{{PID: 10}, {PID: 20}, {PID: 30}}}
		app.ensureMetricProcessSelection()
		app.draw()
		rect := app.metrics.processRect
		app.handleMouse(vaxis.Mouse{Col: rect.col, Row: rect.row + 1, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
		if app.metrics.processPID != 20 {
			t.Fatalf("process click selected %d at %v", app.metrics.processPID, size)
		}
		app.handleMouse(vaxis.Mouse{Col: rect.col, Row: rect.row + 1, Button: vaxis.MouseWheelDown, EventType: vaxis.EventPress})
		if app.metrics.processPID != 30 {
			t.Fatal("process wheel did not move selection")
		}
	}
}

func TestTUIMetricsBucketsKeepMissingIntervals(t *testing.T) {
	now := time.Now().UTC()
	zero, half, full := 0.0, 50.0, 100.0
	points := []daemon.NodeMetricPoint{{SampledAt: now.Add(-55 * time.Second), CPUUsagePercent: &zero}, {SampledAt: now.Add(-35 * time.Second), CPUUsagePercent: &half}, {SampledAt: now.Add(-31 * time.Second), CPUUsagePercent: &full}, {SampledAt: now.Add(-5 * time.Second)}}
	columns := tuiMetricColumns(points, now, time.Minute, 6, metricCPU)
	if columns[0] == nil || *columns[0] != 0 || columns[1] != nil || columns[2] == nil || *columns[2] != 75 || columns[5] != nil {
		t.Fatalf("unexpected time buckets: %+v", columns)
	}
}

func TestTUIMetricsRejectsResponsesAfterNavigationAndPause(t *testing.T) {
	app, _ := newTestManager(t, 80, 24)
	app.nodeDetail = true
	app.detailTab = tuiDetailMetrics
	app.metrics.nodeID = app.state.selectedEntry().node.ID
	app.metrics.serial = 2
	app.managerGeneration = 3
	response := tuiMetricsDataEvent{serial: 2, generation: 3, data: daemon.NodeMetricsSnapshot{NodeID: app.metrics.nodeID, Epoch: "new", History: []daemon.NodeMetricPoint{{SampledAt: time.Now()}}}}
	app.metrics.paused = true
	app.applyMetricsData(response)
	if len(app.metrics.history.points) != 0 {
		t.Fatal("response advanced a paused graph")
	}
	app.metrics.paused = false
	app.managerGeneration++
	app.applyMetricsData(response)
	if len(app.metrics.history.points) != 0 {
		t.Fatal("response crossed navigation generation")
	}
	response.generation = app.managerGeneration
	app.applyMetricsData(response)
	if len(app.metrics.history.points) != 1 {
		t.Fatal("current response was not applied")
	}
	response.data.Epoch = "restart"
	response.data.History = nil
	app.applyMetricsData(response)
	if len(app.metrics.history.points) != 0 {
		t.Fatal("daemon restart retained the previous epoch's history")
	}
}

func TestTUIMetricsWindowAndPauseKeysAtBothSizes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		app, _ := newTestManager(t, size[0], size[1])
		app.nodeDetail = true
		app.detailTab = tuiDetailMetrics
		app.metrics.nodeID = app.state.selectedEntry().node.ID
		app.handleKey(vaxis.Key{Text: "+", Keycode: '+'})
		if app.metrics.window != 1 {
			t.Fatal("+ did not expand the window")
		}
		app.handleKey(vaxis.Key{Text: " ", Keycode: ' '})
		if !app.metrics.paused {
			t.Fatal("Space did not pause metrics")
		}
		if len(app.fleet.marks) != 0 {
			t.Fatal("Metrics Space marked a fleet row")
		}
		app.draw()
		screen := renderedScreenText(t, app.vx, size[0], size[1])
		for _, want := range []string{"5m", "CPU", "Memory", "Disk", "paused"} {
			if !strings.Contains(screen, want) {
				t.Fatalf("missing %q at %v:\n%s", want, size, screen)
			}
		}
	}
}
