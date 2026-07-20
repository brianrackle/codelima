package codelima

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/test_lima/internal/codelima/daemon"
)

func TestHandoffTransportUsesFramedUnixStreamWithDescriptorPassing(t *testing.T) {
	root, err := os.MkdirTemp(filepath.Join("..", "..", "tmp"), "hf-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	path := filepath.Join(root, "handoff.sock")

	listener, err := listenHandoff(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	if got := listener.Addr().Network(); got != "unix" {
		t.Fatalf("handoff listener network = %q, want portable Unix stream", got)
	}

	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readPipe.Close(); _ = writePipe.Close() }()

	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer func() { _ = conn.Close() }()
		peer := newHandoffConnection(conn, handoffFramingLengthPrefixed)
		serverDone <- peer.writeJSON(daemon.HandoffMessage{Type: "fds", TerminalIDs: []string{"term-1"}}, []int{int(readPipe.Fd())})
	}()

	peer, err := dialHandoff(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	if peer.framing != handoffFramingLengthPrefixed {
		t.Fatalf("handoff framing = %v, want length-prefixed", peer.framing)
	}
	message, fds, err := peer.readMessage()
	if err != nil {
		t.Fatal(err)
	}
	if message.Type != "fds" || len(fds) != 1 {
		t.Fatalf("handoff message = %#v, fds = %v", message, fds)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}

	transferred := os.NewFile(uintptr(fds[0]), "transferred-pipe")
	if transferred == nil {
		t.Fatal("transferred descriptor is invalid")
	}
	defer func() { _ = transferred.Close() }()
	if _, err := writePipe.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	_ = transferred.SetReadDeadline(time.Now().Add(time.Second))
	data := make([]byte, 1)
	if _, err := transferred.Read(data); err != nil || string(data) != "x" {
		t.Fatalf("read transferred descriptor = %q, %v", data, err)
	}
}

func TestHandoffTransportRejectsDescriptorsOnControlMessage(t *testing.T) {
	root, err := os.MkdirTemp(filepath.Join("..", "..", "tmp"), "hf-control-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	path := filepath.Join(root, "handoff.sock")

	listener, err := listenHandoff(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readPipe.Close(); _ = writePipe.Close() }()

	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer func() { _ = conn.Close() }()
		peer := newHandoffConnection(conn, handoffFramingLengthPrefixed)
		serverDone <- peer.writeJSON(daemon.HandoffMessage{Type: "ready"}, []int{int(readPipe.Fd())})
	}()

	peer, err := dialHandoff(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	if _, err := peer.readControlMessage(); err == nil || !strings.Contains(err.Error(), "unexpectedly carried descriptors") {
		t.Fatalf("readControlMessage() error = %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestUnsupportedLegacyHandoffTransportDetection(t *testing.T) {
	t.Parallel()
	legacy := daemon.Error(
		"ExternalCommandFailed",
		"daemon live update failed: listen unixpacket /tmp/handoff.sock: socket: protocol not supported",
		ExitExternalFailure,
		nil,
	)
	if !isUnsupportedLegacyHandoffTransport(legacy) {
		t.Fatal("legacy macOS unixpacket failure was not recognized")
	}
	if isUnsupportedLegacyHandoffTransport(context.DeadlineExceeded) {
		t.Fatal("unrelated update failure would trigger destructive restart fallback")
	}
	if isUnsupportedLegacyHandoffTransport(daemon.Error("ExternalCommandFailed", "protocol not supported", ExitExternalFailure, nil)) {
		t.Fatal("generic protocol error would trigger legacy fallback")
	}
	if !strings.Contains(legacy.Error(), "unixpacket") {
		t.Fatal("test fixture does not describe the legacy transport")
	}
}
