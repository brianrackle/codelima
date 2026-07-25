package codelima

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

const forwardingPollInterval = time.Second

type guestCPUCounters struct {
	Total uint64
	Idle  uint64
}

type guestResourceUsage struct {
	UsedBytes  uint64
	TotalBytes uint64
}

type forwardingGuestObservation struct {
	Ports  []int
	CPU    guestCPUCounters
	Memory guestResourceUsage
	Disk   guestResourceUsage
}

type nodeCPUUsageSample struct {
	Percent   float64
	SampledAt time.Time
}

type nodeResourceUsageSample struct {
	Memory    guestResourceUsage
	Disk      guestResourceUsage
	SampledAt time.Time
}

type forwardingPeer interface {
	Observe(context.Context) (forwardingGuestObservation, error)
	DialContext(context.Context, string, string) (net.Conn, error)
	Close() error
}

type forwardingPeerFactory interface {
	Prepare(context.Context) error
	Connect(context.Context, Node) (forwardingPeer, error)
}

type sshForwardingPeer struct{ client *ssh.Client }

func (p *sshForwardingPeer) Observe(ctx context.Context) (forwardingGuestObservation, error) {
	session, err := p.client.NewSession()
	if err != nil {
		return forwardingGuestObservation{}, err
	}
	defer func() { _ = session.Close() }()
	type commandResult struct {
		output []byte
		err    error
	}
	result := make(chan commandResult, 1)
	go func() {
		output, runErr := session.CombinedOutput(`head -n 1 /proc/stat
awk '/^MemTotal:/ { total=$2 } /^MemAvailable:/ { available=$2 } END { if (total != "" && available != "") printf "codelima-memory %s %s\n", total, available }' /proc/meminfo
df -Pk / | awk 'NR == 2 { printf "codelima-disk %s %s\n", $2, $3 }'
cat /proc/net/tcp /proc/net/tcp6`)
		result <- commandResult{output: output, err: runErr}
	}()
	select {
	case <-ctx.Done():
		_ = session.Close()
		return forwardingGuestObservation{}, ctx.Err()
	case outcome := <-result:
		if outcome.err != nil {
			return forwardingGuestObservation{}, outcome.err
		}
		return parseForwardingGuestObservation(outcome.output), nil
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

func parseForwardingGuestObservation(data []byte) forwardingGuestObservation {
	observation := forwardingGuestObservation{Ports: parseProcNetTCP(data)}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "cpu":
			if counters, ok := parseGuestCPUCounters(fields); ok {
				observation.CPU = counters
			}
		case "codelima-memory":
			if usage, ok := parseGuestResourceUsage(fields, true); ok {
				observation.Memory = usage
			}
		case "codelima-disk":
			if usage, ok := parseGuestResourceUsage(fields, false); ok {
				observation.Disk = usage
			}
		}
	}
	return observation
}

func parseGuestCPUCounters(fields []string) (guestCPUCounters, bool) {
	if len(fields) < 6 {
		return guestCPUCounters{}, false
	}

	// /proc/stat includes guest and guest_nice after steal, but those
	// counters are already included in user and nice. Sum through steal
	// only so guest CPU time is not counted twice.
	count := min(len(fields)-1, 8)
	values := make([]uint64, count)
	for index := range count {
		value, err := strconv.ParseUint(fields[index+1], 10, 64)
		if err != nil {
			return guestCPUCounters{}, false
		}
		values[index] = value
	}

	var total uint64
	for _, value := range values {
		if math.MaxUint64-total < value {
			return guestCPUCounters{}, false
		}
		total += value
	}
	if math.MaxUint64-values[3] < values[4] {
		return guestCPUCounters{}, false
	}
	return guestCPUCounters{Total: total, Idle: values[3] + values[4]}, true
}

func parseGuestResourceUsage(fields []string, secondValueIsAvailable bool) (guestResourceUsage, bool) {
	if len(fields) != 3 {
		return guestResourceUsage{}, false
	}
	totalKiB, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil || totalKiB == 0 || totalKiB > math.MaxUint64/1024 {
		return guestResourceUsage{}, false
	}
	secondKiB, err := strconv.ParseUint(fields[2], 10, 64)
	if err != nil || secondKiB > totalKiB {
		return guestResourceUsage{}, false
	}
	usedKiB := secondKiB
	if secondValueIsAvailable {
		usedKiB = totalKiB - secondKiB
	}
	return guestResourceUsage{
		UsedBytes:  usedKiB * 1024,
		TotalBytes: totalKiB * 1024,
	}, true
}

func guestCPUUsagePercent(previous, current guestCPUCounters) (float64, bool) {
	if previous.Total == 0 || current.Total <= previous.Total || current.Idle < previous.Idle {
		return 0, false
	}
	totalDelta := current.Total - previous.Total
	idleDelta := current.Idle - previous.Idle
	if idleDelta > totalDelta {
		return 0, false
	}
	return float64(totalDelta-idleDelta) * 100 / float64(totalDelta), true
}

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
	listeners   []net.Listener
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

	mu        sync.RWMutex
	wg        sync.WaitGroup
	cancel    context.CancelFunc
	prepared  bool
	peers     map[string]forwardingPeer
	failures  map[string]int
	routes    map[dynamicRouteKey]*dynamicForwardingRoute
	known     map[string]bool
	servers   map[int]*dynamicPortServer
	cpu       map[string]nodeCPUUsageSample
	resources map[string]nodeResourceUsageSample
	counters  map[string]guestCPUCounters
	lastErr   string
	lastPoll  time.Time
}

func newDynamicForwarder(service *Service, runtime LimaSSHRuntime) *dynamicForwarder {
	return &dynamicForwarder{
		logger: service.log(),

		service: service, factory: limaSSHForwardingPeerFactory{runtime: runtime}, interval: forwardingPollInterval,
		listen: net.Listen, peers: map[string]forwardingPeer{}, failures: map[string]int{},
		routes: map[dynamicRouteKey]*dynamicForwardingRoute{}, known: map[string]bool{}, servers: map[int]*dynamicPortServer{},
		cpu: map[string]nodeCPUUsageSample{}, resources: map[string]nodeResourceUsageSample{},
		counters: map[string]guestCPUCounters{},
	}
}

func (f *dynamicForwarder) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	f.mu.Lock()
	f.cancel = cancel
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
	prepared := f.prepared
	f.mu.RUnlock()
	if !prepared {
		if err := f.factory.Prepare(ctx); err != nil {
			f.recordError(fmt.Errorf("prepare dynamic forwarding: %w", err))
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
		delete(f.cpu, nodeID)
		delete(f.resources, nodeID)
		delete(f.counters, nodeID)
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
		f.mu.RUnlock()
		if peer == nil {
			peer, err = f.factory.Connect(ctx, node)
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
		observation, discoverErr := peer.Observe(discoveryCtx)
		cancelDiscovery()
		if discoverErr != nil {
			f.handleDiscoveryFailure(node, peer, discoverErr)
			continue
		}
		f.recordNodeUsage(node.ID, observation, time.Now().UTC())
		f.replaceNodeRoutes(node, peer, observation.Ports)
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
	delete(f.cpu, node.ID)
	delete(f.resources, node.ID)
	if f.failures[node.ID] < 2 {
		f.mu.Unlock()
		return
	}
	delete(f.peers, node.ID)
	delete(f.failures, node.ID)
	delete(f.counters, node.ID)
	f.removeNodeRoutesLocked(node.ID)
	f.mu.Unlock()
	// Close outside the lock: ServeHTTP read-locks it and SSH teardown can be
	// slow.
	_ = peer.Close()
}

func (f *dynamicForwarder) recordNodeUsage(nodeID string, observation forwardingGuestObservation, sampledAt time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cpu == nil {
		f.cpu = map[string]nodeCPUUsageSample{}
	}
	if f.resources == nil {
		f.resources = map[string]nodeResourceUsageSample{}
	}
	if f.counters == nil {
		f.counters = map[string]guestCPUCounters{}
	}
	if observation.CPU.Total == 0 {
		delete(f.cpu, nodeID)
		delete(f.counters, nodeID)
	} else {
		previous, hadPrevious := f.counters[nodeID]
		f.counters[nodeID] = observation.CPU
		percent, valid := guestCPUUsagePercent(previous, observation.CPU)
		if !hadPrevious || !valid {
			delete(f.cpu, nodeID)
		} else {
			f.cpu[nodeID] = nodeCPUUsageSample{Percent: percent, SampledAt: sampledAt}
		}
	}

	sample := nodeResourceUsageSample{SampledAt: sampledAt}
	if validGuestResourceUsage(observation.Memory) {
		sample.Memory = observation.Memory
	}
	if validGuestResourceUsage(observation.Disk) {
		sample.Disk = observation.Disk
	}
	if sample.Memory.TotalBytes == 0 && sample.Disk.TotalBytes == 0 {
		delete(f.resources, nodeID)
	} else {
		f.resources[nodeID] = sample
	}
}

func validGuestResourceUsage(usage guestResourceUsage) bool {
	return usage.TotalBytes > 0 && usage.UsedBytes <= usage.TotalBytes
}

// addNodeUsage merges daemon-owned live telemetry into a node-list response.
// The store never sees these fields, keeping ordinary reads free of metadata
// writes and preventing a one-second metric from churning node.yaml.
func (f *dynamicForwarder) addNodeUsage(nodes []Node) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for index := range nodes {
		observation := nodes[index].LastRuntimeObservation
		if observation == nil || observation.Status != ObservationRunning {
			continue
		}
		enriched := *observation
		if sample, ok := f.cpu[nodes[index].ID]; ok {
			percent := sample.Percent
			sampledAt := sample.SampledAt
			enriched.CPUUsagePercent = &percent
			enriched.CPUUsageSampledAt = &sampledAt
		}
		if sample, ok := f.resources[nodes[index].ID]; ok {
			if validGuestResourceUsage(sample.Memory) {
				used, total := sample.Memory.UsedBytes, sample.Memory.TotalBytes
				enriched.MemoryUsedBytes = &used
				enriched.MemoryTotalBytes = &total
			}
			if validGuestResourceUsage(sample.Disk) {
				used, total := sample.Disk.UsedBytes, sample.Disk.TotalBytes
				enriched.DiskUsedBytes = &used
				enriched.DiskTotalBytes = &total
			}
			sampledAt := sample.SampledAt
			enriched.ResourceUsageSampledAt = &sampledAt
		}
		nodes[index].LastRuntimeObservation = &enriched
	}
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
		listeners, err := f.listenLoopbacks(port)
		if err != nil {
			f.servers[port] = &dynamicPortServer{port: port, status: "conflicted", lastErr: err.Error()}
			f.lastErr = err.Error()
			continue
		}
		server := &http.Server{Handler: &dynamicForwardingHandler{forwarder: f, port: port}, ReadHeaderTimeout: 10 * time.Second}
		defaultNode := f.firstClaimantLocked(port)
		f.servers[port] = &dynamicPortServer{port: port, listeners: listeners, server: server, defaultNode: defaultNode, status: "serving"}
		for _, listener := range listeners {
			go f.serveListener(server, listener)
			f.log().Info("dynamic forwarding listener serving", "address", listener.Addr().String())
		}
		f.log().Info("dynamic forwarding generic route claimed", "port", port, "node", defaultNode)
	}
	f.mu.Unlock()
	for _, server := range closing {
		_ = server.Close()
	}
}

func (f *dynamicForwarder) listenLoopbacks(port int) ([]net.Listener, error) {
	portText := strconv.Itoa(port)
	ipv4, err := f.listen("tcp4", net.JoinHostPort("127.0.0.1", portText))
	if err != nil {
		return nil, fmt.Errorf("listen on IPv4 loopback: %w", err)
	}
	listeners := []net.Listener{ipv4}
	ipv6, err := f.listen("tcp6", net.JoinHostPort("::1", portText))
	if err == nil {
		return append(listeners, ipv6), nil
	}
	if ipv6LoopbackUnavailable(err) {
		return listeners, nil
	}
	closeErr := ipv4.Close()
	return nil, errors.Join(fmt.Errorf("listen on IPv6 loopback: %w", err), closeErr)
}

func ipv6LoopbackUnavailable(err error) bool {
	return errors.Is(err, syscall.EAFNOSUPPORT) ||
		errors.Is(err, syscall.EPROTONOSUPPORT) ||
		errors.Is(err, syscall.EADDRNOTAVAIL)
}

func (f *dynamicForwarder) serveListener(server *http.Server, listener net.Listener) {
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		f.recordError(err)
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
		addresses := make([]string, 0, len(server.listeners))
		for _, listener := range server.listeners {
			addresses = append(addresses, listener.Addr().String())
		}
		address := ""
		if len(addresses) > 0 {
			address = addresses[0]
		}
		ports = append(ports, map[string]any{"port": port, "address": address, "addresses": addresses, "default_node": server.defaultNode, "status": server.status, "error": server.lastErr})
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
	f.cpu = map[string]nodeCPUUsageSample{}
	f.resources = map[string]nodeResourceUsageSample{}
	f.counters = map[string]guestCPUCounters{}
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
