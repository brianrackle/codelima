package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestServerReadyRunsOnlyWithOwnershipAndBeforeAccepting(t *testing.T) {
	home := daemonServerTestHome(t)
	entered, release := make(chan context.Context, 1), make(chan struct{})
	server := NewServer(Config{Home: home, Version: "test", Handler: testHandler{}, Ready: func(ctx context.Context) {
		entered <- ctx
		select {
		case <-release:
		case <-ctx.Done():
		}
	}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() { cancel(); <-done }()
	var workerCtx context.Context
	select {
	case workerCtx = <-entered:
	case <-time.After(time.Second):
		t.Fatal("ready callback was not called")
	}
	if server.accepting.Load() {
		t.Fatal("clients were admitted before ready initialization")
	}
	var losingCalls atomic.Int32
	loser := NewServer(Config{Home: home, Version: "test", Handler: testHandler{}, Ready: func(context.Context) { losingCalls.Add(1) }})
	if err := loser.Run(ctx); err == nil || losingCalls.Load() != 0 {
		t.Fatal("another startup ran producers without owning the daemon lock", err)
	}
	close(release)
	server.Stop()
	select {
	case <-workerCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("server stop did not cancel its ready workers")
	}
}
