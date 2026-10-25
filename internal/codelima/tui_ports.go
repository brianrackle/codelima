package codelima

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/textinput"
)

type tuiPortsState struct {
	snapshot                                                  daemon.PortsSnapshot
	loaded, loading, pinning                                  bool
	err                                                       error
	generation, pinGeneration                                 uint64
	cancel, pinCancel                                         context.CancelFunc
	startedAt                                                 time.Time
	selected, query, beforeQuery, beforeSelected, pendingNode string
	input                                                     *textinput.Model
	scroll, detailScroll, detailHeight                        int
	mouseRows                                                 []tuiPortMouseRow
	controls                                                  []tuiPortControl
}
type tuiPortMouseRow struct {
	id   string
	rect tuiRect
}
type tuiPortControl struct {
	key  vaxis.Key
	rect tuiRect
}
type tuiPortsEvent struct {
	generation uint64
	snapshot   daemon.PortsSnapshot
	err        error
}
type tuiPortPinEvent struct {
	generation uint64
	node       string
	port       int
	err        error
}

func (a *vaxisTUIApp) portsVisible() bool {
	return a.view == tuiViewPorts || a.view == tuiViewNodes
}

func (a *vaxisTUIApp) startPortsLoad(force bool) {
	s := &a.ports
	if !a.portsVisible() {
		a.cancelPortsLoad()
		return
	}
	if a.service == nil || a.service.daemonClient == nil || a.daemonDisconnected || s.loading || s.pinning || (!force && time.Since(s.startedAt) < time.Second) {
		return
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	s.generation++
	s.cancel, s.loading, s.startedAt = cancel, true, time.Now()
	generation, client := s.generation, a.service.daemonClient
	load := func() tuiPortsEvent {
		defer cancel()
		e := tuiPortsEvent{generation: generation}
		e.err = client.Call(ctx, "ports.list", nil, &e.snapshot)
		return e
	}
	if a.postEvent == nil {
		a.applyPortsData(load())
		return
	}
	go func() { a.postCompletion(load()) }()
}

func (a *vaxisTUIApp) cancelPortsLoad() {
	s := &a.ports
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.generation++
	s.loading = false
}

func (a *vaxisTUIApp) cancelPortsWork() {
	a.cancelPortsLoad()
	a.ports.pinGeneration++
	if a.ports.pinCancel != nil {
		a.ports.pinCancel()
		a.ports.pinCancel = nil
	}
}

func (a *vaxisTUIApp) applyPortsData(e tuiPortsEvent) {
	s := &a.ports
	if e.generation != s.generation || !a.portsVisible() {
		return
	}
	s.loading, s.cancel, s.err = false, nil, e.err
	if e.err != nil {
		return
	}
	s.snapshot, s.loaded = e.snapshot, true
	a.reconcilePortSelection()
}

func (a *vaxisTUIApp) portRows() []daemon.PortRoute {
	words := strings.Fields(strings.ToLower(a.ports.query))
	rows := []daemon.PortRoute{}
	for _, row := range a.ports.snapshot.Rows {
		text := strings.ToLower(fmt.Sprintf("%d %d %s %s %s %s %s", row.Port, row.GuestPort, row.NodeSlug, row.Process, row.Address, row.Status, row.Kind))
		if slices.ContainsFunc(words, func(word string) bool { return !strings.Contains(text, word) }) {
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

func (a *vaxisTUIApp) portSelection() (daemon.PortRoute, bool) {
	for _, row := range a.portRows() {
		if row.ID == a.ports.selected {
			return row, true
		}
	}
	return daemon.PortRoute{}, false
}

func (a *vaxisTUIApp) reconcilePortSelection() {
	rows := a.portRows()
	if slices.ContainsFunc(rows, func(row daemon.PortRoute) bool { return row.ID == a.ports.selected }) {
		return
	}
	a.ports.selected, a.ports.detailScroll = "", 0
	if len(rows) != 0 {
		a.ports.selected = rows[0].ID
	}
}

func (a *vaxisTUIApp) movePortSelection(delta int) {
	rows := a.portRows()
	if len(rows) == 0 {
		return
	}
	index := slices.IndexFunc(rows, func(row daemon.PortRoute) bool { return row.ID == a.ports.selected })
	a.ports.selected = rows[max(0, min(len(rows)-1, index+delta))].ID
	a.ports.detailScroll = 0
}

func (a *vaxisTUIApp) updatePortsInput(event vaxis.Event) {
	s := &a.ports
	s.input.Update(event)
	s.query = plainLogText(s.input.String())
	s.scroll = 0
	a.reconcilePortSelection()
}

func (a *vaxisTUIApp) handlePortsSearchKey(key vaxis.Key) bool {
	s := &a.ports
	if a.view != tuiViewPorts || s.input == nil {
		return false
	}
	switch {
	case key.EventType == vaxis.EventPaste:
		a.updatePortsInput(key)
	case key.Matches(vaxis.KeyEsc) && tuiKeyActivates(key, true):
		s.query, s.input = s.beforeQuery, nil
		s.selected = s.beforeSelected
		a.reconcilePortSelection()
	case key.Matches(vaxis.KeyEnter) && tuiKeyActivates(key, true):
		s.input = nil
	default:
		a.updatePortsInput(key)
	}
	return true
}

func (a *vaxisTUIApp) handlePortsKey(key vaxis.Key) bool {
	if a.view != tuiViewPorts {
		return false
	}
	s := &a.ports
	row, selected := a.portSelection()
	switch {
	case key.MatchString("/"):
		s.beforeQuery, s.input = s.query, textinput.New()
		s.beforeSelected = s.selected
		s.input.SetContent(s.query)
	case key.MatchString("C"):
		a.startPortsLoad(true)
	case key.Matches(vaxis.KeyPgDown):
		s.detailScroll += max(1, s.detailHeight-1)
	case key.Matches(vaxis.KeyPgUp):
		s.detailScroll = max(0, s.detailScroll-max(1, s.detailHeight-1))
	case key.MatchString("p"):
		a.pinSelectedPort()
	case key.Matches(vaxis.KeyEnter):
		a.openPortNode()
	case key.MatchString("y"):
		if selected {
			if err := a.copyToHostClipboard(row.Address); err != nil {
				a.setStatus(slog.LevelError, err.Error())
			} else {
				a.setStatus(slog.LevelInfo, "Copied "+row.Address)
			}
		}
	case key.MatchString("o"):
		if selected && row.Kind == "http" && row.Status != "conflicted" {
			if err := a.openHyperlink(row.Address); err != nil {
				a.setStatus(slog.LevelError, err.Error())
			}
		} else {
			a.setStatus(slog.LevelWarn, "Select an available HTTP route to open in a browser")
		}
	default:
		return false
	}
	return true
}

func (a *vaxisTUIApp) pinSelectedPort() {
	row, ok := a.portSelection()
	if !ok || row.Kind != "http" || row.Status == "conflicted" {
		a.setStatus(slog.LevelWarn, "Select an available HTTP route to pin")
		return
	}
	if a.ports.pinning {
		return
	}
	if a.service == nil || a.service.daemonClient == nil || a.daemonDisconnected {
		a.setStatus(slog.LevelError, "Reconnect to the daemon before pinning a route")
		return
	}
	a.cancelPortsLoad()
	s := &a.ports
	s.pinGeneration++
	s.pinning = true
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	s.pinCancel = cancel
	generation, client := s.pinGeneration, a.service.daemonClient
	work := func() tuiPortPinEvent {
		defer cancel()
		err := client.Call(ctx, "ports.pin", daemon.PinPortParams{NodeID: row.NodeID, Port: row.Port}, nil)
		return tuiPortPinEvent{generation: generation, node: row.NodeSlug, port: row.Port, err: err}
	}
	if a.postEvent == nil {
		a.finishPortPin(work())
		return
	}
	go func() { a.postCompletion(work()) }()
}

func (a *vaxisTUIApp) finishPortPin(e tuiPortPinEvent) {
	if e.generation != a.ports.pinGeneration {
		return
	}
	a.ports.pinning, a.ports.pinCancel = false, nil
	if e.err != nil {
		a.setStatus(slog.LevelError, e.err.Error()+" · refresh Ports to inspect ownership")
	} else {
		a.setStatus(slog.LevelInfo, fmt.Sprintf("Pinned localhost:%d to %s until its listener stops", e.port, e.node))
	}
	a.startPortsLoad(true)
}

func (a *vaxisTUIApp) openPortNode() {
	row, ok := a.portSelection()
	if !ok {
		return
	}
	a.fleet.setFilter("")
	if node, exists := a.state.nodesByID[row.NodeID]; exists && node.DeletedAt == nil {
		a.fleet.selectID(row.NodeID)
		a.selectFleetNode()
		a.openNodeDetail(tuiDetailOverview)
		return
	}
	a.treeWorkspaceRoot, a.ports.pendingNode = "", row.NodeID
	a.startDataRefresh(terminal.NodeTarget(row.NodeID).String())
}

func (a *vaxisTUIApp) finishPortsNodeJump() {
	id := a.ports.pendingNode
	a.ports.pendingNode = ""
	if id == "" || a.view != tuiViewPorts {
		return
	}
	if a.state.selectedEntry().node.ID != id {
		a.setStatus(slog.LevelWarn, "The route's node is no longer available")
		return
	}
	a.openNodeDetail(tuiDetailOverview)
}

func (a *vaxisTUIApp) handlePortsMouse(mouse vaxis.Mouse) bool {
	if a.view != tuiViewPorts {
		return false
	}
	if mouse.EventType != vaxis.EventPress {
		return true
	}
	switch mouse.Button {
	case vaxis.MouseWheelUp:
		a.movePortSelection(-1)
	case vaxis.MouseWheelDown:
		a.movePortSelection(1)
	case vaxis.MouseLeftButton:
		for _, control := range a.ports.controls {
			if control.rect.contains(mouse.Col, mouse.Row) {
				a.handleKey(control.key)
				return true
			}
		}
		for _, row := range a.ports.mouseRows {
			if row.rect.contains(mouse.Col, mouse.Row) {
				a.ports.selected, a.ports.detailScroll = row.id, 0
				return true
			}
		}
	}
	return true
}
