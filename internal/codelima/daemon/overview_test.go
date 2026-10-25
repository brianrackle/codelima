package daemon

import (
	"context"
	"testing"
)

func TestDaemonOverviewCountsSubscribedFrontendsWithoutReadingTerminalGrids(t *testing.T) {
	h := overviewTestHandler{}
	s := NewServer(Config{Handler: h})
	s.clients = map[string]*clientConn{
		"request":     {clientInstanceID: "first"},
		"events":      {clientInstanceID: "first"},
		"replacement": {clientInstanceID: "first"},
		"observer":    {clientInstanceID: "second"},
		"probe":       {clientInstanceID: "probe"},
	}
	for _, id := range []string{"events", "replacement", "observer"} {
		s.clients[id].subscribed.Store(true)
	}
	s.input = inputLease{clientInstanceID: "first"}
	value, err := s.handle(context.Background(), s.clients["request"], Request{Method: "daemon.overview"}, false)
	if err != nil {
		t.Fatal(err)
	}
	got := value.(Overview)
	if got.Status.Subscribers != 2 || got.Status.InputOwner != "first" || !got.Status.Running {
		t.Fatalf("incorrect observed clients: %+v", got.Status)
	}
	if got.Runtime == nil || got.Runtime.Forwarding.Listeners != 7 {
		t.Fatalf("lost runtime summary: %+v", got)
	}
	s.clients["observer"].subscribed.Store(false)
	if s.status().Subscribers != 1 {
		t.Fatal("unsubscribed clients counted as attached frontends")
	}
}

func TestDaemonOverviewReportsUnavailableRuntimeForOtherHandlers(t *testing.T) {
	s := NewServer(Config{Handler: testHandler{}})
	value, err := s.handle(context.Background(), &clientConn{}, Request{Method: "daemon.overview"}, false)
	if err != nil || value.(Overview).Runtime != nil {
		t.Fatalf("unavailable runtime summary must not be fabricated: %v, %v", value, err)
	}
}

type overviewTestHandler struct{ testHandler }

func (overviewTestHandler) Overview() RuntimeOverview {
	return RuntimeOverview{Forwarding: ForwardingOverview{Enabled: true, Listeners: 7}}
}

func (overviewTestHandler) Snapshot(context.Context) (any, error) {
	panic("overview must not collect terminal grids or renderer diagnostics")
}
