package codelima

import (
	"context"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func TestTUINodeRestartMarksTakePriorityOverFailedSelection(t *testing.T) {
	a, sessions := newTestManager(t, 80, 24)
	a.service, _ = newTestService(t)
	a.state.nodes[0].Status = NodeStatusFailed
	a.state.nodes[1].Status = NodeStatusRunning
	if err := a.state.replaceNodes(a.state.nodes, ""); err != nil {
		t.Fatal(err)
	}
	a.refreshFleet()
	a.fleet.selectID(a.state.nodes[0].ID)
	a.selectFleetNode()
	for _, node := range a.state.nodes {
		a.fleet.marks[node.ID] = true
	}
	a.handleKey(vaxis.Key{Keycode: 'r', Text: "r"})
	d, ok := a.overlay.(*tuiBatchReview)
	if !ok || !strings.Contains(strings.ToLower(d.dialog.Title), "restart") {
		t.Fatal("marked restart was lost to selected failed-node retry", a.overlay, a.status)
	}
	a.draw()
	screen := renderedScreenText(t, a.vx, 80, 24)
	for _, node := range a.state.nodes {
		if !strings.Contains(screen, node.Slug) {
			t.Fatal("batch confirmation omitted a marked node", screen)
		}
	}
	if len(sessions.opened) != 0 {
		t.Fatal("restart confirmation created sessions")
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEsc})
	if a.overlay != nil || len(a.fleet.marks) != len(a.state.nodes) {
		t.Fatal("canceling restart lost marks or left its dialog open")
	}
}

func TestTUINodeRestartDetailIgnoresFleetMarks(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.service, _ = newTestService(t)
	cfg := a.service.cfg
	cfg.ConfirmStop = "always"
	a.preferences, a.nodeDetail = &cfg, true
	selected := a.state.selectedEntry().node
	for _, node := range a.state.nodes {
		a.fleet.marks[node.ID] = true
	}
	a.handleKey(vaxis.Key{Keycode: 'r', Text: "r"})
	d, ok := a.overlay.(*tuiDialog)
	if !ok || d.Title != "Restart "+selected.Slug {
		t.Fatal("detail restart acted on unrelated fleet marks", a.overlay, a.status)
	}
}

func TestTUINodeRestartFooterDistinguishesFailedRetry(t *testing.T) {
	for _, size := range [][2]int{{150, 38}, {80, 24}} {
		a, _ := newTestManager(t, size[0], size[1])
		a.state.nodes[0].Status = NodeStatusFailed
		if err := a.state.replaceNodes(a.state.nodes, ""); err != nil {
			t.Fatal(err)
		}
		a.refreshFleet()
		a.fleet.selectID(a.state.nodes[0].ID)
		a.selectFleetNode()
		if footer := a.managerFooter(); !strings.Contains(footer, "r retry") || strings.Contains(footer, "r restart") {
			t.Errorf("%dx%d failed selection advertises the wrong action: %s", size[0], size[1], footer)
		}
		a.fleet.marks[a.state.nodes[0].ID] = true
		if footer := a.managerFooter(); !strings.Contains(footer, "r restart") || strings.Contains(footer, "r retry") {
			t.Errorf("%dx%d marked selection advertises the wrong action: %s", size[0], size[1], footer)
		}
	}
}

func TestTUINodeRestartConfirmationExplainsRetryAtBothSizes(t *testing.T) {
	for _, size := range [][2]int{{150, 38}, {80, 24}} {
		a, _ := newTestManager(t, size[0], size[1])
		a.service, _ = newTestService(t)
		cfg := a.service.cfg
		cfg.ConfirmStop = "always"
		a.preferences, a.nodeDetail = &cfg, true
		if err := a.performAction(tuiActionSpec{ID: tuiActionNodeRestart}); err != nil {
			t.Fatal(err)
		}
		a.draw()
		screen := renderedScreenText(t, a.vx, size[0], size[1])
		for _, want := range []string{"ends its guest sessions", "Completed bootstrap", "use Retry to resume it."} {
			if !strings.Contains(screen, want) {
				t.Errorf("%dx%d restart confirmation lost %q:\n%s", size[0], size[1], want, screen)
			}
		}
	}
}

func TestTUINodeRestartBatchRunsEveryMarkedNode(t *testing.T) {
	s, first := setupBootstrapStepsTest(t)
	ctx := context.Background()
	second, err := s.NodeCreate(ctx, NodeCreateInput{Slug: "second", Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	nodes := []Node{first, second}
	for i := range nodes {
		nodes[i], err = s.NodeStart(ctx, nodes[i].ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	a, sessions := newTestManager(t, 80, 24)
	a.service = batchTestFrontend(t, s)
	a.state, err = newTUIState(nodes, sessions)
	if err != nil {
		t.Fatal(err)
	}
	a.refreshFleet()
	a.fleet.markVisible()
	fake := s.sandbox.(*fakeSandbox)
	fake.calls = nil
	a.handleKey(vaxis.Key{Keycode: 'r', Text: "r"})
	d := a.activeDialog()
	if d == nil || len(fake.calls) != 0 {
		t.Fatal("bulk restart ran before one confirmation", a.overlay, fake.calls)
	}
	if err := d.OnSubmit(nil); err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		calls := strings.Join(fake.calls, "\n")
		if !strings.Contains(calls, "stop "+node.SandboxName) || !strings.Contains(calls, "start "+node.SandboxName) {
			t.Fatal("bulk restart skipped a marked node", node.Slug, calls)
		}
	}
	if len(a.fleet.marks) != 0 {
		t.Fatal("completed bulk restart retained marks")
	}
}

func TestTUINodeRestartRunningNodeHonorsStopConfirmation(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	ctx := context.Background()
	node, err := s.NodeStart(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, sessions := newTestManager(t, 80, 24)
	a.service = s
	a.state, err = newTUIState([]Node{node}, sessions)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.cfg
	cfg.ConfirmStop = "always"
	a.preferences = &cfg
	a.refreshFleet()
	fake := s.sandbox.(*fakeSandbox)
	fake.calls = nil
	a.handleKey(vaxis.Key{Keycode: 'r', Text: "r"})
	d, ok := a.overlay.(*tuiDialog)
	if !ok || !strings.Contains(d.Title, node.Slug) || !strings.Contains(strings.Join(d.Description, " "), "session") {
		t.Fatal("running restart lacks its node and session impact", a.overlay, a.status)
	}
	if len(fake.calls) != 0 {
		t.Fatal("restart ran before confirmation", fake.calls)
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEsc})
	if len(fake.calls) != 0 {
		t.Fatal("canceled restart changed runtime", fake.calls)
	}
	a.handleKey(vaxis.Key{Keycode: 'r', Text: "r"})
	d = a.overlay.(*tuiDialog)
	if err := d.OnSubmit(nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(fake.calls, "\n"), "stop "+node.SandboxName) || !strings.Contains(a.status, "restarted") {
		t.Fatal("confirmed restart did not run", fake.calls, a.status)
	}
}
