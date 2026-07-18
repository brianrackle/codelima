package daemon

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
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

type Config struct {
	Home        string
	Version     string
	Handler     Handler
	ReadTimeout time.Duration
}

type Server struct {
	cfg       Config
	paths     Paths
	identity  Identity
	lock      *os.File
	request   net.Listener
	events    net.Listener
	stop      chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
	mu        sync.Mutex
	clients   map[string]*clientConn
	input     string
	accepting atomic.Bool
	replaced  atomic.Bool
}

type clientConn struct {
	id         string
	conn       net.Conn
	encoder    *json.Encoder
	writeMu    sync.Mutex
	subscribed bool
}

func NewServer(cfg Config) *Server {
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 30 * time.Second
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
	s.accepting.Store(true)
	s.wg.Add(2)
	go s.acceptLoop(s.request, false)
	go s.acceptLoop(s.events, true)
	select {
	case <-ctx.Done():
	case <-s.stop:
	}
	s.accepting.Store(false)
	_ = s.request.Close()
	_ = s.events.Close()
	s.closeClients()
	s.wg.Wait()
	return s.cfg.Handler.Close()
}

func (s *Server) Stop() { s.stopOnce.Do(func() { close(s.stop) }) }

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
	clients := make([]*clientConn, 0, len(s.clients))
	for _, client := range s.clients {
		if client.subscribed {
			clients = append(clients, client)
		}
	}
	s.mu.Unlock()
	for _, client := range clients {
		client.writeMu.Lock()
		err := client.encoder.Encode(Event{Event: name, Data: data})
		client.writeMu.Unlock()
		if err != nil {
			_ = client.conn.Close()
		}
	}
}

func (s *Server) prepare() error {
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
			return Error("PreconditionFailed", "daemon already running", 5, nil)
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
		_ = s.request.Close()
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
	defer func() { _ = conn.Close() }()
	client := &clientConn{id: randomToken(), conn: conn, encoder: json.NewEncoder(conn)}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), MaxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout))
	if !scanner.Scan() {
		return
	}
	request, err := decodeRequest(scanner.Bytes())
	if err != nil || request.Method != "hello" {
		s.writeResponse(client, Response{ID: request.ID, Error: &RPCError{Category: "InvalidArgument", Message: "hello must be the first request", Code: 2}})
		return
	}
	var hello HelloParams
	if err := json.Unmarshal(request.Params, &hello); err != nil {
		s.writeResponse(client, Response{ID: request.ID, Error: &RPCError{Category: "InvalidArgument", Message: "invalid hello params", Code: 2}})
		return
	}
	if hello.Version != s.cfg.Version || hello.Protocol != ProtocolVersion {
		s.writeResponse(client, Response{ID: request.ID, Error: &RPCError{Category: "PreconditionFailed", Message: fmt.Sprintf("daemon version mismatch (daemon %s protocol %d, client %s protocol %d); restart the daemon", s.cfg.Version, ProtocolVersion, hello.Version, hello.Protocol), Code: 5}})
		return
	}
	s.mu.Lock()
	if hello.WantInput && s.input == "" {
		s.input = client.id
	}
	s.clients[client.id] = client
	owner := s.input == client.id
	s.mu.Unlock()
	defer s.removeClient(client.id)
	s.writeResult(client, request.ID, HelloResult{Version: s.cfg.Version, Protocol: ProtocolVersion, ClientID: client.id, InputOwner: owner})

	for {
		// ReadTimeout bounds the unauthenticated hello and the time an event
		// client may occupy client.sock without subscribing. Authenticated RPC
		// clients are intentionally persistent: the TUI can sit idle for an
		// arbitrary amount of time before opening a terminal or changing a node.
		if eventSocket && !client.subscribed {
			_ = conn.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout))
		} else {
			_ = conn.SetReadDeadline(time.Time{})
		}
		if !scanner.Scan() {
			break
		}
		request, err := decodeRequest(scanner.Bytes())
		if err != nil {
			s.writeResponse(client, Response{Error: AsRPCError(err)})
			continue
		}
		result, callErr := s.handle(context.Background(), client, request, eventSocket)
		if callErr != nil {
			s.writeResponse(client, Response{ID: request.ID, Error: AsRPCError(callErr)})
			continue
		}
		s.writeResult(client, request.ID, result)
		if request.Method == "events.subscribe" {
			s.mu.Lock()
			client.subscribed = true
			s.mu.Unlock()
		}
	}
}

func (s *Server) handle(ctx context.Context, client *clientConn, request Request, eventSocket bool) (any, error) {
	s.mu.Lock()
	owner := s.input == client.id
	s.mu.Unlock()
	switch request.Method {
	case "daemon.ping", "daemon.status":
		return s.status(), nil
	case "daemon.stop":
		s.Broadcast("daemon.shutdown", nil)
		time.AfterFunc(10*time.Millisecond, s.Stop)
		return map[string]bool{"stopping": true}, nil
	case "daemon.snapshot":
		return s.cfg.Handler.Snapshot(ctx)
	case "events.subscribe":
		if !eventSocket {
			return nil, Error("PreconditionFailed", "events.subscribe requires client.sock", 5, nil)
		}
		return map[string]bool{"subscribed": true}, nil
	case "input.takeover":
		s.mu.Lock()
		previous := s.input
		s.input = client.id
		s.mu.Unlock()
		if previous != "" && previous != client.id {
			s.Broadcast("input.revoked", map[string]string{"client_id": previous})
		}
		return map[string]bool{"input_owner": true}, nil
	}
	if mutatingInputMethod(request.Method) && !owner {
		return nil, Error("PreconditionFailed", "client is observe-only; request input.takeover first", 5, map[string]any{"client_id": client.id})
	}
	return s.cfg.Handler.Handle(ctx, ClientContext{ID: client.id, InputOwner: owner}, request.Method, request.Params)
}

func mutatingInputMethod(method string) bool {
	switch method {
	case "daemon.update", "terminal.send_text", "terminal.send_keys", "terminal.send_input", "terminal.send_event", "terminal.resize", "terminal.focus", "terminal.scroll", "terminal.open", "terminal.close":
		return true
	default:
		return false
	}
}

func (s *Server) status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{Running: true, PID: s.identity.PID, Version: s.identity.Version, Protocol: ProtocolVersion, Identity: s.identity.Token, StartedAt: s.identity.StartedAt, TerminalCount: s.cfg.Handler.TerminalCount(), InputOwner: s.input}
}

func decodeRequest(data []byte) (Request, error) {
	if len(data) > MaxMessageSize {
		return Request{}, Error("InvalidArgument", "daemon request exceeds 1 MiB", 2, nil)
	}
	var request Request
	if err := json.Unmarshal(data, &request); err != nil {
		return Request{}, Error("InvalidArgument", "invalid daemon request", 2, nil)
	}
	if request.Method == "" {
		return Request{}, Error("InvalidArgument", "daemon method is required", 2, nil)
	}
	return request, nil
}

func (s *Server) writeResult(client *clientConn, id uint64, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		s.writeResponse(client, Response{ID: id, Error: &RPCError{Category: "Internal", Message: err.Error(), Code: 7}})
		return
	}
	s.writeResponse(client, Response{ID: id, Result: data})
}

func (s *Server) writeResponse(client *clientConn, response Response) {
	client.writeMu.Lock()
	_ = client.encoder.Encode(response)
	client.writeMu.Unlock()
}

func (s *Server) removeClient(id string) {
	s.mu.Lock()
	delete(s.clients, id)
	if s.input == id {
		s.input = ""
	}
	s.mu.Unlock()
}

func (s *Server) closeClients() {
	s.mu.Lock()
	clients := make([]*clientConn, 0, len(s.clients))
	for _, client := range s.clients {
		clients = append(clients, client)
	}
	s.mu.Unlock()
	for _, client := range clients {
		_ = client.conn.Close()
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
	return writeAtomic(path, append(data, '\n'), mode)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp-" + randomToken()
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
