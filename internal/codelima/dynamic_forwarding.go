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

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

const (
	forwardingPollInterval = time.Second
	// forwardingScanTimeout bounds one guest listener scan and
	// forwardingTelemetryTimeout bounds one telemetry sample. The two run as
	// separate guest commands so a slow or failing telemetry probe can never
	// influence routing state.
	forwardingScanTimeout      = 10 * time.Second
	forwardingTelemetryTimeout = 10 * time.Second
	// forwardingRouteGrace keeps a node's routes serving across transient
	// discovery failures. Once it elapses the routes lapse into retryable 502s;
	// only a confirmed stop or delete removes them (invariant I6).
	forwardingRouteGrace = 15 * time.Second
	// forwardingStopGrace is how long a not-running observation must persist,
	// with nothing contradicting it, before the node counts as confirmed
	// stopped. A guest that still answers its scan overrides the observation.
	forwardingStopGrace = 15 * time.Second
	// forwardingDialTimeout bounds one proxied dial into a guest so a wedged
	// peer fails the request quickly instead of hanging until the client
	// gives up.
	forwardingDialTimeout = 5 * time.Second
	// Keepalive settings for the per-peer health monitor. A dead-but-not-closed
	// SSH connection (the normal state after host sleep/wake) is otherwise
	// invisible until something blocks on it.
	forwardingKeepaliveInterval  = 5 * time.Second
	forwardingKeepaliveTimeout   = 5 * time.Second
	forwardingKeepaliveTolerance = 3
	// forwardingConcurrency caps the per-node reconcile fan-out so one
	// unreachable node cannot freeze discovery for every other node.
	forwardingConcurrency = 4
	// Per-node retry backoff, which also rate-limits the matching warn logs.
	forwardingBackoffInitial = time.Second
	forwardingBackoffMaximum = 30 * time.Second
	forwardingWarnInterval   = 30 * time.Second
	// forwardingClaimMemory is how long a removed route's discovery time is
	// remembered, so a node that blinks and returns reclaims the generic host
	// instead of permanently losing it to a later claimant.
	forwardingClaimMemory = 2 * time.Minute
	// codexLoginCallbackPort is Codex CLI's default browser-auth callback.
	// Its newest-listener policy is documented in ADR 119.
	codexLoginCallbackPort = 1455
)

// Guest commands. Listener discovery is deliberately the cheapest possible
// read: telemetry sampling runs separately so its cost and failures stay out of
// the routing path.
const (
	guestPortScanCommand  = "cat /proc/net/tcp /proc/net/tcp6"
	guestTelemetryCommand = `head -n 1 /proc/stat
awk '/^MemTotal:/ { total=$2 } /^MemAvailable:/ { available=$2 } END { if (total != "" && available != "") printf "codelima-memory %s %s\n", total, available }' /proc/meminfo
df -Pk / | awk 'NR == 2 { printf "codelima-disk %s %s\n", $2, $3 }'`
)

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

// nodeUsageEvent is the payload of the node.usage_changed broadcast. It is an
// alias, not a second declaration: the daemon package owns every event payload
// shape (see daemon.NodeUsageEvent for what the fields mean), and a publisher
// and a subscriber describing the same bytes with two structs is how a wire
// contract drifts. The local spelling is kept because this package is where the
// samples are produced.
type nodeUsageEvent = daemon.NodeUsageEvent

type forwardingPeer interface {
	// ScanPorts lists the guest's listening ports. It is separate from
	// SampleTelemetry on purpose: routes follow this call alone.
	ScanPorts(context.Context) ([]int, error)
	// SampleTelemetry reads CPU, memory, and disk counters. Its failures are
	// never allowed to affect peers or routes.
	SampleTelemetry(context.Context) (forwardingGuestObservation, error)
	// Ping is a cheap liveness probe used by the keepalive monitor.
	Ping(context.Context) error
	DialContext(context.Context, string, string) (net.Conn, error)
	Close() error
}

type forwardingPeerFactory interface {
	Prepare(context.Context) error
	Connect(context.Context, Node) (forwardingPeer, error)
}

type sshForwardingPeer struct{ client *ssh.Client }

func (p *sshForwardingPeer) ScanPorts(ctx context.Context) ([]int, error) {
	output, err := p.run(ctx, guestPortScanCommand)
	if err != nil {
		return nil, err
	}
	return parseProcNetTCP(output), nil
}

func (p *sshForwardingPeer) SampleTelemetry(ctx context.Context) (forwardingGuestObservation, error) {
	output, err := p.run(ctx, guestTelemetryCommand)
	if err != nil {
		return forwardingGuestObservation{}, err
	}
	return parseForwardingGuestObservation(output), nil
}

// Ping issues OpenSSH's keepalive global request. A refusal reply still proves
// the transport is alive; only a transport error or an expired context counts
// as a missed keepalive.
func (p *sshForwardingPeer) Ping(ctx context.Context) error {
	result := make(chan error, 1)
	go func() {
		_, _, err := p.client.SendRequest("keepalive@openssh.com", true, nil)
		result <- err
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-result:
		return err
	}
}

// run executes one guest command under the caller's deadline. ssh sessions have
// no context-aware API, so the command runs on its own goroutine and the
// session is closed on cancellation to release it.
func (p *sshForwardingPeer) run(ctx context.Context, command string) ([]byte, error) {
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
		output, runErr := session.CombinedOutput(command)
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
		return outcome.output, nil
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

// peerHealthMonitor detects a dead-but-not-closed SSH peer. Nothing else
// notices one: after host sleep/wake the host-side socket survives while the
// guest end is gone, so scans and proxied dials only fail once their deadlines
// expire. The monitor pings on an interval and, after a tolerated number of
// consecutive misses, closes the peer so in-flight work fails immediately and
// the next reconcile reconnects. Losing a peer never removes routes.
type peerHealthMonitor struct {
	peer      forwardingPeer
	node      string
	interval  time.Duration
	timeout   time.Duration
	tolerance int
	logger    *slog.Logger

	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.Mutex
	misses  int
	dead    bool
	lastErr string
}

func newPeerHealthMonitor(peer forwardingPeer, node string, interval, timeout time.Duration, tolerance int, logger *slog.Logger) *peerHealthMonitor {
	if logger == nil {
		logger = discardLogger()
	}
	return &peerHealthMonitor{
		peer: peer, node: node, interval: interval, timeout: timeout,
		tolerance: tolerance, logger: logger, done: make(chan struct{}),
	}
}

// start runs the ping loop until stop is called or the forwarder's own context
// ends. It is deliberately not tied to one reconcile pass: dead peers must be
// discovered between passes, not only while one is running.
func (m *peerHealthMonitor) start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	go m.run(ctx)
}

func (m *peerHealthMonitor) run(ctx context.Context) {
	defer close(m.done)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		pingCtx, cancel := context.WithTimeout(ctx, m.timeout)
		err := m.peer.Ping(pingCtx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		if err == nil {
			m.misses = 0
			m.mu.Unlock()
			continue
		}
		m.misses++
		m.lastErr = err.Error()
		misses := m.misses
		m.mu.Unlock()
		if misses < m.tolerance {
			continue
		}
		m.logger.Warn("dynamic forwarding peer keepalive lost", "node", m.node, "misses", misses, "error", err.Error())
		// Closing here is what unblocks anything already waiting on the dead
		// connection; the reconcile loop reconnects on its next pass. The peer
		// is marked dead only afterwards so an observer that sees "not alive"
		// can rely on the connection already being torn down.
		_ = m.peer.Close()
		m.mu.Lock()
		m.dead = true
		m.mu.Unlock()
		return
	}
}

func (m *peerHealthMonitor) alive() bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.dead
}

func (m *peerHealthMonitor) failure() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastErr
}

func (m *peerHealthMonitor) stop() {
	if m == nil || m.cancel == nil {
		return
	}
	m.cancel()
	<-m.done
}

// retiredPeer carries a peer and its monitor out of the forwarder lock. Peer
// teardown closes an SSH connection and monitor teardown waits for a ping to
// return, and ServeHTTP read-locks the forwarder, so neither may run under it.
type retiredPeer struct {
	peer    forwardingPeer
	monitor *peerHealthMonitor
	node    string
	reason  string
}

func (r retiredPeer) close() {
	r.monitor.stop()
	if r.peer != nil {
		_ = r.peer.Close()
	}
}

func (r retiredPeer) empty() bool { return r.peer == nil && r.monitor == nil }

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

// routeDiscoveryMemory remembers when a removed route was first discovered so a
// node that briefly disappears reclaims the generic host on its return instead
// of being permanently outranked by a later claimant.
type routeDiscoveryMemory struct {
	discoveredAt time.Time
	removedAt    time.Time
}

type dynamicForwardingRoute struct {
	key          dynamicRouteKey
	nodeID       string
	transport    *http.Transport
	proxy        *httputil.ReverseProxy
	dialTimeout  time.Duration
	discoveredAt time.Time
	seenAt       time.Time
	lapsedAt     time.Time
	// lapsed marks a route whose node is failing discovery. The route and its
	// host listener stay in place and answer a retryable 502 rather than
	// disappearing, which would surface as a cached connection refusal.
	lapsed bool

	// peerMu guards peer alone. Proxied dials read it without the forwarder
	// lock so a reconnect can re-point live routes at the replacement peer.
	peerMu sync.Mutex
	peer   forwardingPeer
}

func newDynamicForwardingRoute(node Node, port int, peer forwardingPeer, discoveredAt time.Time, logger *slog.Logger) *dynamicForwardingRoute {
	if logger == nil {
		logger = discardLogger()
	}
	route := &dynamicForwardingRoute{
		key:          dynamicRouteKey{node: strings.ToLower(node.SandboxName), port: port},
		nodeID:       node.ID,
		peer:         peer,
		dialTimeout:  forwardingDialTimeout,
		discoveredAt: discoveredAt,
		seenAt:       discoveredAt,
	}
	route.transport = &http.Transport{
		Proxy:                 nil,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			// Bound the dial: a dead-but-not-closed peer would otherwise hold
			// the request open for as long as the client waits.
			dialCtx, cancel := context.WithTimeout(ctx, durationOrDefault(route.dialTimeout, forwardingDialTimeout))
			defer cancel()
			return dialGuestLoopback(dialCtx, route.currentPeer(), port)
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

func (r *dynamicForwardingRoute) currentPeer() forwardingPeer {
	r.peerMu.Lock()
	defer r.peerMu.Unlock()
	return r.peer
}

func (r *dynamicForwardingRoute) setPeer(peer forwardingPeer) {
	if peer == nil {
		return
	}
	r.peerMu.Lock()
	defer r.peerMu.Unlock()
	r.peer = peer
}

func (r *dynamicForwardingRoute) url() string {
	return fmt.Sprintf("http://%s.localhost:%d", r.key.node, r.key.port)
}

func dialGuestLoopback(ctx context.Context, peer forwardingPeer, port int) (net.Conn, error) {
	if peer == nil {
		return nil, errors.New("no forwarding peer for this node")
	}
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

// forwardingNodeState is one node's peer lifecycle:
//
//	healthy   - the last listener scan succeeded; routes serve traffic.
//	suspect   - scans or keepalives are failing; routes keep serving through
//	            the grace window and telemetry is dropped.
//	lapsed    - the grace window elapsed; routes and their host listeners stay
//	            bound but answer 502 while the peer is rebuilt.
//	retired   - a confirmed stop or delete; only here are routes removed.
type forwardingNodeState struct {
	node    Node
	peer    forwardingPeer
	monitor *peerHealthMonitor

	lastHealthyAt   time.Time
	failures        int
	firstFailureAt  time.Time
	notRunningSince time.Time
	routesLapsed    bool

	backoff      time.Duration
	backoffUntil time.Time
	lastWarnAt   time.Time
	lastErr      string
}

// confirmedStopped reports whether the node counts as a confirmed stop. The
// observer cache alone never qualifies. The not-running observation must have
// persisted for the whole grace window and the guest must have stopped
// answering: either there is no transport at all, or discovery has been failing
// long enough for the routes to lapse. A guest that still answers its scan is
// running whatever a synthesized or stale cache entry says.
func (s *forwardingNodeState) confirmedStopped(now time.Time, grace time.Duration) bool {
	if s.notRunningSince.IsZero() || now.Sub(s.notRunningSince) < grace {
		return false
	}
	return s.peer == nil || s.routesLapsed
}

func (s *forwardingNodeState) noteHealthy(now time.Time) {
	s.lastHealthyAt = now
	s.failures = 0
	s.firstFailureAt = time.Time{}
	s.routesLapsed = false
	s.backoff = 0
	s.backoffUntil = time.Time{}
	s.lastWarnAt = time.Time{}
	s.lastErr = ""
}

func (s *forwardingNodeState) health() string {
	switch {
	case s.routesLapsed:
		return "lapsed"
	case s.failures > 0:
		return "suspect"
	case s.peer == nil:
		return "connecting"
	default:
		return "healthy"
	}
}

type dynamicForwarder struct {
	service  *Service
	factory  forwardingPeerFactory
	interval time.Duration
	listen   func(string, string) (net.Listener, error)
	logger   *slog.Logger
	// now is injectable so grace windows and claim memory are testable.
	now func() time.Time

	scanTimeout        time.Duration
	telemetryTimeout   time.Duration
	dialTimeout        time.Duration
	routeGrace         time.Duration
	stopGrace          time.Duration
	claimMemory        time.Duration
	warnInterval       time.Duration
	keepaliveInterval  time.Duration
	keepaliveTimeout   time.Duration
	keepaliveTolerance int
	concurrency        int

	mu sync.RWMutex
	// broadcast publishes daemon events. It is nil until the host wires it (and
	// in every forwarder built without a daemon), so every publish is guarded.
	broadcast  func(string, any)
	wg         sync.WaitGroup
	cancel     context.CancelFunc
	prepared   bool
	nodes      map[string]*forwardingNodeState
	routes     map[dynamicRouteKey]*dynamicForwardingRoute
	discovered map[dynamicRouteKey]routeDiscoveryMemory
	known      map[string]bool
	servers    map[int]*dynamicPortServer
	cpu        map[string]nodeCPUUsageSample
	resources  map[string]nodeResourceUsageSample
	counters   map[string]guestCPUCounters
	lastErr    string
	lastErrAt  time.Time
	// inventoryErr is the persistent NodeList failure. It is reported
	// separately from lastErr because it degrades every node at once.
	inventoryErr      string
	inventoryErrSince time.Time
	inventoryWarnAt   time.Time
	lastPoll          time.Time
}

func newDynamicForwarder(service *Service, runtime LimaSSHRuntime) *dynamicForwarder {
	return &dynamicForwarder{
		logger: service.log(),

		service: service, factory: limaSSHForwardingPeerFactory{runtime: runtime}, interval: forwardingPollInterval,
		listen: net.Listen, now: func() time.Time { return time.Now().UTC() },
		scanTimeout: forwardingScanTimeout, telemetryTimeout: forwardingTelemetryTimeout,
		dialTimeout: forwardingDialTimeout, routeGrace: forwardingRouteGrace, stopGrace: forwardingStopGrace,
		claimMemory: forwardingClaimMemory, warnInterval: forwardingWarnInterval,
		keepaliveInterval: forwardingKeepaliveInterval, keepaliveTimeout: forwardingKeepaliveTimeout,
		keepaliveTolerance: forwardingKeepaliveTolerance, concurrency: forwardingConcurrency,
		nodes:  map[string]*forwardingNodeState{},
		routes: map[dynamicRouteKey]*dynamicForwardingRoute{}, discovered: map[dynamicRouteKey]routeDiscoveryMemory{},
		known: map[string]bool{}, servers: map[int]*dynamicPortServer{},
		cpu: map[string]nodeCPUUsageSample{}, resources: map[string]nodeResourceUsageSample{},
		counters: map[string]guestCPUCounters{},
	}
}

func durationOrDefault(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func intOrDefault(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func (f *dynamicForwarder) nowUTC() time.Time {
	if f != nil && f.now != nil {
		return f.now().UTC()
	}
	return time.Now().UTC()
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
	if !f.ensurePrepared(ctx) {
		return
	}
	now := f.nowUTC()
	var targets []Node
	nodes, err := f.service.NodeList(ctx, false)
	if err != nil {
		// A failed inventory read is not evidence that any node stopped, so
		// membership is left untouched and the known peers keep working.
		targets = f.retainMembership(err, now)
	} else {
		targets = f.syncMembership(nodes, now)
	}
	f.reconcileTargets(ctx, targets)
	f.reconcileServers()
	f.mu.Lock()
	f.lastPoll = f.nowUTC()
	f.mu.Unlock()
}

func (f *dynamicForwarder) ensurePrepared(ctx context.Context) bool {
	f.mu.RLock()
	prepared := f.prepared
	f.mu.RUnlock()
	if prepared {
		return true
	}
	if err := f.factory.Prepare(ctx); err != nil {
		f.recordError(fmt.Errorf("prepare dynamic forwarding: %w", err))
		return false
	}
	f.mu.Lock()
	f.prepared = true
	f.mu.Unlock()
	return true
}

// retainMembership handles a NodeList failure: nothing is torn down, the
// condition is recorded for the daemon snapshot, and every already-known node
// is still worked so existing routes keep refreshing and lost peers can
// reconnect while the inventory read stays broken.
func (f *dynamicForwarder) retainMembership(err error, now time.Time) []Node {
	f.mu.Lock()
	f.inventoryErr = err.Error()
	if f.inventoryErrSince.IsZero() {
		f.inventoryErrSince = now
	}
	f.lastErr = err.Error()
	f.lastErrAt = now
	warn := now.Sub(f.inventoryWarnAt) >= durationOrDefault(f.warnInterval, forwardingWarnInterval)
	if warn {
		f.inventoryWarnAt = now
	}
	// Every already-known node stays in the work set, including one whose peer
	// was just retired: without an inventory read this is the only path back to
	// a working transport.
	targets := make([]Node, 0, len(f.nodes))
	for _, state := range f.nodes {
		targets = append(targets, state.node)
	}
	f.mu.Unlock()
	if warn {
		f.log().Warn("dynamic forwarding node inventory unavailable; retaining peers and routes",
			"nodes", len(targets), "error", err.Error())
	}
	return sortForwardingTargets(targets)
}

// syncMembership applies a successful inventory read. Peers and routes are
// removed only for a confirmed delete (the node left a successful list) or a
// confirmed stop (a not-running observation that survived the grace window
// without the guest answering).
func (f *dynamicForwarder) syncMembership(nodes []Node, now time.Time) []Node {
	running := map[string]Node{}
	present := map[string]Node{}
	for _, node := range nodes {
		present[node.ID] = node
		if node.LastRuntimeObservation != nil && node.LastRuntimeObservation.Status == ObservationRunning {
			running[node.ID] = node
		}
	}

	stopGrace := durationOrDefault(f.stopGrace, forwardingStopGrace)
	var retired []retiredPeer
	targets := make([]Node, 0, len(present))
	f.mu.Lock()
	f.inventoryErr = ""
	f.inventoryErrSince = time.Time{}
	f.inventoryWarnAt = time.Time{}
	if f.nodes == nil {
		f.nodes = map[string]*forwardingNodeState{}
	}
	for id, state := range f.nodes {
		node, listed := present[id]
		if !listed {
			retired = append(retired, f.retireNodeLocked(id, "node deleted", now))
			continue
		}
		state.node = node
		if _, isRunning := running[id]; isRunning {
			state.notRunningSince = time.Time{}
			continue
		}
		if state.notRunningSince.IsZero() {
			state.notRunningSince = now
		}
		if state.confirmedStopped(now, stopGrace) {
			retired = append(retired, f.retireNodeLocked(id, "node stopped", now))
			continue
		}
		// Unconfirmed: keep probing. A guest that still answers its scan
		// contradicts the observation and holds on to its routes.
		if state.peer != nil {
			targets = append(targets, node)
		}
	}
	for id, node := range running {
		if f.nodes[id] == nil {
			f.nodes[id] = &forwardingNodeState{node: node}
		}
		targets = append(targets, node)
	}
	f.known = map[string]bool{}
	for _, state := range f.nodes {
		f.known[strings.ToLower(state.node.SandboxName)] = true
	}
	f.pruneDiscoveryMemoryLocked(now)
	f.mu.Unlock()

	for _, peer := range retired {
		if peer.empty() {
			continue
		}
		peer.close()
		f.log().Info("dynamic forwarding peer released", "node", peer.node, "reason", peer.reason)
	}
	return sortForwardingTargets(targets)
}

func sortForwardingTargets(targets []Node) []Node {
	sort.Slice(targets, func(i, j int) bool { return targets[i].SandboxName < targets[j].SandboxName })
	return targets
}

// reconcileTargets runs per-node discovery concurrently under a small cap so a
// node that is booting, wedged, or asleep cannot delay any other node.
func (f *dynamicForwarder) reconcileTargets(ctx context.Context, targets []Node) {
	if len(targets) == 0 {
		return
	}
	limit := min(intOrDefault(f.concurrency, forwardingConcurrency), len(targets))
	work := make(chan Node)
	var workers sync.WaitGroup
	workers.Add(limit)
	for range limit {
		go func() {
			defer workers.Done()
			for node := range work {
				f.reconcileNode(ctx, node)
			}
		}()
	}
	for _, node := range targets {
		select {
		case work <- node:
		case <-ctx.Done():
		}
	}
	close(work)
	workers.Wait()
}

func (f *dynamicForwarder) reconcileNode(ctx context.Context, node Node) {
	if ctx.Err() != nil {
		return
	}
	now := f.nowUTC()
	f.mu.Lock()
	state := f.nodes[node.ID]
	if state == nil {
		f.mu.Unlock()
		return
	}
	if !state.backoffUntil.IsZero() && now.Before(state.backoffUntil) {
		f.mu.Unlock()
		return
	}
	peer := state.peer
	dead := state.peer != nil && !state.monitor.alive()
	f.mu.Unlock()

	if dead {
		f.retirePeer(node, "keepalive lost")
		peer = nil
	}
	if peer == nil {
		connected, err := f.factory.Connect(ctx, node)
		if err != nil {
			f.recordNodeFailure(node, fmt.Errorf("connect %s forwarding peer: %w", node.SandboxName, err))
			return
		}
		peer = f.adoptPeer(ctx, node, connected)
	}

	scanCtx, cancelScan := context.WithTimeout(ctx, durationOrDefault(f.scanTimeout, forwardingScanTimeout))
	ports, scanErr := peer.ScanPorts(scanCtx)
	cancelScan()
	if scanErr != nil {
		f.recordNodeFailure(node, fmt.Errorf("scan %s guest listeners: %w", node.SandboxName, scanErr))
		return
	}
	f.applyScanResult(node, peer, ports, f.nowUTC())

	// Telemetry is sampled separately and its failure is contained here: a
	// guest too busy to report CPU still keeps its routes.
	telemetryCtx, cancelTelemetry := context.WithTimeout(ctx, durationOrDefault(f.telemetryTimeout, forwardingTelemetryTimeout))
	telemetry, telemetryErr := peer.SampleTelemetry(telemetryCtx)
	cancelTelemetry()
	if telemetryErr != nil {
		f.clearNodeUsage(node.ID)
		return
	}
	f.recordNodeUsage(node.ID, telemetry, f.nowUTC())
}

// adoptPeer installs a freshly connected peer, starts its keepalive monitor,
// and re-points the node's existing routes at it so a reconnect repairs lapsed
// routes instead of leaving them bound to the dead connection.
func (f *dynamicForwarder) adoptPeer(ctx context.Context, node Node, peer forwardingPeer) forwardingPeer {
	monitor := newPeerHealthMonitor(peer, node.SandboxName,
		durationOrDefault(f.keepaliveInterval, forwardingKeepaliveInterval),
		durationOrDefault(f.keepaliveTimeout, forwardingKeepaliveTimeout),
		intOrDefault(f.keepaliveTolerance, forwardingKeepaliveTolerance), f.logger)
	monitor.start(ctx)
	var replaced retiredPeer
	f.mu.Lock()
	state := f.nodes[node.ID]
	if state == nil {
		f.mu.Unlock()
		monitor.stop()
		_ = peer.Close()
		return peer
	}
	if state.peer != nil {
		replaced = retiredPeer{peer: state.peer, monitor: state.monitor, node: node.SandboxName, reason: "replaced"}
	}
	state.peer = peer
	state.monitor = monitor
	for _, route := range f.routes {
		if route.nodeID == node.ID {
			route.setPeer(peer)
			// Idle connections belong to the replaced transport; reusing one
			// would send the next request back into the dead peer.
			route.close()
		}
	}
	f.mu.Unlock()
	if !replaced.empty() {
		replaced.close()
	}
	f.log().Info("dynamic forwarding peer connected", "node", node.SandboxName)
	return peer
}

// retirePeer drops a node's transport without touching its routes. The routes
// stay bound and lapse only through the ordinary grace window.
func (f *dynamicForwarder) retirePeer(node Node, reason string) {
	f.mu.Lock()
	state := f.nodes[node.ID]
	if state == nil || state.peer == nil {
		f.mu.Unlock()
		return
	}
	retired := retiredPeer{peer: state.peer, monitor: state.monitor, node: node.SandboxName, reason: reason}
	state.peer = nil
	state.monitor = nil
	f.mu.Unlock()
	retired.close()
	f.log().Info("dynamic forwarding peer retired", "node", node.SandboxName, "reason", reason)
}

// recordNodeFailure applies one connect or scan failure. Routes survive the
// grace window, then lapse into retryable 502s; they are never removed here.
func (f *dynamicForwarder) recordNodeFailure(node Node, err error) {
	now := f.nowUTC()
	grace := durationOrDefault(f.routeGrace, forwardingRouteGrace)
	f.mu.Lock()
	state := f.nodes[node.ID]
	if state == nil {
		f.mu.Unlock()
		return
	}
	state.failures++
	if state.firstFailureAt.IsZero() {
		state.firstFailureAt = now
	}
	state.lastErr = err.Error()
	state.backoff = nextForwardingBackoff(state.backoff)
	state.backoffUntil = now.Add(state.backoff)
	f.lastErr = err.Error()
	f.lastErrAt = now
	// Telemetry is dropped immediately; stale CPU numbers are worse than none.
	delete(f.cpu, node.ID)
	delete(f.resources, node.ID)
	delete(f.counters, node.ID)

	lapse := !state.routesLapsed && state.failures >= 2 && now.Sub(state.firstFailureAt) >= grace
	var lapsed []string
	var retired retiredPeer
	if lapse {
		state.routesLapsed = true
		lapsed = f.lapseNodeRoutesLocked(node.ID, now)
		if state.peer != nil {
			retired = retiredPeer{peer: state.peer, monitor: state.monitor, node: node.SandboxName, reason: "discovery grace elapsed"}
			state.peer = nil
			state.monitor = nil
		}
	}
	failures := state.failures
	retryAfter := state.backoffUntil
	warn := state.lastWarnAt.IsZero() || now.Sub(state.lastWarnAt) >= durationOrDefault(f.warnInterval, forwardingWarnInterval)
	if warn {
		state.lastWarnAt = now
	}
	f.mu.Unlock()

	if !retired.empty() {
		retired.close()
	}
	for _, route := range lapsed {
		f.log().Warn("dynamic forwarding route lapsed; host listener still answering 502", "url", route)
	}
	if warn {
		f.log().Warn("dynamic forwarding node discovery failed", "node", node.SandboxName,
			"failures", failures, "retry_after", retryAfter, "error", err.Error())
	}
}

func nextForwardingBackoff(current time.Duration) time.Duration {
	if current <= 0 {
		return forwardingBackoffInitial
	}
	return min(current*2, forwardingBackoffMaximum)
}

func (f *dynamicForwarder) recordNodeUsage(nodeID string, observation forwardingGuestObservation, sampledAt time.Time) {
	f.mu.Lock()
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

	broadcast, event := f.broadcast, f.nodeUsageEventLocked(nodeID, sampledAt)
	f.mu.Unlock()
	publishNodeUsage(broadcast, event)
}

// clearNodeUsage drops a node's telemetry without touching its peer or routes.
// The drop is published like a sample: a subscriber holding the previous
// numbers must stop showing them, and "no usage as of now" is the only thing
// that says so.
func (f *dynamicForwarder) clearNodeUsage(nodeID string) {
	f.mu.Lock()
	delete(f.cpu, nodeID)
	delete(f.resources, nodeID)
	delete(f.counters, nodeID)
	broadcast, event := f.broadcast, f.nodeUsageEventLocked(nodeID, f.nowUTC())
	f.mu.Unlock()
	publishNodeUsage(broadcast, event)
}

// nodeUsageEventLocked renders a node's current usage exactly as addNodeUsage
// would merge it into a node.list reply, so a pushed sample and a polled one
// describe the same thing and are comparable by SampledAt. Callers hold f.mu.
func (f *dynamicForwarder) nodeUsageEventLocked(nodeID string, sampledAt time.Time) nodeUsageEvent {
	event := nodeUsageEvent{NodeID: nodeID, SampledAt: sampledAt}
	if sample, ok := f.cpu[nodeID]; ok {
		percent := sample.Percent
		event.CPUUsagePercent = &percent
	}
	if sample, ok := f.resources[nodeID]; ok {
		if validGuestResourceUsage(sample.Memory) {
			used, total := sample.Memory.UsedBytes, sample.Memory.TotalBytes
			event.MemoryUsedBytes, event.MemoryTotalBytes = &used, &total
		}
		if validGuestResourceUsage(sample.Disk) {
			used, total := sample.Disk.UsedBytes, sample.Disk.TotalBytes
			event.DiskUsedBytes, event.DiskTotalBytes = &used, &total
		}
	}
	return event
}

// publishNodeUsage runs outside f.mu on purpose: the broadcast walks the
// server's subscriber set and marshals the payload, and the per-node reconcile
// fan-out must not hold the forwarder's lock across that.
func publishNodeUsage(broadcast func(string, any), event nodeUsageEvent) {
	if broadcast == nil {
		return
	}
	broadcast(daemon.EventNodeUsageChanged, event)
}

// setBroadcast installs the daemon's gated event publisher. The forwarder never
// learns about the server: the host hands it the same closure it uses itself
// (daemonHost.wireServerLinks), which suppresses events until the server is
// actually serving.
func (f *dynamicForwarder) setBroadcast(broadcast func(string, any)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broadcast = broadcast
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

// applyScanResult installs the authoritative result of a successful listener
// scan: new ports gain routes, surviving ports are refreshed and un-lapsed, and
// ports the guest genuinely stopped listening on are removed.
func (f *dynamicForwarder) applyScanResult(node Node, peer forwardingPeer, ports []int, now time.Time) {
	wanted := map[dynamicRouteKey]bool{}
	var added, removed, restored []string
	f.mu.Lock()
	if state := f.nodes[node.ID]; state != nil {
		state.noteHealthy(now)
	}
	for _, port := range ports {
		key := dynamicRouteKey{node: strings.ToLower(node.SandboxName), port: port}
		wanted[key] = true
		if route := f.routes[key]; route != nil {
			route.seenAt = now
			route.setPeer(peer)
			if route.lapsed {
				route.lapsed = false
				route.lapsedAt = time.Time{}
				restored = append(restored, route.url())
			}
			continue
		}
		route := newDynamicForwardingRoute(node, port, peer, f.discoveryTimeLocked(key, now), f.logger)
		route.seenAt = now
		route.dialTimeout = durationOrDefault(f.dialTimeout, forwardingDialTimeout)
		f.routes[key] = route
		added = append(added, route.url())
	}
	for key, route := range f.routes {
		if route.nodeID == node.ID && !wanted[key] {
			f.removeRouteLocked(key, route, now)
			removed = append(removed, route.url())
		}
	}
	f.mu.Unlock()

	for _, url := range added {
		f.log().Info("dynamic forwarding route added", "url", url)
	}
	for _, url := range restored {
		f.log().Info("dynamic forwarding route restored", "url", url)
	}
	for _, url := range removed {
		f.log().Info("dynamic forwarding route removed", "url", url)
	}
}

// discoveryTimeLocked returns the discovery timestamp a new route should carry.
// A route that was removed recently keeps its original timestamp so the generic
// claim returns to its previous owner rather than migrating permanently. The
// Codex callback port is excluded: ADR 119 makes it follow the newest listener.
func (f *dynamicForwarder) discoveryTimeLocked(key dynamicRouteKey, now time.Time) time.Time {
	if key.port == codexLoginCallbackPort {
		return now
	}
	memory, ok := f.discovered[key]
	if !ok || memory.removedAt.IsZero() || now.Sub(memory.removedAt) > durationOrDefault(f.claimMemory, forwardingClaimMemory) {
		return now
	}
	return memory.discoveredAt
}

func (f *dynamicForwarder) removeRouteLocked(key dynamicRouteKey, route *dynamicForwardingRoute, now time.Time) {
	route.close()
	if f.discovered == nil {
		f.discovered = map[dynamicRouteKey]routeDiscoveryMemory{}
	}
	f.discovered[key] = routeDiscoveryMemory{discoveredAt: route.discoveredAt, removedAt: now}
	delete(f.routes, key)
}

func (f *dynamicForwarder) pruneDiscoveryMemoryLocked(now time.Time) {
	window := durationOrDefault(f.claimMemory, forwardingClaimMemory)
	for key, memory := range f.discovered {
		if !memory.removedAt.IsZero() && now.Sub(memory.removedAt) > window {
			delete(f.discovered, key)
		}
	}
}

// lapseNodeRoutesLocked degrades a node's routes instead of deleting them: the
// port keeps its host listener and answers 502 until the peer recovers.
func (f *dynamicForwarder) lapseNodeRoutesLocked(nodeID string, now time.Time) []string {
	var lapsed []string
	for _, route := range f.routes {
		if route.nodeID != nodeID || route.lapsed {
			continue
		}
		route.lapsed = true
		route.lapsedAt = now
		route.close()
		lapsed = append(lapsed, route.url())
	}
	sort.Strings(lapsed)
	return lapsed
}

// retireNodeLocked is the only path that removes a node's routes, reached only
// from a confirmed stop or delete.
func (f *dynamicForwarder) retireNodeLocked(nodeID, reason string, now time.Time) retiredPeer {
	state := f.nodes[nodeID]
	delete(f.nodes, nodeID)
	delete(f.cpu, nodeID)
	delete(f.resources, nodeID)
	delete(f.counters, nodeID)
	f.removeNodeRoutesLocked(nodeID, now)
	if state == nil {
		return retiredPeer{}
	}
	return retiredPeer{peer: state.peer, monitor: state.monitor, node: state.node.SandboxName, reason: reason}
}

func (f *dynamicForwarder) removeNodeRoutesLocked(nodeID string, now time.Time) {
	for key, route := range f.routes {
		if route.nodeID == nodeID {
			f.removeRouteLocked(key, route, now)
		}
	}
}

func (f *dynamicForwarder) reconcileServers() {
	f.mu.Lock()
	var closing []*http.Server
	wanted := map[int]bool{}
	// Lapsed routes still count: keeping the listener bound is what turns a
	// transient failure into a retryable 502 instead of a connection refusal
	// that clients cache.
	for key := range f.routes {
		wanted[key.port] = true
	}
	for port, server := range f.servers {
		if wanted[port] && server.status == "serving" {
			preferredNode := f.preferredClaimantLocked(port)
			if server.defaultNode != preferredNode {
				server.defaultNode = preferredNode
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
			f.lastErrAt = f.nowUTC()
			continue
		}
		server := &http.Server{Handler: &dynamicForwardingHandler{forwarder: f, port: port}, ReadHeaderTimeout: 10 * time.Second}
		defaultNode := f.preferredClaimantLocked(port)
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

func (f *dynamicForwarder) preferredClaimantLocked(port int) string {
	newest := port == codexLoginCallbackPort
	if node := f.selectClaimantLocked(port, newest, false); node != "" {
		return node
	}
	// Every route on this port is lapsed. Keep the generic host pointed at one
	// of them so it answers 502 rather than "unknown codelima node".
	return f.selectClaimantLocked(port, newest, true)
}

func (f *dynamicForwarder) selectClaimantLocked(port int, newest, includeLapsed bool) string {
	var selected *dynamicForwardingRoute
	for key, route := range f.routes {
		if key.port != port || (route.lapsed && !includeLapsed) {
			continue
		}
		betterTime := route.discoveredAt.Before
		if newest {
			betterTime = route.discoveredAt.After
		}
		if selected == nil || betterTime(selected.discoveredAt) ||
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
	f.lastErrAt = f.nowUTC()
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
		http.Error(writer, "request host must be localhost, a loopback address, or {node}.localhost", http.StatusMisdirectedRequest)
		return
	}
	route := h.forwarder.routes[dynamicRouteKey{node: nodeName, port: h.port}]
	lapsed := route != nil && route.lapsed
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
	if lapsed {
		// The route is degraded, not gone: answer a retryable status instead of
		// closing the listener, which clients cache as connection refused.
		http.Error(writer, "codelima is reconnecting to the node service", http.StatusBadGateway)
		return
	}
	route.proxy.ServeHTTP(writer, request)
}

// isGenericForwardingHost reports whether a request Host names the local
// machine generically. Every loopback literal qualifies, including the IPv6
// literal the forwarder itself binds.
func isGenericForwardingHost(hostport string) bool {
	host, ok := forwardingHostname(hostport)
	if !ok {
		return false
	}
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
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
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		// A bracketed IPv6 literal without a port, as sent for the default port.
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
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
	for key, route := range f.routes {
		state := "serving"
		if route.lapsed {
			state = "lapsed"
		}
		entry := map[string]any{
			"node": key.node, "port": key.port, "url": route.url(), "state": state,
			"discovered_at": route.discoveredAt, "last_seen_at": route.seenAt,
		}
		if route.lapsed {
			entry["lapsed_at"] = route.lapsedAt
		}
		routes = append(routes, entry)
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

	connected := 0
	nodes := make([]map[string]any, 0, len(f.nodes))
	for _, state := range f.nodes {
		if state.peer != nil {
			connected++
		}
		entry := map[string]any{
			"node": state.node.SandboxName, "connected": state.peer != nil,
			"health": state.health(), "failures": state.failures,
		}
		if !state.lastHealthyAt.IsZero() {
			entry["last_healthy_at"] = state.lastHealthyAt
		}
		if !state.notRunningSince.IsZero() {
			entry["not_running_since"] = state.notRunningSince
		}
		if !state.backoffUntil.IsZero() {
			entry["retry_after"] = state.backoffUntil
		}
		if state.lastErr != "" {
			entry["last_error"] = state.lastErr
		} else if failure := state.monitor.failure(); failure != "" {
			entry["last_error"] = failure
		}
		nodes = append(nodes, entry)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i]["node"].(string) < nodes[j]["node"].(string) })

	snapshot := map[string]any{
		"enabled": true, "authorized": f.prepared, "routes": routes, "ports": ports,
		"nodes": nodes, "peers": connected, "last_error": f.lastErr, "last_poll_at": f.lastPoll,
		"degraded": f.inventoryErr != "" || anyLapsedRoute(f.routes),
	}
	if !f.lastErrAt.IsZero() {
		snapshot["last_error_at"] = f.lastErrAt
	}
	if f.inventoryErr != "" {
		snapshot["node_list_error"] = f.inventoryErr
		snapshot["node_list_error_since"] = f.inventoryErrSince
	}
	return snapshot
}

func anyLapsedRoute(routes map[dynamicRouteKey]*dynamicForwardingRoute) bool {
	for _, route := range routes {
		if route.lapsed {
			return true
		}
	}
	return false
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
	retired := make([]retiredPeer, 0, len(f.nodes))
	for _, state := range f.nodes {
		if state.peer != nil || state.monitor != nil {
			retired = append(retired, retiredPeer{peer: state.peer, monitor: state.monitor, node: state.node.SandboxName, reason: "shutdown"})
		}
	}
	f.servers = map[int]*dynamicPortServer{}
	f.nodes = map[string]*forwardingNodeState{}
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
	for _, peer := range retired {
		peer.monitor.stop()
		if peer.peer != nil {
			closeErr = errors.Join(closeErr, peer.peer.Close())
		}
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
