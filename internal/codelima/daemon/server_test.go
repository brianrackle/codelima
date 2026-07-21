package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
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
	conn, err := net.Dial("unix", paths.Client)
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
	conn, err := net.Dial("unix", paths.Socket)
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
