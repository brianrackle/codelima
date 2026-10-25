package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/hostinfo"
)

// Exercise both halves over the real socket. The predecessor's journal rejects
// this event until update owns the barrier; only the importer can then drain it.
func TestActivityNativePendingEventSurvivesCommittedUpdate(t *testing.T) {
	harness := newUpdateHandoffHarness(t)
	host := harness.host
	host.markServing()
	host.activityEvents.owned.Store(true)
	request := activityMessageRequest{ID: newID(), Source: "host", Text: "pending before replacement", Action: activityOpenHost}
	journal, err := host.activityJournal()
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(filepath.Dir(journal.dir), "activity.saved")
	if err := os.Rename(journal.dir, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal.dir, []byte("temporarily unavailable"), 0o600); err != nil {
		t.Fatal(err)
	}
	host.activityEvents.enqueue(request)
	if host.activityEvents.flush() {
		t.Fatal("blocked journal unexpectedly accepted the event")
	}
	done := harness.startUpdate()
	token := pendingActivityUpdateToken(t, harness)
	successor := NewService(harness.service.cfg, newFakeSandbox(), strings.NewReader(""), ioDiscard{}, ioDiscard{})
	successor.localTerminals = true
	ctx, cancel := context.WithCancel(context.Background())
	imported := make(chan error, 1)
	go func() { imported <- runDaemonImport(ctx, successor, harness.socket, token, nil) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-imported:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("importer did not stop")
		}
	})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("update did not commit")
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	// Force an owned initialization attempt while storage is still broken.
	// Recovery must retry opening as well as retaining the pending requests.
	client, err := daemonclient.Dial(ctx, daemonclient.Options{Home: successor.cfg.MetadataRoot, Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	var page ActivityPage
	callErr := client.Call(ctx, "activity.list", nil, &page)
	_ = client.Close()
	if callErr != nil || page.MessageError == "" {
		t.Fatal("blocked journal was not observed", page, callErr)
	}
	if err := os.Remove(journal.dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, journal.dir); err != nil {
		t.Fatal(err)
	}
	waitForHandoff(t, 3*time.Second, func() bool {
		reopened, err := newActivityJournal(successor)
		if err != nil {
			return false
		}
		records, _ := reopened.Snapshot()
		return slices.ContainsFunc(records, func(record ActivityMessage) bool { return record.activityMessageRequest == request })
	}, "pending native event in the committed successor")
}

func pendingActivityUpdateToken(t *testing.T, harness *updateHandoffHarness) string {
	t.Helper()
	var token string
	waitForHandoff(t, 10*time.Second, func() bool {
		data, err := os.ReadFile(harness.argsPath)
		if err != nil {
			return false
		}
		args := strings.Fields(string(data))
		index := slices.Index(args, "--token")
		if index < 0 || index+1 == len(args) {
			return false
		}
		token = args[index+1]
		return true
	}, "handoff token")
	return token
}

func TestActivityNativeHandoffFreezesQueueWithTerminalBaselines(t *testing.T) {
	s, _ := newTestService(t)
	old := newDaemonHost(s)
	entry := &daemonTerminalEntry{state: daemon.TerminalState{TerminalID: "term_cut", Target: "node:" + newID(), Kind: "node-host-shell"}}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for i := uint64(1); i <= 64; i++ {
			entry.activity.observe(old.activityEvents, entry.state, daemon.Snapshot{Metadata: TerminalMetadata{BellCount: i}})
		}
	}()
	data, err := old.activityEvents.prepareHandoff([]*daemonTerminalEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	<-finished
	var cut activityHandoffState
	if err := json.Unmarshal(data, &cut); err != nil {
		t.Fatal(err)
	}
	count := cut.Terminals[entry.state.TerminalID].BellCount
	if len(cut.Pending) != int(count) || entry.activity.metadata.BellCount != count {
		t.Fatal("publication advanced beyond the transferred queue", cut, entry.activity)
	}
	next := newDaemonHost(s)
	manifest := daemon.HandoffManifest{Version: daemon.HandoffVersion, Activity: data, Session: daemon.Session{Terminals: []daemon.TerminalState{entry.state}}}
	if err := next.activityEvents.restoreHandoff(manifest); err != nil {
		t.Fatal(err)
	}
	observer := terminalActivityState{metadata: next.activityEvents.adoptedBaseline(entry.state.TerminalID, TerminalMetadata{BellCount: 64})}
	observer.observe(next.activityEvents, entry.state, daemon.Snapshot{Metadata: TerminalMetadata{BellCount: 64}})
	observer.observe(next.activityEvents, entry.state, daemon.Snapshot{Metadata: TerminalMetadata{BellCount: 64}})
	want := len(cut.Pending)
	if count < 64 {
		want++ // One summary for output not observed before the cut.
	}
	if len(next.activityEvents.pending) != want || !slices.Equal(next.activityEvents.pending[:len(cut.Pending)], cut.Pending) {
		t.Fatal("handoff lost, replayed or changed an admitted message", next.activityEvents.pending, cut)
	}
}

func TestActivityNativeHandoffRetainsCapacityRoutesAndOverflow(t *testing.T) {
	s, _ := newTestService(t)
	old := newDaemonHost(s)
	route := activityRoute{NodeID: newID(), NodeSlug: "alpha", Port: 1455, Serving: true}
	view := forwardingActivitySnapshot{Routes: map[dynamicRouteKey]activityRoute{{node: "alpha", port: 1455}: route}, Owners: map[int]activityRoute{1455: route}}
	capacity, nodes := hostinfo.Snapshot{CPUs: 2}, []Node{{VCPUs: 3, LastRuntimeObservation: &RuntimeObservation{Exists: true, Status: ObservationRunning}}}
	old.activityEvents.observeForwarding(view)
	old.activityEvents.observeCapacity(capacity, nodes)
	for range nativeActivityQueueLimit {
		old.activityEvents.enqueue(activityMessageRequest{Source: "host", Text: "queued"})
	}
	data, err := old.activityEvents.prepareHandoff(nil)
	if err != nil {
		t.Fatal(err)
	}
	next := newDaemonHost(s)
	if err := next.activityEvents.restoreHandoff(daemon.HandoffManifest{Version: daemon.HandoffVersion, Activity: data}); err != nil {
		t.Fatal(err)
	}
	next.activityEvents.observeForwarding(view)
	next.activityEvents.observeCapacity(capacity, nodes)
	if len(next.activityEvents.pending) != nativeActivityQueueLimit || next.activityEvents.dropped != 3 {
		t.Fatal("unchanged observations repeated or overflow count changed", next.activityEvents.pending, next.activityEvents.dropped)
	}
	next.markServing()
	next.activityEvents.owned.Store(true)
	for next.activityEvents.flush() {
	}
	journal, err := next.activityJournal()
	if err != nil {
		t.Fatal(err)
	}
	records, _ := journal.Snapshot()
	if len(records) != nativeActivityQueueLimit+1 || !strings.Contains(records[0].Text, "3 native Activity events") {
		t.Fatal("handoff lost its overflow report", len(records), records[0])
	}
}

func TestActivityNativePendingQueueResumesAfterRejectedUpdate(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-ownership", true: "after-ownership"}[committed], func(t *testing.T) {
			harness := newUpdateHandoffHarness(t)
			host := harness.host
			host.markServing()
			host.activityEvents.owned.Store(true)
			request := activityMessageRequest{ID: newID(), Source: "host", Text: "kept across rollback"}
			host.activityEvents.enqueue(request)
			done := harness.startUpdate()
			peer, token := harness.dialSuccessor(t)
			harness.receiveHandoff(t, peer, token)
			if committed {
				if err := peer.WriteJSON(daemon.HandoffMessage{Type: "ready"}, nil); err != nil {
					t.Fatal(err)
				}
				if message, err := peer.ReadControlMessage(); err != nil || message.Type != "commit" {
					t.Fatal(message, err)
				}
			}
			if err := peer.WriteJSON(daemon.HandoffMessage{Type: "failed", Error: "injected Activity rollback"}, nil); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("rejected import committed")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("update did not roll back")
			}
			host.activityEvents.enqueue(activityMessageRequest{Source: "host", Text: "after rollback"})
			if !host.activityEvents.flush() {
				t.Fatal("rollback did not resume persistence")
			}
			journal, err := host.activityJournal()
			if err != nil {
				t.Fatal(err)
			}
			records, _ := journal.Snapshot()
			if len(records) != 2 || records[1].activityMessageRequest != request || records[0].Text != "after rollback" {
				t.Fatal("rollback lost queue identity or kept admission frozen", records)
			}
		})
	}
}

func TestActivityNativeHandoffRejectsInvalidStateBeforeInstallation(t *testing.T) {
	s, _ := newTestService(t)
	valid := activityMessageRequest{ID: newID(), Source: "host", Text: "valid"}
	for _, state := range []activityHandoffState{
		{Pending: []activityMessageRequest{valid, valid}},
		{Pending: []activityMessageRequest{{ID: newID(), Source: "host", Text: "\x1bunsafe"}}},
		{Pending: make([]activityMessageRequest, nativeActivityQueueLimit+1)},
		{Terminals: map[string]TerminalMetadata{"unknown": {}}},
		{Routes: []activityHandoffRoute{{Node: "bad", Port: -1}}},
		{Owners: map[int]activityRoute{1455: {NodeID: "not-a-uuid", Port: 1455}}},
	} {
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		host := newDaemonHost(s)
		host.activityEvents.enqueue(valid)
		if err := host.activityEvents.restoreHandoff(daemon.HandoffManifest{Version: daemon.HandoffVersion, Activity: data}); err == nil {
			t.Fatal("invalid transfer accepted", state)
		}
		if len(host.activityEvents.pending) != 1 || host.activityEvents.pending[0] != valid {
			t.Fatal("invalid transfer partially changed state")
		}
	}
}
