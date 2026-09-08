package codelima

import (
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/textinput"
)

type tuiInspectionRequest struct {
	target   string
	receiver terminalInteractionReceiver
	request  TerminalInteractionRequest
	version  uint64
	copy     bool
	epoch    uint64
}

type tuiInspectionResult struct {
	request tuiInspectionRequest
	result  TerminalInteractionResult
	err     error
}

// One bounded worker performs inspection RPCs; the UI owns every result.
// Its independent result channel cannot lose a completion in redraw traffic.
type tuiTerminalInspector struct {
	requests chan tuiInspectionRequest
	results  chan tuiInspectionResult
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
}

func newTUITerminalInspector() *tuiTerminalInspector {
	inspector := &tuiTerminalInspector{requests: make(chan tuiInspectionRequest, 32), results: make(chan tuiInspectionResult, 32), stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(inspector.done)
		for {
			select {
			case <-inspector.stop:
				return
			default:
			}
			select {
			case <-inspector.stop:
				return
			case request := <-inspector.requests:
				result, err := request.receiver.Interact(request.request)
				select {
				case inspector.results <- tuiInspectionResult{request: request, result: result, err: err}:
				case <-inspector.stop:
					return
				}
			}
		}
	}()
	return inspector
}

func (i *tuiTerminalInspector) close() {
	if i == nil {
		return
	}
	i.once.Do(func() { close(i.stop) })
	<-i.done
}

type tuiTerminalSearch struct {
	target  string
	input   *textinput.Model
	version uint64
	pending bool
	status  TerminalSearchStatus
	tick    *time.Timer
	closing bool
}

type tuiTerminalSearchTick struct{ version uint64 }

func (a *vaxisTUIApp) supportsTerminalInspection(target string) bool {
	if a.sessions == nil {
		return false
	}
	term, ok := a.sessions.SessionTerminal(target)
	if !ok {
		return false
	}
	_, ok = term.(terminalInteractionReceiver)
	return ok
}

func (a *vaxisTUIApp) inspectionResults() <-chan tuiInspectionResult {
	if a.inspector == nil {
		return nil
	}
	return a.inspector.results
}

func (a *vaxisTUIApp) queueTerminalInteraction(target string, request TerminalInteractionRequest, copyText bool, version uint64) error {
	term, ok := a.sessions.SessionTerminal(target)
	if !ok {
		return errors.New("terminal is unavailable")
	}
	receiver, ok := term.(terminalInteractionReceiver)
	if !ok {
		return errors.New("terminal inspection is unavailable")
	}
	if a.inspector == nil {
		a.inspector = newTUITerminalInspector()
	}
	select {
	case a.inspector.requests <- tuiInspectionRequest{target: target, receiver: receiver, request: request, copy: copyText, version: version, epoch: a.graphicsEpoch}:
		return nil
	default:
		return errors.New("terminal inspection queue is full")
	}
}

func (a *vaxisTUIApp) handleInspectionResult(event tuiInspectionResult) {
	if event.request.epoch != a.graphicsEpoch || a.sessions == nil {
		return
	}
	current, ok := a.sessions.SessionTerminal(event.request.target)
	if !ok || current == nil || !reflect.TypeOf(current).Comparable() || any(current) != any(event.request.receiver) {
		return
	}
	if event.request.request.Action == "colors" {
		// An observer parks a rejected propagation until seat reacquisition.
		if event.err != nil && !isSeatRejection(event.err) {
			if event.request.request.Colors == a.hostColors {
				delete(a.hostColorsSent, event.request.target)
				a.hostColorsRetry = time.Now().Add(time.Second)
				if a.hostColorsTick != nil {
					a.hostColorsTick.Stop()
				}
				a.hostColorsTick = time.NewTimer(time.Second)
			}
			a.setStatus(slog.LevelError, event.err.Error())
		}
		return
	}
	switch event.request.request.Action {
	case "search", "tick", "next", "previous":
		if a.search == nil || event.request.version != a.search.version {
			return
		}
	}
	if a.state == nil || event.request.target != a.state.activeSessionKey() {
		return
	}
	if event.err != nil {
		a.setStatus(slog.LevelError, event.err.Error())
		if a.search != nil && a.search.version == event.request.version {
			a.search.pending = false
		}
		return
	}
	if event.request.copy && event.result.Text != "" {
		if err := a.copyToHostClipboard(event.result.Text); err != nil {
			a.setStatus(slog.LevelError, err.Error())
		} else {
			a.setStatus(slog.LevelInfo, "copied terminal selection")
		}
	}
	if a.search != nil && a.search.version == event.request.version {
		a.search.status, a.search.pending = event.result.Search, false
		if !event.result.Search.CaughtUp {
			if a.search.tick != nil {
				a.search.tick.Stop()
			}
			a.search.tick = time.NewTimer(50 * time.Millisecond)
		}
	}
}

func (a *vaxisTUIApp) searchTickEvents() <-chan time.Time {
	if a.search == nil || a.search.tick == nil {
		return nil
	}
	return a.search.tick.C
}

func (a *vaxisTUIApp) openTerminalSearch() error {
	if a.state == nil || a.sessions == nil {
		return errors.New("terminal is unavailable")
	}
	if a.search != nil {
		a.closeTerminalSearch()
		return nil
	}
	if !a.supportsTerminalInspection(a.state.activeSessionKey()) {
		return errors.New("terminal search is unavailable")
	}
	a.searchVersion++
	a.search = &tuiTerminalSearch{target: a.state.activeSessionKey(), input: textinput.New().SetPrompt("Find: "), version: a.searchVersion}
	return nil
}

func (a *vaxisTUIApp) closeTerminalSearch() {
	if a.search == nil {
		return
	}
	if a.search.tick != nil {
		a.search.tick.Stop()
	}
	if !a.supportsTerminalInspection(a.search.target) {
		a.search = nil
		return
	}
	if err := a.queueTerminalInteraction(a.search.target, TerminalInteractionRequest{Action: "search"}, false, 0); err != nil {
		a.search.closing = true
		a.search.pending = false
		a.search.tick = time.NewTimer(50 * time.Millisecond)
		return
	}
	a.search = nil
}

func (a *vaxisTUIApp) closeTerminalInspector() {
	var receiver terminalInteractionReceiver
	if a.search != nil && a.sessions != nil {
		if term, ok := a.sessions.SessionTerminal(a.search.target); ok {
			receiver, _ = term.(terminalInteractionReceiver)
		}
		if a.search.tick != nil {
			a.search.tick.Stop()
		}
	}
	a.inspector.close()
	// Shutdown discards queued work; apply the idempotent final clear only
	// after the bounded worker joins, so it cannot race a newer query.
	if receiver != nil {
		_, _ = receiver.Interact(TerminalInteractionRequest{Action: "search"})
	}
	a.search = nil
	if a.hostColorsTick != nil {
		a.hostColorsTick.Stop()
	}
}

func (a *vaxisTUIApp) searchTick() {
	if a.search != nil && a.search.closing {
		a.closeTerminalSearch()
		return
	}
	if a.search == nil || a.search.pending || a.search.input.String() == "" {
		return
	}
	a.search.pending = true
	if err := a.queueTerminalInteraction(a.search.target, TerminalInteractionRequest{Action: "tick"}, false, a.search.version); err != nil {
		a.search.pending = false
		a.setStatus(slog.LevelError, err.Error())
	}
}

func (a *vaxisTUIApp) handleSearchKey(key vaxis.Key) {
	if a.search.closing {
		a.closeTerminalSearch()
		return
	}
	if key.Keycode == vaxis.KeyEsc || key.Keycode == vaxis.KeyF07 {
		a.closeTerminalSearch()
		return
	}
	request := TerminalInteractionRequest{}
	switch key.Keycode {
	case vaxis.KeyEnter:
		request.Action = "next"
		if key.Modifiers&vaxis.ModShift != 0 {
			request.Action = "previous"
		}
	default:
		previous := a.search.input.String()
		a.search.input.Update(key)
		if len(a.search.input.String()) > 4096 {
			a.search.input.SetContent(previous)
			return
		}
		if previous == a.search.input.String() {
			return
		}
		a.searchVersion++
		a.search.version = a.searchVersion
		request.Action, request.Query = "search", a.search.input.String()
	}
	a.search.pending = true
	if err := a.queueTerminalInteraction(a.search.target, request, false, a.search.version); err != nil {
		a.search.pending = false
		a.setStatus(slog.LevelError, err.Error())
	}
}

func (a *vaxisTUIApp) drawTerminalSearch(win vaxis.Window) {
	if a.search == nil || a.search.target != a.state.activeSessionKey() {
		return
	}
	width, height := win.Size()
	if width < 20 || height < 2 {
		return
	}
	bar := win.New(0, height-2, width, 2)
	bar.Clear()
	a.search.input.Draw(bar.New(0, 0, width, 1))
	progress := "searching"
	if a.search.status.CaughtUp {
		progress = "caught up"
	}
	bar.Println(1, vaxis.Segment{Text: fmt.Sprintf("%d matches (%s) · Enter next · Shift+Enter previous · Esc close", a.search.status.Matches, progress)})
}
