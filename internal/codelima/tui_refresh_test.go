package codelima

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
)

// The daemon pushes node.status_changed the moment a lifecycle operation lands.
// That push — not the fallback ticker — is what makes a started node appear, so
// it must reach the event loop as a reload request rather than a bare redraw of
// the same stale list.
func TestDaemonNodeStatusPushRequestsANodeReload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	events := make(chan vaxis.Event, 16)
	sessions := newTUISessionStore(ctx, service, func(event vaxis.Event) { events <- event })
	sessions.nodeChangeDebounce = 5 * time.Millisecond
	t.Cleanup(sessions.Close)

	sessions.handleDaemonEvent(daemon.Event{Event: "node.status_changed", Data: map[string]any{}})

	select {
	case event := <-events:
		if _, ok := event.(tuiNodesChangedEvent); !ok {
			t.Fatalf("node.status_changed posted %T, want tuiNodesChangedEvent", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("node.status_changed never reached the event loop; the TUI would wait out the fallback ticker")
	}
}

// A lifecycle command against several nodes emits one push per node. Reloading
// per push would rebuild the once-per-second polling the pushes replaced, so
// the burst has to collapse into a single reload request.
func TestDaemonNodeStatusPushBurstCollapsesIntoOneReload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	events := make(chan vaxis.Event, 32)
	sessions := newTUISessionStore(ctx, service, func(event vaxis.Event) { events <- event })
	sessions.nodeChangeDebounce = 50 * time.Millisecond
	t.Cleanup(sessions.Close)

	for range 8 {
		sessions.handleDaemonEvent(daemon.Event{Event: "node.status_changed", Data: map[string]any{}})
	}

	select {
	case event := <-events:
		if _, ok := event.(tuiNodesChangedEvent); !ok {
			t.Fatalf("posted %T, want tuiNodesChangedEvent", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the push burst never produced a reload request")
	}

	// Nothing further may follow: the debounce window closed with one request.
	select {
	case event := <-events:
		t.Fatalf("a burst of 8 pushes produced a second event (%T); want exactly one reload", event)
	case <-time.After(200 * time.Millisecond):
	}
}

// Close must leave nothing scheduled: a debounce timer that fires afterwards
// would post into a torn-down TUI.
func TestClosedSessionStoreDropsAScheduledNodeReload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	events := make(chan vaxis.Event, 8)
	sessions := newTUISessionStore(ctx, service, func(event vaxis.Event) { events <- event })
	sessions.nodeChangeDebounce = 50 * time.Millisecond

	sessions.handleDaemonEvent(daemon.Event{Event: "node.status_changed", Data: map[string]any{}})
	sessions.Close()

	select {
	case event := <-events:
		t.Fatalf("a closed session store still posted %T", event)
	case <-time.After(300 * time.Millisecond):
	}
}

// The usage push has to survive the JSON round trip the event stream puts it
// through — the payload arrives as an untyped map — and it must not be mistaken
// for a list change: a 1Hz sample that scheduled a reload would put a node.list
// round trip back on a one-second timer.
func TestDaemonNodeUsagePushDeliversTheSampleAndNeverReloads(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	events := make(chan vaxis.Event, 16)
	sessions := newTUISessionStore(ctx, service, func(event vaxis.Event) { events <- event })
	sessions.nodeChangeDebounce = 5 * time.Millisecond
	t.Cleanup(sessions.Close)

	percent := 42.5
	memoryUsed, memoryTotal := uint64(2<<30), uint64(4<<30)
	published := nodeUsageEvent{
		NodeID:           "node-1",
		SampledAt:        time.Now().UTC().Truncate(time.Millisecond),
		CPUUsagePercent:  &percent,
		MemoryUsedBytes:  &memoryUsed,
		MemoryTotalBytes: &memoryTotal,
	}
	sessions.handleDaemonEvent(daemon.Event{Event: "node.usage_changed", Data: daemonEventWireData(t, published)})

	select {
	case event := <-events:
		usage, ok := event.(tuiNodeUsageEvent)
		if !ok {
			t.Fatalf("node.usage_changed posted %T, want tuiNodeUsageEvent", event)
		}
		if usage.Usage.NodeID != published.NodeID || !usage.Usage.SampledAt.Equal(published.SampledAt) {
			t.Fatalf("decoded usage = %+v, want %+v", usage.Usage, published)
		}
		if usage.Usage.CPUUsagePercent == nil || *usage.Usage.CPUUsagePercent != percent {
			t.Fatalf("decoded CPU usage = %v, want %v", usage.Usage.CPUUsagePercent, percent)
		}
		if usage.Usage.MemoryUsedBytes == nil || *usage.Usage.MemoryUsedBytes != memoryUsed ||
			usage.Usage.MemoryTotalBytes == nil || *usage.Usage.MemoryTotalBytes != memoryTotal {
			t.Fatalf("decoded memory usage = %v/%v, want %d/%d",
				usage.Usage.MemoryUsedBytes, usage.Usage.MemoryTotalBytes, memoryUsed, memoryTotal)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("node.usage_changed never reached the event loop")
	}

	// Nothing else may follow: no reload request, no redraw of a stale list.
	select {
	case event := <-events:
		t.Fatalf("a usage push also produced %T; usage must update in place", event)
	case <-time.After(300 * time.Millisecond):
	}
}

// daemonEventWireData reproduces what a broadcast payload looks like after the
// event stream has marshaled and decoded it: an untyped map, not the struct the
// daemon published.
func daemonEventWireData(t *testing.T, payload any) any {
	t.Helper()

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal(payload) error = %v", err)
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("Unmarshal(payload) error = %v", err)
	}
	return data
}

// A pushed sample updates the node record in place. The node's store record is
// removed first, so a node.list reload would empty the tree: the entry
// surviving with fresh numbers is the proof that no reload happened.
func TestPushedNodeUsageUpdatesInPlaceWithoutAListReload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "usage-node")

	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())
	if len(app.state.entries) != 1 {
		t.Fatalf("expected one node entry, got %d", len(app.state.entries))
	}
	if got := nodeCPUUsageText(app.state.entries[0].node); got != "--" {
		t.Fatalf("initial CPU text = %q, want %q", got, "--")
	}

	if err := os.RemoveAll(service.store.nodeDir(node.ID)); err != nil {
		t.Fatalf("RemoveAll(node dir) error = %v", err)
	}

	percent := 42.5
	memoryUsed, memoryTotal := uint64(2<<30), uint64(4<<30)
	diskUsed, diskTotal := uint64(8<<30), uint64(32<<30)
	quit, err := app.handleEvent(tuiNodeUsageEvent{Usage: nodeUsageEvent{
		NodeID:           node.ID,
		SampledAt:        time.Now().UTC(),
		CPUUsagePercent:  &percent,
		MemoryUsedBytes:  &memoryUsed,
		MemoryTotalBytes: &memoryTotal,
		DiskUsedBytes:    &diskUsed,
		DiskTotalBytes:   &diskTotal,
	}})
	if err != nil || quit {
		t.Fatalf("handleEvent(node usage) = %v, %v", quit, err)
	}

	if len(app.state.entries) != 1 {
		t.Fatalf("the usage push reloaded the node list: %d entries remain", len(app.state.entries))
	}
	entry := app.state.entries[0]
	if got := nodeCPUUsageText(entry.node); got != "42.5%" {
		t.Fatalf("CPU text = %q, want %q", got, "42.5%")
	}
	if got := nodeMemoryUsageText(entry.node); got != "2.0/4.0 GiB" {
		t.Fatalf("memory text = %q, want %q", got, "2.0/4.0 GiB")
	}
	if got := nodeDiskUsageText(entry.node); got != "8.0/32.0 GiB" {
		t.Fatalf("disk text = %q, want %q", got, "8.0/32.0 GiB")
	}
	// The record behind the tree, the info pane, and every lookup is the same
	// one, so the derived views must have moved together.
	if indexed, ok := app.state.nodesByID[node.ID]; !ok || nodeCPUUsageText(indexed) != "42.5%" {
		t.Fatalf("the node index kept the pre-push record: %+v", indexed.LastRuntimeObservation)
	}
}

// A 1Hz push races the node.list reload that carries the same numbers. The
// rules: membership belongs to the reload, and the newest sample wins.
func TestPushedNodeUsageNeverResurrectsOrRegresses(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "usage-race-node")
	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())

	// A sample for a node this window does not hold is dropped outright: the
	// reload owns membership, so a push must not create an entry.
	if app.applyNodeUsage(nodeUsageEvent{NodeID: "not-in-this-window", SampledAt: time.Now().UTC()}) {
		t.Fatal("a usage sample for an unknown node was applied")
	}
	if len(app.state.entries) != 1 {
		t.Fatalf("a usage sample changed the entry set: %d entries", len(app.state.entries))
	}

	pushedAt := time.Now().UTC()
	pushedPercent := 90.0
	if !app.applyNodeUsage(nodeUsageEvent{NodeID: node.ID, SampledAt: pushedAt, CPUUsagePercent: &pushedPercent}) {
		t.Fatal("a usage sample for a known node was dropped")
	}

	// A reload whose embedded usage predates the last push must not regress the
	// display back to the older numbers.
	stalePercent := 10.0
	staleAt := pushedAt.Add(-2 * time.Second)
	stale := app.state.nodes[0]
	stale.LastRuntimeObservation = &RuntimeObservation{
		Name: stale.SandboxName, Exists: true, Status: ObservationRunning,
		CPUUsagePercent: &stalePercent, CPUUsageSampledAt: &staleAt, ResourceUsageSampledAt: &staleAt,
	}
	if err := app.applyReloadedNodes([]Node{stale}, ""); err != nil {
		t.Fatalf("applyReloadedNodes() error = %v", err)
	}
	if got := nodeCPUUsageText(app.state.entries[0].node); got != "90.0%" {
		t.Fatalf("CPU text after a reload carrying older usage = %q, want the pushed %q", got, "90.0%")
	}

	// A push older than what the reload carried is ignored in the same way.
	if app.applyNodeUsage(nodeUsageEvent{NodeID: node.ID, SampledAt: staleAt, CPUUsagePercent: &stalePercent}) {
		t.Fatal("a usage sample older than the current one was applied")
	}
	if got := nodeCPUUsageText(app.state.entries[0].node); got != "90.0%" {
		t.Fatalf("CPU text after a stale push = %q, want %q", got, "90.0%")
	}

	// A reload that drops the node drops its sample with it, so nothing can
	// outlive the record it describes.
	if err := app.applyReloadedNodes(nil, ""); err != nil {
		t.Fatalf("applyReloadedNodes(empty) error = %v", err)
	}
	if len(app.nodeUsage) != 0 {
		t.Fatalf("usage samples survived their nodes: %+v", app.nodeUsage)
	}
}

// Replaces the retired TestTUIAutoRefreshSamplesNodeCPUOncePerSecond: the
// per-second freshness it pinned is now delivered by the daemon's usage push,
// not by the auto-refresh tick, which is deliberately ten times slower.
func TestNodeUsageFreshnessComesFromPushesNotThePoll(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "usage-freshness-node")
	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())

	if got := tuiAutoRefreshInterval(&Service{daemonClient: &daemonclient.Client{}}); got <= time.Second {
		t.Fatalf("daemon fallback poll = %s; this test only means something while the poll is slow", got)
	}

	sampledAt := time.Now().UTC()
	for sample, percent := range []float64{12.5, 37.5, 62.5} {
		value := percent
		at := sampledAt.Add(time.Duration(sample) * time.Second)
		before := app.drawPasses
		quit, err := app.handleEvent(tuiNodeUsageEvent{Usage: nodeUsageEvent{
			NodeID: node.ID, SampledAt: at, CPUUsagePercent: &value,
		}})
		if err != nil || quit {
			t.Fatalf("handleEvent(sample %d) = %v, %v", sample, quit, err)
		}
		want := fmt.Sprintf("%.1f%%", value)
		if got := nodeCPUUsageText(app.state.entries[0].node); got != want {
			t.Fatalf("sample %d rendered %q, want %q", sample, got, want)
		}
		if app.drawPasses == before {
			t.Fatalf("sample %d applied without asking for a repaint", sample)
		}
	}
}

func TestNodesChangedEventReloadsTheNodeList(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)

	app := newTestTUIApp(t, ctx, service, newFakeTUISessionManager())
	if len(app.state.entries) != 0 {
		t.Fatalf("expected an empty initial node list, got %d entries", len(app.state.entries))
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{Slug: "pushed-node", Directory: workspace})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	if quit, err := app.handleEvent(tuiNodesChangedEvent{}); err != nil || quit {
		t.Fatalf("handleEvent(nodes changed) = %v, %v", quit, err)
	}
	if index := app.state.findEntryByKey(nodeTargetKey(node.ID)); index < 0 {
		t.Fatalf("a pushed node change did not reload the node list")
	}
}

// Terminal damage and daemon pushes arrive as redraw-only events at up to 20Hz.
// Consecutive ones render identical frames, so the loop must spend one draw on
// the whole queued run instead of one per event.
func TestServeCollapsesQueuedRedrawsIntoASingleDraw(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	app, events := newAsyncTestTUIApp(t, ctx, service)

	for range 12 {
		events <- vaxis.Redraw{}
	}
	events <- vaxis.QuitEvent{}

	if err := app.serve(events); err != nil {
		t.Fatalf("serve() error = %v", err)
	}
	if app.drawPasses != 1 {
		t.Fatalf("12 queued redraws cost %d draws, want 1", app.drawPasses)
	}
}

// Only the draw is deduplicated. Everything else queued behind a redraw burst
// must still be applied, in order — the one-shot completion and handshake
// events are latches, so dropping one is a hang rather than a lost frame.
func TestServeAppliesNonRedrawEventsQueuedBehindRedraws(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	app, events := newAsyncTestTUIApp(t, ctx, service)

	var pushed []string
	app.clipboardPush = func(text string) error {
		pushed = append(pushed, text)
		return nil
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{Slug: "queued-node", Directory: workspace})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	nodes, err := loadTUINodes(ctx, service, "")
	if err != nil {
		t.Fatalf("loadTUINodes() error = %v", err)
	}

	events <- vaxis.Redraw{}
	events <- vaxis.Redraw{}
	events <- tuiClipboardEvent{Text: "first"}
	events <- vaxis.Redraw{}
	// A completion event: dropping this one latches refreshInFlight forever.
	events <- tuiRefreshCompleteEvent{Nodes: nodes}
	events <- vaxis.Redraw{}
	events <- tuiClipboardEvent{Text: "second"}
	events <- vaxis.QuitEvent{}

	app.refreshInFlight = true
	app.refreshDeadline = time.Now().Add(tuiRefreshStallTimeout)
	if err := app.serve(events); err != nil {
		t.Fatalf("serve() error = %v", err)
	}

	if len(pushed) != 2 || pushed[0] != "first" || pushed[1] != "second" {
		t.Fatalf("clipboard events applied = %#v, want [first second] in order", pushed)
	}
	if app.refreshInFlight {
		t.Fatalf("the queued refresh completion was dropped; the refresh latch never cleared")
	}
	if index := app.state.findEntryByKey(nodeTargetKey(node.ID)); index < 0 {
		t.Fatalf("the queued refresh completion never applied its node list")
	}
}
