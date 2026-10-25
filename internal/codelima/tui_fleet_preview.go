package codelima

import (
	"fmt"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"go.rockorager.dev/vaxis"
)

func (a *vaxisTUIApp) fleetPreviewFor(node Node) *fleetPreviewEvidence {
	p := a.fleetEvidence.snapshot.Preview
	if p == nil || p.NodeID != node.ID || !p.NodeUpdatedAt.Equal(node.UpdatedAt) {
		return nil
	}
	return p
}

func (a *vaxisTUIApp) fleetRuntimeLabel(node Node) string {
	vm, mount, nested := "vm ?", "mount ?", "nested ?"
	if p := a.fleetPreviewFor(node); p != nil && p.Runtime != nil {
		if p.Runtime.VMType != "" {
			vm = p.Runtime.VMType
		}
		if p.Runtime.MountType != "" {
			mount = p.Runtime.MountType
		}
		if p.Runtime.Nested != nil {
			nested = "nested off"
			if *p.Runtime.Nested {
				nested = "nested virt"
			}
		}
	}
	if observation := node.LastRuntimeObservation; observation != nil && observation.VMType != "" {
		vm = observation.VMType
	}
	label := vm + " · " + mount + " · " + nested
	if a.fleetEvidenceStale() {
		label = "last: " + label
	}
	return label
}

func (a *vaxisTUIApp) fleetWorkspaceLabel(node Node) string {
	label := nodeWorkspaceMode(node)
	if label != "mounted" {
		return label
	}
	path := coalesce(node.GuestWorkspacePath, node.WorkspaceMountPath, node.DirectoryPath)
	if path == node.DirectoryPath {
		label += " · same path"
	} else {
		label += " · " + path
	}
	mode := "access ?"
	if p := a.fleetPreviewFor(node); p != nil && p.Runtime != nil && p.Runtime.WorkspaceWritable != nil {
		mode = "ro"
		if *p.Runtime.WorkspaceWritable {
			mode = "rw"
		}
	}
	return label + " · " + mode
}

func (a *vaxisTUIApp) fleetAuthLabel(node Node) string {
	if !node.ImportHostAuth {
		return "not requested"
	}
	if !node.AuthImportCompleted {
		return "pending"
	}
	if p := a.fleetPreviewFor(node); p != nil && p.AuthImport != nil {
		r := p.AuthImport
		return "imported " + r.ImportedAt.Local().Format("Jan 2") + " · " + countLabel(r.ItemCount, "item", "items")
	}
	if node.ParentNodeID != "" {
		return "inherited · no receipt"
	}
	return "done · receipt unavailable"
}

func (a *vaxisTUIApp) drawNodeIdentity(win vaxis.Window, record tuiFleetRecord, theme tuiTheme) {
	node := record.Node
	statusCell, statusStyle := a.fleetCell(record, tuiFleetSortStatus, false, 30, theme)
	status := statusCell.String()
	if !record.StartedAt.IsZero() {
		uptime, _ := a.fleetCell(record, tuiFleetSortUptime, false, 12, theme)
		status += " · up " + uptime.String()
	}
	created, auth := "—", "auth not imported"
	if !node.CreatedAt.IsZero() {
		created = node.CreatedAt.Local().Format("Jan 2")
	}
	if node.ImportHostAuth {
		auth = "auth pending"
		if node.AuthImportCompleted {
			auth = "auth imported"
		}
	}
	agents, checkNote := a.fleetAgentCheckSegments(node, theme)
	fields := [][2]string{
		{"status", status}, {"template", fmt.Sprintf("%s · %d vCPU", node.ConfigurationSlug, node.VCPUs)},
		{"resources", fmt.Sprintf("%s mem · %s disk", formatTUIResourceSize(node.MemoryMiB), formatTUIResourceSize(node.DiskMiB))},
		{"image", node.Image}, {"directory", node.DirectoryPath}, {"workspace", a.fleetWorkspaceLabel(node)},
		{"runtime", a.fleetRuntimeLabel(node)}, {"agents", ""}, {"checks", checkNote}, {"created", created + " · " + auth}, {"lima", node.SandboxName},
	}
	width, height := win.Size()
	row := 0
	for i, field := range fields {
		if row >= height {
			break
		}
		tuiLine(win.New(0, row, 10, 1), 0, field[0], theme.dim)
		if field[0] == "agents" {
			row += a.drawFleetAgentChecks(win.New(10, row, max(0, width-10), height-row), agents)
			continue
		}
		style := theme.text
		if i == 0 {
			style = statusStyle
		} else if i == 1 {
			style = theme.violet
		}
		tuiLine(win.New(10, row, max(0, width-10), 1), 0, field[1], style)
		row++
	}
}

func fleetPreviewMemory(node Node) string {
	o := node.LastRuntimeObservation
	if o == nil || o.MemoryUsedBytes == nil || o.MemoryTotalBytes == nil || *o.MemoryTotalBytes == 0 || *o.MemoryUsedBytes > *o.MemoryTotalBytes {
		return "MEMORY —"
	}
	return fmt.Sprintf("MEMORY %s/%s GiB · %.0f%%", tuiGiB(*o.MemoryUsedBytes), tuiGiB(*o.MemoryTotalBytes), 100*float64(*o.MemoryUsedBytes)/float64(*o.MemoryTotalBytes))
}

func (a *vaxisTUIApp) drawFleetResources(win vaxis.Window, record tuiFleetRecord, theme tuiTheme) {
	width, height := win.Size()
	now := time.Now()
	var points []daemon.NodeMetricPoint
	p := a.fleetPreviewFor(record.Node)
	if p != nil {
		points = p.History
	} else if o := record.Node.LastRuntimeObservation; o != nil {
		points = o.CPUHistory
	}
	visible := make([]daemon.NodeMetricPoint, 0, len(points))
	for _, point := range points {
		if point.SampledAt.After(now.Add(-time.Minute)) && !point.SampledAt.After(now) {
			visible = append(visible, point)
		}
	}
	stats, _, _ := strings.Cut(metricStatistics(visible, metricCPU), " at ")
	if len(visible) == 0 {
		stats = "avg — · peak —"
	}
	cpu := "—"
	if value, ok := tuiFleetMetric(record, tuiFleetSortCPU); ok {
		cpu = fmt.Sprintf("%.0f%%", value)
	}
	label := "CPU " + cpu + " · " + stats
	stale := a.daemonDisconnected || p != nil && a.fleetEvidenceStale()
	if o := record.Node.LastRuntimeObservation; o == nil || o.Status != ObservationRunning {
		stale = len(visible) > 0 || stale
	}
	if o := record.Node.LastRuntimeObservation; o != nil && o.ResourceUsageSampledAt != nil && now.Sub(*o.ResourceUsageSampledAt) > 5*time.Second {
		stale = true
	}
	if stale {
		label = "CPU last " + cpu + " · " + stats
	}
	tuiLine(win, 0, label, theme.mint)
	cpuHeight := max(2, min(6, (height-3)/2))
	drawMetricAxes(win.New(0, 1, width, cpuHeight), visible, now, time.Minute, metricCPU, theme.mint, theme)
	row := cpuHeight + 1
	memory := fleetPreviewMemory(record.Node)
	if stale {
		memory = strings.Replace(memory, "MEMORY ", "MEMORY last ", 1)
	}
	tuiLine(win, row, memory, theme.blue)
	available := "available —"
	if o := record.Node.LastRuntimeObservation; o != nil && fleetPreviewMemory(record.Node) != "MEMORY —" {
		available = "available " + tuiGiB(*o.MemoryTotalBytes-*o.MemoryUsedBytes) + " GiB"
	}
	tuiLine(win, row+1, available, theme.dim)
	drawMetricAxes(win.New(0, row+2, width, max(0, min(5, height-row-2))), visible, now, time.Minute, metricMemory, theme.blue, theme)
}
