package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
)

func TestDaemonControlStrictUpdateDoesNotRestartOrTakeSeat(t *testing.T) {
	service, _ := newHandoffTestService(t)
	host := &controlTestHost{daemonHost: newDaemonHost(service)}
	server := daemon.NewServer(daemon.Config{Home: service.cfg.MetadataRoot, Version: Version, Handler: host})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitForDaemonPing(t, service.cfg.MetadataRoot)
	owner, err := daemonclient.Dial(ctx, daemonclient.Options{Home: service.cfg.MetadataRoot, Version: Version, WantInput: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close() }()
	_, err = updateDaemon(ctx, service, daemonUpdateOptions{})
	if !isUnsupportedLegacyHandoffTransport(err) || host.updates.Load() != 1 {
		t.Fatalf("unexpected strict-update outcome: %v / %d", err, host.updates.Load())
	}
	var status daemon.Status
	if err := owner.Call(ctx, "daemon.status", nil, &status); err != nil {
		t.Fatal("strict update stopped daemon:", err)
	}
	if status.InputOwner != owner.HelloSnapshot().ClientID {
		t.Fatal("update stole the TUI input seat")
	}
}

func TestDaemonControlLostUpdateReplyIsNotResubmitted(t *testing.T) {
	service, _ := newHandoffTestService(t)
	host := &controlTestHost{daemonHost: newDaemonHost(service)}
	server := daemon.NewServer(daemon.Config{Home: service.cfg.MetadataRoot, Version: Version, Handler: host})
	host.interrupt = server.Stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitForDaemonPing(t, service.cfg.MetadataRoot)
	_, err := updateDaemon(ctx, service, daemonUpdateOptions{})
	var delivery *daemonclient.DeliveryError
	if !errors.As(err, &delivery) || delivery.Outcome != daemonclient.DeliveryOutcomeUnknown {
		t.Fatalf("lost mutation reply was not reported as unknown: %v", err)
	}
	if host.updates.Load() != 1 {
		t.Fatalf("ambiguous update resubmitted %d times", host.updates.Load())
	}
}

type controlTestHost struct {
	*daemonHost
	updates   atomic.Int32
	interrupt func()
}

func (h *controlTestHost) Handle(ctx context.Context, client daemon.ClientContext, method string, params json.RawMessage) (any, error) {
	if method != "daemon.update" {
		return h.daemonHost.Handle(ctx, client, method, params)
	}
	h.updates.Add(1)
	if h.interrupt != nil {
		go h.interrupt()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, daemon.Error("PreconditionFailed", "unsupported live handoff", ExitPreconditionFailed, map[string]any{"handoff_transport_unsupported": true})
}
