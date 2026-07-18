package daemonclient

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brianrackle/test_lima/internal/codelima/daemon"
)

func TestClientExactVersionHandshake(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
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
