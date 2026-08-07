package codelima

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
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

	limit := rendererResizeEventBytes + 5
	journal := newRendererJournal(limit)
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
	if snapshot.Bytes > limit {
		t.Fatalf("journal bytes = %d, want <= %d", snapshot.Bytes, limit)
	}
	if len(snapshot.Events) != 2 || snapshot.Events[0].ID != second.ID || snapshot.Events[1].ID != third.ID {
		t.Fatalf("journal events = %#v", snapshot.Events)
	}
}

type rendererTornReader struct {
	chunks [][]byte
}

func (r *rendererTornReader) Read(buffer []byte) (int, error) {
	for len(r.chunks) > 0 && len(r.chunks[0]) == 0 {
		r.chunks = r.chunks[1:]
	}
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	read := copy(buffer, r.chunks[0])
	r.chunks[0] = r.chunks[0][read:]
	return read, nil
}

type rendererDripWriter struct {
	written bytes.Buffer
}

func (w *rendererDripWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	w.written.WriteByte(data[0])
	return 1, nil
}

// TestRendererFrameSurvivesTornStreams covers the framing contract the worker
// link depends on: a control socket delivers arbitrary byte boundaries, so a
// frame split anywhere must reassemble, a truncated frame must fail cleanly
// instead of decoding garbage, and a partial write must be completed.
func TestRendererFrameSurvivesTornStreams(t *testing.T) {
	t.Parallel()

	params, err := marshalRendererParams(rendererOutputParams{EventID: 9, Data: []byte("torn frame payload")})
	if err != nil {
		t.Fatal(err)
	}
	want := rendererWorkerFrame{
		Type:       rendererFrameRequest,
		ID:         11,
		Generation: 4,
		Method:     "output",
		Params:     params,
	}
	var stream bytes.Buffer
	if err := writeRendererFrame(&stream, want); err != nil {
		t.Fatal(err)
	}
	encoded := stream.Bytes()

	for split := range len(encoded) + 1 {
		reader := &rendererTornReader{chunks: [][]byte{encoded[:split], encoded[split:]}}
		got, err := readRendererFrame(reader)
		if err != nil {
			t.Fatalf("frame split at %d: %v", split, err)
		}
		if got.ID != want.ID || got.Generation != want.Generation || got.Method != want.Method ||
			string(got.Params) != string(want.Params) {
			t.Fatalf("frame split at %d reassembled as %#v", split, got)
		}
	}

	for cut := 1; cut < len(encoded); cut++ {
		// A cut exactly on the header boundary reads no payload bytes at all,
		// which io.ReadFull reports as a plain EOF; anywhere else is a short
		// read. Either way the frame must be rejected, never half-decoded.
		_, err := readRendererFrame(bytes.NewReader(encoded[:cut]))
		if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			t.Fatalf("frame truncated at %d gave err = %v, want a truncation error", cut, err)
		}
	}
	if _, err := readRendererFrame(bytes.NewReader(nil)); !errors.Is(err, io.EOF) {
		t.Fatalf("empty stream err = %v, want %v", err, io.EOF)
	}

	writer := &rendererDripWriter{}
	if err := writeRendererFrame(writer, want); err != nil {
		t.Fatalf("one-byte-at-a-time write: %v", err)
	}
	if !bytes.Equal(writer.written.Bytes(), encoded) {
		t.Fatal("one-byte-at-a-time write did not reproduce the frame")
	}
}
