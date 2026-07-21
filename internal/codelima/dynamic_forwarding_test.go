package codelima

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseProcNetTCPSelectsUnprivilegedLoopbackAndWildcardListeners(t *testing.T) {
	t.Parallel()
	input := []byte(`  sl  local_address rem_address   st
   0: 0100007F:1F90 00000000:0000 0A
   1: 00000000:0BB8 00000000:0000 0A
   2: 0100007F:0016 00000000:0000 0A
   3: 0200007F:2382 00000000:0000 0A
   4: 0100007F:1F90 00000000:0000 0A
   5: 0100007F:2383 00000000:0000 01
malformed
   0: 00000000000000000000000000000000:1451 00000000000000000000000000000000:0000 0A
   1: 00000000000000000000000001000000:2384 00000000000000000000000000000000:0000 0A
`)
	want := []int{3000, 5201, 8080, 9092}
	if got := parseProcNetTCP(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseProcNetTCP() = %v, want %v", got, want)
	}
}

func TestNodeNameFromLocalhost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		host string
		want string
		ok   bool
	}{
		{host: "test-node.localhost:8080", want: "test-node", ok: true},
		{host: "TEST-NODE.LOCALHOST.:8080", want: "test-node", ok: true},
		{host: "test-node.localhost", want: "test-node", ok: true},
		{host: "localhost:8080"},
		{host: "test-node.local:8080"},
		{host: "nested.test-node.localhost:8080"},
		{host: "test-node.localhost:bad"},
	}
	for _, test := range tests {
		t.Run(test.host, func(t *testing.T) {
			got, ok := nodeNameFromLocalhost(test.host)
			if got != test.want || ok != test.ok {
				t.Fatalf("nodeNameFromLocalhost(%q) = %q, %v; want %q, %v", test.host, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestIsGenericForwardingHost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		host string
		want bool
	}{
		{host: "localhost:8080", want: true},
		{host: "LOCALHOST.:8080", want: true},
		{host: "localhost", want: true},
		{host: "127.0.0.1:8080", want: true},
		{host: "127.0.0.1", want: true},
		{host: "test-node.localhost:8080"},
		{host: "127.0.0.2:8080"},
		{host: "localhost:bad"},
		{host: "127.0.0.1:bad"},
		{host: "other:8080"},
	}
	for _, test := range tests {
		t.Run(test.host, func(t *testing.T) {
			if got := isGenericForwardingHost(test.host); got != test.want {
				t.Fatalf("isGenericForwardingHost(%q) = %v, want %v", test.host, got, test.want)
			}
		})
	}
}

func TestDynamicForwardingHandlerRoutesSamePortByNodeHost(t *testing.T) {
	t.Parallel()
	upstreams := map[string]*httptest.Server{}
	for _, node := range []string{"one", "two"} {
		upstreams[node] = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("X-Original-Host", request.Host)
			_, _ = writer.Write([]byte(node))
		}))
		defer upstreams[node].Close()
	}
	forwarder := &dynamicForwarder{
		routes: map[dynamicRouteKey]*dynamicForwardingRoute{},
		known:  map[string]bool{"one": true, "two": true, "idle": true},
	}
	for _, node := range []string{"one", "two"} {
		address := strings.TrimPrefix(upstreams[node].URL, "http://")
		peer := &dialAddressPeer{address: address}
		metadata := Node{ID: node, SandboxName: node}
		forwarder.routes[dynamicRouteKey{node: node, port: 8080}] = newDynamicForwardingRoute(metadata, 8080, peer, time.Now(), nil)
	}
	handler := &dynamicForwardingHandler{forwarder: forwarder, port: 8080}

	for _, node := range []string{"one", "two"} {
		request := httptest.NewRequest(http.MethodGet, "http://ignored/path", nil)
		request.Host = node + ".localhost:8080"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != node {
			t.Fatalf("request for %s returned %d %q", node, response.Code, response.Body.String())
		}
		if got := response.Header().Get("X-Original-Host"); got != node+".localhost:8080" {
			t.Fatalf("original Host = %q", got)
		}
	}

	assertForwardingStatus(t, handler, "other.localhost:8080", http.StatusMisdirectedRequest)
	assertForwardingStatus(t, handler, "idle.localhost:8080", http.StatusNotFound)
	assertForwardingStatus(t, handler, "one.local:8080", http.StatusMisdirectedRequest)
}

func TestDynamicForwardingHandlerRoutesGenericLocalhostToFirstActiveClaim(t *testing.T) {
	t.Parallel()
	port := reserveTCPPort(t)
	upstreams := map[string]*httptest.Server{}
	for _, node := range []string{"first", "second"} {
		upstreams[node] = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("X-Original-Host", request.Host)
			_, _ = writer.Write([]byte(node))
		}))
		defer upstreams[node].Close()
	}

	firstSeen := time.Now().UTC()
	forwarder := &dynamicForwarder{
		listen:   net.Listen,
		peers:    map[string]forwardingPeer{},
		routes:   map[dynamicRouteKey]*dynamicForwardingRoute{},
		known:    map[string]bool{"first": true, "second": true},
		servers:  map[int]*dynamicPortServer{},
		failures: map[string]int{},
	}
	for offset, node := range []string{"first", "second"} {
		peer := &dialAddressPeer{address: strings.TrimPrefix(upstreams[node].URL, "http://")}
		metadata := Node{ID: node, SandboxName: node}
		key := dynamicRouteKey{node: node, port: port}
		forwarder.routes[key] = newDynamicForwardingRoute(metadata, port, peer, firstSeen.Add(time.Duration(offset)*time.Millisecond), nil)
	}
	forwarder.reconcileServers()
	defer func() { _ = forwarder.Close() }()

	handler := &dynamicForwardingHandler{forwarder: forwarder, port: port}
	assertForwardingResponse(t, handler, "localhost:"+strconv.Itoa(port), "first")
	assertForwardingResponse(t, handler, "127.0.0.1:"+strconv.Itoa(port), "first")
	assertForwardingResponse(t, handler, "second.localhost:"+strconv.Itoa(port), "second")

	forwarder.mu.Lock()
	firstKey := dynamicRouteKey{node: "first", port: port}
	forwarder.routes[firstKey].close()
	delete(forwarder.routes, firstKey)
	forwarder.mu.Unlock()
	forwarder.reconcileServers()

	assertForwardingResponse(t, handler, "localhost:"+strconv.Itoa(port), "second")
	assertForwardingResponse(t, handler, "127.0.0.1:"+strconv.Itoa(port), "second")
	ports := forwarder.Snapshot()["ports"].([]map[string]any)
	if len(ports) != 1 || ports[0]["default_node"] != "second" {
		t.Fatalf("port snapshot = %#v, want second as the generic localhost claimant", ports)
	}
}

func TestDynamicForwardingHandlerReturnsBadGatewayWhenTunnelFails(t *testing.T) {
	t.Parallel()
	forwarder := &dynamicForwarder{routes: map[dynamicRouteKey]*dynamicForwardingRoute{}, known: map[string]bool{"node": true}}
	forwarder.routes[dynamicRouteKey{node: "node", port: 8080}] = newDynamicForwardingRoute(
		Node{ID: "node", SandboxName: "node"}, 8080, failingForwardingPeer{}, time.Now(), nil,
	)
	assertForwardingStatus(t, &dynamicForwardingHandler{forwarder: forwarder, port: 8080}, "node.localhost:8080", http.StatusBadGateway)
}

func TestDynamicForwardingRouteFallsBackToIPv6GuestLoopback(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Original-Host", request.Host)
		_, _ = writer.Write([]byte("ipv6-loopback"))
	}))
	defer upstream.Close()

	peer := &ipv6OnlyForwardingPeer{upstreamAddress: strings.TrimPrefix(upstream.URL, "http://")}
	forwarder := &dynamicForwarder{
		routes:  map[dynamicRouteKey]*dynamicForwardingRoute{},
		known:   map[string]bool{"node": true},
		servers: map[int]*dynamicPortServer{5173: {port: 5173, defaultNode: "node", status: "serving"}},
	}
	forwarder.routes[dynamicRouteKey{node: "node", port: 5173}] = newDynamicForwardingRoute(
		Node{ID: "node", SandboxName: "node"}, 5173, peer, time.Now(), nil,
	)

	assertForwardingResponse(t, &dynamicForwardingHandler{forwarder: forwarder, port: 5173}, "node.localhost:5173", "ipv6-loopback")
	assertForwardingResponse(t, &dynamicForwardingHandler{forwarder: forwarder, port: 5173}, "localhost:5173", "ipv6-loopback")
	wantAttempts := []string{"127.0.0.1:5173", "[::1]:5173"}
	if got := peer.snapshotAttempts(); !reflect.DeepEqual(got, wantAttempts) {
		t.Fatalf("guest dial attempts = %v, want %v", got, wantAttempts)
	}
}

func TestDynamicForwardingHandlerPassesHTTPUpgrade(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.EqualFold(request.Header.Get("Connection"), "upgrade") || request.Header.Get("Upgrade") != "test" {
			http.Error(writer, "missing upgrade", http.StatusBadRequest)
			return
		}
		hijacker := writer.(http.Hijacker)
		connection, buffer, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		_, _ = buffer.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\nupgraded")
		_ = buffer.Flush()
	}))
	defer upstream.Close()
	peer := &dialAddressPeer{address: strings.TrimPrefix(upstream.URL, "http://")}
	forwarder := &dynamicForwarder{routes: map[dynamicRouteKey]*dynamicForwardingRoute{}, known: map[string]bool{"node": true}}
	forwarder.routes[dynamicRouteKey{node: "node", port: 8080}] = newDynamicForwardingRoute(Node{ID: "node", SandboxName: "node"}, 8080, peer, time.Now(), nil)
	server := httptest.NewServer(&dynamicForwardingHandler{forwarder: forwarder, port: 8080})
	defer server.Close()

	connection, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = connection.Close() }()
	_, _ = fmt.Fprintf(connection, "GET /socket HTTP/1.1\r\nHost: node.localhost:8080\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatalf("ReadResponse() error = %v", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d", response.StatusCode)
	}
}

func TestDynamicForwarderReconcilesRoutesListenersAndStoppedNodes(t *testing.T) {
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "test-node", "running")
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("from-node"))
	}))
	defer upstream.Close()
	port := reserveTCPPort(t)
	peer := &controllableForwardingPeer{ports: []int{port}, address: strings.TrimPrefix(upstream.URL, "http://")}
	factory := &fakeForwardingPeerFactory{peers: map[string]*controllableForwardingPeer{node.SandboxName: peer}}
	forwarder := newTestDynamicForwarder(service, factory)
	forwarder.reconcile(context.Background())
	defer func() { _ = forwarder.Close() }()

	request, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("forwarded request error = %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	buffer, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response error = %v", err)
	}
	if got := string(buffer); got != "from-node" {
		t.Fatalf("forwarded body = %q", got)
	}

	peer.mu.Lock()
	peer.ports = nil
	peer.mu.Unlock()
	forwarder.reconcile(context.Background())
	if connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond); err == nil {
		_ = connection.Close()
		t.Fatal("host listener remained after guest listener disappeared")
	}

	fake := service.sandbox.(*fakeSandbox)
	fake.mu.Lock()
	fake.observations[node.SandboxName] = RuntimeObservation{Name: node.SandboxName, Exists: true, Status: "stopped"}
	fake.mu.Unlock()
	forwarder.reconcile(context.Background())
	peer.mu.Lock()
	closed := peer.closed
	peer.mu.Unlock()
	if !closed {
		t.Fatal("forwarding peer remained open after node stopped")
	}
}

func TestDynamicForwarderRecoversFromHostBindConflict(t *testing.T) {
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "conflict-node", "running")
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = occupied.Close() })
	port := occupied.Addr().(*net.TCPAddr).Port
	peer := &controllableForwardingPeer{ports: []int{port}}
	forwarder := newTestDynamicForwarder(service, &fakeForwardingPeerFactory{peers: map[string]*controllableForwardingPeer{node.SandboxName: peer}})
	forwarder.interval = 5 * time.Millisecond
	if err := forwarder.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = forwarder.Close() }()
	waitForForwardingPortStatus(t, forwarder, port, "conflicted")
	conflictedPorts := forwarder.Snapshot()["ports"].([]map[string]any)
	if len(conflictedPorts) != 1 || conflictedPorts[0]["default_node"] != "" {
		t.Fatalf("conflicted port snapshot = %#v, want no claimant before a successful host bind", conflictedPorts)
	}
	if err := occupied.Close(); err != nil {
		t.Fatal(err)
	}
	waitForForwardingPortStatus(t, forwarder, port, "serving")
	ports := forwarder.Snapshot()["ports"].([]map[string]any)
	if len(ports) != 1 || ports[0]["default_node"] != node.SandboxName {
		t.Fatalf("port snapshot after retry = %#v, want %s as the generic localhost claimant", ports, node.SandboxName)
	}
}

func TestDynamicForwarderRetriesTransportPreparationWithoutBlockingDaemon(t *testing.T) {
	service, _ := newTestService(t)
	factory := &retryingForwardingPeerFactory{}
	forwarder := newTestDynamicForwarder(service, factory)
	forwarder.interval = 5 * time.Millisecond
	if err := forwarder.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = forwarder.Close() }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if forwarder.Snapshot()["authorized"].(bool) {
			factory.mu.Lock()
			calls := factory.calls
			factory.mu.Unlock()
			if calls < 2 {
				t.Fatalf("Prepare() calls = %d, want retry", calls)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("forwarder did not recover after authorization retry")
}

func saveForwardingTestNode(t *testing.T, service *Service, sandboxName string, status ObservationStatus) Node {
	t.Helper()
	node := Node{ID: newID(), Slug: sandboxName, DirectoryPath: t.TempDir(), SandboxName: sandboxName, Status: "created", LifecycleState: "created", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := service.store.SaveNode(node, BootstrapState{}); err != nil {
		t.Fatalf("SaveNode() error = %v", err)
	}
	fake := service.sandbox.(*fakeSandbox)
	fake.mu.Lock()
	fake.observations[sandboxName] = RuntimeObservation{Name: sandboxName, Exists: true, Status: status}
	fake.mu.Unlock()
	return node
}

func reserveTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func newTestDynamicForwarder(service *Service, factory forwardingPeerFactory) *dynamicForwarder {
	return &dynamicForwarder{
		service: service, factory: factory, interval: time.Hour, listen: net.Listen,
		peers: map[string]forwardingPeer{}, failures: map[string]int{}, routes: map[dynamicRouteKey]*dynamicForwardingRoute{},
		known: map[string]bool{}, servers: map[int]*dynamicPortServer{},
	}
}

type fakeForwardingPeerFactory struct {
	peers map[string]*controllableForwardingPeer
}

type retryingForwardingPeerFactory struct {
	mu    sync.Mutex
	calls int
}

func (f *retryingForwardingPeerFactory) Prepare(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls == 1 {
		return fmt.Errorf("temporary authorization failure")
	}
	return nil
}
func (*retryingForwardingPeerFactory) Connect(context.Context, Node) (forwardingPeer, error) {
	return nil, fmt.Errorf("unexpected Connect call")
}

func (*fakeForwardingPeerFactory) Prepare(context.Context) error { return nil }
func (f *fakeForwardingPeerFactory) Connect(_ context.Context, node Node) (forwardingPeer, error) {
	peer := f.peers[node.SandboxName]
	if peer == nil {
		return nil, fmt.Errorf("no peer for %s", node.SandboxName)
	}
	return peer, nil
}

type controllableForwardingPeer struct {
	mu      sync.Mutex
	ports   []int
	address string
	closed  bool
}

func (p *controllableForwardingPeer) Discover(context.Context) ([]int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.ports), nil
}
func (p *controllableForwardingPeer) DialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, p.address)
}
func (p *controllableForwardingPeer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}

func assertForwardingStatus(t *testing.T, handler http.Handler, host string, want int) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://ignored/", nil)
	request.Host = host
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("host %q returned %d, want %d", host, response.Code, want)
	}
}

func assertForwardingResponse(t *testing.T, handler http.Handler, host, wantBody string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://ignored/", nil)
	request.Host = host
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != wantBody {
		t.Fatalf("host %q returned %d %q, want 200 %q", host, response.Code, response.Body.String(), wantBody)
	}
	if got := response.Header().Get("X-Original-Host"); got != host {
		t.Fatalf("upstream Host = %q, want %q", got, host)
	}
}

func waitForForwardingPortStatus(t *testing.T, forwarder *dynamicForwarder, port int, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ports := forwarder.Snapshot()["ports"].([]map[string]any)
		for _, candidate := range ports {
			if candidate["port"] == port && candidate["status"] == want {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("port %d did not reach forwarding status %q; snapshot=%#v", port, want, forwarder.Snapshot())
}

type dialAddressPeer struct{ address string }

func (*dialAddressPeer) Discover(context.Context) ([]int, error) { return nil, nil }
func (p *dialAddressPeer) DialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, p.address)
}
func (*dialAddressPeer) Close() error { return nil }

type failingForwardingPeer struct{}

func (failingForwardingPeer) Discover(context.Context) ([]int, error) { return nil, nil }
func (failingForwardingPeer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, fmt.Errorf("injected tunnel failure")
}
func (failingForwardingPeer) Close() error { return nil }

type ipv6OnlyForwardingPeer struct {
	mu              sync.Mutex
	upstreamAddress string
	attempts        []string
}

func (*ipv6OnlyForwardingPeer) Discover(context.Context) ([]int, error) { return nil, nil }
func (p *ipv6OnlyForwardingPeer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	p.mu.Lock()
	p.attempts = append(p.attempts, address)
	p.mu.Unlock()
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if host != "::1" {
		return nil, fmt.Errorf("guest IPv4 loopback is not listening")
	}
	return (&net.Dialer{}).DialContext(ctx, network, p.upstreamAddress)
}
func (*ipv6OnlyForwardingPeer) Close() error { return nil }
func (p *ipv6OnlyForwardingPeer) snapshotAttempts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.attempts)
}
