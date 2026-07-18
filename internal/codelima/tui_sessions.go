package codelima

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/test_lima/internal/codelima/daemon"
	"github.com/brianrackle/test_lima/internal/codelima/daemonclient"
	"github.com/brianrackle/test_lima/internal/codelima/terminal"
)

type tuiSession struct {
	key        string
	target     string
	kind       tuiTreeEntryKind
	label      string
	project    Project
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
	events      *daemonclient.Client
	eventCancel context.CancelFunc

	preferredCols int
	preferredRows int
}

func newTUISessionStore(ctx context.Context, service *Service, postEvent func(vaxis.Event)) *tuiSessionStore {
	store := &tuiSessionStore{
		ctx:       ctx,
		service:   service,
		postEvent: postEvent,
		sessions:  map[string]*tuiSession{},
		targets:   map[terminal.TargetKey]*terminal.TargetTerminalState{},
		registry:  terminal.NewTerminalRuntimeRegistry[tuiTerminal](),
	}
	if service != nil && service.daemonClient != nil {
		if err := store.restoreDaemonSessions(); err != nil {
			service.log().Error("restore daemon terminal tabs failed", "error", err.Error())
		}
		store.startDaemonEvents()
	}
	return store
}

func (s *tuiSessionStore) startDaemonEvents() {
	ctx, cancel := context.WithCancel(s.ctx)
	client, err := daemonclient.Dial(ctx, daemonclient.Options{Home: s.service.cfg.MetadataRoot, Version: Version, Events: true, Timeout: 2 * time.Second})
	if err != nil {
		cancel()
		s.service.log().Error("connect daemon event stream failed", "error", err.Error())
		return
	}
	if err := client.Subscribe(ctx, []string{"terminal", "target", "node", "daemon", "input"}); err != nil {
		_ = client.Close()
		cancel()
		s.service.log().Error("subscribe daemon events failed", "error", err.Error())
		return
	}
	s.events, s.eventCancel = client, cancel
	go func() {
		for {
			event, err := client.NextEvent(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			s.handleDaemonEvent(event)
		}
	}()
}

func (s *tuiSessionStore) handleDaemonEvent(event daemon.Event) {
	data, _ := event.Data.(map[string]any)
	sessionKey, _ := data["tab_id"].(string)
	switch event.Event {
	case "terminal.dirty", "terminal.resized", "target.tabs_changed", "node.status_changed":
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
			s.postEvent(tuiTerminalClosedEvent{TargetKey: sessionKey})
		}
	case "input.revoked":
		clientID, _ := data["client_id"].(string)
		if s.postEvent != nil && s.service != nil && s.service.daemonClient != nil && clientID == s.service.daemonClient.Hello.ClientID {
			s.postEvent(tuiTerminalErrorEvent{Err: errors.New("terminal input ownership was taken by another client")})
		}
	case "daemon.shutdown":
		if s.postEvent != nil {
			s.postEvent(tuiTerminalErrorEvent{Err: errors.New("codelima daemon stopped")})
		}
	}
}

var newSessionTUITerminal = newTUITerminal

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
// project or node target, in the order they were opened.
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

func (s *tuiSessionStore) SetPreferredTerminalSize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}

	s.preferredCols = cols
	s.preferredRows = rows
}

func (s *tuiSessionStore) OpenProjectTab(project Project) (string, error) {
	target := terminal.ProjectTarget(project.ID)
	targetKey := target.String()
	s.ClearSessionError(targetKey)

	spec, err := s.service.TerminalLaunchSpec(target, terminal.ProjectHostShell, project.WorkspacePath)
	if err != nil {
		s.setSessionError(targetKey, err)
		return "", err
	}

	key, err := s.launchTabFromSpec(targetKey, spec, &tuiSession{
		kind:    tuiTreeEntryProject,
		label:   project.Slug,
		project: project,
	}, nil)
	if err != nil {
		s.setSessionError(targetKey, err)
		return "", err
	}
	s.service.log().Debug("terminal opened", "kind", "project", "target", targetKey, "session", key)
	return key, nil
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
		kind:  tuiTreeEntryNode,
		label: node.Slug,
		node:  node,
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
		kind: tuiTreeEntryProject, label: node.Slug + " host", node: node,
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
		kind := terminal.NodeHostShell.String()
		if session.kind == tuiTreeEntryNode {
			kind = terminal.NodeShell.String()
		}
		var state daemon.TerminalState
		err := s.service.daemonClient.Call(s.ctx, "terminal.open", map[string]any{
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
		remote := newDaemonTUITerminal(s.service.daemonClient, state.TerminalID, s.postEvent)
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
	term := newSessionTUITerminal(key, s.postEvent)
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
	if err := s.service.daemonClient.Call(s.ctx, "terminal.list", nil, &states); err != nil {
		return fromDaemonError(err)
	}
	for _, state := range states {
		key := state.TabID
		if key == "" {
			key = state.Target + "#" + state.TerminalID
		}
		kind := tuiTreeEntryNode
		session := &tuiSession{key: key, target: state.Target, label: state.Label, terminalID: terminal.TerminalID(state.TerminalID)}
		target, err := terminal.ParseTargetKey(state.Target)
		if err == nil && target.Kind == terminal.TargetNode {
			if state.Kind == terminal.NodeHostShell.String() {
				kind = tuiTreeEntryProject
			}
			if node, nodeErr := s.service.store.NodeByID(target.ID); nodeErr == nil {
				session.node = node
			}
		}
		session.kind = kind
		remote := newDaemonTUITerminal(s.service.daemonClient, state.TerminalID, s.postEvent)
		if _, ok := s.registry.Register(session.terminalID, remote); !ok {
			remote.Detach()
			continue
		}
		s.putSession(session)
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

func (s *tuiSessionStore) Session(targetKey string) (*tuiSession, bool) {
	session, ok := s.sessions[targetKey]
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
	if s.events != nil {
		_ = s.events.Close()
		s.events = nil
	}
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
