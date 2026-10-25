//go:build integration

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima"
	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
)

func TestDaemonOverviewTracksCommittedUpdateAndResetsOnRestart(t *testing.T) {
	h := newHarness(t)
	h.run(true, "daemon", "start")
	read := func() daemon.Overview {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client, err := daemonclient.Dial(ctx, daemonclient.Options{Home: h.home, Version: codelima.Version})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close() }()
		var overview daemon.Overview
		if err := client.Call(ctx, "daemon.overview", nil, &overview); err != nil {
			t.Fatal(err)
		}
		if overview.Runtime == nil {
			t.Fatal("daemon did not supply runtime overview")
		}
		return overview
	}
	before := read()
	if !before.Runtime.UpdatedAt.IsZero() || before.Status.Subscribers != 0 {
		t.Fatalf("invented update or subscribers: %+v", before)
	}
	started := time.Now()
	h.run(true, "daemon", "update")
	after := read()
	if after.Status.PID == before.Status.PID || after.Runtime.UpdatedAt.Before(started) || after.Runtime.UpdatedAt.After(time.Now()) {
		t.Fatalf("missing committed update evidence: %+v / %+v", after.Status, after.Runtime)
	}
	h.run(true, "daemon", "stop")
	// daemon.stop acknowledges before ownership has been released. Wait for
	// the request listener to disappear; start also waits for exclusive ownership.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := daemonclient.Ping(context.Background(), h.home, codelima.Version); err != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.run(true, "daemon", "start")
	restarted := read()
	if restarted.Status.PID == after.Status.PID || !restarted.Runtime.UpdatedAt.IsZero() {
		t.Fatalf("ordinary restart claimed a live update: %+v / %+v", restarted.Status, restarted.Runtime)
	}
}
