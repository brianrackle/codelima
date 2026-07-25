package daemon

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/brianrackle/codelima/internal/atomicfile"
)

type ClientContext struct {
	ID         string
	InputOwner bool
}

type Handler interface {
	Handle(context.Context, ClientContext, string, json.RawMessage) (any, error)
	Snapshot(context.Context) (any, error)
	TerminalCount() int
	Close() error
}

// SynchronizationHandler may provide a compact authoritative reconnect cut.
// Large immutable terminal cell grids are deliberately fetched per terminal
// after this cut so one reconnect frame stays bounded as tab count grows.
type SynchronizationHandler interface {
	SynchronizationSnapshot(context.Context) (any, error)
}

type Config struct {
	Home              string
	Version           string
	Handler           Handler
	Logger            *slog.Logger
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	RequestTimeout    time.Duration
	MaxInFlight       int
	MutationQueueSize int
	OutboundMaxFrames int
	OutboundMaxBytes  int
	HeartbeatInterval time.Duration
}

type Server struct {
	cfg      Config
	paths    Paths
	identity Identity
	lock     *os.File
	request  net.Listener
	events   net.Listener
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	mu       sync.Mutex
	clients  map[string]*clientConn
	input    inputLease
	revision uint64
	nextConn atomic.Uint64
	// baseCtx is derived from Run's context and cancelled when the server
	// stops; every connection derives its handler context from it so in-flight
	// handlers observe shutdown instead of running on a detached Background.
	baseCtx    context.Context
	baseCancel context.CancelFunc
	accepting  atomic.Bool
	replaced   atomic.Bool
}

type clientConn struct {
	id                   string
	clientInstanceID     string
	connectionID         uint64
	connectionGeneration uint64
	daemonEpoch          string
	conn                 net.Conn
	outbound             *outboundQueue
	subscribed           atomic.Bool
	cancel               context.CancelCauseFunc
	failure              connectionFailure
	closeOnce            sync.Once
	writerDone           chan struct{}

	lastReadUnixNano  atomic.Int64
	lastWriteUnixNano atomic.Int64
	inboundFrames     atomic.Uint64
	outboundFrames    atomic.Uint64
	inboundBytes      atomic.Uint64
	outboundBytes     atomic.Uint64
}

type inputLease struct {
	clientInstanceID string
	connectionID     uint64
}

func NewServer(cfg Config) *Server {
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 30 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 5 * time.Second
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.MaxInFlight <= 0 {
		cfg.MaxInFlight = 16
	}
	if cfg.MutationQueueSize <= 0 {
		cfg.MutationQueueSize = 64
	}
	if cfg.OutboundMaxFrames <= 0 {
		cfg.OutboundMaxFrames = 256
	}
	if cfg.OutboundMaxBytes <= 0 {
		cfg.OutboundMaxBytes = 64 << 20
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 2 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Server{cfg: cfg, paths: HomePaths(cfg.Home), stop: make(chan struct{}), clients: map[string]*clientConn{}}
}

func (s *Server) Run(ctx context.Context) error {
	if s.cfg.Handler == nil {
		return errors.New("daemon handler is required")
	}
	if err := s.prepare(); err != nil {
		return err
	}
	defer s.cleanup()
	s.baseCtx, s.baseCancel = context.WithCancel(ctx)
	defer s.baseCancel()
	s.accepting.Store(true)
	s.wg.Add(3)
	go s.acceptLoop(s.request, false)
	go s.acceptLoop(s.events, true)
	go s.heartbeatLoop(s.baseCtx)
	select {
	case <-ctx.Done():
	case <-s.stop:
	}
	s.accepting.Store(false)
	s.baseCancel()
	_ = s.request.Close()
	_ = s.events.Close()
	s.closeClients()
	s.wg.Wait()
	return s.cfg.Handler.Close()
}

func (s *Server) heartbeatLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		s.mu.Lock()
		event := Event{
			Event:         "daemon.heartbeat",
			DaemonEpoch:   s.identity.Token,
			StateSequence: s.revision,
		}
		clients := make([]*clientConn, 0, len(s.clients))
		for _, client := range s.clients {
			if client.subscribed.Load() {
				clients = append(clients, client)
			}
		}
		s.mu.Unlock()
		frame, err := marshalLine(event)
		if err != nil {
			continue
		}
		for _, client := range clients {
			if !client.outbound.TryPush(frame, outboundLow) {
				client.fail("daemon", "ready", CloseQueueFull, errors.New("daemon client outbound queue is full during heartbeat"))
			}
		}
	}
}

func (s *Server) Stop() { s.stopOnce.Do(func() { close(s.stop) }) }

// DisconnectClient deliberately invalidates one physical connection without
// changing daemon-owned terminal state. Diagnostics and deterministic fault
// tests use this to exercise client reconnection by connection ID.
func (s *Server) DisconnectClient(connectionID uint64, reason ConnectionCloseReason) bool {
	s.mu.Lock()
	var target *clientConn
	for _, client := range s.clients {
		if client.connectionID == connectionID {
			target = client
			break
		}
	}
	s.mu.Unlock()
	if target == nil {
		return false
	}
	target.fail("daemon", "ready", reason, errors.New("daemon connection deliberately disconnected"))
	return true
}

// stopAccepting closes the public listeners and removes their socket files so
// new dials fail immediately, without touching existing connections. Run's
// shutdown path tolerates the double close.
func (s *Server) stopAccepting() {
	s.accepting.Store(false)
	if s.request != nil {
		_ = s.request.Close()
	}
	if s.events != nil {
		_ = s.events.Close()
	}
	_ = os.Remove(s.paths.Socket)
	_ = os.Remove(s.paths.Client)
}

// PrepareReplacement releases only the public listeners and daemon lock. Live
// client connections and the handler stay alive until the importing daemon has
// bound the public surface and acknowledged commit.
func (s *Server) PrepareReplacement() error {
	if !s.replaced.CompareAndSwap(false, true) {
		return errors.New("daemon replacement already in progress")
	}
	s.accepting.Store(false)
	if s.request != nil {
		_ = s.request.Close()
	}
	if s.events != nil {
		_ = s.events.Close()
	}
	_ = os.Remove(s.paths.Socket)
	_ = os.Remove(s.paths.Client)
	if s.lock != nil {
		_ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
		_ = s.lock.Close()
		s.lock = nil
	}
	return nil
}

func (s *Server) ResumeAfterReplacement() error {
	lock, err := os.OpenFile(s.paths.Lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return err
	}
	oldUmask := syscall.Umask(0o177)
	request, err := net.Listen("unix", s.paths.Socket)
	if err == nil {
		s.events, err = net.Listen("unix", s.paths.Client)
	}
	syscall.Umask(oldUmask)
	if err != nil {
		if request != nil {
			_ = request.Close()
		}
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
		return err
	}
	s.request, s.lock = request, lock
	s.accepting.Store(true)
	s.replaced.Store(false)
	s.wg.Add(2)
	go s.acceptLoop(s.request, false)
	go s.acceptLoop(s.events, true)
	return nil
}

func (s *Server) Broadcast(name string, data any) {
	s.mu.Lock()
	s.revision++
	event := Event{Event: name, Data: data, DaemonEpoch: s.identity.Token, StateSequence: s.revision}
	clients := make([]*clientConn, 0, len(s.clients))
	for _, client := range s.clients {
		if client.subscribed.Load() {
			clients = append(clients, client)
		}
	}
	s.mu.Unlock()

	frame, err := marshalLine(event)
	if err != nil {
		s.cfg.Logger.Error("encode daemon event failed", "event", name, "error", err)
		return
	}
	for _, client := range clients {
		if !client.outbound.TryPush(frame, outboundLow) {
			client.fail("daemon", "ready", CloseQueueFull, errors.New("daemon client outbound queue is full"))
		}
	}
}

func (s *Server) prepare() (err error) {
	// Unwind every acquired resource when a later step fails: without this a
	// listen or chmod failure leaked the held flock and open listeners.
	defer func() {
		if err == nil {
			return
		}
		if s.events != nil {
			_ = s.events.Close()
			s.events = nil
		}
		if s.request != nil {
			_ = s.request.Close()
			s.request = nil
		}
		if s.lock != nil {
			_ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
			_ = s.lock.Close()
			s.lock = nil
		}
	}()

	for _, dir := range []string{s.paths.Dir, filepath.Dir(s.paths.Lock)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	lock, err := os.OpenFile(s.paths.Lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return Error("PreconditionFailed", "daemon already running", CodePreconditionFailed, nil)
		}
		return err
	}
	s.lock = lock
	for _, path := range []string{s.paths.Socket, s.paths.Client} {
		_ = os.Remove(path)
	}
	oldUmask := syscall.Umask(0o177)
	s.request, err = net.Listen("unix", s.paths.Socket)
	if err != nil {
		syscall.Umask(oldUmask)
		return err
	}
	s.events, err = net.Listen("unix", s.paths.Client)
	syscall.Umask(oldUmask)
	if err != nil {
		return err
	}
	for _, path := range []string{s.paths.Socket, s.paths.Client} {
		if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
			return err
		}
	}
	s.identity = Identity{Token: randomToken(), Version: s.cfg.Version, Protocol: ProtocolVersion, PID: os.Getpid(), StartedAt: time.Now().UTC()}
	if err := writeJSONAtomic(s.paths.Identity, s.identity, 0o600); err != nil {
		return err
	}
	return writeAtomic(s.paths.PID, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
}

func (s *Server) acceptLoop(listener net.Listener, eventSocket bool) {
	defer s.wg.Done()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if !s.accepting.Load() {
				return
			}
			// Back off on persistent accept errors (e.g. EMFILE) instead of
			// spinning hot.
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if err := RequireSameUserPeer(conn); err != nil {
			_ = conn.Close()
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serveConn(conn, eventSocket)
		}()
	}
}

func (s *Server) serveConn(conn net.Conn, eventSocket bool) {
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), MaxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout))
	if !scanner.Scan() {
		_ = conn.Close()
		return
	}
	request, err := decodeRequest(scanner.Bytes())
	if err != nil || request.Method != "hello" {
		s.writeImmediate(conn, Response{ID: request.ID, Error: &RPCError{Category: "InvalidArgument", Message: "hello must be the first request", Code: CodeInvalidArgument}})
		_ = conn.Close()
		return
	}
	var hello HelloParams
	if err := json.Unmarshal(request.Params, &hello); err != nil {
		s.writeImmediate(conn, Response{ID: request.ID, Error: &RPCError{Category: "InvalidArgument", Message: "invalid hello params", Code: CodeInvalidArgument}})
		_ = conn.Close()
		return
	}
	if hello.Version != s.cfg.Version || hello.Protocol != ProtocolVersion {
		s.writeImmediate(conn, Response{ID: request.ID, Error: &RPCError{Category: "PreconditionFailed", Message: fmt.Sprintf("daemon version mismatch (daemon %s protocol %d, client %s protocol %d); restart the daemon", s.cfg.Version, ProtocolVersion, hello.Version, hello.Protocol), Code: CodePreconditionFailed}})
		_ = conn.Close()
		return
	}

	baseCtx := s.baseCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	connCtx, connCancel := context.WithCancelCause(baseCtx)
	connectionID := s.nextConn.Add(1)
	clientInstanceID := hello.ClientInstanceID
	if clientInstanceID == "" {
		clientInstanceID = randomToken()
	}
	client := &clientConn{
		id:                   strconv.FormatUint(connectionID, 10),
		clientInstanceID:     clientInstanceID,
		connectionID:         connectionID,
		connectionGeneration: hello.ConnectionGeneration,
		daemonEpoch:          s.identity.Token,
		conn:                 conn,
		outbound:             newOutboundQueue(s.cfg.OutboundMaxFrames, s.cfg.OutboundMaxBytes),
		cancel:               connCancel,
		writerDone:           make(chan struct{}),
	}
	client.noteRead(len(scanner.Bytes()))
	go client.writePump(connCtx, s.cfg.WriteTimeout)

	s.mu.Lock()
	if hello.WantInput && (s.input.clientInstanceID == "" || s.input.clientInstanceID == client.clientInstanceID) {
		s.input = inputLease{clientInstanceID: client.clientInstanceID, connectionID: client.connectionID}
	}
	s.clients[client.id] = client
	owner := s.input.clientInstanceID == client.clientInstanceID && s.input.connectionID == client.connectionID
	revision := s.revision
	s.mu.Unlock()
	defer func() {
		if client.failure.Cause() == nil {
			reason, cause := classifyReadClose(scanner.Err(), context.Cause(connCtx))
			client.fail("kernel/peer", "ready", reason, cause)
		}
		s.removeClient(client.id)
		<-client.writerDone
	}()
	s.writeResult(client, request.ID, HelloResult{
		Version:       s.cfg.Version,
		Protocol:      ProtocolVersion,
		ClientID:      client.clientInstanceID,
		ConnectionID:  connectionID,
		DaemonEpoch:   s.identity.Token,
		StateSequence: revision,
		InputOwner:    owner,
	})
	mutations := make(chan Request, s.cfg.MutationQueueSize)
	go s.runMutationLane(connCtx, client, mutations, eventSocket)
	querySlots := make(chan struct{}, s.cfg.MaxInFlight)

	for {
		// ReadTimeout bounds the unauthenticated hello and the time an event
		// client may occupy client.sock without subscribing. Authenticated RPC
		// clients are intentionally persistent: the TUI can sit idle for an
		// arbitrary amount of time before opening a terminal or changing a node.
		if eventSocket && !client.subscribed.Load() {
			_ = conn.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout))
		} else {
			_ = conn.SetReadDeadline(time.Time{})
		}
		if !scanner.Scan() {
			break
		}
		client.noteRead(len(scanner.Bytes()))
		request, err := decodeRequest(scanner.Bytes())
		if err != nil {
			client.fail("daemon", "ready", CloseProtocolError, err)
			break
		}
		if request.Method == "events.subscribe" {
			s.subscribe(connCtx, client, request, eventSocket)
			continue
		}
		if mutatingInputMethod(request.Method) {
			select {
			case mutations <- request:
			default:
				client.fail("daemon", "ready", CloseQueueFull, errors.New("daemon client mutation queue is full"))
			}
			continue
		}
		select {
		case querySlots <- struct{}{}:
			go func(request Request) {
				defer func() { <-querySlots }()
				s.dispatchRequest(connCtx, client, request, eventSocket)
			}(request)
		default:
			s.writeResponse(client, Response{ID: request.ID, Error: &RPCError{
				Category: "PreconditionFailed",
				Message:  "too many in-flight daemon requests",
				Code:     CodePreconditionFailed,
			}})
		}
	}
}

func (s *Server) runMutationLane(ctx context.Context, client *clientConn, requests <-chan Request, eventSocket bool) {
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-requests:
			s.dispatchRequest(ctx, client, request, eventSocket)
		}
	}
}

func (s *Server) dispatchRequest(ctx context.Context, client *clientConn, request Request, eventSocket bool) {
	requestCtx, requestCancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer requestCancel()
	result, callErr := s.handle(requestCtx, client, request, eventSocket)
	if callErr != nil {
		s.writeResponse(client, Response{ID: request.ID, Error: AsRPCError(callErr)})
		return
	}
	s.writeResult(client, request.ID, result)
}

func (s *Server) subscribe(ctx context.Context, client *clientConn, request Request, eventSocket bool) {
	if !eventSocket {
		s.writeResponse(client, Response{ID: request.ID, Error: AsRPCError(Error(
			"PreconditionFailed",
			"events.subscribe requires client.sock",
			CodePreconditionFailed,
			nil,
		))})
		return
	}

	// Capture a state snapshot across a stable event revision. The usual path
	// performs no external I/O and succeeds on the first attempt. If state is
	// changing continuously, the bounded fallback takes the server state lock
	// for the final capture so the sync response and subsequent event stream
	// still define one authoritative cut.
	for attempt := 0; ; attempt++ {
		s.mu.Lock()
		before := s.revision
		s.mu.Unlock()

		var (
			state any
			err   error
		)
		if attempt < 8 {
			state, err = s.synchronizationSnapshot(ctx)
		} else {
			s.mu.Lock()
			state, err = s.synchronizationSnapshot(ctx)
			if err == nil {
				s.enqueueSyncLocked(client, request.ID, state, s.revision)
			}
			s.mu.Unlock()
			if err != nil {
				s.writeResponse(client, Response{ID: request.ID, Error: AsRPCError(err)})
			}
			return
		}
		if err != nil {
			s.writeResponse(client, Response{ID: request.ID, Error: AsRPCError(err)})
			return
		}

		s.mu.Lock()
		if s.revision != before {
			s.mu.Unlock()
			continue
		}
		s.enqueueSyncLocked(client, request.ID, state, before)
		s.mu.Unlock()
		return
	}
}

func (s *Server) enqueueSyncLocked(client *clientConn, requestID uint64, state any, revision uint64) {
	data, err := json.Marshal(state)
	if err != nil {
		s.writeResponse(client, Response{ID: requestID, Error: AsRPCError(err)})
		return
	}
	result, err := json.Marshal(SyncSnapshot{
		ProtocolVersion: ProtocolVersion,
		DaemonEpoch:     s.identity.Token,
		StateSequence:   revision,
		State:           data,
	})
	if err != nil {
		s.writeResponse(client, Response{ID: requestID, Error: AsRPCError(err)})
		return
	}
	frame, err := marshalLine(Response{ID: requestID, Result: result})
	if err != nil {
		client.fail("daemon", "synchronizing", CloseWriteError, err)
		return
	}
	if !client.outbound.TryPush(frame, outboundHigh) {
		client.fail("daemon", "synchronizing", CloseQueueFull, errors.New("daemon client sync queue is full"))
		return
	}
	client.subscribed.Store(true)
}

func (s *Server) writeImmediate(conn net.Conn, response Response) {
	frame, err := marshalLine(response)
	if err != nil {
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(s.cfg.WriteTimeout))
	_ = writeAll(conn, frame)
}

func marshalLine(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if written > 0 {
			data = data[written:]
		}
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (c *clientConn) writePump(ctx context.Context, timeout time.Duration) {
	defer close(c.writerDone)
	for {
		frame, err := c.outbound.Pop(ctx)
		if err != nil {
			return
		}
		if err := c.conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
			c.fail("daemon", "ready", CloseWriteError, err)
			return
		}
		if err := writeAll(c.conn, frame); err != nil {
			reason := CloseWriteError
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				reason = CloseWriteTimeout
			}
			c.fail("daemon", "ready", reason, err)
			return
		}
		c.lastWriteUnixNano.Store(time.Now().UnixNano())
		c.outboundFrames.Add(1)
		c.outboundBytes.Add(uint64(len(frame)))
	}
}

func (c *clientConn) noteRead(size int) {
	c.lastReadUnixNano.Store(time.Now().UnixNano())
	c.inboundFrames.Add(1)
	c.inboundBytes.Add(uint64(size))
}

func (c *clientConn) fail(initiator, phase string, reason ConnectionCloseReason, cause error) {
	depth, bytes, oldest := c.outbound.Stats()
	record := ConnectionCloseRecord{
		ConnectionID:     c.connectionID,
		ClientInstanceID: c.clientInstanceID,
		DaemonEpoch:      c.daemonEpoch,
		Initiator:        initiator,
		Phase:            phase,
		Reason:           reason,
		OutboundDepth:    depth,
		OutboundBytesNow: bytes,
		OldestOutbound:   oldest,
		InboundFrames:    c.inboundFrames.Load(),
		OutboundFrames:   c.outboundFrames.Load(),
		InboundBytes:     c.inboundBytes.Load(),
		OutboundBytes:    c.outboundBytes.Load(),
	}
	if value := c.lastReadUnixNano.Load(); value > 0 {
		record.LastReadAt = time.Unix(0, value)
	}
	if value := c.lastWriteUnixNano.Load(); value > 0 {
		record.LastWriteAt = time.Unix(0, value)
	}
	if cause != nil {
		record.Underlying = cause.Error()
	}
	if !c.failure.Fail(record, cause) {
		return
	}
	c.closeOnce.Do(func() {
		c.outbound.Close()
		c.cancel(cause)
		_ = c.conn.Close()
	})
}

func classifyReadClose(readErr, contextErr error) (ConnectionCloseReason, error) {
	if readErr == nil {
		if contextErr != nil {
			return CloseContextCancelled, contextErr
		}
		return ClosePeerEOF, io.EOF
	}
	var netErr net.Error
	if errors.As(readErr, &netErr) && netErr.Timeout() {
		return CloseReadTimeout, readErr
	}
	return CloseReadError, readErr
}

func (s *Server) handle(ctx context.Context, client *clientConn, request Request, eventSocket bool) (any, error) {
	s.mu.Lock()
	owner := s.input.clientInstanceID == client.clientInstanceID && s.input.connectionID == client.connectionID
	s.mu.Unlock()
	switch request.Method {
	case "daemon.ping", "daemon.status":
		return s.status(), nil
	case "daemon.stop":
		s.Broadcast("daemon.shutdown", nil)
		// Stop accepting BEFORE responding so a client that saw this response
		// can never ping a daemon that still looks alive: the response rides
		// the already-accepted connection, so closing the listeners here is
		// safe, and the timed Stop only delays teardown of live connections
		// until the reply has flushed.
		s.stopAccepting()
		time.AfterFunc(10*time.Millisecond, s.Stop)
		return map[string]bool{"stopping": true}, nil
	case "daemon.snapshot":
		return s.cfg.Handler.Snapshot(ctx)
	case "events.subscribe":
		if !eventSocket {
			return nil, Error("PreconditionFailed", "events.subscribe requires client.sock", CodePreconditionFailed, nil)
		}
		return map[string]bool{"subscribed": true}, nil
	case "daemon.sync":
		state, err := s.synchronizationSnapshot(ctx)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(state)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		snapshot := SyncSnapshot{ProtocolVersion: ProtocolVersion, DaemonEpoch: s.identity.Token, StateSequence: s.revision, State: data}
		s.mu.Unlock()
		return snapshot, nil
	case "input.takeover":
		s.mu.Lock()
		previous := s.input
		s.input = inputLease{clientInstanceID: client.clientInstanceID, connectionID: client.connectionID}
		s.mu.Unlock()
		if previous.clientInstanceID != "" && previous.clientInstanceID != client.clientInstanceID {
			s.Broadcast("input.revoked", map[string]string{"client_id": previous.clientInstanceID})
		}
		return map[string]bool{"input_owner": true}, nil
	}
	if mutatingInputMethod(request.Method) && !owner {
		return nil, Error("PreconditionFailed", "client is observe-only; request input.takeover first", 5, map[string]any{"client_id": client.id})
	}
	return s.cfg.Handler.Handle(ctx, ClientContext{ID: client.id, InputOwner: owner}, request.Method, request.Params)
}

func (s *Server) synchronizationSnapshot(ctx context.Context) (any, error) {
	if handler, ok := s.cfg.Handler.(SynchronizationHandler); ok {
		return handler.SynchronizationSnapshot(ctx)
	}
	return s.cfg.Handler.Snapshot(ctx)
}

func mutatingInputMethod(method string) bool {
	switch method {
	case "daemon.update", "terminal.send_text", "terminal.send_keys", "terminal.send_input", "terminal.send_event", "terminal.resize", "terminal.focus", "terminal.scroll", "terminal.open", "terminal.close", "terminal.move", "input.takeover":
		return true
	default:
		return false
	}
}

func (s *Server) status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{
		Running:       true,
		PID:           s.identity.PID,
		Version:       s.identity.Version,
		Protocol:      ProtocolVersion,
		Identity:      s.identity.Token,
		StartedAt:     s.identity.StartedAt,
		TerminalCount: s.cfg.Handler.TerminalCount(),
		InputOwner:    s.input.clientInstanceID,
		DaemonEpoch:   s.identity.Token,
		StateSequence: s.revision,
	}
}

func decodeRequest(data []byte) (Request, error) {
	// serveConn's scanner already caps lines at MaxMessageSize (dropping the
	// connection on overflow); this guard is defense-in-depth for direct
	// callers.
	if len(data) > MaxMessageSize {
		return Request{}, Error("InvalidArgument", "daemon request exceeds 1 MiB", CodeInvalidArgument, nil)
	}
	var request Request
	if err := json.Unmarshal(data, &request); err != nil {
		return Request{}, Error("InvalidArgument", "invalid daemon request", CodeInvalidArgument, nil)
	}
	if request.Method == "" {
		return Request{}, Error("InvalidArgument", "daemon method is required", CodeInvalidArgument, nil)
	}
	return request, nil
}

func (s *Server) writeResult(client *clientConn, id uint64, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		s.writeResponse(client, Response{ID: id, Error: &RPCError{Category: "Internal", Message: err.Error(), Code: CodeInternalFailure}})
		return
	}
	s.writeResponse(client, Response{ID: id, Result: data})
}

func (s *Server) writeResponse(client *clientConn, response Response) {
	frame, err := marshalLine(response)
	if err != nil {
		client.fail("daemon", "ready", CloseWriteError, err)
		return
	}
	if !client.outbound.TryPush(frame, outboundHigh) {
		client.fail("daemon", "ready", CloseQueueFull, errors.New("daemon client response queue is full"))
	}
}

func (s *Server) removeClient(id string) {
	s.mu.Lock()
	client := s.clients[id]
	delete(s.clients, id)
	if client != nil && s.input.connectionID == client.connectionID {
		s.input = inputLease{}
	}
	s.mu.Unlock()
	if client != nil {
		record := client.failure.Record()
		s.cfg.Logger.LogAttrs(
			context.Background(),
			slog.LevelInfo,
			"daemon connection closed",
			slog.Uint64("connection_id", record.ConnectionID),
			slog.String("client_instance_id", record.ClientInstanceID),
			slog.String("daemon_epoch", record.DaemonEpoch),
			slog.String("initiator", record.Initiator),
			slog.String("phase", record.Phase),
			slog.String("reason", string(record.Reason)),
			slog.String("underlying", record.Underlying),
			slog.Int("outbound_queue_depth", record.OutboundDepth),
			slog.Int("outbound_queue_bytes", record.OutboundBytesNow),
		)
	}
}

func (s *Server) closeClients() {
	s.mu.Lock()
	clients := make([]*clientConn, 0, len(s.clients))
	for _, client := range s.clients {
		clients = append(clients, client)
	}
	s.mu.Unlock()
	for _, client := range clients {
		client.fail("daemon", "shutdown", CloseDaemonShutdown, errors.New("daemon shutting down"))
	}
}

func (s *Server) cleanup() {
	if !s.replaced.Load() {
		for _, path := range []string{s.paths.Socket, s.paths.Client, s.paths.PID, s.paths.Identity} {
			_ = os.Remove(path)
		}
	}
	if s.lock != nil {
		_ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
		_ = s.lock.Close()
	}
}

func randomToken() string {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(data)
}

func writeJSONAtomic(path string, value any, mode os.FileMode) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, append(data, '\n'), mode)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	return atomicfile.WriteFile(path, data, mode)
}
