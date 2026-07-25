package codelima

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

type scriptedDaemonEventConnection struct {
	snapshot daemon.SyncSnapshot
	events   []daemon.Event
	index    int
	closed   bool
}

func (c *scriptedDaemonEventConnection) SubscribeSync(context.Context, []string) (daemon.SyncSnapshot, error) {
	return c.snapshot, nil
}

func (c *scriptedDaemonEventConnection) NextEvent(context.Context) (daemon.Event, error) {
	if c.index >= len(c.events) {
		return daemon.Event{}, io.EOF
	}
	event := c.events[c.index]
	c.index++
	return event, nil
}

func (c *scriptedDaemonEventConnection) Close() error {
	c.closed = true
	return nil
}

func TestDaemonConnectionSupervisorReconnectsAndResynchronizes(t *testing.T) {
	t.Parallel()

	first := &scriptedDaemonEventConnection{
		snapshot: daemon.SyncSnapshot{DaemonEpoch: "epoch", StateSequence: 2},
	}
	second := &scriptedDaemonEventConnection{
		snapshot: daemon.SyncSnapshot{DaemonEpoch: "epoch", StateSequence: 2},
		events: []daemon.Event{{
			Event:         "terminal.dirty",
			DaemonEpoch:   "epoch",
			StateSequence: 3,
		}},
	}
	connections := []daemonEventConnection{first, second}
	var mu sync.Mutex
	dials := 0
	syncs := 0
	handled := 0
	ctx, cancel := context.WithCancel(context.Background())

	err := runDaemonConnectionSupervisor(ctx, daemonConnectionSupervisorOptions{
		Dial: func(context.Context) (daemonEventConnection, error) {
			mu.Lock()
			defer mu.Unlock()
			connection := connections[dials]
			dials++
			return connection, nil
		},
		OnSync: func(context.Context, daemon.SyncSnapshot) error {
			syncs++
			return nil
		},
		OnEvent: func(daemon.Event) {
			handled++
			cancel()
		},
		Sleep: func(context.Context, time.Duration) error { return nil },
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runDaemonConnectionSupervisor() error = %v, want context.Canceled", err)
	}
	if dials != 2 || syncs != 2 || handled != 1 {
		t.Fatalf("supervisor activity = (%d dials, %d syncs, %d events), want (2, 2, 1)", dials, syncs, handled)
	}
	if !first.closed || !second.closed {
		t.Fatalf("connections closed = (%v, %v), want both true", first.closed, second.closed)
	}
}

func TestDaemonConnectionSupervisorResynchronizesOnSequenceGap(t *testing.T) {
	t.Parallel()

	gapped := &scriptedDaemonEventConnection{
		snapshot: daemon.SyncSnapshot{DaemonEpoch: "epoch", StateSequence: 4},
		events: []daemon.Event{{
			Event:         "terminal.dirty",
			DaemonEpoch:   "epoch",
			StateSequence: 6,
		}},
	}
	recovered := &scriptedDaemonEventConnection{
		snapshot: daemon.SyncSnapshot{DaemonEpoch: "epoch", StateSequence: 6},
		events: []daemon.Event{{
			Event:         "terminal.dirty",
			DaemonEpoch:   "epoch",
			StateSequence: 7,
		}},
	}
	connections := []daemonEventConnection{gapped, recovered}
	ctx, cancel := context.WithCancel(context.Background())
	dials := 0
	handled := 0

	err := runDaemonConnectionSupervisor(ctx, daemonConnectionSupervisorOptions{
		Dial: func(context.Context) (daemonEventConnection, error) {
			connection := connections[dials]
			dials++
			return connection, nil
		},
		OnEvent: func(daemon.Event) {
			handled++
			cancel()
		},
		Sleep: func(context.Context, time.Duration) error { return nil },
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runDaemonConnectionSupervisor() error = %v, want context.Canceled", err)
	}
	if dials != 2 || handled != 1 {
		t.Fatalf("gap recovery = (%d dials, %d handled), want (2, 1)", dials, handled)
	}
}
