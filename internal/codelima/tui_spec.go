package codelima

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"go.rockorager.dev/vaxis"
)

const tuiSpecPanelCount = 3

type tuiSpecState struct {
	nodeID           string
	loaded, inFlight bool
	snapshot         nodeSpecSnapshot
	err              error
	serial           uint64
	cancel           context.CancelFunc
	panel            int
	cursor, offset   [tuiSpecPanelCount]int
	rows             [tuiSpecPanelCount][]tuiSpecRow
	rects, tabs      [tuiSpecPanelCount]tuiRect
}

type tuiSpecDataEvent struct {
	serial, generation uint64
	data               nodeSpecSnapshot
	err                error
}

func (a *vaxisTUIApp) specVisible() bool {
	return a.view == tuiViewNodes && a.nodeDetail && a.detailTab == tuiDetailSpec
}

func (a *vaxisTUIApp) cancelSpecLoad() {
	if a.spec.cancel != nil {
		a.spec.cancel()
		a.spec.cancel = nil
	}
	a.spec.serial++
	a.spec.inFlight = false
}

func (a *vaxisTUIApp) cancelHiddenSpecLoad() {
	if a.view != tuiViewNodes || !a.nodeDetail {
		a.cancelSpecLoad()
	}
}

// Keep drift available on every node tab, including its amber tab indicator.
func (a *vaxisTUIApp) startSpecLoad() {
	if a.view != tuiViewNodes || !a.nodeDetail {
		a.cancelHiddenSpecLoad()
		return
	}
	s := &a.spec
	nodeID := a.state.selectedEntry().node.ID
	if nodeID == "" {
		return
	}
	if nodeID != s.nodeID {
		a.cancelSpecLoad()
		*s = tuiSpecState{nodeID: nodeID, serial: s.serial}
	}
	if a.service == nil || s.inFlight {
		return
	}
	s.serial++
	serial, generation, service := s.serial, a.managerGeneration, a.service
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	s.cancel, s.inFlight = cancel, true
	load := func() tuiSpecDataEvent {
		defer cancel()
		data, err := service.NodeSpec(ctx, nodeID)
		return tuiSpecDataEvent{serial: serial, generation: generation, data: data, err: err}
	}
	if a.postEvent == nil {
		a.applySpecData(load())
		return
	}
	go func() { a.postCompletion(load()) }()
}

func (a *vaxisTUIApp) applySpecData(event tuiSpecDataEvent) {
	s := &a.spec
	if event.serial != s.serial {
		return
	}
	s.inFlight = false
	if a.view != tuiViewNodes || !a.nodeDetail || event.generation != a.managerGeneration {
		return
	}
	if event.err == nil && (event.data.Node.ID != s.nodeID || s.nodeID != a.state.selectedEntry().node.ID) {
		return
	}
	s.loaded, s.err = true, event.err
	if event.err == nil {
		s.snapshot = event.data
	}
}

func (a *vaxisTUIApp) moveSpecSelection(delta int) {
	s := &a.spec
	s.cursor[s.panel] = max(0, min(len(s.rows[s.panel])-1, s.cursor[s.panel]+delta))
}

func (a *vaxisTUIApp) handleSpecKey(key vaxis.Key) bool {
	if !a.specVisible() {
		return false
	}
	s := &a.spec
	var err error
	switch {
	case key.Matches(vaxis.KeyLeft), key.Matches(vaxis.KeyUp, vaxis.ModCtrl):
		s.panel = (s.panel + tuiSpecPanelCount - 1) % tuiSpecPanelCount
	case key.Matches(vaxis.KeyRight), key.Matches(vaxis.KeyDown, vaxis.ModCtrl):
		s.panel = (s.panel + 1) % tuiSpecPanelCount
	case key.Matches(vaxis.KeyPgUp):
		a.moveSpecSelection(-max(1, s.rects[s.panel].height-1))
	case key.Matches(vaxis.KeyPgDown):
		a.moveSpecSelection(max(1, s.rects[s.panel].height-1))
	case key.Matches(vaxis.KeyHome):
		s.cursor[s.panel] = 0
	case key.Matches(vaxis.KeyEnd):
		s.cursor[s.panel] = max(0, len(s.rows[s.panel])-1)
	case key.MatchString("N"), key.MatchString("e"):
		if s.snapshot.Template == nil {
			err = errors.New("the source template is unavailable; clone the frozen node with c")
			break
		}
		if key.MatchString("e") {
			a.openUpdateConfigurationDialog(*s.snapshot.Template)
			break
		}
		err = a.openCreateNodeDialogWithDefaults(s.snapshot.Node.DirectoryPath, s.snapshot.Template.Slug)
	case key.MatchString("y"):
		if s.snapshot.LimaPath == "" {
			err = errors.New("Lima YAML path is not available yet")
			break
		}
		err = a.copyToHostClipboard(s.snapshot.LimaPath)
		if err == nil {
			a.setStatus(slog.LevelInfo, "sent Lima YAML path copy request to host")
		}
	case key.MatchString("v"):
		err = a.openFilePager(s.snapshot.LimaPath)
	case key.Matches(vaxis.KeyEnter):
		rows, cursor := s.rows[s.panel], s.cursor[s.panel]
		if cursor >= 0 && cursor < len(rows) {
			if rows[cursor].command != "" {
				a.openSpecCommandLogs(rows[cursor].command)
			}
			if action := rows[cursor].action; action != 0 {
				a.dispatchManagerKey(vaxis.Key{Keycode: action, Text: string(action)})
			}
		}
	default:
		return false
	}
	if err != nil {
		a.setStatus(slog.LevelError, err.Error())
	}
	return true
}

func (a *vaxisTUIApp) openSpecCommandLogs(command string) {
	a.cancelLogsLoad()
	query := strings.TrimSpace(strings.Split(command, "\n")[0])
	a.logs = tuiLogsState{nodeID: a.spec.nodeID, source: 1, wrap: true, follow: false, query: plainLogText(query), selected: -1, serial: a.logs.serial}
	a.openNodeDetail(tuiDetailLogs)
	// Opening another tab normally resumes follow. A step is an inspection of
	// a specific command, so keep its selected output still after the first read.
	a.logs.follow = false
}

func (a *vaxisTUIApp) handleSpecMouse(mouse vaxis.Mouse) bool {
	if !a.specVisible() {
		return false
	}
	if mouse.EventType != vaxis.EventPress {
		return true
	}
	s := &a.spec
	for i, rect := range s.tabs {
		if rect.contains(mouse.Col, mouse.Row) && mouse.Button == vaxis.MouseLeftButton {
			s.panel = i
			return true
		}
	}
	for i, rect := range s.rects {
		if !rect.contains(mouse.Col, mouse.Row) {
			continue
		}
		s.panel = i
		switch mouse.Button {
		case vaxis.MouseWheelUp:
			a.moveSpecSelection(-1)
		case vaxis.MouseWheelDown:
			a.moveSpecSelection(1)
		case vaxis.MouseLeftButton:
			s.cursor[i] = max(0, min(len(s.rows[i])-1, s.offset[i]+mouse.Row-rect.row))
			if len(s.rows[i]) > 0 {
				if action := s.rows[i][s.cursor[i]].action; action != 0 {
					a.dispatchManagerKey(vaxis.Key{Keycode: action, Text: string(action)})
				}
			}
		}
		return true
	}
	return true
}
