package codelima

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/textinput"
)

var tuiLogLevels = [...]string{"all", "info+", "warn+", "error"}

type tuiLogsState struct {
	nodeID             string
	source, level      int
	follow, wrap       bool
	loaded, inFlight   bool
	snapshot           nodeLogSnapshot
	selected, offset   int
	selectedPart       int
	revealMatch        bool
	layoutWidth        int
	query, beforeQuery string
	input              *textinput.Model
	serial             uint64
	cancel             context.CancelFunc
	err                error
	bodyRect           tuiRect
	sourceRects        [4]tuiRect
	mouseRows          []int
}

type tuiLogsDataEvent struct {
	serial, generation uint64
	data               nodeLogSnapshot
	err                error
}

func (a *vaxisTUIApp) logsVisible() bool {
	return a.view == tuiViewNodes && a.nodeDetail && a.detailTab == tuiDetailLogs
}

func (a *vaxisTUIApp) cancelLogsLoad() {
	if a.logs.cancel != nil {
		a.logs.cancel()
		a.logs.cancel = nil
	}
	a.logs.serial++
	a.logs.inFlight = false
}

func (a *vaxisTUIApp) cancelHiddenLogsLoad() {
	if !a.logsVisible() {
		a.cancelLogsLoad()
		a.logs.input = nil
	}
}

func (a *vaxisTUIApp) startLogsLoad(force bool) {
	if !a.logsVisible() {
		a.cancelHiddenLogsLoad()
		return
	}
	l := &a.logs
	nodeID := a.state.selectedEntry().node.ID
	if nodeID == "" {
		return
	}
	if nodeID != l.nodeID {
		a.cancelLogsLoad()
		*l = tuiLogsState{nodeID: nodeID, source: l.source, wrap: l.wrap, follow: true, selected: -1, serial: l.serial}
	}
	if a.service == nil || l.inFlight || (!force && l.loaded && !l.follow) {
		return
	}
	l.serial++
	serial, generation := l.serial, a.managerGeneration
	source, service := nodeLogSources[l.source], a.service
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	l.cancel, l.inFlight = cancel, true
	load := func() tuiLogsDataEvent {
		defer cancel()
		data, err := service.NodeLogSource(ctx, nodeID, source)
		return tuiLogsDataEvent{serial: serial, generation: generation, data: data, err: err}
	}
	if a.postEvent == nil {
		a.applyLogsData(load())
		return
	}
	go func() { a.postCompletion(load()) }()
}

func (a *vaxisTUIApp) applyLogsData(event tuiLogsDataEvent) {
	l := &a.logs
	if event.serial != l.serial {
		return
	}
	l.inFlight = false
	if !a.logsVisible() || event.generation != a.managerGeneration {
		return
	}
	initial := !l.loaded
	l.loaded, l.err = true, event.err
	if event.err != nil {
		return
	}
	if event.data.NodeID != l.nodeID || event.data.Source != nodeLogSources[l.source] {
		return
	}
	// Preserve the selected record across a growing tail, but do not confuse
	// reused file offsets after rotation with the old record.
	var selected *nodeLogLine
	if l.selected >= 0 && l.selected < len(l.snapshot.Lines) {
		selected = &l.snapshot.Lines[l.selected]
	}
	l.snapshot, l.selected = event.data, -1
	if selected != nil {
		for i, line := range l.snapshot.Lines {
			if line.Offset == selected.Offset && line.Text == selected.Text && line.Kind == selected.Kind {
				l.selected = i
				break
			}
		}
	}
	if l.follow {
		l.selectLast()
	} else {
		l.ensureSelection()
		if initial && l.query != "" {
			l.nextMatch(1, true)
		}
	}
}

func logLevelRank(level string) int {
	switch strings.ToLower(level) {
	case "trace", "debug":
		return 0
	case "warn", "warning":
		return 2
	case "error", "fatal", "panic":
		return 3
	default:
		return 1
	}
}

func (l *tuiLogsState) visibleLines() []int {
	var indices []int
	for i, line := range l.snapshot.Lines {
		if logLevelRank(line.Level) >= l.level {
			indices = append(indices, i)
		}
	}
	return indices
}

func (l *tuiLogsState) ensureSelection() {
	indices := l.visibleLines()
	if slices.Contains(indices, l.selected) {
		return
	}
	l.selected, l.offset = -1, 0
	l.selectedPart = 0
	if len(indices) > 0 {
		l.selected = indices[0]
	}
}

func (l *tuiLogsState) selectLast() {
	l.selected = -1
	indices := l.visibleLines()
	if len(indices) > 0 {
		l.selected = indices[len(indices)-1]
	}
}

func (l *tuiLogsState) searchPattern() *regexp.Regexp {
	if l.query == "" {
		return nil
	}
	return regexp.MustCompile("(?i)" + regexp.QuoteMeta(l.query))
}

func (l *tuiLogsState) matches() []int {
	pattern := l.searchPattern()
	var result []int
	if pattern == nil {
		return result
	}
	for _, i := range l.visibleLines() {
		if pattern.MatchString(logLineText(l.snapshot.Lines[i])) {
			result = append(result, i)
		}
	}
	return result
}

func (l *tuiLogsState) nextMatch(delta int, includeCurrent bool) {
	matches := l.matches()
	if len(matches) == 0 {
		return
	}
	l.selectedPart = 0
	l.revealMatch = true
	if delta > 0 {
		for _, index := range matches {
			if index > l.selected || (includeCurrent && index == l.selected) {
				l.selected = index
				return
			}
		}
		l.selected = matches[0]
	} else {
		for i := len(matches) - 1; i >= 0; i-- {
			if matches[i] < l.selected {
				l.selected = matches[i]
				return
			}
		}
		l.selected = matches[len(matches)-1]
	}
}

func (a *vaxisTUIApp) pauseLogs() { a.logs.follow = false; a.cancelLogsLoad() }

func (a *vaxisTUIApp) moveLogSelection(delta int) {
	a.pauseLogs()
	l := &a.logs
	l.ensureSelection()
	if l.wrap && l.bodyRect.width > 0 {
		rows := a.logRows(l.bodyRect.width)
		for i, row := range rows {
			if row.index != l.selected {
				continue
			}
			cursor := min(len(rows)-1, i+l.selectedPart)
			next := max(0, min(len(rows)-1, cursor+delta))
			l.selected = rows[next].index
			first := next
			for first > 0 && rows[first-1].index == l.selected {
				first--
			}
			l.selectedPart = next - first
			return
		}
	}
	indices := l.visibleLines()
	for i, index := range indices {
		if index == l.selected {
			l.selected = indices[max(0, min(len(indices)-1, i+delta))]
			l.selectedPart = 0
			return
		}
	}
}

func (a *vaxisTUIApp) changeLogSource(source int) {
	a.cancelLogsLoad()
	l := &a.logs
	*l = tuiLogsState{nodeID: l.nodeID, source: source, follow: true, wrap: l.wrap, selected: -1, serial: l.serial}
	a.startLogsLoad(true)
}

// Search owns every text key (including view numbers and bracketed paste)
// before the manager's global actions. Escape restores the previous query.
func (a *vaxisTUIApp) handleLogsSearchKey(key vaxis.Key) bool {
	return a.handleLogsSearchAction(a.keyAction(key), key)
}

func (a *vaxisTUIApp) handleLogsSearchAction(action string, key vaxis.Key) bool {
	if !a.logsVisible() || a.logs.input == nil {
		return false
	}
	l := &a.logs
	switch {
	case action == "input.cancel" && tuiKeyActivates(key, true):
		l.query, l.input = l.beforeQuery, nil
		a.claimShortcutKey(key)
	case action == "input.accept" && tuiKeyActivates(key, true):
		l.input = nil
		a.claimShortcutKey(key)
	default:
		a.updateLogsSearchInput(key)
	}
	return true
}

func (a *vaxisTUIApp) updateLogsSearchInput(event vaxis.Event) {
	l := &a.logs
	if l.input == nil {
		return
	}
	l.input.Update(event)
	query := plainLogText(l.input.String())
	if query != l.query {
		l.query = query
		l.nextMatch(1, true)
	}
}

func (a *vaxisTUIApp) handleLogsAction(action string) bool {
	if !a.logsVisible() {
		return false
	}
	l := &a.logs
	switch {
	case action == "logs.source_previous":
		a.changeLogSource((l.source + len(nodeLogSources) - 1) % len(nodeLogSources))
	case action == "logs.source_next":
		a.changeLogSource((l.source + 1) % len(nodeLogSources))
	case action == "logs.follow":
		if l.follow {
			a.pauseLogs()
		} else {
			l.follow = true
			l.selectLast()
			a.startLogsLoad(true)
		}
	case action == "logs.search":
		a.pauseLogs()
		l.beforeQuery = l.query
		l.input = textinput.New()
		l.input.SetContent(l.query)
	case action == "logs.match_next", action == "logs.match_previous":
		a.pauseLogs()
		delta := 1
		if action == "logs.match_previous" {
			delta = -1
		}
		l.nextMatch(delta, false)
	case action == "logs.wrap":
		l.wrap, l.offset = !l.wrap, 0
		l.selectedPart = 0
		l.revealMatch = l.query != ""
	case action == "logs.level":
		l.level, l.offset = (l.level+1)%len(tuiLogLevels), 0
		l.ensureSelection()
		if l.follow {
			l.selectLast()
		}
	case action == "logs.first":
		a.pauseLogs()
		l.selected, l.offset = -1, 0
		l.selectedPart = 0
		l.ensureSelection()
	case action == "logs.last":
		l.follow = true
		l.selectLast()
		a.startLogsLoad(true)
	case action == "navigation.page_up", action == "navigation.page_down":
		delta := max(1, l.bodyRect.height-1)
		if action == "navigation.page_up" {
			delta = -delta
		}
		a.moveLogSelection(delta)
	case action == "logs.copy":
		if l.selected >= 0 && l.selected < len(l.snapshot.Lines) {
			if err := a.copyToHostClipboard(logLineText(l.snapshot.Lines[l.selected])); err != nil {
				a.setStatus(slog.LevelError, err.Error())
			} else {
				a.setStatus(slog.LevelInfo, "Log line sent to host clipboard")
			}
		}
	case action == "logs.pager":
		if err := a.openLogPager(); err != nil {
			a.setStatus(slog.LevelError, err.Error())
		}
	default:
		return false
	}
	return true
}

func logLineText(line nodeLogLine) string {
	if line.Kind == "" && line.Timestamp.IsZero() {
		return line.Text
	}
	timestamp := ""
	if !line.Timestamp.IsZero() {
		timestamp = line.Timestamp.Format("Jan 02 15:04:05")
	}
	return fmt.Sprintf("%-16s %-5s %-26s %s", timestamp, line.Level, line.Kind, line.Text)
}

func nodeLogPagerArgs(pager, path string) ([]string, error) {
	if strings.TrimSpace(pager) == "" {
		pager = "less"
	}
	args, ok := argvFromQuotedCommand(pager)
	if !ok || len(args) == 0 {
		return nil, errors.New("PAGER must be an executable and quoted arguments; use a wrapper for shell scripts")
	}
	return append(args, path), nil
}

func (a *vaxisTUIApp) openLogPager() error {
	l := &a.logs
	if l.snapshot.Path == "" || l.snapshot.Missing {
		return errors.New("this log file does not exist yet")
	}
	a.pauseLogs()
	return a.openFilePager(l.snapshot.Path)
}

func (a *vaxisTUIApp) openFilePager(path string) error {
	if path == "" {
		return errors.New("file path is not available yet")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("pager input must be a regular file")
	}
	args, err := nodeLogPagerArgs(os.Getenv("PAGER"), path)
	if err != nil {
		return err
	}
	return a.runInteractiveProcess(args)
}

func (a *vaxisTUIApp) runInteractiveProcess(args []string) error {
	if _, err := exec.LookPath(args[0]); err != nil {
		return fmt.Errorf("interactive command: %w", err)
	}
	if a.vx == nil {
		return errors.New("interactive terminal is unavailable")
	}
	if err := a.vx.Suspend(); err != nil {
		return err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	runErr := cmd.Run()
	return errors.Join(runErr, a.vx.Resume())
}
