package codelima

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

type tuiActivityTerminalEvent struct {
	serial uint64
	rowID  string
	state  daemon.TerminalState
	err    error
}

func resolveActivityTerminal(ctx context.Context, client daemonRPCCaller, nodeID, terminalID string) (daemon.TerminalState, error) {
	var sessions []daemon.TerminalState
	if err := client.Call(ctx, "terminal.list", nil, &sessions); err != nil {
		return daemon.TerminalState{}, err
	}
	for _, state := range sessions {
		if state.TerminalID == terminalID && state.Target == terminal.NodeTarget(nodeID).String() && (state.Kind == terminal.NodeShell.String() || state.Kind == terminal.NodeHostShell.String()) {
			return state, nil
		}
	}
	return daemon.TerminalState{}, errors.New("that session has ended; its Activity history is retained")
}

func (a *vaxisTUIApp) startActivityTerminal(row tuiActivityRow) {
	if a.service == nil || a.service.daemonClient == nil || a.daemonDisconnected {
		a.setStatus(slog.LevelError, "Session attachment requires a daemon connection")
		return
	}
	a.cancelActivityAttachment()
	if a.rawShellPending {
		return
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	a.activity.attachCancel, a.rawShellPending = cancel, true
	serial, client := a.activity.attachSerial, a.service.daemonClient
	a.setFooterStatus(slog.LevelInfo, "Opening recorded session…")
	go func() {
		state, err := resolveActivityTerminal(ctx, client, row.nodeID, row.terminalID)
		a.postCompletion(tuiActivityTerminalEvent{serial: serial, rowID: row.id, state: state, err: err})
	}()
}

func (a *vaxisTUIApp) cancelActivityAttachment() {
	a.activity.attachSerial++
	if cancel := a.activity.attachCancel; cancel != nil {
		cancel()
		a.activity.attachCancel, a.rawShellPending = nil, false
	}
}

func (a *vaxisTUIApp) finishActivityTerminal(event tuiActivityTerminalEvent) {
	if event.serial != a.activity.attachSerial || a.activity.attachCancel == nil {
		return
	}
	a.cancelActivityAttachment()
	if a.view != tuiViewActivity || a.activity.selected != event.rowID {
		return
	}
	a.finishRawShell(tuiRawShellReadyEvent{State: event.state, Err: event.err, Mode: "attach"})
}
