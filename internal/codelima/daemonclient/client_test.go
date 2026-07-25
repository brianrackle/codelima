package daemonclient

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/testutil"
)

func TestClientExactVersionHandshake(t *testing.T) {
	t.Parallel()
	home := testutil.TempDir(t, "dc-")
	paths := daemon.HomePaths(home)
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		decoder, encoder := json.NewDecoder(conn), json.NewEncoder(conn)
		var request daemon.Request
		_ = decoder.Decode(&request)
		data, _ := json.Marshal(daemon.HelloResult{Version: "1.2.3", Protocol: daemon.ProtocolVersion, ClientID: "client"})
		_ = encoder.Encode(daemon.Response{ID: request.ID, Result: data})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := Dial(ctx, Options{Home: filepath.Clean(home), Version: "1.2.3"})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = client.Close() }()
	if client.Hello.ClientID != "client" {
		t.Fatalf("hello = %#v", client.Hello)
	}
}

func TestRequestConnectionSurvivesIdlePastHandshakeTimeout(t *testing.T) {
	t.Parallel()

	home := testutil.TempDir(t, "dc-idle-")
	paths := daemon.HomePaths(home)
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	var accepted atomic.Int32
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			accepted.Add(1)
			go func() {
				defer func() { _ = conn.Close() }()
				decoder, encoder := json.NewDecoder(conn), json.NewEncoder(conn)
				for {
					var request daemon.Request
					if decoder.Decode(&request) != nil {
						return
					}
					var result any = map[string]bool{"ok": true}
					if request.Method == "hello" {
						result = daemon.HelloResult{
							Version: "1.2.3", Protocol: daemon.ProtocolVersion, ClientID: "idle-client",
						}
					}
					data, marshalErr := json.Marshal(result)
					if marshalErr != nil || encoder.Encode(daemon.Response{ID: request.ID, Result: data}) != nil {
						return
					}
				}
			}()
		}
	}()

	const requestTimeout = 75 * time.Millisecond
	client, err := Dial(context.Background(), Options{
		Home: filepath.Clean(home), Version: "1.2.3", Timeout: requestTimeout,
	})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	time.Sleep(3 * requestTimeout)
	var result map[string]bool
	if err := client.Call(context.Background(), "daemon.ping", nil, &result); err != nil {
		t.Fatalf("Call() after idle error = %v", err)
	}
	if !result["ok"] {
		t.Fatalf("Call() after idle result = %#v", result)
	}
	if got := accepted.Load(); got != 1 {
		t.Fatalf("accepted connections = %d, want the original request connection", got)
	}
}

func TestClientReconnectPreservesLogicalIdentityAndIncrementsGeneration(t *testing.T) {
	t.Parallel()

	home := testutil.TempDir(t, "dc-reconnect-")
	paths := daemon.HomePaths(home)
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	hellos := make(chan daemon.HelloParams, 2)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		for connection := uint64(1); connection <= 2; connection++ {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			decoder, encoder := json.NewDecoder(conn), json.NewEncoder(conn)
			var request daemon.Request
			if decoder.Decode(&request) != nil {
				_ = conn.Close()
				return
			}
			var hello daemon.HelloParams
			_ = json.Unmarshal(request.Params, &hello)
			hellos <- hello
			data, _ := json.Marshal(daemon.HelloResult{
				Version:      "1.2.3",
				Protocol:     daemon.ProtocolVersion,
				ClientID:     hello.ClientInstanceID,
				ConnectionID: connection,
				DaemonEpoch:  "epoch-1",
			})
			_ = encoder.Encode(daemon.Response{ID: request.ID, Result: data})

			if connection == 1 {
				// Consume a mutation and drop the acknowledgement. The client
				// must report an uncertain outcome and never replay it.
				_ = decoder.Decode(&request)
				_ = conn.Close()
				continue
			}
			if decoder.Decode(&request) == nil {
				result, _ := json.Marshal(map[string]bool{"ok": true})
				_ = encoder.Encode(daemon.Response{ID: request.ID, Result: result})
			}
			_ = conn.Close()
		}
	}()

	client, err := Dial(context.Background(), Options{
		Home:             filepath.Clean(home),
		Version:          "1.2.3",
		ClientInstanceID: "tui-instance",
	})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	err = client.Call(context.Background(), "terminal.send_input", map[string]any{"data": []byte("x")}, nil)
	var deliveryErr *DeliveryError
	if !errors.As(err, &deliveryErr) || deliveryErr.Outcome != DeliveryOutcomeUnknown {
		t.Fatalf("dropped mutation error = %#v, want DeliveryOutcomeUnknown", err)
	}

	if err := client.Reconnect(context.Background()); err != nil {
		t.Fatalf("Reconnect() error = %v", err)
	}
	var result map[string]bool
	if err := client.Call(context.Background(), "daemon.ping", nil, &result); err != nil {
		t.Fatalf("Call() after reconnect error = %v", err)
	}
	if !result["ok"] {
		t.Fatalf("Call() after reconnect result = %#v", result)
	}

	first, second := <-hellos, <-hellos
	if first.ClientInstanceID != "tui-instance" || second.ClientInstanceID != first.ClientInstanceID {
		t.Fatalf("logical client IDs = (%q, %q), want stable tui-instance", first.ClientInstanceID, second.ClientInstanceID)
	}
	if first.ConnectionGeneration != 1 || second.ConnectionGeneration != 2 {
		t.Fatalf("connection generations = (%d, %d), want (1, 2)", first.ConnectionGeneration, second.ConnectionGeneration)
	}
	<-serverDone
}

func TestConcurrentPingIsNotSerializedBehindBlockedRequest(t *testing.T) {
	t.Parallel()

	home := testutil.TempDir(t, "dc-concurrent-")
	paths := daemon.HomePaths(home)
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	blockedReceived := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		decoder, encoder := json.NewDecoder(conn), json.NewEncoder(conn)
		var request daemon.Request
		if decoder.Decode(&request) != nil {
			return
		}
		hello, _ := json.Marshal(daemon.HelloResult{
			Version: "1.2.3", Protocol: daemon.ProtocolVersion, ClientID: "concurrent",
		})
		if encoder.Encode(daemon.Response{ID: request.ID, Result: hello}) != nil {
			return
		}
		for {
			if decoder.Decode(&request) != nil {
				return
			}
			if request.Method == "blocked" {
				close(blockedReceived)
				continue
			}
			if request.Method == "daemon.ping" {
				result, _ := json.Marshal(map[string]bool{"ok": true})
				_ = encoder.Encode(daemon.Response{ID: request.ID, Result: result})
			}
		}
	}()

	client, err := Dial(context.Background(), Options{
		Home: filepath.Clean(home), Version: "1.2.3", Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	blockedCtx, cancelBlocked := context.WithCancel(context.Background())
	blockedDone := make(chan error, 1)
	go func() {
		blockedDone <- client.Call(blockedCtx, "blocked", nil, nil)
	}()
	select {
	case <-blockedReceived:
	case <-time.After(time.Second):
		t.Fatal("server did not receive blocked request")
	}

	start := time.Now()
	var result map[string]bool
	if err := client.Call(context.Background(), "daemon.ping", nil, &result); err != nil {
		t.Fatalf("concurrent daemon.ping: %v", err)
	}
	if !result["ok"] {
		t.Fatalf("daemon.ping result = %#v", result)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("concurrent daemon.ping waited %s behind blocked request", elapsed)
	}

	cancelBlocked()
	if err := <-blockedDone; err == nil {
		t.Fatal("blocked call succeeded after cancellation")
	}
	_ = client.Close()
	<-serverDone
}
