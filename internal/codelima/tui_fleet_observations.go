package codelima

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

// The Nodes and Ports views consume the same asynchronous routing snapshot.
// Retained rows remain useful for filtering after a failed refresh, but cannot
// authorize a browser launch or masquerade as a complete port count.
func (a *vaxisTUIApp) fleetPortsCurrent() bool {
	s := a.ports
	age := time.Since(s.snapshot.LastPollAt)
	return s.loaded && s.err == nil && !a.daemonDisconnected && s.snapshot.InventoryError == "" &&
		!s.snapshot.LastPollAt.IsZero() && age >= 0 && age <= max(10*time.Second, 3*time.Duration(s.snapshot.IntervalSeconds)*time.Second)
}

func (a *vaxisTUIApp) fleetRoutes(nodeID string) []daemon.PortRoute {
	var routes []daemon.PortRoute
	for _, route := range a.ports.snapshot.Rows {
		if route.NodeID == nodeID {
			routes = append(routes, route)
		}
	}
	slices.SortFunc(routes, func(a, b daemon.PortRoute) int {
		if c := cmp.Compare(a.Port, b.Port); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return routes
}

func (a *vaxisTUIApp) openFleetPort() error {
	record, ok := a.fleetSelected()
	if !ok {
		return fmt.Errorf("select a node with a live HTTP route")
	}
	if !a.fleetPortsCurrent() {
		return fmt.Errorf("forwarding observation unavailable or stale; use 3 Ports to inspect")
	}
	for _, route := range a.fleetRoutes(record.Node.ID) {
		if a.fleetRouteOpenable(record, route) {
			return a.openHyperlink(route.Address)
		}
	}
	return fmt.Errorf("no live HTTP route for %s; use 3 Ports to inspect", record.Node.Slug)
}

func (a *vaxisTUIApp) fleetRouteOpenable(record tuiFleetRecord, route daemon.PortRoute) bool {
	snapshot := a.ports.snapshot
	ready := slices.ContainsFunc(snapshot.Nodes, func(node daemon.PortNode) bool { return node.ID == record.Node.ID && node.Ready })
	return a.fleetPortsCurrent() && snapshot.Enabled && snapshot.Authorized && ready && record.Status == "running" &&
		route.NodeID == record.Node.ID && route.Kind == "http" && route.Status == "live" &&
		(strings.HasPrefix(route.Address, "http://") || strings.HasPrefix(route.Address, "https://"))
}

func tuiGiB(value uint64) string {
	return strings.TrimSuffix(strconv.FormatFloat(float64(value)/(1<<30), 'f', 1, 64), ".0")
}

func (a *vaxisTUIApp) fleetPhase(nodeID string) string {
	for _, operation := range a.activity.page.Running {
		if operation.NodeID != nodeID || operation.Progress == nil {
			continue
		}
		progress := operation.Progress
		text := plainLogText(progress.Phase)
		if progress.Total > 0 {
			text += fmt.Sprintf(" %d/%d phases", progress.Completed, progress.Total)
		}
		if progress.Detail != "" {
			text += " · " + plainLogText(progress.Detail)
		}
		return strings.TrimSpace(text)
	}
	return ""
}

func (a *vaxisTUIApp) drawFleetRoutes(win vaxis.Window, nodeID string, row, height int, theme tuiTheme) {
	label := "Forwarded routes · o open"
	if !a.fleetPortsCurrent() {
		label = "Routes unavailable / stale"
	}
	tuiLine(win, row, label, theme.muted)
	routes := a.fleetRoutes(nodeID)
	if len(routes) == 0 {
		text := "No observed routes"
		if !a.fleetPortsCurrent() {
			text = "3 Ports to inspect"
		}
		tuiLine(win, row+1, text, theme.dim)
		return
	}
	for i, route := range routes {
		if row+1+i >= height {
			break
		}
		tuiLine(win, row+1+i, route.Address+" · "+route.Status, theme.blue)
	}
}

func tuiFleetBytes(record tuiFleetRecord, field tuiFleetSort) string {
	observation := record.Node.LastRuntimeObservation
	if observation == nil {
		return "—"
	}
	used, total := observation.MemoryUsedBytes, observation.MemoryTotalBytes
	if field == tuiFleetSortDisk {
		used, total = observation.DiskUsedBytes, observation.DiskTotalBytes
	}
	if used == nil || total == nil || *total == 0 {
		return "—"
	}
	return tuiGiB(*used) + "/" + tuiGiB(*total) + "G"
}

func (host tuiHostSummary) compactAllocation() string {
	if !host.AllocationsKnown {
		return "allocation —"
	}
	cpuTotal, memTotal := "—", "—"
	if host.Capacity.CPUs > 0 {
		cpuTotal = strconv.Itoa(host.Capacity.CPUs)
	}
	if host.Capacity.MemoryBytes != nil {
		memTotal = tuiGiB(*host.Capacity.MemoryBytes)
	}
	text := fmt.Sprintf("%d/%s vCPU %s/%sG", host.AllocatedCPUs, cpuTotal, tuiGiB(host.AllocatedMemoryBytes), memTotal)
	if host.UnknownNodes > 0 {
		text += " +?"
	}
	return text
}

type tuiMarkedImpact struct {
	targets                                                                     []tuiFleetRecord
	hidden, sessions                                                            int
	freed, remaining                                                            hostAllocations
	known                                                                       bool
	unknownTargets                                                              bool
	guestSessions, hostSessions, unclassifiedSessions, working, activityUnknown int
	guestWorking, guestActivityUnknown                                          int
}

func (a *vaxisTUIApp) markedImpact() tuiMarkedImpact {
	impact := tuiMarkedImpact{targets: a.fleet.targets(), known: a.host.AllocationsKnown}
	visible := make(map[string]bool, len(a.fleet.visible))
	for _, record := range a.fleet.visible {
		visible[record.Node.ID] = true
	}
	for _, record := range impact.targets {
		if !visible[record.Node.ID] {
			impact.hidden++
		}
		impact.sessions += record.Sessions
		for _, session := range a.fleetSessions(record.Node) {
			switch {
			case !session.kindKnown:
				impact.unclassifiedSessions++
			case session.kind == terminal.NodeShell:
				impact.guestSessions++
				if session.activity == "working" {
					impact.guestWorking++
				} else if session.activity == "unknown" {
					impact.guestActivityUnknown++
				}
			case session.kind == terminal.NodeHostShell:
				impact.hostSessions++
			default:
				impact.unclassifiedSessions++
			}
			if session.activity == "working" {
				impact.working++
			} else if session.activity == "unknown" {
				impact.activityUnknown++
			}
		}
		if summarizeHostAllocations([]Node{record.Node}).UnknownNodes > 0 {
			impact.unknownTargets = true
		}
	}
	// Intersect with the global allocation snapshot. Nodes outside this scope
	// still contribute to the projection after all marked nodes have stopped.
	var released, retained []Node
	for _, node := range a.host.Nodes {
		if a.fleet.marks[node.ID] {
			released = append(released, node)
		} else {
			retained = append(retained, node)
		}
	}
	impact.freed, impact.remaining = summarizeHostAllocations(released), summarizeHostAllocations(retained)
	impact.remaining.UnknownNodes = a.host.UnknownNodes
	return impact
}

func (impact tuiMarkedImpact) summary() string {
	text := fmt.Sprintf("%d marked · %d hidden · %d sessions", len(impact.targets), impact.hidden, impact.sessions)
	if impact.working > 0 {
		text += fmt.Sprintf(" · %d working", impact.working)
	}
	if !impact.known {
		return text + " · allocation unavailable"
	}
	release := "stop frees"
	if impact.working > 0 {
		release = "free"
	}
	if impact.unknownTargets {
		release = "known release"
	}
	return text + fmt.Sprintf(" · %s %d vCPU / %s GiB", release, impact.freed.AllocatedCPUs, tuiGiB(impact.freed.AllocatedMemoryBytes))
}

func (a *vaxisTUIApp) drawMarkedPreview(win vaxis.Window, theme tuiTheme) {
	impact := a.markedImpact()
	inner := border.All(win, theme.border)
	width, height := inner.Size()
	tuiLine(win.New(2, 0, width-2, 1), 0, fmt.Sprintf(" %d marked · %d hidden · actions apply to all ", len(impact.targets), impact.hidden), theme.mint)
	limit := max(0, height-6)
	for i, record := range impact.targets {
		if i >= limit {
			break
		}
		text := fmt.Sprintf("%-20s %-12s %2d vCPU  %6s memory  %d sessions", record.Node.Slug, record.Node.ConfigurationSlug, record.Node.VCPUs, formatTUIResourceSize(record.Node.MemoryMiB), record.Sessions)
		if !slices.ContainsFunc(a.fleet.visible, func(r tuiFleetRecord) bool { return r.Node.ID == record.Node.ID }) {
			text += " · hidden by filter"
		}
		text += a.fleetSessionSummary(record.Node)
		tuiLine(inner.New(1, i, width-2, 1), 0, text, theme.text)
	}
	row := min(len(impact.targets), limit)
	if len(impact.targets) > limit {
		tuiLine(inner, row, fmt.Sprintf(" … %d more marked", len(impact.targets)-limit), theme.muted)
		row++
	}
	tuiLine(inner.New(1, row+1, width-2, 1), 0, impact.summary(), theme.text)
	remaining := tuiHostSummary{Capacity: a.host.Capacity, AllocationsKnown: impact.known, AllocatedCPUs: impact.remaining.AllocatedCPUs, AllocatedMemoryBytes: impact.remaining.AllocatedMemoryBytes, UnknownNodes: impact.remaining.UnknownNodes}
	tuiLine(inner.New(1, row+2, width-2, 1), 0, "After stop: "+strings.ReplaceAll(remaining.compactAllocation(), "G", " GiB"), theme.muted)
	tuiLine(inner.New(1, row+3, width-2, 1), 0, impact.sessionWarning(), theme.amber)
	tuiLine(inner.New(1, row+4, width-2, 1), 0, "s start/stop · r restart · d delete · Space mark · * mark shown · Esc clear", theme.muted)
}
