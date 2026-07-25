package daemon

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// ErrHandoffTransportUnsupported marks a handoff-listen failure caused by the
// platform lacking the required socket transport. The daemon.update handler
// tags its RPC error with the handoff_transport_unsupported field when the
// cause wraps this sentinel, so clients discriminate on the typed field
// instead of the error text.
var ErrHandoffTransportUnsupported = errors.New("handoff transport unsupported")

const (
	// MaxHandoffFDsPerFrame bounds how many descriptors one handoff frame may
	// carry. The sender batches terminal PTYs to this size and the receiver
	// allocates its ancillary-data buffer from the same constant, so the two
	// sides cannot drift apart.
	MaxHandoffFDsPerFrame = 64

	// MaxHandoffReplayBytesPerTerminal matches the renderer journal bound.
	// Replay travels in smaller JSON chunks because base64 expansion makes a
	// full 1 MiB byte slice larger than MaxMessageSize on the wire.
	MaxHandoffReplayBytesPerTerminal = 1 << 20
	MaxHandoffReplayChunkBytes       = MaxMessageSize / 2
	MaxHandoffTotalReplayBytes       = MaxHandoffFDsPerFrame * MaxHandoffReplayBytesPerTerminal
)

type HandoffFraming uint8

const (
	HandoffFramingLegacyPacket HandoffFraming = iota
	HandoffFramingLengthPrefixed
)

type HandoffConnection struct {
	conn    *net.UnixConn
	Framing HandoffFraming
}

func NewHandoffConnection(conn *net.UnixConn, framing HandoffFraming) *HandoffConnection {
	return &HandoffConnection{conn: conn, Framing: framing}
}

func (c *HandoffConnection) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// ListenHandoff uses SOCK_STREAM because Darwin does not implement Unix
// SOCK_SEQPACKET. SCM_RIGHTS works on Unix streams on both Darwin and Linux;
// explicit length framing supplies the message boundaries streams lack.
func ListenHandoff(path string) (*net.UnixListener, error) {
	if os.Getenv("CODELIMA_HANDOFF_FORCE_UNSUPPORTED_TRANSPORT") == "1" {
		// The message keeps the legacy "unixpacket ... protocol not supported"
		// text so pre-typed-field clients still recognize the condition.
		return nil, fmt.Errorf("listen unixpacket %s: socket: protocol not supported: %w", path, ErrHandoffTransportUnsupported)
	}
	return net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
}

// DialHandoff prefers the portable framed stream. The unixpacket fallback is
// importer-only compatibility with the immediately previous Linux daemon,
// whose listener and packet framing are already fixed in the running process.
func DialHandoff(path string) (*HandoffConnection, error) {
	conn, streamErr := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if streamErr == nil {
		return NewHandoffConnection(conn, HandoffFramingLengthPrefixed), nil
	}
	legacy, legacyErr := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: path, Net: "unixpacket"})
	if legacyErr == nil {
		return NewHandoffConnection(legacy, HandoffFramingLegacyPacket), nil
	}
	return nil, fmt.Errorf("dial handoff stream: %w (legacy unixpacket: %w)", streamErr, legacyErr)
}

func (c *HandoffConnection) WriteJSON(value any, fds []int) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > MaxMessageSize {
		return fmt.Errorf("handoff message size %d is outside 1..%d", len(data), MaxMessageSize)
	}
	payload := data
	if c.Framing == HandoffFramingLengthPrefixed {
		payload = make([]byte, 4+len(data))
		binary.BigEndian.PutUint32(payload[:4], uint32(len(data)))
		copy(payload[4:], data)
	}
	var rights []byte
	if len(fds) > 0 {
		rights = unix.UnixRights(fds...)
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	n, _, err := c.conn.WriteMsgUnix(payload, rights, nil)
	if err != nil {
		return err
	}
	if c.Framing == HandoffFramingLegacyPacket {
		if n != len(payload) {
			return io.ErrShortWrite
		}
		return nil
	}
	for n < len(payload) {
		written, writeErr := c.conn.Write(payload[n:])
		if writeErr != nil {
			return writeErr
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		n += written
	}
	return nil
}

func (c *HandoffConnection) ReadMessage() (HandoffMessage, []int, error) {
	data, fds, err := c.readPayload()
	if err != nil {
		return HandoffMessage{}, nil, err
	}
	var message HandoffMessage
	if err := json.Unmarshal(data, &message); err != nil {
		CloseHandoffFDs(fds)
		return HandoffMessage{}, nil, err
	}
	return message, fds, nil
}

func (c *HandoffConnection) ReadControlMessage() (HandoffMessage, error) {
	message, fds, err := c.ReadMessage()
	if err != nil {
		return HandoffMessage{}, err
	}
	if len(fds) != 0 {
		CloseHandoffFDs(fds)
		return HandoffMessage{}, fmt.Errorf("handoff control message unexpectedly carried descriptors")
	}
	return message, nil
}

func (c *HandoffConnection) ReadManifest() (HandoffManifest, error) {
	data, fds, err := c.readPayload()
	if err != nil {
		return HandoffManifest{}, err
	}
	if len(fds) != 0 {
		CloseHandoffFDs(fds)
		return HandoffManifest{}, fmt.Errorf("handoff manifest unexpectedly carried descriptors")
	}
	var manifest HandoffManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return HandoffManifest{}, err
	}
	return manifest, nil
}

func (c *HandoffConnection) WriteReplay(terminalID string, replay []byte) error {
	if terminalID == "" {
		return errors.New("handoff replay requires a terminal id")
	}
	if len(replay) > MaxHandoffReplayBytesPerTerminal {
		return fmt.Errorf(
			"handoff replay for %s is %d bytes, limit is %d",
			terminalID,
			len(replay),
			MaxHandoffReplayBytesPerTerminal,
		)
	}
	for offset := 0; offset < len(replay); offset += MaxHandoffReplayChunkBytes {
		end := min(offset+MaxHandoffReplayChunkBytes, len(replay))
		if err := c.WriteJSON(HandoffMessage{
			Type:       "replay",
			TerminalID: terminalID,
			Offset:     offset,
			Replay:     replay[offset:end],
		}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *HandoffConnection) ReadReplay(terminalID string, size int) ([]byte, error) {
	if terminalID == "" {
		return nil, errors.New("handoff replay requires a terminal id")
	}
	if size < 0 || size > MaxHandoffReplayBytesPerTerminal {
		return nil, fmt.Errorf(
			"handoff replay size for %s is %d, limit is %d",
			terminalID,
			size,
			MaxHandoffReplayBytesPerTerminal,
		)
	}
	replay := make([]byte, 0, size)
	for len(replay) < size {
		message, fds, err := c.ReadMessage()
		if err != nil {
			return nil, err
		}
		if len(fds) != 0 {
			CloseHandoffFDs(fds)
			return nil, errors.New("handoff replay unexpectedly carried descriptors")
		}
		if message.Type != "replay" ||
			message.TerminalID != terminalID ||
			message.Offset != len(replay) ||
			len(message.Replay) == 0 ||
			len(message.Replay) > MaxHandoffReplayChunkBytes ||
			len(replay)+len(message.Replay) > size {
			return nil, fmt.Errorf("invalid handoff replay chunk for %s at offset %d", terminalID, len(replay))
		}
		replay = append(replay, message.Replay...)
	}
	return replay, nil
}

func (c *HandoffConnection) readPayload() ([]byte, []int, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	if c.Framing == HandoffFramingLegacyPacket {
		data := make([]byte, MaxMessageSize)
		oob := make([]byte, unix.CmsgSpace(MaxHandoffFDsPerFrame*4))
		n, oobn, flags, _, err := c.conn.ReadMsgUnix(data, oob)
		if err != nil {
			return nil, nil, err
		}
		fds, parseErr := parseHandoffFDs(oob[:oobn])
		if parseErr != nil {
			return nil, nil, parseErr
		}
		if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
			CloseHandoffFDs(fds)
			return nil, nil, fmt.Errorf("truncated legacy handoff packet")
		}
		return data[:n], fds, nil
	}

	header := make([]byte, 4)
	fds, err := c.readExact(header)
	if err != nil {
		return nil, nil, err
	}
	size := binary.BigEndian.Uint32(header)
	if size == 0 || size > MaxMessageSize {
		CloseHandoffFDs(fds)
		return nil, nil, fmt.Errorf("invalid handoff frame size %d", size)
	}
	data := make([]byte, int(size))
	bodyFDs, err := c.readExact(data)
	fds = append(fds, bodyFDs...)
	if err != nil {
		CloseHandoffFDs(fds)
		return nil, nil, err
	}
	return data, fds, nil
}

func (c *HandoffConnection) readExact(target []byte) ([]int, error) {
	var fds []int
	for offset := 0; offset < len(target); {
		oob := make([]byte, unix.CmsgSpace(MaxHandoffFDsPerFrame*4))
		n, oobn, flags, _, err := c.conn.ReadMsgUnix(target[offset:], oob)
		if err != nil {
			CloseHandoffFDs(fds)
			return nil, err
		}
		values, parseErr := parseHandoffFDs(oob[:oobn])
		if parseErr != nil {
			CloseHandoffFDs(fds)
			return nil, parseErr
		}
		if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
			CloseHandoffFDs(values)
			CloseHandoffFDs(fds)
			return nil, fmt.Errorf("truncated handoff stream frame")
		}
		fds = append(fds, values...)
		if n == 0 {
			CloseHandoffFDs(fds)
			return nil, io.ErrUnexpectedEOF
		}
		offset += n
	}
	return fds, nil
}

func parseHandoffFDs(oob []byte) ([]int, error) {
	if len(oob) == 0 {
		return nil, nil
	}
	messages, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, err
	}
	var fds []int
	for _, message := range messages {
		values, err := unix.ParseUnixRights(&message)
		if err != nil {
			CloseHandoffFDs(fds)
			return nil, err
		}
		fds = append(fds, values...)
	}
	return fds, nil
}

// CloseHandoffFDs closes every descriptor in fds, ignoring errors. Callers use
// it to reject frames whose descriptors must not leak.
func CloseHandoffFDs(fds []int) {
	for _, fd := range fds {
		_ = unix.Close(fd)
	}
}
