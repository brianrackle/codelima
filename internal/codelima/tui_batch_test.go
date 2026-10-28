package codelima

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"go.rockorager.dev/vaxis"
)

func batchTestFrontend(t *testing.T, s *Service) *Service {
	t.Helper()
	home, _ := testCLIOperationServer(t, s, Version)
	client, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	frontend := s.withIO(io.Discard, io.Discard)
	frontend.daemonClient = client
	return frontend
}

func batchReviewFixture(t *testing.T, action tuiActionID, width, height int) (*vaxisTUIApp, *tuiBatchReview) {
	t.Helper()
	a, _ := newTestManager(t, width, height)
	for i := range a.state.nodes {
		a.state.nodes[i].Status = NodeStatusStopped
		if action == tuiActionNodeStop {
			a.state.nodes[i].Status = NodeStatusRunning
		}
	}
	a.refreshFleet()
	plan := []tuiPaletteNodePlan{{a.state.nodes[0].ID, action}, {a.state.nodes[1].ID, action}}
	if err := a.openBatchReview("Review 2 nodes", plan, true); err != nil {
		t.Fatal(err)
	}
	return a, a.overlay.(*tuiBatchReview)
}

func TestTUIBatchCapturesTargetsAndRequiresExactDeleteCount(t *testing.T) {
	a, d := batchReviewFixture(t, tuiActionNodeDelete, 80, 24)
	var accepted []NodeBatchTarget
	d.run = func(targets []NodeBatchTarget) error { accepted = targets; return nil }
	a.fleet.marks = map[string]bool{}
	a.fleet.selectID(a.state.nodes[1].ID)
	for _, text := range []string{"1", "02", "2 ", " 2", ""} {
		d.dialog.SetFieldValue("confirm", text)
		if done, _ := d.Update(vaxis.Key{Keycode: vaxis.KeyEnter}); done || len(accepted) > 0 {
			t.Fatal("inexact count admitted", text)
		}
	}
	d.dialog.SetFieldValue("confirm", "2")
	if done, err := d.Update(vaxis.Key{Keycode: vaxis.KeyEnter}); !done || err != nil || len(accepted) != 2 {
		t.Fatal("exact captured count refused", done, err, accepted)
	}
	if accepted[0].NodeID != a.state.nodes[0].ID {
		t.Fatal("selection retargeted review")
	}
}

func TestTUIBatchRejectsStaleHiddenAndRepeatedActions(t *testing.T) {
	for _, condition := range []string{"renamed", "disconnected", "unavailable", "small", "repeat", "release", "paste"} {
		t.Run(condition, func(t *testing.T) {
			w, h := 80, 24
			if condition == "small" {
				w, h = 40, 10
			}
			a, d := batchReviewFixture(t, tuiActionNodeStart, w, h)
			called := false
			d.run = func([]NodeBatchTarget) error { called = true; return nil }
			key := vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl}
			switch condition {
			case "renamed":
				a.state.nodes[1].Slug = "renamed"
			case "disconnected":
				a.daemonDisconnected = true
			case "unavailable":
				a.state.nodes[1].Status = NodeStatusRunning
			case "repeat":
				key.EventType = vaxis.EventRepeat
			case "release":
				key.EventType = vaxis.EventRelease
			case "paste":
				key.EventType = vaxis.EventPaste
			}
			a.draw()
			if done, _ := d.Update(key); done || called {
				t.Fatal("invalid review admitted", condition)
			}
			if done, _ := d.Update(vaxis.Key{Keycode: vaxis.KeyEsc}); !done {
				t.Fatal("Esc did not discard review")
			}
		})
	}
}

func TestTUIBatchResponsiveReviewMouseAndQueuedEvidence(t *testing.T) {
	for _, width := range []int{80, 150} {
		a, d := batchReviewFixture(t, tuiActionNodeStop, width, 28)
		a.draw()
		screen := renderedScreenText(t, a.vx, width, 28)
		for _, want := range []string{"Review 2 nodes", a.state.nodes[0].Slug, a.state.nodes[1].Slug, "guest", "host", "parallel", "Activity", "Esc Cancel"} {
			if !strings.Contains(screen, want) {
				t.Fatalf("missing %q at %d:\n%s", want, width, screen)
			}
		}
		called := false
		d.run = func([]NodeBatchTarget) error { called = true; return nil }
		for _, control := range d.controls {
			if control.id == "submit" {
				_, _ = d.Update(vaxis.Mouse{Col: control.rect.col, Row: control.rect.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
			}
		}
		if !called {
			t.Fatal("mouse submission failed")
		}
		a.activity.page.Running = []NodeOperation{{ID: "parent", State: "running", Batch: &NodeBatchState{Members: []NodeBatchMember{{NodeBatchTarget: d.targets[0], State: "queued"}}}}}
		node := a.state.nodes[0]
		if got := a.operationDisplayStatus(tuiTreeEntry{node: node}); got != "queued" {
			t.Fatal("queued node appears available", got)
		}
		if !strings.Contains(a.fleetPhase(node.ID), "queued") {
			t.Fatal("missing queued row progress")
		}
	}
}

func TestTUIBatchProgressCountsNodes(t *testing.T) {
	for _, width := range []int{80, 150} {
		a, _ := newTestManager(t, width, 28)
		a.view = tuiViewActivity
		a.activity.page.Running = []NodeOperation{{ID: "batch", NodeSlug: "64 nodes", Request: NodeOperationRequest{Kind: "batch"}, State: "running", Batch: &NodeBatchState{Total: 64, Succeeded: 32, Running: 4, Queued: 28}, Progress: &NodeOperationProgress{Total: 64, Completed: 32, Phase: "Batch"}}}
		a.draw()
		screen := renderedScreenText(t, a.vx, width, 28)
		if !strings.Contains(screen, "32/64 nodes") || strings.Contains(screen, "32/64 phases") {
			t.Fatalf("batch counts were mislabeled at %d:\n%s", width, screen)
		}
	}
}

func TestTUIBatchOutcomeViewClearsUnderlyingActivity(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	win := a.vx.Window()
	for row := range 24 {
		tuiLine(win, row, strings.Repeat("#", 80), a.managerTheme().text)
	}
	view := tuiOperationOutputView{app: a, record: NodeOperation{State: "failed", Batch: &NodeBatchState{Total: 2, Succeeded: 1, Failed: 1, Members: []NodeBatchMember{{NodeBatchTarget: NodeBatchTarget{NodeSlug: "alpha", Kind: "stop"}, State: "succeeded"}, {NodeBatchTarget: NodeBatchTarget{NodeSlug: "beta", Kind: "stop"}, State: "failed", Error: "fixture failure"}}}}}
	view.Draw(win, vaxis.Style{}, vaxis.Style{}, vaxis.Style{})
	screen := renderedScreenText(t, a.vx, 80, 24)
	if strings.Contains(screen, "#") || !strings.Contains(screen, "alpha · stop · succeeded") || !strings.Contains(screen, "beta · stop · failed") {
		t.Fatal("batch outcomes leaked underlying content or omitted a member", screen)
	}
}

func TestTUIBatchValidationPersistsUntilInputChanges(t *testing.T) {
	_, d := batchReviewFixture(t, tuiActionNodeDelete, 80, 24)
	d.dialog.SetFieldValue("confirm", "2 ")
	_, _ = d.Update(vaxis.Key{Keycode: vaxis.KeyEnter})
	message := d.dialog.Error
	if message == "" {
		t.Fatal("missing exact-count validation")
	}
	for _, event := range []vaxis.Event{vaxis.PasteEndEvent{}, vaxis.Key{Keycode: vaxis.KeyLeft}} {
		_, _ = d.Update(event)
		if d.dialog.Error != message {
			t.Fatal("an unchanged input cleared validation", event)
		}
	}
	_, _ = d.Update(vaxis.Key{Keycode: '3', Text: "3"})
	if d.dialog.Error != "" {
		t.Fatal("editing did not clear stale validation")
	}
}

func TestTUIBatchDeleteRecoveryRequiresCurrentUnclaimedTarget(t *testing.T) {
	for _, condition := range []string{"released", "stale", "disconnected", "revision", "claimed", "queued", "running"} {
		t.Run(condition, func(t *testing.T) {
			a, _ := newTestManager(t, 80, 24)
			node := &a.state.nodes[0]
			node.Status, node.UpdatedAt = NodeStatusTerminating, time.Now()
			evidence := fleetNodeEvidence{NodeUpdatedAt: node.UpdatedAt, ClaimState: "unclaimed"}
			a.fleetEvidence = tuiFleetEvidenceState{loaded: true, receivedAt: time.Now()}
			switch condition {
			case "stale":
				a.fleetEvidence.receivedAt = time.Now().Add(-time.Minute)
			case "disconnected":
				a.daemonDisconnected = true
			case "revision":
				evidence.NodeUpdatedAt = node.UpdatedAt.Add(-time.Second)
			case "claimed":
				evidence.ClaimState = "active"
			case "queued":
				a.activity.page.Running = []NodeOperation{{State: "running", Batch: &NodeBatchState{Members: []NodeBatchMember{{NodeBatchTarget: NodeBatchTarget{NodeID: node.ID}, State: "queued"}}}}}
			case "running":
				a.activity.page.Running = []NodeOperation{{NodeID: node.ID, State: "running", Request: NodeOperationRequest{Kind: "delete"}}}
			}
			a.fleetEvidence.snapshot.Nodes = map[string]fleetNodeEvidence{node.ID: evidence}
			_, err := a.paletteNodeAction(node.ID, tuiActionNodeDelete)
			if (err == nil) != (condition == "released") {
				t.Fatal("incorrect deletion recovery availability", err)
			}
			if condition == "released" {
				if !a.fleetDeleteRetryable(*node) || !strings.Contains(a.fleetPhase(node.ID), "Delete incomplete") {
					t.Fatal("completed deletion failure still appears active")
				}
				for _, action := range []tuiActionID{tuiActionNodeStart, tuiActionNodeRetry, tuiActionNodeRestart} {
					if _, err := a.paletteNodeAction(node.ID, action); err == nil {
						t.Fatal("deletion recovery offered an unrelated lifecycle action", action)
					}
				}
			}
		})
	}
}

func TestTUIBatchDeleteReviewWarnsThatHostSessionsEnd(t *testing.T) {
	a, d := batchReviewFixture(t, tuiActionNodeDelete, 150, 38)
	var text strings.Builder
	for _, line := range d.lines(a.managerTheme()) {
		text.WriteString(line.text + "\n")
	}
	if !strings.Contains(text.String(), "Guest and host sessions end for deleted nodes") || strings.Contains(text.String(), "Host sessions keep running") {
		t.Fatal("deletion review promised to preserve node-owned host sessions", text.String())
	}
}
