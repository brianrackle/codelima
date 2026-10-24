package codelima

import (
	"testing"
	"time"
)

func TestNodeMetricsParsesCoresCacheSwapAndUptime(t *testing.T) {
	sample := parseForwardingGuestObservation([]byte("cpu 100 0 0 100 0 0 0 0\ncpu0 50 0 0 50 0 0 0 0\ncpu1 50 0 0 50 0 0 0 0\ncodelima-memory 1000 600\ncodelima-memory-detail 600 200 100 75\ncodelima-uptime 123.25\n"))
	if len(sample.CPUCores) != 2 || sample.Details.MemoryAvailableBytes == nil || *sample.Details.MemoryAvailableBytes != 600*1024 || sample.Details.MemoryCachedBytes == nil || *sample.Details.MemoryCachedBytes != 200*1024 || sample.Details.SwapUsedBytes == nil || *sample.Details.SwapUsedBytes != 25*1024 || sample.Details.UptimeSeconds == nil || *sample.Details.UptimeSeconds != 123.25 {
		t.Fatalf("invalid detail parse: %+v", sample)
	}
	for _, bad := range []string{"codelima-memory-detail 1 2 3 4", "codelima-memory-detail -1 0 0 0", "codelima-uptime NaN", "codelima-uptime -1", "codelima-uptime +Inf"} {
		got := parseForwardingGuestObservation([]byte(bad))
		if got.Details.SwapUsedBytes != nil || got.Details.UptimeSeconds != nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestForwarderMetricsComputesEachCoreAndClearsDetails(t *testing.T) {
	now := time.Now().UTC()
	f := &dynamicForwarder{now: func() time.Time { return now }}
	before := forwardingGuestObservation{CPUCores: map[int]guestCPUCounters{0: {Total: 100, Idle: 50}, 1: {Total: 100, Idle: 50}}}
	f.recordNodeUsage("node", before, now)
	now = now.Add(time.Second)
	after := forwardingGuestObservation{CPUCores: map[int]guestCPUCounters{0: {Total: 200, Idle: 75}, 1: {Total: 200, Idle: 125}, 2: {Total: 50, Idle: 25}}}
	f.recordNodeUsage("node", after, now)
	snapshot := f.metricsSnapshot("node", "", time.Time{})
	cores := snapshot.Latest.Details.CPUCores
	if len(cores) != 3 || cores[0].UsagePercent == nil || *cores[0].UsagePercent != 75 || *cores[1].UsagePercent != 25 || cores[2].UsagePercent != nil {
		t.Fatalf("incorrect per-core deltas: %+v", cores)
	}
	*cores[0].UsagePercent = 99
	if *f.metricsSnapshot("node", "", time.Time{}).Latest.Details.CPUCores[0].UsagePercent != 75 {
		t.Fatal("reader mutated current sample")
	}
	now = now.Add(time.Second)
	f.clearNodeUsage("node")
	if f.metricsSnapshot("node", "", time.Time{}).Latest.Details != nil {
		t.Fatal("clear retained old detail readings")
	}
}
