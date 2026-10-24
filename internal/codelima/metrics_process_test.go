package codelima

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func TestNodeMetricsProcessesOnlyOnExplicitRequest(t *testing.T) {
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "process-node")
	host := newDaemonHost(service)
	peer := &metricsProcessPeer{}
	host.forwarder = &dynamicForwarder{nodes: map[string]*forwardingNodeState{node.ID: {peer: peer}}}
	host.forwarder.recordNodeUsage(node.ID, forwardingGuestObservation{Memory: guestResourceUsage{UsedBytes: 1, TotalBytes: 2}}, time.Now())
	for _, processes := range []bool{false, true} {
		raw, _ := json.Marshal(daemon.NodeMetricsParams{Node: node.ID, Processes: processes})
		value, err := host.Handle(context.Background(), daemon.ClientContext{}, "node.metrics", raw)
		if err != nil {
			t.Fatal(err)
		}
		result := value.(daemon.NodeMetricsSnapshot)
		if (result.Processes != nil) != processes || len(result.History) != 1 {
			t.Fatal("wrong optional process response")
		}
		if !processes && peer.calls.Load() != 0 {
			t.Fatal("history read sampled processes")
		}
	}
	host.forwarder.nodes = nil
	raw, _ := json.Marshal(daemon.NodeMetricsParams{Node: node.ID, Processes: true})
	value, err := host.Handle(context.Background(), daemon.ClientContext{}, "node.metrics", raw)
	if err != nil {
		t.Fatal(err)
	}
	result := value.(daemon.NodeMetricsSnapshot)
	if len(result.History) != 1 || result.ProcessError == "" || result.Processes != nil {
		t.Fatal("process error discarded aggregate history")
	}
}

func processFixture(uptime float64, ticks, start uint64) []byte {
	return []byte(fmt.Sprintf("codelima-clock 100\ncodelima-uptime %.2f\ncodelima-load 1.1 2.2 3.3 2/100 42\ncodelima-stat 42 (a tricky ) name) R 1 2 3 4 5 6 7 8 9 10 %d 0 0 0 0 0 0 0 %d 0\ncodelima-process 42 guest 2048 node app.js\ncodelima-process codelima-complete\n", uptime, ticks, start))
}

func TestNodeMetricsProcessDeltasAndPIDReuse(t *testing.T) {
	first, err := parseGuestProcesses(processFixture(10, 100, 5))
	if err != nil {
		t.Fatal(err)
	}
	second, err := parseGuestProcesses(processFixture(12, 250, 5))
	if err != nil {
		t.Fatal(err)
	}
	result := processSnapshot(second, &first, time.Now())
	if len(result.Rows) != 1 || result.Rows[0].CPUPercent == nil || *result.Rows[0].CPUPercent != 75 || result.Rows[0].RSSBytes != 2097152 || result.Rows[0].Command != "node app.js" {
		t.Fatalf("incorrect process sample: %+v", result.Rows)
	}
	for _, changed := range []guestProcessObservation{
		mustGuestProcesses(t, processFixture(14, 300, 6)),
		mustGuestProcesses(t, processFixture(2, 300, 5)),
		mustGuestProcesses(t, processFixture(100, 300, 5)),
		mustGuestProcesses(t, processFixture(14, 20, 5)),
	} {
		if got := processSnapshot(changed, &second, time.Now()); got.Rows[0].CPUPercent != nil {
			t.Fatal("invalid CPU baseline reused")
		}
	}
}

func mustGuestProcesses(t *testing.T, data []byte) guestProcessObservation {
	t.Helper()
	got, err := parseGuestProcesses(data)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestNodeMetricsProcessRejectsInvalidAndBoundsInput(t *testing.T) {
	for _, data := range []string{"", "codelima-clock 0\ncodelima-uptime 1", "codelima-clock 100\ncodelima-uptime NaN", strings.Repeat("x", maxProcessOutput+1)} {
		if _, err := parseGuestProcesses([]byte(data)); err == nil {
			t.Fatal("invalid process sample accepted")
		}
	}
	data := string(processFixture(10, 100, 5))
	for i := 1; i <= maxGuestProcesses+20; i++ {
		data += fmt.Sprintf("codelima-process %d guest 10 cmd\n", i)
	}
	got := mustGuestProcesses(t, []byte(data))
	if len(got.rows) > maxGuestProcesses || !got.truncated {
		t.Fatal("unbounded process rows")
	}
}

type metricsProcessPeer struct {
	forwardingPeer
	calls atomic.Int32
	wait  chan struct{}
}

func (p *metricsProcessPeer) SampleProcesses(ctx context.Context) (guestProcessObservation, error) {
	p.calls.Add(1)
	if p.wait != nil {
		select {
		case <-p.wait:
		case <-ctx.Done():
			return guestProcessObservation{}, ctx.Err()
		}
	}
	return parseGuestProcesses(processFixture(10, 100, 5))
}

func TestForwarderMetricsProcessCacheAndCancellation(t *testing.T) {
	peer := &metricsProcessPeer{wait: make(chan struct{})}
	f := &dynamicForwarder{nodes: map[string]*forwardingNodeState{"node": {peer: peer}}}
	done := make(chan error, 1)
	go func() { _, err := f.sampleProcesses(context.Background(), "node", "cpu"); done <- err }()
	deadline := time.Now().Add(time.Second)
	for peer.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if peer.calls.Load() != 1 {
		t.Fatal("probe did not start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.sampleProcesses(ctx, "node", "cpu"); err == nil {
		t.Fatal("canceled waiter was not canceled")
	}
	close(peer.wait)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	first, err := f.sampleProcesses(context.Background(), "node", "cpu")
	if err != nil {
		t.Fatal(err)
	}
	first.Rows[0].Command = "mutated"
	second, err := f.sampleProcesses(context.Background(), "node", "memory")
	if err != nil {
		t.Fatal(err)
	}
	if peer.calls.Load() != 1 || second.Rows[0].Command == "mutated" {
		t.Fatal("cache was resampled or aliased")
	}
}
