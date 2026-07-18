package codelima

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
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

func TestDynamicForwardingHandlerRoutesSamePortByNodeHost(t *testing.T) {
	t.Parallel()
	upstreams := map[string]*httptest.Server{}
	for _, node := range []string{"one", "two"} {
		node := node
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
		forwarder.routes[dynamicRouteKey{node: node, port: 8080}] = newDynamicForwardingRoute(metadata, 8080, peer, time.Now())
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

func TestDynamicForwardingHandlerReturnsBadGatewayWhenTunnelFails(t *testing.T) {
	t.Parallel()
	forwarder := &dynamicForwarder{routes: map[dynamicRouteKey]*dynamicForwardingRoute{}, known: map[string]bool{"node": true}}
	forwarder.routes[dynamicRouteKey{node: "node", port: 8080}] = newDynamicForwardingRoute(
		Node{ID: "node", SandboxName: "node"}, 8080, failingForwardingPeer{}, time.Now(),
	)
	assertForwardingStatus(t, &dynamicForwardingHandler{forwarder: forwarder, port: 8080}, "node.localhost:8080", http.StatusBadGateway)
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
	forwarder.routes[dynamicRouteKey{node: "node", port: 8080}] = newDynamicForwardingRoute(Node{ID: "node", SandboxName: "node"}, 8080, peer, time.Now())
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

func TestLoadOrCreateForwardingSignerIsIdempotentAndPrivate(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	first, publicPath, err := loadOrCreateForwardingSigner(home)
	if err != nil {
		t.Fatalf("loadOrCreateForwardingSigner() error = %v", err)
	}
	second, secondPublicPath, err := loadOrCreateForwardingSigner(home)
	if err != nil {
		t.Fatalf("second loadOrCreateForwardingSigner() error = %v", err)
	}
	if !reflect.DeepEqual(first.PublicKey().Marshal(), second.PublicKey().Marshal()) || publicPath != secondPublicPath {
		t.Fatal("expected forwarding key to be reused")
	}
	assertFileMode(t, filepath.Dir(publicPath), 0o700)
	assertFileMode(t, strings.TrimSuffix(publicPath, ".pub"), 0o600)
	assertFileMode(t, publicPath, 0o644)
	if err := os.Chmod(publicPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadOrCreateForwardingSigner(home); err != nil {
		t.Fatalf("mode-repair loadOrCreateForwardingSigner() error = %v", err)
	}
	assertFileMode(t, publicPath, 0o644)
}

func TestDynamicForwarderReconcilesRoutesListenersAndStoppedNodes(t *testing.T) {
	service, _ := newTestService(t)
	project := saveForwardingTestNode(t, service, "test-node", "running")
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("from-node"))
	}))
	defer upstream.Close()
	port := reserveTCPPort(t)
	peer := &controllableForwardingPeer{ports: []int{port}, address: strings.TrimPrefix(upstream.URL, "http://")}
	factory := &fakeForwardingPeerFactory{peers: map[string]*controllableForwardingPeer{project.SandboxName: peer}}
	forwarder := newTestDynamicForwarder(service, factory)
	forwarder.reconcile(context.Background())
	defer func() { _ = forwarder.Close() }()

	request, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "test-node.localhost:" + strconv.Itoa(port)
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
	fake.observations[project.SandboxName] = RuntimeObservation{Name: project.SandboxName, Exists: true, Status: "stopped"}
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
	port := occupied.Addr().(*net.TCPAddr).Port
	peer := &controllableForwardingPeer{ports: []int{port}}
	forwarder := newTestDynamicForwarder(service, &fakeForwardingPeerFactory{peers: map[string]*controllableForwardingPeer{node.SandboxName: peer}})
	defer func() { _ = forwarder.Close() }()
	forwarder.reconcile(context.Background())
	if got := forwarder.Snapshot()["ports"].([]map[string]any)[0]["status"]; got != "conflicted" {
		t.Fatalf("listener status = %v, want conflicted", got)
	}
	if err := occupied.Close(); err != nil {
		t.Fatal(err)
	}
	forwarder.reconcile(context.Background())
	if got := forwarder.Snapshot()["ports"].([]map[string]any)[0]["status"]; got != "serving" {
		t.Fatalf("listener status after retry = %v, want serving", got)
	}
}

func TestDynamicForwarderRetriesKeyAuthorizationWithoutBlockingDaemon(t *testing.T) {
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

func saveForwardingTestNode(t *testing.T, service *Service, sandboxName, status string) Node {
	t.Helper()
	project := Project{ID: newID(), Slug: "forwarding-project", WorkspacePath: t.TempDir(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := service.store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject() error = %v", err)
	}
	node := Node{ID: newID(), Slug: sandboxName, ProjectID: project.ID, SandboxName: sandboxName, Status: "created", LifecycleState: "created", CreatedAt: time.Now(), UpdatedAt: time.Now()}
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

func (f *retryingForwardingPeerFactory) Prepare(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls == 1 {
		return fmt.Errorf("temporary authorization failure")
	}
	return nil
}
func (*retryingForwardingPeerFactory) Connect(context.Context, Node, ssh.Signer) (forwardingPeer, error) {
	return nil, fmt.Errorf("unexpected Connect call")
}

func (*fakeForwardingPeerFactory) Prepare(context.Context, string) error { return nil }
func (f *fakeForwardingPeerFactory) Connect(_ context.Context, node Node, _ ssh.Signer) (forwardingPeer, error) {
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

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode for %q = %o, want %o", path, got, want)
	}
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
