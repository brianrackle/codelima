package codelima

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"go.rockorager.dev/vaxis"
)

func TestActivityPageProjectsObservedProgressWithoutOutputBodies(t *testing.T) {
	s, _ := newTestService(t)
	m := testNodeOperations(t, s)
	op := NodeOperation{ID: newID(), State: "running", CreatedAt: time.Now(), CurrentPhase: "bootstrap", Phases: initialNodeOperationPhases("start"), Output: []string{"private output"}}
	for i := range 4 {
		op.Phases[i].State = "succeeded"
	}
	op.Phases[1].State = "skipped"
	op.Phases[4].State, op.Phases[4].Detail = "running", "step 5/6 · rust-toolchain · install rust"
	m.records[op.ID] = &runningNodeOperation{record: op}
	records := m.List()
	if len(records) != 1 || records[0].Progress == nil {
		t.Fatal("running list has no measured progress", records)
	}
	p := records[0].Progress
	if p.Completed != 4 || p.Total != 7 || p.Phase != "Bootstrap" || p.Detail != op.Phases[4].Detail || len(records[0].Output) != 0 || len(records[0].Phases) != 0 {
		t.Fatal("summary invented progress or copied output bodies", records[0])
	}
	p.Detail = "mutated"
	full, err := m.Get(op.ID)
	if err != nil || full.Phases[4].Detail != op.Phases[4].Detail {
		t.Fatal("summary exposed mutable receipt state", full, err)
	}
}

func TestTUIActivityCompactRunningSelectionScrollsAndAcceptsSecondLineClicks(t *testing.T) {
	a := testActivityApp(t, 80, 24)
	a.activity.page.Running = nil
	for i := range 6 {
		a.activity.page.Running = append(a.activity.page.Running, NodeOperation{ID: fmt.Sprintf("op%d", i), NodeID: newID(), NodeSlug: fmt.Sprintf("node%d", i), State: "running", Request: NodeOperationRequest{Kind: "start"}, CreatedAt: time.Now(), Progress: &NodeOperationProgress{Completed: i, Total: 7, Phase: "Bootstrap"}})
	}
	a.activity.selected = "op5"
	a.draw()
	if a.activity.runningScroll != 4 {
		t.Fatal("compact rows did not scroll by operation identity", a.activity.runningScroll)
	}
	for _, row := range a.activity.mouseRows {
		if row.id == "op4" {
			if row.rect.height != 2 {
				t.Fatal("progress line is outside the operation's mouse target", row.rect)
			}
			a.handleActivityMouse(vaxis.Mouse{EventType: vaxis.EventPress, Button: vaxis.MouseLeftButton, Col: row.rect.col, Row: row.rect.row + 1})
			if a.activity.selected != "op4" {
				t.Fatal("second-line click selected the wrong operation", a.activity.selected)
			}
			return
		}
	}
	t.Fatal("scrolled operation has no visible mouse target")
}

func TestTUIActivityRunningProgressFitsWideAndCompactViews(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		a := testActivityApp(t, size[0], size[1])
		op := &a.activity.page.Running[0]
		op.Progress = &NodeOperationProgress{Completed: 4, Total: 7, Phase: "Bootstrap", Detail: "step 5/6 · rust-toolchain"}
		a.activity.selected = op.ID
		a.draw()
		screen := renderedScreenText(t, a.vx, size[0], size[1])
		for _, want := range []string{"4/7 phases", "step 5/6", "rust-toolchain", "█", "░"} {
			if !strings.Contains(screen, want) {
				t.Fatalf("%v missing %q:\n%s", size, want, screen)
			}
		}
		op.Progress = nil
		a.draw()
		if !strings.Contains(renderedScreenText(t, a.vx, size[0], size[1]), "progress unavailable") {
			t.Fatal("legacy receipt invented a measured percentage")
		}
	}
}

func TestActivityPageCompletionDurationRequiresObservedFinish(t *testing.T) {
	now := time.Now()
	op := NodeOperation{ID: newID(), Request: NodeOperationRequest{Kind: "start"}, State: "succeeded", CreatedAt: now.Add(-38 * time.Second), FinishedAt: &now}
	if record := activityOperationRecord(op, false); !strings.HasSuffix(record.Text, " · 38s") {
		t.Fatal("completion omitted measured duration", record)
	}
	op.State, op.FinishedAt = "interrupted", nil
	if record := activityOperationRecord(op, false); record.Text != "start · interrupted" {
		t.Fatal("unknown finish invented a duration", record)
	}
}
