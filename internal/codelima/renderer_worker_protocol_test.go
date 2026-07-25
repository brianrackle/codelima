package codelima

import (
	"bytes"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"
)

func TestRendererWorkerPathBesideMainExecutable(t *testing.T) {
	t.Parallel()

	got := rendererWorkerPathBeside("/opt/codelima/bin/codelima-real")
	if want := "/opt/codelima/bin/codelima-renderer-worker"; got != want {
		t.Fatalf("rendererWorkerPathBeside() = %q, want %q", got, want)
	}
}

func TestRendererInputEventIDsAreGenerationScoped(t *testing.T) {
	t.Parallel()

	if got := rendererInputEventID(42); got != rendererInputEventBit|42 {
		t.Fatalf("rendererInputEventID(42) = %#x", got)
	}
}

func TestRendererReplaySuppressesTerminalResponses(t *testing.T) {
	t.Parallel()

	serverConn, peerConn := net.Pipe()
	defer func() {
		_ = serverConn.Close()
		_ = peerConn.Close()
	}()
	server := &rendererWorkerServer{conn: serverConn}
	server.replaying.Store(true)
	done := make(chan struct{})
	go func() {
		server.postPTYWrite(7, 1, []byte("response"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("replay attempted to write a terminal response")
	}
	_ = peerConn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	var one [1]byte
	if _, err := peerConn.Read(one[:]); err == nil {
		t.Fatal("replay emitted a terminal response")
	}
}

func TestRendererWorkerFrameRoundTripAndSizeBound(t *testing.T) {
	t.Parallel()

	params, err := marshalRendererParams(rendererOutputParams{EventID: 7, Data: []byte("hello")})
	if err != nil {
		t.Fatal(err)
	}
	want := rendererWorkerFrame{
		Type:       rendererFrameRequest,
		ID:         3,
		Generation: 2,
		Method:     "output",
		Params:     params,
	}
	var stream bytes.Buffer
	if err := writeRendererFrame(&stream, want); err != nil {
		t.Fatal(err)
	}
	got, err := readRendererFrame(&stream)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != want.Type || got.ID != want.ID || got.Generation != want.Generation || got.Method != want.Method || string(got.Params) != string(want.Params) {
		t.Fatalf("renderer frame = %#v, want %#v", got, want)
	}

	var oversized bytes.Buffer
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, rendererWorkerMaxFrameSize+1)
	oversized.Write(header)
	if _, err := readRendererFrame(&oversized); err == nil || !strings.Contains(err.Error(), "invalid renderer frame size") {
		t.Fatalf("oversized renderer frame error = %v", err)
	}
}

func TestRendererJournalIsBoundedAndPreservesEventIDs(t *testing.T) {
	t.Parallel()

	journal := newRendererJournal(5)
	first := journal.AppendOutput([]byte("1234"))
	second := journal.AppendResize(100, 30)
	third := journal.AppendOutput([]byte("5678"))
	snapshot := journal.Snapshot()

	if first.ID != 1 || second.ID != 2 || third.ID != 3 {
		t.Fatalf("journal IDs = %d, %d, %d", first.ID, second.ID, third.ID)
	}
	if !snapshot.Partial {
		t.Fatal("bounded journal did not report partial recovery after truncation")
	}
	if snapshot.Bytes > 5 {
		t.Fatalf("journal bytes = %d, want <= 5", snapshot.Bytes)
	}
	if len(snapshot.Events) != 2 || snapshot.Events[0].ID != second.ID || snapshot.Events[1].ID != third.ID {
		t.Fatalf("journal events = %#v", snapshot.Events)
	}
}
