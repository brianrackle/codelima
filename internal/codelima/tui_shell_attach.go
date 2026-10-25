//go:build darwin || linux

package codelima

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

type tuiRawShellReadyEvent struct {
	State daemon.TerminalState
	Err   error
	Mode  string
}

func (s *Service) attachNodeShell(ctx context.Context, value string) error {
	if err := s.ensureReadyForWrite(ctx); err != nil {
		return err
	}
	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return err
	}
	client, err := s.connectTUIDaemon(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	session, err := resolveRawShell(ctx, client, node, false, false, "")
	if err != nil {
		return err
	}
	_, err = attachDaemonTerminal(ctx, client, session.TerminalID)
	return err
}

// resolveRawShell reuses a daemon session by identity. A fresh session is
// created only for an explicit new-shell action or when none exists.
func resolveRawShell(ctx context.Context, client *daemonclient.Client, node Node, host, fresh bool, sessionID string) (daemon.TerminalState, error) {
	if client == nil {
		return daemon.TerminalState{}, errors.New("shell attachment requires the daemon")
	}
	kind := terminal.NodeShell.String()
	if host {
		kind = terminal.NodeHostShell.String()
	}
	var sessions []daemon.TerminalState
	if err := client.Call(ctx, "terminal.list", nil, &sessions); err != nil {
		return daemon.TerminalState{}, err
	}
	for _, session := range sessions {
		if session.Target != terminal.NodeTarget(node.ID).String() {
			continue
		}
		if (sessionID != "" && session.TerminalID == sessionID) || (sessionID == "" && !fresh && session.Kind == kind) {
			return session, nil
		}
	}
	if sessionID != "" {
		return daemon.TerminalState{}, errors.New("that session has ended")
	}
	var session daemon.TerminalState
	err := client.Call(ctx, "terminal.open", terminalOpenParams{Target: terminal.NodeTarget(node.ID).String(), Kind: kind}, &session)
	return session, err
}

func (a *vaxisTUIApp) startRawShell(host, fresh bool, sessionID string) error {
	return a.startShellMode("attach", host, fresh, sessionID)
}

func (a *vaxisTUIApp) startShellMode(mode string, host, fresh bool, sessionID string) error {
	if mode == "embedded" {
		return a.openEmbeddedShell(host)
	}
	entry := a.state.selectedEntry()
	if !entry.valid() {
		return errors.New("select a node first")
	}
	if !host && !nodeGuestShellReady(entry.node) {
		return errors.New(nodeGuestShellUnavailable(entry.node, "attach"))
	}
	if a.rawShellPending {
		return errors.New("a shell is already opening")
	}
	if a.service == nil || a.service.daemonClient == nil {
		return errors.New("shell attachment requires the daemon")
	}
	cfg := a.managerSettings()
	if err := checkShellLaunch(cfg, mode); err != nil {
		return err
	}
	a.rawShellPending = true
	client, node, ctx := a.service.daemonClient, entry.node, a.ctx
	a.setStatus(slog.LevelInfo, "Opening shell · "+node.Slug)
	go func() {
		state, err := resolveRawShell(ctx, client, node, host, fresh, sessionID)
		if err == nil && mode != "attach" {
			err = launchManagedShell(ctx, cfg, mode, state.TerminalID, node.DirectoryPath, node.Slug)
		}
		a.postCompletion(tuiRawShellReadyEvent{State: state, Err: err, Mode: mode})
	}()
	return nil
}

func (a *vaxisTUIApp) finishRawShell(event tuiRawShellReadyEvent) {
	a.rawShellPending = false
	if event.Err != nil {
		a.setStatus(slog.LevelError, event.Err.Error())
		return
	}
	if err := a.installCreatedDaemonTab(tuiDaemonTerminalCreatedEvent(event.State)); err != nil {
		a.setStatus(slog.LevelError, err.Error())
		return
	}
	if a.overviewVisible() && event.State.Target == terminal.NodeTarget(a.state.selectedEntry().node.ID).String() {
		a.reconcileOverview(a.state.selectedEntry().node)
		for _, key := range a.sessions.TargetSessionKeys(event.State.Target) {
			if session, ok := a.sessions.Session(key); ok && string(session.terminalID) == event.State.TerminalID {
				a.overview.session = key
				break
			}
		}
	}
	if event.Mode == "window" || event.Mode == "tmux" {
		a.setStatus(slog.LevelInfo, "Opened "+event.State.Label+" in "+event.Mode+" · session belongs to the daemon")
		return
	}
	if a.vx == nil {
		a.setStatus(slog.LevelError, "interactive terminal is unavailable")
		return
	}
	if err := a.vx.Suspend(); err != nil {
		a.setStatus(slog.LevelError, err.Error())
		return
	}
	leftRunning, err := attachDaemonTerminal(a.ctx, a.service.daemonClient, event.State.TerminalID)
	resumeErr := a.vx.Resume()
	var rpcErr *daemon.RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == daemon.CodeNotFound {
		// A close event can overtake the asynchronous open reply. Do not keep
		// the optimistically adopted tab when the daemon proves it has ended.
		a.handleTerminalClosed(tuiTerminalClosedEvent{SessionKey: event.State.TabID})
	}
	if err = errors.Join(err, resumeErr); err != nil {
		a.setStatus(slog.LevelError, "Shell attachment: "+err.Error())
	} else if leftRunning {
		a.setStatus(slog.LevelInfo, fmt.Sprintf("Detached from %s · session keeps running", event.State.Label))
	} else {
		a.handleTerminalClosed(tuiTerminalClosedEvent{SessionKey: event.State.TabID})
		a.setStatus(slog.LevelInfo, "Session ended · "+event.State.Label)
	}
	a.state.focusTree()
	a.syncSessionFocus()
	a.startDataRefresh("")
}

func (a *vaxisTUIApp) installCreatedDaemonTab(event tuiDaemonTerminalCreatedEvent) error {
	state := daemon.TerminalState(event)
	target, err := terminal.ParseTargetKey(state.Target)
	if err != nil {
		return err
	}
	kind := terminal.NodeShell
	if state.Kind == terminal.NodeHostShell.String() {
		kind = terminal.NodeHostShell
	}
	node := a.state.nodesByID[target.ID]
	_, err = a.sessions.installDaemonTab(tuiDaemonTerminalOpenedEvent{State: state, TargetKey: state.Target, Label: state.Label, ShellKind: kind, Node: node})
	return err
}
