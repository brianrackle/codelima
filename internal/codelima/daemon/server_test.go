package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/testutil"
)

func TestProtocolRoundTripAndOversizeRejection(t *testing.T) {
	t.Parallel()
	request := Request{ID: 7, Method: "daemon.ping", Params: json.RawMessage(`{"x":1}`)}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeRequest(data)
	if err != nil || decoded.ID != 7 || decoded.Method != "daemon.ping" {
		t.Fatalf("decodeRequest() = %#v, %v", decoded, err)
	}
	oversized := bytes.Repeat([]byte("x"), MaxMessageSize+1)
	if _, err := decodeRequest(oversized); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("expected size-cap error, got %v", err)
	}
}

func TestTerminalMoveRequiresInputOwnership(t *testing.T) {
	t.Parallel()

	if !mutatingInputMethod("terminal.move") {
		t.Fatalf("terminal.move must be protected as a mutating input method")
	}
}

func TestHomePathsStayUnderPrivateDaemonDirectory(t *testing.T) {
	t.Parallel()
	paths := HomePaths("/home/demo")
	if paths.Socket != "/home/demo/_daemon/daemon.sock" || paths.Client != "/home/demo/_daemon/client.sock" || paths.Session != "/home/demo/_daemon/session.json" {
		t.Fatalf("HomePaths() = %#v", paths)
	}
}

func TestSubscribedEventConnectionSurvivesRequestReadTimeout(t *testing.T) {
	home := daemonServerTestHome(t)
	server := NewServer(Config{Home: home, Version: "test", Handler: testHandler{}, ReadTimeout: 25 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	paths := HomePaths(home)
	deadline := time.Now().Add(time.Second)
	for !existsForTest(paths.Client) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	conn, err := dialServerForTest(paths.Client)
	if err != nil {
		cancel()
		<-done
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	encoder, decoder := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := encoder.Encode(Request{ID: 1, Method: "hello", Params: mustJSONForTest(t, HelloParams{Version: "test", Protocol: ProtocolVersion})}); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := decoder.Decode(&response); err != nil || response.Error != nil {
		t.Fatalf("hello response = %#v, %v", response, err)
	}
	if err := encoder.Encode(Request{ID: 2, Method: "events.subscribe", Params: mustJSONForTest(t, map[string]any{"topics": []string{"terminal"}})}); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&response); err != nil || response.Error != nil {
		t.Fatalf("subscribe response = %#v, %v", response, err)
	}

	time.Sleep(3 * 25 * time.Millisecond)
	server.Broadcast("terminal.dirty", map[string]string{"terminal_id": "term-1"})
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var event Event
	if err := decoder.Decode(&event); err != nil {
		t.Fatalf("event connection closed after request timeout: %v", err)
	}
	if event.Event != "terminal.dirty" {
		t.Fatalf("event = %#v", event)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Server.Run() error = %v", err)
	}
}

func TestSubscribedEventConnectionReceivesApplicationHeartbeat(t *testing.T) {
	home := daemonServerTestHome(t)
	server := NewServer(Config{
		Home:              home,
		Version:           "test",
		Handler:           testHandler{},
		HeartbeatInterval: 10 * time.Millisecond,
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
	conn, err := dialServerForTest(paths.Client)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	encoder, decoder := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := encoder.Encode(Request{ID: 1, Method: "hello", Params: mustJSONForTest(t, HelloParams{
		Version: "test", Protocol: ProtocolVersion,
	})}); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := decoder.Decode(&response); err != nil || response.Error != nil {
		t.Fatalf("hello response = %#v, %v", response, err)
	}
	if err := encoder.Encode(Request{ID: 2, Method: "events.subscribe"}); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&response); err != nil || response.Error != nil {
		t.Fatalf("subscribe response = %#v, %v", response, err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var event Event
	if err := decoder.Decode(&event); err != nil {
		t.Fatal(err)
	}
	if event.Event != "daemon.heartbeat" || event.DaemonEpoch == "" {
		t.Fatalf("heartbeat event = %#v", event)
	}
}

func TestAuthenticatedRequestConnectionSurvivesReadTimeout(t *testing.T) {
	home := daemonServerTestHome(t)
	server := NewServer(Config{Home: home, Version: "test", Handler: testHandler{}, ReadTimeout: 25 * time.Millisecond})
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
	deadline := time.Now().Add(time.Second)
	for !existsForTest(paths.Socket) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	conn, err := dialServerForTest(paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	encoder, decoder := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := encoder.Encode(Request{ID: 1, Method: "hello", Params: mustJSONForTest(t, HelloParams{Version: "test", Protocol: ProtocolVersion, WantInput: true})}); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := decoder.Decode(&response); err != nil || response.Error != nil {
		t.Fatalf("hello response = %#v, %v", response, err)
	}

	// The request timeout protects unauthenticated handshakes. Once a client
	// has authenticated, an idle TUI connection must remain usable.
	time.Sleep(3 * 25 * time.Millisecond)
	if err := encoder.Encode(Request{ID: 2, Method: "terminal.open", Params: mustJSONForTest(t, map[string]string{"target": "node:test"})}); err != nil {
		t.Fatalf("write request after idle interval: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if err := decoder.Decode(&response); err != nil || response.Error != nil {
		t.Fatalf("request connection closed after idle interval: response=%#v error=%v", response, err)
	}
	if response.ID != 2 {
		t.Fatalf("response ID = %d, want 2", response.ID)
	}
}

func TestEventSubscriptionReturnsAuthoritativeEpochAndSequence(t *testing.T) {
	home := daemonServerTestHome(t)
	server := NewServer(Config{Home: home, Version: "test", Handler: testHandler{}})
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
	conn, err := net.Dial("unix", paths.Client)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	encoder, decoder := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := encoder.Encode(Request{ID: 1, Method: "hello", Params: mustJSONForTest(t, HelloParams{
		Version:              "test",
		Protocol:             ProtocolVersion,
		ClientInstanceID:     "tui-1",
		ConnectionGeneration: 7,
	})}); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := decoder.Decode(&response); err != nil || response.Error != nil {
		t.Fatalf("hello response = %#v, %v", response, err)
	}
	var hello HelloResult
	if err := DecodeResult(response, &hello); err != nil {
		t.Fatal(err)
	}
	if hello.ClientID != "tui-1" || hello.ConnectionID == 0 || hello.DaemonEpoch == "" {
		t.Fatalf("hello identity = %#v", hello)
	}

	if err := encoder.Encode(Request{ID: 2, Method: "events.subscribe"}); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&response); err != nil || response.Error != nil {
		t.Fatalf("sync response = %#v, %v", response, err)
	}
	var syncState SyncSnapshot
	if err := DecodeResult(response, &syncState); err != nil {
		t.Fatal(err)
	}
	if syncState.DaemonEpoch != hello.DaemonEpoch || syncState.StateSequence != hello.StateSequence {
		t.Fatalf("sync identity = %#v, hello = %#v", syncState, hello)
	}

	server.Broadcast("terminal.dirty", map[string]string{"terminal_id": "term-1"})
	var event Event
	if err := decoder.Decode(&event); err != nil {
		t.Fatal(err)
	}
	if event.DaemonEpoch != syncState.DaemonEpoch || event.StateSequence != syncState.StateSequence+1 {
		t.Fatalf("event identity = %#v, sync = %#v", event, syncState)
	}
}

func TestBroadcastNeverWaitsForNonReadingClient(t *testing.T) {
	home := daemonServerTestHome(t)
	var logs lockedBuffer
	server := NewServer(Config{
		Home:              home,
		Version:           "test",
		Handler:           testHandler{},
		Logger:            slog.New(slog.NewTextHandler(&logs, nil)),
		OutboundMaxFrames: 1,
		OutboundMaxBytes:  2 << 20,
		WriteTimeout:      5 * time.Second,
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
	conn, err := net.Dial("unix", paths.Client)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	encoder, decoder := json.NewEncoder(conn), json.NewDecoder(conn)
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

	payload := map[string]string{"data": strings.Repeat("x", 900_000)}
	started := time.Now()
	for index := 0; index < 4; index++ {
		server.Broadcast("terminal.snapshot", payload)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Broadcast() waited %s for a non-reading client", elapsed)
	}

	waitForConditionForServerTest(t, time.Second, func() bool {
		return strings.Contains(logs.String(), "queue-full")
	}, "slow client queue-full close record")
}

func TestBlockedRequestDoesNotPreventPingOnSameConnection(t *testing.T) {
	home := daemonServerTestHome(t)
	handler := &blockingTestHandler{entered: make(chan struct{})}
	server := NewServer(Config{
		Home:           home,
		Version:        "test",
		Handler:        handler,
		RequestTimeout: time.Second,
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
	waitForServerSocket(t, paths.Socket)
	conn, err := net.Dial("unix", paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	encoder, decoder := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := encoder.Encode(Request{ID: 1, Method: "hello", Params: mustJSONForTest(t, HelloParams{Version: "test", Protocol: ProtocolVersion})}); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}

	if err := encoder.Encode(Request{ID: 2, Method: "blocked"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handler.entered:
	case <-time.After(time.Second):
		t.Fatal("blocked request did not enter handler")
	}
	if err := encoder.Encode(Request{ID: 3, Method: "daemon.ping"}); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if err := decoder.Decode(&response); err != nil {
		t.Fatalf("ping response while another request blocked: %v", err)
	}
	if response.ID != 3 || response.Error != nil {
		t.Fatalf("ping response = %#v, want successful response ID 3", response)
	}
}

type blockingTestHandler struct {
	entered chan struct{}
	once    sync.Once
}

func (h *blockingTestHandler) Handle(ctx context.Context, _ ClientContext, method string, _ json.RawMessage) (any, error) {
	if method == "blocked" {
		h.once.Do(func() { close(h.entered) })
		<-ctx.Done()
		return nil, context.Cause(ctx)
	}
	return map[string]bool{"ok": true}, nil
}

func (*blockingTestHandler) Snapshot(context.Context) (any, error) { return nil, nil }
func (*blockingTestHandler) TerminalCount() int                    { return 0 }
func (*blockingTestHandler) Close() error                          { return nil }

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

type testHandler struct{}

func (testHandler) Handle(context.Context, ClientContext, string, json.RawMessage) (any, error) {
	return map[string]bool{"ok": true}, nil
}
func (testHandler) Snapshot(context.Context) (any, error) { return nil, nil }
func (testHandler) TerminalCount() int                    { return 0 }
func (testHandler) Close() error                          { return nil }

func existsForTest(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mustJSONForTest(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func daemonServerTestHome(t *testing.T) string {
	t.Helper()
	return testutil.TempDir(t, "ds-")
}

func waitForServerSocket(t *testing.T, path string) {
	t.Helper()
	waitForConditionForServerTest(t, time.Second, func() bool {
		return existsForTest(path)
	}, "daemon socket "+path)
}

func dialServerForTest(path string) (net.Conn, error) {
	deadline := time.Now().Add(time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", path)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		time.Sleep(time.Millisecond)
	}
	return nil, lastErr
}

func waitForConditionForServerTest(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}
