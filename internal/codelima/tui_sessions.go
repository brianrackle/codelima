package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

type tuiSession struct {
	key        string
	target     string
	shellKind  terminal.TerminalKind
	label      string
	node       Node
	terminalID terminal.TerminalID
}

type tuiSessionStore struct {
	ctx       context.Context
	service   *Service
	postEvent func(vaxis.Event)
	sessions  map[string]*tuiSession
	// targets holds per-target tab bookkeeping (ordered tabs, monotonic tab
	// counter, and the last open error). It replaces the former parallel
	// tabCounters/sessionOrder/sessionErrors maps (see ADR 61, Track 1 PR3).
	targets     map[terminal.TargetKey]*terminal.TargetTerminalState
	registry    *terminal.TerminalRuntimeRegistry[tuiTerminal]
	eventMu     sync.Mutex
	events      *daemonclient.Client
	eventCancel context.CancelFunc
	daemonReady atomic.Bool
	// newTerminal builds the terminal backend for locally spawned tabs. It is
	// a per-store field (defaulting to newTUITerminal) rather than a package
	// variable so tests can swap in fakes without racing parallel tests.
	newTerminal func(key string, post func(vaxis.Event)) tuiTerminal

	preferredCols int
	preferredRows int
}

type daemonEventReader interface {
	NextEvent(context.Context) (daemon.Event, error)
}

func runDaemonEventLoop(
	ctx context.Context,
	reader daemonEventReader,
	handle func(daemon.Event),
	reportError func(error),
) {
	for {
		event, err := reader.NextEvent(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			if reportError != nil {
				reportError(err)
			}
			return
		}
		handle(event)
		if event.Event == "daemon.shutdown" || event.Event == "daemon.update_committed" {
			return
		}
	}
}

func newTUISessionStore(ctx context.Context, service *Service, postEvent func(vaxis.Event)) *tuiSessionStore {
	store := &tuiSessionStore{
		ctx:         ctx,
		service:     service,
		postEvent:   postEvent,
		sessions:    map[string]*tuiSession{},
		targets:     map[terminal.TargetKey]*terminal.TargetTerminalState{},
		registry:    terminal.NewTerminalRuntimeRegistry[tuiTerminal](),
		newTerminal: newTUITerminal,
	}
	if service != nil && service.daemonClient != nil {
		store.daemonReady.Store(true)
		if err := store.restoreDaemonSessions(); err != nil {
			service.log().Error("restore daemon terminal tabs failed", "error", err.Error())
		}
		store.startDaemonEvents()
	}
	return store
}

func (s *tuiSessionStore) startDaemonEvents() {
	ctx, cancel := context.WithCancel(s.ctx)
	s.eventCancel = cancel
	clientInstanceID := s.service.daemonClient.HelloSnapshot().ClientID
	go func() {
		_ = runDaemonConnectionSupervisor(ctx, daemonConnectionSupervisorOptions{
			Dial: func(dialCtx context.Context) (daemonEventConnection, error) {
				client, err := daemonclient.Dial(dialCtx, daemonclient.Options{
					Home:             s.service.cfg.MetadataRoot,
					Version:          Version,
					Events:           true,
					Timeout:          2 * time.Second,
					ClientInstanceID: clientInstanceID,
				})
				if err != nil {
					return nil, err
				}
				s.eventMu.Lock()
				s.events = client
				s.eventMu.Unlock()
				return client, nil
			},
			OnSync:  s.prepareDaemonSynchronization,
			OnEvent: s.handleDaemonEvent,
			OnStatus: func(status daemonConnectionStatus) {
				if status.State != daemonConnectionDisconnected {
					return
				}
				s.daemonReady.Store(false)
				s.service.log().Error(
					"daemon event stream disconnected; reconnecting",
					"generation", status.Generation,
					"epoch", status.Epoch,
					"sequence", status.Sequence,
					"error", status.Err,
					"connection_id", status.CloseRecord.ConnectionID,
					"client_instance_id", status.CloseRecord.ClientInstanceID,
					"initiator", status.CloseRecord.Initiator,
					"phase", status.CloseRecord.Phase,
					"reason", status.CloseRecord.Reason,
					"underlying", status.CloseRecord.Underlying,
				)
				if s.postEvent != nil {
					s.postEvent(tuiDaemonDisconnectedEvent{Err: fmt.Errorf("daemon connection lost; reconnecting: %w", status.Err)})
				}
			},
		})
	}()
}

func (s *tuiSessionStore) prepareDaemonSynchronization(ctx context.Context, snapshot daemon.SyncSnapshot) error {
	if s.service == nil || s.service.daemonClient == nil {
		return errors.New("daemon request client is unavailable")
	}
	requestClient := s.service.daemonClient
	hello := requestClient.HelloSnapshot()
	if hello.DaemonEpoch != snapshot.DaemonEpoch {
		if err := requestClient.Reconnect(ctx); err != nil {
			return fmt.Errorf("reconnect daemon request stream: %w", err)
		}
	} else {
		var status daemon.Status
		pingCtx, cancel := context.WithTimeout(ctx, daemonRPCTimeout)
		err := requestClient.Call(pingCtx, "daemon.ping", nil, &status)
		cancel()
		if err != nil {
			if reconnectErr := requestClient.Reconnect(ctx); reconnectErr != nil {
				return fmt.Errorf("reconnect daemon request stream after failed ping: %w", reconnectErr)
			}
		}
	}
	if err := takeTUIDaemonInput(ctx, requestClient); err != nil {
		return fmt.Errorf("reclaim daemon input after reconnect: %w", err)
	}
	if s.postEvent == nil {
		return errors.New("TUI event sink is unavailable during daemon synchronization")
	}
	applied := make(chan error, 1)
	s.postEvent(tuiDaemonSynchronizedEvent{Snapshot: snapshot, applied: applied})
	select {
	case err := <-applied:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Call is the reconnect-aware RPC seam used by daemon terminal adapters. It
// rejects input and snapshots until the authoritative sync has been installed
// on the TUI actor, preventing accidental mutation of a stale session.
func (s *tuiSessionStore) Call(ctx context.Context, method string, params any, result any) error {
	if !s.daemonReady.Load() {
		return &daemonclient.DeliveryError{
			Outcome: daemonclient.DeliveryNotSent,
			Err:     errors.New("daemon connection is synchronizing"),
		}
	}
	if s.service == nil || s.service.daemonClient == nil {
		return errors.New("daemon request client is unavailable")
	}
	return s.service.daemonClient.Call(ctx, method, params, result)
}

func (s *tuiSessionStore) handleDaemonEvent(event daemon.Event) {
	data, _ := event.Data.(map[string]any)
	sessionKey, _ := data["tab_id"].(string)
	switch event.Event {
	case "terminal.dirty", "terminal.resized":
		terminalID, _ := data["terminal_id"].(string)
		if terminalID != "" && s.postEvent != nil {
			s.postEvent(tuiDaemonTerminalDirtyEvent{TerminalID: terminalID})
		}
	case "target.tabs_changed", "node.status_changed":
		if s.postEvent != nil {
			s.postEvent(vaxis.Redraw{})
		}
	case "terminal.clipboard":
		text, _ := data["text"].(string)
		if s.postEvent != nil {
			s.postEvent(tuiClipboardEvent{TargetKey: sessionKey, Text: text})
		}
	case "terminal.closed":
		if sessionKey != "" && s.postEvent != nil {
			s.postEvent(tuiTerminalClosedEvent{SessionKey: sessionKey})
		}
	case "daemon.shutdown", "daemon.update_committed":
		// The connection supervisor treats these as intentional reconnect
		// boundaries and reports the resulting physical-link transition once.
	}
}

func (s *tuiSessionStore) HasSession(sessionKey string) bool {
	_, ok := s.sessions[sessionKey]
	return ok
}

// formatSessionKey renders a terminal tab (session) key for a target as
// "<targetKey>#<n>". The "#n" suffix is purely a per-target ordering/display
// discriminator; nothing ever parses it back into a target (session→target
// resolution uses the stored session.target field — see ADR 61). Production
// (nextSessionKey) and the test fake share this single formatter.
func formatSessionKey(targetKey string, counter int) string {
	return fmt.Sprintf("%s#%d", targetKey, counter)
}

// targetState returns the per-target bookkeeping for targetKey, creating it on
// first use. targetKey is always a valid "project:"/"node:" string at call
// sites (built via TargetKey.String()); an unparseable key yields a throwaway
// state so callers still get a usable value rather than a nil.
func (s *tuiSessionStore) targetState(targetKey string) *terminal.TargetTerminalState {
	tk, err := terminal.ParseTargetKey(targetKey)
	if err != nil {
		return &terminal.TargetTerminalState{}
	}
	st, ok := s.targets[tk]
	if !ok {
		st = &terminal.TargetTerminalState{Target: tk}
		s.targets[tk] = st
	}
	return st
}

// lookupTargetState returns the existing per-target bookkeeping for targetKey
// without creating it.
func (s *tuiSessionStore) lookupTargetState(targetKey string) (*terminal.TargetTerminalState, bool) {
	tk, err := terminal.ParseTargetKey(targetKey)
	if err != nil {
		return nil, false
	}
	st, ok := s.targets[tk]
	return st, ok
}

// nextSessionKey allocates a unique tab key for the target. Each explicit
// open-tab command produces a fresh session keyed "<target>#<n>" from the
// target's monotonic tab counter.
func (s *tuiSessionStore) nextSessionKey(targetKey string) string {
	return formatSessionKey(targetKey, s.targetState(targetKey).AllocateTabIndex())
}

// TargetSessionKeys lists the open terminal tabs that belong to a single
// project or node target, in their current display order.
func (s *tuiSessionStore) TargetSessionKeys(targetKey string) []string {
	if targetKey == "" {
		return nil
	}
	st, ok := s.lookupTargetState(targetKey)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(st.Tabs))
	for _, id := range st.TabIDs() {
		keys = append(keys, string(id))
	}
	return keys
}

// MoveTab moves one terminal tab one position left or right within its target.
// The daemon owns durable ordering when present; the local state changes only
// after that mutation succeeds.
func (s *tuiSessionStore) MoveTab(targetKey, sessionKey string, direction int) error {
	if direction != -1 && direction != 1 {
		return fmt.Errorf("terminal tab move direction must be -1 or 1")
	}
	session := s.sessions[sessionKey]
	if session == nil || session.target != targetKey {
		return fmt.Errorf("terminal tab is not open for the focused item")
	}
	st, ok := s.lookupTargetState(targetKey)
	if !ok || !st.HasTab(terminal.TabID(sessionKey)) {
		return fmt.Errorf("terminal tab is not open for the focused item")
	}

	if s.service != nil && s.service.daemonClient != nil {
		if err := s.Call(s.ctx, "terminal.move", map[string]any{
			"terminal_id": string(session.terminalID),
			"delta":       direction,
		}, nil); err != nil {
			return fromDaemonError(err)
		}
	}
	st.MoveTab(terminal.TabID(sessionKey), direction)
	return nil
}

func (s *tuiSessionStore) SetPreferredTerminalSize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}

	s.preferredCols = cols
	s.preferredRows = rows
}

func (s *tuiSessionStore) OpenNodeTab(node Node) (string, error) {
	target := terminal.NodeTarget(node.ID)
	targetKey := target.String()
	s.ClearSessionError(targetKey)

	spec, err := s.service.TerminalLaunchSpec(target, terminal.NodeShell, "")
	if err != nil {
		s.setSessionError(targetKey, err)
		return "", err
	}

	key, err := s.launchTabFromSpec(targetKey, spec, &tuiSession{
		shellKind: terminal.NodeShell,
		label:     node.Slug,
		node:      node,
	}, func(startErr error) error {
		return nodeTabStartError(spec.Argv[0], startErr)
	})
	if err != nil {
		s.setSessionError(targetKey, err)
		return "", err
	}
	s.service.log().Debug("terminal opened", "kind", "node", "target", targetKey, "session", key)
	return key, nil
}

func (s *tuiSessionStore) OpenNodeHostTab(node Node) (string, error) {
	target := terminal.NodeTarget(node.ID)
	targetKey := target.String()
	s.ClearSessionError(targetKey)
	spec, err := s.service.TerminalLaunchSpec(target, terminal.NodeHostShell, node.DirectoryPath)
	if err != nil {
		s.setSessionError(targetKey, err)
		return "", err
	}
	key, err := s.launchTabFromSpec(targetKey, spec, &tuiSession{
		shellKind: terminal.NodeHostShell, label: node.Slug + " host", node: node,
	}, nil)
	if err != nil {
		s.setSessionError(targetKey, err)
		return "", err
	}
	s.service.log().Debug("terminal opened", "kind", "node-host", "target", targetKey, "session", key)
	return key, nil
}

// launchTabFromSpec spawns a terminal for an already-built LaunchSpec, registers
// its runtime, and records the session. It is the single spawn path shared by
// both open flows: the LaunchSpec is the one shell-launch contract (built by
// Service.TerminalLaunchSpec) and this method only turns it into a running
// child. It does not touch the target error record — callers own that
// (ClearSessionError/setSessionError) so both flows keep identical bookkeeping.
// wrapStartErr, when non-nil, decorates a Start failure before it is returned.
func (s *tuiSessionStore) launchTabFromSpec(targetKey string, spec LaunchSpec, session *tuiSession, wrapStartErr func(error) error) (string, error) {
	if s.service.daemonClient != nil {
		kind := session.shellKind.String()
		var state daemon.TerminalState
		err := s.Call(s.ctx, "terminal.open", map[string]any{
			"target": targetKey,
			"kind":   kind,
			"label":  session.label,
			"cols":   s.preferredCols,
			"rows":   s.preferredRows,
		}, &state)
		if err != nil {
			return "", fromDaemonError(err)
		}
		key := state.TabID
		if key == "" {
			key = formatSessionKey(targetKey, s.targetState(targetKey).AllocateTabIndex())
		}
		remote := newDaemonTUITerminal(s, state.TerminalID, s.postEvent)
		runtime, ok := s.registry.Register(terminal.TerminalID(state.TerminalID), remote)
		if !ok {
			remote.Detach()
			return "", fmt.Errorf("daemon returned duplicate terminal id %q", state.TerminalID)
		}
		session.key = key
		session.target = targetKey
		session.terminalID = runtime.ID
		s.putSession(session)
		return key, nil
	}

	command := exec.CommandContext(s.ctx, spec.Argv[0], spec.Argv[1:]...)
	command.Env = spec.Env
	command.Dir = spec.Dir

	key := s.nextSessionKey(targetKey)
	term := s.newTerminal(key, s.postEvent)
	if s.preferredCols > 0 && s.preferredRows > 0 {
		term.Resize(s.preferredCols, s.preferredRows)
	}
	if err := term.Start(command); err != nil {
		if wrapStartErr != nil {
			err = wrapStartErr(err)
		}
		return "", err
	}

	runtime := s.registry.Allocate(term)
	session.key = key
	session.target = targetKey
	session.terminalID = runtime.ID
	s.putSession(session)
	return key, nil
}

func (s *tuiSessionStore) restoreDaemonSessions() error {
	var states []daemon.TerminalState
	if err := s.Call(s.ctx, "terminal.list", nil, &states); err != nil {
		return fromDaemonError(err)
	}
	return s.replaceDaemonSessions(states)
}

func (s *tuiSessionStore) applyDaemonSynchronization(snapshot daemon.SyncSnapshot) error {
	var state struct {
		Session           daemon.Session             `json:"session"`
		TerminalSnapshots map[string]daemon.Snapshot `json:"terminal_snapshots"`
	}
	if err := json.Unmarshal(snapshot.State, &state); err != nil {
		return fmt.Errorf("decode daemon synchronization: %w", err)
	}
	if state.Session.Version != daemon.SessionVersion {
		return fmt.Errorf("daemon synchronization session version %d, want %d", state.Session.Version, daemon.SessionVersion)
	}
	if err := s.replaceDaemonSessions(state.Session.Terminals); err != nil {
		return err
	}
	for terminalID, snapshot := range state.TerminalSnapshots {
		runtime, ok := s.registry.Lookup(terminal.TerminalID(terminalID))
		if !ok {
			continue
		}
		if view, ok := runtime.Backend.(*daemonTUITerminal); ok {
			view.installSnapshot(snapshot)
		}
	}
	s.daemonReady.Store(true)
	return nil
}

func (s *tuiSessionStore) replaceDaemonSessions(states []daemon.TerminalState) error {
	existingByTerminal := make(map[terminal.TerminalID]*tuiSession, len(s.sessions))
	for _, session := range s.sessions {
		existingByTerminal[session.terminalID] = session
	}
	keep := make(map[terminal.TerminalID]struct{}, len(states))

	for _, state := range states {
		key := state.TabID
		if key == "" {
			key = state.Target + "#" + state.TerminalID
		}
		shellKind := terminal.NodeShell
		terminalID := terminal.TerminalID(state.TerminalID)
		keep[terminalID] = struct{}{}
		session := existingByTerminal[terminalID]
		if session == nil {
			session = &tuiSession{terminalID: terminalID}
		} else if session.key != key {
			delete(s.sessions, session.key)
		}
		session.key, session.target, session.label = key, state.Target, state.Label
		target, err := terminal.ParseTargetKey(state.Target)
		if err == nil && target.Kind == terminal.TargetNode {
			if state.Kind == terminal.NodeHostShell.String() {
				shellKind = terminal.NodeHostShell
			}
			if node, nodeErr := s.service.store.NodeByID(target.ID); nodeErr == nil {
				session.node = node
			}
		}
		session.shellKind = shellKind
		if _, ok := s.registry.Lookup(terminalID); !ok {
			remote := newDaemonTUITerminal(s, state.TerminalID, s.postEvent)
			if _, registered := s.registry.Register(terminalID, remote); !registered {
				remote.Detach()
				return fmt.Errorf("register synchronized daemon terminal %q", state.TerminalID)
			}
		}
		s.sessions[key] = session
	}

	for key, session := range s.sessions {
		if _, ok := keep[session.terminalID]; ok {
			continue
		}
		delete(s.sessions, key)
		if runtime, ok := s.registry.Remove(session.terminalID); ok {
			if detachable, ok := runtime.Backend.(interface{ Detach() }); ok {
				detachable.Detach()
			}
		}
	}

	for _, targetState := range s.targets {
		targetState.Tabs = nil
	}
	for _, state := range states {
		key := state.TabID
		if key == "" {
			key = state.Target + "#" + state.TerminalID
		}
		s.targetState(state.Target).AppendTab(terminal.TerminalTabState{
			ID:         terminal.TabID(key),
			Label:      state.Label,
			TerminalID: terminal.TerminalID(state.TerminalID),
		})
	}
	return nil
}

func resolveCodelimaExecutablePath(executable string) string {
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		return resolved
	}
	return executable
}

func nodeTabStartError(executable string, err error) error {
	if errors.Is(err, syscall.ENOEXEC) {
		return fmt.Errorf("binary at %q is not compatible with this platform; run make build on this platform and restart codelima: %w", executable, err)
	}
	return fmt.Errorf("start node shell with codelima executable %q: %w", executable, err)
}

func (s *tuiSessionStore) putSession(session *tuiSession) {
	if session == nil || session.key == "" {
		return
	}
	s.targetState(session.target).AppendTab(terminal.TerminalTabState{
		ID:         terminal.TabID(session.key),
		Label:      session.label,
		TerminalID: session.terminalID,
	})
	s.sessions[session.key] = session
}

func (s *tuiSessionStore) Session(sessionKey string) (*tuiSession, bool) {
	session, ok := s.sessions[sessionKey]
	return session, ok
}

func (s *tuiSessionStore) SessionError(targetKey string) error {
	if st, ok := s.lookupTargetState(targetKey); ok {
		return st.OpenError
	}
	return nil
}

// setSessionError records the last open failure for a target.
func (s *tuiSessionStore) setSessionError(targetKey string, err error) {
	s.targetState(targetKey).OpenError = err
}

// ClearSessionError drops any recorded open error for a target. It is the
// sanctioned accessor so callers do not reach into the per-target state.
func (s *tuiSessionStore) ClearSessionError(targetKey string) {
	if st, ok := s.lookupTargetState(targetKey); ok {
		st.OpenError = nil
	}
}

// terminalFor resolves a session's live terminal backend through the runtime
// registry. It returns false when the session is nil or its runtime is gone.
func (s *tuiSessionStore) terminalFor(session *tuiSession) (tuiTerminal, bool) {
	if session == nil {
		return nil, false
	}
	runtime, ok := s.registry.Lookup(session.terminalID)
	if !ok {
		return nil, false
	}
	return runtime.Backend, true
}

// SessionTerminal resolves the live terminal backend for a session key. It is
// the sanctioned accessor so callers do not reach into the sessions map or the
// session's terminal handle.
func (s *tuiSessionStore) SessionTerminal(sessionKey string) (tuiTerminal, bool) {
	session, ok := s.sessions[sessionKey]
	if !ok {
		return nil, false
	}
	return s.terminalFor(session)
}

type daemonSnapshotView interface {
	markSnapshotDirty()
	requestSnapshot()
}

// markDaemonTerminalDirty runs on the TUI event loop. Hidden tabs retain only
// a dirty bit; the visible tab pulls a coalesced snapshot immediately. A hidden
// tab catches up when Draw requests its pending snapshot after a tab switch.
func (s *tuiSessionStore) markDaemonTerminalDirty(terminalID, activeSessionKey string) {
	runtime, ok := s.registry.Lookup(terminal.TerminalID(terminalID))
	if !ok {
		return
	}
	view, ok := runtime.Backend.(daemonSnapshotView)
	if !ok {
		return
	}
	view.markSnapshotDirty()
	active := s.sessions[activeSessionKey]
	if active != nil && active.terminalID == runtime.ID {
		view.requestSnapshot()
	}
}

// SyncFocus focuses the terminal for activeSessionKey when focusActive is set
// and blurs every other open terminal. It owns the sessions-map iteration so
// the app does not reach into store internals.
func (s *tuiSessionStore) SyncFocus(activeSessionKey string, focusActive bool) {
	for sessionKey, session := range s.sessions {
		term, ok := s.terminalFor(session)
		if !ok {
			continue
		}
		if focusActive && sessionKey == activeSessionKey {
			term.Focus()
			continue
		}
		term.Blur()
	}
}

// PruneStaleSessions closes every open session whose target no longer exists
// (per keep) and drops every recorded target error whose target is likewise
// gone. It owns the per-target iteration so the app does not reach into store
// internals.
func (s *tuiSessionStore) PruneStaleSessions(keep func(targetKey string) bool) {
	var orphans []string
	for sessionKey, session := range s.sessions {
		if !keep(session.target) {
			orphans = append(orphans, sessionKey)
		}
	}
	for _, sessionKey := range orphans {
		s.CloseSession(sessionKey)
	}
	for tk, st := range s.targets {
		if st.OpenError != nil && !keep(tk.String()) {
			st.OpenError = nil
		}
	}
}

func (s *tuiSessionStore) RemoveSession(sessionKey string) (*tuiSession, bool) {
	session := s.sessions[sessionKey]
	if session == nil {
		return nil, false
	}
	delete(s.sessions, sessionKey)
	s.removeTab(session)
	// The terminal exited on its own (finish path); just forget the runtime.
	s.registry.Remove(session.terminalID)
	return session, true
}

func (s *tuiSessionStore) Close() {
	if s.eventCancel != nil {
		s.eventCancel()
	}
	s.eventMu.Lock()
	if s.events != nil {
		_ = s.events.Close()
		s.events = nil
	}
	s.eventMu.Unlock()
	for sessionKey, session := range s.sessions {
		if runtime, ok := s.registry.Remove(session.terminalID); ok {
			if detachable, ok := runtime.Backend.(interface{ Detach() }); ok {
				detachable.Detach()
			} else {
				runtime.Backend.Close()
			}
		}
		s.service.log().Debug("terminal closed", "target", session.target, "session", sessionKey, "reason", "shutdown")
		delete(s.sessions, sessionKey)
	}
	for _, st := range s.targets {
		st.Tabs = nil
	}
}

func (s *tuiSessionStore) CloseSession(sessionKey string) {
	session, ok := s.sessions[sessionKey]
	if !ok {
		return
	}

	delete(s.sessions, sessionKey)
	s.removeTab(session)
	if runtime, ok := s.registry.Remove(session.terminalID); ok {
		runtime.Backend.Close()
	}
	s.service.log().Debug("terminal closed", "target", session.target, "session", sessionKey, "reason", "tab-close")
}

// CloseTargetSessions closes every open terminal tab for a project or node
// target and clears the target's recorded open error.
func (s *tuiSessionStore) CloseTargetSessions(targetKey string) {
	for _, sessionKey := range s.TargetSessionKeys(targetKey) {
		s.CloseSession(sessionKey)
	}
	s.ClearSessionError(targetKey)
}

func (s *tuiSessionStore) CloseNode(nodeID string) {
	s.CloseTargetSessions(terminal.NodeTarget(nodeID).String())
}

// removeTab drops a session's tab from its target's ordered tab list.
func (s *tuiSessionStore) removeTab(session *tuiSession) {
	if st, ok := s.lookupTargetState(session.target); ok {
		st.RemoveTab(terminal.TabID(session.key))
	}
}
