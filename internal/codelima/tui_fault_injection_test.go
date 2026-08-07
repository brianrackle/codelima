package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"github.com/brianrackle/codelima/internal/testutil"
)

// hungDaemonCaller answers every RPC by blocking until the test releases it.
// It stands in for a daemon that has stopped servicing its mutation lane.
type hungDaemonCaller struct {
	release chan struct{}
	started chan string
}

func newHungDaemonCaller() *hungDaemonCaller {
	return &hungDaemonCaller{release: make(chan struct{}), started: make(chan string, 16)}
}

func (c *hungDaemonCaller) Call(ctx context.Context, method string, _ any, _ any) error {
	select {
	case c.started <- method:
	default:
	}
	select {
	case <-c.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *hungDaemonCaller) awaitCall(t *testing.T, want string) {
	t.Helper()

	select {
	case got := <-c.started:
		if got != want {
			t.Fatalf("daemon RPC = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for the %q RPC to start", want)
	}
}

// I1: Draw calls Resize on every frame. A hung daemon must not turn a redraw
// into a stall, so Resize only records the latest geometry.
func TestDaemonTUITerminalResizeDoesNotBlockDrawOnHungDaemon(t *testing.T) {
	t.Parallel()

	caller := newHungDaemonCaller()
	defer close(caller.release)

	term := &daemonTUITerminal{client: caller, id: "term-1", stop: make(chan struct{})}
	t.Cleanup(term.Detach)

	returned := make(chan struct{})
	go func() {
		term.Resize(80, 24)
		term.Resize(120, 40)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("Resize blocked the draw path on the hung daemon resize RPC")
	}
	caller.awaitCall(t, "terminal.resize")

	// The reassert loop keeps the latest geometry pending instead of retrying
	// once per redraw; the outstanding RPC never bounces back into Draw.
	term.Resize(200, 60)
}

// I1: focus transitions are driven from key, mouse, and tab handling, so
// terminal.focus must not run on the event loop either.
func TestDaemonTUITerminalFocusDoesNotBlockEventLoopOnHungDaemon(t *testing.T) {
	t.Parallel()

	caller := newHungDaemonCaller()
	defer close(caller.release)

	term := &daemonTUITerminal{client: caller, id: "term-1", stop: make(chan struct{})}
	t.Cleanup(term.Detach)

	returned := make(chan struct{})
	go func() {
		term.Focus()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("Focus blocked the event loop on the hung daemon focus RPC")
	}
	caller.awaitCall(t, "terminal.focus")
}

// hangingTerminalOpenHandler is a daemon whose terminal.open never returns
// until the test releases it.
type hangingTerminalOpenHandler struct {
	release chan struct{}
	opened  chan daemon.TerminalState
	mu      sync.Mutex
	opens   map[string]int
}

func (h *hangingTerminalOpenHandler) openCount(target string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.opens[target]
}

func (h *hangingTerminalOpenHandler) Handle(ctx context.Context, _ daemon.ClientContext, method string, params json.RawMessage) (any, error) {
	switch method {
	case "terminal.open":
		var request struct {
			Target string `json:"target"`
			Kind   string `json:"kind"`
			Label  string `json:"label"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, err
		}
		state := daemon.TerminalState{
			TerminalID: "term-" + request.Target,
			TabID:      request.Target + "#1",
			Target:     request.Target,
			Kind:       request.Kind,
			Label:      request.Label,
			CreatedAt:  time.Now(),
			Cols:       80,
			Rows:       24,
		}
		h.mu.Lock()
		h.opens[request.Target]++
		h.mu.Unlock()
		select {
		case h.opened <- state:
		default:
		}
		select {
		case <-h.release:
			return state, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case "terminal.list":
		return []daemon.TerminalState{}, nil
	default:
		return map[string]bool{"ok": true}, nil
	}
}

func (*hangingTerminalOpenHandler) Snapshot(context.Context) (any, error) {
	return map[string]any{"session": daemon.Session{Version: daemon.SessionVersion}}, nil
}

func (*hangingTerminalOpenHandler) TerminalCount() int { return 0 }
func (*hangingTerminalOpenHandler) Close() error       { return nil }

// I1: terminal.open fires on every selection move onto a running node. Against a
// daemon that never answers, the event loop must keep handling keys, and the
// once-per-second implicit open must not stack requests.
func TestTUISelectionMoveDoesNotBlockOnHungDaemonTerminalOpen(t *testing.T) {
	t.Parallel()

	home := testutil.TempDir(t, "tui-hung-open-")
	handler := &hangingTerminalOpenHandler{
		release: make(chan struct{}),
		opened:  make(chan daemon.TerminalState, 8),
		opens:   map[string]int{},
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(handler.release) }) }

	server := daemon.NewServer(daemon.Config{Home: home, Version: Version, Handler: handler})
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-serverDone; err != nil {
			t.Errorf("Server.Run() error = %v", err)
		}
	})
	// Registered after the server teardown so it runs before it: a handler still
	// parked on release must be let go before Run is awaited.
	t.Cleanup(release)
	waitForCondition(t, 2*time.Second, func() bool {
		_, err := daemonclient.Ping(context.Background(), home, Version)
		return err == nil
	}, "daemon startup")

	requestClient, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version, WantInput: true})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(DefaultConfig(home), newFakeSandbox(), nil, ioDiscard{}, ioDiscard{})
	events := make(chan vaxis.Event, 64)
	store := newTUISessionStore(ctx, service, func(event vaxis.Event) { events <- event })
	t.Cleanup(func() {
		store.Close()
		_ = requestClient.Close()
	})
	// Attach the client after construction so no reconnect supervisor runs. Its
	// handshake posts an authoritative snapshot that holds no terminals, and
	// whether that lands before or after the opened-tab event is pure timing —
	// this test is about the open path, not about synchronization ordering.
	service.daemonClient = requestClient
	store.daemonReady.Store(true)

	nodes := []Node{
		{ID: "node-a", Slug: "node-a", Status: NodeStatusRunning},
		{ID: "node-b", Slug: "node-b", Status: NodeStatusRunning},
	}
	state, err := newTUIState(nodes, store)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	app := &vaxisTUIApp{
		ctx:        ctx,
		service:    service,
		state:      state,
		sessions:   store,
		postEvent:  func(event vaxis.Event) { events <- event },
		operations: map[string]*tuiOperationState{},
		messages:   newTUIMessageLog(20),
	}

	start := time.Now()
	app.handleKey(vaxis.Key{Keycode: vaxis.KeyDown})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("selection move waited %s on the hung terminal.open", elapsed)
	}
	if got := app.state.selectedEntry().node.ID; got != "node-b" {
		t.Fatalf("selection = %q, want node-b", got)
	}
	select {
	case <-handler.opened:
	case <-time.After(2 * time.Second):
		t.Fatal("selection move did not issue terminal.open")
	}

	// The loop keeps running while the open is outstanding, and returning to the
	// same node does not stack another open for it.
	targetKey := terminal.NodeTarget("node-b").String()
	for range 3 {
		start = time.Now()
		app.handleKey(vaxis.Key{Keycode: vaxis.KeyUp})
		app.handleKey(vaxis.Key{Keycode: vaxis.KeyDown})
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("key handling waited %s behind the hung terminal.open", elapsed)
		}
	}
	if got := handler.openCount(targetKey); got != 1 {
		t.Fatalf("terminal.open requests for %s = %d, want 1 while one is outstanding", targetKey, got)
	}

	release()
	awaitAsyncTUIState(t, app, events, func() bool {
		return len(store.TargetSessionKeys(targetKey)) == 1
	}, "the released terminal.open to install its tab")
}

// I1: submitting a configuration dialog acquires a global flock. While another
// process holds it the event loop must keep processing keys.
func TestTUIDialogSubmitUnderHeldLockKeepsEventLoopResponsive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	if err := service.ensureReadyForWrite(ctx); err != nil {
		t.Fatalf("ensureReadyForWrite() error = %v", err)
	}
	if _, err := service.NodeCreate(ctx, NodeCreateInput{Slug: "root-node", Directory: workspace}); err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	locks, err := acquireLockSet(ctx, service.cfg.MetadataRoot, nil, []lockKey{lockConfigurations, lockEnvironments}, nil)
	if err != nil {
		t.Fatalf("acquireLockSet() error = %v", err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			locks.release()
		}
	}
	t.Cleanup(release)

	app, events := newAsyncTestTUIApp(t, ctx, service)
	app.messages = newTUIMessageLog(50)
	app.openCreateConfigurationDialog()
	app.activeDialog().SetFieldValue("slug", "locked-recipe")

	start := time.Now()
	if quit, err := app.handleEvent(vaxis.Key{Keycode: vaxis.KeyEnter}); err != nil || quit {
		t.Fatalf("handleEvent(dialog submit) = (%v, %v), want (false, nil)", quit, err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("dialog submit held the event loop for %s", elapsed)
	}
	if len(app.operations) != 1 {
		t.Fatalf("dialog submit registered %d background operations, want 1", len(app.operations))
	}

	// The loop is still live while the mutation waits on the flock.
	start = time.Now()
	if quit, err := app.handleEvent(vaxis.Key{Keycode: vaxis.KeyDown}); err != nil || quit {
		t.Fatalf("handleEvent(navigation) = (%v, %v), want (false, nil)", quit, err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("navigation key waited %s behind the blocked mutation", elapsed)
	}

	release()
	awaitQuiescentTUI(t, app, events, "blocked configuration create to finish")
	if _, err := service.ConfigurationShow(ctx, "locked-recipe"); err != nil {
		t.Fatalf("ConfigurationShow(locked-recipe) error = %v", err)
	}
}

// awaitQuiescentTUI waits until the app has no background work outstanding.
//
// The load-bearing half is len(app.operations) == 0: an operation is removed
// only in finishOperation, which runs after its goroutine has recorded and
// posted its completion, so an empty map proves no goroutine is still writing
// inside the service's temporary home. Tests must reach that state before they
// return, or a lingering writer races t.TempDir's RemoveAll and fails cleanup
// with "directory not empty". The refresh flag is included because a reload
// is the other goroutine an applied result spawns; it only ever reads
// (NodeList reconciles with persist=false), so it cannot corrupt cleanup, but
// waiting for it keeps the test from leaking a live goroutine either.
func awaitQuiescentTUI(t *testing.T, app *vaxisTUIApp, events <-chan vaxis.Event, description string) {
	t.Helper()

	awaitAsyncTUIState(t, app, events, func() bool {
		return len(app.operations) == 0 && !app.refreshInFlight
	}, description)
}

// hangingTerminalListHandler is a daemon whose terminal.list never answers.
type hangingTerminalListHandler struct {
	release chan struct{}
	listed  chan struct{}
}

func (h *hangingTerminalListHandler) Handle(ctx context.Context, _ daemon.ClientContext, method string, _ json.RawMessage) (any, error) {
	if method != "terminal.list" {
		return map[string]bool{"ok": true}, nil
	}
	select {
	case h.listed <- struct{}{}:
	default:
	}
	select {
	case <-h.release:
		return []daemon.TerminalState{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (*hangingTerminalListHandler) Snapshot(context.Context) (any, error) {
	return map[string]any{"session": daemon.Session{Version: daemon.SessionVersion}}, nil
}

func (*hangingTerminalListHandler) TerminalCount() int { return 0 }
func (*hangingTerminalListHandler) Close() error       { return nil }

// I2: startup adoption of the daemon's tabs is bounded. A daemon that never
// answers terminal.list must degrade to "no restored tabs" with a visible
// reason, not hang the TUI before its event loop even starts.
func TestTUISessionStoreRestoreDegradesWhenTerminalListHangs(t *testing.T) {
	t.Parallel()

	home := testutil.TempDir(t, "tui-hung-list-")
	handler := &hangingTerminalListHandler{release: make(chan struct{}), listed: make(chan struct{}, 2)}
	server := daemon.NewServer(daemon.Config{Home: home, Version: Version, Handler: handler})
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-serverDone; err != nil {
			t.Errorf("Server.Run() error = %v", err)
		}
	})
	// Registered after the server teardown so it runs before it: a handler still
	// parked on release must be let go before Run is awaited.
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(handler.release) }) })
	waitForCondition(t, 2*time.Second, func() bool {
		_, err := daemonclient.Ping(context.Background(), home, Version)
		return err == nil
	}, "daemon startup")

	requestClient, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version, WantInput: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = requestClient.Close() })

	// Attach the daemon client after construction so the restore runs under the
	// test's own bound rather than the ten-second production one.
	service := NewService(DefaultConfig(home), newFakeSandbox(), nil, ioDiscard{}, ioDiscard{})
	events := make(chan vaxis.Event, 8)
	store := newTUISessionStore(ctx, service, func(event vaxis.Event) { events <- event })
	t.Cleanup(store.Close)
	service.daemonClient = requestClient
	store.daemonReady.Store(true)
	store.restoreTimeout = 200 * time.Millisecond

	start := time.Now()
	store.restoreDaemonSessions()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("startup tab restore waited %s on the hung daemon", elapsed)
	}
	select {
	case <-handler.listed:
	case <-time.After(2 * time.Second):
		t.Fatal("restore did not issue terminal.list")
	}
	if len(store.sessions) != 0 {
		t.Fatalf("degraded restore adopted %d tabs, want none", len(store.sessions))
	}

	select {
	case event := <-events:
		failure, ok := event.(tuiTerminalErrorEvent)
		if !ok {
			t.Fatalf("posted event = %T, want tuiTerminalErrorEvent", event)
		}
		if failure.Err == nil || !strings.Contains(failure.Err.Error(), "restore daemon terminal tabs") {
			t.Fatalf("degraded restore error = %v", failure.Err)
		}
	default:
		t.Fatal("degraded restore did not surface a reason")
	}
}

// I2: auto-refresh must survive a dropped tuiRefreshCompleteEvent. vaxis
// PostEvent discards events once its queue is full, which is exactly when the
// loop is busy.
func TestTUIAutoRefreshSelfHealsAfterDroppedCompletion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())

	node, err := service.NodeCreate(ctx, NodeCreateInput{Slug: "root-node", Directory: workspace})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	// Simulate a load whose completion event never arrived: the latch is set and
	// no result was ever applied.
	app.refreshInFlight = true
	app.refreshDeadline = time.Now().Add(time.Minute)
	if quit, err := app.handleEvent(tuiRefreshTickEvent{}); err != nil || quit {
		t.Fatalf("handleEvent(suppressed tick) = (%v, %v), want (false, nil)", quit, err)
	}
	if index := app.state.findEntryByKey(nodeTargetKey(node.ID)); index >= 0 {
		t.Fatal("a tick inside the stall window started a second overlapping reload")
	}

	app.refreshDeadline = time.Now().Add(-time.Millisecond)
	if quit, err := app.handleEvent(tuiRefreshTickEvent{}); err != nil || quit {
		t.Fatalf("handleEvent(recovery tick) = (%v, %v), want (false, nil)", quit, err)
	}
	if app.refreshInFlight {
		t.Fatal("recovery refresh did not clear the in-flight latch")
	}
	if index := app.state.findEntryByKey(nodeTargetKey(node.ID)); index < 0 {
		t.Fatal("auto-refresh did not resume after the dropped completion event")
	}
}

// I2: a dropped tuiOperationCompleteEvent must not latch "already in progress".
// The operation records its own completion, and the next tick reaps it.
func TestTUIBackgroundOperationSelfHealsAfterDroppedCompletion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	node, err := service.NodeCreate(ctx, NodeCreateInput{Slug: "root-node", Directory: workspace})
	if err != nil {
		t.Fatalf("NodeCreate(root-node) error = %v", err)
	}

	app, events := newAsyncTestTUIApp(t, ctx, service)
	app.messages = newTUIMessageLog(50)
	selectTUIEntry(t, app, nodeTargetKey(node.ID))
	if err := app.performAction(tuiActionSpec{ID: tuiActionNodeStart}); err != nil {
		t.Fatalf("performAction(start) error = %v", err)
	}

	// Drop every event the operation posts, including its completion.
	waitForCondition(t, 2*time.Second, func() bool {
		return app.operations[app.operationOrder[0]].completion.Load() != nil
	}, "background operation to finish")
	drainDroppedTUIEvents(events)

	if quit, tickErr := app.handleEvent(tuiRefreshTickEvent{}); tickErr != nil || quit {
		t.Fatalf("handleEvent(refresh tick) = (%v, %v), want (false, nil)", quit, tickErr)
	}
	if len(app.operations) != 0 {
		t.Fatalf("refresh tick left %d finished operations latched", len(app.operations))
	}
	var sawSummary bool
	for _, entry := range app.messages.Entries() {
		if strings.Contains(entry.Text, "Starting root-node") {
			sawSummary = true
		}
	}
	if !sawSummary {
		t.Fatal("the reaped completion did not report its outcome")
	}
	if err := app.performAction(tuiActionSpec{ID: tuiActionNodeStop}); err != nil {
		t.Fatalf("performAction(stop) after the lost completion = %v", err)
	}
	awaitQuiescentTUI(t, app, events, "the retried stop to finish")
}

// I2: the "daemon connection is synchronizing" state must clear itself. A
// dropped tuiDaemonSynchronizedEvent has to fail the handshake, not park the
// reconnect supervisor forever.
func TestDaemonSynchronizationHandshakeTimesOutWhenEventIsDropped(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	store := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	store.syncApplyTimeout = 50 * time.Millisecond
	store.daemonReady.Store(false)
	t.Cleanup(store.Close)

	done := make(chan error, 1)
	go func() { done <- store.awaitDaemonSynchronization(ctx, daemon.SyncSnapshot{DaemonEpoch: "epoch"}) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "timed out installing daemon synchronization") {
			t.Fatalf("awaitDaemonSynchronization() error = %v, want an install timeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("awaitDaemonSynchronization blocked permanently on the dropped handshake")
	}

	if err := store.Call(ctx, "terminal.snapshot", nil, nil); err == nil ||
		!strings.Contains(err.Error(), "daemon connection is synchronizing") {
		t.Fatalf("store Call before a delivered handshake = %v", err)
	}

	// A handshake that does reach the loop clears the synchronizing state.
	delivered := make(chan tuiDaemonSynchronizedEvent, 1)
	store.postEvent = func(event vaxis.Event) {
		if sync, ok := event.(tuiDaemonSynchronizedEvent); ok {
			delivered <- sync
		}
	}
	state := []byte(fmt.Sprintf(`{"session":{"version":%d}}`, daemon.SessionVersion))
	go func() {
		done <- store.awaitDaemonSynchronization(ctx, daemon.SyncSnapshot{DaemonEpoch: "epoch", State: state})
	}()
	select {
	case event := <-delivered:
		event.complete(store.applyDaemonSynchronization(event.Snapshot))
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the redelivered handshake")
	}
	if err := <-done; err != nil {
		t.Fatalf("awaitDaemonSynchronization() after redelivery = %v", err)
	}
	if !store.daemonReady.Load() {
		t.Fatal("synchronizing state did not clear after the handshake was installed")
	}
}

// I2: the reconnect supervisor must redial rather than park when the handshake
// never reaches the event loop.
func TestReconnectSupervisorRedialsWhenSynchronizationEventIsLost(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service, _ := newTestService(t)
	store := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	store.syncApplyTimeout = 20 * time.Millisecond
	t.Cleanup(store.Close)

	var mu sync.Mutex
	dials := 0
	err := runDaemonConnectionSupervisor(ctx, daemonConnectionSupervisorOptions{
		Dial: func(context.Context) (daemonEventConnection, error) {
			mu.Lock()
			dials++
			current := dials
			mu.Unlock()
			if current >= 3 {
				cancel()
			}
			return &scriptedDaemonEventConnection{snapshot: daemon.SyncSnapshot{DaemonEpoch: "epoch"}}, nil
		},
		OnSync: store.awaitDaemonSynchronization,
		Sleep:  func(context.Context, time.Duration) error { return nil },
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runDaemonConnectionSupervisor() error = %v, want context.Canceled", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if dials < 3 {
		t.Fatalf("supervisor dialed %d times, want it to keep redialing past the lost handshake", dials)
	}
}

// I1: quitting must not pay the terminal teardown cost once per tab.
func TestTUISessionStoreClosesTabsConcurrently(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	store := newTUISessionStore(ctx, service, func(vaxis.Event) {})

	const tabs = 6
	const teardown = 150 * time.Millisecond
	for range tabs {
		putTestSession(store, nodeTargetKey(newID()), &tuiSession{
			shellKind: terminal.NodeShell,
		}, &slowCloseTUITerminal{fakeTUITerminal: newFakeTUITerminal(), delay: teardown})
	}

	start := time.Now()
	store.Close()
	elapsed := time.Since(start)
	if elapsed > tabs*teardown/2 {
		t.Fatalf("closing %d tabs took %s; teardown is still serialized per tab", tabs, elapsed)
	}
	if len(store.sessions) != 0 {
		t.Fatalf("sessions remaining after Close = %d", len(store.sessions))
	}
}

type slowCloseTUITerminal struct {
	*fakeTUITerminal
	delay time.Duration
}

func (t *slowCloseTUITerminal) Close() {
	time.Sleep(t.delay)
	t.fakeTUITerminal.Close()
}

// drainDroppedTUIEvents discards queued events without handing them to the app,
// standing in for vaxis dropping them once its queue is full.
func drainDroppedTUIEvents(events <-chan vaxis.Event) {
	for {
		select {
		case <-events:
		default:
			return
		}
	}
}
