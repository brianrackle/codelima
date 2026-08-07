package daemon

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMethodDeliveryClassesCoverEveryMutation(t *testing.T) {
	t.Parallel()

	cases := map[string]DeliveryClass{
		"terminal.send_event": ClassInput,
		"terminal.send_text":  ClassInput,
		"terminal.send_keys":  ClassInput,
		"terminal.send_input": ClassInput,
		"terminal.scroll":     ClassInput,
		"terminal.resize":     ClassReplaceable,
		"terminal.focus":      ClassReplaceable,
		"terminal.open":       ClassControl,
		"terminal.close":      ClassControl,
		"terminal.move":       ClassControl,
		// A manual renderer restart is an idempotent keyed mutation whose
		// ordering the renderer supervisor's own state lock already protects, so
		// it belongs with the other control-class terminal mutations rather than
		// on an input lane or in the lifecycle timeout class.
		"terminal.restart_renderer": ClassControl,
		"input.takeover":            ClassOwnership,
		"node.start":                ClassLifecycle,
		"node.stop":                 ClassLifecycle,
		"daemon.update":             ClassLifecycle,
		"node.list":                 ClassQuery,
		"terminal.list":             ClassQuery,
		"terminal.read":             ClassQuery,
		"daemon.ping":               ClassQuery,
	}
	for method, want := range cases {
		if got := ClassifyMethod(method); got != want {
			t.Errorf("ClassifyMethod(%q) = %d, want %d", method, got, want)
		}
		if got := MutatingInputMethod(method); got != (want != ClassQuery) {
			t.Errorf("MutatingInputMethod(%q) = %v", method, got)
		}
	}
	// Node lifecycle and a manual renderer restart mutate daemon-owned state and
	// must not bypass the input-ownership gate.
	for _, method := range []string{"node.start", "node.stop", "terminal.restart_renderer"} {
		if !MutatingInputMethod(method) {
			t.Errorf("%s must be gated on input ownership", method)
		}
	}
}

func TestSlowControlMutationDoesNotStallSameConnectionInput(t *testing.T) {
	handler := newLaneTestHandler()
	release := handler.block("terminal.close")
	defer close(release)
	client, stop := startLaneTestServer(t, Config{Handler: handler})
	defer stop()

	client.send(t, 2, "terminal.close", map[string]any{"terminal_id": "term-1"})
	handler.awaitEntered(t, "terminal.close")

	// Input for the terminal being torn down, and for an unrelated terminal,
	// must both be delivered while the teardown is still running.
	client.send(t, 3, "terminal.send_event", map[string]any{"terminal_id": "term-1", "type": "key"})
	client.send(t, 4, "terminal.send_event", map[string]any{"terminal_id": "term-2", "type": "key"})
	for _, id := range []uint64{3, 4} {
		response := client.await(t, id, 500*time.Millisecond)
		if response.Error != nil {
			t.Fatalf("input response %d = %#v", id, response.Error)
		}
	}
	if _, ok := client.poll(2); ok {
		t.Fatal("terminal.close replied before its blocking teardown finished")
	}
}

func TestFullTerminalInputLaneRejectsWithoutClosingConnection(t *testing.T) {
	handler := newLaneTestHandler()
	release := handler.block("terminal.send_event")
	defer close(release)
	client, stop := startLaneTestServer(t, Config{Handler: handler, MutationQueueSize: 1})
	defer stop()

	client.send(t, 2, "terminal.send_event", map[string]any{"terminal_id": "term-1", "type": "key"})
	handler.awaitEntered(t, "terminal.send_event")
	for id := uint64(3); id <= 10; id++ {
		client.send(t, id, "terminal.send_event", map[string]any{"terminal_id": "term-1", "type": "key"})
	}

	rejected := false
	for id := uint64(4); id <= 10; id++ {
		response := client.await(t, id, time.Second)
		if response.Error != nil && strings.Contains(response.Error.Message, "input queue is full") {
			rejected = true
			break
		}
	}
	if !rejected {
		t.Fatal("a saturated input lane did not report the loss on a request")
	}
	// The connection must survive: a stuck terminal is not a reason to drop
	// every other terminal on the same transport.
	client.send(t, 100, "daemon.ping", nil)
	if response := client.await(t, 100, time.Second); response.Error != nil {
		t.Fatalf("daemon.ping after input-lane saturation = %#v", response.Error)
	}
}

func TestInputTakeoverIsAppliedBeforeFollowingInput(t *testing.T) {
	handler := newLaneTestHandler()
	home := daemonServerTestHome(t)
	// The first connection holds the input lease, so the second starts
	// observe-only.
	_, stop := startLaneTestServer(t, Config{Home: home, Handler: handler})
	defer stop()

	observer := newLaneTestConnection(t, HomePaths(home).Socket, false)
	defer observer.close()
	// The first rejected frame leaves an idle input lane in place, so a
	// concurrently dispatched takeover would have every chance to lose the
	// race that connection read order must prevent.
	observer.send(t, 2, "terminal.send_event", map[string]any{"terminal_id": "term-1", "type": "key"})
	if response := observer.await(t, 2, time.Second); response.Error == nil {
		t.Fatal("observe-only client sent input without taking ownership")
	}

	observer.send(t, 3, "input.takeover", nil)
	observer.send(t, 4, "terminal.send_event", map[string]any{"terminal_id": "term-1", "type": "key"})
	if response := observer.await(t, 4, time.Second); response.Error != nil {
		t.Fatalf("input frame raced ahead of the takeover it depends on: %#v", response.Error)
	}
}

func TestReplaceableRequestsCoalesceToLatestValue(t *testing.T) {
	handler := newLaneTestHandler()
	release := handler.block("terminal.resize")
	client, stop := startLaneTestServer(t, Config{Handler: handler})
	defer stop()

	client.send(t, 2, "terminal.resize", map[string]any{"terminal_id": "term-1", "cols": 1, "rows": 10})
	handler.awaitEntered(t, "terminal.resize")
	for id := uint64(3); id <= 7; id++ {
		client.send(t, id, "terminal.resize", map[string]any{"terminal_id": "term-1", "cols": int(id), "rows": 10})
	}
	// The connection reader is serial, so an answered ping proves every queued
	// resize has already reached its lane.
	client.send(t, 99, "daemon.ping", nil)
	if response := client.await(t, 99, time.Second); response.Error != nil {
		t.Fatalf("daemon.ping while resizes queue = %#v", response.Error)
	}
	close(release)

	for id := uint64(2); id <= 7; id++ {
		if response := client.await(t, id, 2*time.Second); response.Error != nil {
			t.Fatalf("resize response %d = %#v", id, response.Error)
		}
	}
	applied := handler.appliedNumbers("terminal.resize", "cols")
	if len(applied) != 2 || applied[0] != 1 || applied[1] != 7 {
		t.Fatalf("applied resizes = %v, want the in-flight value then only the latest queued value", applied)
	}
}

func TestLifecycleRequestOutlivesShortRequestTimeout(t *testing.T) {
	handler := newLaneTestHandler()
	handler.delay("node.start", 250*time.Millisecond)
	handler.delay("terminal.list", 250*time.Millisecond)
	client, stop := startLaneTestServer(t, Config{Handler: handler, RequestTimeout: 40 * time.Millisecond})
	defer stop()

	client.send(t, 2, "node.start", map[string]any{"node": "demo"})
	client.send(t, 3, "terminal.list", nil)

	if response := client.await(t, 2, 3*time.Second); response.Error != nil {
		t.Fatalf("node.start was cancelled by the short request timeout: %#v", response.Error)
	}
	response := client.await(t, 3, 3*time.Second)
	if response.Error == nil {
		t.Fatal("a fast-class method ignored its short request timeout")
	}
	if !strings.Contains(response.Error.Message, "deadline") {
		t.Fatalf("fast-class timeout error = %#v", response.Error)
	}
}

func TestLifecycleRequestTimeoutClassIsConfigurable(t *testing.T) {
	handler := newLaneTestHandler()
	handler.delay("node.start", 2*time.Second)
	client, stop := startLaneTestServer(t, Config{
		Handler:                 handler,
		RequestTimeout:          5 * time.Second,
		LifecycleRequestTimeout: 40 * time.Millisecond,
	})
	defer stop()

	client.send(t, 2, "node.start", map[string]any{"node": "demo"})
	response := client.await(t, 2, 3*time.Second)
	if response.Error == nil || !strings.Contains(response.Error.Message, "deadline") {
		t.Fatalf("configured lifecycle timeout was not applied: %#v", response.Error)
	}
}

func TestStalledClientDoesNotStallOtherConnections(t *testing.T) {
	home := daemonServerTestHome(t)
	handler := newLaneTestHandler()
	server := NewServer(Config{
		Home:              home,
		Version:           "test",
		Handler:           handler,
		OutboundMaxFrames: 2,
		WriteTimeout:      50 * time.Millisecond,
	})
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
	waitForServerSocket(t, paths.Client)
	waitForServerSocket(t, paths.Socket)

	// A stalled subscriber never reads: it stands in for a SIGSTOP'd TUI.
	stalled, err := dialServerForTest(paths.Client)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stalled.Close() }()
	encoder, decoder := json.NewEncoder(stalled), json.NewDecoder(stalled)
	if err := encoder.Encode(Request{ID: 1, Method: "hello", Params: mustJSONForTest(t, HelloParams{Version: "test", Protocol: ProtocolVersion})}); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Encode(Request{ID: 2, Method: "events.subscribe"}); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	// A blocking request from the stalled client also stays in flight.
	blockedRelease := handler.block("terminal.open")
	defer close(blockedRelease)
	if err := encoder.Encode(Request{ID: 3, Method: "terminal.open", Params: mustJSONForTest(t, map[string]string{"target": "node:demo"})}); err != nil {
		t.Fatal(err)
	}

	healthy := newLaneTestConnection(t, paths.Socket, false)
	defer healthy.close()
	payload := map[string]string{"data": strings.Repeat("x", 4096)}
	for index := 0; index < 64; index++ {
		server.Broadcast("terminal.dirty", payload)
	}
	for id := uint64(10); id < 20; id++ {
		started := time.Now()
		healthy.send(t, id, "daemon.ping", nil)
		if response := healthy.await(t, id, time.Second); response.Error != nil {
			t.Fatalf("ping %d while another connection is stalled = %#v", id, response.Error)
		}
		if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
			t.Fatalf("ping %d took %s while another connection was stalled", id, elapsed)
		}
	}
}

// laneTestHandler blocks or slows selected methods and records what actually
// reached the handler, so lane routing and coalescing are observable.
type laneTestHandler struct {
	mu      sync.Mutex
	applied []laneAppliedRequest
	blocked map[string]chan struct{}
	delays  map[string]time.Duration
	entered chan string
}

type laneAppliedRequest struct {
	method string
	params map[string]any
}

func newLaneTestHandler() *laneTestHandler {
	return &laneTestHandler{
		blocked: map[string]chan struct{}{},
		delays:  map[string]time.Duration{},
		entered: make(chan string, 128),
	}
}

func (h *laneTestHandler) block(method string) chan struct{} {
	release := make(chan struct{})
	h.mu.Lock()
	h.blocked[method] = release
	h.mu.Unlock()
	return release
}

func (h *laneTestHandler) delay(method string, delay time.Duration) {
	h.mu.Lock()
	h.delays[method] = delay
	h.mu.Unlock()
}

func (h *laneTestHandler) awaitEntered(t *testing.T, method string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case entered := <-h.entered:
			if entered == method {
				return
			}
		case <-deadline:
			t.Fatalf("handler never entered %s", method)
		}
	}
}

func (h *laneTestHandler) appliedNumbers(method, field string) []float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	var values []float64
	for _, request := range h.applied {
		if request.method != method {
			continue
		}
		value, _ := request.params[field].(float64)
		values = append(values, value)
	}
	return values
}

func (h *laneTestHandler) Handle(ctx context.Context, _ ClientContext, method string, raw json.RawMessage) (any, error) {
	params := map[string]any{}
	_ = json.Unmarshal(raw, &params)
	h.mu.Lock()
	h.applied = append(h.applied, laneAppliedRequest{method: method, params: params})
	release, delay := h.blocked[method], h.delays[method]
	h.mu.Unlock()
	select {
	case h.entered <- method:
	default:
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	return map[string]string{"method": method}, nil
}

func (*laneTestHandler) Snapshot(context.Context) (any, error) { return nil, nil }
func (*laneTestHandler) TerminalCount() int                    { return 0 }
func (*laneTestHandler) Close() error                          { return nil }

// laneTestConnection matches responses by request ID because delivery classes
// are dispatched independently and no longer complete in submission order.
type laneTestConnection struct {
	conn      net.Conn
	encoder   *json.Encoder
	mu        sync.Mutex
	responses map[uint64]Response
	readErr   error
	updated   chan struct{}
}

func startLaneTestServer(t *testing.T, cfg Config) (*laneTestConnection, func()) {
	t.Helper()
	if cfg.Home == "" {
		cfg.Home = daemonServerTestHome(t)
	}
	cfg.Version = "test"
	server := NewServer(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	paths := HomePaths(cfg.Home)
	waitForServerSocket(t, paths.Socket)
	client := newLaneTestConnection(t, paths.Socket, true)
	return client, func() {
		client.close()
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Server.Run() error = %v", err)
		}
	}
}

func newLaneTestConnection(t *testing.T, path string, wantInput bool) *laneTestConnection {
	t.Helper()
	conn, err := dialServerForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	client := &laneTestConnection{
		conn:      conn,
		encoder:   json.NewEncoder(conn),
		responses: map[uint64]Response{},
		updated:   make(chan struct{}, 1),
	}
	if err := client.encoder.Encode(Request{ID: 1, Method: "hello", Params: mustJSONForTest(t, HelloParams{
		Version: "test", Protocol: ProtocolVersion, WantInput: wantInput,
	})}); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(conn)
	var hello Response
	if err := decoder.Decode(&hello); err != nil || hello.Error != nil {
		t.Fatalf("hello response = %#v, %v", hello, err)
	}
	go client.readResponses(decoder)
	return client
}

func (c *laneTestConnection) readResponses(decoder *json.Decoder) {
	for {
		var response Response
		if err := decoder.Decode(&response); err != nil {
			c.mu.Lock()
			c.readErr = err
			c.mu.Unlock()
			c.notify()
			return
		}
		c.mu.Lock()
		c.responses[response.ID] = response
		c.mu.Unlock()
		c.notify()
	}
}

func (c *laneTestConnection) notify() {
	select {
	case c.updated <- struct{}{}:
	default:
	}
}

func (c *laneTestConnection) send(t *testing.T, id uint64, method string, params any) {
	t.Helper()
	var raw json.RawMessage
	if params != nil {
		raw = mustJSONForTest(t, params)
	}
	if err := c.encoder.Encode(Request{ID: id, Method: method, Params: raw}); err != nil {
		t.Fatalf("send %s: %v", method, err)
	}
}

func (c *laneTestConnection) poll(id uint64) (Response, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	response, ok := c.responses[id]
	return response, ok
}

func (c *laneTestConnection) await(t *testing.T, id uint64, timeout time.Duration) Response {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if response, ok := c.poll(id); ok {
			return response
		}
		c.mu.Lock()
		err := c.readErr
		c.mu.Unlock()
		if err != nil {
			t.Fatalf("connection closed while waiting for response %d: %v", id, err)
		}
		select {
		case <-c.updated:
		case <-deadline:
			t.Fatalf("timed out waiting for response %d", id)
		case <-time.After(time.Millisecond):
		}
	}
}

func (c *laneTestConnection) close() {
	_ = c.conn.Close()
}
