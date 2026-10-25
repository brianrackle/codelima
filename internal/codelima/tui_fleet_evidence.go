package codelima

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.rockorager.dev/vaxis"
)

type tuiFleetEvidenceState struct {
	snapshot              fleetEvidenceSnapshot
	request               fleetEvidenceRequest
	serial                uint64
	loading, loaded       bool
	startedAt, receivedAt time.Time
	cancel                context.CancelFunc
	err                   error
}

type tuiFleetEvidenceEvent struct {
	serial   uint64
	request  fleetEvidenceRequest
	snapshot fleetEvidenceSnapshot
	err      error
}

func (a *vaxisTUIApp) fleetEvidenceVisible() bool {
	return a.view == tuiViewNodes && (!a.nodeDetail || a.detailTab == tuiDetailOverview) && a.fleet != nil
}

func (a *vaxisTUIApp) fleetEvidenceRequest() fleetEvidenceRequest {
	request := fleetEvidenceRequest{}
	if !a.fleetEvidenceVisible() {
		return request
	}
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			request.Nodes = append(request.Nodes, id)
		}
	}
	add(a.fleet.selectedID)
	if a.nodeDetail {
		request.RecentNode = a.fleet.selectedID
		return request
	}
	for _, row := range a.fleet.viewport(max(1, a.treeContentRect.height)) {
		add(row.NodeID)
	}
	if a.vx != nil {
		width, height := a.vx.Window().Size()
		if len(a.fleet.marks) == 0 && layoutTUIFleet(width, height, len(a.fleet.rows)).Preview.height > 0 {
			request.RecentNode = a.fleet.selectedID
		}
	}
	slices.Sort(request.Nodes)
	return request
}

func sameFleetEvidenceRequest(a, b fleetEvidenceRequest) bool {
	return a.RecentNode == b.RecentNode && slices.Equal(a.Nodes, b.Nodes)
}

func (a *vaxisTUIApp) cancelFleetEvidenceLoad() {
	s := &a.fleetEvidence
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.serial++
	s.loading = false
}

func (a *vaxisTUIApp) startFleetEvidenceLoad(force bool) {
	if !a.fleetEvidenceVisible() || a.daemonDisconnected {
		a.cancelFleetEvidenceLoad()
		return
	}
	if a.service == nil || a.service.daemonClient == nil {
		return
	}
	request := a.fleetEvidenceRequest()
	s := &a.fleetEvidence
	if sameFleetEvidenceRequest(s.request, request) {
		if s.loading || !force && time.Since(s.startedAt) < time.Second {
			return
		}
	}
	a.cancelFleetEvidenceLoad()
	if len(request.Nodes) == 0 {
		return
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	s.loading, s.cancel, s.request, s.startedAt = true, cancel, request, time.Now()
	serial, client := s.serial, a.service.daemonClient
	load := func() tuiFleetEvidenceEvent {
		defer cancel()
		e := tuiFleetEvidenceEvent{serial: serial, request: request, snapshot: fleetEvidenceSnapshot{Nodes: map[string]fleetNodeEvidence{}, RecentNode: request.RecentNode}}
		// Very tall windows use bounded batches under one deadline. The server
		// still enforces a per-request cap; ordinary windows need one call.
		for start := 0; start < len(request.Nodes); start += fleetEvidenceNodeLimit {
			batch := fleetEvidenceRequest{Nodes: request.Nodes[start:min(len(request.Nodes), start+fleetEvidenceNodeLimit)]}
			if slices.Contains(batch.Nodes, request.RecentNode) {
				batch.RecentNode = request.RecentNode
			}
			var snapshot fleetEvidenceSnapshot
			if err := client.Call(ctx, "node.fleet_evidence", batch, &snapshot); err != nil {
				e.err = fromDaemonError(err)
				return e
			}
			for id, node := range snapshot.Nodes {
				if slices.Contains(batch.Nodes, id) {
					e.snapshot.Nodes[id] = node
				}
			}
			if batch.RecentNode != "" && snapshot.RecentNode == batch.RecentNode {
				if snapshot.Preview != nil && snapshot.Preview.NodeID == batch.RecentNode {
					e.snapshot.Preview = snapshot.Preview
				}
				e.snapshot.Recent = snapshot.Recent
				e.snapshot.RecentError = snapshot.RecentError
			}
			if e.snapshot.ObservedAt.IsZero() || snapshot.ObservedAt.Before(e.snapshot.ObservedAt) {
				e.snapshot.ObservedAt = snapshot.ObservedAt
			}
			if snapshot.LifecycleError != "" {
				e.snapshot.LifecycleError = snapshot.LifecycleError
			}
		}
		return e
	}
	if a.postEvent == nil {
		a.applyFleetEvidence(load())
		return
	}
	go func() { a.postCompletion(load()) }()
}

func (a *vaxisTUIApp) applyFleetEvidence(e tuiFleetEvidenceEvent) {
	s := &a.fleetEvidence
	if e.serial != s.serial {
		return
	}
	s.loading, s.cancel = false, nil
	if !a.fleetEvidenceVisible() || !sameFleetEvidenceRequest(e.request, a.fleetEvidenceRequest()) {
		return
	}
	s.err = e.err
	if e.err == nil {
		s.snapshot, s.loaded, s.receivedAt = e.snapshot, true, time.Now()
	}
}

func (a *vaxisTUIApp) fleetEvidenceStale() bool {
	s := a.fleetEvidence
	return s.err != nil || a.daemonDisconnected || !s.loaded || time.Since(s.receivedAt) > 5*time.Second
}

func (a *vaxisTUIApp) fleetReceiptFailed(node Node) bool {
	if a.fleetEvidenceStale() || !nodeStatusInFlight(node.Status) {
		return false
	}
	e, ok := a.fleetEvidence.snapshot.Nodes[node.ID]
	if !ok || e.Error != "" || e.Start == nil {
		return false
	}
	start := e.Start
	// Recovery deliberately records no invented finish time. Only classify
	// the same observed metadata revision, with no live lifecycle owner.
	if start.State != "failed" && start.State != "interrupted" || e.ClaimState != "unclaimed" || !e.NodeUpdatedAt.Equal(node.UpdatedAt) {
		return false
	}
	for _, running := range a.activity.page.Running {
		if running.NodeID == node.ID && running.ID != start.ID {
			return false
		}
	}
	return true
}

func (a *vaxisTUIApp) fleetEvidenceLine(record tuiFleetRecord) string {
	e, ok := a.fleetEvidence.snapshot.Nodes[record.Node.ID]
	if !ok {
		return ""
	}
	if !e.NodeUpdatedAt.IsZero() && !e.NodeUpdatedAt.Equal(record.Node.UpdatedAt) || e.ClaimState == "active" && e.Start != nil && (e.Start.State == "failed" || e.Start.State == "interrupted") {
		return ""
	}
	// Running Activity is fresher than a retained response from before the
	// next start. Never show the previous attempt's failed step during it.
	for _, running := range a.activity.page.Running {
		if running.NodeID == record.Node.ID && (e.Start == nil || e.Start.ID != running.ID) {
			return ""
		}
	}
	if e.Error != "" {
		if a.fleetPhase(record.Node.ID) != "" {
			return ""
		}
		return a.keyHints("evidence unavailable · {node.open} details")
	}
	text := ""
	switch {
	case e.Step != nil && (record.Status == "failed" || record.Status == "starting"):
		step := e.Step
		text = fmt.Sprintf("%s %d/%d", step.Kind, step.Index, step.Total)
		if e.Start != nil && e.Start.State == "interrupted" {
			text += " · interrupted"
		}
		if step.Source != "" {
			text += " · " + step.Source
		}
		if step.ExitCode != nil {
			text += fmt.Sprintf(" · exit %d", *step.ExitCode)
		}
		if step.DurationMillis != nil {
			text += " · " + (time.Duration(*step.DurationMillis) * time.Millisecond).Round(time.Millisecond).String()
		}
	case e.Start != nil && record.Status == "failed" && (e.Start.State == "failed" || e.Start.State == "interrupted"):
		start := e.Start
		phase := start.Phase
		if start.Progress != nil && start.Progress.Phase != "" {
			phase = start.Progress.Phase
		}
		text = strings.TrimSpace(phase + " " + start.State)
		if start.FinishedAt != nil && !start.CreatedAt.IsZero() && !start.FinishedAt.Before(start.CreatedAt) {
			text += " · " + start.FinishedAt.Sub(start.CreatedAt).Round(time.Second).String()
		}
		if start.Error != "" {
			text += " · " + start.Error
		}
	case e.StoppedAt != nil && record.Status == "stopped":
		text = "last stop " + max(time.Duration(0), time.Since(*e.StoppedAt)).Truncate(time.Second).String() + " ago · s to start"
	}
	if text != "" && a.fleetEvidenceStale() {
		text = "last: " + text
	}
	return text
}

func (a *vaxisTUIApp) drawFleetRecent(win vaxis.Window, nodeID string, row, height int, theme tuiTheme) {
	s := a.fleetEvidence
	label := "Recent"
	if s.snapshot.RecentNode != nodeID || !s.loaded {
		tuiLine(win, row, label, theme.muted)
		tuiLine(win, row+1, "Recent events unavailable", theme.dim)
		return
	}
	if a.fleetEvidenceStale() {
		label += " · stale"
	} else if s.snapshot.RecentError != "" {
		label += " · incomplete"
	}
	tuiLine(win, row, label, theme.muted)
	if len(s.snapshot.Recent) == 0 {
		text := "No retained events"
		if s.snapshot.RecentError != "" {
			text = "History unavailable"
		}
		tuiLine(win, row+1, text, theme.dim)
	}
	for i, event := range s.snapshot.Recent[:min(3, len(s.snapshot.Recent))] {
		if row+1+i >= height {
			break
		}
		if event.NodeID != nodeID {
			continue
		}
		tuiLine(win, row+1+i, event.Time.Local().Format("15:04")+" "+event.Text, theme.muted)
	}
}
