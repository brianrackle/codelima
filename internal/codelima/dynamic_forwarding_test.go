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

	"github.com/brianrackle/codelima/internal/codelima/daemon"
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

func TestParseForwardingGuestObservationIncludesResourcesCPUAndListeners(t *testing.T) {
	t.Parallel()

	input := []byte(`cpu  100 10 30 500 20 5 5 10 40 2
codelima-memory 4194304 1048576
codelima-disk 33554432 8388608
  sl  local_address rem_address   st
   0: 0100007F:1F90 00000000:0000 0A
   1: 00000000000000000000000000000001:1451 00000000000000000000000000000000:0000 0A
`)
	got := parseForwardingGuestObservation(input)
	if got.CPU != (guestCPUCounters{Total: 680, Idle: 520}) {
		t.Fatalf("CPU counters = %#v, want total 680 and idle 520", got.CPU)
	}
	if want := []int{5201, 8080}; !reflect.DeepEqual(got.Ports, want) {
		t.Fatalf("ports = %v, want %v", got.Ports, want)
	}
	if want := (guestResourceUsage{UsedBytes: 3 << 30, TotalBytes: 4 << 30}); got.Memory != want {
		t.Fatalf("memory usage = %#v, want %#v", got.Memory, want)
	}
	if want := (guestResourceUsage{UsedBytes: 8 << 30, TotalBytes: 32 << 30}); got.Disk != want {
		t.Fatalf("disk usage = %#v, want %#v", got.Disk, want)
	}
}

func TestParseForwardingGuestObservationRejectsInvalidResources(t *testing.T) {
	t.Parallel()

	input := []byte(`codelima-memory 1024 2048
codelima-disk 1024 not-a-number
`)
	got := parseForwardingGuestObservation(input)
	if got.Memory != (guestResourceUsage{}) || got.Disk != (guestResourceUsage{}) {
		t.Fatalf("invalid resources = memory %#v, disk %#v; want unavailable", got.Memory, got.Disk)
	}
}

func TestGuestCPUUsagePercentUsesCounterDeltas(t *testing.T) {
	t.Parallel()

	got, ok := guestCPUUsagePercent(
		guestCPUCounters{Total: 1000, Idle: 700},
		guestCPUCounters{Total: 1200, Idle: 750},
	)
	if !ok || got != 75 {
		t.Fatalf("guestCPUUsagePercent() = (%v, %v), want (75, true)", got, ok)
	}

	for name, current := range map[string]guestCPUCounters{
		"no elapsed ticks": {Total: 1000, Idle: 700},
		"counter reset":    {Total: 900, Idle: 600},
		"idle overflow":    {Total: 1200, Idle: 950},
	} {
		t.Run(name, func(t *testing.T) {
			if usage, valid := guestCPUUsagePercent(
				guestCPUCounters{Total: 1000, Idle: 700},
				current,
			); valid {
				t.Fatalf("guestCPUUsagePercent() = (%v, true), want unavailable", usage)
			}
		})
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
		// The forwarder binds ::1 as well, so its literal must be accepted too.
		{host: "[::1]:8080", want: true},
		{host: "[::1]", want: true},
		{host: "127.0.0.2:8080", want: true},
		{host: "test-node.localhost:8080"},
		{host: "192.168.1.10:8080"},
		{host: "localhost:bad"},
		{host: "127.0.0.1:bad"},
		{host: "[::1]:bad"},
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
	port := reserveDualLoopbackPort(t)
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
		listen:  net.Listen,
		nodes:   map[string]*forwardingNodeState{},
		routes:  map[dynamicRouteKey]*dynamicForwardingRoute{},
		known:   map[string]bool{"first": true, "second": true},
		servers: map[int]*dynamicPortServer{},
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

func TestDynamicForwardingHandlerRoutesCodexLoginCallbackToNewestActiveClaim(t *testing.T) {
	t.Parallel()

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
		routes:  map[dynamicRouteKey]*dynamicForwardingRoute{},
		known:   map[string]bool{"first": true, "second": true},
		servers: map[int]*dynamicPortServer{},
		nodes:   map[string]*forwardingNodeState{},
	}
	for offset, node := range []string{"first", "second"} {
		peer := &dialAddressPeer{address: strings.TrimPrefix(upstreams[node].URL, "http://")}
		metadata := Node{ID: node, SandboxName: node}
		key := dynamicRouteKey{node: node, port: codexLoginCallbackPort}
		forwarder.routes[key] = newDynamicForwardingRoute(metadata, codexLoginCallbackPort, peer, firstSeen.Add(time.Duration(offset)*time.Millisecond), nil)
	}
	forwarder.servers[codexLoginCallbackPort] = &dynamicPortServer{
		port:        codexLoginCallbackPort,
		defaultNode: "first",
		status:      "serving",
	}
	defer func() { _ = forwarder.Close() }()

	forwarder.reconcileServers()

	handler := &dynamicForwardingHandler{forwarder: forwarder, port: codexLoginCallbackPort}
	assertForwardingResponse(t, handler, "localhost:1455", "second")
	assertForwardingResponse(t, handler, "127.0.0.1:1455", "second")
	assertForwardingResponse(t, handler, "first.localhost:1455", "first")

	forwarder.mu.Lock()
	secondKey := dynamicRouteKey{node: "second", port: codexLoginCallbackPort}
	forwarder.routes[secondKey].close()
	delete(forwarder.routes, secondKey)
	forwarder.mu.Unlock()
	forwarder.reconcileServers()

	assertForwardingResponse(t, handler, "localhost:1455", "first")
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
	node := saveForwardingTestNode(t, service, "test-node")
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("from-node"))
	}))
	defer upstream.Close()
	port := reserveDualLoopbackPort(t)
	peer := &controllableForwardingPeer{ports: []int{port}, address: strings.TrimPrefix(upstream.URL, "http://")}
	factory := &fakeForwardingPeerFactory{peers: map[string]*controllableForwardingPeer{node.SandboxName: peer}}
	forwarder, clock := newClockedTestDynamicForwarder(service, factory)
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

	// A successful scan that no longer lists the port is authoritative: the
	// route and its host listener go away immediately.
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
	peer.failScans(fmt.Errorf("instance is gone"))
	forwarder.reconcile(context.Background())
	if peer.isClosed() {
		t.Fatal("forwarding peer was released before the stop was confirmed")
	}
	// Two failures spanning the grace window retire the transport; the stop is
	// only confirmed once the observation has also held for its own window.
	clock.advance(forwardingRouteGrace + time.Second)
	forwarder.reconcile(context.Background())
	clock.advance(forwardingStopGrace + time.Second)
	forwarder.reconcile(context.Background())
	if !peer.isClosed() {
		t.Fatal("forwarding peer remained open after the node stop was confirmed")
	}
	if forwardingNodeSnapshot(forwarder, node.SandboxName) != nil {
		t.Fatal("stopped node remained in the forwarding table")
	}
}

func TestDynamicForwarderAddsLiveResourceUsageToNodeObservations(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "cpu-node")
	peer := &controllableForwardingPeer{
		observations: []forwardingGuestObservation{
			{
				CPU:    guestCPUCounters{Total: 1000, Idle: 700},
				Memory: guestResourceUsage{UsedBytes: 2 << 30, TotalBytes: 4 << 30},
				Disk:   guestResourceUsage{UsedBytes: 8 << 30, TotalBytes: 32 << 30},
			},
			{
				CPU:    guestCPUCounters{Total: 1200, Idle: 750},
				Memory: guestResourceUsage{UsedBytes: 3 << 30, TotalBytes: 4 << 30},
				Disk:   guestResourceUsage{UsedBytes: 9 << 30, TotalBytes: 32 << 30},
			},
		},
	}
	forwarder := newTestDynamicForwarder(service, &fakeForwardingPeerFactory{
		peers: map[string]*controllableForwardingPeer{node.SandboxName: peer},
	})
	forwarder.reconcile(context.Background())
	forwarder.reconcile(context.Background())
	defer func() { _ = forwarder.Close() }()

	nodes, err := service.NodeList(context.Background(), false)
	if err != nil {
		t.Fatalf("NodeList() error = %v", err)
	}
	forwarder.addNodeUsage(nodes)
	if len(nodes) != 1 || nodes[0].LastRuntimeObservation == nil {
		t.Fatalf("enriched nodes = %#v, want one runtime observation", nodes)
	}
	observation := nodes[0].LastRuntimeObservation
	if observation.CPUUsagePercent == nil || *observation.CPUUsagePercent != 75 {
		t.Fatalf("CPU usage = %v, want 75", observation.CPUUsagePercent)
	}
	if observation.CPUUsageSampledAt == nil || observation.CPUUsageSampledAt.IsZero() {
		t.Fatalf("CPU sample time = %v, want a timestamp", observation.CPUUsageSampledAt)
	}
	if observation.MemoryUsedBytes == nil || *observation.MemoryUsedBytes != 3<<30 ||
		observation.MemoryTotalBytes == nil || *observation.MemoryTotalBytes != 4<<30 {
		t.Fatalf("memory usage = %v/%v, want 3 GiB/4 GiB", observation.MemoryUsedBytes, observation.MemoryTotalBytes)
	}
	if observation.DiskUsedBytes == nil || *observation.DiskUsedBytes != 9<<30 ||
		observation.DiskTotalBytes == nil || *observation.DiskTotalBytes != 32<<30 {
		t.Fatalf("disk usage = %v/%v, want 9 GiB/32 GiB", observation.DiskUsedBytes, observation.DiskTotalBytes)
	}
	if observation.ResourceUsageSampledAt == nil || observation.ResourceUsageSampledAt.IsZero() {
		t.Fatalf("resource sample time = %v, want a timestamp", observation.ResourceUsageSampledAt)
	}
}

// capturedBroadcast records the events a forwarder publishes. It stands in for
// the host's gated publisher, which is all the forwarder is ever given.
type capturedBroadcast struct {
	mu     sync.Mutex
	names  []string
	usage  []nodeUsageEvent
	others []string
}

func (c *capturedBroadcast) publish(name string, data any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.names = append(c.names, name)
	if event, ok := data.(nodeUsageEvent); ok && name == "node.usage_changed" {
		c.usage = append(c.usage, event)
		return
	}
	c.others = append(c.others, name)
}

func (c *capturedBroadcast) usageEvents() []nodeUsageEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.usage)
}

// Live usage is sampled once a second per running node. It has to be pushed:
// it is the one thing a node.list reply carries that nothing else announces, so
// without this event the TUI can only be as fresh as its slow fallback poll.
func TestDynamicForwarderPublishesEveryUsageSample(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "usage-push-node")
	peer := &controllableForwardingPeer{
		observations: []forwardingGuestObservation{
			{
				CPU:    guestCPUCounters{Total: 1000, Idle: 700},
				Memory: guestResourceUsage{UsedBytes: 2 << 30, TotalBytes: 4 << 30},
				Disk:   guestResourceUsage{UsedBytes: 8 << 30, TotalBytes: 32 << 30},
			},
			{
				CPU:    guestCPUCounters{Total: 1200, Idle: 750},
				Memory: guestResourceUsage{UsedBytes: 3 << 30, TotalBytes: 4 << 30},
				Disk:   guestResourceUsage{UsedBytes: 9 << 30, TotalBytes: 32 << 30},
			},
		},
	}
	forwarder := newTestDynamicForwarder(service, &fakeForwardingPeerFactory{
		peers: map[string]*controllableForwardingPeer{node.SandboxName: peer},
	})
	defer func() { _ = forwarder.Close() }()

	events := &capturedBroadcast{}
	forwarder.setBroadcast(events.publish)

	forwarder.reconcile(context.Background())
	forwarder.reconcile(context.Background())

	published := events.usageEvents()
	if len(published) != 2 {
		t.Fatalf("published %d usage events, want one per sample: %+v", len(published), published)
	}

	// The first sample has no previous CPU counters to difference against, so
	// it reports memory and disk only — exactly what node.list would carry.
	first := published[0]
	if first.NodeID != node.ID || first.SampledAt.IsZero() {
		t.Fatalf("first usage event = %+v, want node %s with a sample time", first, node.ID)
	}
	if first.CPUUsagePercent != nil {
		t.Fatalf("first usage event reported CPU %v without a previous counter", *first.CPUUsagePercent)
	}

	second := published[1]
	if second.CPUUsagePercent == nil || *second.CPUUsagePercent != 75 {
		t.Fatalf("pushed CPU usage = %v, want 75", second.CPUUsagePercent)
	}
	if second.MemoryUsedBytes == nil || *second.MemoryUsedBytes != 3<<30 ||
		second.MemoryTotalBytes == nil || *second.MemoryTotalBytes != 4<<30 {
		t.Fatalf("pushed memory usage = %v/%v, want 3 GiB/4 GiB", second.MemoryUsedBytes, second.MemoryTotalBytes)
	}
	if second.DiskUsedBytes == nil || *second.DiskUsedBytes != 9<<30 ||
		second.DiskTotalBytes == nil || *second.DiskTotalBytes != 32<<30 {
		t.Fatalf("pushed disk usage = %v/%v, want 9 GiB/32 GiB", second.DiskUsedBytes, second.DiskTotalBytes)
	}

	// The push and the node.list enrichment must describe the same sample, or
	// the subscriber cannot merge them by timestamp.
	nodes, err := service.NodeList(context.Background(), false)
	if err != nil {
		t.Fatalf("NodeList() error = %v", err)
	}
	forwarder.addNodeUsage(nodes)
	if len(nodes) != 1 || nodes[0].LastRuntimeObservation == nil {
		t.Fatalf("enriched nodes = %#v, want one runtime observation", nodes)
	}
	listed := nodeUsageFromNode(nodes[0])
	if !reflect.DeepEqual(listed.CPUUsagePercent, second.CPUUsagePercent) ||
		!reflect.DeepEqual(listed.MemoryUsedBytes, second.MemoryUsedBytes) ||
		!reflect.DeepEqual(listed.DiskTotalBytes, second.DiskTotalBytes) {
		t.Fatalf("pushed sample %+v disagrees with the node.list enrichment %+v", second, listed)
	}
	if !listed.SampledAt.Equal(second.SampledAt) {
		t.Fatalf("pushed sample time %s != node.list sample time %s", second.SampledAt, listed.SampledAt)
	}
}

// A subscriber holding the previous numbers must stop showing them when the
// daemon drops the reading, so the drop is published like any other sample.
func TestDynamicForwarderPublishesClearedUsage(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "usage-clear-node")
	peer := &controllableForwardingPeer{
		observations: []forwardingGuestObservation{{
			CPU:    guestCPUCounters{Total: 1000, Idle: 700},
			Memory: guestResourceUsage{UsedBytes: 2 << 30, TotalBytes: 4 << 30},
		}},
	}
	forwarder := newTestDynamicForwarder(service, &fakeForwardingPeerFactory{
		peers: map[string]*controllableForwardingPeer{node.SandboxName: peer},
	})
	defer func() { _ = forwarder.Close() }()

	events := &capturedBroadcast{}
	forwarder.setBroadcast(events.publish)
	forwarder.reconcile(context.Background())

	peer.mu.Lock()
	peer.telemetryErr = fmt.Errorf("guest telemetry unavailable")
	peer.mu.Unlock()
	forwarder.reconcile(context.Background())

	published := events.usageEvents()
	if len(published) != 2 {
		t.Fatalf("published %d usage events, want a sample and its clear: %+v", len(published), published)
	}
	cleared := published[1]
	if cleared.NodeID != node.ID || cleared.SampledAt.IsZero() {
		t.Fatalf("cleared usage event = %+v, want node %s with a sample time", cleared, node.ID)
	}
	if cleared.CPUUsagePercent != nil || cleared.MemoryUsedBytes != nil || cleared.DiskTotalBytes != nil {
		t.Fatalf("cleared usage event still carried readings: %+v", cleared)
	}
}

// The forwarder must never learn about the server: it publishes through the
// same gated closure the host uses, handed to it when the links are wired.
func TestDaemonHostWiresTheUsagePublisherIntoTheForwarder(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	host := newDaemonHost(service)
	host.forwarder = newTestDynamicForwarder(service, &fakeForwardingPeerFactory{})
	defer func() { _ = host.forwarder.Close() }()

	host.forwarder.mu.RLock()
	before := host.forwarder.broadcast
	host.forwarder.mu.RUnlock()
	if before != nil {
		t.Fatal("an unwired forwarder already had a publisher")
	}

	host.wireServerLinks(daemon.NewServer(daemon.Config{
		Home: service.cfg.MetadataRoot, Version: Version, Handler: host, Logger: service.log(),
	}))

	host.forwarder.mu.RLock()
	after := host.forwarder.broadcast
	host.forwarder.mu.RUnlock()
	if after == nil {
		t.Fatal("wireServerLinks left the forwarder with no publisher; usage samples would never be pushed")
	}
}

func TestDynamicForwarderServesHostnameRoutesOnIPv6Loopback(t *testing.T) {
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "ipv6-host-route")
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("from-node"))
	}))
	defer upstream.Close()

	port := reserveDualLoopbackPort(t)
	peer := &controllableForwardingPeer{ports: []int{port}, address: strings.TrimPrefix(upstream.URL, "http://")}
	forwarder := newTestDynamicForwarder(service, &fakeForwardingPeerFactory{
		peers: map[string]*controllableForwardingPeer{node.SandboxName: peer},
	})
	forwarder.reconcile(context.Background())
	defer func() { _ = forwarder.Close() }()

	ports := forwarder.Snapshot()["ports"].([]map[string]any)
	if len(ports) != 1 {
		t.Fatalf("forwarding ports = %#v, want one port", ports)
	}
	addresses := ports[0]["addresses"].([]string)
	if len(addresses) != 2 ||
		!slices.Contains(addresses, net.JoinHostPort("127.0.0.1", strconv.Itoa(port))) ||
		!slices.Contains(addresses, net.JoinHostPort("::1", strconv.Itoa(port))) {
		t.Fatalf("forwarding addresses = %v, want both host loopbacks", addresses)
	}

	client := &http.Client{
		Timeout:   time.Second,
		Transport: &http.Transport{Proxy: nil},
	}
	for _, host := range []string{"localhost", node.SandboxName + ".localhost"} {
		request, err := http.NewRequest(http.MethodGet, "http://"+net.JoinHostPort("::1", strconv.Itoa(port))+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Host = net.JoinHostPort(host, strconv.Itoa(port))
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("IPv6 loopback request for %s failed: %v", host, err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatalf("read IPv6 loopback response for %s: %v", host, readErr)
		}
		if response.StatusCode != http.StatusOK || string(body) != "from-node" {
			t.Fatalf("IPv6 loopback request for %s returned %d %q", host, response.StatusCode, body)
		}
	}
}

func TestDynamicForwarderRollsBackIPv4ListenerWhenIPv6PortConflicts(t *testing.T) {
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "ipv6-conflict-node")
	occupied, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	t.Cleanup(func() { _ = occupied.Close() })
	port := occupied.Addr().(*net.TCPAddr).Port
	peer := &controllableForwardingPeer{ports: []int{port}}
	forwarder := newTestDynamicForwarder(service, &fakeForwardingPeerFactory{
		peers: map[string]*controllableForwardingPeer{node.SandboxName: peer},
	})
	forwarder.reconcile(context.Background())
	defer func() { _ = forwarder.Close() }()

	ports := forwarder.Snapshot()["ports"].([]map[string]any)
	if len(ports) != 1 || ports[0]["status"] != "conflicted" {
		t.Fatalf("forwarding ports = %#v, want one conflicted port", ports)
	}
	ipv4, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("IPv4 listener was not rolled back after IPv6 conflict: %v", err)
	}
	if err := ipv4.Close(); err != nil {
		t.Fatal(err)
	}

	if err := occupied.Close(); err != nil {
		t.Fatal(err)
	}
	forwarder.reconcile(context.Background())
	ports = forwarder.Snapshot()["ports"].([]map[string]any)
	if len(ports) != 1 || ports[0]["status"] != "serving" {
		t.Fatalf("forwarding ports after conflict release = %#v, want serving", ports)
	}
}

func TestDynamicForwarderRecoversFromHostBindConflict(t *testing.T) {
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "conflict-node")
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

func saveForwardingTestNode(t *testing.T, service *Service, sandboxName string) Node {
	t.Helper()
	node := Node{ID: newID(), Slug: sandboxName, DirectoryPath: t.TempDir(), SandboxName: sandboxName, Status: "created", LifecycleState: "created", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := service.store.SaveNode(node, BootstrapState{}); err != nil {
		t.Fatalf("SaveNode() error = %v", err)
	}
	fake := service.sandbox.(*fakeSandbox)
	fake.mu.Lock()
	fake.observations[sandboxName] = RuntimeObservation{Name: sandboxName, Exists: true, Status: "running"}
	fake.mu.Unlock()
	return node
}

func reserveDualLoopbackPort(t *testing.T) int {
	t.Helper()
	for range 20 {
		ipv6, err := net.Listen("tcp6", "[::1]:0")
		if err != nil {
			t.Skipf("IPv6 loopback is unavailable: %v", err)
		}
		port := ipv6.Addr().(*net.TCPAddr).Port
		ipv4, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			_ = ipv6.Close()
			continue
		}
		if err := ipv4.Close(); err != nil {
			_ = ipv6.Close()
			t.Fatal(err)
		}
		if err := ipv6.Close(); err != nil {
			t.Fatal(err)
		}
		return port
	}
	t.Fatal("could not reserve a port on both loopback address families")
	return 0
}

func newTestDynamicForwarder(service *Service, factory forwardingPeerFactory) *dynamicForwarder {
	forwarder, _ := newClockedTestDynamicForwarder(service, factory)
	return forwarder
}

// newClockedTestDynamicForwarder builds a forwarder whose grace windows,
// claim memory, and backoff run on a controllable clock. Keepalives are parked
// on an hour-long interval so only tests that exercise them see a ping.
func newClockedTestDynamicForwarder(service *Service, factory forwardingPeerFactory) (*dynamicForwarder, *forwardingTestClock) {
	clock := &forwardingTestClock{now: time.Now().UTC()}
	return &dynamicForwarder{
		service: service, factory: factory, interval: time.Hour, listen: net.Listen, now: clock.Now,
		scanTimeout: 2 * time.Second, telemetryTimeout: 2 * time.Second, dialTimeout: time.Second,
		routeGrace: forwardingRouteGrace, stopGrace: forwardingStopGrace,
		claimMemory: forwardingClaimMemory, warnInterval: forwardingWarnInterval,
		keepaliveInterval: time.Hour, keepaliveTimeout: time.Second, keepaliveTolerance: forwardingKeepaliveTolerance,
		concurrency: forwardingConcurrency,
		nodes:       map[string]*forwardingNodeState{},
		routes:      map[dynamicRouteKey]*dynamicForwardingRoute{},
		discovered:  map[dynamicRouteKey]routeDiscoveryMemory{},
		known:       map[string]bool{}, servers: map[int]*dynamicPortServer{},
		cpu: map[string]nodeCPUUsageSample{}, resources: map[string]nodeResourceUsageSample{},
		counters: map[string]guestCPUCounters{},
	}, clock
}

// forwardingTestClock drives the forwarder's grace windows without sleeping.
type forwardingTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *forwardingTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *forwardingTestClock) advance(elapsed time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(elapsed)
}

type fakeForwardingPeerFactory struct {
	mu       sync.Mutex
	peers    map[string]*controllableForwardingPeer
	replaced map[string][]*controllableForwardingPeer
	connects map[string]int
	err      error
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.connects == nil {
		f.connects = map[string]int{}
	}
	f.connects[node.SandboxName]++
	if f.err != nil {
		return nil, f.err
	}
	// A queued replacement models a reconnect handing back a fresh transport.
	if queued := f.replaced[node.SandboxName]; len(queued) > 0 {
		peer := queued[0]
		f.replaced[node.SandboxName] = queued[1:]
		f.peers[node.SandboxName] = peer
		return peer, nil
	}
	peer := f.peers[node.SandboxName]
	if peer == nil {
		return nil, fmt.Errorf("no peer for %s", node.SandboxName)
	}
	return peer, nil
}

func (f *fakeForwardingPeerFactory) connectCount(sandboxName string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connects[sandboxName]
}

type controllableForwardingPeer struct {
	mu           sync.Mutex
	ports        []int
	observations []forwardingGuestObservation
	next         int
	address      string
	closed       bool
	scanErr      error
	telemetryErr error
	pingErr      error
	pingFailures int
	scans        int
	pings        int
	// scanGate blocks every scan until it is closed, modelling a node whose
	// discovery hangs; scanEntered announces that a scan is in flight.
	scanGate    chan struct{}
	scanEntered chan struct{}
}

func (p *controllableForwardingPeer) ScanPorts(ctx context.Context) ([]int, error) {
	p.mu.Lock()
	p.scans++
	gate, entered, err := p.scanGate, p.scanEntered, p.scanErr
	ports := slices.Clone(p.ports)
	p.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return ports, nil
}

func (p *controllableForwardingPeer) SampleTelemetry(context.Context) (forwardingGuestObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.telemetryErr != nil {
		return forwardingGuestObservation{}, p.telemetryErr
	}
	if p.next < len(p.observations) {
		observation := p.observations[p.next]
		p.next++
		observation.Ports = nil
		return observation, nil
	}
	return forwardingGuestObservation{}, nil
}

func (p *controllableForwardingPeer) Ping(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pings++
	if p.pingFailures > 0 {
		p.pingFailures--
		return fmt.Errorf("keepalive timed out")
	}
	return p.pingErr
}

func (p *controllableForwardingPeer) pingCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pings
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

func (p *controllableForwardingPeer) failScans(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scanErr = err
}

func (p *controllableForwardingPeer) setPorts(ports []int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ports = ports
	p.scanErr = nil
}

func (p *controllableForwardingPeer) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *controllableForwardingPeer) scanCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scans
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

func (*dialAddressPeer) ScanPorts(context.Context) ([]int, error) { return nil, nil }
func (*dialAddressPeer) SampleTelemetry(context.Context) (forwardingGuestObservation, error) {
	return forwardingGuestObservation{}, nil
}
func (*dialAddressPeer) Ping(context.Context) error { return nil }
func (p *dialAddressPeer) DialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, p.address)
}
func (*dialAddressPeer) Close() error { return nil }

type failingForwardingPeer struct{}

func (failingForwardingPeer) ScanPorts(context.Context) ([]int, error) { return nil, nil }
func (failingForwardingPeer) SampleTelemetry(context.Context) (forwardingGuestObservation, error) {
	return forwardingGuestObservation{}, nil
}
func (failingForwardingPeer) Ping(context.Context) error { return nil }
func (failingForwardingPeer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, fmt.Errorf("injected tunnel failure")
}
func (failingForwardingPeer) Close() error { return nil }

// blackHoleForwardingPeer accepts a dial and never answers, modelling a peer
// whose guest end vanished without the host socket noticing.
type blackHoleForwardingPeer struct {
	mu       sync.Mutex
	attempts int
}

func (*blackHoleForwardingPeer) ScanPorts(context.Context) ([]int, error) { return nil, nil }
func (*blackHoleForwardingPeer) SampleTelemetry(context.Context) (forwardingGuestObservation, error) {
	return forwardingGuestObservation{}, nil
}
func (*blackHoleForwardingPeer) Ping(context.Context) error { return nil }
func (p *blackHoleForwardingPeer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	p.mu.Lock()
	p.attempts++
	p.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}
func (*blackHoleForwardingPeer) Close() error { return nil }

type ipv6OnlyForwardingPeer struct {
	mu              sync.Mutex
	upstreamAddress string
	attempts        []string
}

func (*ipv6OnlyForwardingPeer) ScanPorts(context.Context) ([]int, error) { return nil, nil }
func (*ipv6OnlyForwardingPeer) SampleTelemetry(context.Context) (forwardingGuestObservation, error) {
	return forwardingGuestObservation{}, nil
}
func (*ipv6OnlyForwardingPeer) Ping(context.Context) error { return nil }
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

// --- Resilience: peer health, grace windows, and confirmed teardown (I6) ---

func TestPeerHealthMonitorClosesPeerAfterConsecutiveKeepaliveMisses(t *testing.T) {
	t.Parallel()

	peer := &controllableForwardingPeer{pingErr: fmt.Errorf("ssh: connection lost")}
	monitor := newPeerHealthMonitor(peer, "dead-node", time.Millisecond, 50*time.Millisecond, 3, nil)
	monitor.start(context.Background())
	defer monitor.stop()

	waitForForwardingCondition(t, "peer declared dead", func() bool { return !monitor.alive() })
	if !peer.isClosed() {
		t.Fatal("a peer that missed its keepalives was not closed")
	}
	if got := peer.pingCount(); got < 3 {
		t.Fatalf("keepalive attempts = %d, want at least the 3 tolerated misses", got)
	}
	if monitor.failure() == "" {
		t.Fatal("keepalive failure was not recorded for the snapshot")
	}
}

func TestPeerHealthMonitorToleratesIsolatedKeepaliveMisses(t *testing.T) {
	t.Parallel()

	peer := &controllableForwardingPeer{pingFailures: 2}
	monitor := newPeerHealthMonitor(peer, "flaky-node", time.Millisecond, 50*time.Millisecond, 3, nil)
	monitor.start(context.Background())
	defer monitor.stop()

	waitForForwardingCondition(t, "keepalives resumed", func() bool { return peer.pingCount() >= 6 })
	if !monitor.alive() {
		t.Fatal("misses below the tolerance killed a live peer")
	}
	if peer.isClosed() {
		t.Fatal("a live peer was closed")
	}
}

func TestDynamicForwarderReconnectsPeerAfterKeepaliveLossAndRepointsRoutes(t *testing.T) {
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "keepalive-node")
	stale := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("stale-peer"))
	}))
	defer stale.Close()
	fresh := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("fresh-peer"))
	}))
	defer fresh.Close()

	port := reserveDualLoopbackPort(t)
	deadPeer := &controllableForwardingPeer{
		ports: []int{port}, address: strings.TrimPrefix(stale.URL, "http://"),
		pingErr: fmt.Errorf("ssh: connection lost"),
	}
	livePeer := &controllableForwardingPeer{ports: []int{port}, address: strings.TrimPrefix(fresh.URL, "http://")}
	factory := &fakeForwardingPeerFactory{
		peers:    map[string]*controllableForwardingPeer{node.SandboxName: deadPeer},
		replaced: map[string][]*controllableForwardingPeer{node.SandboxName: {deadPeer, livePeer}},
	}
	forwarder, clock := newClockedTestDynamicForwarder(service, factory)
	forwarder.keepaliveInterval = time.Millisecond
	forwarder.keepaliveTimeout = 100 * time.Millisecond
	forwarder.keepaliveTolerance = 2
	defer func() { _ = forwarder.Close() }()

	forwarder.reconcile(context.Background())
	if status, body := forwardingHostRequest(t, port); status != http.StatusOK || body != "stale-peer" {
		t.Fatalf("initial request = %d %q", status, body)
	}
	waitForForwardingCondition(t, "keepalive loss closed the peer", deadPeer.isClosed)

	// Losing a peer never removes routes: the port keeps its listener.
	if got := forwardingRouteState(forwarder, node.SandboxName, port); got != "serving" {
		t.Fatalf("route state after keepalive loss = %q, want serving", got)
	}
	clock.advance(time.Second)
	forwarder.reconcile(context.Background())
	if got := factory.connectCount(node.SandboxName); got != 2 {
		t.Fatalf("connect attempts = %d, want a reconnect after the keepalive loss", got)
	}
	if status, body := forwardingHostRequest(t, port); status != http.StatusOK || body != "fresh-peer" {
		t.Fatalf("request after reconnect = %d %q, want the replacement peer", status, body)
	}
}

func TestDynamicForwarderKeepsRoutesThroughTransientScanFailure(t *testing.T) {
	harness := newForwardingHarness(t, "grace-node")
	harness.reconcile()
	harness.assertRouteState("serving")

	harness.peer.failScans(fmt.Errorf("guest is too busy to answer"))
	for range 3 {
		harness.clock.advance(2 * time.Second)
		harness.reconcile()
	}
	harness.assertRouteState("serving")
	if status, body := forwardingHostRequest(t, harness.port); status != http.StatusOK || body != "from-node" {
		t.Fatalf("request inside the grace window = %d %q, want the guest response", status, body)
	}

	harness.peer.setPorts([]int{harness.port})
	harness.clock.advance(time.Minute)
	harness.reconcile()
	harness.assertRouteState("serving")
	if got := forwardingNodeSnapshot(harness.forwarder, harness.node.SandboxName)["health"]; got != "healthy" {
		t.Fatalf("node health after recovery = %v, want healthy", got)
	}
}

func TestDynamicForwarderLapsedRoutesAnswerBadGatewayWithoutClosingListener(t *testing.T) {
	harness := newForwardingHarness(t, "lapse-node")
	harness.reconcile()

	harness.peer.failScans(fmt.Errorf("ssh: connection lost"))
	harness.clock.advance(2 * time.Second)
	harness.reconcile()
	harness.clock.advance(forwardingRouteGrace + time.Second)
	harness.reconcile()
	harness.assertRouteState("lapsed")

	// The whole point of I6: the socket still answers, with a retryable status.
	// A closed listener would surface as a connection refusal that clients cache.
	if status, _ := forwardingHostRequest(t, harness.port); status != http.StatusBadGateway {
		t.Fatalf("lapsed route status = %d, want %d", status, http.StatusBadGateway)
	}
	if status, _ := forwardingNamedHostRequest(t, harness.node.SandboxName, harness.port); status != http.StatusBadGateway {
		t.Fatalf("lapsed named-host status = %d, want %d", status, http.StatusBadGateway)
	}
	snapshot := harness.forwarder.Snapshot()
	if snapshot["degraded"] != true {
		t.Fatalf("snapshot degraded = %v, want true while routes are lapsed", snapshot["degraded"])
	}

	harness.peer.setPorts([]int{harness.port})
	harness.clock.advance(time.Minute)
	harness.reconcile()
	harness.assertRouteState("serving")
	if status, body := forwardingHostRequest(t, harness.port); status != http.StatusOK || body != "from-node" {
		t.Fatalf("request after recovery = %d %q", status, body)
	}
}

func TestDynamicForwarderRemovesRoutesOnlyOnConfirmedStop(t *testing.T) {
	harness := newForwardingHarness(t, "confirm-node")
	harness.reconcile()
	harness.assertRouteState("serving")

	// A not-running observation is not a stop while the guest still answers:
	// watch-synthesized and stale cache entries must not tear anything down.
	harness.setObservationStatus(ObservationStopped)
	harness.clock.advance(forwardingStopGrace + time.Second)
	harness.reconcile()
	harness.assertRouteState("serving")
	if status, body := forwardingHostRequest(t, harness.port); status != http.StatusOK || body != "from-node" {
		t.Fatalf("request during an unconfirmed stop = %d %q", status, body)
	}

	// Now the guest is genuinely gone. Routes lapse first, then the stop is
	// confirmed and only then does anything get removed.
	harness.peer.failScans(fmt.Errorf("ssh: connection lost"))
	harness.clock.advance(2 * time.Second)
	harness.reconcile()
	harness.clock.advance(forwardingRouteGrace + time.Second)
	harness.reconcile()
	harness.assertRouteState("lapsed")

	harness.clock.advance(forwardingStopGrace + time.Second)
	harness.reconcile()
	if got := forwardingRouteState(harness.forwarder, harness.node.SandboxName, harness.port); got != "" {
		t.Fatalf("route state after a confirmed stop = %q, want removed", got)
	}
	if connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(harness.port)), 200*time.Millisecond); err == nil {
		_ = connection.Close()
		t.Fatal("host listener remained bound after a confirmed stop")
	}
}

func TestDynamicForwarderKeepsPeersAndRoutesWhenNodeListFails(t *testing.T) {
	harness := newForwardingHarness(t, "inventory-node")
	harness.reconcile()
	scansBefore := harness.peer.scanCount()

	fake := harness.service.sandbox.(*fakeSandbox)
	fake.mu.Lock()
	fake.listErr = fmt.Errorf("limactl list is unreadable")
	fake.mu.Unlock()

	harness.clock.advance(time.Second)
	harness.reconcile()
	harness.assertRouteState("serving")
	if status, body := forwardingHostRequest(t, harness.port); status != http.StatusOK || body != "from-node" {
		t.Fatalf("request during a node-list outage = %d %q", status, body)
	}
	if harness.peer.scanCount() <= scansBefore {
		t.Fatal("discovery stopped on a known peer during a node-list outage")
	}
	snapshot := harness.forwarder.Snapshot()
	if snapshot["node_list_error"] == nil || snapshot["degraded"] != true {
		t.Fatalf("snapshot did not surface the node-list failure: %#v", snapshot)
	}

	fake.mu.Lock()
	fake.listErr = nil
	fake.mu.Unlock()
	harness.clock.advance(time.Second)
	harness.reconcile()
	if snapshot := harness.forwarder.Snapshot(); snapshot["node_list_error"] != nil || snapshot["degraded"] != false {
		t.Fatalf("node-list error was not cleared after recovery: %#v", snapshot)
	}
	harness.assertRouteState("serving")
}

func TestDynamicForwarderRestoresGenericClaimAfterRouteLapse(t *testing.T) {
	service, factory, port, peers := newSharedPortForwardingFixture(t, "alpha-node", "beta-node")
	forwarder, clock := newClockedTestDynamicForwarder(service, factory)
	forwarder.listen = ephemeralForwardingListen
	defer func() { _ = forwarder.Close() }()

	// alpha claims the generic host first; beta appears a tick later.
	setForwardingObservationStatus(t, service, "beta-node", ObservationStopped)
	forwarder.reconcile(context.Background())
	clock.advance(time.Second)
	setForwardingObservationStatus(t, service, "beta-node", ObservationRunning)
	forwarder.reconcile(context.Background())
	if got := forwardingClaimant(t, forwarder, port); got != "alpha-node" {
		t.Fatalf("initial claimant = %q, want alpha-node", got)
	}

	peers["alpha-node"].failScans(fmt.Errorf("ssh: connection lost"))
	clock.advance(2 * time.Second)
	forwarder.reconcile(context.Background())
	clock.advance(forwardingRouteGrace + time.Second)
	forwarder.reconcile(context.Background())
	if got := forwardingClaimant(t, forwarder, port); got != "beta-node" {
		t.Fatalf("claimant while alpha lapsed = %q, want beta-node", got)
	}

	peers["alpha-node"].setPorts([]int{port})
	clock.advance(10 * time.Second)
	forwarder.reconcile(context.Background())
	if got := forwardingRouteState(forwarder, "alpha-node", port); got != "serving" {
		t.Fatalf("alpha route state after recovery = %q, want serving", got)
	}
	if got := forwardingClaimant(t, forwarder, port); got != "alpha-node" {
		t.Fatalf("claimant after alpha recovered = %q, want the sticky original claimant", got)
	}
}

func TestDynamicForwarderRestoresGenericClaimAfterRediscovery(t *testing.T) {
	service, factory, port, peers := newSharedPortForwardingFixture(t, "alpha-node", "beta-node")
	forwarder, clock := newClockedTestDynamicForwarder(service, factory)
	forwarder.listen = ephemeralForwardingListen
	defer func() { _ = forwarder.Close() }()

	setForwardingObservationStatus(t, service, "beta-node", ObservationStopped)
	forwarder.reconcile(context.Background())
	clock.advance(time.Second)
	setForwardingObservationStatus(t, service, "beta-node", ObservationRunning)
	forwarder.reconcile(context.Background())
	if got := forwardingClaimant(t, forwarder, port); got != "alpha-node" {
		t.Fatalf("initial claimant = %q, want alpha-node", got)
	}

	// alpha's guest listener disappears outright, so its route is removed.
	peers["alpha-node"].setPorts(nil)
	clock.advance(time.Second)
	forwarder.reconcile(context.Background())
	if got := forwardingClaimant(t, forwarder, port); got != "beta-node" {
		t.Fatalf("claimant after alpha's listener disappeared = %q, want beta-node", got)
	}

	// It returns inside the claim-memory window, so the claim comes back with it
	// instead of beta keeping the generic host forever.
	peers["alpha-node"].setPorts([]int{port})
	clock.advance(10 * time.Second)
	forwarder.reconcile(context.Background())
	if got := forwardingClaimant(t, forwarder, port); got != "alpha-node" {
		t.Fatalf("claimant after alpha returned = %q, want the previous claimant", got)
	}

	// Past the window a rediscovered listener is genuinely new and does not
	// displace the incumbent.
	peers["alpha-node"].setPorts(nil)
	clock.advance(time.Second)
	forwarder.reconcile(context.Background())
	peers["alpha-node"].setPorts([]int{port})
	clock.advance(forwardingClaimMemory + time.Second)
	forwarder.reconcile(context.Background())
	if got := forwardingClaimant(t, forwarder, port); got != "beta-node" {
		t.Fatalf("claimant after the memory window expired = %q, want beta-node", got)
	}
}

func TestDynamicForwarderDiscoversNodesConcurrently(t *testing.T) {
	service, _ := newTestService(t)
	// The blocked node sorts first so a sequential reconcile would never reach
	// the quick node.
	blocked := saveForwardingTestNode(t, service, "aa-blocked-node")
	quick := saveForwardingTestNode(t, service, "zz-quick-node")
	gate := make(chan struct{})
	entered := make(chan struct{}, 4)
	blockedPeer := &controllableForwardingPeer{scanGate: gate, scanEntered: entered}
	port := reserveDualLoopbackPort(t)
	quickPeer := &controllableForwardingPeer{ports: []int{port}}
	factory := &fakeForwardingPeerFactory{peers: map[string]*controllableForwardingPeer{
		blocked.SandboxName: blockedPeer,
		quick.SandboxName:   quickPeer,
	}}
	forwarder, _ := newClockedTestDynamicForwarder(service, factory)
	forwarder.scanTimeout = 30 * time.Second
	defer func() { _ = forwarder.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		forwarder.reconcile(context.Background())
	}()
	released := false
	defer func() {
		if !released {
			close(gate)
		}
		<-done
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the blocked node's scan never started")
	}
	waitForForwardingCondition(t, "the quick node was routed while another node hung", func() bool {
		return forwardingRouteState(forwarder, quick.SandboxName, port) == "serving"
	})
	released = true
	close(gate)
	<-done
}

func TestDynamicForwarderBacksOffFailingNodeDiscovery(t *testing.T) {
	harness := newForwardingHarness(t, "backoff-node")
	harness.reconcile()

	harness.peer.failScans(fmt.Errorf("ssh: connection lost"))
	harness.clock.advance(time.Second)
	harness.reconcile()
	scans := harness.peer.scanCount()

	harness.clock.advance(100 * time.Millisecond)
	harness.reconcile()
	if harness.peer.scanCount() != scans {
		t.Fatal("a failing node was retried inside its backoff window")
	}
	if forwardingNodeSnapshot(harness.forwarder, harness.node.SandboxName)["retry_after"] == nil {
		t.Fatal("snapshot did not report the node's retry deadline")
	}

	harness.clock.advance(2 * time.Second)
	harness.reconcile()
	if harness.peer.scanCount() == scans {
		t.Fatal("a failing node was never retried after its backoff elapsed")
	}
}

func TestDynamicForwarderTelemetryFailureLeavesRoutesServing(t *testing.T) {
	harness := newForwardingHarness(t, "telemetry-node")
	harness.reconcile()

	harness.peer.mu.Lock()
	harness.peer.telemetryErr = fmt.Errorf("df hung on a stuck mount")
	harness.peer.mu.Unlock()
	harness.clock.advance(time.Second)
	harness.reconcile()

	harness.assertRouteState("serving")
	if status, body := forwardingHostRequest(t, harness.port); status != http.StatusOK || body != "from-node" {
		t.Fatalf("request after a telemetry failure = %d %q", status, body)
	}
	if got := forwardingNodeSnapshot(harness.forwarder, harness.node.SandboxName)["health"]; got != "healthy" {
		t.Fatalf("node health after a telemetry-only failure = %v, want healthy", got)
	}
}

func TestDynamicForwardingRouteBoundsProxiedDial(t *testing.T) {
	t.Parallel()

	if forwardingDialTimeout > 5*time.Second {
		t.Fatalf("default proxied dial timeout = %v, want a fail-fast bound", forwardingDialTimeout)
	}
	peer := &blackHoleForwardingPeer{}
	forwarder := &dynamicForwarder{routes: map[dynamicRouteKey]*dynamicForwardingRoute{}, known: map[string]bool{"node": true}}
	route := newDynamicForwardingRoute(Node{ID: "node", SandboxName: "node"}, 8080, peer, time.Now(), nil)
	if route.dialTimeout != forwardingDialTimeout {
		t.Fatalf("route dial timeout = %v, want %v", route.dialTimeout, forwardingDialTimeout)
	}
	route.dialTimeout = 150 * time.Millisecond
	forwarder.routes[dynamicRouteKey{node: "node", port: 8080}] = route

	started := time.Now()
	assertForwardingStatus(t, &dynamicForwardingHandler{forwarder: forwarder, port: 8080}, "node.localhost:8080", http.StatusBadGateway)
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("a black-hole peer held the request for %v; the dial is not bounded", elapsed)
	}
}

func TestDynamicForwarderServesIPv6LoopbackLiteralHost(t *testing.T) {
	harness := newForwardingHarness(t, "ipv6-literal-node")
	harness.reconcile()

	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	address := net.JoinHostPort("::1", strconv.Itoa(harness.port))
	request, err := http.NewRequest(http.MethodGet, "http://"+address+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("IPv6 literal request failed: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "from-node" {
		t.Fatalf("http://[::1]:%d returned %d %q, want the guest response", harness.port, response.StatusCode, body)
	}
}

// forwardingHarness wires one node, one guest listener, and one controllable
// peer to a forwarder on a test clock.
type forwardingHarness struct {
	t         *testing.T
	service   *Service
	node      Node
	peer      *controllableForwardingPeer
	factory   *fakeForwardingPeerFactory
	forwarder *dynamicForwarder
	clock     *forwardingTestClock
	port      int
}

// forwardingHarnessBody is what the harness's guest listener answers with.
const forwardingHarnessBody = "from-node"

func newForwardingHarness(t *testing.T, sandboxName string) *forwardingHarness {
	t.Helper()
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, sandboxName)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(forwardingHarnessBody))
	}))
	t.Cleanup(upstream.Close)
	port := reserveDualLoopbackPort(t)
	peer := &controllableForwardingPeer{ports: []int{port}, address: strings.TrimPrefix(upstream.URL, "http://")}
	factory := &fakeForwardingPeerFactory{peers: map[string]*controllableForwardingPeer{sandboxName: peer}}
	forwarder, clock := newClockedTestDynamicForwarder(service, factory)
	t.Cleanup(func() { _ = forwarder.Close() })
	return &forwardingHarness{t: t, service: service, node: node, peer: peer, factory: factory, forwarder: forwarder, clock: clock, port: port}
}

func (h *forwardingHarness) reconcile() { h.forwarder.reconcile(context.Background()) }

func (h *forwardingHarness) setObservationStatus(status ObservationStatus) {
	setForwardingObservationStatus(h.t, h.service, h.node.SandboxName, status)
}

func (h *forwardingHarness) assertRouteState(want string) {
	h.t.Helper()
	if got := forwardingRouteState(h.forwarder, h.node.SandboxName, h.port); got != want {
		h.t.Fatalf("route state = %q, want %q; snapshot=%#v", got, want, h.forwarder.Snapshot())
	}
}

// newSharedPortForwardingFixture puts several nodes on one guest port. Its
// caller binds ephemeral host sockets, so the guest port is a fixed number that
// never has to be free on the host.
func newSharedPortForwardingFixture(t *testing.T, sandboxNames ...string) (*Service, *fakeForwardingPeerFactory, int, map[string]*controllableForwardingPeer) {
	t.Helper()
	service, _ := newTestService(t)
	const port = 8080
	peers := map[string]*controllableForwardingPeer{}
	for _, sandboxName := range sandboxNames {
		saveForwardingTestNode(t, service, sandboxName)
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(sandboxName))
		}))
		t.Cleanup(upstream.Close)
		peers[sandboxName] = &controllableForwardingPeer{ports: []int{port}, address: strings.TrimPrefix(upstream.URL, "http://")}
	}
	return service, &fakeForwardingPeerFactory{peers: peers}, port, peers
}

// ephemeralForwardingListen serves a logical port from an ephemeral socket, so
// tests that only assert routing decisions never contend for a fixed host port.
func ephemeralForwardingListen(string, string) (net.Listener, error) {
	return net.Listen("tcp4", "127.0.0.1:0")
}

func setForwardingObservationStatus(t *testing.T, service *Service, sandboxName string, status ObservationStatus) {
	t.Helper()
	fake := service.sandbox.(*fakeSandbox)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.observations[sandboxName] = RuntimeObservation{Name: sandboxName, Exists: true, Status: status}
}

func forwardingRouteState(forwarder *dynamicForwarder, sandboxName string, port int) string {
	for _, route := range forwarder.Snapshot()["routes"].([]map[string]any) {
		if route["node"] == strings.ToLower(sandboxName) && route["port"] == port {
			return route["state"].(string)
		}
	}
	return ""
}

func forwardingNodeSnapshot(forwarder *dynamicForwarder, sandboxName string) map[string]any {
	for _, node := range forwarder.Snapshot()["nodes"].([]map[string]any) {
		if node["node"] == sandboxName {
			return node
		}
	}
	return nil
}

func forwardingClaimant(t *testing.T, forwarder *dynamicForwarder, port int) string {
	t.Helper()
	for _, entry := range forwarder.Snapshot()["ports"].([]map[string]any) {
		if entry["port"] == port {
			return entry["default_node"].(string)
		}
	}
	t.Fatalf("port %d is not being served; snapshot=%#v", port, forwarder.Snapshot())
	return ""
}

// forwardingHostRequest reaches the generic host listener. A transport error
// here means the listener was closed, which is the failure mode I6 forbids.
func forwardingHostRequest(t *testing.T, port int) (int, string) {
	t.Helper()
	return forwardingRequest(t, port, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}

func forwardingNamedHostRequest(t *testing.T, sandboxName string, port int) (int, string) {
	t.Helper()
	return forwardingRequest(t, port, net.JoinHostPort(strings.ToLower(sandboxName)+".localhost", strconv.Itoa(port)))
}

func forwardingRequest(t *testing.T, port int, host string) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}}
	request, err := http.NewRequest(http.MethodGet, "http://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(port))+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = host
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("request to the host listener failed (a refused connection means the listener was closed): %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	return response.StatusCode, strings.TrimSpace(string(body))
}

func waitForForwardingCondition(t *testing.T, description string, satisfied func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if satisfied() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}
