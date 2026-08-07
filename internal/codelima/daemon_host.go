package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"golang.org/x/sys/unix"
)

type daemonTerminal interface {
	tuiTerminal
	ReadVisible(ReadFormat) ReadResult
	ReadRecent(ReadFormat) ReadResult
	Snapshot() SnapshotResult
	Scroll(int)
	SendInput([]byte)
}

type handoffDaemonTerminal interface {
	daemonTerminal
	BeginHandoff() handoffTerminalState
	ReleaseAfterHandoff()
	RollbackHandoff() error
}

// rendererRestartableTerminal is a terminal runtime that owns a replaceable
// renderer process. It is probed rather than folded into daemonTerminal because
// only the isolated runtime has a renderer to replace: the in-process Ghostty
// and vaxis terminals draw on the daemon's own goroutines, so there is nothing
// for a restart to act on. Keeping it optional also keeps the shared interface
// -- which every build tag and every test fake must satisfy -- from growing a
// method that all but one implementation would answer with the same error.
type rendererRestartableTerminal interface {
	RestartRenderer() error
}

type daemonTerminalEntry struct {
	state daemon.TerminalState
	term  daemonTerminal

	cache            atomic.Pointer[daemonTerminalCache]
	snapshotSequence atomic.Uint64
	snapshotWake     chan struct{}
	snapshotStop     chan struct{}
	snapshotStopOnce sync.Once
}

// daemonTerminalCache is one published screen. It holds only what publication
// already produced: the immutable snapshot and its plain visible text. Every
// other read variant is fetched from the terminal when terminal.read asks for
// it, so publication cost does not scale with variants nobody reads.
type daemonTerminalCache struct {
	snapshot    daemon.Snapshot
	visibleText ReadResult
}

// daemonHost owns three locks and they are always taken in this order:
// updateGate, then persistMu, then mu. updateGate is the live-update barrier;
// persist takes persistMu and then mu through list. No path holds mu while
// taking persistMu, and no path holds either while taking updateGate.
type daemonHost struct {
	service               *Service
	forwarder             *dynamicForwarder
	observer              LimaObservationRuntime
	virtioFSReclaimTicker *virtioFSReclaimTicker
	virtioFSSupported     bool
	restore               string
	session               string
	// updateGate admits terminal-set mutations on the read side and a live
	// update on the write side, so the handoff manifest is always the complete
	// tab set.
	updateGate sync.RWMutex
	// replaced records that a live update committed. From that moment the tab
	// set belongs to the successor daemon: this daemon must neither rewrite
	// session.json nor accept a mutation it could never hand over.
	replaced atomic.Bool
	// serving records that the daemon server is accepting clients, which is the
	// point from which broadcasting is both meaningful and safe.
	serving              atomic.Bool
	mu                   sync.RWMutex
	persistMu            sync.Mutex
	teardownMu           sync.Mutex
	teardowns            []chan struct{}
	terminals            map[string]*daemonTerminalEntry
	terminalOrder        []string
	broadcast            func(string, any)
	prepareReplacement   func() error
	resumeReplacement    func() error
	stopServer           func()
	terminalCloseTimeout time.Duration
	terminalFactory      func(string, func(vaxis.Event)) tuiTerminal
}

func newDaemonHost(service *Service) *daemonHost {
	paths := daemon.HomePaths(service.cfg.MetadataRoot)
	host := &daemonHost{
		service: service, restore: service.cfg.Daemon.Restore, session: paths.Session,
		terminals: map[string]*daemonTerminalEntry{}, broadcast: func(string, any) {},
		prepareReplacement:   func() error { return errors.New("daemon replacement unavailable") },
		resumeReplacement:    func() error { return errors.New("daemon replacement unavailable") },
		stopServer:           func() {},
		terminalCloseTimeout: 5 * time.Second,
		terminalFactory:      newIsolatedDaemonTerminal,
	}
	if runtime, ok := service.sandbox.(LimaSSHRuntime); ok {
		host.forwarder = newDynamicForwarder(service, runtime)
	}
	if observer, ok := service.sandbox.(LimaObservationRuntime); ok {
		host.observer = observer
	}
	if virtioFSReclaimSupported() {
		host.virtioFSSupported = true
		if service.cfg.Daemon.VirtioFSReclaim {
			host.virtioFSReclaimTicker = newVirtioFSReclaimTicker(
				service.reclaimMountedNodeFilesystemCaches,
				defaultVirtioFSReclaimInterval,
			)
			host.virtioFSReclaimTicker.logger = service.log()
		}
	}
	return host
}

func (h *daemonHost) newTerminalEntry(state daemon.TerminalState, runtime daemonTerminal) *daemonTerminalEntry {
	entry := &daemonTerminalEntry{
		state:        state,
		term:         runtime,
		snapshotWake: make(chan struct{}, 1),
		snapshotStop: make(chan struct{}),
	}
	go h.runTerminalSnapshotPublisher(entry)
	entry.requestSnapshot()
	return entry
}

func (e *daemonTerminalEntry) requestSnapshot() {
	if e == nil || e.snapshotWake == nil {
		return
	}
	select {
	case e.snapshotWake <- struct{}{}:
	default:
	}
}

func (e *daemonTerminalEntry) stopSnapshotPublisher() {
	if e == nil || e.snapshotStop == nil {
		return
	}
	e.snapshotStopOnce.Do(func() { close(e.snapshotStop) })
}

func (h *daemonHost) runTerminalSnapshotPublisher(entry *daemonTerminalEntry) {
	for {
		select {
		case <-entry.snapshotStop:
			return
		case <-entry.snapshotWake:
		}

		result := entry.term.Snapshot()
		if result.Err != nil {
			h.broadcast(daemon.EventTerminalError, daemon.TerminalErrorEvent{
				Error:      result.Err.Error(),
				TerminalID: entry.state.TerminalID,
			})
			continue
		}

		published := daemonSnapshot(result.Snapshot)
		published.SnapshotSequence = entry.snapshotSequence.Add(1)
		published.ProducedAt = time.Now().UTC()
		cache := &daemonTerminalCache{
			snapshot: published,
			visibleText: ReadResult{
				Text:       daemonSnapshotText(published),
				Generation: published.Generation,
			},
		}
		entry.cache.Store(cache)
		h.broadcast(daemon.EventTerminalDirty, daemon.TerminalDirtyEvent{
			SnapshotSequence: published.SnapshotSequence,
			Stale:            published.Stale,
			TerminalID:       entry.state.TerminalID,
		})
		// Nothing else is pulled here. The published frame carries the screen and
		// its plain visible text, which the snapshot already contains; the ANSI
		// viewport render and both scrollback renders each walk the whole grid or
		// the retained scrollback, and used to be recomputed on every wake -- up
		// to 20 times a second per terminal -- whether or not any client ever
		// called terminal.read. The terminal renders them on demand instead and
		// memoizes them for the life of the published screen, so terminal.read
		// pays for them exactly once per screen and a terminal nobody reads pays
		// nothing.
	}
}

func (e *daemonTerminalEntry) markSnapshotStale() {
	if e == nil {
		return
	}
	if current := e.cache.Load(); current != nil && !current.snapshot.Stale {
		next := *current
		next.snapshot.Stale = true
		e.cache.Store(&next)
	}
	e.requestSnapshot()
}

func (h *daemonHost) Handle(ctx context.Context, _ daemon.ClientContext, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "daemon.update":
		var params struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(raw, &params)
		return h.update(params.Path)
	case "configuration.list":
		var params struct {
			IncludeDeleted bool `json:"include_deleted"`
		}
		_ = json.Unmarshal(raw, &params)
		return h.service.ConfigurationList(ctx, params.IncludeDeleted)
	case "node.list":
		var params struct {
			IncludeDeleted bool `json:"include_deleted"`
		}
		_ = json.Unmarshal(raw, &params)
		nodes, err := h.service.NodeList(ctx, params.IncludeDeleted)
		if err != nil {
			return nil, toDaemonError(err)
		}
		if h.forwarder != nil {
			h.forwarder.addNodeUsage(nodes)
		}
		return nodes, nil
	case "node.start", "node.stop":
		var params struct {
			Node string `json:"node"`
		}
		if err := json.Unmarshal(raw, &params); err != nil || strings.TrimSpace(params.Node) == "" {
			return nil, daemon.Error("InvalidArgument", "node is required", ExitInvalidArgument, nil)
		}
		var result Node
		var err error
		if method == "node.start" {
			result, err = h.service.NodeStart(ctx, params.Node)
		} else {
			result, err = h.service.NodeStop(ctx, params.Node)
		}
		if err == nil {
			h.broadcast(daemon.EventNodeStatusChanged, result)
		}
		return result, toDaemonError(err)
	case "terminal.list":
		return h.list(), nil
	case "terminal.open":
		var params terminalOpenParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, daemon.Error("InvalidArgument", "invalid terminal.open params", ExitInvalidArgument, nil)
		}
		return h.open(ctx, params, "")
	case "terminal.close":
		var params terminalIDParams
		_ = json.Unmarshal(raw, &params)
		return map[string]bool{"closed": true}, h.close(params.TerminalID)
	case "terminal.restart_renderer":
		var params terminalIDParams
		_ = json.Unmarshal(raw, &params)
		return map[string]bool{"restarted": true}, h.restartRenderer(params.TerminalID)
	case "terminal.move":
		var params struct {
			TerminalID string `json:"terminal_id"`
			Delta      int    `json:"delta"`
		}
		if err := json.Unmarshal(raw, &params); err != nil || params.TerminalID == "" || (params.Delta != -1 && params.Delta != 1) {
			return nil, daemon.Error("InvalidArgument", "terminal.move requires a terminal id and delta -1 or 1", ExitInvalidArgument, nil)
		}
		moved, err := h.move(params.TerminalID, params.Delta)
		return map[string]bool{"moved": moved}, err
	case "terminal.resize":
		var params struct {
			TerminalID string `json:"terminal_id"`
			Cols       int    `json:"cols"`
			Rows       int    `json:"rows"`
		}
		if err := json.Unmarshal(raw, &params); err != nil || params.Cols <= 0 || params.Rows <= 0 {
			return nil, daemon.Error("InvalidArgument", "positive terminal size is required", ExitInvalidArgument, nil)
		}
		entry, err := h.lookup(params.TerminalID)
		if err != nil {
			return nil, err
		}
		h.mu.RLock()
		unchanged := entry.state.Cols == params.Cols && entry.state.Rows == params.Rows
		state := entry.state
		h.mu.RUnlock()
		if unchanged {
			return state, nil
		}
		entry.term.Resize(params.Cols, params.Rows)
		h.mu.Lock()
		entry.state.Cols, entry.state.Rows = params.Cols, params.Rows
		state = entry.state
		h.mu.Unlock()
		_ = h.persist()
		h.broadcast(daemon.EventTerminalResized, state)
		return state, nil
	case "terminal.focus":
		var params terminalIDParams
		_ = json.Unmarshal(raw, &params)
		entry, err := h.lookup(params.TerminalID)
		if err != nil {
			return nil, err
		}
		entry.term.Focus()
		return map[string]bool{"focused": true}, nil
	case "terminal.scroll":
		var params struct {
			TerminalID string `json:"terminal_id"`
			Delta      int    `json:"delta"`
		}
		_ = json.Unmarshal(raw, &params)
		entry, err := h.lookup(params.TerminalID)
		if err != nil {
			return nil, err
		}
		entry.term.Scroll(params.Delta)
		return map[string]bool{"scrolled": true}, nil
	case "terminal.read":
		var params struct {
			TerminalID string `json:"terminal_id"`
			Source     string `json:"source"`
			Format     string `json:"format"`
		}
		_ = json.Unmarshal(raw, &params)
		entry, err := h.lookup(params.TerminalID)
		if err != nil {
			return nil, err
		}
		cache := entry.cache.Load()
		if cache == nil {
			return nil, daemon.Error("DependencyUnavailable", "terminal snapshot is not available yet", ExitDependencyUnavailable, map[string]any{"terminal_id": params.TerminalID})
		}
		// Only the plain visible text rides along with the published screen. The
		// other three variants are pulled from the terminal here, which renders
		// each one at most once per published screen and memoizes it, so this
		// stays a cheap map lookup for every read after the first.
		result := cache.visibleText
		switch {
		case params.Source == "recent" && params.Format == "ansi":
			result = entry.term.ReadRecent(ReadANSI)
		case params.Source == "recent":
			result = entry.term.ReadRecent(ReadText)
		case params.Format == "ansi":
			result = entry.term.ReadVisible(ReadANSI)
		}
		if result.Text == "" && result.Err == nil {
			result = cache.visibleText
		}
		return result, toDaemonError(result.Err)
	case "terminal.snapshot":
		var params terminalIDParams
		_ = json.Unmarshal(raw, &params)
		entry, err := h.lookup(params.TerminalID)
		if err != nil {
			return nil, err
		}
		cache := entry.cache.Load()
		if cache == nil {
			return nil, daemon.Error("DependencyUnavailable", "terminal snapshot is not available yet", ExitDependencyUnavailable, map[string]any{"terminal_id": params.TerminalID})
		}
		return cache.snapshot, nil
	case "terminal.send_text", "terminal.send_input", "terminal.send_keys":
		var params struct {
			TerminalID string   `json:"terminal_id"`
			Text       string   `json:"text"`
			Data       []byte   `json:"data"`
			Keys       []string `json:"keys"`
		}
		_ = json.Unmarshal(raw, &params)
		entry, err := h.lookup(params.TerminalID)
		if err != nil {
			return nil, err
		}
		data := params.Data
		if method == "terminal.send_text" {
			data = []byte(params.Text)
		} else if method == "terminal.send_keys" {
			data, err = encodeDaemonKeys(params.Keys)
			if err != nil {
				return nil, err
			}
		}
		entry.term.SendInput(data)
		return map[string]int{"bytes": len(data)}, nil
	case "terminal.send_event":
		var params struct {
			TerminalID string `json:"terminal_id"`
			Type       string `json:"type"`
			Keycode    rune   `json:"keycode"`
			Shifted    rune   `json:"shifted_code"`
			Text       string `json:"text"`
			Modifiers  uint8  `json:"modifiers"`
			EventType  uint8  `json:"event_type"`
			Col        int    `json:"col"`
			Row        int    `json:"row"`
			Button     int    `json:"button"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, daemon.Error("InvalidArgument", "invalid terminal event", ExitInvalidArgument, nil)
		}
		entry, err := h.lookup(params.TerminalID)
		if err != nil {
			return nil, err
		}
		switch params.Type {
		case "key":
			entry.term.Update(vaxis.Key{Keycode: params.Keycode, ShiftedCode: params.Shifted, Text: params.Text, Modifiers: vaxis.ModifierMask(params.Modifiers), EventType: vaxis.EventType(params.EventType)})
		case "mouse":
			entry.term.Update(vaxis.Mouse{Col: params.Col, Row: params.Row, Button: vaxis.MouseButton(params.Button), EventType: vaxis.EventType(params.EventType)})
		case "paste":
			entry.term.Update(vaxis.PasteStartEvent{})
			entry.term.Update(normalizeTUITerminalEvent(vaxis.Key{Text: params.Text, EventType: vaxis.EventPaste}))
			entry.term.Update(vaxis.PasteEndEvent{})
		default:
			return nil, daemon.Error("InvalidArgument", "terminal event type must be key, paste, or mouse", ExitInvalidArgument, nil)
		}
		return map[string]bool{"sent": true}, nil
	default:
		return nil, daemon.Error("InvalidArgument", "unknown daemon method", ExitInvalidArgument, map[string]any{"method": method})
	}
}

type terminalOpenParams struct {
	Target string `json:"target"`
	Kind   string `json:"kind"`
	Label  string `json:"label,omitempty"`
	Cols   int    `json:"cols,omitempty"`
	Rows   int    `json:"rows,omitempty"`
}

type terminalIDParams struct {
	TerminalID string `json:"terminal_id"`
}

// enterTerminalMutation admits a change to the daemon-owned tab set. Control
// requests now dispatch concurrently, so a terminal.open can land while
// daemon.update is quiescing; without this barrier that tab would exist only in
// a daemon that is about to stop, and no manifest would carry it.
//
// The chosen semantics are hold-until-the-update-resolves: a mutation that
// arrives during a handoff waits for it. If the update fails, this daemon still
// owns the tabs and the mutation is served normally. If the update commits, the
// mutation is rejected with a retryable PreconditionFailed so the caller
// reissues it against the successor daemon. A tab therefore either exists
// before the manifest is captured, and is handed over, or is never created at
// all; it can never be created into a daemon that has already handed off.
func (h *daemonHost) enterTerminalMutation() error {
	h.updateGate.RLock()
	if h.replaced.Load() {
		h.updateGate.RUnlock()
		return daemon.Error(
			"PreconditionFailed",
			"daemon was replaced by a live update; retry against the new daemon",
			ExitPreconditionFailed,
			map[string]any{"daemon_replaced": true},
		)
	}
	return nil
}

func (h *daemonHost) leaveTerminalMutation() {
	h.updateGate.RUnlock()
}

func (h *daemonHost) open(ctx context.Context, params terminalOpenParams, terminalID string) (daemon.TerminalState, error) {
	if err := h.enterTerminalMutation(); err != nil {
		return daemon.TerminalState{}, err
	}
	defer h.leaveTerminalMutation()
	return h.openTerminal(ctx, params, terminalID)
}

// openTerminal is the ungated body of open. Callers must already hold the
// update barrier; the read side is not reentrant, so the rollback path below
// closes through closeTerminal rather than close.
func (h *daemonHost) openTerminal(ctx context.Context, params terminalOpenParams, terminalID string) (daemon.TerminalState, error) {
	target, err := terminal.ParseTargetKey(params.Target)
	if err != nil {
		return daemon.TerminalState{}, daemon.Error("InvalidArgument", err.Error(), ExitInvalidArgument, nil)
	}
	kind := terminal.NodeHostShell
	workspace := ""
	switch params.Kind {
	case "node-host-shell", "host":
		node, nodeErr := h.service.nodeTerminalMetadata(ctx, target.ID)
		if nodeErr != nil {
			return daemon.TerminalState{}, toDaemonError(nodeErr)
		}
		workspace = node.DirectoryPath
		if params.Label == "" {
			params.Label = node.Slug + " host"
		}
	case "node-shell", "node":
		kind = terminal.NodeShell
		node, nodeErr := h.service.nodeTerminalMetadata(ctx, target.ID)
		if nodeErr != nil {
			return daemon.TerminalState{}, toDaemonError(nodeErr)
		}
		if params.Label == "" {
			params.Label = node.Slug
		}
	default:
		return daemon.TerminalState{}, daemon.Error("InvalidArgument", "terminal kind must be node-host-shell or node-shell", ExitInvalidArgument, nil)
	}
	spec, err := h.service.TerminalLaunchSpec(target, kind, workspace)
	if err != nil {
		return daemon.TerminalState{}, toDaemonError(err)
	}
	if terminalID == "" {
		terminalID = "term_" + strings.ReplaceAll(newID(), "-", "")
	}
	cols, rows := params.Cols, params.Rows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	factory := h.terminalFactory
	if factory == nil {
		factory = newIsolatedDaemonTerminal
	}
	base := factory(terminalID, func(event vaxis.Event) { h.handleTerminalEvent(terminalID, event) })
	runtime, ok := base.(daemonTerminal)
	if !ok {
		base.Close()
		return daemon.TerminalState{}, daemon.Error("DependencyUnavailable", "daemon terminals require the Ghostty runtime", ExitDependencyUnavailable, nil)
	}
	runtime.Resize(cols, rows)
	command := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	command.Env, command.Dir = spec.Env, spec.Dir
	if err := runtime.Start(command); err != nil {
		runtime.Close()
		return daemon.TerminalState{}, toDaemonError(err)
	}
	state := daemon.TerminalState{TerminalID: terminalID, TabID: params.Target + "#" + terminalID, Target: params.Target, Kind: kind.String(), Label: params.Label, CWD: spec.Dir, Argv: slices.Clone(spec.Argv), CreatedAt: time.Now().UTC(), Cols: cols, Rows: rows}
	h.mu.Lock()
	h.ensureTerminalOrderLocked()
	h.terminals[terminalID] = h.newTerminalEntry(state, runtime)
	h.terminalOrder = append(h.terminalOrder, terminalID)
	h.mu.Unlock()
	if err := h.persist(); err != nil {
		_ = h.closeTerminal(terminalID)
		return daemon.TerminalState{}, err
	}
	h.broadcast(daemon.EventTerminalCreated, state)
	h.broadcast(daemon.EventTargetTabsChanged, daemon.TargetTabsChangedEvent{Target: state.Target})
	return state, nil
}

func (h *daemonHost) close(id string) error {
	if err := h.enterTerminalMutation(); err != nil {
		return err
	}
	defer h.leaveTerminalMutation()
	return h.closeTerminal(id)
}

// restartRenderer forces one terminal's renderer process to be replaced. It
// takes the same update barrier close and move do, for the same reason: a
// renderer restart and a live handoff are two ways of replacing the same
// renderer, and they must not overlap.
//
// BeginHandoff quiesces a terminal by draining the PTY writer, duplicating the
// PTY fd and closing the renderer, then clears the terminal's renderer pointer.
// A restart dispatched concurrently -- terminal.restart_renderer is control
// class, so it runs off the connection reader like terminal.open -- could read
// the renderer pointer just before that and wake the supervisor just after, so
// the predecessor would spawn a fresh worker and replay the journal into it
// while the successor daemon was adopting the very same PTY. The barrier makes
// that ordering unrepresentable, and after a committed update it converts the
// request into the retryable PreconditionFailed that tells the caller to reissue
// against the successor -- which is where the terminal, and its renderer, now
// live.
//
// The restart itself is a flag and a wake on the supervisor goroutine, so the
// gate is held for microseconds and the reply does not wait for the replacement
// process to come up.
func (h *daemonHost) restartRenderer(id string) error {
	if err := h.enterTerminalMutation(); err != nil {
		return err
	}
	defer h.leaveTerminalMutation()
	entry, err := h.lookup(id)
	if err != nil {
		return err
	}
	restartable, ok := entry.term.(rendererRestartableTerminal)
	if !ok {
		return daemon.Error(
			string(CategoryUnsupportedFeature),
			"terminal runtime has no renderer process to restart",
			ExitPreconditionFailed,
			map[string]any{"terminal_id": id},
		)
	}
	if err := restartable.RestartRenderer(); err != nil {
		return toDaemonError(err)
	}
	return nil
}

// closeTerminal is the ungated body of close. Callers must already hold the
// update barrier.
func (h *daemonHost) closeTerminal(id string) error {
	h.mu.Lock()
	entry, ok := h.terminals[id]
	if ok {
		h.ensureTerminalOrderLocked()
		delete(h.terminals, id)
		h.removeTerminalOrderLocked(id)
	}
	h.mu.Unlock()
	if !ok {
		return daemon.Error("NotFound", "terminal not found", ExitNotFound, map[string]any{"terminal_id": id})
	}
	entry.stopSnapshotPublisher()
	// Runtime teardown escalates SIGHUP to SIGTERM to SIGKILL and can take
	// seconds against a signal-immune shell. Every piece of daemon-owned state
	// this request owns is already committed, so the reply must not wait for
	// the process to exit: a connection is a transport boundary, not a
	// serialization boundary.
	h.closeTerminalRuntimeAsync(entry)
	persistErr := h.persist()
	h.broadcast(daemon.EventTerminalClosed, entry.state)
	h.broadcast(daemon.EventTargetTabsChanged, daemon.TargetTabsChangedEvent{Target: entry.state.Target})
	return persistErr
}

// closeTerminalRuntimeAsync finishes a bounded runtime teardown after the reply
// has been sent. Daemon shutdown still waits for these within its own bounded
// deadline so a live update never orphans a child process mid-escalation.
func (h *daemonHost) closeTerminalRuntimeAsync(entry *daemonTerminalEntry) {
	tracked := h.trackTeardown()
	go func() {
		defer close(tracked)
		done := make(chan struct{})
		go func() {
			entry.term.Close()
			close(done)
		}()
		timeout := h.terminalCloseTimeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			if h.service != nil {
				h.service.log().Warn(
					"terminal close exceeded its bounded shutdown deadline",
					"terminal", entry.state.TerminalID,
					"timeout", timeout,
				)
			}
		}
	}()
}

// trackTeardown registers a background teardown and prunes finished ones, so
// shutdown can wait for work in flight without an unbounded registry.
func (h *daemonHost) trackTeardown() chan struct{} {
	tracked := make(chan struct{})
	h.teardownMu.Lock()
	h.teardowns = slices.DeleteFunc(h.teardowns, func(pending chan struct{}) bool {
		select {
		case <-pending:
			return true
		default:
			return false
		}
	})
	h.teardowns = append(h.teardowns, tracked)
	h.teardownMu.Unlock()
	return tracked
}

func (h *daemonHost) pendingTeardowns() []chan struct{} {
	h.teardownMu.Lock()
	defer h.teardownMu.Unlock()
	return slices.Clone(h.teardowns)
}

// move reorders the daemon-owned tab list, which the handoff manifest carries,
// so it takes the same update barrier open and close do.
func (h *daemonHost) move(id string, direction int) (bool, error) {
	if err := h.enterTerminalMutation(); err != nil {
		return false, err
	}
	defer h.leaveTerminalMutation()
	return h.moveTerminal(id, direction)
}

func (h *daemonHost) moveTerminal(id string, direction int) (bool, error) {
	h.mu.Lock()
	entry, ok := h.terminals[id]
	if !ok {
		h.mu.Unlock()
		return false, daemon.Error("NotFound", "terminal not found", ExitNotFound, map[string]any{"terminal_id": id})
	}
	h.ensureTerminalOrderLocked()
	index := slices.Index(h.terminalOrder, id)
	neighbor := index + direction
	for neighbor >= 0 && neighbor < len(h.terminalOrder) {
		neighborEntry := h.terminals[h.terminalOrder[neighbor]]
		if neighborEntry != nil && neighborEntry.state.Target == entry.state.Target {
			h.terminalOrder[index], h.terminalOrder[neighbor] = h.terminalOrder[neighbor], h.terminalOrder[index]
			break
		}
		neighbor += direction
	}
	moved := neighbor >= 0 && neighbor < len(h.terminalOrder)
	target := entry.state.Target
	h.mu.Unlock()

	if !moved {
		return false, nil
	}
	if err := h.persist(); err != nil {
		return false, err
	}
	h.broadcast(daemon.EventTargetTabsChanged, daemon.TargetTabsChangedEvent{Target: target})
	return true, nil
}

func (h *daemonHost) lookup(id string) (*daemonTerminalEntry, error) {
	h.mu.RLock()
	entry, ok := h.terminals[id]
	h.mu.RUnlock()
	if !ok {
		return nil, daemon.Error("NotFound", "terminal not found", ExitNotFound, map[string]any{"terminal_id": id})
	}
	return entry, nil
}

func (h *daemonHost) list() []daemon.TerminalState {
	h.mu.RLock()
	defer h.mu.RUnlock()
	states := make([]daemon.TerminalState, 0, len(h.terminals))
	for _, id := range h.terminalIDsInOrderLocked() {
		states = append(states, h.terminals[id].state)
	}
	return states
}

// terminalIDsInOrderLocked returns the explicit daemon-owned order plus any
// map entries not yet tracked by older construction paths. Callers must hold at
// least h.mu.RLock.
func (h *daemonHost) terminalIDsInOrderLocked() []string {
	ids := make([]string, 0, len(h.terminals))
	seen := make(map[string]struct{}, len(h.terminals))
	for _, id := range h.terminalOrder {
		if _, ok := h.terminals[id]; !ok {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		ids = append(ids, id)
		seen[id] = struct{}{}
	}

	missing := make([]string, 0, len(h.terminals)-len(ids))
	for id := range h.terminals {
		if _, ok := seen[id]; !ok {
			missing = append(missing, id)
		}
	}
	slices.SortFunc(missing, func(left, right string) int {
		return compareDaemonTerminalStateOrder(h.terminals[left].state, h.terminals[right].state)
	})
	return append(ids, missing...)
}

// ensureTerminalOrderLocked normalizes the explicit order before a mutation.
// Callers must hold h.mu.Lock.
func (h *daemonHost) ensureTerminalOrderLocked() {
	h.terminalOrder = h.terminalIDsInOrderLocked()
}

// removeTerminalOrderLocked removes id from the explicit order. Callers must
// hold h.mu.Lock.
func (h *daemonHost) removeTerminalOrderLocked(id string) {
	if index := slices.Index(h.terminalOrder, id); index >= 0 {
		h.terminalOrder = slices.Delete(h.terminalOrder, index, index+1)
	}
}

// compareDaemonTerminalStateOrder provides a deterministic creation-order
// fallback for legacy construction paths that have no explicit order yet.
func compareDaemonTerminalStateOrder(left, right daemon.TerminalState) int {
	if order := left.CreatedAt.Compare(right.CreatedAt); order != 0 {
		return order
	}
	return strings.Compare(left.TerminalID, right.TerminalID)
}

func (h *daemonHost) Snapshot(context.Context) (any, error) {
	terminalSnapshots := make(map[string]daemon.Snapshot)
	terminalRuntimes := make(map[string]map[string]any)
	h.mu.RLock()
	for id, entry := range h.terminals {
		if cache := entry.cache.Load(); cache != nil {
			terminalSnapshots[id] = cache.snapshot
		}
		if runtime, ok := entry.term.(interface{ RuntimeDiagnostics() map[string]any }); ok {
			terminalRuntimes[id] = runtime.RuntimeDiagnostics()
		}
	}
	h.mu.RUnlock()
	forwarding := map[string]any{"enabled": false}
	if h.forwarder != nil {
		forwarding = h.forwarder.Snapshot()
	}
	virtioFSReclaim := map[string]any{
		"enabled":   h.service.cfg.Daemon.VirtioFSReclaim,
		"supported": h.virtioFSSupported,
	}
	if h.virtioFSReclaimTicker != nil {
		state := h.virtioFSReclaimTicker.Snapshot()
		virtioFSReclaim["interval_seconds"] = state.IntervalSeconds
		virtioFSReclaim["last_reclaimed_nodes"] = state.LastReclaimedNodes
		if !state.LastRunAt.IsZero() {
			virtioFSReclaim["last_run_at"] = state.LastRunAt
		}
		if !state.NextRunAt.IsZero() {
			virtioFSReclaim["next_run_at"] = state.NextRunAt
		}
		if state.LastError != "" {
			virtioFSReclaim["last_error"] = state.LastError
		}
	}
	observation := map[string]any{"connected": false}
	if h.observer != nil {
		observation = h.observer.ObservationSnapshot()
	}
	return map[string]any{
		"session":            daemon.Session{Version: daemon.SessionVersion, Terminals: h.list()},
		"terminal_snapshots": terminalSnapshots,
		"terminal_runtimes":  terminalRuntimes,
		"forwarding":         forwarding,
		"lima_observation":   observation,
		"virtiofs_reclaim":   virtioFSReclaim,
	}, nil
}

func (h *daemonHost) SynchronizationSnapshot(context.Context) (any, error) {
	return map[string]any{
		"session": daemon.Session{Version: daemon.SessionVersion, Terminals: h.list()},
	}, nil
}

func (h *daemonHost) TerminalCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.terminals)
}

func (h *daemonHost) Close() error {
	// Session state is recovery intent. Persist it before any potentially slow
	// forwarding or terminal teardown so a forced legacy-daemon shutdown can
	// still respawn every saved tab. After a committed live update persist is a
	// no-op, because the successor daemon owns those tabs and already wrote
	// them.
	closeErr := h.persist()
	if h.virtioFSReclaimTicker != nil {
		h.virtioFSReclaimTicker.Close()
	}
	if h.forwarder != nil {
		closeErr = errors.Join(closeErr, h.forwarder.Close())
	}
	if h.observer != nil {
		closeErr = errors.Join(closeErr, h.observer.StopObservation())
	}
	h.mu.Lock()
	entries := make([]*daemonTerminalEntry, 0, len(h.terminals))
	for id, entry := range h.terminals {
		entries = append(entries, entry)
		delete(h.terminals, id)
	}
	h.mu.Unlock()
	var terminals sync.WaitGroup
	terminals.Add(len(entries))
	for _, entry := range entries {
		entry.stopSnapshotPublisher()
		go func() {
			defer terminals.Done()
			entry.term.Close()
		}()
	}
	// Teardowns already running behind an answered terminal.close share the
	// same bounded deadline, so shutdown never abandons a child mid-escalation.
	pending := h.pendingTeardowns()
	terminalsDone := make(chan struct{})
	go func() {
		terminals.Wait()
		for _, teardown := range pending {
			<-teardown
		}
		close(terminalsDone)
	}()
	timeout := h.terminalCloseTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	timer := time.NewTimer(timeout)
	select {
	case <-terminalsDone:
		if !timer.Stop() {
			<-timer.C
		}
	case <-timer.C:
		closeErr = errors.Join(closeErr, fmt.Errorf("terminal shutdown exceeded %s", timeout))
	}
	if value := os.Getenv("CODELIMA_TEST_DAEMON_SHUTDOWN_DELAY"); value != "" {
		if delay, err := time.ParseDuration(value); err == nil && delay > 0 {
			time.Sleep(delay)
		}
	}
	return closeErr
}

func (h *daemonHost) startForwarding(ctx context.Context) error {
	if h.forwarder == nil {
		return nil
	}
	return h.forwarder.Start(ctx)
}

func (h *daemonHost) startObservation(ctx context.Context) error {
	if h.observer == nil {
		return nil
	}
	return h.observer.StartObservation(ctx)
}

func (h *daemonHost) startVirtioFSReclaimTicker(ctx context.Context) {
	if h.virtioFSReclaimTicker != nil {
		h.virtioFSReclaimTicker.Start(ctx)
	}
}

// persist serializes the capture of terminal state with the write of it.
// Control operations now run concurrently, and two interleaved persists could
// otherwise leave session.json describing an older tab set than the daemon
// actually owns.
//
// Once a live update has committed, this daemon no longer owns the tab set: the
// successor already wrote the authoritative session, and every terminal has
// been removed from this host. Writing again here would replace real recovery
// intent with an empty list, so the persist is skipped instead.
func (h *daemonHost) persist() error {
	if h.replaced.Load() {
		return nil
	}
	h.persistMu.Lock()
	defer h.persistMu.Unlock()
	session := daemon.Session{Version: daemon.SessionVersion, Terminals: h.list()}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(h.session, append(data, '\n'), 0o600)
}

func (h *daemonHost) restoreSession(ctx context.Context) error {
	// "forget" is an instruction to discard persisted terminal intent. Apply
	// it before reading or validating the old schema so stale or corrupt cache
	// data can never prevent the daemon from starting in this mode.
	if h.restore == "forget" {
		return h.persist()
	}

	data, err := os.ReadFile(h.session)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var session daemon.Session
	if err := json.Unmarshal(data, &session); err != nil {
		return h.quarantineSession("corrupt", "quarantined corrupt daemon session", "error", err.Error())
	}
	if session.Version != daemon.SessionVersion {
		return h.quarantineSession(
			fmt.Sprintf("unsupported-v%d", session.Version),
			"quarantined incompatible daemon session",
			"version", session.Version,
		)
	}
	for _, state := range session.Terminals {
		restored, openErr := h.open(ctx, terminalOpenParams{Target: state.Target, Kind: state.Kind, Label: state.Label, Cols: state.Cols, Rows: state.Rows}, state.TerminalID)
		if openErr != nil {
			h.service.log().Error("daemon terminal restore failed", "terminal", state.TerminalID, "error", openErr.Error())
			continue
		}
		if !state.CreatedAt.IsZero() {
			h.mu.Lock()
			if entry := h.terminals[restored.TerminalID]; entry != nil {
				entry.state.CreatedAt = state.CreatedAt
			}
			h.mu.Unlock()
		}
	}
	return h.persist()
}

// quarantineSession preserves unusable terminal restoration metadata beside
// the live session path, writes a fresh empty/current session, and lets daemon
// startup continue. session.json is a recoverable cache of terminal launch
// intent; it must not make the node control plane unavailable.
func (h *daemonHost) quarantineSession(category, message string, attrs ...any) error {
	now := time.Now().UTC()
	if h.service != nil && h.service.now != nil {
		now = h.service.now().UTC()
	}
	quarantinePath := fmt.Sprintf(
		"%s.%s-%s-%s",
		h.session,
		category,
		now.Format("20060102T150405.000000000Z"),
		newID()[:8],
	)
	if err := os.Rename(h.session, quarantinePath); err != nil {
		return fmt.Errorf("quarantine daemon session %s: %w", h.session, err)
	}
	if err := h.persist(); err != nil {
		return fmt.Errorf("initialize daemon session after quarantining %s: %w", quarantinePath, err)
	}

	logAttrs := []any{"path", h.session, "quarantine_path", quarantinePath}
	logAttrs = append(logAttrs, attrs...)
	h.service.log().Warn(message, logAttrs...)
	return nil
}

func (h *daemonHost) handleTerminalEvent(id string, event vaxis.Event) {
	switch value := event.(type) {
	case vaxis.Redraw:
		if entry, err := h.lookup(id); err == nil {
			entry.markSnapshotStale()
		}
	case tuiClipboardEvent:
		tabID := ""
		if entry, err := h.lookup(id); err == nil {
			tabID = entry.state.TabID
		}
		h.broadcast(daemon.EventTerminalClipboard, daemon.TerminalClipboardEvent{TabID: tabID, TerminalID: id, Text: value.Text})
	case tuiTerminalClosedEvent:
		h.mu.Lock()
		entry := h.terminals[id]
		h.ensureTerminalOrderLocked()
		delete(h.terminals, id)
		h.removeTerminalOrderLocked(id)
		h.mu.Unlock()
		if entry != nil {
			entry.stopSnapshotPublisher()
		}
		_ = h.persist()
		if entry != nil {
			h.broadcast(daemon.EventTerminalClosed, entry.state)
		}
	case tuiTerminalErrorEvent:
		h.broadcast(daemon.EventTerminalError, daemon.TerminalErrorEvent{Error: value.Err.Error(), TerminalID: id})
	}
}

// daemonSnapshot lifts a terminal-runtime snapshot into the published protocol
// shape. The cells are copied rather than shared because the published snapshot
// outlives the caller's grid, but no per-cell conversion happens: SnapshotCell
// is the same struct on both sides (see daemon.SnapshotCell), so the cell
// encoding cannot drift between the renderer worker link and the daemon RPC.
func daemonSnapshot(snapshot TerminalSnapshot) daemon.Snapshot {
	return daemon.Snapshot{
		Cols:          snapshot.Cols,
		Rows:          snapshot.Rows,
		Cells:         slices.Clone(snapshot.Cells),
		CursorX:       snapshot.CursorX,
		CursorY:       snapshot.CursorY,
		CursorVisible: snapshot.CursorVisible,
		Generation:    snapshot.Generation,
		CapturesMouse: snapshot.CapturesMouse,
		Stale:         snapshot.Stale,
	}
}

func encodeDaemonKeys(keys []string) ([]byte, error) {
	var data []byte
	for _, key := range keys {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "enter":
			data = append(data, '\r')
		case "escape", "esc":
			data = append(data, 0x1b)
		case "tab":
			data = append(data, '\t')
		case "up":
			data = append(data, "\x1b[A"...)
		case "down":
			data = append(data, "\x1b[B"...)
		case "right":
			data = append(data, "\x1b[C"...)
		case "left":
			data = append(data, "\x1b[D"...)
		default:
			if strings.HasPrefix(strings.ToLower(key), "ctrl+") && len(key) == 6 {
				data = append(data, key[5]&0x1f)
			} else {
				return nil, daemon.Error("InvalidArgument", "unsupported key", ExitInvalidArgument, map[string]any{"key": key})
			}
		}
	}
	return data, nil
}

func toDaemonError(err error) error {
	if err == nil {
		return nil
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		return daemon.Error(string(appErr.Category), appErr.Message, appErr.Code, appErr.Fields)
	}
	var rpcErr *daemon.RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr
	}
	return daemon.Error("Internal", err.Error(), ExitInternalFailure, nil)
}

// enableDaemonFileLogging switches the Service logger (and with it the Store's
// warning sink) to the daemon's rotating file sink at the configured level.
// runDaemon calls it once at startup and defers the returned closer.
//
// Unlike enableFileLogging this deliberately leaves the process-global package
// logger and the limactl subprocess sink alone: both currently write to the
// inherited descriptor that renderer workers and the crash handler also hold,
// and moving them would change where a child's diagnostics land.
func enableDaemonFileLogging(service *Service) (func() error, error) {
	path := filepath.Join(daemon.HomePaths(service.cfg.MetadataRoot).Dir, "daemon.log")
	logger, closeLog, err := newDaemonFileLogger(path, service.logLevel)
	if err != nil {
		return nil, err
	}
	service.SetLogger(logger, service.logLevel)
	return closeLog, nil
}

func runDaemon(ctx context.Context, service *Service) error {
	if err := service.ensureReadyForWrite(ctx); err != nil {
		return err
	}
	// The daemon inherits daemon.log as its stdout/stderr from whoever started
	// it, so without this every structured record it ever writes accumulates in
	// one file that nothing truncates — a daemon lives for days. Failure here is
	// not fatal: logging to the inherited descriptor is what it did before.
	if closeLog, err := enableDaemonFileLogging(service); err != nil {
		service.log().Warn("daemon log rotation unavailable; continuing on the inherited descriptor", "error", err)
	} else {
		defer func() { _ = closeLog() }()
	}
	host := newDaemonHost(service)
	// Request scheduling and timeouts are per delivery class, so the server
	// defaults are correct here: fast control and query work keeps the short
	// RequestTimeout, and node or daemon lifecycle work runs to its own
	// internal budget rather than a blanket request deadline.
	server := daemon.NewServer(daemon.Config{
		Home: service.cfg.MetadataRoot, Version: Version, Handler: host, Logger: service.log(),
	})
	host.wireServerLinks(server)
	// A fresh daemon publishes from the moment it is wired: it has no adopted
	// terminals, so restore starts every publisher on this side of Run exactly
	// as it always has.
	host.markServing()
	if err := host.restoreSession(ctx); err != nil {
		return err
	}
	if err := host.startObservation(ctx); err != nil {
		return fmt.Errorf("start Lima observation: %w", err)
	}
	if err := host.startForwarding(ctx); err != nil {
		return fmt.Errorf("start dynamic forwarding: %w", err)
	}
	host.startVirtioFSReclaimTicker(ctx)
	return server.Run(ctx)
}

func startDaemon(ctx context.Context, service *Service) (daemon.Status, error) {
	if err := service.ensureReadyForWrite(ctx); err != nil {
		return daemon.Status{}, err
	}
	if status, err := daemonclient.Ping(ctx, service.cfg.MetadataRoot, Version); err == nil && status.Running {
		return daemon.Status{}, preconditionFailed("daemon already running", map[string]any{"pid": status.PID})
	}
	if err := recoverDaemonBeforeStart(ctx, service.cfg.MetadataRoot); err != nil {
		return daemon.Status{}, dependencyUnavailable("previous daemon did not release startup ownership", err, nil)
	}
	// Another current daemon may have won the startup race while recovery was
	// waiting for the previous lock owner.
	if status, err := daemonclient.Ping(ctx, service.cfg.MetadataRoot, Version); err == nil && status.Running {
		return daemon.Status{}, preconditionFailed("daemon already running", map[string]any{"pid": status.PID})
	}
	executable, err := os.Executable()
	if err != nil {
		return daemon.Status{}, err
	}
	executable = resolveCodelimaExecutablePath(executable)
	paths := daemon.HomePaths(service.cfg.MetadataRoot)
	logFile, err := os.OpenFile(filepath.Join(paths.Dir, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return daemon.Status{}, err
	}
	command := exec.Command(executable, "--home", service.cfg.MetadataRoot, "daemon", "run")
	command.Env = os.Environ()
	command.Stdout, command.Stderr = logFile, logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return daemon.Status{}, err
	}
	_ = logFile.Close()
	waitCh := make(chan error, 1)
	go func() { waitCh <- command.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, pingErr := daemonclient.Ping(ctx, service.cfg.MetadataRoot, Version)
		if pingErr == nil {
			return status, nil
		}
		select {
		case waitErr := <-waitCh:
			return daemon.Status{}, externalCommandFailed("daemon exited before becoming ready", waitErr, nil)
		case <-ctx.Done():
			_ = command.Process.Kill()
			<-waitCh
			return daemon.Status{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	_ = command.Process.Kill()
	<-waitCh
	return daemon.Status{}, dependencyUnavailable("daemon did not become ready within 5 seconds", nil, nil)
}

func recoverDaemonBeforeStart(ctx context.Context, home string) error {
	paths := daemon.HomePaths(home)
	available, err := daemonLockAvailable(paths.Lock)
	if err != nil || available {
		return err
	}
	identity, identityErr := readDaemonIdentity(home)
	if identityErr != nil || !recoverableDaemonIdentity(identity) {
		return waitForDaemonShutdown(ctx, home, daemonStartRecoveryTimeout)
	}

	client, dialErr := daemonclient.Dial(ctx, daemonclient.Options{
		Home: home, Version: identity.Version, Protocol: identity.Protocol, Timeout: 2 * time.Second,
	})
	terminalCount := 0
	if dialErr == nil {
		var status daemon.Status
		_ = client.Call(ctx, "daemon.status", nil, &status)
		terminalCount = status.TerminalCount
		var stopped map[string]bool
		stopErr := client.Call(ctx, "daemon.stop", nil, &stopped)
		_ = client.Close()
		if stopErr != nil {
			return fmt.Errorf("stop previous daemon before start: %w", stopErr)
		}
	}
	return finishDaemonShutdown(ctx, home, identity, daemonShutdownTimeout(terminalCount))
}

func daemonStatus(ctx context.Context, service *Service) (daemon.Status, error) {
	status, err := daemonclient.Ping(ctx, service.cfg.MetadataRoot, Version)
	if err != nil {
		return daemon.Status{}, dependencyUnavailable("daemon not running (codelima daemon start)", err, nil)
	}
	return status, nil
}

func stopDaemon(ctx context.Context, service *Service) (map[string]bool, error) {
	client, err := daemonclient.Dial(ctx, daemonclient.Options{Home: service.cfg.MetadataRoot, Version: Version})
	if err != nil {
		return nil, dependencyUnavailable("daemon not running (codelima daemon start)", err, nil)
	}
	defer func() { _ = client.Close() }()
	result := map[string]bool{}
	if err := client.Call(ctx, "daemon.stop", nil, &result); err != nil {
		return nil, fromDaemonError(err)
	}
	return result, nil
}

func fromDaemonError(err error) error {
	var rpcErr *daemon.RPCError
	if errors.As(err, &rpcErr) {
		return newAppError(ErrorCategory(rpcErr.Category), rpcErr.Message, rpcErr.Code, nil, rpcErr.Fields)
	}
	return err
}

type quiescedTerminal struct {
	id    string
	entry *daemonTerminalEntry
	term  handoffDaemonTerminal
	state handoffTerminalState
}

// wireServerLinks publishes the server-backed callbacks onto the host. It must
// run before the host owns any terminal entry: entries start goroutines that
// read these fields, and those reads are ordered only by having been written
// before the goroutine was created.
//
// Publication is gated on serving because Server.Broadcast reads the daemon
// epoch that Server.Run writes while it binds its sockets. An imported daemon
// owns live terminals before Run starts, so their publishers would otherwise
// read that state concurrently. Nothing can be subscribed before the server
// serves, so a suppressed event has no audience.
func (h *daemonHost) wireServerLinks(server *daemon.Server) {
	h.broadcast = func(name string, data any) {
		if !h.serving.Load() {
			return
		}
		server.Broadcast(name, data)
	}
	// The forwarder samples per-node usage on its own goroutines and must be
	// able to publish it without knowing about the server. It gets the same
	// gated publisher, wired here rather than in the constructor for the reason
	// above: it is only safe to publish once the server exists, and startForwarding
	// always runs after this.
	if h.forwarder != nil {
		h.forwarder.setBroadcast(h.broadcast)
	}
	h.prepareReplacement = server.PrepareReplacement
	h.resumeReplacement = server.ResumeAfterReplacement
	h.stopServer = server.Stop
}

// markServing opens publication and wakes every publisher, so state that
// changed while events were suppressed is republished instead of lost with
// them.
func (h *daemonHost) markServing() {
	h.serving.Store(true)
	h.mu.RLock()
	entries := make([]*daemonTerminalEntry, 0, len(h.terminals))
	for _, entry := range h.terminals {
		entries = append(entries, entry)
	}
	h.mu.RUnlock()
	for _, entry := range entries {
		entry.requestSnapshot()
	}
}

// handoffAdopter builds a live terminal around a handed-off PTY. It is a seam
// so adoption ordering can be exercised without the renderer worker binary.
type handoffAdopter func(
	targetKey string,
	postEvent func(vaxis.Event),
	pty *os.File,
	childPID, cols, rows int,
	replay []byte,
	replayPartial bool,
) (daemonTerminal, error)

// adoptHandoffTerminals installs the handed-off runtimes on the importing host.
// Adoption starts renderer supervisors that immediately deliver callbacks, and
// those callbacks read h.terminals under h.mu, so every entry is published
// under the same lock rather than written into a bare map. fdsByID loses each
// descriptor it hands over; whatever remains is still owned by the caller.
func (h *daemonHost) adoptHandoffTerminals(
	manifest daemon.HandoffManifest,
	fdsByID map[string]int,
	adopt handoffAdopter,
) ([]handoffDaemonTerminal, error) {
	states := make(map[string]daemon.TerminalState, len(manifest.Session.Terminals))
	for _, state := range manifest.Session.Terminals {
		states[state.TerminalID] = state
	}
	var imported []handoffDaemonTerminal
	release := func() {
		h.mu.RLock()
		entries := make([]*daemonTerminalEntry, 0, len(h.terminals))
		for _, entry := range h.terminals {
			entries = append(entries, entry)
		}
		h.mu.RUnlock()
		for _, entry := range entries {
			entry.stopSnapshotPublisher()
		}
		for _, term := range imported {
			term.ReleaseAfterHandoff()
		}
	}
	for _, runtimeState := range manifest.Runtimes {
		state, ok := states[runtimeState.TerminalID]
		fd, fdOK := fdsByID[runtimeState.TerminalID]
		if !ok || !fdOK {
			release()
			return nil, errors.New("handoff terminal metadata does not match fds")
		}
		delete(fdsByID, runtimeState.TerminalID)
		ptyFile := os.NewFile(uintptr(fd), "codelima-imported-pty")
		term, adoptErr := adopt(
			state.TabID,
			func(event vaxis.Event) { h.handleTerminalEvent(state.TerminalID, event) },
			ptyFile,
			runtimeState.ChildPID,
			runtimeState.Cols,
			runtimeState.Rows,
			runtimeState.Replay,
			runtimeState.ReplayPartial,
		)
		if adoptErr != nil {
			_ = ptyFile.Close()
			release()
			return nil, adoptErr
		}
		handoff, ok := term.(handoffDaemonTerminal)
		if !ok {
			term.Close()
			release()
			return nil, errors.New("adopted terminal does not support handoff release")
		}
		imported = append(imported, handoff)
		h.mu.Lock()
		h.terminals[state.TerminalID] = h.newTerminalEntry(state, term)
		h.mu.Unlock()
	}
	// The manifest session carries the daemon-owned tab order, which is not the
	// runtime order, so the explicit order is rebuilt from it once every entry
	// is published.
	h.mu.Lock()
	h.terminalOrder = h.terminalOrder[:0]
	for _, state := range manifest.Session.Terminals {
		if _, ok := h.terminals[state.TerminalID]; ok {
			h.terminalOrder = append(h.terminalOrder, state.TerminalID)
		}
	}
	h.mu.Unlock()
	return imported, nil
}

func (h *daemonHost) update(binaryPath string) (map[string]any, error) {
	if binaryPath == "" {
		return nil, daemon.Error("InvalidArgument", "daemon update requires the caller binary path", ExitInvalidArgument, nil)
	}
	binaryPath = resolveCodelimaExecutablePath(binaryPath)
	if info, err := os.Stat(binaryPath); err != nil || info.IsDir() {
		return nil, daemon.Error("InvalidArgument", "daemon update binary is unavailable", ExitInvalidArgument, map[string]any{"path": binaryPath})
	}

	// The barrier is held for the whole handoff, so the quiesced runtimes and
	// the manifest session describe the same tab set and no concurrently
	// dispatched terminal.open can add a tab after either was captured.
	h.updateGate.Lock()
	defer h.updateGate.Unlock()
	if h.replaced.Load() {
		return nil, daemon.Error(
			"PreconditionFailed",
			"daemon was already replaced by a live update",
			ExitPreconditionFailed,
			map[string]any{"daemon_replaced": true},
		)
	}

	h.broadcast(daemon.EventDaemonUpdateStarting, nil)
	h.mu.RLock()
	entries := make([]*daemonTerminalEntry, 0, len(h.terminals))
	for _, id := range h.terminalIDsInOrderLocked() {
		entries = append(entries, h.terminals[id])
	}
	h.mu.RUnlock()
	quiesced := make([]quiescedTerminal, 0, len(entries))
	rollback := func(cause error, prepared bool, process *os.Process) error {
		if process != nil {
			_ = process.Kill()
			_, _ = process.Wait()
		}
		if prepared {
			_ = h.resumeReplacement()
		}
		for _, item := range quiesced {
			if item.state.PTY != nil {
				_ = item.state.PTY.Close()
			}
			_ = item.term.RollbackHandoff()
		}
		h.broadcast(daemon.EventDaemonUpdateFailed, daemon.DaemonUpdateFailedEvent{Error: cause.Error()})
		var fields map[string]any
		if errors.Is(cause, daemon.ErrHandoffTransportUnsupported) {
			fields = map[string]any{"handoff_transport_unsupported": true}
		}
		return daemon.Error("ExternalCommandFailed", "daemon live update failed: "+cause.Error(), ExitExternalFailure, fields)
	}
	for _, entry := range entries {
		term, ok := entry.term.(handoffDaemonTerminal)
		if !ok {
			return nil, rollback(errors.New("terminal backend does not support live handoff"), false, nil)
		}
		state := term.BeginHandoff()
		if state.Err != nil {
			return nil, rollback(state.Err, false, nil)
		}
		quiesced = append(quiesced, quiescedTerminal{id: entry.state.TerminalID, entry: entry, term: term, state: state})
	}

	token := strings.ReplaceAll(newID(), "-", "")
	handoffPath := filepath.Join(daemon.HomePaths(h.service.cfg.MetadataRoot).Dir, fmt.Sprintf("handoff-%d.sock", os.Getpid()))
	_ = os.Remove(handoffPath)
	listener, err := daemon.ListenHandoff(handoffPath)
	if err != nil {
		return nil, rollback(err, false, nil)
	}
	defer func() { _ = listener.Close(); _ = os.Remove(handoffPath) }()
	_ = os.Chmod(handoffPath, 0o600)
	logPath := filepath.Join(daemon.HomePaths(h.service.cfg.MetadataRoot).Dir, "daemon-import.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, rollback(err, false, nil)
	}
	command := exec.Command(binaryPath, "--home", h.service.cfg.MetadataRoot, "daemon", "import", "--handoff", handoffPath, "--token", token)
	command.Stdout, command.Stderr = logFile, logFile
	command.Env = os.Environ()
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return nil, rollback(err, false, nil)
	}
	_ = logFile.Close()
	process := command.Process
	_ = listener.SetDeadline(time.Now().Add(30 * time.Second))
	conn, err := listener.AcceptUnix()
	if err != nil {
		return nil, rollback(err, false, process)
	}
	defer func() { _ = conn.Close() }()
	if err := daemon.RequireSameUserPeer(conn); err != nil {
		return nil, rollback(err, false, process)
	}
	peer := daemon.NewHandoffConnection(conn, daemon.HandoffFramingLengthPrefixed)
	message, err := peer.ReadControlMessage()
	if err != nil || message.Type != "hello" || message.Token != token {
		if err == nil {
			err = errors.New("invalid handoff authentication")
		}
		return nil, rollback(err, false, process)
	}
	manifest := daemon.HandoffManifest{Version: daemon.HandoffVersion, BinaryVersion: Version, Token: token, Session: daemon.Session{Version: daemon.SessionVersion, Terminals: h.list()}}
	totalReplayBytes := 0
	for _, item := range quiesced {
		if len(item.state.Replay) > daemon.MaxHandoffReplayBytesPerTerminal {
			return nil, rollback(fmt.Errorf(
				"handoff replay for %s is %d bytes, limit is %d",
				item.id,
				len(item.state.Replay),
				daemon.MaxHandoffReplayBytesPerTerminal,
			), false, process)
		}
		totalReplayBytes += len(item.state.Replay)
		if totalReplayBytes > daemon.MaxHandoffTotalReplayBytes {
			return nil, rollback(fmt.Errorf(
				"handoff replay is %d bytes, total limit is %d",
				totalReplayBytes,
				daemon.MaxHandoffTotalReplayBytes,
			), false, process)
		}
		manifest.Runtimes = append(manifest.Runtimes, daemon.HandoffRuntime{
			TerminalID: item.id, ChildPID: item.state.ChildPID,
			Cols: item.state.Cols, Rows: item.state.Rows,
			ReplaySize: len(item.state.Replay), ReplayPartial: item.state.ReplayPartial,
		})
	}
	if err := peer.WriteJSON(manifest, nil); err != nil {
		return nil, rollback(err, false, process)
	}
	for _, item := range quiesced {
		if err := peer.WriteReplay(item.id, item.state.Replay); err != nil {
			return nil, rollback(err, false, process)
		}
	}
	for start := 0; start < len(quiesced); start += daemon.MaxHandoffFDsPerFrame {
		end := min(start+daemon.MaxHandoffFDsPerFrame, len(quiesced))
		ids := make([]string, 0, end-start)
		fds := make([]int, 0, end-start)
		for _, item := range quiesced[start:end] {
			ids = append(ids, item.id)
			fds = append(fds, int(item.state.PTY.Fd()))
		}
		if err := peer.WriteJSON(daemon.HandoffMessage{Type: "fds", TerminalIDs: ids}, fds); err != nil {
			return nil, rollback(err, false, process)
		}
	}
	message, err = peer.ReadControlMessage()
	if err != nil || message.Type != "ready" {
		if err == nil {
			err = errors.New(message.Error)
		}
		return nil, rollback(err, false, process)
	}
	if err := h.prepareReplacement(); err != nil {
		return nil, rollback(err, false, process)
	}
	prepared := true
	if err := peer.WriteJSON(daemon.HandoffMessage{Type: "commit", Token: token}, nil); err != nil {
		return nil, rollback(err, prepared, process)
	}
	message, err = peer.ReadControlMessage()
	if err != nil || message.Type != "committed" {
		if err == nil {
			err = errors.New(message.Error)
		}
		return nil, rollback(err, prepared, process)
	}

	// The successor has committed and has already written session.json for the
	// tabs it adopted. Mark this daemon replaced before its own tab set is torn
	// down, so neither the teardown below nor Close can rewrite that file with
	// the empty list this daemon is left holding.
	h.replaced.Store(true)
	h.mu.Lock()
	for _, item := range quiesced {
		delete(h.terminals, item.id)
	}
	h.mu.Unlock()
	for _, item := range quiesced {
		_ = item.state.PTY.Close()
		item.term.ReleaseAfterHandoff()
	}
	h.broadcast(daemon.EventDaemonUpdateCommitted, daemon.DaemonUpdateCommittedEvent{PID: message.PID})
	time.AfterFunc(10*time.Millisecond, h.stopServer)
	return map[string]any{"updated": true, "live_handoff": true, "terminals": len(quiesced)}, nil
}

func importDaemon(ctx context.Context, service *Service, handoffPath, token string) error {
	return runDaemonImport(ctx, service, handoffPath, token, adoptIsolatedDaemonTerminal)
}

// runDaemonImport is the receiving half of a live update. adopt is a parameter
// so the handoff protocol, the adoption ordering and the session-integrity
// window can be exercised without a renderer worker binary.
func runDaemonImport(ctx context.Context, service *Service, handoffPath, token string, adopt handoffAdopter) error {
	if handoffPath == "" || token == "" {
		return invalidArgument("daemon import requires --handoff and --token", nil)
	}
	peer, err := daemon.DialHandoff(handoffPath)
	if err != nil {
		return err
	}
	defer func() { _ = peer.Close() }()
	if err := peer.WriteJSON(daemon.HandoffMessage{Type: "hello", Token: token}, nil); err != nil {
		return err
	}
	manifest, err := peer.ReadManifest()
	if err != nil {
		return err
	}
	validHandoffVersion := manifest.Version == daemon.HandoffVersion
	if peer.Framing == daemon.HandoffFramingLengthPrefixed {
		validHandoffVersion = validHandoffVersion || manifest.Version == daemon.PreviousStreamHandoffVersion
	} else {
		validHandoffVersion = manifest.Version == daemon.LegacyHandoffVersion
	}
	if !validHandoffVersion || manifest.Token != token || manifest.Session.Version != daemon.SessionVersion {
		_ = peer.WriteJSON(daemon.HandoffMessage{Type: "failed", Error: "invalid handoff manifest"}, nil)
		return errors.New("invalid handoff manifest")
	}
	runtimeIDs := make(map[string]struct{}, len(manifest.Runtimes))
	totalReplayBytes := 0
	for index := range manifest.Runtimes {
		runtimeState := &manifest.Runtimes[index]
		if runtimeState.TerminalID == "" {
			return errors.New("handoff runtime has an empty terminal id")
		}
		if _, duplicate := runtimeIDs[runtimeState.TerminalID]; duplicate {
			return errors.New("handoff manifest has duplicate terminal runtimes")
		}
		runtimeIDs[runtimeState.TerminalID] = struct{}{}
		replayBytes := len(runtimeState.Replay)
		if manifest.Version == daemon.HandoffVersion {
			if replayBytes != 0 {
				return errors.New("chunked handoff manifest contains inline replay")
			}
			replayBytes = runtimeState.ReplaySize
		}
		if replayBytes < 0 || replayBytes > daemon.MaxHandoffReplayBytesPerTerminal {
			return fmt.Errorf(
				"handoff replay for %s is %d bytes, limit is %d",
				runtimeState.TerminalID,
				replayBytes,
				daemon.MaxHandoffReplayBytesPerTerminal,
			)
		}
		totalReplayBytes += replayBytes
		if totalReplayBytes > daemon.MaxHandoffTotalReplayBytes {
			return fmt.Errorf(
				"handoff replay is %d bytes, total limit is %d",
				totalReplayBytes,
				daemon.MaxHandoffTotalReplayBytes,
			)
		}
	}
	if manifest.Version == daemon.HandoffVersion {
		for index := range manifest.Runtimes {
			runtimeState := &manifest.Runtimes[index]
			replay, readErr := peer.ReadReplay(runtimeState.TerminalID, runtimeState.ReplaySize)
			if readErr != nil {
				return readErr
			}
			runtimeState.Replay = replay
		}
	}
	fdsByID := map[string]int{}
	closePendingFDs := func() {
		for id, fd := range fdsByID {
			_ = unix.Close(fd)
			delete(fdsByID, id)
		}
	}
	defer closePendingFDs()
	for len(fdsByID) < len(manifest.Runtimes) {
		message, fds, readErr := peer.ReadMessage()
		if readErr != nil {
			return readErr
		}
		if message.Type != "fds" || len(message.TerminalIDs) != len(fds) {
			daemon.CloseHandoffFDs(fds)
			return errors.New("invalid handoff fd batch")
		}
		batchIDs := make(map[string]struct{}, len(message.TerminalIDs))
		for _, id := range message.TerminalIDs {
			if _, expected := runtimeIDs[id]; !expected {
				daemon.CloseHandoffFDs(fds)
				return errors.New("handoff fd batch names an unknown terminal")
			}
			if _, duplicate := fdsByID[id]; duplicate {
				daemon.CloseHandoffFDs(fds)
				return errors.New("handoff fd batch duplicates a terminal")
			}
			if _, duplicate := batchIDs[id]; duplicate {
				daemon.CloseHandoffFDs(fds)
				return errors.New("handoff fd batch duplicates a terminal")
			}
			batchIDs[id] = struct{}{}
		}
		for i, id := range message.TerminalIDs {
			fdsByID[id] = fds[i]
		}
	}
	// A live-updated daemon is exactly the long-lived one, and its inherited
	// stdout/stderr is a per-update daemon-import.log that nothing ever
	// truncates. Move its structured records onto the same rotating daemon.log
	// the freshly started daemon uses, before the server captures the logger.
	if closeLog, logErr := enableDaemonFileLogging(service); logErr != nil {
		service.log().Warn("daemon log rotation unavailable; continuing on the inherited descriptor", "error", logErr)
	} else {
		defer func() { _ = closeLog() }()
	}
	host := newDaemonHost(service)
	// The server links are published before the first terminal entry exists.
	// newTerminalEntry starts a publisher goroutine that reads host.broadcast
	// without further synchronization, so assigning it afterwards would let a
	// goroutine observe a partially wired host.
	server := daemon.NewServer(daemon.Config{
		Home: service.cfg.MetadataRoot, Version: Version, Handler: host, Logger: service.log(),
	})
	host.wireServerLinks(server)
	imported, err := host.adoptHandoffTerminals(manifest, fdsByID, adopt)
	if err != nil {
		_ = peer.WriteJSON(daemon.HandoffMessage{Type: "failed", Error: err.Error()}, nil)
		return err
	}
	releaseImported := func() {
		for _, term := range imported {
			term.ReleaseAfterHandoff()
		}
	}
	if os.Getenv("CODELIMA_HANDOFF_FAIL_IMPORT") == "1" {
		releaseImported()
		_ = peer.WriteJSON(daemon.HandoffMessage{Type: "failed", Error: "injected import failure"}, nil)
		return errors.New("injected import failure")
	}
	if err := peer.WriteJSON(daemon.HandoffMessage{Type: "ready"}, nil); err != nil {
		releaseImported()
		return err
	}
	message, err := peer.ReadControlMessage()
	if err != nil || message.Type != "commit" || message.Token != token {
		releaseImported()
		if err == nil {
			err = errors.New("handoff commit was not authorized")
		}
		return err
	}
	for _, term := range imported {
		activatable, ok := term.(interface{ ActivateAfterHandoff() })
		if !ok {
			releaseImported()
			return errors.New("imported terminal cannot activate after handoff")
		}
		activatable.ActivateAfterHandoff()
	}
	// session.json is recovery intent for the tab set this daemon now owns.
	// Write it before the predecessor is told the handoff committed: from that
	// moment the predecessor may exit, and a crash in between must still find
	// every handed-off tab on disk. A failed write is reported as a handoff
	// failure so the predecessor rolls back with its own session intact, rather
	// than leaving the tab set unrecoverable.
	if err := host.persist(); err != nil {
		releaseImported()
		_ = peer.WriteJSON(daemon.HandoffMessage{Type: "failed", Error: err.Error()}, nil)
		return fmt.Errorf("persist imported daemon session: %w", err)
	}
	if err := host.startObservation(ctx); err != nil {
		releaseImported()
		return fmt.Errorf("start Lima observation: %w", err)
	}
	if err := host.startForwarding(ctx); err != nil {
		releaseImported()
		return fmt.Errorf("start dynamic forwarding: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, pingErr := daemonclient.Ping(ctx, service.cfg.MetadataRoot, Version); pingErr == nil {
			// The server is answering, so the adopted terminals may publish.
			host.markServing()
			if err := peer.WriteJSON(daemon.HandoffMessage{Type: "committed", PID: os.Getpid()}, nil); err != nil {
				server.Stop()
				return err
			}
			host.startVirtioFSReclaimTicker(ctx)
			return <-done
		}
		select {
		case runErr := <-done:
			_ = peer.WriteJSON(daemon.HandoffMessage{Type: "failed", Error: runErr.Error()}, nil)
			return runErr
		case <-ctx.Done():
			server.Stop()
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	server.Stop()
	return errors.New("imported daemon did not become ready")
}
