package codelima

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type publishedTCPMapping struct {
	nodeID    string
	guestPort int
	conflict  bool
}

// publishedTCPServer owns only its listeners and accepted connections. Closing
// it cancels pending guest dials and drains both copying directions before the
// port is reassigned. It never closes the node's shared SSH peer.
type publishedTCPServer struct {
	mapping     publishedTCPMapping
	listeners   []net.Listener
	lastErr     string
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	closed      bool
	connections map[net.Conn]struct{}
	wg          sync.WaitGroup
}

func (f *dynamicForwarder) publishedMappingsLocked() map[int]publishedTCPMapping {
	wanted := map[int]publishedTCPMapping{}
	for id, state := range f.nodes {
		if !state.node.DaemonPortForwarding || !applicationRoutesReady(state.node) {
			continue
		}
		ports, err := validatePorts(state.node.Ports)
		if err != nil {
			continue
		}
		for _, value := range ports {
			parts := strings.SplitN(value, ":", 2)
			host, _ := strconv.Atoi(parts[0])
			guest, _ := strconv.Atoi(parts[1])
			if _, exists := wanted[host]; exists {
				wanted[host] = publishedTCPMapping{conflict: true}
			} else {
				wanted[host] = publishedTCPMapping{nodeID: id, guestPort: guest}
			}
		}
	}
	return wanted
}

func (f *dynamicForwarder) reconcileTCPServers() {
	f.mu.Lock()
	wanted := f.publishedMappingsLocked()
	if f.tcpServers == nil {
		f.tcpServers = map[int]*publishedTCPServer{}
	}
	var closing []*publishedTCPServer
	var httpClosing []*http.Server
	for port, server := range f.tcpServers {
		mapping, exists := wanted[port]
		if !exists || mapping != server.mapping || (server.lastErr != "" && len(server.listeners) != 0) {
			closing = append(closing, server)
			delete(f.tcpServers, port)
		}
	}
	for port := range wanted {
		if server := f.servers[port]; server != nil && server.server != nil {
			httpClosing = append(httpClosing, server.server)
			delete(f.servers, port)
		}
	}
	f.mu.Unlock()
	// Release old ownership before attempting a bind. Never wait for a relay
	// while holding f.mu: its in-flight dial may need the cached peer.
	for _, server := range closing {
		server.close()
	}
	for _, server := range httpClosing {
		_ = server.Close()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for port, mapping := range wanted {
		if server := f.tcpServers[port]; server != nil && len(server.listeners) != 0 {
			continue
		}
		server := &publishedTCPServer{mapping: mapping}
		f.tcpServers[port] = server
		if mapping.conflict {
			server.lastErr = "multiple ready nodes reserve this published host port"
			continue
		}
		listeners, err := f.listenLoopbacks(port)
		if err != nil {
			server.lastErr = err.Error()
			continue
		}
		server.listeners = listeners
		server.ctx, server.cancel = context.WithCancel(context.Background())
		server.connections = map[net.Conn]struct{}{}
		// Keep a nonzero count until every accept loop has exited; this makes
		// connection Adds safe while close is waiting for the loops to drain.
		server.wg.Add(len(listeners))
		for _, listener := range listeners {
			go server.accept(listener, func(ctx context.Context) (net.Conn, error) {
				return f.dialPublishedGuest(ctx, mapping)
			}, func(err error) {
				f.mu.Lock()
				if f.tcpServers[port] == server {
					server.lastErr = err.Error()
				}
				f.mu.Unlock()
			})
		}
	}
}

func (f *dynamicForwarder) dialPublishedGuest(ctx context.Context, mapping publishedTCPMapping) (net.Conn, error) {
	f.mu.RLock()
	state := f.nodes[mapping.nodeID]
	var peer forwardingPeer
	if state != nil && applicationRoutesReady(state.node) && !state.routesLapsed {
		peer = state.peer
	}
	f.mu.RUnlock()
	if peer == nil {
		return nil, errors.New("guest tunnel is unavailable; retrying")
	}
	dialCtx, cancel := context.WithTimeout(ctx, durationOrDefault(f.dialTimeout, forwardingDialTimeout))
	defer cancel()
	return dialGuestLoopback(dialCtx, peer, mapping.guestPort)
}

func (s *publishedTCPServer) track(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		_ = conn.Close()
		return false
	}
	s.connections[conn] = struct{}{}
	return true
}

func (s *publishedTCPServer) release(conn net.Conn) {
	_ = conn.Close()
	s.mu.Lock()
	delete(s.connections, conn)
	s.mu.Unlock()
}

func (s *publishedTCPServer) accept(listener net.Listener, dial func(context.Context) (net.Conn, error), failed func(error)) {
	defer s.wg.Done()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				failed(err)
			}
			return
		}
		if !s.track(conn) {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.release(conn)
			guest, err := dial(s.ctx)
			if err != nil || !s.track(guest) {
				return
			}
			defer s.release(guest)
			relayTCP(conn, guest)
		}()
	}
}

func relayTCP(host, guest net.Conn) {
	copyDone := make(chan struct{}, 1)
	copyDirection := func(dst, src net.Conn) {
		_, err := io.Copy(dst, src)
		if half, ok := dst.(interface{ CloseWrite() error }); ok && err == nil {
			err = half.CloseWrite()
		} else {
			err = errors.New("connection does not support half-close")
		}
		if err != nil {
			_ = host.Close()
			_ = guest.Close()
		}
	}
	go func() { copyDirection(guest, host); copyDone <- struct{}{} }()
	copyDirection(host, guest)
	<-copyDone
}

func (s *publishedTCPServer) close() {
	s.mu.Lock()
	s.closed = true
	connections := make([]net.Conn, 0, len(s.connections))
	for conn := range s.connections {
		connections = append(connections, conn)
	}
	s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	for _, listener := range s.listeners {
		_ = listener.Close()
	}
	for _, conn := range connections {
		_ = conn.Close()
	}
	s.wg.Wait()
}

func (f *dynamicForwarder) publishedTCPStatusLocked(server *publishedTCPServer) string {
	if len(server.listeners) == 0 || server.lastErr != "" {
		return "conflicted"
	}
	state := f.nodes[server.mapping.nodeID]
	if state == nil || state.peer == nil || state.routesLapsed {
		return "retrying"
	}
	return "serving"
}

func (f *dynamicForwarder) publishedTCPDegradedLocked() bool {
	for _, server := range f.tcpServers {
		if f.publishedTCPStatusLocked(server) != "serving" {
			return true
		}
	}
	return false
}

func (f *dynamicForwarder) publishedTCPSnapshotLocked() []map[string]any {
	rows := make([]map[string]any, 0, len(f.tcpServers))
	for port, server := range f.tcpServers {
		rows = append(rows, map[string]any{"host_port": port, "guest_port": server.mapping.guestPort, "node_id": server.mapping.nodeID,
			"address": fmt.Sprintf("localhost:%d", port), "status": f.publishedTCPStatusLocked(server), "error": server.lastErr})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["host_port"].(int) < rows[j]["host_port"].(int) })
	return rows
}
