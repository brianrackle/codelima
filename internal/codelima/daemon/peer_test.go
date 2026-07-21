//go:build darwin || linux

package daemon

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/brianrackle/codelima/internal/testutil"
)

func TestRequireSameUserPeerAcceptsOwnerConnection(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t, "peer-"), "peer.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	accepted := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			err = RequireSameUserPeer(conn)
			_ = conn.Close()
		}
		accepted <- err
	}()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	if err := <-accepted; err != nil {
		t.Fatalf("RequireSameUserPeer() error = %v", err)
	}
}
