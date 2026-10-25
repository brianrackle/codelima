package codelima

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"go.rockorager.dev/vaxis"
)

func TestTUISystemDaemonRefreshIsSingleFlightBoundedAndCancelledOnNavigation(t *testing.T) {
	service, _ := newHandoffTestService(t)
	host := &blockingOverviewHost{daemonHost: newDaemonHost(service), release: make(chan struct{}), entered: make(chan struct{}, 4)}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(host.release) }) }
	server := daemon.NewServer(daemon.Config{Home: service.cfg.MetadataRoot, Version: Version, Handler: host})
	ctx, cancel := context.WithCancel(context.Background())
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
	a.service, a.view = service, tuiViewSystem
	events := make(chan vaxis.Event, 4)
	a.postEvent = func(e vaxis.Event) { events <- e }
	receive := func() tuiDaemonOverviewEvent {
		t.Helper()
		select {
		case e := <-events:
			return e.(tuiDaemonOverviewEvent)
		case <-time.After(7 * time.Second):
			t.Fatal("overview worker did not respect its deadline")
			return tuiDaemonOverviewEvent{}
		}
	}
	a.startDaemonOverview(true)
	select {
	case <-host.entered:
	case <-time.After(time.Second):
		t.Fatal("request did not reach daemon")
	}
	generation := a.systemDaemon.generation
	a.startDaemonOverview(true)
	if a.systemDaemon.generation != generation || host.calls.Load() != 1 {
		t.Fatal("overlapping refresh admitted")
	}
	e := receive()
	if e.err == nil {
		t.Fatal("blocked status request had no deadline")
	}
	a.applyDaemonOverview(e)
	a.startDaemonOverview(true)
	select {
	case <-host.entered:
	case <-time.After(time.Second):
		t.Fatal("retry did not reach daemon")
	}
	a.view = tuiViewNodes
	a.startDaemonOverview(false)
	e = receive()
	if e.err == nil || a.systemDaemon.loading {
		t.Fatal("hidden request not cancelled")
	}
	a.applyDaemonOverview(e)
	if !a.systemDaemon.checkedAt.IsZero() {
		t.Fatal("cancelled request installed data")
	}
	release()
	a.view = tuiViewSystem
	a.startDaemonOverview(true)
	a.applyDaemonOverview(receive())
	if a.systemDaemon.err != nil || a.systemDaemon.checkedAt.IsZero() {
		t.Fatalf("failed to recover: %+v", a.systemDaemon)
	}
	generation = a.systemDaemon.generation
	a.startDaemonOverview(false)
	if generation != a.systemDaemon.generation {
		t.Fatal("periodic refresh ignored cache interval")
	}
}

type blockingOverviewHost struct {
	*daemonHost
	release, entered chan struct{}
	calls            atomic.Int32
}

func (h *blockingOverviewHost) Overview() daemon.RuntimeOverview {
	h.calls.Add(1)
	h.entered <- struct{}{}
	<-h.release
	return h.daemonHost.Overview()
}

func TestTUISystemDaemonObservedStatusAndDetailsAtBothSizes(t *testing.T) {
	for _, size := range [][2]int{{150, 38}, {80, 24}} {
		a, _ := newTestManager(t, size[0], size[1])
		a.view, a.systemPanel = tuiViewSystem, tuiSystemDaemon
		a.systemDaemon.generation = 1
		a.applyDaemonOverview(tuiDaemonOverviewEvent{generation: 1, checkedAt: time.Now(), clientID: "this-tui", overview: daemon.Overview{
			Status: daemon.Status{Running: true, PID: 123, Version: "0.4.0", Protocol: daemon.ProtocolVersion, TerminalCount: 7, Subscribers: 2, InputOwner: "this-tui", StartedAt: time.Now().Add(-time.Hour)},
			Runtime: &daemon.RuntimeOverview{
				UpdatedAt:  time.Now().Add(-time.Minute),
				Forwarding: daemon.ForwardingOverview{Enabled: true, Authorized: true, Listeners: 4, Ports: 3, DegradedPorts: 1, LapsedRoutes: 2, IntervalSeconds: 1, InventoryError: strings.Repeat("long path 世界 ", 50) + "final failure"},
				Reclaim:    daemon.ReclaimOverview{Enabled: true, Supported: false},
			},
		}})
		a.draw()
		screen := renderedScreenText(t, a.vx, size[0], size[1])
		for _, want := range []string{"pid 123", "0.4.0", "2 subscribers", "this TUI holds seat", "4 listeners", "1 port degraded", "2 routes lapsed", "unsupported on host", "Enter details"} {
			if !strings.Contains(screen, want) {
				t.Fatalf("missing %q at %v:\n%s", want, size, screen)
			}
		}
		a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
		view, ok := a.overlay.(*tuiTextView)
		if !ok || !strings.Contains(view.text, "final failure") {
			t.Fatal("full diagnostic details missing")
		}
		a.overlay = nil
		for _, c := range a.systemDaemon.controls {
			if c.action == "details" {
				a.handleMouse(vaxis.Mouse{EventType: vaxis.EventPress, Button: vaxis.MouseLeftButton, Col: c.rect.col, Row: c.rect.row})
			}
		}
		if _, ok := a.overlay.(*tuiTextView); !ok {
			t.Fatal("mouse details unavailable")
		}
	}
}

func TestTUISystemDaemonRetainsStaleEvidenceAndRejectsLateReplies(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.view, a.systemPanel = tuiViewSystem, tuiSystemDaemon
	a.systemDaemon.generation = 2
	a.applyDaemonOverview(tuiDaemonOverviewEvent{generation: 2, checkedAt: time.Now(), overview: daemon.Overview{Status: daemon.Status{Running: true, PID: 555}}})
	a.systemDaemon.generation++
	a.applyDaemonOverview(tuiDaemonOverviewEvent{generation: 2, overview: daemon.Overview{Status: daemon.Status{PID: 999}}})
	a.applyDaemonOverview(tuiDaemonOverviewEvent{generation: 3, err: errors.New("connection failed")})
	a.draw()
	screen := renderedScreenText(t, a.vx, 80, 24)
	for _, want := range []string{"stale", "connection failed", "pid 555", "runtime summary unavailable"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("missing %q:\n%s", want, screen)
		}
	}
	a.cancelDaemonOverview()
	a.applyDaemonOverview(tuiDaemonOverviewEvent{generation: 3, checkedAt: time.Now()})
	if a.systemDaemon.overview.Status.PID != 555 {
		t.Fatal("cancelled worker erased evidence")
	}
	a.systemDaemon.err = nil
	a.daemonDisconnected = true
	a.draw()
	if !strings.Contains(renderedScreenText(t, a.vx, 80, 24), "stale") {
		t.Fatal("disconnected data presented as current")
	}
}

func TestTUISystemDaemonFirstFailureAndNoRuntimeAreExplicit(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.view, a.systemPanel = tuiViewSystem, tuiSystemDaemon
	a.systemDaemon.err = errors.New(strings.Repeat("first error ", 100) + "failure tail")
	a.openDaemonDetails()
	if v, ok := a.overlay.(*tuiTextView); !ok || !strings.Contains(v.text, "failure tail") {
		t.Fatal("first failure details unavailable")
	}
	a.overlay = nil
	a.systemDaemon.err = nil
	a.applyDaemonOverview(tuiDaemonOverviewEvent{checkedAt: time.Now(), overview: daemon.Overview{Status: daemon.Status{Running: true}, Runtime: &daemon.RuntimeOverview{}}})
	a.draw()
	screen := renderedScreenText(t, a.vx, 80, 24)
	for _, want := range []string{"forwarding  off", "telemetry   off", "unsupported on host", "no seat owner"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("missing %q:\n%s", want, screen)
		}
	}
}
