package codelima

import (
	"fmt"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

const (
	tuiSystemDoctor = iota
	tuiSystemDaemon
	tuiSystemHost
	tuiSystemSettings
	tuiSystemPanelCount
)

func (a *vaxisTUIApp) drawSystem(win vaxis.Window, theme tuiTheme) {
	width, height := win.Size()
	tuiLine(win.New(2, 2, width-4, 1), 0, a.keyHints("System · {navigation.next_tab navigation.previous_tab} panel · {navigation.up navigation.down} scroll · {system.refresh} check · {system.repair} repair"), theme.mint)
	a.systemRects = [tuiSystemPanelCount]tuiRect{}
	a.doctor.controls = nil
	a.systemDaemon.controls = nil
	full := tuiRect{col: 1, row: 4, width: width - 2, height: height - 7}
	if width >= 120 && height >= 34 {
		left, top := (full.width-1)/2, (full.height-1)/2
		for panel := range tuiSystemPanelCount {
			r := full
			r.width, r.height = left, top
			if panel%2 == 1 {
				r.col += left + 1
				r.width = full.width - left - 1
			}
			if panel >= 2 {
				r.row += top + 1
				r.height = full.height - top - 1
			}
			a.systemRects[panel] = r
		}
	} else {
		a.systemRects[a.systemPanel] = full
	}
	for panel, rect := range a.systemRects {
		if rect.width == 0 {
			continue
		}
		window := win.New(rect.col, rect.row, rect.width, rect.height)
		if panel == tuiSystemSettings {
			a.drawManagerSettings(window, theme)
			continue
		}
		style := theme.border
		if a.systemPanel == panel {
			style = theme.mint
		}
		inner := border.All(window, style)
		switch panel {
		case tuiSystemDoctor:
			a.drawDoctor(inner, theme)
		case tuiSystemDaemon:
			a.drawSystemDaemon(inner, theme)
		case tuiSystemHost:
			a.drawSystemHost(inner, theme)
		}
	}
}

func (a *vaxisTUIApp) drawSystemHost(win vaxis.Window, theme tuiTheme) {
	host := a.host
	tuiLine(win, 0, "Host capacity · "+host.model(), theme.muted)
	if !host.AllocationsKnown {
		tuiLine(win, 2, "VM allocation unavailable", theme.amber)
		return
	}
	tuiLine(win, 2, "ALLOCATED TO VMs THAT ARE UP · all directories", theme.muted)
	cpuLine, cpuStyle := tuiGaugeLine{prefix: "vCPU    ", suffix: fmt.Sprintf("%d / %d", host.AllocatedCPUs, host.Capacity.CPUs)}, theme.text
	if host.Capacity.CPUs > 0 {
		cpuLine.percent, cpuLine.cells = 100*float64(host.AllocatedCPUs)/float64(host.Capacity.CPUs), 8
		cpuLine.suffix = "  " + cpuLine.suffix
	}
	if host.Capacity.CPUs > 0 && host.AllocatedCPUs > uint64(host.Capacity.CPUs) {
		cpuLine.suffix += fmt.Sprintf(" · overcommitted by %d", host.AllocatedCPUs-uint64(host.Capacity.CPUs))
		cpuStyle = theme.coral
	}
	cpuLine.draw(win, 3, cpuStyle, theme)
	memoryLine, memoryStyle := tuiGaugeLine{prefix: "memory  ", suffix: fmt.Sprintf("%s / %s", tuiHostBytes(&host.AllocatedMemoryBytes), tuiHostBytes(host.Capacity.MemoryBytes))}, theme.text
	if host.Capacity.MemoryBytes != nil && *host.Capacity.MemoryBytes > 0 {
		memoryLine.percent, memoryLine.cells = 100*float64(host.AllocatedMemoryBytes)/float64(*host.Capacity.MemoryBytes), 8
		memoryLine.suffix = "  " + memoryLine.suffix
	}
	if host.Capacity.MemoryBytes != nil && host.AllocatedMemoryBytes > *host.Capacity.MemoryBytes {
		memoryLine.suffix += " · overcommitted"
		memoryStyle = theme.coral
	}
	memoryLine.draw(win, 4, memoryStyle, theme)
	tuiLine(win, 5, "disk    "+tuiHostBytes(host.Capacity.DiskAvailableBytes)+" free on LIMA_HOME volume", theme.text)
	_, height := win.Size()
	lines := make([]string, 0, len(host.Nodes)+1)
	if host.UnknownNodes > 0 {
		lines = append(lines, countLabel(host.UnknownNodes, "node has no runtime observation", "nodes have no runtime observation"))
	}
	for _, node := range host.Nodes {
		status := nodeVMStatus(node) + " · setup " + nodeSetupStatus(node)
		memory := uint64(node.MemoryMiB) << 20
		lines = append(lines, fmt.Sprintf("%-18s %2d CPU  %s  %s", node.Slug, node.VCPUs, tuiHostBytes(&memory), status))
	}
	for i, line := range visibleSystemLines(lines, &a.systemScroll[tuiSystemHost], height-7) {
		tuiLine(win, 7+i, line, theme.text)
	}
}

func visibleSystemLines(lines []string, scroll *int, height int) []string {
	height = max(0, height)
	*scroll = max(0, min(*scroll, len(lines)-height))
	return lines[*scroll:min(len(lines), *scroll+height)]
}
