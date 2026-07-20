package codelima

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/brianrackle/test_lima/internal/codelima/daemon"
	"golang.org/x/sys/unix"
)

const maxHandoffFDsPerFrame = 64

type handoffFraming uint8

const (
	handoffFramingLegacyPacket handoffFraming = iota
	handoffFramingLengthPrefixed
)

type handoffConnection struct {
	conn    *net.UnixConn
	framing handoffFraming
}

func newHandoffConnection(conn *net.UnixConn, framing handoffFraming) *handoffConnection {
	return &handoffConnection{conn: conn, framing: framing}
}

func (c *handoffConnection) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// listenHandoff uses SOCK_STREAM because Darwin does not implement Unix
// SOCK_SEQPACKET. SCM_RIGHTS works on Unix streams on both Darwin and Linux;
// explicit length framing supplies the message boundaries streams lack.
func listenHandoff(path string) (*net.UnixListener, error) {
	if os.Getenv("CODELIMA_HANDOFF_FORCE_UNSUPPORTED_TRANSPORT") == "1" {
		return nil, fmt.Errorf("listen unixpacket %s: socket: protocol not supported", path)
	}
	return net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
}

// dialHandoff prefers the portable framed stream. The unixpacket fallback is
// importer-only compatibility with the immediately previous Linux daemon,
// whose listener and packet framing are already fixed in the running process.
func dialHandoff(path string) (*handoffConnection, error) {
	conn, streamErr := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if streamErr == nil {
		return newHandoffConnection(conn, handoffFramingLengthPrefixed), nil
	}
	legacy, legacyErr := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: path, Net: "unixpacket"})
	if legacyErr == nil {
		return newHandoffConnection(legacy, handoffFramingLegacyPacket), nil
	}
	return nil, fmt.Errorf("dial handoff stream: %w (legacy unixpacket: %v)", streamErr, legacyErr)
}

func (c *handoffConnection) writeJSON(value any, fds []int) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > daemon.MaxMessageSize {
		return fmt.Errorf("handoff message size %d is outside 1..%d", len(data), daemon.MaxMessageSize)
	}
	payload := data
	if c.framing == handoffFramingLengthPrefixed {
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
	if c.framing == handoffFramingLegacyPacket {
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

func (c *handoffConnection) readMessage() (daemon.HandoffMessage, []int, error) {
	data, fds, err := c.readPayload()
	if err != nil {
		return daemon.HandoffMessage{}, nil, err
	}
	var message daemon.HandoffMessage
	if err := json.Unmarshal(data, &message); err != nil {
		closeHandoffFDs(fds)
		return daemon.HandoffMessage{}, nil, err
	}
	return message, fds, nil
}

func (c *handoffConnection) readControlMessage() (daemon.HandoffMessage, error) {
	message, fds, err := c.readMessage()
	if err != nil {
		return daemon.HandoffMessage{}, err
	}
	if len(fds) != 0 {
		closeHandoffFDs(fds)
		return daemon.HandoffMessage{}, fmt.Errorf("handoff control message unexpectedly carried descriptors")
	}
	return message, nil
}

func (c *handoffConnection) readManifest() (daemon.HandoffManifest, error) {
	data, fds, err := c.readPayload()
	if err != nil {
		return daemon.HandoffManifest{}, err
	}
	if len(fds) != 0 {
		closeHandoffFDs(fds)
		return daemon.HandoffManifest{}, fmt.Errorf("handoff manifest unexpectedly carried descriptors")
	}
	var manifest daemon.HandoffManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return daemon.HandoffManifest{}, err
	}
	return manifest, nil
}

func (c *handoffConnection) readPayload() ([]byte, []int, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	if c.framing == handoffFramingLegacyPacket {
		data := make([]byte, daemon.MaxMessageSize)
		oob := make([]byte, unix.CmsgSpace(maxHandoffFDsPerFrame*4))
		n, oobn, flags, _, err := c.conn.ReadMsgUnix(data, oob)
		if err != nil {
			return nil, nil, err
		}
		fds, parseErr := parseHandoffFDs(oob[:oobn])
		if parseErr != nil {
			return nil, nil, parseErr
		}
		if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
			closeHandoffFDs(fds)
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
	if size == 0 || size > daemon.MaxMessageSize {
		closeHandoffFDs(fds)
		return nil, nil, fmt.Errorf("invalid handoff frame size %d", size)
	}
	data := make([]byte, int(size))
	bodyFDs, err := c.readExact(data)
	fds = append(fds, bodyFDs...)
	if err != nil {
		closeHandoffFDs(fds)
		return nil, nil, err
	}
	return data, fds, nil
}

func (c *handoffConnection) readExact(target []byte) ([]int, error) {
	var fds []int
	for offset := 0; offset < len(target); {
		oob := make([]byte, unix.CmsgSpace(maxHandoffFDsPerFrame*4))
		n, oobn, flags, _, err := c.conn.ReadMsgUnix(target[offset:], oob)
		if err != nil {
			closeHandoffFDs(fds)
			return nil, err
		}
		values, parseErr := parseHandoffFDs(oob[:oobn])
		if parseErr != nil {
			closeHandoffFDs(fds)
			return nil, parseErr
		}
		if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
			closeHandoffFDs(values)
			closeHandoffFDs(fds)
			return nil, fmt.Errorf("truncated handoff stream frame")
		}
		fds = append(fds, values...)
		if n == 0 {
			closeHandoffFDs(fds)
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
			closeHandoffFDs(fds)
			return nil, err
		}
		fds = append(fds, values...)
	}
	return fds, nil
}

func closeHandoffFDs(fds []int) {
	for _, fd := range fds {
		_ = unix.Close(fd)
	}
}
