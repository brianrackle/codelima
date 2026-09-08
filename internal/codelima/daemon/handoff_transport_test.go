package daemon

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/testutil"
)

// handoffTestTempDir creates a temp dir under the repo-rooted tmp/ so Unix
// socket paths stay within the kernel sun_path limit (t.TempDir can exceed it).
func handoffTestTempDir(t *testing.T, prefix string) string {
	t.Helper()
	return testutil.TempDir(t, prefix)
}

func TestHandoffTransportUsesFramedUnixStreamWithDescriptorPassing(t *testing.T) {
	root := handoffTestTempDir(t, "hf-")
	path := filepath.Join(root, "handoff.sock")

	listener, err := ListenHandoff(path)
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
		peer := NewHandoffConnection(conn, HandoffFramingLengthPrefixed)
		serverDone <- peer.WriteJSON(HandoffMessage{Type: "fds", TerminalIDs: []string{"term-1"}}, []int{int(readPipe.Fd())})
	}()

	peer, err := DialHandoff(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	if peer.Framing != HandoffFramingLengthPrefixed {
		t.Fatalf("handoff framing = %v, want length-prefixed", peer.Framing)
	}
	message, fds, err := peer.ReadMessage()
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
	root := handoffTestTempDir(t, "hf-control-")
	path := filepath.Join(root, "handoff.sock")

	listener, err := ListenHandoff(path)
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
		peer := NewHandoffConnection(conn, HandoffFramingLengthPrefixed)
		serverDone <- peer.WriteJSON(HandoffMessage{Type: "ready"}, []int{int(readPipe.Fd())})
	}()

	peer, err := DialHandoff(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	if _, err := peer.ReadControlMessage(); err == nil || !strings.Contains(err.Error(), "unexpectedly carried descriptors") {
		t.Fatalf("ReadControlMessage() error = %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestHandoffTransportChunksFullTerminalReplay(t *testing.T) {
	testHandoffBlobRoundTrip(t, MaxHandoffReplayBytesPerTerminal, (*HandoffConnection).WriteReplay, (*HandoffConnection).ReadReplay)
}

func TestHandoffTransportChunksBoundedRecoveryBundle(t *testing.T) {
	testHandoffBlobRoundTrip(t, MaxHandoffRecoveryBytesPerTerminal, (*HandoffConnection).WriteRecovery, (*HandoffConnection).ReadRecovery)
	connection := &HandoffConnection{}
	if _, err := connection.ReadRecovery("terminal", MaxHandoffRecoveryBytesPerTerminal+1); err == nil {
		t.Fatal("oversized recovery accepted before allocation")
	}
	if _, err := connection.ReadRecovery("terminal", -1); err == nil {
		t.Fatal("negative recovery accepted before allocation")
	}
}

func TestHandoffRecoveryManifestBoundsBeforePayloadAllocation(t *testing.T) {
	for _, manifest := range []HandoffManifest{
		{Version: HandoffVersion, Runtimes: []HandoffRuntime{{RecoverySize: -1}}},
		{Version: HandoffVersion, Runtimes: []HandoffRuntime{{RecoverySize: MaxHandoffRecoveryBytesPerTerminal + 1}}},
		{Version: PreviousChunkedHandoffVersion, Runtimes: []HandoffRuntime{{RecoverySize: 1}}},
		{Version: HandoffVersion, Runtimes: []HandoffRuntime{{RecoverySize: 24 << 20}, {RecoverySize: 24 << 20}, {RecoverySize: 24 << 20}}},
	} {
		if err := ValidateHandoffRecoverySizes(manifest); err == nil {
			t.Fatalf("accepted invalid recovery sizes: %+v", manifest)
		}
	}
	if err := ValidateHandoffRecoverySizes(HandoffManifest{Version: HandoffVersion, Runtimes: []HandoffRuntime{{RecoverySize: 24 << 20}, {RecoverySize: 24 << 20}, {RecoverySize: 16 << 20}}}); err != nil {
		t.Fatalf("rejected exact aggregate bound: %v", err)
	}
}

func testHandoffBlobRoundTrip(t *testing.T, size int, write func(*HandoffConnection, string, []byte) error, read func(*HandoffConnection, string, int) ([]byte, error)) {
	t.Helper()
	root := handoffTestTempDir(t, "hf-replay-")
	path := filepath.Join(root, "handoff.sock")

	listener, err := ListenHandoff(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	replay := make([]byte, size)
	for index := range replay {
		replay[index] = byte(index % 251)
	}
	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer func() { _ = conn.Close() }()
		serverDone <- write(NewHandoffConnection(conn, HandoffFramingLengthPrefixed), "term-large", replay)
	}()

	peer, err := DialHandoff(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	got, err := read(peer, "term-large", len(replay))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, replay) {
		t.Fatalf("chunked replay differs: got %d bytes, want %d", len(got), len(replay))
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestHandoffTransportRejectsOversizedTerminalReplay(t *testing.T) {
	connection := &HandoffConnection{}
	err := connection.WriteReplay("term-oversized", make([]byte, MaxHandoffReplayBytesPerTerminal+1))
	if err == nil || !strings.Contains(err.Error(), "limit is") {
		t.Fatalf("WriteReplay() error = %v, want explicit replay limit", err)
	}
}
