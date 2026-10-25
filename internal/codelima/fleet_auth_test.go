package codelima

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFleetEvidenceAuthReceiptUsesRecordedImportOnly(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	node.ImportHostAuth, node.AuthImportCompleted = true, true
	at := s.now().Add(-time.Hour)
	appendReceipt := func(at time.Time, imported any) {
		t.Helper()
		if err := s.store.AppendNodeEvent(node.ID, Event{Timestamp: at, Type: "node.auth.imported", Fields: map[string]any{
			"imported": imported, "skipped": map[string]string{"id_rsa": "/private/credential/path"},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	appendReceipt(at, []string{"id_ed25519", "id_ed25519.pub", "gitconfig", "codex", "claude", "claude_state"})
	h := newDaemonHost(s)
	t.Cleanup(func() { _ = h.Close() })
	p := h.fleetPreview(node, false)
	if p.AuthImport == nil || !p.AuthImport.ImportedAt.Equal(at) || p.AuthImport.ItemCount != 6 || strings.Join(p.AuthImport.Categories, ", ") != "ssh, git, codex, claude" {
		t.Fatalf("missing recorded receipt: %+v", p.AuthImport)
	}
	encoded, err := json.Marshal(p)
	if err != nil || strings.Contains(string(encoded), "/private") || strings.Contains(string(encoded), "id_ed25519") {
		t.Fatalf("receipt exposed paths or artifact names: %s %v", encoded, err)
	}
	// File order decides the latest completed import; wall clocks may move.
	appendReceipt(at.Add(-time.Minute), []string{})
	p = h.fleetPreview(node, false)
	if p.AuthImport == nil || p.AuthImport.ItemCount != 0 || len(p.AuthImport.Categories) != 0 {
		t.Fatalf("empty import became an earlier success: %+v", p.AuthImport)
	}
	// A malformed latest receipt must not resurrect a previous receipt.
	appendReceipt(at, []string{"codex", "unrecognized-secret"})
	p = h.fleetPreview(node, false)
	if p.AuthImport != nil {
		t.Fatalf("invented import after malformed receipt: %+v", p.AuthImport)
	}
	appendReceipt(at, []string{"codex"})
	f, err := os.OpenFile(s.store.nodeEventsPath(node.ID), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteString(`{"timestamp":"invalid","type":"node.auth.imported","fields":{"imported":["codex"]}}` + "\n")
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal(writeErr, closeErr)
	}
	if p = h.fleetPreview(node, false); p.AuthImport != nil {
		t.Fatalf("malformed latest timestamp revived an earlier import: %+v", p.AuthImport)
	}
	appendReceipt(at, []string{"codex"})
	node.AuthImportCompleted = false
	if h.fleetPreview(node, false).AuthImport != nil {
		t.Fatal("claimed import before its completion marker")
	}
}

func TestFleetEvidenceAuthReceiptBoundsHistoryAndDoesNotFollowParents(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	node.ImportHostAuth, node.AuthImportCompleted = true, true
	h := newDaemonHost(s)
	t.Cleanup(func() { _ = h.Close() })
	if err := s.store.AppendNodeEvent(node.ID, Event{Timestamp: s.now(), Type: "node.auth.imported", Fields: map[string]any{"imported": []string{"codex"}}}); err != nil {
		t.Fatal(err)
	}
	child := node
	child.ID, child.ParentNodeID = newID(), node.ID
	if h.fleetPreview(child, false).AuthImport != nil {
		t.Fatal("attributed a parent's mutable history to a clone")
	}
	f, err := os.OpenFile(s.store.nodeEventsPath(node.ID), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteString(strings.Repeat("x", nodeLogByteLimit+1) + "\n")
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal(writeErr, closeErr)
	}
	if h.fleetPreview(node, false).AuthImport != nil {
		t.Fatal("read beyond the bounded event history")
	}
}

func TestTUIOverviewAuthReceiptUsesIdentityAndRemainsReadable(t *testing.T) {
	for _, size := range [][2]int{{150, 38}, {80, 24}} {
		a := overviewFixture(t, size[0], size[1])
		node := &a.state.nodes[0]
		node.ImportHostAuth, node.AuthImportCompleted = true, true
		at := time.Date(2026, time.October, 21, 9, 42, 0, 0, time.Local)
		a.fleetEvidence = tuiFleetEvidenceState{loaded: true, receivedAt: time.Now(), snapshot: fleetEvidenceSnapshot{Preview: &fleetPreviewEvidence{
			NodeID: node.ID, NodeUpdatedAt: node.UpdatedAt,
			AuthImport: &fleetAuthImportEvidence{ImportedAt: at, ItemCount: 6, Categories: []string{"ssh", "git", "codex", "claude"}},
		}}}
		a.refreshFleet()
		a.overview.panel = tuiOverviewIdentity
		a.draw()
		var screens strings.Builder
		for range 20 {
			screens.WriteString(renderedScreenText(t, a.vx, size[0], size[1]))
			a.moveOverviewSelection(1)
			a.draw()
		}
		for _, want := range []string{"Oct 21", "6 items", "ssh, git, codex, claude"} {
			if !strings.Contains(screens.String(), want) {
				t.Fatalf("missing %q at %v:\n%s", want, size, screens.String())
			}
		}
		a.fleetEvidence.snapshot.Preview.NodeID = "another-node"
		if got := a.fleetAuthLabel(*node); strings.Contains(got, "Oct 21") || !strings.Contains(got, "receipt unavailable") {
			t.Fatalf("cross-node or invented receipt: %q", got)
		}
		node.ParentNodeID = "parent"
		if got := a.fleetAuthLabel(*node); !strings.Contains(got, "inherited") {
			t.Fatalf("clone claimed a fresh import: %q", got)
		}
	}
}
