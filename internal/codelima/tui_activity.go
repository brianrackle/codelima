package codelima

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/textinput"
)

type tuiActivityState struct {
	nativeEpoch                  string
	nativeThrough                uint64
	page                         ActivityPage
	loaded, inFlight             bool
	serial                       uint64
	cancel                       context.CancelFunc
	lastLoad                     time.Time
	err                          error
	selected, pendingNode        string
	before                       string
	previous                     []string
	level                        int
	query, beforeQuery           string
	input                        *textinput.Model
	runningScroll, historyScroll int
	viewport                     int
	mouseRows                    []tuiActivityMouseRow
	canceling                    map[string]bool
}

type tuiActivityDataEvent struct {
	serial uint64
	before string
	page   ActivityPage
	err    error
}

type tuiActivityCancelEvent struct {
	id  string
	err error
}

type tuiActivityRow struct {
	id, nodeID, node, text string
	at                     time.Time
	level                  slog.Level
	operation              bool
	success                bool
	kind, state            string
	progress               *NodeOperationProgress
}

type tuiActivityMouseRow struct {
	id   string
	rect tuiRect
}

// This projection is global, including when the fleet is scoped or another
// view is open. A separate serial prevents late history-page replies from
// replacing a new page without tying daemon ownership to navigation.
func (a *vaxisTUIApp) startActivityLoad(force bool) {
	s := &a.activity
	if s.inFlight && time.Since(s.lastLoad) >= 6*time.Second {
		if s.cancel != nil {
			s.cancel()
		}
		s.serial++
		s.inFlight = false
	}
	if a.service == nil || a.service.daemonClient == nil || a.daemonDisconnected || s.inFlight {
		return
	}
	interval := 10 * time.Second
	if a.view == tuiViewActivity || len(s.page.Running) > 0 {
		interval = time.Second
	}
	if !force && time.Since(s.lastLoad) < interval {
		return
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	s.serial++
	s.cancel, s.inFlight, s.lastLoad = cancel, true, time.Now()
	serial, before, client := s.serial, s.before, a.service.daemonClient
	load := func() tuiActivityDataEvent {
		defer cancel()
		var page ActivityPage
		err := client.Call(ctx, "activity.list", map[string]any{"before": before, "limit": 50}, &page)
		return tuiActivityDataEvent{serial: serial, before: before, page: page, err: err}
	}
	if a.postEvent == nil {
		a.applyActivityData(load())
		return
	}
	go func() { a.postCompletion(load()) }()
}

func (a *vaxisTUIApp) applyActivityData(event tuiActivityDataEvent) {
	s := &a.activity
	if s.serial != event.serial || s.before != event.before {
		return
	}
	s.inFlight, s.loaded, s.err = false, true, event.err
	if event.err != nil {
		return
	}
	s.page = event.page
	a.reconcileActivitySelection()
}

func activityOperationRow(op NodeOperation) tuiActivityRow {
	row := activityRecordRow(activityOperationRecord(op, false))
	row.kind, row.state, row.progress = op.Request.Kind, op.State, op.Progress
	if op.CurrentPhase != "" {
		row.text += " · " + plainLogText(op.CurrentPhase)
	}
	return row
}

func activityRecordRow(record ActivityRecord) tuiActivityRow {
	text := record.Text
	if record.OperationID != "" && record.Source != "" {
		text += " · via " + strings.ToUpper(record.Source)
	}
	return tuiActivityRow{id: record.ID, nodeID: record.NodeID, node: coalesce(record.NodeSlug, record.NodeID, record.Source), text: plainLogText(text), at: record.Time, level: record.Level, operation: record.OperationID != "", success: record.Success}
}

func (a *vaxisTUIApp) activityRows() (running, history []tuiActivityRow) {
	s := &a.activity
	for _, op := range s.page.Running {
		running = append(running, activityOperationRow(op))
	}
	seen := make(map[string]bool)
	for _, record := range s.page.History {
		history = append(history, activityRecordRow(record))
		seen[record.ID] = true
	}
	// Only the newest page overlays this window's pending messages. Acked
	// messages remain until a same-epoch snapshot covers their sequence;
	// membership in the current fifty rows is not a persistence watermark.
	if s.before == "" {
		for _, entry := range a.messages.Entries() {
			id := "message:" + entry.ID
			if seen[id] {
				continue
			}
			if entry.ack.Epoch != "" {
				if s.loaded && s.page.Epoch != "" && (entry.ack.Epoch != s.page.Epoch || entry.ack.Message.Sequence <= s.page.MessageThrough) {
					continue
				}
				history = append(history, activityRecordRow(activityMessageRecord(entry.ack.Message)))
			} else {
				history = append(history, tuiActivityRow{id: id, node: "this window", text: plainLogText(entry.Text), at: entry.Time, level: entry.Level})
			}
		}
	}
	slices.SortStableFunc(history, func(x, y tuiActivityRow) int { return y.at.Compare(x.at) })
	history = slices.DeleteFunc(history, func(row tuiActivityRow) bool {
		if (s.level == 1 && row.level < slog.LevelWarn) || (s.level == 2 && row.level < slog.LevelError) {
			return true
		}
		return !strings.Contains(strings.ToLower(row.node+" "+row.text), strings.ToLower(s.query))
	})
	return running, history
}

func (a *vaxisTUIApp) activitySelection() (tuiActivityRow, bool) {
	running, history := a.activityRows()
	for _, rows := range [][]tuiActivityRow{running, history} {
		for _, row := range rows {
			if row.id == a.activity.selected {
				return row, true
			}
		}
	}
	return tuiActivityRow{}, false
}

func (a *vaxisTUIApp) reconcileActivitySelection() {
	if _, ok := a.activitySelection(); ok {
		return
	}
	running, history := a.activityRows()
	a.activity.selected = ""
	if len(history) > 0 {
		a.activity.selected = history[0].id
	} else if len(running) > 0 {
		a.activity.selected = running[0].id
	}
}

func (a *vaxisTUIApp) moveActivitySelection(delta int) {
	running, history := a.activityRows()
	rows := slices.Concat(running, history)
	if len(rows) == 0 {
		return
	}
	index := slices.IndexFunc(rows, func(row tuiActivityRow) bool { return row.id == a.activity.selected })
	a.activity.selected = rows[max(0, min(len(rows)-1, index+delta))].id
}

func (a *vaxisTUIApp) updateActivityInput(event vaxis.Event) {
	s := &a.activity
	s.input.Update(event)
	s.query = plainLogText(s.input.String())
	s.historyScroll = 0
	a.reconcileActivitySelection()
}

func (a *vaxisTUIApp) handleActivitySearchKey(key vaxis.Key) bool {
	s := &a.activity
	if a.view != tuiViewActivity || s.input == nil {
		return false
	}
	switch {
	case key.EventType == vaxis.EventPaste:
		a.updateActivityInput(key)
	case key.Matches(vaxis.KeyEsc) && tuiKeyActivates(key, true):
		s.query, s.input = s.beforeQuery, nil
		a.reconcileActivitySelection()
	case key.Matches(vaxis.KeyEnter) && tuiKeyActivates(key, true):
		s.input = nil
	default:
		a.updateActivityInput(key)
	}
	return true
}

func (a *vaxisTUIApp) changeActivityPage(direction int) {
	s := &a.activity
	before := s.before
	switch {
	case direction == 0:
		before, s.previous = "", nil
	case direction > 0 && s.page.Next != "":
		s.previous = append(s.previous, before)
		before = s.page.Next
	case direction < 0 && len(s.previous) > 0:
		before = s.previous[len(s.previous)-1]
		s.previous = s.previous[:len(s.previous)-1]
	default:
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.serial++
	s.before, s.inFlight, s.loaded = before, false, false
	s.selected, s.historyScroll = "", 0
	s.page.History, s.page.Next = nil, ""
	a.startActivityLoad(true)
}

func (a *vaxisTUIApp) handleActivityKey(key vaxis.Key) bool {
	if a.view != tuiViewActivity {
		return false
	}
	s := &a.activity
	switch {
	case key.MatchString("/"):
		s.beforeQuery, s.input = s.query, textinput.New()
		s.input.SetContent(s.query)
	case key.MatchString("f"):
		s.level = (s.level + 1) % 3
		a.reconcileActivitySelection()
	case key.MatchString("["):
		a.changeActivityPage(-1)
	case key.MatchString("]"):
		a.changeActivityPage(1)
	case key.MatchString("g"), key.Matches(vaxis.KeyHome):
		a.changeActivityPage(0)
	case key.Matches(vaxis.KeyPgUp):
		a.moveActivitySelection(-max(1, s.viewport))
	case key.Matches(vaxis.KeyPgDown):
		a.moveActivitySelection(max(1, s.viewport))
	case key.MatchString("y"):
		if row, ok := a.activitySelection(); ok {
			if err := a.copyToHostClipboard(row.at.Format(time.RFC3339) + " " + row.node + " " + row.text); err != nil {
				a.setStatus(slog.LevelError, err.Error())
			} else {
				a.setStatus(slog.LevelInfo, "Copied activity record")
			}
		}
	case key.MatchString("K"):
		a.cancelActivityOperation()
	case key.Matches(vaxis.KeyEnter):
		a.openActivityNode()
	default:
		return false
	}
	return true
}

func (a *vaxisTUIApp) cancelActivityOperation() {
	s := &a.activity
	index := slices.IndexFunc(s.page.Running, func(op NodeOperation) bool { return op.ID == s.selected })
	if index < 0 || s.canceling[s.selected] {
		return
	}
	a.requestOperationCancel(s.selected)
}

func (a *vaxisTUIApp) requestOperationCancel(id string) {
	s := &a.activity
	if id == "" || s.canceling[id] {
		return
	}
	if a.service == nil || a.service.daemonClient == nil {
		a.setStatus(slog.LevelError, "Daemon connection is required to cancel an operation")
		return
	}
	client := a.service.daemonClient
	if s.canceling == nil {
		s.canceling = make(map[string]bool)
	}
	s.canceling[id] = true
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cancel := func() tuiActivityCancelEvent {
		ctx, done := context.WithTimeout(ctx, 5*time.Second)
		defer done()
		var record NodeOperation
		err := client.Call(ctx, "operation.cancel", map[string]string{"id": id}, &record)
		return tuiActivityCancelEvent{id: id, err: err}
	}
	if a.postEvent == nil {
		a.finishActivityCancel(cancel())
		return
	}
	go func() { a.postCompletion(cancel()) }()
}

func (a *vaxisTUIApp) finishActivityCancel(event tuiActivityCancelEvent) {
	delete(a.activity.canceling, event.id)
	if event.err != nil {
		a.setStatus(slog.LevelError, event.err.Error())
	} else {
		a.setStatus(slog.LevelInfo, "Cancellation requested; start/retry also stops the VM")
	}
	a.startActivityLoad(true)
}

func (a *vaxisTUIApp) openActivityNode() {
	row, ok := a.activitySelection()
	if !ok || row.nodeID == "" {
		return
	}
	a.fleet.setFilter("")
	if node, exists := a.state.nodesByID[row.nodeID]; exists && node.DeletedAt == nil {
		a.fleet.selectID(row.nodeID)
		a.selectFleetNode()
		a.openNodeDetail(tuiDetailOverview)
		return
	}
	// Resolve outside the current directory projection through the normal
	// asynchronous refresh. Navigation can revoke this pending jump.
	a.treeWorkspaceRoot = ""
	a.fleet.groupByDir = a.managerSettings().GroupByDirectory
	a.activity.pendingNode = row.nodeID
	a.startDataRefresh(terminal.NodeTarget(row.nodeID).String())
}

func (a *vaxisTUIApp) finishActivityNodeJump() {
	id := a.activity.pendingNode
	a.activity.pendingNode = ""
	if id == "" || a.view != tuiViewActivity {
		return
	}
	if a.state.selectedEntry().node.ID != id {
		a.setStatus(slog.LevelWarn, "Operation retained; its node is no longer available")
		return
	}
	a.openNodeDetail(tuiDetailOverview)
}
