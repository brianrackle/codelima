package codelima

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const forwardingPollInterval = time.Second

type forwardingPeer interface {
	Discover(context.Context) ([]int, error)
	DialContext(context.Context, string, string) (net.Conn, error)
	Close() error
}

type forwardingPeerFactory interface {
	Prepare(context.Context, string) error
	Connect(context.Context, Node, ssh.Signer) (forwardingPeer, error)
}

type sshForwardingPeerFactory struct{ runtime SandboxSSHRuntime }

func (f sshForwardingPeerFactory) Prepare(ctx context.Context, publicKeyPath string) error {
	return f.runtime.AuthorizeSSHKey(ctx, publicKeyPath)
}

func (f sshForwardingPeerFactory) Connect(ctx context.Context, node Node, signer ssh.Signer) (forwardingPeer, error) {
	transport, err := f.runtime.OpenSSHTransport(ctx, node.SandboxName)
	if err != nil {
		return nil, err
	}
	connection := &sshTransportConn{ReadWriteCloser: transport}
	config := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Transport is already scoped to the resolved sandbox process.
	}
	type handshakeResult struct {
		connection ssh.Conn
		channels   <-chan ssh.NewChannel
		requests   <-chan *ssh.Request
		err        error
	}
	result := make(chan handshakeResult, 1)
	go func() {
		conn, channels, requests, handshakeErr := ssh.NewClientConn(connection, node.SandboxName, config)
		result <- handshakeResult{connection: conn, channels: channels, requests: requests, err: handshakeErr}
	}()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		_ = connection.Close()
		return nil, ctx.Err()
	case <-timer.C:
		_ = connection.Close()
		return nil, errors.New("microsandbox SSH handshake timed out")
	case outcome := <-result:
		if outcome.err != nil {
			_ = connection.Close()
			return nil, outcome.err
		}
		return &sshForwardingPeer{client: ssh.NewClient(outcome.connection, outcome.channels, outcome.requests)}, nil
	}
}

type sshTransportConn struct{ io.ReadWriteCloser }

func (*sshTransportConn) LocalAddr() net.Addr              { return forwardingAddr("host") }
func (*sshTransportConn) RemoteAddr() net.Addr             { return forwardingAddr("sandbox") }
func (*sshTransportConn) SetDeadline(time.Time) error      { return nil }
func (*sshTransportConn) SetReadDeadline(time.Time) error  { return nil }
func (*sshTransportConn) SetWriteDeadline(time.Time) error { return nil }

type forwardingAddr string

func (a forwardingAddr) Network() string { return "stdio" }
func (a forwardingAddr) String() string  { return string(a) }

type sshForwardingPeer struct{ client *ssh.Client }

func (p *sshForwardingPeer) Discover(ctx context.Context) ([]int, error) {
	session, err := p.client.NewSession()
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()
	type commandResult struct {
		output []byte
		err    error
	}
	result := make(chan commandResult, 1)
	go func() {
		output, runErr := session.CombinedOutput("cat /proc/net/tcp /proc/net/tcp6")
		result <- commandResult{output: output, err: runErr}
	}()
	select {
	case <-ctx.Done():
		_ = session.Close()
		return nil, ctx.Err()
	case outcome := <-result:
		if outcome.err != nil {
			return nil, outcome.err
		}
		return parseProcNetTCP(outcome.output), nil
	}
}

func (p *sshForwardingPeer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	type dialResult struct {
		connection net.Conn
		err        error
	}
	result := make(chan dialResult, 1)
	go func() {
		connection, err := p.client.Dial(network, address)
		result <- dialResult{connection: connection, err: err}
	}()
	select {
	case <-ctx.Done():
		go func() {
			outcome := <-result
			if outcome.connection != nil {
				_ = outcome.connection.Close()
			}
		}()
		return nil, ctx.Err()
	case outcome := <-result:
		return outcome.connection, outcome.err
	}
}

func (p *sshForwardingPeer) Close() error { return p.client.Close() }

func parseProcNetTCP(data []byte) []int {
	ports := map[int]struct{}{}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[3] != "0A" {
			continue
		}
		local := strings.Split(fields[1], ":")
		if len(local) != 2 || !forwardableProcAddress(local[0]) {
			continue
		}
		port64, err := strconv.ParseUint(local[1], 16, 16)
		if err != nil || port64 < 1024 {
			continue
		}
		ports[int(port64)] = struct{}{}
	}
	result := make([]int, 0, len(ports))
	for port := range ports {
		result = append(result, port)
	}
	sort.Ints(result)
	return result
}

func forwardableProcAddress(address string) bool {
	address = strings.ToUpper(address)
	switch address {
	case "00000000", "0100007F",
		"00000000000000000000000000000000",
		"00000000000000000000000001000000",
		"00000000000000000000000000000001":
		return true
	default:
		return false
	}
}

func loadOrCreateForwardingSigner(home string) (ssh.Signer, string, error) {
	directory := filepath.Join(home, "_daemon", "forwarding")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, "", err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, "", err
	}
	privatePath := filepath.Join(directory, "id_ed25519")
	publicPath := privatePath + ".pub"
	privatePEM, err := os.ReadFile(privatePath)
	if os.IsNotExist(err) {
		_, privateKey, generateErr := ed25519.GenerateKey(rand.Reader)
		if generateErr != nil {
			return nil, "", generateErr
		}
		encoded, marshalErr := x509.MarshalPKCS8PrivateKey(privateKey)
		if marshalErr != nil {
			return nil, "", marshalErr
		}
		privatePEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
		if err := atomicWriteFile(privatePath, privatePEM, 0o600); err != nil {
			return nil, "", err
		}
	} else if err != nil {
		return nil, "", err
	}
	if err := os.Chmod(privatePath, 0o600); err != nil {
		return nil, "", err
	}
	signer, err := ssh.ParsePrivateKey(privatePEM)
	if err != nil {
		return nil, "", err
	}
	publicKey := ssh.MarshalAuthorizedKey(signer.PublicKey())
	current, readErr := os.ReadFile(publicPath)
	if readErr != nil || string(current) != string(publicKey) {
		if err := atomicWriteFile(publicPath, publicKey, 0o644); err != nil {
			return nil, "", err
		}
	}
	if err := os.Chmod(publicPath, 0o644); err != nil {
		return nil, "", err
	}
	return signer, publicPath, nil
}

type dynamicRouteKey struct {
	node string
	port int
}

type dynamicForwardingRoute struct {
	key          dynamicRouteKey
	nodeID       string
	peer         forwardingPeer
	transport    *http.Transport
	proxy        *httputil.ReverseProxy
	discoveredAt time.Time
	seenAt       time.Time
}

func newDynamicForwardingRoute(node Node, port int, peer forwardingPeer, seenAt time.Time, logger *slog.Logger) *dynamicForwardingRoute {
	if logger == nil {
		logger = discardLogger()
	}
	route := &dynamicForwardingRoute{
		key:          dynamicRouteKey{node: strings.ToLower(node.SandboxName), port: port},
		nodeID:       node.ID,
		peer:         peer,
		discoveredAt: seenAt,
		seenAt:       seenAt,
	}
	route.transport = &http.Transport{
		Proxy:                 nil,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialGuestLoopback(ctx, route.peer, port)
		},
		ForceAttemptHTTP2: false,
	}
	route.proxy = &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(&url.URL{Scheme: "http", Host: "guest"})
			request.Out.Host = request.In.Host
		},
		Transport: route.transport,
		ErrorHandler: func(writer http.ResponseWriter, _ *http.Request, err error) {
			logger.Warn("dynamic forwarding request failed", "node", route.key.node, "port", route.key.port, "error", err.Error())
			http.Error(writer, "codelima could not reach the node service", http.StatusBadGateway)
		},
	}
	return route
}

func dialGuestLoopback(ctx context.Context, peer forwardingPeer, port int) (net.Conn, error) {
	var dialErr error
	for _, host := range []string{"127.0.0.1", "::1"} {
		address := net.JoinHostPort(host, strconv.Itoa(port))
		connection, err := peer.DialContext(ctx, "tcp", address)
		if err == nil {
			return connection, nil
		}
		dialErr = errors.Join(dialErr, fmt.Errorf("dial guest %s: %w", address, err))
		if ctx.Err() != nil {
			break
		}
	}
	return nil, dialErr
}

func (r *dynamicForwardingRoute) close() { r.transport.CloseIdleConnections() }

type dynamicPortServer struct {
	port        int
	listener    net.Listener
	server      *http.Server
	defaultNode string
	status      string
	lastErr     string
}

type dynamicForwarder struct {
	service  *Service
	factory  forwardingPeerFactory
	interval time.Duration
	listen   func(string, string) (net.Listener, error)
	logger   *slog.Logger

	mu       sync.RWMutex
	wg       sync.WaitGroup
	cancel   context.CancelFunc
	signer   ssh.Signer
	keyPath  string
	prepared bool
	peers    map[string]forwardingPeer
	failures map[string]int
	routes   map[dynamicRouteKey]*dynamicForwardingRoute
	known    map[string]bool
	servers  map[int]*dynamicPortServer
	lastErr  string
	lastPoll time.Time
}

func newDynamicForwarder(service *Service, runtime SandboxSSHRuntime) *dynamicForwarder {
	return &dynamicForwarder{
		logger: service.log(),

		service: service, factory: sshForwardingPeerFactory{runtime: runtime}, interval: forwardingPollInterval,
		listen: net.Listen, peers: map[string]forwardingPeer{}, failures: map[string]int{},
		routes: map[dynamicRouteKey]*dynamicForwardingRoute{}, known: map[string]bool{}, servers: map[int]*dynamicPortServer{},
	}
}

func (f *dynamicForwarder) Start(parent context.Context) error {
	signer, publicKeyPath, err := loadOrCreateForwardingSigner(f.service.cfg.MetadataRoot)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	f.mu.Lock()
	f.signer, f.keyPath, f.cancel = signer, publicKeyPath, cancel
	f.mu.Unlock()
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		f.run(ctx)
	}()
	return nil
}

func (f *dynamicForwarder) run(ctx context.Context) {
	f.reconcile(ctx)
	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.reconcile(ctx)
		}
	}
}

func (f *dynamicForwarder) reconcile(ctx context.Context) {
	f.mu.RLock()
	prepared, keyPath := f.prepared, f.keyPath
	f.mu.RUnlock()
	if !prepared {
		if err := f.factory.Prepare(ctx, keyPath); err != nil {
			f.recordError(fmt.Errorf("authorize dynamic forwarding key: %w", err))
			return
		}
		f.mu.Lock()
		f.prepared = true
		f.mu.Unlock()
	}
	nodes, err := f.service.NodeList(ctx, false)
	if err != nil {
		f.recordError(err)
		return
	}
	running := map[string]Node{}
	for _, node := range nodes {
		if node.LastRuntimeObservation != nil && node.LastRuntimeObservation.Status == ObservationRunning {
			running[node.ID] = node
		}
	}

	// Collect peers to close under the lock, close them outside it:
	// peer.Close tears down an SSH connection, and ServeHTTP read-locks this
	// mutex, so a slow teardown must not stall proxied requests
	// (reconcileServers already uses the same pattern for listeners).
	var closing []forwardingPeer
	f.mu.Lock()
	for nodeID, peer := range f.peers {
		if _, ok := running[nodeID]; ok {
			continue
		}
		closing = append(closing, peer)
		delete(f.peers, nodeID)
		delete(f.failures, nodeID)
		f.removeNodeRoutesLocked(nodeID)
	}
	f.known = map[string]bool{}
	for _, node := range running {
		f.known[strings.ToLower(node.SandboxName)] = true
	}
	f.mu.Unlock()
	for _, peer := range closing {
		_ = peer.Close()
	}

	for nodeID, node := range running {
		f.mu.RLock()
		peer := f.peers[nodeID]
		signer := f.signer
		f.mu.RUnlock()
		if peer == nil {
			peer, err = f.factory.Connect(ctx, node, signer)
			if err != nil {
				f.recordError(fmt.Errorf("connect %s forwarding peer: %w", node.SandboxName, err))
				continue
			}
			f.mu.Lock()
			f.peers[nodeID] = peer
			f.failures[nodeID] = 0
			f.mu.Unlock()
			f.log().Info("dynamic forwarding peer connected", "node", node.SandboxName)
		}
		discoveryCtx, cancelDiscovery := context.WithTimeout(ctx, 10*time.Second)
		ports, discoverErr := peer.Discover(discoveryCtx)
		cancelDiscovery()
		if discoverErr != nil {
			f.handleDiscoveryFailure(node, peer, discoverErr)
			continue
		}
		f.replaceNodeRoutes(node, peer, ports)
	}
	f.reconcileServers()
	f.mu.Lock()
	f.lastPoll = time.Now().UTC()
	f.mu.Unlock()
}

func (f *dynamicForwarder) handleDiscoveryFailure(node Node, peer forwardingPeer, err error) {
	f.mu.Lock()
	f.failures[node.ID]++
	f.lastErr = err.Error()
	if f.failures[node.ID] < 2 {
		f.mu.Unlock()
		return
	}
	delete(f.peers, node.ID)
	delete(f.failures, node.ID)
	f.removeNodeRoutesLocked(node.ID)
	f.mu.Unlock()
	// Close outside the lock: ServeHTTP read-locks it and SSH teardown can be
	// slow.
	_ = peer.Close()
}

func (f *dynamicForwarder) replaceNodeRoutes(node Node, peer forwardingPeer, ports []int) {
	now := time.Now().UTC()
	wanted := map[dynamicRouteKey]bool{}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[node.ID] = 0
	for _, port := range ports {
		key := dynamicRouteKey{node: strings.ToLower(node.SandboxName), port: port}
		wanted[key] = true
		if route := f.routes[key]; route != nil {
			route.seenAt = now
			continue
		}
		f.routes[key] = newDynamicForwardingRoute(node, port, peer, now, f.logger)
		f.log().Info("dynamic forwarding route added", "url", fmt.Sprintf("http://%s.localhost:%d", key.node, port))
	}
	for key, route := range f.routes {
		if route.nodeID == node.ID && !wanted[key] {
			route.close()
			delete(f.routes, key)
			f.log().Info("dynamic forwarding route removed", "url", fmt.Sprintf("http://%s.localhost:%d", key.node, key.port))
		}
	}
}

func (f *dynamicForwarder) removeNodeRoutesLocked(nodeID string) {
	for key, route := range f.routes {
		if route.nodeID == nodeID {
			route.close()
			delete(f.routes, key)
		}
	}
}

func (f *dynamicForwarder) reconcileServers() {
	f.mu.Lock()
	var closing []*http.Server
	wanted := map[int]bool{}
	for key := range f.routes {
		wanted[key.port] = true
	}
	for port, server := range f.servers {
		if wanted[port] && server.status == "serving" {
			if _, ok := f.routes[dynamicRouteKey{node: server.defaultNode, port: port}]; !ok {
				server.defaultNode = f.firstClaimantLocked(port)
				f.log().Info("dynamic forwarding generic route claimed", "port", port, "node", server.defaultNode)
			}
			continue
		}
		if !wanted[port] {
			if server.server != nil {
				closing = append(closing, server.server)
			}
			delete(f.servers, port)
		}
	}
	for port := range wanted {
		if current := f.servers[port]; current != nil && current.status == "serving" {
			continue
		}
		listener, err := f.listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			f.servers[port] = &dynamicPortServer{port: port, status: "conflicted", lastErr: err.Error()}
			f.lastErr = err.Error()
			continue
		}
		server := &http.Server{Handler: &dynamicForwardingHandler{forwarder: f, port: port}, ReadHeaderTimeout: 10 * time.Second}
		defaultNode := f.firstClaimantLocked(port)
		f.servers[port] = &dynamicPortServer{port: port, listener: listener, server: server, defaultNode: defaultNode, status: "serving"}
		go func() {
			if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				f.recordError(serveErr)
			}
		}()
		f.log().Info("dynamic forwarding listener serving", "address", listener.Addr().String())
		f.log().Info("dynamic forwarding generic route claimed", "port", port, "node", defaultNode)
	}
	f.mu.Unlock()
	for _, server := range closing {
		_ = server.Close()
	}
}

func (f *dynamicForwarder) firstClaimantLocked(port int) string {
	var selected *dynamicForwardingRoute
	for key, route := range f.routes {
		if key.port != port {
			continue
		}
		if selected == nil || route.discoveredAt.Before(selected.discoveredAt) ||
			(route.discoveredAt.Equal(selected.discoveredAt) && route.key.node < selected.key.node) {
			selected = route
		}
	}
	if selected == nil {
		return ""
	}
	return selected.key.node
}

func (f *dynamicForwarder) recordError(err error) {
	if err == nil {
		return
	}
	f.mu.Lock()
	f.lastErr = err.Error()
	f.mu.Unlock()
	f.log().Warn("dynamic forwarding error", "error", err.Error())
}

type dynamicForwardingHandler struct {
	forwarder *dynamicForwarder
	port      int
}

func (h *dynamicForwardingHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	nodeName, ok := nodeNameFromLocalhost(request.Host)
	h.forwarder.mu.RLock()
	if !ok && isGenericForwardingHost(request.Host) {
		if server := h.forwarder.servers[h.port]; server != nil {
			nodeName = server.defaultNode
			ok = nodeName != ""
		}
	}
	if !ok {
		h.forwarder.mu.RUnlock()
		http.Error(writer, "request host must be localhost, 127.0.0.1, or {node}.localhost", http.StatusMisdirectedRequest)
		return
	}
	route := h.forwarder.routes[dynamicRouteKey{node: nodeName, port: h.port}]
	known := h.forwarder.known[nodeName]
	h.forwarder.mu.RUnlock()
	if route == nil {
		if known {
			http.Error(writer, "the node is not listening on this port", http.StatusNotFound)
			return
		}
		http.Error(writer, "unknown codelima node", http.StatusMisdirectedRequest)
		return
	}
	route.proxy.ServeHTTP(writer, request)
}

func isGenericForwardingHost(hostport string) bool {
	host, ok := forwardingHostname(hostport)
	return ok && (host == "localhost" || host == "127.0.0.1")
}

func nodeNameFromLocalhost(hostport string) (string, bool) {
	host, ok := forwardingHostname(hostport)
	if !ok {
		return "", false
	}
	if !strings.HasSuffix(host, ".localhost") {
		return "", false
	}
	node := strings.TrimSuffix(host, ".localhost")
	if node == "" || strings.Contains(node, ".") {
		return "", false
	}
	return node, true
}

func forwardingHostname(hostport string) (string, bool) {
	host := strings.TrimSpace(hostport)
	if parsed, port, err := net.SplitHostPort(host); err == nil {
		portNumber, parseErr := strconv.Atoi(port)
		if parseErr != nil || portNumber < 1 || portNumber > 65535 {
			return "", false
		}
		host = parsed
	} else if strings.Count(host, ":") != 0 {
		return "", false
	}
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSuffix(host, ".")), ".")
	if host == "" {
		return "", false
	}
	return host, true
}

func (f *dynamicForwarder) Snapshot() map[string]any {
	f.mu.RLock()
	defer f.mu.RUnlock()
	routes := make([]map[string]any, 0, len(f.routes))
	for key := range f.routes {
		routes = append(routes, map[string]any{"node": key.node, "port": key.port, "url": fmt.Sprintf("http://%s.localhost:%d", key.node, key.port)})
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i]["url"].(string) < routes[j]["url"].(string) })
	ports := make([]map[string]any, 0, len(f.servers))
	for port, server := range f.servers {
		address := ""
		if server.listener != nil {
			address = server.listener.Addr().String()
		}
		ports = append(ports, map[string]any{"port": port, "address": address, "default_node": server.defaultNode, "status": server.status, "error": server.lastErr})
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i]["port"].(int) < ports[j]["port"].(int) })
	return map[string]any{"enabled": true, "authorized": f.prepared, "routes": routes, "ports": ports, "peers": len(f.peers), "last_error": f.lastErr, "last_poll_at": f.lastPoll}
}

func (f *dynamicForwarder) Close() error {
	f.mu.Lock()
	if f.cancel != nil {
		f.cancel()
	}
	f.mu.Unlock()
	f.wg.Wait()
	f.mu.Lock()
	servers := f.servers
	peers := f.peers
	f.servers = map[int]*dynamicPortServer{}
	f.peers = map[string]forwardingPeer{}
	for _, route := range f.routes {
		route.close()
	}
	f.routes = map[dynamicRouteKey]*dynamicForwardingRoute{}
	f.mu.Unlock()
	var closeErr error
	for _, server := range servers {
		if server.server != nil {
			closeErr = errors.Join(closeErr, server.server.Close())
		}
	}
	for _, peer := range peers {
		closeErr = errors.Join(closeErr, peer.Close())
	}
	return closeErr
}

// log returns the forwarder logger, tolerating test-constructed forwarders
// that never set one.
func (f *dynamicForwarder) log() *slog.Logger {
	if f != nil && f.logger != nil {
		return f.logger
	}
	return discardLogger()
}
