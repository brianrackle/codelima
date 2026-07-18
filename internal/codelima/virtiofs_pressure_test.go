package codelima

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestVirtioFSPressureControllerTriggersAtThresholdAndHonorsCooldown(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	sampler := &queuedHostFilePressureSampler{samples: []hostFilePressure{
		{used: 59, limit: 100},
		{used: 60, limit: 100},
		{used: 20, limit: 100},
		{used: 80, limit: 100},
		{used: 80, limit: 100},
		{used: 30, limit: 100},
	}}
	reclaims := 0
	controller := newVirtioFSPressureController(
		sampler.sample,
		func(context.Context) (int, error) {
			reclaims++
			return 1, nil
		},
		virtioFSPressureOptions{thresholdPercent: 60, cooldown: 30 * time.Second},
	)
	controller.now = func() time.Time { return now }

	controller.check(context.Background())
	if reclaims != 0 {
		t.Fatalf("reclaims below threshold = %d, want 0", reclaims)
	}

	controller.check(context.Background())
	if reclaims != 1 {
		t.Fatalf("reclaims at threshold = %d, want 1", reclaims)
	}
	state := controller.Snapshot()
	if state.LastReclaimedNodes != 1 || state.LastReleasedFiles != 40 {
		t.Fatalf("snapshot after reclaim = %+v", state)
	}

	now = now.Add(15 * time.Second)
	controller.check(context.Background())
	if reclaims != 1 {
		t.Fatalf("reclaims during cooldown = %d, want 1", reclaims)
	}

	now = now.Add(16 * time.Second)
	controller.check(context.Background())
	if reclaims != 2 {
		t.Fatalf("reclaims after cooldown = %d, want 2", reclaims)
	}
}

func TestVirtioFSPressureControllerUsesCooldownWhenReclaimIsIneffective(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	sampler := &queuedHostFilePressureSampler{samples: []hostFilePressure{
		{used: 70, limit: 100},
		{used: 70, limit: 100},
		{used: 75, limit: 100},
		{used: 75, limit: 100},
		{used: 30, limit: 100},
	}}
	reclaims := 0
	controller := newVirtioFSPressureController(
		sampler.sample,
		func(context.Context) (int, error) {
			reclaims++
			return 1, nil
		},
		virtioFSPressureOptions{thresholdPercent: 60, cooldown: 30 * time.Second},
	)
	controller.now = func() time.Time { return now }

	controller.check(context.Background())
	if reclaims != 1 {
		t.Fatalf("initial reclaims = %d, want 1", reclaims)
	}
	state := controller.Snapshot()
	if state.LastReleasedFiles != 0 || state.NextAttemptAt.Sub(now) != 30*time.Second {
		t.Fatalf("ineffective reclaim state = %+v", state)
	}

	now = now.Add(15 * time.Second)
	controller.check(context.Background())
	if reclaims != 1 {
		t.Fatalf("reclaims during cooldown = %d, want 1", reclaims)
	}

	now = now.Add(16 * time.Second)
	controller.check(context.Background())
	if reclaims != 2 {
		t.Fatalf("reclaims after cooldown = %d, want 2", reclaims)
	}
}

func TestVirtioFSPressureControllerUsesShortCooldownWhenNoMountedNodesRun(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	sampler := &queuedHostFilePressureSampler{samples: []hostFilePressure{{used: 70, limit: 100}}}
	controller := newVirtioFSPressureController(
		sampler.sample,
		func(context.Context) (int, error) { return 0, nil },
		virtioFSPressureOptions{thresholdPercent: 60, cooldown: 30 * time.Second},
	)
	controller.now = func() time.Time { return now }

	controller.check(context.Background())
	state := controller.Snapshot()
	if state.NextAttemptAt.Sub(now) != 30*time.Second {
		t.Fatalf("no-node next attempt = %v, want 30-second cooldown", state.NextAttemptAt)
	}
}

func TestVirtioFSPressureControllerRecordsSamplingAndReclaimErrors(t *testing.T) {
	t.Parallel()
	sampleErr := errors.New("sample failed")
	controller := newVirtioFSPressureController(
		func() (hostFilePressure, error) { return hostFilePressure{}, sampleErr },
		func(context.Context) (int, error) { return 0, errors.New("unexpected reclaim") },
		virtioFSPressureOptions{thresholdPercent: 60, cooldown: 30 * time.Second},
	)
	controller.check(context.Background())
	if got := controller.Snapshot().LastError; got != sampleErr.Error() {
		t.Fatalf("sampling error = %q, want %q", got, sampleErr)
	}

	reclaimErr := errors.New("reclaim failed")
	controller.sample = func() (hostFilePressure, error) { return hostFilePressure{used: 70, limit: 100}, nil }
	controller.reclaim = func(context.Context) (int, error) { return 0, reclaimErr }
	controller.check(context.Background())
	state := controller.Snapshot()
	if got := state.LastError; got != reclaimErr.Error() {
		t.Fatalf("reclaim error = %q, want %q", got, reclaimErr)
	}
	if state.NextAttemptAt.Sub(state.LastAttemptAt) != 30*time.Second {
		t.Fatalf("reclaim error cooldown = %v, want 30 seconds", state.NextAttemptAt.Sub(state.LastAttemptAt))
	}
}

func TestVirtioFSPressureControllerCloseCancelsActiveReclaim(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	controller := newVirtioFSPressureController(
		func() (hostFilePressure, error) { return hostFilePressure{used: 70, limit: 100}, nil },
		func(ctx context.Context) (int, error) {
			close(entered)
			<-ctx.Done()
			return 0, ctx.Err()
		},
		virtioFSPressureOptions{thresholdPercent: 60},
	)
	controller.Start(context.Background())
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("controller did not begin reclaim")
	}

	closed := make(chan struct{})
	go func() {
		controller.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("controller Close did not cancel active reclaim")
	}
	if got := controller.Snapshot().LastError; got != context.Canceled.Error() {
		t.Fatalf("canceled reclaim error = %q", got)
	}
}

func TestDaemonSnapshotReportsVirtioFSReclaimState(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	controller := newVirtioFSPressureController(
		func() (hostFilePressure, error) { return hostFilePressure{}, nil },
		func(context.Context) (int, error) { return 0, nil },
		virtioFSPressureOptions{thresholdPercent: 20},
	)
	controller.state.UsedFiles = 240
	controller.state.LimitFiles = 400
	controller.state.LastReclaimedNodes = 2
	controller.state.LastReleasedFiles = 123

	host := newDaemonHost(service)
	host.virtioFSSupported = true
	host.virtioFSPressure = controller
	result, err := host.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	snapshot := result.(map[string]any)
	status := snapshot["virtiofs_reclaim"].(map[string]any)
	if status["enabled"] != true || status["supported"] != true || status["threshold_percent"] != 20 {
		t.Fatalf("VirtioFS reclaim status = %#v", status)
	}
	if status["used_files"] != uint64(240) || status["limit_files"] != uint64(400) || status["last_reclaimed_nodes"] != 2 || status["last_released_files"] != uint64(123) {
		t.Fatalf("VirtioFS reclaim metrics = %#v", status)
	}
}

func TestHostFilePressureThresholdRoundsUp(t *testing.T) {
	t.Parallel()
	if (hostFilePressure{used: 1, limit: 3}).atOrAbove(60) {
		t.Fatal("one of three files unexpectedly reached a 60 percent threshold")
	}
	if !(hostFilePressure{used: 2, limit: 3}).atOrAbove(60) {
		t.Fatal("two of three files did not reach a 60 percent threshold")
	}
	if (hostFilePressure{used: 1, limit: 0}).atOrAbove(60) {
		t.Fatal("zero limit unexpectedly reached the threshold")
	}
}

func TestVirtioFSPressureControllerDefaults(t *testing.T) {
	t.Parallel()
	controller := newVirtioFSPressureController(
		func() (hostFilePressure, error) { return hostFilePressure{}, nil },
		func(context.Context) (int, error) { return 0, nil },
		virtioFSPressureOptions{},
	)
	if controller.options.cooldown != 30*time.Second {
		t.Fatalf("default cooldown = %v, want 30 seconds", controller.options.cooldown)
	}
	if controller.options.thresholdPercent != 20 {
		t.Fatalf("default threshold = %d, want 20", controller.options.thresholdPercent)
	}
}

type queuedHostFilePressureSampler struct {
	samples []hostFilePressure
}

func (s *queuedHostFilePressureSampler) sample() (hostFilePressure, error) {
	if len(s.samples) == 0 {
		return hostFilePressure{}, errors.New("no queued pressure sample")
	}
	sample := s.samples[0]
	s.samples = s.samples[1:]
	return sample, nil
}
