package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
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

	if !MutatingInputMethod("terminal.move") {
		t.Fatalf("terminal.move must be protected as a mutating input method")
	}
}

// TestHelloRejectsASkewedProtocolBeforeAnyPayloadFlows is the check that makes
// bumping ProtocolVersion a safe way to retire a wire shape. The version is
// exact-match (ADR 65) and is settled in the very first exchange, before any
// snapshot, event or result crosses the connection -- so a client built against
// the previous per-cell encoding is turned away with a stated cause rather than
// left to misparse compact cells as verbose ones.
func TestHelloRejectsASkewedProtocolBeforeAnyPayloadFlows(t *testing.T) {
	home := daemonServerTestHome(t)
	server := NewServer(Config{Home: home, Version: "test", Handler: testHandler{}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()
	paths := HomePaths(home)
	waitForConditionForServerTest(t, time.Second, func() bool { return existsForTest(paths.Socket) }, "daemon request socket")

	for name, hello := range map[string]HelloParams{
		"older protocol":  {Version: "test", Protocol: ProtocolVersion - 1},
		"newer protocol":  {Version: "test", Protocol: ProtocolVersion + 1},
		"missing version": {Version: "test"},
		"other build":     {Version: "some-other-build", Protocol: ProtocolVersion},
	} {
		conn, err := dialServerForTest(paths.Socket)
		if err != nil {
			t.Fatal(err)
		}
		encoder, decoder := json.NewEncoder(conn), json.NewDecoder(conn)
		if err := encoder.Encode(Request{ID: 1, Method: "hello", Params: mustJSONForTest(t, hello)}); err != nil {
			t.Fatal(err)
		}
		var response Response
		if err := decoder.Decode(&response); err != nil {
			t.Fatalf("%s: hello response: %v", name, err)
		}
		if response.Error == nil || response.Error.Code != CodePreconditionFailed ||
			!strings.Contains(response.Error.Message, "daemon version mismatch") {
			t.Fatalf("%s: hello response = %#v, want an explicit version rejection", name, response)
		}
		// The connection is closed by the rejection, so nothing further can be
		// exchanged under an unagreed protocol.
		if err := decoder.Decode(&response); err == nil {
			t.Fatalf("%s: connection stayed usable after a version rejection", name)
		}
		_ = conn.Close()
	}
}

func TestHomePathsStayUnderPrivateDaemonDirectory(t *testing.T) {
	t.Parallel()
	paths := HomePaths("/home/demo")
	if paths.Socket != "/home/demo/_daemon/daemon.sock" || paths.Client != "/home/demo/_daemon/client.sock" || paths.Session != "/home/demo/_daemon/session.json" {
		t.Fatalf("HomePaths() = %#v", paths)
	}
}

// The daemon's private surface is 0600 sockets inside a 0700 directory. It is
// established without touching the process-global umask, so it must hold on the
// live-update resume path too, which binds its own listeners.
func TestDaemonSocketsStayOwnerOnlyAcrossReplacementResume(t *testing.T) {
	home := daemonServerTestHome(t)
	paths := HomePaths(home)
	server := NewServer(Config{Home: home, Version: "test", Handler: testHandler{}})
	// prepare, PrepareReplacement and ResumeAfterReplacement are each fully
	// synchronous, so driving them directly exercises both bind paths with no
	// concurrent Run. That is deliberate: they touch the listener fields without
	// holding s.mu, and a filesystem readiness poll is not a happens-before edge
	// the race detector can observe, so waiting on the pid file and then calling
	// them from the test goroutine reports a race that the production ordering
	// (prepare -> acceptLoop -> serveConn -> handler) does not actually have.
	if err := server.prepare(); err != nil {
		t.Fatalf("prepare() error = %v", err)
	}
	t.Cleanup(func() {
		server.stopAccepting()
		server.wg.Wait()
		server.cleanup()
	})
	assertPrivateDaemonSurfaceForTest(t, paths, "initial bind")

	if err := server.PrepareReplacement(); err != nil {
		t.Fatalf("PrepareReplacement() error = %v", err)
	}
	if err := server.ResumeAfterReplacement(); err != nil {
		t.Fatalf("ResumeAfterReplacement() error = %v", err)
	}
	assertPrivateDaemonSurfaceForTest(t, paths, "resume after replacement")
}

// MkdirAll does not touch an existing directory's mode, so a daemon directory
// left readable by an older build or a loose umask has to be tightened on
// reuse rather than trusted.
func TestRunTightensAPreexistingGroupReadableDaemonDirectory(t *testing.T) {
	home := daemonServerTestHome(t)
	paths := HomePaths(home)
	for _, dir := range []string{paths.Dir, filepath.Dir(paths.Lock)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	server := NewServer(Config{Home: home, Version: "test", Handler: testHandler{}})
	runServerUntilCleanupForTest(t, server, paths)
	assertPrivateDaemonSurfaceForTest(t, paths, "reused loose directory")
	if info, err := os.Stat(filepath.Dir(paths.Lock)); err != nil {
		t.Fatal(err)
	} else if mode := info.Mode().Perm(); mode != 0o700 {
		t.Fatalf("lock dir mode = %#o, want 0700", mode)
	}
}

// runServerUntilCleanupForTest starts server and returns once it is fully
// bound. It waits for the PID file, which prepare writes only after both
// listeners are bound and chmodded, so callers never observe the socket in the
// instant it exists at the default mode. Shutdown is registered as cleanup so a
// failed assertion still unbinds the sockets.
func runServerUntilCleanupForTest(t *testing.T, server *Server, paths Paths) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Server.Run() error = %v", err)
		}
	})
	waitForConditionForServerTest(t, 5*time.Second, func() bool {
		return existsForTest(paths.PID)
	}, "daemon pid file "+paths.PID)
}

func assertPrivateDaemonSurfaceForTest(t *testing.T, paths Paths, phase string) {
	t.Helper()
	info, err := os.Stat(paths.Dir)
	if err != nil {
		t.Fatalf("%s: stat daemon dir: %v", phase, err)
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Fatalf("%s: daemon dir mode = %#o, want 0700", phase, mode)
	}
	// A few filesystems (virtiofs, some network mounts) reject chmod on a socket
	// inode, so the guaranteed invariant is "0600, or unreachable because the
	// directory is 0700" -- which is what listenPrivate enforces. Assert the
	// exact mode wherever the filesystem can actually hold it.
	chmodHonored := socketChmodHonoredForTest(t, paths.Dir)
	for _, path := range []string{paths.Socket, paths.Client} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s: stat %s: %v", phase, path, err)
		}
		mode := info.Mode().Perm()
		if chmodHonored && mode != 0o600 {
			t.Fatalf("%s: %s mode = %#o, want 0600", phase, path, mode)
		}
		if !chmodHonored && mode&0o077 != 0 {
			t.Logf("%s: %s mode = %#o; filesystem refuses chmod on sockets, relying on the 0700 directory", phase, path, mode)
		}
	}
}

// When chmod on the socket is refused, the 0700 directory is the only thing
// keeping other users out, so listenPrivate fails closed on it rather than
// binding an unreachable-only-by-luck socket.
func TestVerifyPrivateDirRejectsAnyGroupOrOtherAccess(t *testing.T) {
	t.Parallel()
	dir := testutil.TempDir(t, "vpd-")
	// Always hand the directory back in a removable state, including on failure.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	for _, mode := range []os.FileMode{0o700, 0o600, 0o500} {
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		if err := verifyPrivateDir(dir); err != nil {
			t.Fatalf("verifyPrivateDir(%#o) = %v, want nil", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o755, 0o750, 0o701, 0o770, 0o707} {
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		if err := verifyPrivateDir(dir); err == nil {
			t.Fatalf("verifyPrivateDir(%#o) = nil, want an error", mode)
		}
	}
}

// socketChmodHonoredForTest reports whether dir's filesystem lets a Unix socket
// be chmodded, using a throwaway socket beside the ones under test.
func socketChmodHonoredForTest(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "chmod-probe.sock")
	_ = os.Remove(probe)
	listener, err := net.Listen("unix", probe)
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(probe)
	}()
	if err := os.Chmod(probe, 0o600); err != nil {
		return false
	}
	info, err := os.Stat(probe)
	if err != nil {
		t.Fatalf("probe stat: %v", err)
	}
	return info.Mode().Perm() == 0o600
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
	conn, err := dialServerForTest(paths.Client)
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

// TestBroadcastWithUnencodablePayloadConsumesNoStateSequence pins the
// invariant that makes reconnect storms impossible from the daemon side: an
// event whose payload cannot be encoded must not burn a state sequence. A
// consumed-but-unsent revision leaves a gap, and a subscriber that sees a gap
// answers it with a full resync reconnect (ADR 107).
func TestBroadcastWithUnencodablePayloadConsumesNoStateSequence(t *testing.T) {
	home := daemonServerTestHome(t)
	var logs lockedBuffer
	server := NewServer(Config{
		Home:    home,
		Version: "test",
		Handler: testHandler{},
		Logger:  slog.New(slog.NewTextHandler(&logs, nil)),
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
		Version: "test", Protocol: ProtocolVersion, ClientInstanceID: "tui-1",
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
		t.Fatalf("sync response = %#v, %v", response, err)
	}
	var syncState SyncSnapshot
	if err := DecodeResult(response, &syncState); err != nil {
		t.Fatal(err)
	}

	// A channel has no JSON encoding, so this Broadcast must be dropped whole.
	server.Broadcast("terminal.dirty", make(chan int))
	server.Broadcast("terminal.dirty", map[string]string{"terminal_id": "term-1"})

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var event Event
	if err := decoder.Decode(&event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event.Event != "terminal.dirty" {
		t.Fatalf("event = %#v", event)
	}
	if event.StateSequence != syncState.StateSequence+1 {
		t.Fatalf("state sequence = %d, want %d: the dropped event consumed a revision and opened a resync gap",
			event.StateSequence, syncState.StateSequence+1)
	}
	if !strings.Contains(logs.String(), "encode daemon event failed") {
		t.Fatalf("expected an encode-failure record, got %q", logs.String())
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
	conn, err := dialServerForTest(paths.Client)
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
	conn, err := dialServerForTest(paths.Socket)
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
