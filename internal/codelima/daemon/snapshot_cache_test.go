package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// snapshotTestHandler serves one immutable terminal snapshot per revision, the
// way the daemon host does: clients pull it, and the same bytes are correct for
// every one of them until the renderer publishes again.
type snapshotTestHandler struct {
	mu       sync.Mutex
	snapshot Snapshot
}

func (h *snapshotTestHandler) publish(snapshot Snapshot) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.snapshot = snapshot
}

func (h *snapshotTestHandler) Handle(_ context.Context, _ ClientContext, method string, _ json.RawMessage) (any, error) {
	if method == "terminal.snapshot" {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.snapshot, nil
	}
	return map[string]bool{"ok": true}, nil
}

func (h *snapshotTestHandler) Snapshot(context.Context) (any, error) { return nil, nil }
func (h *snapshotTestHandler) TerminalCount() int                    { return 1 }
func (h *snapshotTestHandler) Close() error                          { return nil }

func makeTestSnapshot(t testing.TB, cols, rows int, sequence uint64) Snapshot {
	t.Helper()
	snapshot := Snapshot{
		Cols:             cols,
		Rows:             rows,
		Cells:            make([]SnapshotCell, 0, cols*rows),
		Generation:       sequence,
		SnapshotSequence: sequence,
		ProducedAt:       time.Unix(0, int64(sequence)).UTC(),
	}
	for index := 0; index < cols*rows; index++ {
		snapshot.Cells = append(snapshot.Cells, SnapshotCell{
			Grapheme:  string(rune('a' + index%26)),
			Width:     1,
			FG:        0x00cc99ff,
			BG:        0x00112233,
			Bold:      index%3 == 0,
			Underline: index%7 == 0,
		})
	}
	return snapshot
}

// The daemon used to re-marshal the identical grid for every client that pulled
// it, so a second attached window doubled the encoding cost of a screen that is
// byte-for-byte the same. One encode per published revision, shared by every
// puller, is the contract.
func TestTerminalSnapshotIsEncodedOncePerRevisionAcrossClients(t *testing.T) {
	t.Parallel()
	var cache snapshotBodyCache
	first := makeTestSnapshot(t, 80, 24, 1)

	bodyOne, err := cache.body("term-1", first)
	if err != nil {
		t.Fatal(err)
	}
	bodyTwo, err := cache.body("term-1", first)
	if err != nil {
		t.Fatal(err)
	}
	if got := cache.encodes.Load(); got != 1 {
		t.Fatalf("two clients pulling one revision produced %d encodes, want 1", got)
	}
	if cache.reuses.Load() != 1 {
		t.Fatalf("second pull was not served from the cache, reuses = %d", cache.reuses.Load())
	}
	if string(bodyOne) != string(bodyTwo) {
		t.Fatal("cached snapshot body differs between clients")
	}
	var decoded Snapshot
	if err := json.Unmarshal(bodyOne, &decoded); err != nil {
		t.Fatalf("cached body is not a decodable snapshot: %v", err)
	}
	if decoded.SnapshotSequence != 1 || len(decoded.Cells) != 80*24 {
		t.Fatalf("cached body = sequence %d with %d cells", decoded.SnapshotSequence, len(decoded.Cells))
	}

	// A new publication invalidates: the sequence advances.
	if _, err := cache.body("term-1", makeTestSnapshot(t, 80, 24, 2)); err != nil {
		t.Fatal(err)
	}
	if got := cache.encodes.Load(); got != 2 {
		t.Fatalf("new revision produced %d total encodes, want 2", got)
	}

	// Re-stamping staleness onto the same sequence is a different value and must
	// also invalidate.
	stale := makeTestSnapshot(t, 80, 24, 2)
	stale.Stale = true
	if _, err := cache.body("term-1", stale); err != nil {
		t.Fatal(err)
	}
	if got := cache.encodes.Load(); got != 3 {
		t.Fatalf("staleness re-stamp produced %d total encodes, want 3", got)
	}

	// Terminals never share an entry.
	if _, err := cache.body("term-2", first); err != nil {
		t.Fatal(err)
	}
	if got := cache.encodes.Load(); got != 4 {
		t.Fatalf("second terminal produced %d total encodes, want 4", got)
	}
}

func TestSnapshotBodyCacheStaysBounded(t *testing.T) {
	t.Parallel()
	var cache snapshotBodyCache
	for index := 0; index < maxSnapshotBodyCacheEntries*3; index++ {
		if _, err := cache.body(fmt.Sprintf("term-%d", index), makeTestSnapshot(t, 8, 2, uint64(index+1))); err != nil {
			t.Fatal(err)
		}
	}
	cache.mu.Lock()
	entries, bytes := len(cache.entries), cache.bytes
	cache.mu.Unlock()
	if entries > maxSnapshotBodyCacheEntries {
		t.Fatalf("cache retained %d entries, want at most %d", entries, maxSnapshotBodyCacheEntries)
	}
	if bytes > maxSnapshotBodyCacheBytes {
		t.Fatalf("cache retained %d bytes, want at most %d", bytes, maxSnapshotBodyCacheBytes)
	}
}

// End-to-end: two connected clients pulling the same terminal.snapshot revision
// must produce one marshal, and both must receive the same complete grid.
func TestConnectedClientsShareOneSnapshotEncoding(t *testing.T) {
	home := daemonServerTestHome(t)
	handler := &snapshotTestHandler{}
	handler.publish(makeTestSnapshot(t, 40, 12, 1))
	server := NewServer(Config{Home: home, Version: "test", Handler: handler})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Server.Run() error = %v", err)
		}
	}()

	paths := HomePaths(home)
	waitForServerSocket(t, paths.Socket)
	pull := func(client string) Snapshot {
		t.Helper()
		conn, err := dialServerForTest(paths.Socket)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		encoder, decoder := json.NewEncoder(conn), json.NewDecoder(conn)
		if err := encoder.Encode(Request{ID: 1, Method: "hello", Params: mustJSONForTest(t, HelloParams{
			Version: "test", Protocol: ProtocolVersion, ClientInstanceID: client,
		})}); err != nil {
			t.Fatal(err)
		}
		var response Response
		if err := decoder.Decode(&response); err != nil || response.Error != nil {
			t.Fatalf("hello response = %#v, %v", response, err)
		}
		if err := encoder.Encode(Request{ID: 2, Method: "terminal.snapshot", Params: mustJSONForTest(t, map[string]string{
			"terminal_id": "term-1",
		})}); err != nil {
			t.Fatal(err)
		}
		if err := decoder.Decode(&response); err != nil || response.Error != nil {
			t.Fatalf("snapshot response = %#v, %v", response, err)
		}
		var snapshot Snapshot
		if err := DecodeResult(response, &snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}

	first, second := pull("client-a"), pull("client-b")
	if first.SnapshotSequence != 1 || second.SnapshotSequence != 1 {
		t.Fatalf("clients saw sequences %d and %d, want 1", first.SnapshotSequence, second.SnapshotSequence)
	}
	if len(first.Cells) != 40*12 || len(second.Cells) != len(first.Cells) {
		t.Fatalf("clients saw %d and %d cells, want %d", len(first.Cells), len(second.Cells), 40*12)
	}
	if got := server.snapshots.encodes.Load(); got != 1 {
		t.Fatalf("two clients pulling one revision produced %d encodes, want 1", got)
	}

	handler.publish(makeTestSnapshot(t, 40, 12, 2))
	if next := pull("client-a"); next.SnapshotSequence != 2 {
		t.Fatalf("client saw sequence %d after republication, want 2", next.SnapshotSequence)
	}
	if got := server.snapshots.encodes.Load(); got != 2 {
		t.Fatalf("republication produced %d total encodes, want 2", got)
	}
}

// events.subscribe used to run its capture on the connection reader, and its
// bounded fallback held the server state lock across the handler call. A capture
// that cannot finish therefore froze every other connection on the daemon. The
// capture now runs off the reader and holds only the event-publication barrier.
func TestSlowSubscribeCaptureDoesNotStallOtherConnections(t *testing.T) {
	home := daemonServerTestHome(t)
	handler := &subscribeStallHandler{entered: make(chan struct{}), release: make(chan struct{})}
	server := NewServer(Config{Home: home, Version: "test", Handler: handler})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() {
		cancel()
		handler.finish()
		if err := <-done; err != nil {
			t.Errorf("Server.Run() error = %v", err)
		}
	}()

	paths := HomePaths(home)
	waitForServerSocket(t, paths.Client)
	events, err := dialServerForTest(paths.Client)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = events.Close() }()
	eventEncoder, eventDecoder := json.NewEncoder(events), json.NewDecoder(events)
	if err := eventEncoder.Encode(Request{ID: 1, Method: "hello", Params: mustJSONForTest(t, HelloParams{Version: "test", Protocol: ProtocolVersion})}); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := eventDecoder.Decode(&response); err != nil || response.Error != nil {
		t.Fatalf("hello response = %#v, %v", response, err)
	}
	if err := eventEncoder.Encode(Request{ID: 2, Method: "events.subscribe"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handler.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("subscribe capture never reached the handler")
	}

	// The capture is parked inside the handler. Another connection must still be
	// served, which it cannot be if the capture owns the server state lock.
	request, err := dialServerForTest(paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = request.Close() }()
	requestEncoder, requestDecoder := json.NewEncoder(request), json.NewDecoder(request)
	if err := requestEncoder.Encode(Request{ID: 1, Method: "hello", Params: mustJSONForTest(t, HelloParams{Version: "test", Protocol: ProtocolVersion})}); err != nil {
		t.Fatal(err)
	}
	if err := requestDecoder.Decode(&response); err != nil || response.Error != nil {
		t.Fatalf("hello during stalled capture = %#v, %v", response, err)
	}
	if err := request.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := requestEncoder.Encode(Request{ID: 2, Method: "daemon.ping"}); err != nil {
		t.Fatal(err)
	}
	if err := requestDecoder.Decode(&response); err != nil || response.Error != nil {
		t.Fatalf("daemon.ping during stalled subscribe capture = %#v, %v", response, err)
	}

	handler.finish()
	if err := events.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := eventDecoder.Decode(&response); err != nil || response.Error != nil {
		t.Fatalf("sync response after release = %#v, %v", response, err)
	}
	var syncState SyncSnapshot
	if err := DecodeResult(response, &syncState); err != nil {
		t.Fatal(err)
	}
	if syncState.DaemonEpoch == "" {
		t.Fatalf("sync snapshot = %#v, want an authoritative cut", syncState)
	}
}

// subscribeStallHandler parks the first synchronization capture until released,
// so a test can observe what the rest of the daemon can still do meanwhile.
type subscribeStallHandler struct {
	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once
	doneOnce  sync.Once
}

func (h *subscribeStallHandler) finish() { h.doneOnce.Do(func() { close(h.release) }) }

func (h *subscribeStallHandler) Handle(context.Context, ClientContext, string, json.RawMessage) (any, error) {
	return map[string]bool{"ok": true}, nil
}

func (h *subscribeStallHandler) Snapshot(ctx context.Context) (any, error) {
	h.enterOnce.Do(func() { close(h.entered) })
	select {
	case <-h.release:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
	return map[string]any{"session": Session{Version: SessionVersion}}, nil
}

func (h *subscribeStallHandler) TerminalCount() int { return 0 }
func (h *subscribeStallHandler) Close() error       { return nil }

// A second events.subscribe while a capture is in flight is refused rather than
// racing it: the capture owns the connection's subscription transition.
func TestConcurrentSubscribeOnOneConnectionIsRefused(t *testing.T) {
	t.Parallel()
	client := &clientConn{
		id:       "1",
		outbound: newOutboundQueue(8, 1<<20),
	}
	server := NewServer(Config{Home: t.TempDir(), Version: "test", Handler: testHandler{}})
	if !client.subscribing.CompareAndSwap(false, true) {
		t.Fatal("fresh connection already reports a capture in flight")
	}
	server.beginSubscribe(context.Background(), client, Request{ID: 9, Method: "events.subscribe"}, true)
	frame, err := client.outbound.Pop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(frame), "already in progress") {
		t.Fatalf("second subscribe response = %s", frame)
	}
}

// BenchmarkTerminalSnapshotBodyPerClient measures one published revision of a
// 160x50 grid being pulled by four attached clients: the old cost was one full
// marshal per client per pull, the new cost is one marshal per revision.
// Event publishers are attached to the server before Run binds anything, so a
// broadcast can land while prepare is still generating the daemon identity that
// every event carries. Under -race an unsynchronized identity assignment fails
// here.
func TestBroadcastDuringServerStartupIsRaceFree(t *testing.T) {
	home := daemonServerTestHome(t)
	server := NewServer(Config{Home: home, Version: "test", Handler: testHandler{}, HeartbeatInterval: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	stopBroadcasting := make(chan struct{})
	broadcasting := make(chan struct{})
	go func() {
		defer close(broadcasting)
		for {
			select {
			case <-stopBroadcasting:
				return
			default:
			}
			server.Broadcast("terminal.dirty", map[string]string{"terminal_id": "term-1"})
			_ = server.status()
		}
	}()

	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	waitForServerSocket(t, HomePaths(home).Socket)
	close(stopBroadcasting)
	<-broadcasting

	cancel()
	if err := <-done; err != nil {
		t.Errorf("Server.Run() error = %v", err)
	}
}

func BenchmarkTerminalSnapshotBodyPerClient(b *testing.B) {
	const clients = 4
	base := makeTestSnapshot(b, 160, 50, 1)
	revision := func(sequence uint64) Snapshot {
		next := base
		next.SnapshotSequence = sequence
		next.Generation = sequence
		return next
	}

	b.Run("marshal-per-client", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			snapshot := revision(uint64(i + 1))
			for client := 0; client < clients; client++ {
				data, err := json.Marshal(snapshot)
				if err != nil {
					b.Fatal(err)
				}
				if len(data) == 0 {
					b.Fatal("empty snapshot body")
				}
			}
		}
	})

	b.Run("cached-per-revision", func(b *testing.B) {
		b.ReportAllocs()
		var cache snapshotBodyCache
		for i := 0; i < b.N; i++ {
			snapshot := revision(uint64(i + 1))
			for client := 0; client < clients; client++ {
				data, err := cache.body("term-1", snapshot)
				if err != nil {
					b.Fatal(err)
				}
				if len(data) == 0 {
					b.Fatal("empty snapshot body")
				}
			}
		}
	})
}
