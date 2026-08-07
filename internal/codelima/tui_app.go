package codelima

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/widgets/term"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

type vaxisTUIRunner struct{}

func newTUIRunner() TUIRunner {
	return &vaxisTUIRunner{}
}

type vaxisTUIApp struct {
	ctx       context.Context
	service   *Service
	vx        *vaxis.Vaxis
	postEvent func(vaxis.Event)
	// postEventBlocking delivers one-shot completion events. vaxis PostEvent
	// silently drops events once its queue is full — exactly when the loop is
	// busy — and every dropped completion latches a state that never clears.
	// nil falls back to postEvent (tests that never fill the queue).
	postEventBlocking func(vaxis.Event)
	treeWorkspaceRoot string
	openLink          func(string) error
	screenHyperlinkAt func(int, int) (string, bool)
	state             *tuiState
	sessions          *tuiSessionStore
	operations        map[string]*tuiOperationState
	operationOrder    []string
	linkRegions       []tuiLinkRegion
	terminalMouse     *tuiTerminalMouseGesture
	// logoAnimation is non-nil only during the bounded startup logo effect.
	// It is owned by the UI event loop alongside every other presentation
	// state; nil renders the stable product name.
	logoAnimation *tuiLogoAnimation
	// daemonDisconnected is latched after the daemon session ends or its
	// request connection fails. Focus events must not probe a dead connection
	// and replace the actionable reopen guidance with broken-pipe noise.
	daemonDisconnected bool
	// overlay is the single active right-pane override (dialog, menu,
	// selector, or messages view); nil means no overlay is showing.
	overlay     tuiOverlay
	messages    *tuiMessageLog
	status      string
	statusLevel slog.Level
	// refreshInFlight suppresses overlapping periodic reloads. refreshDeadline
	// bounds it: a lost tuiRefreshCompleteEvent must not stop auto-refresh, so
	// the next tick past the deadline starts a fresh load regardless.
	refreshInFlight  bool
	refreshDeadline  time.Time
	clipboardPush    func(string) error
	treeContentRect  tuiRect
	terminalBodyRect tuiRect
	// drawPasses counts full-screen rebuilds. It is owned by the event loop
	// like every other field here; only the redraw-coalescing test reads it.
	drawPasses int
	// nodeUsage is the newest live usage sample seen per node id, from either
	// source (the daemon's push or the copy embedded in a node.list reply). It
	// is what lets the two races in applyNodeUsage be decided; it is pruned to
	// the reloaded node set so nothing outlives the node it describes.
	nodeUsage map[string]nodeUsageEvent
}

// postCompletion delivers a one-shot completion or handshake event. Unlike
// ordinary redraw traffic these events carry a latch release, so they use the
// blocking sink when the app has one.
func (a *vaxisTUIApp) postCompletion(event vaxis.Event) {
	if a.postEventBlocking != nil {
		a.postEventBlocking(event)
		return
	}
	if a.postEvent != nil {
		a.postEvent(event)
	}
}

// Typed views of the active overlay: nil when no overlay of that concrete
// type is showing. Dialog flows and tests use these instead of per-type
// fields.
func (a *vaxisTUIApp) activeDialog() *tuiDialog {
	dialog, _ := a.overlay.(*tuiDialog)
	return dialog
}

func (a *vaxisTUIApp) activeMenu() *tuiMenu {
	menu, _ := a.overlay.(*tuiMenu)
	return menu
}

func (a *vaxisTUIApp) activeSelector() *tuiSelector {
	selector, _ := a.overlay.(*tuiSelector)
	return selector
}

func (a *vaxisTUIApp) activeMessagesView() *tuiMessagesView {
	view, _ := a.overlay.(*tuiMessagesView)
	return view
}

// showSelector displays a selector overlay. A selector opened while a dialog
// is showing is a field picker: the dialog is recorded as the selector's
// parent and restored when the selector is dismissed, preserving the old
// behavior where a.selector stacked over a.dialog.
func (a *vaxisTUIApp) showSelector(selector *tuiSelector) {
	if dialog, ok := a.overlay.(*tuiDialog); ok {
		selector.Parent = dialog
	}
	a.overlay = selector
}

// overlayParent returns the overlay to restore when overlay is dismissed:
// the recorded parent for field-picker selectors, nil for everything else.
func overlayParent(overlay tuiOverlay) tuiOverlay {
	if selector, ok := overlay.(*tuiSelector); ok && selector.Parent != nil {
		return selector.Parent
	}
	return nil
}

const (
	terminalViewToggleFooterHint  = "Opt-`/F6"
	terminalViewToggleTextHint    = "Opt-` or F6"
	hostTerminalTabOpenFooterHint = "Opt-Shift-t"
	infoViewToggleFooterHint      = "[i]"
	terminalTabOpenFooterHint     = "Opt-t"
	terminalTabNextFooterHint     = "Opt-Right"
	terminalTabPrevFooterHint     = "Opt-Left"
	terminalTabMoveNextFooterHint = "Opt-Shift-Right"
	terminalTabMovePrevFooterHint = "Opt-Shift-Left"
	terminalTabCloseFooterHint    = "Opt-w"
)

// tuiDaemonAutoRefreshInterval is the fallback poll under a daemon. The daemon
// pushes node.status_changed the moment a lifecycle operation lands and the TUI
// reloads from that push, so this ticker is only the safety net invariant I2
// asks for — a dropped push must not leave the tree stale forever — and not how
// node state normally arrives. Note that node.list is also the transport for
// the daemon's live CPU/memory/disk telemetry, which nothing pushes; the tree's
// usage lines therefore refresh on this interval.
const tuiDaemonAutoRefreshInterval = 10 * time.Second

// tuiLocalAutoRefreshInterval is the poll with no daemon to push. Nothing
// observes node state in that mode, so the interval is the whole freshness
// budget: three seconds still surfaces a node started from another terminal
// promptly, at a third of the runtime listings the one-second poll cost.
const tuiLocalAutoRefreshInterval = 3 * time.Second

// tuiRefreshStallTimeout bounds how long one in-flight node reload suppresses
// the next periodic one. It is the self-healing half of the refresh latch.
const tuiRefreshStallTimeout = 10 * time.Second

// tuiAutoRefreshInterval picks the fallback poll for this session: the daemon
// has a push channel to lean on, a standalone TUI does not.
func tuiAutoRefreshInterval(service *Service) time.Duration {
	if service != nil && service.daemonClient != nil {
		return tuiDaemonAutoRefreshInterval
	}

	return tuiLocalAutoRefreshInterval
}

func (r *vaxisTUIRunner) Run(ctx context.Context, service *Service, workspaceRoot string) error {
	// TUI mode logs to a file sink (CODELIMA_HOME/_logs/codelima.log) instead of
	// stderr so structured logs never corrupt the rendered chrome. This also
	// routes the libghostty stderr capture to the same file (ADR 59). A failure
	// to open the file leaves the discard/CLI logger in place rather than
	// aborting the TUI.
	if closeLog, err := service.enableFileLogging(); err == nil {
		defer func() { _ = closeLog() }()
	}

	nodes, err := loadTUINodes(ctx, service, workspaceRoot)
	if err != nil {
		return err
	}

	vx, err := vaxis.New(vaxis.Options{})
	if err != nil {
		return err
	}
	defer vx.Close()

	sessions := newTUISessionStore(ctx, service, vx.PostEvent)
	sessions.postEventBlocking = vx.PostEventBlocking
	defer sessions.Close()

	state, err := newTUIState(nodes, sessions)
	if err != nil {
		return err
	}

	app := &vaxisTUIApp{
		ctx:               ctx,
		service:           service,
		vx:                vx,
		postEvent:         vx.PostEvent,
		postEventBlocking: vx.PostEventBlocking,
		treeWorkspaceRoot: workspaceRoot,
		state:             state,
		sessions:          sessions,
		operations:        map[string]*tuiOperationState{},
		messages:          newTUIMessageLog(tuiMessageLogDefaultCap),
		logoAnimation:     newTUILogoAnimation(),
	}
	winWidth, winHeight := vx.Window().Size()
	cols, rows := tuiEmbeddedTerminalSize(winWidth, winHeight, tuiFocusTree)
	sessions.SetPreferredTerminalSize(cols, rows)
	if err := state.ensureSelectedTerminalTab(); err != nil {
		app.setStatus(slog.LevelError, err.Error())
	}
	app.syncSessionFocus()
	app.draw()

	stopLogoAnimation := startTUILogoAnimation(ctx, vx.PostEvent)
	defer stopLogoAnimation()
	stopRefresh := startTUIAutoRefresh(ctx, vx.PostEvent, tuiAutoRefreshInterval(service))
	defer stopRefresh()

	return app.serve(vx.Events())
}

// serve runs the TUI event loop until the context is cancelled, the event
// channel closes, or a handler requests quit. It owns the guaranteed teardown
// of the live terminal sessions: draining them is deferred here so that a
// cancelled context (e.g. Ctrl+C wired through signal.NotifyContext in main)
// still closes every terminal — group-killing the VM shells — instead of the
// process exiting through a happy-path-only cleanup. Run keeps its own
// sessions.Close() defer as a backstop for failures before the loop starts.
// Daemon-backed runtimes detach here and keep their PTYs alive; local fallback
// runtimes still close and group-kill their child trees. Close drains the
// session map, so a second call is a no-op. Run also keeps
// host-terminal restoration (vx.Close) and auto-refresh cancellation deferred
// there, keyed to the resources it owns.
func (a *vaxisTUIApp) serve(events chan vaxis.Event) error {
	defer a.sessions.Close()

	// carried holds the one event pulled off the queue while collapsing a
	// redraw burst. A channel cannot be un-read, so the first non-redraw event
	// the collapse encounters is parked here and handled on the next
	// iteration — in its original order, before anything queued behind it.
	var carried vaxis.Event
	for {
		event := carried
		carried = nil
		if event == nil {
			select {
			case <-a.ctx.Done():
				return a.ctx.Err()
			case queued, ok := <-events:
				if !ok {
					return nil
				}
				event = queued
			}
		}

		// Terminal damage and daemon pushes arrive as redraw-only events at up
		// to 20Hz; each one repaints the whole screen. Consecutive ones render
		// identical frames, so drop the run and let this event's own draw stand
		// for all of them. Only redraws are dropped: every other event mutates
		// state, and the one-shot completion and handshake events release
		// latches (invariant I2), so losing one is a hang rather than a frame.
		if isTUIRedrawOnlyEvent(event) {
			carried = collapseQueuedRedraws(events)
		}

		quit, err := a.handleEvent(event)
		if err != nil {
			return err
		}
		if quit {
			return nil
		}
	}
}

// isTUIRedrawOnlyEvent reports whether an event's entire effect is "render the
// current state". Such events are interchangeable, so a queued run of them can
// be served by a single draw.
func isTUIRedrawOnlyEvent(event vaxis.Event) bool {
	_, ok := event.(vaxis.Redraw)
	return ok
}

// collapseQueuedRedraws discards the redraw-only events already queued, without
// blocking. It stops at the first event of any other kind and returns it for
// the caller to handle next; nil means the queue held nothing else.
func collapseQueuedRedraws(events chan vaxis.Event) vaxis.Event {
	for {
		select {
		case queued, ok := <-events:
			if !ok {
				return nil
			}
			if !isTUIRedrawOnlyEvent(queued) {
				return queued
			}
		default:
			return nil
		}
	}
}

func loadTUINodes(ctx context.Context, service *Service, workspaceRoot string) ([]Node, error) {
	if service != nil && service.daemonClient != nil {
		var nodes []Node
		if err := service.daemonClient.Call(ctx, "node.list", map[string]bool{"include_deleted": false}, &nodes); err != nil {
			return nil, fromDaemonError(err)
		}
		return filterNodesByDirectoryRoot(nodes, workspaceRoot)
	}
	return service.NodeListByDirectoryRoot(ctx, workspaceRoot, false)
}

// serve() contract even while every current handler routes errors to status.
//
//nolint:unparam // the error result is the event-loop abort channel; it is part of the
func (a *vaxisTUIApp) handleEvent(event vaxis.Event) (bool, error) {
	switch event := event.(type) {
	case tuiLogoAnimationTickEvent:
		if a.logoAnimation == nil {
			return false, nil
		}
		if a.logoAnimation.advance(event.Elapsed) {
			a.logoAnimation = nil
		}
		a.drawHeaderLogo()
		return false, nil
	case tuiDaemonTerminalDirtyEvent:
		a.sessions.markDaemonTerminalDirty(event.TerminalID, a.state.activeSessionKey())
		return false, nil
	case tuiRefreshTickEvent:
		a.reapCompletedOperations()
		a.startDataRefresh("")
		return false, nil
	case tuiNodesChangedEvent:
		// The daemon pushed a node lifecycle change. Reload now instead of
		// waiting out the fallback ticker; the load runs off the loop and the
		// draw happens when tuiRefreshCompleteEvent lands. This does the tick's
		// work, not a subset of it: the dropped-completion sweep now runs at
		// push latency instead of at the slowed tick.
		a.reapCompletedOperations()
		a.startDataRefresh("")
		return false, nil
	case tuiNodeUsageEvent:
		if a.applyNodeUsage(event.Usage) {
			a.requestRedraw()
		}
		return false, nil
	case tuiRefreshCompleteEvent:
		a.finishDataRefresh(event)
		a.draw()
		return false, nil
	case tuiClipboardEvent:
		if err := a.copyToHostClipboard(event.Text); err != nil {
			a.setStatus(slog.LevelError, err.Error())
		} else {
			a.setStatus(slog.LevelInfo, "synced VM clipboard to host clipboard")
		}
		a.draw()
		return false, nil
	case tuiOperationProgressEvent:
		a.appendOperationLine(event.OperationID, event.Line)
		a.draw()
		return false, nil
	case tuiOperationCompleteEvent:
		a.finishOperation(event)
		a.draw()
		return false, nil
	case tuiTerminalClosedEvent:
		a.handleTerminalClosed(event)
		a.draw()
		return false, nil
	case tuiDaemonTerminalOpenedEvent:
		a.handleDaemonTerminalOpened(event)
		a.draw()
		return false, nil
	case tuiDaemonInputReclaimedEvent:
		a.finishDaemonInputTakeover(event)
		a.draw()
		return false, nil
	case tuiDaemonDisconnectedEvent:
		a.daemonDisconnected = true
		a.setStatus(slog.LevelError, event.Err.Error())
		a.draw()
		return false, nil
	case tuiDaemonSynchronizedEvent:
		var syncErr error
		if a.sessions != nil {
			syncErr = a.sessions.applyDaemonSynchronization(event.Snapshot)
			if syncErr != nil {
				event.complete(syncErr)
				a.daemonDisconnected = true
				a.setStatus(slog.LevelError, fmt.Sprintf("daemon synchronization failed; reconnecting: %v", syncErr))
				a.draw()
				return false, nil
			}
		}
		event.complete(nil)
		wasDisconnected := a.daemonDisconnected
		a.daemonDisconnected = false
		if wasDisconnected {
			a.setStatus(slog.LevelInfo, "reconnected to daemon")
		}
		a.draw()
		return false, nil
	case tuiTerminalErrorEvent:
		if a.daemonDisconnected {
			if a.service != nil {
				a.service.log().Error("terminal request failed after daemon disconnect", "error", event.Err.Error())
			}
			a.draw()
			return false, nil
		}
		a.setStatus(slog.LevelError, event.Err.Error())
		a.draw()
		return false, nil
	case vaxis.FocusIn:
		// A newer TUI can revoke this request connection while both windows stay
		// open. Window focus is an explicit user handoff, so reclaim ownership
		// before the next key or mouse event can mutate a terminal.
		a.startDaemonInputTakeover()
		a.draw()
		return false, nil
	}

	if key, ok := event.(vaxis.Key); ok && isQuitKey(key) && a.overlay != nil {
		return true, nil
	}

	if a.overlay != nil {
		overlay := a.overlay
		done, err := overlay.Update(event)
		if err != nil {
			// Overlay Update errors are non-fatal: they surface in the footer
			// status and dismiss the overlay. (Previously dialog errors aborted
			// the whole TUI while selector/menu errors went to status; status
			// is now the one policy.)
			a.setStatus(slog.LevelError, err.Error())
		}
		// An overlay handler may have opened a replacement overlay (menus open
		// selectors, selectors open confirmation dialogs); only dismiss when
		// the dispatched overlay is still the active one.
		if (done || err != nil) && a.overlay == overlay {
			a.overlay = overlayParent(overlay)
		}
		a.draw()
		return false, nil
	}

	switch event := event.(type) {
	case vaxis.Key:
		if a.state != nil && a.state.focus == tuiFocusTerminal && isTUITerminalPayloadKey(event) {
			a.forwardTerminalEvent(event)
			return false, nil
		}
		quit := a.handleKey(event)
		a.draw()
		return quit, nil
	case vaxis.Mouse:
		a.handleMouse(event)
		a.draw()
		return false, nil
	case vaxis.PasteStartEvent:
		a.forwardTerminalEvent(event)
	case vaxis.PasteEndEvent:
		a.forwardTerminalEvent(event)
	case vaxis.ColorThemeUpdate:
		a.forwardTerminalEvent(event)
		a.draw()
	case vaxis.Resize:
		a.handleResize(event)
		a.draw()
	case vaxis.Redraw:
		a.draw()
	case vaxis.SyncFunc:
		event()
		a.draw()
	case term.EventNotify:
		a.vx.Notify(event.Title, event.Body)
	case vaxis.QuitEvent:
		return true, nil
	}

	return false, nil
}

func (a *vaxisTUIApp) handleTerminalClosed(event tuiTerminalClosedEvent) {
	session, ok := a.sessions.Session(event.SessionKey)
	if !ok {
		return
	}

	targetKey := session.target
	keys := a.sessions.TargetSessionKeys(targetKey)
	a.sessions.RemoveSession(event.SessionKey)
	if a.state.activeTab(targetKey) == event.SessionKey {
		if nextKey := nextActiveTerminalTabAfterClose(keys, event.SessionKey); nextKey != "" {
			a.state.setActiveTab(targetKey, nextKey)
		} else {
			a.state.clearActiveTab(targetKey)
		}
	}
	if a.state.focus == tuiFocusTerminal &&
		a.state.terminalTarget == targetKey &&
		len(a.sessions.TargetSessionKeys(targetKey)) == 0 {
		a.state.focusTree()
	}

	level := slog.LevelInfo
	message := fmt.Sprintf("shell exited for %s", session.label)
	if event.Err != nil {
		level = slog.LevelError
		message = fmt.Sprintf("%s: %s", message, event.Err)
	}
	a.setStatus(level, message)
	a.syncSessionFocus()
}

func (a *vaxisTUIApp) openHyperlink(target string) error {
	if a.openLink != nil {
		return a.openLink(target)
	}
	return openHyperlink(target)
}

// applyNodeUsage merges one pushed usage sample into this window's records.
// A 1Hz push races the node.list reload that carries the same numbers, so the
// rules are:
//
//   - A sample for a node this window does not hold is dropped. The reload owns
//     membership, so a push can never resurrect a node a reload removed, nor
//     add one from outside this window's directory scope.
//   - The newest sample per node wins. Both timestamps are minted by the
//     daemon's forwarder at sample time, so they are directly comparable: a
//     push older than what the last reload carried is ignored here, and a
//     reload carrying older numbers than the last push is corrected in
//     reconcileNodeUsage.
//
// It reports whether anything changed, so a dropped sample costs no repaint.
func (a *vaxisTUIApp) applyNodeUsage(usage nodeUsageEvent) bool {
	if a.state == nil || strings.TrimSpace(usage.NodeID) == "" {
		return false
	}
	if known, ok := a.nodeUsage[usage.NodeID]; ok && usage.SampledAt.Before(known.SampledAt) {
		return false
	}
	if !a.state.applyNodeUsage(usage) {
		return false
	}
	if a.nodeUsage == nil {
		a.nodeUsage = map[string]nodeUsageEvent{}
	}
	a.nodeUsage[usage.NodeID] = usage
	return true
}

// reconcileNodeUsage settles pushed usage against a freshly loaded node list.
// The list embeds the daemon's usage as of the moment it was served, which can
// be older than the last push this window applied; where it is, the push is
// written back over it so a reload never regresses the display. Samples for
// nodes the reload did not return are dropped with them.
func (a *vaxisTUIApp) reconcileNodeUsage() {
	if a.state == nil {
		return
	}

	kept := make(map[string]nodeUsageEvent, len(a.state.nodes))
	for _, node := range a.state.nodes {
		listed := nodeUsageFromNode(node)
		if pushed, ok := a.nodeUsage[node.ID]; ok && listed.SampledAt.Before(pushed.SampledAt) {
			a.state.applyNodeUsage(pushed)
			kept[node.ID] = pushed
			continue
		}
		kept[node.ID] = listed
	}
	a.nodeUsage = kept
}

// requestRedraw asks for a repaint through the event queue rather than drawing
// inline, so a burst of per-node samples collapses into one frame in serve. The
// state is already applied when this is called: a dropped redraw costs a frame,
// never a value. Without a queue (synchronous tests) it draws directly.
func (a *vaxisTUIApp) requestRedraw() {
	if a.postEvent != nil {
		a.postEvent(vaxis.Redraw{})
		return
	}
	a.draw()
}

func (a *vaxisTUIApp) applyReloadedNodes(nodes []Node, preferredKey string) error {
	if err := a.state.replaceNodes(nodes, preferredKey); err != nil {
		return err
	}
	a.reconcileNodeUsage()
	a.sessions.PruneStaleSessions(a.targetKeyStillExists)
	if err := a.ensureSelectedNodeTerminal(); err != nil {
		return err
	}

	a.syncSessionFocus()
	return nil
}

func (a *vaxisTUIApp) ensureSelectedNodeTerminal() error {
	return a.state.ensureSelectedTerminalTab()
}

// targetKeyStillExists reports whether targetKey resolves to a live node in
// either this window's scoped list or the global node store. A path-scoped TUI
// must not close daemon tabs merely because their nodes belong to another TUI
// window's directory scope. Ambiguous store failures also retain tabs: only a
// confirmed missing or deleted node is destructive. A key that does not parse
// as a target is treated as still existing (left untouched).
func (a *vaxisTUIApp) targetKeyStillExists(targetKey string) bool {
	target, err := terminal.ParseTargetKey(targetKey)
	if err != nil {
		return true
	}
	switch target.Kind {
	case terminal.TargetNode:
		if _, ok := a.state.nodesByID[target.ID]; ok {
			return true
		}
		if a.service == nil || a.service.store == nil {
			return false
		}
		node, loadErr := a.service.store.NodeByID(target.ID)
		if loadErr != nil {
			return !IsNotFound(loadErr)
		}
		return node.DeletedAt == nil && node.Status != NodeStatusTerminated
	case terminal.TargetProject:
		// Project targets belong to the retired project-terminal model. Treat
		// any restored bookkeeping for them as stale in schema v4.
		return false
	default:
		return true
	}
}

// startDataRefresh loads the node list off the event loop: it is a daemon RPC
// under a daemon and a store read otherwise, and neither may run on the loop.
// preferredKey is the selection to restore once the load lands; a request that
// carries one always starts, because the periodic tick cannot reproduce it.
func (a *vaxisTUIApp) startDataRefresh(preferredKey string) {
	if preferredKey == "" && a.refreshInFlight && time.Now().Before(a.refreshDeadline) {
		return
	}

	a.refreshInFlight = true
	a.refreshDeadline = time.Now().Add(tuiRefreshStallTimeout)
	if a.postEvent == nil {
		nodes, err := loadTUINodes(a.ctx, a.service, a.treeWorkspaceRoot)
		a.finishDataRefresh(tuiRefreshCompleteEvent{Nodes: nodes, PreferredKey: preferredKey, Err: err})
		return
	}

	go func() {
		nodes, err := loadTUINodes(a.ctx, a.service, a.treeWorkspaceRoot)
		a.postCompletion(tuiRefreshCompleteEvent{Nodes: nodes, PreferredKey: preferredKey, Err: err})
	}()
}

func (a *vaxisTUIApp) finishDataRefresh(event tuiRefreshCompleteEvent) {
	a.refreshInFlight = false
	if event.Err != nil {
		// Auto-refresh errors were previously swallowed silently; surface them at
		// the log seam (warn, not per-tick success) without disturbing the UI,
		// and retain them in the message ring at their true level so a failed
		// background refresh is visible in the messages view. A persistently
		// failing one-second auto-refresh would flood the ring with identical entries,
		// so consecutive duplicates are collapsed.
		a.service.log().Warn("tui refresh failed", "error", event.Err.Error())
		message := "refresh failed: " + event.Err.Error()
		if latest, ok := a.messages.Latest(); !ok || latest.Text != message {
			a.pushMessage(slog.LevelWarn, message)
		}
		return
	}
	if err := a.applyReloadedNodes(event.Nodes, event.PreferredKey); err != nil && a.status == "" {
		a.service.log().Warn("tui node reload failed", "error", err.Error())
		a.setStatus(slog.LevelError, err.Error())
	}
}

// startDaemonInputTakeover reclaims terminal input ownership for this window.
// The RPC runs off the event loop; its outcome returns as
// tuiDaemonInputReclaimedEvent. A window that already knows the daemon is gone
// must not probe the dead connection and replace the actionable reopen
// guidance with broken-pipe noise.
func (a *vaxisTUIApp) startDaemonInputTakeover() {
	if a.daemonDisconnected || a.service == nil || a.service.daemonClient == nil {
		return
	}

	client := a.service.daemonClient
	if a.postEvent == nil {
		a.finishDaemonInputTakeover(tuiDaemonInputReclaimedEvent{Err: takeTUIDaemonInput(a.ctx, client)})
		return
	}
	go func() {
		a.postCompletion(tuiDaemonInputReclaimedEvent{Err: takeTUIDaemonInput(a.ctx, client)})
	}()
}

func (a *vaxisTUIApp) finishDaemonInputTakeover(event tuiDaemonInputReclaimedEvent) {
	if event.Err == nil || a.daemonDisconnected {
		return
	}
	a.service.log().Error("reclaim terminal input ownership failed", "error", event.Err.Error())
	a.daemonDisconnected = true
	a.setStatus(slog.LevelError, "daemon connection lost; reconnecting")
}

// handleDaemonTerminalOpened installs a daemon terminal.open reply. The RPC ran
// off the event loop; the session map, the tab list, and the active-tab record
// are all owned by the loop, so they are updated here.
func (a *vaxisTUIApp) handleDaemonTerminalOpened(event tuiDaemonTerminalOpenedEvent) {
	sessionKey, err := a.sessions.applyOpenedDaemonTab(event)
	if err != nil {
		a.setStatus(slog.LevelError, fmt.Sprintf("start shell for %s: %s", event.Label, err))
		return
	}
	if a.state != nil {
		a.state.setActiveTab(event.TargetKey, sessionKey)
	}
	a.syncSessionFocus()
}

func startTUIAutoRefresh(ctx context.Context, postEvent func(vaxis.Event), interval time.Duration) func() {
	if postEvent == nil || interval <= 0 {
		return func() {}
	}

	refreshCtx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-ticker.C:
				postEvent(tuiRefreshTickEvent{})
			}
		}
	}()
	return cancel
}

// terminalContextEntry is the entry whose terminal tabs the tab keybindings
// operate on: the fullscreen-focused entry in terminal focus, otherwise the
// entry selected in the tree.
func (a *vaxisTUIApp) terminalContextEntry() tuiTreeEntry {
	if a.state.focus == tuiFocusTerminal {
		return a.state.activeTerminalEntry()
	}
	return a.state.selectedEntry()
}

func (a *vaxisTUIApp) openTerminalTab() error {
	entry := a.terminalContextEntry()
	if !entry.valid() {
		return fmt.Errorf("select a node to open a terminal tab")
	}

	if _, err := a.state.openTerminalTabEntry(entry); err != nil {
		return err
	}
	if a.state.focus != tuiFocusTerminal {
		a.state.treePaneMode = tuiTreePaneModeTerminal
	}
	return nil
}

func (a *vaxisTUIApp) openHostTerminalTab() error {
	entry := a.terminalContextEntry()
	if _, err := a.state.openHostTerminalTabEntry(entry); err != nil {
		return err
	}
	if a.state.focus != tuiFocusTerminal {
		a.state.treePaneMode = tuiTreePaneModeTerminal
	}
	return nil
}

func (a *vaxisTUIApp) switchTerminalTab(delta int) error {
	targetKey := a.state.activeTerminalTargetKey()
	keys := a.sessions.TargetSessionKeys(targetKey)
	if len(keys) == 0 {
		return fmt.Errorf("no terminal tabs are open for the focused item")
	}
	if delta == 0 {
		return nil
	}

	current := a.state.activeSessionKey()
	index := -1
	for i, key := range keys {
		if key == current {
			index = i
			break
		}
	}
	if index < 0 {
		index = 0
	} else {
		index = (index + delta) % len(keys)
		if index < 0 {
			index += len(keys)
		}
	}
	a.state.setActiveTab(targetKey, keys[index])
	return nil
}

func (a *vaxisTUIApp) moveTerminalTab(direction int) error {
	targetKey := a.state.activeTerminalTargetKey()
	sessionKey := a.state.activeSessionKey()
	if targetKey == "" || sessionKey == "" {
		return fmt.Errorf("no terminal tab is open for the focused item")
	}
	return a.sessions.MoveTab(targetKey, sessionKey, direction)
}

func (a *vaxisTUIApp) closeTerminalTab() error {
	targetKey := a.state.activeTerminalTargetKey()
	sessionKey := a.state.activeSessionKey()
	if targetKey == "" || sessionKey == "" {
		return fmt.Errorf("no terminal tab is open for the focused item")
	}

	keys := a.sessions.TargetSessionKeys(targetKey)
	nextKey := nextActiveTerminalTabAfterClose(keys, sessionKey)

	a.sessions.CloseSession(sessionKey)
	a.sessions.ClearSessionError(targetKey)

	if nextKey != "" {
		a.state.setActiveTab(targetKey, nextKey)
		return nil
	}

	a.state.clearActiveTab(targetKey)
	if a.state.focus == tuiFocusTerminal && a.state.terminalTarget == targetKey {
		a.state.focusTree()
	}
	return nil
}

func nextActiveTerminalTabAfterClose(keys []string, closingKey string) string {
	for index, key := range keys {
		if key != closingKey {
			continue
		}
		if index+1 < len(keys) {
			return keys[index+1]
		}
		if index > 0 {
			return keys[index-1]
		}
		return ""
	}
	return ""
}

func (a *vaxisTUIApp) forwardTerminalEvent(event vaxis.Event) {
	if a.state.focus != tuiFocusTerminal {
		return
	}

	a.forwardSessionEvent(a.state.activeSessionKey(), event)
}

func (a *vaxisTUIApp) forwardSessionEvent(sessionKey string, event vaxis.Event) {
	term, ok := a.sessions.SessionTerminal(sessionKey)
	if !ok {
		return
	}
	event = normalizeTUITerminalEvent(event)
	term.Update(event)
}

func (a *vaxisTUIApp) syncSessionFocus() {
	a.sessions.SyncFocus(a.state.activeSessionKey(), a.effectiveLayoutFocus() == tuiFocusTerminal)
}
