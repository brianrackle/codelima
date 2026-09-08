//go:build darwin || linux

package codelima

import (
	"testing"
	"time"
)

func TestRendererExecutionDeadlinesFollowWireOrderAndCannotBeExtendedByNewCalls(t *testing.T) {
	now := time.Now()
	link := &rendererLink{pending: map[uint64]rendererPending{
		// Admission IDs are reversed by priority dispatch: ID2 is on the wire
		// first, so a later long operation must not conceal its expired budget.
		1: {StartedAt: now.Add(-time.Second), Deadline: now.Add(time.Hour), Budget: time.Second, DispatchOrder: 2, Method: "checkpoint"},
		2: {StartedAt: now, Deadline: now.Add(-time.Millisecond), Budget: time.Millisecond, DispatchOrder: 1, Method: "graphics"},
	}}
	if err := link.HealthError(time.Second); err == nil {
		t.Fatal("later long operation extended the active short operation")
	}
	link.completePendingLocked(2)
	if err := link.HealthError(time.Second); err != nil {
		t.Fatalf("newly active operation did not receive its own budget: %v", err)
	}
	if remaining := time.Until(link.pending[1].Deadline); remaining <= 0 || remaining > time.Second {
		t.Fatalf("promoted execution deadline = %s", remaining)
	}
	active := link.pending[1]
	active.Deadline = now.Add(-time.Millisecond)
	link.pending[1] = active
	link.pending[3] = rendererPending{StartedAt: now, Deadline: now.Add(time.Hour), Budget: time.Hour, DispatchOrder: 3, Method: "read"}
	if err := link.HealthError(time.Second); err == nil {
		t.Fatal("new read admission hid an expired active checkpoint")
	}
}

func TestRendererQueuedShortDeadlineCannotExpireActiveLongOperation(t *testing.T) {
	now := time.Now()
	link := &rendererLink{pending: map[uint64]rendererPending{
		1: {StartedAt: now.Add(-time.Second), Deadline: now.Add(time.Second), Budget: 4 * time.Second, DispatchOrder: 1, Method: "read"},
		2: {StartedAt: now.Add(-time.Second), Deadline: now.Add(-time.Millisecond), Budget: time.Second, DispatchOrder: 2, Method: "graphics"},
	}}
	if err := link.HealthError(time.Second); err != nil {
		t.Fatalf("queued command expired active native work: %v", err)
	}
	link.completePendingLocked(1)
	if err := link.HealthError(time.Second); err != nil {
		t.Fatalf("queue wait consumed native execution budget: %v", err)
	}
}
