package codelima

import (
	"context"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"go.rockorager.dev/vaxis"
)

var tuiMetricWindows = [...]time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}
var tuiMetricWindowLabels = [...]string{"1m", "5m", "15m", "1h"}

type tuiMetricsState struct {
	nodeID, epoch      string
	history            nodeMetricsHistory
	latest             *nodeUsageEvent
	window             int
	paused             bool
	frozenAt           time.Time
	inFlight           bool
	deadline           time.Time
	serial, generation uint64
	err                error
	processes          *daemon.NodeProcessSnapshot
	processError       string
	processSort        string
	processPID         int
	processOffset      int
	processRect        tuiRect
	cancel             context.CancelFunc
}

type tuiMetricsTickEvent struct{}
type tuiMetricsDataEvent struct {
	serial, generation uint64
	data               daemon.NodeMetricsSnapshot
	err                error
}

func (a *vaxisTUIApp) metricsVisible() bool {
	return a.view == tuiViewNodes && a.nodeDetail && a.detailTab == tuiDetailMetrics && !a.nodeStarting()
}

func (a *vaxisTUIApp) startMetricsLoad() {
	if !a.metricsVisible() {
		a.cancelMetricsLoad()
		return
	}
	nodeID := a.state.selectedEntry().node.ID
	if nodeID == "" {
		return
	}
	m := &a.metrics
	if m.nodeID != nodeID {
		a.cancelMetricsLoad()
		*m = tuiMetricsState{nodeID: nodeID, window: m.window, serial: m.serial}
	}
	if m.paused || a.service == nil || a.service.daemonClient == nil {
		return
	}
	if m.inFlight && m.generation == a.managerGeneration && time.Now().Before(m.deadline) {
		return
	}
	a.cancelMetricsLoad()
	m.serial++
	m.generation = a.managerGeneration
	m.inFlight = true
	m.deadline = time.Now().Add(6 * time.Second)
	serial, generation := m.serial, m.generation
	params := daemon.NodeMetricsParams{Node: nodeID, Epoch: m.epoch, After: m.history.latestAt(), Processes: true, ProcessSort: m.processSort}
	client, ctx := a.service.daemonClient, a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	m.cancel = cancel
	load := func() tuiMetricsDataEvent {
		defer cancel()
		event := tuiMetricsDataEvent{serial: serial, generation: generation}
		event.err = client.Call(ctx, "node.metrics", params, &event.data)
		return event
	}
	if a.postEvent == nil {
		a.applyMetricsData(load())
		return
	}
	go func() { a.postCompletion(load()) }()
}

func (a *vaxisTUIApp) cancelMetricsLoad() {
	if a.metrics.cancel != nil {
		a.metrics.cancel()
		a.metrics.cancel = nil
	}
	a.metrics.inFlight = false
}

func (a *vaxisTUIApp) cancelHiddenMetricsLoad() {
	if !a.metricsVisible() {
		a.cancelMetricsLoad()
	}
}

func (a *vaxisTUIApp) applyMetricsData(event tuiMetricsDataEvent) {
	m := &a.metrics
	if event.serial != m.serial {
		return
	}
	m.inFlight = false
	if !a.metricsVisible() || m.paused || event.generation != a.managerGeneration {
		return
	}
	if event.err != nil {
		m.err = event.err
		return
	}
	if event.data.NodeID != m.nodeID {
		return
	}
	m.err = nil
	if event.data.Epoch != m.epoch {
		m.history = nodeMetricsHistory{}
		m.epoch = event.data.Epoch
	}
	for _, point := range event.data.History {
		m.history.appendPoint(point)
	}
	m.latest = event.data.Latest
	m.processes, m.processError = event.data.Processes, event.data.ProcessError
	a.ensureMetricProcessSelection()
}

func (a *vaxisTUIApp) handleMetricsKey(key vaxis.Key) bool {
	if !a.metricsVisible() {
		return false
	}
	switch {
	case key.MatchString("+"), key.MatchString("-"):
		delta := 1
		if key.MatchString("-") {
			delta = -1
		}
		a.metrics.window = max(0, min(len(tuiMetricWindows)-1, a.metrics.window+delta))
		return true
	case key.Matches(' '):
		a.metrics.paused = !a.metrics.paused
		if a.metrics.paused {
			a.cancelMetricsLoad()
			a.metrics.frozenAt = time.Now()
		} else {
			a.startMetricsLoad()
		}
		return true
	case key.MatchString("p"):
		if a.metrics.processSort == "memory" {
			a.metrics.processSort = "cpu"
		} else {
			a.metrics.processSort = "memory"
		}
		if a.metrics.processes != nil {
			sorted := sortedProcessSnapshot(*a.metrics.processes, a.metrics.processSort)
			a.metrics.processes = &sorted
			a.ensureMetricProcessSelection()
		}
		a.cancelMetricsLoad()
		a.startMetricsLoad()
		return true
	case key.MatchString("y"):
		if a.metrics.processPID > 0 {
			if err := a.copyToHostClipboard(strconv.Itoa(a.metrics.processPID)); err != nil {
				a.setStatus(slog.LevelError, err.Error())
			} else {
				a.setStatus(slog.LevelInfo, "Process PID sent to host clipboard")
			}
		}
		return true
	}
	return false
}

func (a *vaxisTUIApp) ensureMetricProcessSelection() {
	m := &a.metrics
	if m.processes == nil || len(m.processes.Rows) == 0 {
		m.processPID = 0
		return
	}
	for _, row := range m.processes.Rows {
		if row.PID == m.processPID {
			return
		}
	}
	m.processPID = m.processes.Rows[0].PID
	m.processOffset = 0
}

func (a *vaxisTUIApp) moveMetricProcess(delta int) {
	m := &a.metrics
	a.ensureMetricProcessSelection()
	if m.processes == nil {
		return
	}
	for i, row := range m.processes.Rows {
		if row.PID == m.processPID {
			m.processPID = m.processes.Rows[max(0, min(len(m.processes.Rows)-1, i+delta))].PID
			return
		}
	}
}

func metricCPU(point daemon.NodeMetricPoint) *float64 { return point.CPUUsagePercent }
func metricMemory(point daemon.NodeMetricPoint) *float64 {
	if point.MemoryUsedBytes == nil || point.MemoryTotalBytes == nil || *point.MemoryTotalBytes == 0 || *point.MemoryUsedBytes > *point.MemoryTotalBytes {
		return nil
	}
	percent := 100 * float64(*point.MemoryUsedBytes) / float64(*point.MemoryTotalBytes)
	return &percent
}

// Each column averages observed samples in its real time interval. Empty
// intervals remain empty, including a daemon restart or disconnected guest.
func tuiMetricColumns(points []daemon.NodeMetricPoint, now time.Time, window time.Duration, width int, value func(daemon.NodeMetricPoint) *float64) []*float64 {
	columns := make([]*float64, max(0, width))
	sums := make([]float64, len(columns))
	counts := make([]int, len(columns))
	if width < 1 || window <= 0 {
		return columns
	}
	start := now.Add(-window)
	for _, point := range points {
		v := value(point)
		if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 100 || point.SampledAt.Before(start) || point.SampledAt.After(now) {
			continue
		}
		column := min(width-1, int(float64(point.SampledAt.Sub(start))/float64(window)*float64(width)))
		sums[column] += *v
		counts[column]++
	}
	for i, count := range counts {
		if count > 0 {
			v := sums[i] / float64(count)
			columns[i] = &v
		}
	}
	return columns
}

func drawTUIMetricChart(win vaxis.Window, points []daemon.NodeMetricPoint, now time.Time, window time.Duration, value func(daemon.NodeMetricPoint) *float64, style vaxis.Style) {
	width, height := win.Size()
	if width < 1 || height < 1 {
		return
	}
	columns := tuiMetricColumns(points, now, window, width, value)
	for col, v := range columns {
		if v == nil {
			continue
		}
		levels := max(1, int(math.Round(*v/100*float64(height*8))))
		for row := height - 1; row >= 0 && levels > 0; row-- {
			part := min(8, levels)
			levels -= part
			tuiLine(win.New(col, row, 1, 1), 0, string([]rune(" ▁▂▃▄▅▆▇█")[part]), style)
		}
	}
}
