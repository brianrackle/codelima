package daemonclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

const maxResponseSize = 16 << 20

type Options struct {
	Home    string
	Version string
	// Protocol overrides the current protocol only for daemon lifecycle
	// compatibility during update and startup recovery. Ordinary callers must
	// leave it at zero.
	Protocol  int
	WantInput bool
	Events    bool
	Timeout   time.Duration
}

type Client struct {
	conn      net.Conn
	encoder   *json.Encoder
	reader    *bufio.Reader
	timeout   time.Duration
	mu        sync.Mutex
	nextID    atomic.Uint64
	Hello     daemon.HelloResult
	eventMode bool
}

func Dial(ctx context.Context, options Options) (*Client, error) {
	if options.Timeout <= 0 {
		options.Timeout = 5 * time.Second
	}
	protocol := options.Protocol
	if protocol == 0 {
		protocol = daemon.ProtocolVersion
	}
	paths := daemon.HomePaths(options.Home)
	path := paths.Socket
	if options.Events {
		path = paths.Client
	}
	dialer := net.Dialer{Timeout: options.Timeout}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("daemon not running (codelima daemon start): %w", err)
	}
	client := &Client{conn: conn, encoder: json.NewEncoder(conn), reader: bufio.NewReaderSize(conn, 64*1024), timeout: options.Timeout, eventMode: options.Events}
	if err := client.callLocked(ctx, "hello", daemon.HelloParams{Version: options.Version, Protocol: protocol, Capabilities: []string{"terminal.snapshot", "events"}, WantInput: options.WantInput}, &client.Hello); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return client, nil
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("daemon client is closed")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.callLocked(ctx, method, params, result)
}

func (c *Client) callLocked(ctx context.Context, method string, params any, result any) error {
	id := c.nextID.Add(1)
	var raw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = data
	}
	deadline := time.Now().Add(c.timeout)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = c.conn.SetDeadline(deadline)
	if err := c.encoder.Encode(daemon.Request{ID: id, Method: method, Params: raw}); err != nil {
		return err
	}
	line, err := readBoundedLine(c.reader, maxResponseSize)
	if err != nil {
		return err
	}
	var response daemon.Response
	if err := json.Unmarshal(line, &response); err != nil {
		return fmt.Errorf("decode daemon response: %w", err)
	}
	if response.ID != id {
		return fmt.Errorf("daemon response id %d does not match request %d", response.ID, id)
	}
	return daemon.DecodeResult(response, result)
}

func (c *Client) Subscribe(ctx context.Context, topics []string) error {
	return c.Call(ctx, "events.subscribe", map[string]any{"topics": topics}, nil)
}

func (c *Client) NextEvent(ctx context.Context) (daemon.Event, error) {
	if !c.eventMode {
		return daemon.Event{}, fmt.Errorf("client was not opened for events")
	}
	deadline := time.Now().Add(c.timeout)
	if value, ok := ctx.Deadline(); ok {
		deadline = value
	}
	_ = c.conn.SetReadDeadline(deadline)
	line, err := readBoundedLine(c.reader, maxResponseSize)
	if err != nil {
		return daemon.Event{}, err
	}
	var event daemon.Event
	if err := json.Unmarshal(line, &event); err != nil {
		return daemon.Event{}, err
	}
	return event, nil
}

func Ping(ctx context.Context, home, version string) (daemon.Status, error) {
	client, err := Dial(ctx, Options{Home: home, Version: version})
	if err != nil {
		return daemon.Status{}, err
	}
	defer func() { _ = client.Close() }()
	var status daemon.Status
	if err := client.Call(ctx, "daemon.ping", nil, &status); err != nil {
		return daemon.Status{}, err
	}
	return status, nil
}

// readBoundedLine reads one newline-terminated frame without buffering more
// than limit bytes: ReadBytes would slurp an arbitrarily large line into
// memory before any size check could run.
func readBoundedLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > limit {
			return nil, fmt.Errorf("daemon frame exceeds %d bytes", limit)
		}
		if err == nil {
			return line, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
}
