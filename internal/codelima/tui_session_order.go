package codelima

import (
	"context"
	"log/slog"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

type tuiSessionOrderState struct {
	serial  uint64
	loading bool
}

type tuiSessionOrderChangedEvent struct{}

type tuiSessionOrderEvent struct {
	serial uint64
	states []daemon.TerminalState
	err    error
}

// A burst of open/close/move events needs at most one outstanding list read
// and one follow-up. Membership still belongs to the created/closed events and
// full synchronization; a delayed order read must not resurrect a closed tab
// or discard a tab created after that read.
func (a *vaxisTUIApp) requestSessionOrder() {
	a.sessionOrder.serial++
	a.startSessionOrderLoad()
}

func (a *vaxisTUIApp) startSessionOrderLoad() {
	if a.sessionOrder.loading || a.sessions == nil || a.service == nil || a.service.daemonClient == nil || a.daemonDisconnected {
		return
	}
	a.sessionOrder.loading = true
	serial := a.sessionOrder.serial
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	store := a.sessions
	go func() {
		defer cancel()
		event := tuiSessionOrderEvent{serial: serial}
		event.err = store.Call(ctx, "terminal.list", nil, &event.states)
		a.postCompletion(event)
	}()
}

func (a *vaxisTUIApp) applySessionOrder(event tuiSessionOrderEvent) {
	a.sessionOrder.loading = false
	if event.serial != a.sessionOrder.serial {
		a.startSessionOrderLoad()
		return
	}
	if a.daemonDisconnected || a.sessions == nil {
		return
	}
	if event.err != nil {
		a.setFooterStatus(slog.LevelWarn, "Could not refresh session order: "+event.err.Error())
		return
	}
	a.sessions.reorderKnownDaemonSessions(event.states)
}

func (s *tuiSessionStore) reorderKnownDaemonSessions(states []daemon.TerminalState) {
	orders := make(map[string][]terminal.TerminalID)
	for _, record := range states {
		orders[record.Target] = append(orders[record.Target], terminal.TerminalID(record.TerminalID))
	}
	for target, state := range s.targets {
		byID := make(map[terminal.TerminalID]terminal.TerminalTabState, len(state.Tabs))
		for _, tab := range state.Tabs {
			byID[tab.TerminalID] = tab
		}
		ordered := make([]terminal.TerminalTabState, 0, len(state.Tabs))
		for _, id := range orders[target.String()] {
			if tab, ok := byID[id]; ok {
				ordered = append(ordered, tab)
				delete(byID, id)
			}
		}
		for _, tab := range state.Tabs {
			if _, ok := byID[tab.TerminalID]; ok {
				ordered = append(ordered, tab)
			}
		}
		state.Tabs = ordered
	}
}
