package codelima

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

type publishedTestPeer struct {
	*controllableForwardingPeer
	dials chan string
}

func (p *publishedTestPeer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	p.dials <- address
	return p.controllableForwardingPeer.DialContext(ctx, network, address)
}

type publishedTestFactory struct{ peer forwardingPeer }

func (f publishedTestFactory) Prepare(context.Context) error { return nil }
func (f publishedTestFactory) Connect(context.Context, Node) (forwardingPeer, error) {
	return f.peer, nil
}

func TestPublishedTCPRemappingHalfCloseAndReadiness(t *testing.T) {
	guest, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = guest.Close() })
	done := make(chan error, 1)
	go func() {
		conn, err := guest.Accept()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		data, err := io.ReadAll(conn)
		if err == nil {
			_, err = conn.Write(append([]byte("received:"), data...))
		}
		done <- err
	}()
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "database")
	port := reserveDualLoopbackPort(t)
	node.Ports = []string{fmt.Sprintf("%d:5432", port)}
	if err := service.store.SaveNode(node, BootstrapState{}); err != nil {
		t.Fatal(err)
	}
	peer := &publishedTestPeer{controllableForwardingPeer: &controllableForwardingPeer{address: guest.Addr().String(), ports: []int{port}}, dials: make(chan string, 4)}
	f := newTestDynamicForwarder(service, publishedTestFactory{peer})
	t.Cleanup(func() { _ = f.Close() })
	f.reconcile(context.Background())
	conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte{0, 255, 42}); err != nil {
		t.Fatal(err)
	}
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(conn)
	if err != nil || string(data) != "received:\x00\xff*" {
		t.Fatalf("raw response %q: %v", data, err)
	}
	if address := <-peer.dials; address != "127.0.0.1:5432" {
		t.Fatalf("guest address = %s", address)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if status := f.Snapshot()["ports"].([]map[string]any)[0]["status"]; status != "conflicted" {
		t.Fatalf("HTTP on reserved TCP port claims %v", status)
	}
	node.Status = NodeStatusFailed
	if err := service.store.SaveNode(node, BootstrapState{}); err != nil {
		t.Fatal(err)
	}
	f.reconcile(context.Background())
	unexpected, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err == nil {
		_ = unexpected.Close()
		t.Fatal("failed node kept its TCP listener")
	}
}

func TestPublishedTCPLegacyNodeDoesNotCompeteWithLima(t *testing.T) {
	h := newForwardingHarness(t, "legacy")
	node := h.node
	node.DaemonPortForwarding = false
	node.Ports = []string{fmt.Sprintf("%d:5432", reserveDualLoopbackPort(t))}
	if err := h.service.store.SaveNode(node, BootstrapState{}); err != nil {
		t.Fatal(err)
	}
	h.reconcile()
	if len(h.forwarder.tcpServers) != 0 {
		t.Fatal("daemon tried to take over a running legacy mapping")
	}
}

type publishedBlockingPeer struct {
	*controllableForwardingPeer
	entered chan struct{}
}

func (p *publishedBlockingPeer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	close(p.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestPublishedTCPCloseCancelsInFlightDial(t *testing.T) {
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "blocked-dial")
	port := reserveDualLoopbackPort(t)
	node.Ports = []string{fmt.Sprintf("%d:1234", port)}
	if err := service.store.SaveNode(node, BootstrapState{}); err != nil {
		t.Fatal(err)
	}
	peer := &publishedBlockingPeer{controllableForwardingPeer: &controllableForwardingPeer{}, entered: make(chan struct{})}
	f := newTestDynamicForwarder(service, publishedTestFactory{peer})
	t.Cleanup(func() { _ = f.Close() })
	f.reconcile(context.Background())
	conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	select {
	case <-peer.entered:
	case <-time.After(time.Second):
		t.Fatal("relay did not dial")
	}
	done := make(chan error, 1)
	go func() { done <- f.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("closing relay did not cancel guest dial")
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("relay kept accepted connection")
	}
}

func TestPublishedTCPBindConflictRetriesAndConfirmedStopReleases(t *testing.T) {
	h := newForwardingHarness(t, "retry-tcp")
	h.peer.ports = nil // Isolate published TCP from HTTP degradation counts.
	port := reserveDualLoopbackPort(t)
	occupied, err := net.Listen("tcp6", fmt.Sprintf("[::1]:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = occupied.Close() }()
	node := h.node
	node.Ports = []string{fmt.Sprintf("%d:5432", port)}
	if err := h.service.store.SaveNode(node, BootstrapState{}); err != nil {
		t.Fatal(err)
	}
	h.reconcile()
	rows := h.forwarder.Snapshot()["published_tcp"].([]map[string]any)
	if len(rows) != 1 || rows[0]["status"] != "conflicted" {
		t.Fatalf("bind conflict: %v", rows)
	}
	if h.forwarder.Snapshot()["degraded"] != true || h.forwarder.overview().DegradedPorts != 1 {
		t.Fatal("diagnostic/System summary omitted TCP bind failure")
	}
	probe, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("partial IPv4 bind leaked: %v", err)
	}
	_ = probe.Close()
	_ = occupied.Close()
	h.reconcile()
	if row := h.forwarder.Snapshot()["published_tcp"].([]map[string]any)[0]; row["status"] != "serving" {
		t.Fatalf("recovery: %v", row)
	}
	if h.forwarder.Snapshot()["degraded"] != false || h.forwarder.overview().DegradedPorts != 0 {
		t.Fatal("recovered TCP port retained degraded summary")
	}
	h.peer.failScans(errors.New("guest disconnected"))
	h.reconcile()
	h.clock.advance(forwardingRouteGrace + time.Second)
	h.reconcile()
	if row := h.forwarder.Snapshot()["published_tcp"].([]map[string]any)[0]; row["status"] != "retrying" {
		t.Fatalf("degraded: %v", row)
	}
	if h.forwarder.Snapshot()["degraded"] != true || h.forwarder.overview().DegradedPorts != 1 {
		t.Fatal("diagnostic/System summary omitted degraded TCP tunnel")
	}
	probe, err = net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err == nil {
		_ = probe.Close()
		t.Fatal("degraded relay released its host listener")
	}
	h.setObservationStatus(ObservationStopped)
	h.reconcile()
	h.clock.advance(forwardingStopGrace + time.Second)
	h.reconcile()
	if rows := h.forwarder.Snapshot()["published_tcp"].([]map[string]any); len(rows) != 0 {
		t.Fatalf("confirmed stop retained TCP: %v", rows)
	}
}
