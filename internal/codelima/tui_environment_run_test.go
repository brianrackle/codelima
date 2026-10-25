package codelima

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"go.rockorager.dev/vaxis"
)

func TestTUIEnvironmentTestRunsThroughDaemonAndRetainsOutputAfterDeletion(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	s, _ := newTestService(t)
	sandbox := &environmentTestSandbox{SandboxClient: s.sandbox, entered: make(chan Node, 1)}
	s.sandbox = sandbox
	env, err := s.EnvironmentConfigCreate(context.Background(), EnvironmentConfigCreateInput{Slug: "live-test", BootstrapCommands: []string{"printf ENV_TEST_BLOCK"}})
	if err != nil {
		t.Fatal(err)
	}
	home, host := testCLIOperationServer(t, s, Version)
	client, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	a.service = s.withIO(s.stdout, s.stderr)
	a.service.daemonClient = client
	a.view, a.environmentsView = tuiViewTemplates, true
	a.managerData.environments = []EnvironmentConfig{env}
	events := make(chan vaxis.Event, 32)
	a.postEvent = func(e vaxis.Event) { events <- e }
	a.handleKey(vaxis.Key{Keycode: 't', Text: "t", EventType: vaxis.EventRelease})
	if len(a.recipes.tests) != 0 {
		t.Fatal("key release submitted a test")
	}
	a.handleKey(vaxis.Key{Keycode: 't', Text: "t"})
	intent := a.recipes.tests[env.ID]
	if intent == nil {
		t.Fatal("t did not submit selected environment")
	}
	a.handleKey(vaxis.Key{Keycode: 't', Text: "t"})
	var admitted tuiEnvironmentTestAdmissionEvent
	select {
	case e := <-events:
		admitted = e.(tuiEnvironmentTestAdmissionEvent)
	case <-time.After(3 * time.Second):
		t.Fatal("test admission blocked on VM startup")
	}
	if admitted.err != nil {
		t.Fatal(admitted.err)
	}
	a.applyEnvironmentTestAdmission(admitted)
	viewer, ok := a.overlay.(*tuiOperationOutputView)
	if !ok || a.view != tuiViewActivity || viewer.id != admitted.record.ID {
		t.Fatal("test did not open Activity output")
	}
	select {
	case <-sandbox.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("test command never ran")
	}
	// Closing the actual requester socket must not cancel the daemon's work.
	_ = client.Close()
	ops, err := host.nodeOperations()
	if err != nil {
		t.Fatal(err)
	}
	running, err := ops.Get(admitted.record.ID)
	if err != nil || running.State != "running" {
		t.Fatal("requester exit ended execution", running, err)
	}
	if err := ops.Cancel(running.ID); err != nil {
		t.Fatal(err)
	}
	finished := waitNodeOperation(t, ops, running.ID, "canceled")
	if finished.EnvironmentTest.CleanupPending {
		t.Fatal("cancel returned before cleanup")
	}
	if n, err := s.store.NodeByID(finished.NodeID); err != nil || n.DeletedAt == nil {
		t.Fatal("VM was not deleted", n, err)
	}
	page, err := buildActivityPage([]NodeOperation{finished}, nil, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	a.overlay, a.postEvent = nil, nil
	client, err = daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	a.service.daemonClient = client
	a.activity.page, a.activity.selected = page, finished.ID
	a.openActivityNode()
	viewer, ok = a.overlay.(*tuiOperationOutputView)
	if !ok || !strings.Contains(strings.Join(viewer.record.Output, "\n"), "TEST-LIVE-OUTPUT") {
		t.Fatal("deleted test history did not retain its output")
	}
	a.draw()
	text := renderedScreenText(t, a.vx, 150, 38)
	for _, want := range []string{"Test live-test", "TEST-LIVE-OUTPUT", "workspace deleted", "canceled"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q\n%s", want, text)
		}
	}
}

func TestTUIEnvironmentTestAdmissionRetryAndNavigation(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.view, a.environmentsView = tuiViewTemplates, true
	env := EnvironmentConfig{ID: newID(), Slug: "sample"}
	a.managerData.environments = []EnvironmentConfig{env}
	i := &tuiEnvironmentTestIntent{request: NodeOperationRequest{ID: newID(), Kind: "test-environment", Environment: env.ID}, pending: true, generation: a.managerGeneration}
	a.recipes.tests = map[string]*tuiEnvironmentTestIntent{env.ID: i}
	a.applyEnvironmentTestAdmission(tuiEnvironmentTestAdmissionEvent{intent: i, err: errors.New("lost acknowledgment")})
	if a.recipes.tests[env.ID] != i || i.pending || !strings.Contains(a.status, i.request.ID) {
		t.Fatal("ambiguous admission lost retry identity")
	}
	i.pending = true
	a.view = tuiViewSystem
	a.managerGeneration++
	a.applyEnvironmentTestAdmission(tuiEnvironmentTestAdmissionEvent{intent: i, record: NodeOperation{ID: i.request.ID}})
	if a.view != tuiViewSystem || a.overlay != nil || a.recipes.tests[env.ID] != nil {
		t.Fatal("late admission stole navigation")
	}
	i.pending = true
	a.recipes.tests[env.ID] = i
	a.applyEnvironmentTestAdmission(tuiEnvironmentTestAdmissionEvent{intent: i, err: errors.New("empty recipe"), definitive: true})
	if a.recipes.tests[env.ID] != nil {
		t.Fatal("definitive rejection retained retry")
	}
}

func TestTUIEnvironmentTestRunningActivityIdentifiesRecipe(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		a, _ := newTestManager(t, size[0], size[1])
		a.view = tuiViewActivity
		op := NodeOperation{ID: newID(), NodeID: newID(), NodeSlug: "env-test-generated-private-name", State: "running",
			Request: NodeOperationRequest{Kind: "test-environment"}, EnvironmentTest: &EnvironmentTestState{EnvironmentSlug: "curl-check"}}
		a.activity.page.Running = []NodeOperation{op}
		a.draw()
		if text := renderedScreenText(t, a.vx, size[0], size[1]); !strings.Contains(text, "curl-check") {
			t.Fatal("running test hides its environment", text)
		}
		rows, _ := a.activityRows()
		if rows[0].nodeID != op.NodeID || rows[0].id != op.ID || rows[0].action != activityOpenOperation {
			t.Fatal("recipe display changed the operation's navigation identity")
		}
	}
}

func TestTUIEnvironmentTestOutputScrollingAndLateReply(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		a, _ := newTestManager(t, size[0], size[1])
		a.view, a.environmentsView = tuiViewTemplates, true
		a.managerData.environments = []EnvironmentConfig{{ID: "env", Slug: "example", BootstrapCommands: []string{"true"}}}
		a.draw()
		if !strings.Contains(renderedScreenText(t, a.vx, size[0], size[1]), "t test") {
			t.Fatal("test action hidden", size)
		}
		found := false
		for _, c := range a.recipes.controls {
			if c.action == "t" {
				found = true
				a.handleMouse(vaxis.Mouse{Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress, Col: c.rect.col, Row: c.rect.row})
			}
		}
		if !found || !strings.Contains(a.status, "daemon connection") {
			t.Fatal("test mouse action unavailable", size, a.status)
		}
		r := NodeOperation{ID: newID(), State: "succeeded", EnvironmentTest: &EnvironmentTestState{EnvironmentSlug: "example"}}
		for j := 0; j < 100; j++ {
			r.Output = append(r.Output, fmt.Sprintf("line-%03d", j))
		}
		a.openOperationOutput(r.ID, r)
		v := a.overlay.(*tuiOperationOutputView)
		a.draw()
		if !strings.Contains(renderedScreenText(t, a.vx, size[0], size[1]), "line-099") {
			t.Fatal("output did not follow")
		}
		if text := renderedScreenText(t, a.vx, size[0], size[1]); strings.Contains(text, "K cancel") || strings.Contains(text, "test continues") {
			t.Fatal("completed output advertises an active test", text)
		}
		v.record.State = "running"
		a.draw()
		if !strings.Contains(renderedScreenText(t, a.vx, size[0], size[1]), "K cancel") {
			t.Fatal("running output hides cancellation")
		}
		a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyHome})
		a.draw()
		if !strings.Contains(renderedScreenText(t, a.vx, size[0], size[1]), "line-000") {
			t.Fatal("output cannot scroll to start")
		}
		a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEsc})
		a.applyOperationOutput(tuiOperationOutputEvent{view: v, record: r})
		if a.overlay != nil {
			t.Fatal("late reply reopened output")
		}
	}
}
