package codelima

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"go.rockorager.dev/vaxis"
)

type portsBlockingHost struct {
	*daemonHost
	listGate, pinGate chan struct{}
	entered           chan string
	pins              atomic.Int32
}

func (h *portsBlockingHost) Handle(ctx context.Context, client daemon.ClientContext, method string, raw json.RawMessage) (any, error) {
	if method == "ports.list" && h.listGate != nil {
		h.entered <- method
		select {
		case <-h.listGate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	result, err := h.daemonHost.Handle(ctx, client, method, raw)
	if method == "ports.pin" {
		h.pins.Add(1)
		h.entered <- method
		if h.pinGate != nil {
			select {
			case <-h.pinGate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return result, err
}

func portsAsyncFixture(t *testing.T, blockPin bool) (*vaxisTUIApp, *portsBlockingHost, chan vaxis.Event, func()) {
	t.Helper()
	service, _ := newHandoffTestService(t)
	alpha := saveForwardingTestNode(t, service, "alpha")
	beta := saveForwardingTestNode(t, service, "beta")
	port := reserveDualLoopbackPort(t)
	f := newTestDynamicForwarder(service, &fakeForwardingPeerFactory{peers: map[string]*controllableForwardingPeer{
		alpha.SandboxName: {ports: []int{port}}, beta.SandboxName: {ports: []int{port}},
	}})
	f.policyPath = filepath.Join(service.cfg.MetadataRoot, "_daemon", "forwarding.json")
	f.reconcile(context.Background())
	h := &portsBlockingHost{daemonHost: newDaemonHost(service), entered: make(chan string, 8)}
	h.forwarder = f
	gate := make(chan struct{})
	if blockPin {
		h.pinGate = gate
	} else {
		h.listGate = gate
	}
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	ctx, cancel := context.WithCancel(context.Background())
	server := daemon.NewServer(daemon.Config{Home: service.cfg.MetadataRoot, Version: Version, Handler: h})
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() { release(); cancel(); <-done })
	waitForDaemonPing(t, service.cfg.MetadataRoot)
	client, err := daemonclient.Dial(ctx, daemonclient.Options{Home: service.cfg.MetadataRoot, Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	service.daemonClient = client
	a, _ := newTestManager(t, 80, 24)
	a.service, a.view = service, tuiViewPorts
	a.ports.snapshot, a.ports.loaded = f.portsSnapshot(), true
	a.reconcilePortSelection()
	events := make(chan vaxis.Event, 8)
	a.postEvent = func(e vaxis.Event) { events <- e }
	return a, h, events, release
}

func nextPortsEvent(t *testing.T, events <-chan vaxis.Event) vaxis.Event {
	t.Helper()
	select {
	case e := <-events:
		return e
	case <-time.After(7 * time.Second):
		t.Fatal("Ports worker failed to respect its deadline")
		return nil
	}
}

func TestTUIPortsRefreshCancelsOnNavigationAndPreservesCache(t *testing.T) {
	a, h, events, release := portsAsyncFixture(t, false)
	a.startPortsLoad(true)
	select {
	case <-h.entered:
	case <-time.After(time.Second):
		t.Fatal("query did not reach daemon")
	}
	generation := a.ports.generation
	a.startPortsLoad(true)
	if generation != a.ports.generation {
		t.Fatal("overlapping query admitted")
	}
	a.view = tuiViewSystem
	a.startPortsLoad(false)
	e := nextPortsEvent(t, events).(tuiPortsEvent)
	if e.err == nil {
		t.Fatal("navigation did not cancel query")
	}
	a.applyPortsData(e)
	if len(a.ports.snapshot.Rows) != 2 || a.ports.loading {
		t.Fatal("canceled completion replaced cache")
	}
	release()
	a.view = tuiViewPorts
	a.startPortsLoad(true)
	a.applyPortsData(nextPortsEvent(t, events).(tuiPortsEvent))
	if a.ports.err != nil || !a.ports.loaded {
		t.Fatalf("query did not recover: %v", a.ports.err)
	}
}

func TestTUIPortsRefreshContinuesFromPortsToFleet(t *testing.T) {
	a, h, events, release := portsAsyncFixture(t, false)
	a.startPortsLoad(true)
	select {
	case <-h.entered:
	case <-time.After(time.Second):
		t.Fatal("query did not reach daemon")
	}
	generation := a.ports.generation
	a.view = tuiViewNodes
	a.startPortsLoad(false)
	if generation != a.ports.generation || !a.ports.loading {
		t.Fatal("fleet canceled the shared forwarding query")
	}
	release()
	a.applyPortsData(nextPortsEvent(t, events).(tuiPortsEvent))
	if a.ports.err != nil || a.ports.loading || !a.ports.loaded {
		t.Fatalf("fleet did not consume completion: %v", a.ports.err)
	}
}

func TestTUIPortsLostPinReplyDoesNotRetryOrStealNavigation(t *testing.T) {
	a, h, events, release := portsAsyncFixture(t, true)
	a.ports.selected = a.ports.snapshot.Rows[1].ID
	chosen := a.ports.snapshot.Rows[1]
	a.pinSelectedPort()
	select {
	case <-h.entered:
	case <-time.After(time.Second):
		t.Fatal("pin did not reach daemon")
	}
	a.pinSelectedPort()
	if h.pins.Load() != 1 {
		t.Fatal("single-flight pin was submitted twice")
	}
	a.view = tuiViewNodes
	e := nextPortsEvent(t, events).(tuiPortPinEvent)
	if e.err == nil {
		t.Fatal("lost reply was acknowledged")
	}
	a.finishPortPin(e)
	if a.view != tuiViewNodes || a.ports.pinning || h.pins.Load() != 1 {
		t.Fatal("late pin reply stole navigation or retried mutation")
	}
	release()
	a.view = tuiViewPorts
	a.startPortsLoad(true)
	a.applyPortsData(nextPortsEvent(t, events).(tuiPortsEvent))
	for _, row := range a.ports.snapshot.Rows {
		if row.OwnerID != chosen.NodeID || row.OwnerReason != "pinned" {
			t.Fatalf("refresh lost the committed pin: %+v", row)
		}
	}
}
