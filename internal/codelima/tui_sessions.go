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

// tuiDaemonEventReadTimeout is the read deadline on the daemon event stream.
// It carries 3x the daemon's 2s heartbeat interval so a briefly busy daemon
// cannot trip a routine timeout and a full-resync storm (invariant I5).
const tuiDaemonEventReadTimeout = 6 * time.Second

// tuiDaemonSyncApplyTimeout bounds how long the reconnect supervisor waits for
// the event loop to install one authoritative synchronization. Without it a
// dropped tuiDaemonSynchronizedEvent parks the supervisor forever and every
// terminal RPC keeps failing with "daemon connection is synchronizing".
const tuiDaemonSyncApplyTimeout = 10 * time.Second

// tuiTerminalOpenTimeout bounds one off-loop terminal.open and, with it, how
// long a target reports a pending tab open.
const tuiTerminalOpenTimeout = 10 * time.Second

// tuiTerminalRestoreTimeout bounds the startup adoption of the daemon's
// existing tabs so an unresponsive daemon cannot hang TUI startup.
const tuiTerminalRestoreTimeout = 10 * time.Second

// tuiSessionCloseTimeout caps the wall time quit spends on terminal teardown. A
// terminal that outlives it is left to the process exit rather than holding the
// TUI open.
const tuiSessionCloseTimeout = 5 * time.Second

// tuiNodeChangeDebounce collapses a burst of daemon node-change pushes into a
// single node reload. One lifecycle command against several nodes emits one
// push each, and reloading per push would rebuild the once-per-second polling
// the pushes replaced.
const tuiNodeChangeDebounce = 250 * time.Millisecond

type tuiSessionStore struct {
	ctx       context.Context
	service   *Service
	postEvent func(vaxis.Event)
	// postEventBlocking delivers one-shot completion and handshake events that
	// must not be dropped when the vaxis queue is full. nil falls back to
	// postEvent.
	postEventBlocking func(vaxis.Event)
	sessions          map[string]*tuiSession
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
	// pendingOpens maps a target to the deadlines of its in-flight daemon
	// terminal.open requests. It keeps the once-per-second implicit open from
	// stacking requests, and the deadlines keep a lost reply from suppressing
	// opens for that target forever.
	pendingOpens map[string][]time.Time
	// syncApplyTimeout bounds awaitDaemonSynchronization and restoreTimeout
	// bounds the startup tab adoption. Both are per-store fields (defaulting to
	// the package constants) so fault-injection tests can shorten them without
	// racing parallel tests.
	syncApplyTimeout time.Duration
	restoreTimeout   time.Duration

	// nodeChange holds the debounce timer that collapses daemon node-change
	// pushes into one reload request. The timer is owned by this store and
	// stopped (and latched closed) in Close, so nothing it schedules outlives
	// the store. nodeChangeDebounce is per-store so tests can shorten it.
	nodeChangeMu       sync.Mutex
	nodeChangeTimer    *time.Timer
	nodeChangeClosed   bool
	nodeChangeDebounce time.Duration

	preferredCols int
	preferredRows int
}

// postCompletion delivers a one-shot completion or handshake event through the
// blocking sink when the store has one.
func (s *tuiSessionStore) postCompletion(event vaxis.Event) {
	if s.postEventBlocking != nil {
		s.postEventBlocking(event)
		return
	}
	if s.postEvent != nil {
		s.postEvent(event)
	}
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
		if event.Event == daemon.EventDaemonShutdown || event.Event == daemon.EventDaemonUpdateCommitted {
			return
		}
	}
}

func newTUISessionStore(ctx context.Context, service *Service, postEvent func(vaxis.Event)) *tuiSessionStore {
	store := &tuiSessionStore{
		ctx:                ctx,
		service:            service,
		postEvent:          postEvent,
		sessions:           map[string]*tuiSession{},
		targets:            map[terminal.TargetKey]*terminal.TargetTerminalState{},
		registry:           terminal.NewTerminalRuntimeRegistry[tuiTerminal](),
		newTerminal:        newTUITerminal,
		pendingOpens:       map[string][]time.Time{},
		syncApplyTimeout:   tuiDaemonSyncApplyTimeout,
		restoreTimeout:     tuiTerminalRestoreTimeout,
		nodeChangeDebounce: tuiNodeChangeDebounce,
	}
	if service != nil && service.daemonClient != nil {
		store.daemonReady.Store(true)
		store.restoreDaemonSessions()
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
					Timeout:          tuiDaemonEventReadTimeout,
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
	return s.awaitDaemonSynchronization(ctx, snapshot)
}

// awaitDaemonSynchronization hands one authoritative snapshot to the TUI event
// loop, which owns the session map, and waits for it to be installed. The wait
// is bounded: a dropped handshake must fail this attempt so the supervisor
// redials, never park it forever with every terminal RPC rejected as "daemon
// connection is synchronizing" (invariant I2).
func (s *tuiSessionStore) awaitDaemonSynchronization(ctx context.Context, snapshot daemon.SyncSnapshot) error {
	if s.postEvent == nil {
		return errors.New("TUI event sink is unavailable during daemon synchronization")
	}

	timeout := s.syncApplyTimeout
	if timeout <= 0 {
		timeout = tuiDaemonSyncApplyTimeout
	}
	applied := make(chan error, 1)
	s.postCompletion(tuiDaemonSynchronizedEvent{Snapshot: snapshot, applied: applied})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-applied:
		return err
	case <-timer.C:
		return errors.New("timed out installing daemon synchronization on the TUI event loop")
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

// handleDaemonEvent turns one pushed daemon event into a TUI event. Every
// payload is read through daemon.DecodeEventData into the struct the daemon
// package declares for that event, rather than by pulling keys out of the
// generic map the envelope decodes to: the daemon owns the wire shape, so it
// owns the reader, and a renamed field becomes a compile error here instead of
// a silently empty string.
func (s *tuiSessionStore) handleDaemonEvent(event daemon.Event) {
	if s.postEvent == nil {
		return
	}
	switch event.Event {
	case daemon.EventTerminalDirty:
		if dirty, ok := daemon.DecodeEventData[daemon.TerminalDirtyEvent](event.Data); ok && dirty.TerminalID != "" {
			s.postEvent(tuiDaemonTerminalDirtyEvent{TerminalID: dirty.TerminalID})
		}
	case daemon.EventTerminalResized:
		// A resize publishes the whole terminal record; only the identity is
		// wanted here, because the geometry the TUI draws comes from the
		// snapshot the dirty repaint pulls.
		if state, ok := daemon.DecodeEventData[daemon.TerminalState](event.Data); ok && state.TerminalID != "" {
			s.postEvent(tuiDaemonTerminalDirtyEvent{TerminalID: state.TerminalID})
		}
	case daemon.EventTargetTabsChanged:
		s.postEvent(vaxis.Redraw{})
	case daemon.EventNodeStatusChanged:
		// A redraw alone would render the same stale list: the node records
		// live in the daemon, so the change is only visible after a reload.
		// This is the push that lets the fallback poll run at 10s.
		s.scheduleNodeRefresh()
	case daemon.EventNodeUsageChanged:
		// Usage is a per-node value the daemon samples once a second. It is
		// applied in place on the event loop — deliberately not through
		// scheduleNodeRefresh, which would put a node.list round trip on a 1Hz
		// timer and undo the point of the push.
		if usage, ok := decodeDaemonNodeUsage(event.Data); ok {
			s.postEvent(tuiNodeUsageEvent{Usage: usage})
		}
	case daemon.EventTerminalClipboard:
		if clip, ok := daemon.DecodeEventData[daemon.TerminalClipboardEvent](event.Data); ok {
			s.postEvent(tuiClipboardEvent{TargetKey: clip.TabID, Text: clip.Text})
		}
	case daemon.EventTerminalClosed:
		if state, ok := daemon.DecodeEventData[daemon.TerminalState](event.Data); ok && state.TabID != "" {
			s.postEvent(tuiTerminalClosedEvent{SessionKey: state.TabID})
		}
	case daemon.EventDaemonShutdown, daemon.EventDaemonUpdateCommitted:
		// The connection supervisor treats these as intentional reconnect
		// boundaries and reports the resulting physical-link transition once.
	}
}

// scheduleNodeRefresh posts one tuiNodesChangedEvent after the debounce
// window, so every push that arrives inside it joins the same reload. The
// timer is trailing-edge on purpose: a lifecycle command that touches several
// nodes finishes emitting before the reload runs, so the reload observes the
// settled state instead of racing the middle of the burst.
func (s *tuiSessionStore) scheduleNodeRefresh() {
	if s.postEvent == nil {
		return
	}
	debounce := s.nodeChangeDebounce
	if debounce <= 0 {
		debounce = tuiNodeChangeDebounce
	}

	s.nodeChangeMu.Lock()
	defer s.nodeChangeMu.Unlock()
	if s.nodeChangeClosed || s.nodeChangeTimer != nil {
		return
	}
	s.nodeChangeTimer = time.AfterFunc(debounce, func() {
		s.nodeChangeMu.Lock()
		s.nodeChangeTimer = nil
		closed := s.nodeChangeClosed
		s.nodeChangeMu.Unlock()
		if closed {
			return
		}
		s.postEvent(tuiNodesChangedEvent{})
	})
}

// stopNodeRefreshDebounce latches the debounce closed so a timer that already
// fired cannot post into a torn-down TUI.
func (s *tuiSessionStore) stopNodeRefreshDebounce() {
	s.nodeChangeMu.Lock()
	defer s.nodeChangeMu.Unlock()
	s.nodeChangeClosed = true
	if s.nodeChangeTimer != nil {
		s.nodeChangeTimer.Stop()
		s.nodeChangeTimer = nil
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
		params := map[string]any{
			"target": targetKey,
			"kind":   session.shellKind.String(),
			"label":  session.label,
			"cols":   s.preferredCols,
			"rows":   s.preferredRows,
		}
		if s.postEvent != nil {
			s.startDaemonTabOpen(targetKey, params, session)
			return "", nil
		}
		var state daemon.TerminalState
		if err := s.Call(s.ctx, "terminal.open", params, &state); err != nil {
			return "", fromDaemonError(err)
		}
		return s.applyOpenedDaemonTab(tuiDaemonTerminalOpenedEvent{
			TargetKey: targetKey,
			ShellKind: session.shellKind,
			Label:     session.label,
			Node:      session.node,
			State:     state,
		})
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

// startDaemonTabOpen issues terminal.open off the event loop. The loop must
// never wait on the daemon: the reply arrives as tuiDaemonTerminalOpenedEvent
// and the tab appears then. The request is recorded as pending so the implicit
// per-selection open does not re-request it on every refresh tick.
func (s *tuiSessionStore) startDaemonTabOpen(targetKey string, params map[string]any, session *tuiSession) {
	s.markTabOpenPending(targetKey)

	opened := tuiDaemonTerminalOpenedEvent{
		TargetKey: targetKey,
		ShellKind: session.shellKind,
		Label:     session.label,
		Node:      session.node,
	}
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, tuiTerminalOpenTimeout)
		defer cancel()
		if err := s.Call(ctx, "terminal.open", params, &opened.State); err != nil {
			opened.Err = fromDaemonError(err)
		}
		s.postCompletion(opened)
	}()
}

func (s *tuiSessionStore) markTabOpenPending(targetKey string) {
	s.pendingOpens[targetKey] = append(s.livePendingTabOpens(targetKey), time.Now().Add(tuiTerminalOpenTimeout))
}

// PendingTabOpen reports whether a terminal tab for targetKey was requested but
// has not been installed yet. The implicit per-selection open consults it so the
// once-per-second sweep cannot stack requests at a slow daemon; explicit opens
// are never suppressed.
func (s *tuiSessionStore) PendingTabOpen(targetKey string) bool {
	pending := s.livePendingTabOpens(targetKey)
	if len(pending) == 0 {
		delete(s.pendingOpens, targetKey)
		return false
	}
	s.pendingOpens[targetKey] = pending
	return true
}

// livePendingTabOpens returns the target's unexpired in-flight opens. Expired
// records are dropped so a lost reply cannot suppress the implicit open forever.
func (s *tuiSessionStore) livePendingTabOpens(targetKey string) []time.Time {
	pending := s.pendingOpens[targetKey]
	now := time.Now()
	live := make([]time.Time, 0, len(pending))
	for _, deadline := range pending {
		if now.Before(deadline) {
			live = append(live, deadline)
		}
	}
	return live
}

// applyOpenedDaemonTab installs a terminal.open reply. It runs on the TUI event
// loop, which owns the session map, the runtime registry, and the tab order.
func (s *tuiSessionStore) applyOpenedDaemonTab(event tuiDaemonTerminalOpenedEvent) (string, error) {
	if pending := s.livePendingTabOpens(event.TargetKey); len(pending) > 0 {
		s.pendingOpens[event.TargetKey] = pending[1:]
	} else {
		delete(s.pendingOpens, event.TargetKey)
	}
	if event.Err != nil {
		s.setSessionError(event.TargetKey, event.Err)
		return "", event.Err
	}

	key := event.State.TabID
	if key == "" {
		key = formatSessionKey(event.TargetKey, s.targetState(event.TargetKey).AllocateTabIndex())
	}
	remote := newDaemonTUITerminal(s, event.State.TerminalID, s.postEvent)
	runtime, ok := s.registry.Register(terminal.TerminalID(event.State.TerminalID), remote)
	if !ok {
		remote.Detach()
		err := fmt.Errorf("daemon returned duplicate terminal id %q", event.State.TerminalID)
		s.setSessionError(event.TargetKey, err)
		return "", err
	}
	s.putSession(&tuiSession{
		key:        key,
		target:     event.TargetKey,
		shellKind:  event.ShellKind,
		label:      event.Label,
		node:       event.Node,
		terminalID: runtime.ID,
	})
	return key, nil
}

// restoreDaemonSessions adopts the daemon's existing terminal tabs at startup.
// It is bounded and best-effort: a daemon that never answers must not turn TUI
// startup into an indefinite hang. On failure the TUI starts with no restored
// tabs, surfaces the reason, and leaves recovery to the reconnect supervisor,
// whose next synchronization installs the authoritative tab list.
func (s *tuiSessionStore) restoreDaemonSessions() {
	timeout := s.restoreTimeout
	if timeout <= 0 {
		timeout = tuiTerminalRestoreTimeout
	}
	ctx, cancel := context.WithTimeout(s.ctx, timeout)
	defer cancel()

	var states []daemon.TerminalState
	err := s.Call(ctx, "terminal.list", nil, &states)
	if err != nil {
		err = fromDaemonError(err)
	} else {
		err = s.replaceDaemonSessions(states)
	}
	if err == nil {
		return
	}

	s.service.log().Error("restore daemon terminal tabs failed", "error", err.Error())
	s.postCompletion(tuiTerminalErrorEvent{Err: fmt.Errorf("restore daemon terminal tabs: %w", err)})
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
	s.stopNodeRefreshDebounce()
	s.eventMu.Lock()
	if s.events != nil {
		_ = s.events.Close()
		s.events = nil
	}
	s.eventMu.Unlock()

	// Detaching a daemon tab drains its accepted input and closing a local one
	// waits out the SIGHUP/SIGTERM/SIGKILL escalation. Run them concurrently and
	// under one bounded wait so quit costs a single teardown, not one per tab.
	var teardown sync.WaitGroup
	for sessionKey, session := range s.sessions {
		if runtime, ok := s.registry.Remove(session.terminalID); ok {
			teardown.Add(1)
			go func() {
				defer teardown.Done()
				if detachable, ok := runtime.Backend.(interface{ Detach() }); ok {
					detachable.Detach()
					return
				}
				runtime.Backend.Close()
			}()
		}
		s.service.log().Debug("terminal closed", "target", session.target, "session", sessionKey, "reason", "shutdown")
		delete(s.sessions, sessionKey)
	}
	waitBounded(&teardown, tuiSessionCloseTimeout)
	for _, st := range s.targets {
		st.Tabs = nil
	}
	s.pendingOpens = map[string][]time.Time{}
}

// waitBounded waits for group, giving up after timeout.
func waitBounded(group *sync.WaitGroup, timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
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
