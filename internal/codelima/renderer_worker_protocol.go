package codelima

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const rendererWorkerMaxFrameSize = 64 * 1024 * 1024
const rendererInputEventBit = uint64(1) << 63

// rendererWorkerProtocolVersion is the version of the frame protocol spoken
// between the daemon-side supervisor and a renderer worker process. The worker
// is a separate executable resolved beside the running binary at spawn time
// (see resolveRendererWorkerExecutable), so the two can be different builds:
// an upgrade that replaces codelima but leaves an old codelima-renderer-worker
// on disk, a package layout that installs them apart, or a partially-written
// install directory. Without a version the mismatch is silent -- the frames
// still decode as JSON, they just mean something else -- so the worker
// announces this value in its init reply and the supervisor refuses to treat
// the link as ready unless it matches exactly.
//
// Version 1 is the original unversioned protocol: it announced nothing, and
// per-cell snapshot payloads used the Go field names verbatim. Version 2 adds
// this handshake and moves snapshot cells to the compact encoding in
// daemon.SnapshotCell. A worker that predates versioning sends no version at
// all, which decodes as 0 and is rejected as a mismatch -- exactly the intent.
const rendererWorkerProtocolVersion = 2

// errRendererProtocolMismatch marks a version disagreement between this binary
// and the renderer worker beside it. It is deliberately distinct from every
// other renderer failure because it is the only permanent one: the two
// executables on disk do not agree, and no amount of restarting changes that,
// so the supervisor must not spend its restart budget discovering it again.
var errRendererProtocolMismatch = errors.New("renderer worker protocol version mismatch")

const (
	rendererFrameRequest  = "request"
	rendererFrameResponse = "response"
	rendererFrameSnapshot = "snapshot"
	rendererFramePTYWrite = "pty_write"
	rendererFrameEvent    = "event"
)

type rendererWorkerFrame struct {
	Type       string          `json:"type"`
	ID         uint64          `json:"id,omitempty"`
	Generation uint64          `json:"generation"`
	NoReply    bool            `json:"no_reply,omitempty"`
	Method     string          `json:"method,omitempty"`
	Params     json.RawMessage `json:"params,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	EventID    uint64          `json:"event_id,omitempty"`
	Ordinal    uint32          `json:"ordinal,omitempty"`
	Event      string          `json:"event,omitempty"`
}

type rendererInitParams struct {
	TerminalID string                 `json:"terminal_id"`
	Cols       int                    `json:"cols"`
	Rows       int                    `json:"rows"`
	Journal    []rendererJournalEvent `json:"journal,omitempty"`
}

// rendererInitResult is the worker's reply to "init" and the carrier of the
// version handshake. init is the natural place for it: it is the worker's first
// frame, it is a request/response exchange the supervisor already waits on
// before installing the link, and nothing else has been read from the worker at
// that point -- so a mismatched worker is rejected before a single snapshot or
// PTY-write frame has been interpreted.
type rendererInitResult struct {
	Protocol int  `json:"protocol"`
	Ready    bool `json:"ready"`
}

// verifyRendererProtocol checks the version a worker announced in its init
// reply, naming both binaries so the operator knows which one is stale.
//
// A missing version is a mismatch, not a tolerated legacy case: a worker that
// predates the version field simply omits it, which unmarshals as 0, and its
// frame encoding is precisely the skew this check exists to reject. Treating 0
// as "assume compatible" would reinstate the silent misinterpretation.
func verifyRendererProtocol(executable string, raw json.RawMessage) error {
	var result rendererInitResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf(
			"%w: renderer worker %s sent an init reply this binary cannot decode (%w); it speaks a different protocol",
			errRendererProtocolMismatch, executable, err,
		)
	}
	if result.Protocol == rendererWorkerProtocolVersion {
		return nil
	}
	announced := fmt.Sprintf("version %d", result.Protocol)
	if result.Protocol == 0 {
		announced = "no version at all (a worker built before the protocol was versioned)"
	}
	return fmt.Errorf(
		"%w: renderer worker %s announced %s but this binary speaks version %d; "+
			"the codelima binary and the codelima-renderer-worker beside it are from different builds -- reinstall so both come from the same one",
		errRendererProtocolMismatch, executable, announced, rendererWorkerProtocolVersion,
	)
}

type rendererOutputParams struct {
	EventID uint64 `json:"event_id"`
	Data    []byte `json:"data"`
}

type rendererResizeParams struct {
	EventID uint64 `json:"event_id"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
}

type rendererUpdateParams struct {
	Event rendererInputEvent `json:"event"`
}

type rendererInputEvent struct {
	Type      string `json:"type"`
	Keycode   rune   `json:"keycode,omitempty"`
	Shifted   rune   `json:"shifted_code,omitempty"`
	Text      string `json:"text,omitempty"`
	Modifiers uint8  `json:"modifiers,omitempty"`
	EventType uint8  `json:"event_type,omitempty"`
	Col       int    `json:"col,omitempty"`
	Row       int    `json:"row,omitempty"`
	Button    int    `json:"button,omitempty"`
	Focused   bool   `json:"focused,omitempty"`
	Delta     int    `json:"delta,omitempty"`
}

// rendererPublishedState is what a renderer pushes on every screen change. It
// carries the cell grid and the plain visible text only: the visible text is
// the terminal's current-screen accessor and one viewport pass, while the ANSI
// viewport render and both scrollback renders cost a full grid or scrollback
// walk each (one bridge call per scrollback row) and are almost never read.
// Those are fetched with the "read" request when a terminal.read actually asks
// for one, instead of being recomputed and shipped up to 20 times a second.
type rendererPublishedState struct {
	Snapshot    TerminalSnapshot `json:"snapshot"`
	VisibleText ReadResultDTO    `json:"visible_text"`
}

// rendererReadSourceRecent and rendererReadFormatANSI are the wire spellings of
// the non-default read variant selectors.
const (
	rendererReadSourceRecent = "recent"
	rendererReadFormatANSI   = "ansi"
)

type rendererReadParams struct {
	Source string `json:"source,omitempty"`
	Format string `json:"format,omitempty"`
}

func rendererReadRequest(source ReadSource, format ReadFormat) rendererReadParams {
	params := rendererReadParams{}
	if source == ReadRecent {
		params.Source = rendererReadSourceRecent
	}
	if format == ReadANSI {
		params.Format = rendererReadFormatANSI
	}
	return params
}

func (p rendererReadParams) selectors() (ReadSource, ReadFormat) {
	source, format := ReadVisible, ReadText
	if p.Source == rendererReadSourceRecent {
		source = ReadRecent
	}
	if p.Format == rendererReadFormatANSI {
		format = ReadANSI
	}
	return source, format
}

type ReadResultDTO struct {
	Text       string `json:"text"`
	Generation uint64 `json:"generation"`
	Error      string `json:"error,omitempty"`
}

func readResultDTO(result ReadResult) ReadResultDTO {
	dto := ReadResultDTO{Text: result.Text, Generation: result.Generation}
	if result.Err != nil {
		dto.Error = result.Err.Error()
	}
	return dto
}

func (d ReadResultDTO) readResult() ReadResult {
	result := ReadResult{Text: d.Text, Generation: d.Generation}
	if d.Error != "" {
		result.Err = errors.New(d.Error)
	}
	return result
}

func marshalRendererParams(value any) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal renderer params: %w", err)
	}
	return data, nil
}

func rendererInputEventID(requestID uint64) uint64 {
	return requestID | rendererInputEventBit
}

func writeRendererFrame(writer io.Writer, frame rendererWorkerFrame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("marshal renderer frame: %w", err)
	}
	if len(data) == 0 || len(data) > rendererWorkerMaxFrameSize {
		return fmt.Errorf("renderer frame size %d is outside 1..%d", len(data), rendererWorkerMaxFrameSize)
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if err := writeRendererBytes(writer, header); err != nil {
		return err
	}
	return writeRendererBytes(writer, data)
}

func readRendererFrame(reader io.Reader) (rendererWorkerFrame, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return rendererWorkerFrame{}, err
	}
	size := binary.BigEndian.Uint32(header)
	if size == 0 || size > rendererWorkerMaxFrameSize {
		return rendererWorkerFrame{}, fmt.Errorf("invalid renderer frame size %d", size)
	}
	data := make([]byte, int(size))
	if _, err := io.ReadFull(reader, data); err != nil {
		return rendererWorkerFrame{}, err
	}
	var frame rendererWorkerFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return rendererWorkerFrame{}, fmt.Errorf("decode renderer frame: %w", err)
	}
	return frame, nil
}

func writeRendererBytes(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}
