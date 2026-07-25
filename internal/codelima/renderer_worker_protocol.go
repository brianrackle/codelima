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

type rendererPublishedState struct {
	Snapshot    TerminalSnapshot `json:"snapshot"`
	VisibleText ReadResultDTO    `json:"visible_text"`
	VisibleANSI ReadResultDTO    `json:"visible_ansi"`
	RecentText  ReadResultDTO    `json:"recent_text"`
	RecentANSI  ReadResultDTO    `json:"recent_ansi"`
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
