package daemonclient

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

const maxResponseSize = 64 << 20

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
	// ClientInstanceID identifies one logical client process across physical
	// reconnects. It is generated when omitted.
	ClientInstanceID string
}

type Client struct {
	conn             net.Conn
	encoder          *json.Encoder
	reader           *bufio.Reader
	timeout          time.Duration
	mu               sync.Mutex
	writeMu          sync.Mutex
	pendingMu        sync.Mutex
	pending          map[uint64]pendingCall
	failureMu        sync.Mutex
	failureSet       bool
	failureGen       uint64
	lastClose        daemon.ConnectionCloseRecord
	helloMu          sync.RWMutex
	nextID           atomic.Uint64
	Hello            daemon.HelloResult
	eventMode        bool
	options          Options
	closed           bool
	generation       uint64
	activeGeneration atomic.Uint64
	lastRead         atomic.Int64
	lastWrite        atomic.Int64
	readFrames       atomic.Uint64
	writeFrames      atomic.Uint64
	readBytes        atomic.Uint64
	writeBytes       atomic.Uint64
}

type pendingCall struct {
	generation uint64
	method     string
	result     chan callResult
}

type callResult struct {
	response daemon.Response
	err      error
}

func Dial(ctx context.Context, options Options) (*Client, error) {
	if options.Timeout <= 0 {
		options.Timeout = 5 * time.Second
	}
	if options.ClientInstanceID == "" {
		options.ClientInstanceID = newClientInstanceID()
	}
	client := &Client{
		timeout: options.Timeout, eventMode: options.Events, options: options,
		pending: make(map[uint64]pendingCall),
	}
	client.mu.Lock()
	err := client.connectLocked(ctx)
	client.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	c.closed = true
	c.recordClose("client", "shutdown", daemon.CloseAdministrative, errors.New("daemon client closed"))
	err := c.invalidateLocked()
	c.mu.Unlock()
	c.failPending(0, errors.New("daemon client is closed"))
	return err
}

func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	if c == nil {
		return fmt.Errorf("daemon client is closed")
	}
	if c.eventMode {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.closed {
			return fmt.Errorf("daemon client is closed")
		}
		if c.conn == nil {
			if err := c.connectLocked(ctx); err != nil {
				return &DeliveryError{Outcome: DeliveryNotSent, Err: err}
			}
		}
		return c.callLocked(ctx, method, params, result)
	}

	return c.callConcurrent(ctx, method, params, result)
}

func (c *Client) callConcurrent(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("daemon client is closed")
	}
	if c.conn == nil {
		if err := c.connectLocked(ctx); err != nil {
			c.mu.Unlock()
			return &DeliveryError{Outcome: DeliveryNotSent, Err: err}
		}
	}
	conn := c.conn
	encoder := c.encoder
	generation := c.generation
	c.mu.Unlock()

	id := c.nextID.Add(1)
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	reply := make(chan callResult, 1)
	c.pendingMu.Lock()
	c.pending[id] = pendingCall{generation: generation, method: method, result: reply}
	c.pendingMu.Unlock()

	deadline := time.Now().Add(c.timeout)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	c.writeMu.Lock()
	c.mu.Lock()
	connected := !c.closed && c.conn == conn && c.generation == generation
	c.mu.Unlock()
	if !connected {
		c.writeMu.Unlock()
		c.removePending(id)
		return &DeliveryError{Outcome: DeliveryNotSent, Err: errors.New("daemon connection changed before request write")}
	}
	_ = conn.SetWriteDeadline(deadline)
	writeErr := encoder.Encode(daemon.Request{ID: id, Method: method, Params: raw})
	c.writeMu.Unlock()
	if writeErr != nil {
		c.recordClose("client", "ready", daemon.CloseWriteError, writeErr)
		c.invalidateConnection(conn, generation, writeErr)
		outcome := DeliveryDisconnected
		if mutatingMethod(method) {
			outcome = DeliveryOutcomeUnknown
		}
		return &DeliveryError{Outcome: outcome, Err: writeErr}
	}
	c.lastWrite.Store(time.Now().UnixNano())
	c.writeFrames.Add(1)
	c.writeBytes.Add(uint64(len(method) + len(raw)))

	wait := c.timeout
	if until, ok := ctx.Deadline(); ok {
		if remaining := time.Until(until); remaining < wait {
			wait = remaining
		}
	}
	if wait <= 0 {
		wait = time.Nanosecond
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case call := <-reply:
		if call.err != nil {
			outcome := DeliveryDisconnected
			if mutatingMethod(method) {
				outcome = DeliveryOutcomeUnknown
			}
			return &DeliveryError{Outcome: outcome, Err: call.err}
		}
		return daemon.DecodeResult(call.response, result)
	case <-ctx.Done():
		c.removePending(id)
		outcome := DeliveryDisconnected
		if mutatingMethod(method) {
			outcome = DeliveryOutcomeUnknown
		}
		return &DeliveryError{Outcome: outcome, Err: ctx.Err()}
	case <-timer.C:
		c.removePending(id)
		outcome := DeliveryDisconnected
		if mutatingMethod(method) {
			outcome = DeliveryOutcomeUnknown
		}
		return &DeliveryError{Outcome: outcome, Err: fmt.Errorf("daemon request %s exceeded %s", method, c.timeout)}
	}
}

func (c *Client) callLocked(ctx context.Context, method string, params any, result any) error {
	id := c.nextID.Add(1)
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(c.timeout)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = c.conn.SetDeadline(deadline)
	if err := c.encoder.Encode(daemon.Request{ID: id, Method: method, Params: raw}); err != nil {
		c.recordClose("client", "ready", daemon.CloseWriteError, err)
		_ = c.invalidateLocked()
		return &DeliveryError{Outcome: DeliveryOutcomeUnknown, Err: err}
	}
	c.lastWrite.Store(time.Now().UnixNano())
	c.writeFrames.Add(1)
	c.writeBytes.Add(uint64(len(method) + len(raw)))
	line, err := readBoundedLine(c.reader, maxResponseSize)
	if err != nil {
		c.recordClose("kernel/peer", "ready", classifyClientReadReason(err), err)
		_ = c.invalidateLocked()
		outcome := DeliveryDisconnected
		if mutatingMethod(method) {
			outcome = DeliveryOutcomeUnknown
		}
		return &DeliveryError{Outcome: outcome, Err: err}
	}
	c.noteRead(len(line))
	var response daemon.Response
	if err := json.Unmarshal(line, &response); err != nil {
		decodeErr := fmt.Errorf("decode daemon response: %w", err)
		c.recordClose("client", "handshake", daemon.CloseProtocolError, decodeErr)
		_ = c.invalidateLocked()
		return decodeErr
	}
	if response.ID != id {
		return fmt.Errorf("daemon response id %d does not match request %d", response.ID, id)
	}
	return daemon.DecodeResult(response, result)
}

func (c *Client) Subscribe(ctx context.Context, topics []string) error {
	_, err := c.SubscribeSync(ctx, topics)
	return err
}

func (c *Client) SubscribeSync(ctx context.Context, topics []string) (daemon.SyncSnapshot, error) {
	var snapshot daemon.SyncSnapshot
	err := c.Call(ctx, "events.subscribe", map[string]any{"topics": topics}, &snapshot)
	return snapshot, err
}

func (c *Client) NextEvent(ctx context.Context) (daemon.Event, error) {
	if !c.eventMode {
		return daemon.Event{}, fmt.Errorf("client was not opened for events")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.conn == nil {
		return daemon.Event{}, fmt.Errorf("daemon event client is disconnected")
	}
	deadline := time.Now().Add(c.timeout)
	if value, ok := ctx.Deadline(); ok {
		deadline = value
	}
	_ = c.conn.SetReadDeadline(deadline)
	line, err := readBoundedLine(c.reader, maxResponseSize)
	if err != nil {
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			c.recordClose("kernel/peer", "ready", classifyClientReadReason(err), err)
			_ = c.invalidateLocked()
		}
		return daemon.Event{}, err
	}
	c.noteRead(len(line))
	var event daemon.Event
	if err := json.Unmarshal(line, &event); err != nil {
		decodeErr := fmt.Errorf("decode daemon event: %w", err)
		c.recordClose("client", "ready", daemon.CloseProtocolError, decodeErr)
		_ = c.invalidateLocked()
		return daemon.Event{}, decodeErr
	}
	return event, nil
}

func (c *Client) Reconnect(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("daemon client is closed")
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("daemon client is closed")
	}
	oldGeneration := c.generation
	c.recordClose("client", "ready", daemon.CloseAdministrative, errors.New("daemon client reconnect requested"))
	_ = c.invalidateLocked()
	c.failPending(oldGeneration, errors.New("daemon client reconnected"))
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("daemon client is closed")
	}
	return c.connectLocked(ctx)
}

func (c *Client) HelloSnapshot() daemon.HelloResult {
	if c == nil {
		return daemon.HelloResult{}
	}
	c.helloMu.RLock()
	defer c.helloMu.RUnlock()
	return c.Hello
}

func (c *Client) SetInputOwner(owner bool) {
	if c == nil {
		return
	}
	c.helloMu.Lock()
	c.Hello.InputOwner = owner
	c.helloMu.Unlock()
}

func (c *Client) connectLocked(ctx context.Context) error {
	protocol := c.options.Protocol
	if protocol == 0 {
		protocol = daemon.ProtocolVersion
	}
	paths := daemon.HomePaths(c.options.Home)
	path := paths.Socket
	if c.options.Events {
		path = paths.Client
	}
	dialer := net.Dialer{Timeout: c.timeout}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return fmt.Errorf("daemon not running (codelima daemon start): %w", err)
	}
	c.generation++
	c.activeGeneration.Store(c.generation)
	c.failureMu.Lock()
	c.failureSet = false
	c.failureGen = c.generation
	c.lastClose = daemon.ConnectionCloseRecord{}
	c.failureMu.Unlock()
	c.conn = conn
	c.encoder = json.NewEncoder(conn)
	c.reader = bufio.NewReaderSize(conn, 64*1024)
	previousHello := c.HelloSnapshot()
	var hello daemon.HelloResult
	err = c.callLocked(ctx, "hello", daemon.HelloParams{
		Version:              c.options.Version,
		Protocol:             protocol,
		Capabilities:         []string{"terminal.snapshot", "events", "session.sync"},
		WantInput:            c.options.WantInput,
		ClientInstanceID:     c.options.ClientInstanceID,
		ConnectionGeneration: c.generation,
		LastDaemonEpoch:      previousHello.DaemonEpoch,
		LastStateSequence:    previousHello.StateSequence,
	}, &hello)
	if err != nil {
		_ = c.invalidateLocked()
		return err
	}
	c.helloMu.Lock()
	c.Hello = hello
	c.helloMu.Unlock()
	if !c.eventMode {
		go c.readResponses(conn, c.reader, c.generation)
	}
	return nil
}

func (c *Client) invalidateLocked() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	c.encoder = nil
	c.reader = nil
	return err
}

func (c *Client) readResponses(conn net.Conn, reader *bufio.Reader, generation uint64) {
	for {
		line, err := readBoundedLine(reader, maxResponseSize)
		if err != nil {
			c.recordClose("kernel/peer", "ready", classifyClientReadReason(err), err)
			c.invalidateConnection(conn, generation, err)
			return
		}
		c.noteRead(len(line))
		var response daemon.Response
		if err := json.Unmarshal(line, &response); err != nil {
			decodeErr := fmt.Errorf("decode daemon response: %w", err)
			c.recordClose("client", "ready", daemon.CloseProtocolError, decodeErr)
			c.invalidateConnection(conn, generation, decodeErr)
			return
		}
		c.pendingMu.Lock()
		pending, ok := c.pending[response.ID]
		if ok && pending.generation == generation {
			delete(c.pending, response.ID)
		}
		c.pendingMu.Unlock()
		if ok && pending.generation == generation {
			pending.result <- callResult{response: response}
		}
	}
}

func (c *Client) noteRead(size int) {
	c.lastRead.Store(time.Now().UnixNano())
	c.readFrames.Add(1)
	if size > 0 {
		c.readBytes.Add(uint64(size))
	}
}

func (c *Client) recordClose(
	initiator, phase string,
	reason daemon.ConnectionCloseReason,
	cause error,
) {
	c.failureMu.Lock()
	defer c.failureMu.Unlock()
	generation := c.activeGeneration.Load()
	if c.failureSet && c.failureGen == generation {
		return
	}
	hello := c.HelloSnapshot()
	record := daemon.ConnectionCloseRecord{
		ConnectionID:     hello.ConnectionID,
		ClientInstanceID: c.options.ClientInstanceID,
		DaemonEpoch:      hello.DaemonEpoch,
		Initiator:        initiator,
		Phase:            phase,
		Reason:           reason,
		InboundFrames:    c.readFrames.Load(),
		OutboundFrames:   c.writeFrames.Load(),
		InboundBytes:     c.readBytes.Load(),
		OutboundBytes:    c.writeBytes.Load(),
	}
	if value := c.lastRead.Load(); value > 0 {
		record.LastReadAt = time.Unix(0, value)
	}
	if value := c.lastWrite.Load(); value > 0 {
		record.LastWriteAt = time.Unix(0, value)
	}
	if cause != nil {
		record.Underlying = cause.Error()
	}
	c.failureSet = true
	c.failureGen = generation
	c.lastClose = record
}

func (c *Client) LastCloseRecord() daemon.ConnectionCloseRecord {
	if c == nil {
		return daemon.ConnectionCloseRecord{}
	}
	c.failureMu.Lock()
	defer c.failureMu.Unlock()
	return c.lastClose
}

func classifyClientReadReason(err error) daemon.ConnectionCloseReason {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return daemon.CloseReadTimeout
	}
	if errors.Is(err, io.EOF) {
		return daemon.ClosePeerEOF
	}
	return daemon.CloseReadError
}

func (c *Client) invalidateConnection(conn net.Conn, generation uint64, cause error) {
	c.mu.Lock()
	if c.conn == conn && c.generation == generation {
		_ = c.invalidateLocked()
	}
	c.mu.Unlock()
	c.failPending(generation, cause)
}

func (c *Client) failPending(generation uint64, cause error) {
	var pending []pendingCall
	c.pendingMu.Lock()
	for id, call := range c.pending {
		if generation == 0 || call.generation == generation {
			delete(c.pending, id)
			pending = append(pending, call)
		}
	}
	c.pendingMu.Unlock()
	for _, call := range pending {
		call.result <- callResult{err: cause}
	}
}

func (c *Client) removePending(id uint64) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	data, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return data, nil
}

type DeliveryOutcome uint8

const (
	DeliveryNotSent DeliveryOutcome = iota
	DeliveryOutcomeUnknown
	DeliveryDisconnected
)

type DeliveryError struct {
	Outcome DeliveryOutcome
	Err     error
}

func (e *DeliveryError) Error() string {
	if e == nil || e.Err == nil {
		return "daemon connection failed"
	}
	return e.Err.Error()
}

func (e *DeliveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func mutatingMethod(method string) bool {
	switch method {
	case "daemon.update", "terminal.send_text", "terminal.send_keys", "terminal.send_input", "terminal.send_event", "terminal.resize", "terminal.focus", "terminal.scroll", "terminal.open", "terminal.close", "terminal.move", "input.takeover":
		return true
	default:
		return false
	}
}

func newClientInstanceID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("client-%d", time.Now().UnixNano())
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
