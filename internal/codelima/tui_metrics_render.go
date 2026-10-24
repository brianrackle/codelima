package codelima

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

func metricPercent(value *float64) string {
	if value == nil {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", *value)
}

func tuiCPUSparkline(points []daemon.NodeMetricPoint, now time.Time, window time.Duration, width int) string {
	columns := tuiMetricColumns(points, now, window, width, metricCPU)
	var line strings.Builder
	for _, value := range columns {
		if value == nil {
			line.WriteByte(' ')
			continue
		}
		level := min(7, max(0, int(math.Round(*value/100*7))))
		line.WriteRune([]rune("▁▂▃▄▅▆▇█")[level])
	}
	return line.String()
}

func metricResource(used, total *uint64) string {
	percent, ok := tuiFleetUsagePercent(used, total)
	if !ok {
		return "—"
	}
	return fmt.Sprintf("%s / %s · %.0f%%", tuiHostBytes(used), tuiHostBytes(total), percent)
}

func metricDiskGrowth(points []daemon.NodeMetricPoint) string {
	var first, last *uint64
	for _, p := range points {
		if p.DiskUsedBytes != nil {
			if first == nil {
				first = p.DiskUsedBytes
			}
			last = p.DiskUsedBytes
		}
	}
	if first == nil || last == first {
		return "change —"
	}
	delta := float64(*last) - float64(*first)
	return fmt.Sprintf("%+.2f GiB observed", delta/(1<<30))
}

func (a *vaxisTUIApp) drawNodeMetrics(win vaxis.Window, theme tuiTheme) {
	width, height := win.Size()
	if height < 1 {
		return
	}
	m := &a.metrics
	now := time.Now()
	if m.paused {
		now = m.frozenAt
	}
	window := tuiMetricWindows[m.window]
	state := "live · 1s"
	if m.paused {
		state = "paused"
	}
	if m.latest != nil && now.Sub(m.latest.SampledAt) > 5*time.Second {
		state += " · stale"
	}
	tuiLine(win, 0, "window "+tuiMetricWindowLabels[m.window]+" · + / − · Space "+state, theme.mint)
	points := m.history.after(now, now.Add(-window).Add(-time.Nanosecond))
	latest := nodeUsageEvent{}
	if m.latest != nil {
		latest = *m.latest
	}
	if width >= 120 && height >= 22 {
		leftWidth := min(92, width*3/5)
		available := height - 2
		cpuHeight := max(9, available/2)
		memoryHeight := available - cpuHeight - 4
		a.drawMetricCPU(win.New(0, 1, leftWidth, cpuHeight), points, latest, now, window, theme)
		a.drawMetricMemory(win.New(0, 1+cpuHeight, leftWidth, memoryHeight), points, latest, now, window, theme)
		drawMetricDisk(win.New(0, 1+cpuHeight+memoryHeight, leftWidth, 4), points, latest, theme)
		a.drawMetricProcesses(win.New(leftWidth+1, 1, width-leftWidth-1, available), theme, true)
	} else {
		// At 80×24 the graphs become sparklines, leaving the process selection
		// and all controls reachable without hiding either resource history.
		tuiLine(win, 1, "CPU "+metricPercent(latest.CPUUsagePercent), theme.mint)
		drawTUIMetricChart(win.New(0, 2, width, 1), points, now, window, metricCPU, theme.mint)
		tuiLine(win, 3, "Memory "+metricResource(latest.MemoryUsedBytes, latest.MemoryTotalBytes), theme.blue)
		drawTUIMetricChart(win.New(0, 4, width, 1), points, now, window, metricMemory, theme.blue)
		if latest.Details != nil {
			tuiLine(win, 5, "cache "+tuiHostBytes(latest.Details.MemoryCachedBytes)+" · available "+tuiHostBytes(latest.Details.MemoryAvailableBytes)+" · swap "+tuiHostBytes(latest.Details.SwapUsedBytes), theme.dim)
		}
		tuiLine(win, 6, "Disk "+metricResource(latest.DiskUsedBytes, latest.DiskTotalBytes)+" · "+metricDiskGrowth(points), theme.text)
		a.drawMetricProcesses(win.New(0, 7, width, max(0, height-8)), theme, false)
	}
	message := "History 1h · resets on daemon restart · disk excludes host mounts"
	if m.err != nil {
		message = m.err.Error()
	}
	tuiLine(win, height-1, message, theme.dim)
}

func metricStatistics(points []daemon.NodeMetricPoint, value func(daemon.NodeMetricPoint) *float64) string {
	var sum, peak float64
	var peakAt time.Time
	count := 0
	for _, point := range points {
		if v := value(point); v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) {
			sum += *v
			count++
			if peakAt.IsZero() || *v > peak {
				peak = *v
				peakAt = point.SampledAt
			}
		}
	}
	if count == 0 {
		return "waiting for observations"
	}
	return fmt.Sprintf("avg %.0f%% · peak %.0f%% at %s", sum/float64(count), peak, peakAt.Local().Format("15:04"))
}

func drawMetricAxes(win vaxis.Window, points []daemon.NodeMetricPoint, now time.Time, window time.Duration, value func(daemon.NodeMetricPoint) *float64, style vaxis.Style, theme tuiTheme) {
	width, height := win.Size()
	if width < 8 || height < 2 {
		return
	}
	tuiLine(win, 0, "100", theme.dim)
	if height > 3 {
		tuiLine(win, (height-2)/2, " 50", theme.dim)
	}
	tuiLine(win, height-2, "  0", theme.dim)
	drawTUIMetricChart(win.New(5, 0, width-5, height-1), points, now, window, value, style)
	label := "−" + window.String() + "  " + now.Add(-window).Local().Format("15:04:05")
	tuiLine(win.New(5, height-1, width-5, 1), 0, label, theme.dim)
	tuiLine(win.New(width-3, height-1, 3, 1), 0, "now", theme.dim)
}

func (a *vaxisTUIApp) drawMetricCPU(win vaxis.Window, points []daemon.NodeMetricPoint, latest nodeUsageEvent, now time.Time, window time.Duration, theme tuiTheme) {
	inner := border.All(win, theme.border)
	width, height := inner.Size()
	cores := []daemon.CPUCoreUsage(nil)
	if latest.Details != nil {
		cores = latest.Details.CPUCores
	}
	coreCount := len(cores)
	if coreCount == 0 {
		coreCount = int(a.state.selectedEntry().node.VCPUs)
	}
	tuiLine(inner, 0, fmt.Sprintf("CPU %s · %d vCPU · %s", metricPercent(latest.CPUUsagePercent), coreCount, metricStatistics(points, metricCPU)), theme.mint)
	coreRows := min(3, (len(cores)+1)/2)
	drawMetricAxes(inner.New(0, 1, width, max(0, height-coreRows-1)), points, now, window, metricCPU, theme.mint, theme)
	for i, core := range cores {
		if i >= coreRows*2 {
			break
		}
		value := math.NaN()
		if core.UsagePercent != nil {
			value = *core.UsagePercent
		}
		text := fmt.Sprintf("cpu%d %s %s", core.Index, tuiGauge(value, 8), metricPercent(core.UsagePercent))
		if i == coreRows*2-1 && len(cores) > coreRows*2 {
			text += fmt.Sprintf(" · +%d cores", len(cores)-coreRows*2)
		}
		tuiLine(inner.New(i%2*(width/2), height-coreRows+i/2, width/2, 1), 0, text, theme.usage(value))
	}
}

func (a *vaxisTUIApp) drawMetricMemory(win vaxis.Window, points []daemon.NodeMetricPoint, latest nodeUsageEvent, now time.Time, window time.Duration, theme tuiTheme) {
	inner := border.All(win, theme.border)
	width, height := inner.Size()
	tuiLine(inner, 0, "Memory "+metricResource(latest.MemoryUsedBytes, latest.MemoryTotalBytes), theme.blue)
	drawMetricAxes(inner.New(0, 1, width, max(0, height-2)), points, now, window, metricMemory, theme.blue, theme)
	if d := latest.Details; d != nil {
		tuiLine(inner, height-1, "cache "+tuiHostBytes(d.MemoryCachedBytes)+" · available "+tuiHostBytes(d.MemoryAvailableBytes)+" · swap "+tuiHostBytes(d.SwapUsedBytes), theme.dim)
	}
}

func drawMetricDisk(win vaxis.Window, points []daemon.NodeMetricPoint, latest nodeUsageEvent, theme tuiTheme) {
	inner := border.All(win, theme.border)
	percent, ok := tuiFleetUsagePercent(latest.DiskUsedBytes, latest.DiskTotalBytes)
	if !ok {
		percent = math.NaN()
	}
	tuiLine(inner, 0, "Disk / "+tuiGauge(percent, 8)+" "+metricResource(latest.DiskUsedBytes, latest.DiskTotalBytes), theme.text)
	tuiLine(inner, 1, metricDiskGrowth(points)+" · guest root; host mounts excluded", theme.dim)
}

func (a *vaxisTUIApp) drawMetricProcesses(win vaxis.Window, theme tuiTheme, framed bool) {
	a.metrics.processRect = tuiRect{}
	if framed {
		win = border.All(win, theme.mint)
	}
	width, height := win.Size()
	if height < 1 {
		return
	}
	m := &a.metrics
	order := "CPU"
	if m.processSort == "memory" {
		order = "memory"
	}
	tuiLine(win, 0, "Processes · top by "+order+" ↓ · p sort", theme.text)
	if m.processError != "" {
		tuiLine(win, 1, m.processError, theme.amber)
		return
	}
	if m.processes == nil {
		tuiLine(win, 1, "Waiting for guest process sample…", theme.dim)
		return
	}
	if height < 3 {
		return
	}
	tuiLine(win, 1, fmt.Sprintf("%7s %-6s %6s %6s %s", "PID", "USER", "CPU%", "RSS", "COMMAND"), theme.dim)
	visible := max(0, height-3)
	if framed {
		visible = max(0, height-6)
	}
	col, row := win.Origin()
	m.processRect = tuiRect{col: col, row: row + 2, width: width, height: visible}
	selected := 0
	for i, row := range m.processes.Rows {
		if row.PID == m.processPID {
			selected = i
			break
		}
	}
	if selected < m.processOffset {
		m.processOffset = selected
	}
	if selected >= m.processOffset+visible {
		m.processOffset = max(0, selected-visible+1)
	}
	for i := m.processOffset; i < min(len(m.processes.Rows), m.processOffset+visible); i++ {
		row := m.processes.Rows[i]
		style := theme.text
		if row.PID == m.processPID {
			style = theme.selected
			tuiFill(win.New(0, i-m.processOffset+2, width, 1), style)
		}
		cpu := "—"
		if row.CPUPercent != nil {
			cpu = fmt.Sprintf("%.1f", *row.CPUPercent)
		}
		tuiLine(win, i-m.processOffset+2, fmt.Sprintf("%7d %-6.6s %6s %5.0fM %s", row.PID, row.User, cpu, float64(row.RSSBytes)/(1<<20), row.Command), style)
	}
	if framed {
		load := m.processes.Load
		count := fmt.Sprintf("%d", m.processes.Count)
		if m.processes.Count >= maxGuestProcesses && m.processes.Truncated {
			count += "+"
		}
		tuiLine(win, height-4, fmt.Sprintf("load %.2f %.2f %.2f · procs %s", load[0], load[1], load[2], count), theme.dim)
		tuiLine(win, height-3, "CPU% uses one core = 100%", theme.dim)
		tuiLine(win, height-2, "Sampled over SSH while this tab is open", theme.dim)
	}
	status := "↑↓ select · y copy PID · Space pause"
	if m.processes.Truncated {
		status = strings.TrimSpace(fmt.Sprintf("top %d · ", len(m.processes.Rows))) + " " + status
	}
	tuiLine(win, height-1, status, theme.dim)
}
