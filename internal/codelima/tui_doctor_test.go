package codelima

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/hostinfo"
	"go.rockorager.dev/vaxis"
)

func TestTUIDoctorBadgeRetainsOpenFindingsAcrossViews(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		a, _ := newTestManager(t, size[0], size[1])
		a.doctor.generation = 1
		a.applyDoctorData(tuiDoctorDataEvent{generation: 1, checkedAt: time.Now(), report: DoctorReport{
			Checks:   []DoctorCheck{{Name: "git", Status: "ok"}, {Name: "lima", Status: "fail", Message: "missing limactl"}},
			Warnings: []string{"unreadable node record"},
		}})
		a.host = tuiHostSummary{Capacity: hostinfo.Snapshot{CPUs: 4}, AllocationsKnown: true, AllocatedCPUs: 6}
		for _, view := range []tuiView{tuiViewNodes, tuiViewTemplates, tuiViewSystem} {
			a.view = view
			a.applyManagerData(tuiManagerDataEvent{view: view})
			a.draw()
			screen := renderedScreenText(t, a.vx, size[0], size[1])
			label := "5 System ⚠3"
			if size[0] < 100 && view != tuiViewSystem {
				label = "5 ⚠3"
			}
			if !strings.Contains(screen, label) {
				t.Fatalf("open findings lost at %v, view %v:\n%s", size, view, screen)
			}
			for col := range size[0] {
				if renderedCellGrapheme(t, a.vx, col, 0) == "⚠" && renderedCellStyle(t, a.vx, col, 0).Foreground != a.managerTheme().amber.Foreground {
					t.Fatal("System badge lost its warning color")
				}
			}
		}
		a.doctor.generation++
		a.applyDoctorData(tuiDoctorDataEvent{generation: 2, checkedAt: time.Now(), report: DoctorReport{}})
		a.host.AllocatedCPUs = 0
		if a.doctorIssueCount() != 0 {
			t.Fatal("resolved findings retained a badge")
		}
	}
}

func TestTUIDoctorReadsCurrentSettingsWithoutRewritingThem(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.service, _ = newTestService(t)
	if _, err := a.service.SettingsUpdate(context.Background(), "shell.mode", "attach"); err != nil {
		t.Fatal(err)
	}
	a.startDoctorCheck(false, true)
	path := a.service.store.configPath()
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	broken := []byte("daemon: [unfinished")
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	a.startDoctorCheck(false, true)
	if !slices.ContainsFunc(a.doctor.report.Checks, func(c DoctorCheck) bool { return c.Name == "config" && c.Status == "fail" }) {
		t.Fatal("long-lived doctor checked stale startup settings")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(broken) {
		t.Fatal("read-only doctor rewrote malformed settings")
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	a.startDoctorCheck(false, true)
	if !slices.ContainsFunc(a.doctor.report.Checks, func(c DoctorCheck) bool { return c.Name == "config" && c.Status == "ok" }) {
		t.Fatal("restored settings retained a failed check")
	}
}

func TestTUIDoctorFailureKeepsLastSnapshotExplicitlyStale(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.view = tuiViewSystem
	a.doctor.generation = 4
	a.applyDoctorData(tuiDoctorDataEvent{generation: 4, checkedAt: time.Now(), report: DoctorReport{Warnings: []string{"saved warning"}}})
	a.doctor.generation = 5
	a.applyDoctorData(tuiDoctorDataEvent{generation: 4, checkedAt: time.Now()})
	if a.doctorIssueCount() != 1 {
		t.Fatal("late result erased a current finding")
	}
	a.applyDoctorData(tuiDoctorDataEvent{generation: 5, err: errors.New("read failed")})
	a.draw()
	screen := renderedScreenText(t, a.vx, 80, 24)
	for _, want := range []string{"⚠1?", "read failed", "last check", "saved warning"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("missing %q:\n%s", want, screen)
		}
	}
}

func TestTUIDoctorFirstCheckFailureHasReadableDetails(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.view = tuiViewSystem
	a.doctor.err = errors.New(strings.Repeat("long failure ", 200) + "final failure evidence")
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
	if _, ok := a.overlay.(*tuiTextView); !ok {
		t.Fatal("failed first check has no full error details")
	}
	a.draw()
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEnd})
	a.draw()
	if !strings.Contains(renderedScreenText(t, a.vx, 80, 24), "final failure evidence") {
		t.Fatal("failed check details lost the error tail")
	}
}

type blockedDoctorSandbox struct {
	SandboxClient
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (s *blockedDoctorSandbox) Version(ctx context.Context) (string, error) {
	s.calls.Add(1)
	if _, ok := ctx.Deadline(); !ok {
		return "", errors.New("doctor work is not bounded")
	}
	select {
	case s.entered <- struct{}{}:
	default:
	}
	select {
	case <-s.release:
		return s.SandboxClient.Version(ctx)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestTUIDoctorSingleFlightSurvivesNavigationAndCancelsOnClose(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.service, _ = newTestService(t)
	sandbox := &blockedDoctorSandbox{SandboxClient: a.service.sandbox, entered: make(chan struct{}, 2), release: make(chan struct{})}
	a.service.sandbox = sandbox
	events := make(chan vaxis.Event, 8)
	a.postEvent = func(e vaxis.Event) { events <- e }
	a.startDoctorCheck(false, true)
	select {
	case <-sandbox.entered:
	case <-time.After(time.Second):
		t.Fatal("check did not start asynchronously")
	}
	for range 8 {
		a.startDoctorCheck(false, true)
	}
	if sandbox.calls.Load() != 1 {
		t.Fatal("overlapping doctor checks")
	}
	a.view = tuiViewTemplates
	close(sandbox.release)
	select {
	case e := <-events:
		if _, err := a.handleEvent(e); err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("check did not complete")
	}
	if a.doctor.loading || a.doctor.checkedAt.IsZero() || a.view != tuiViewTemplates {
		t.Fatal("navigation discarded the global snapshot")
	}
	a.startDoctorCheck(false, false)
	if sandbox.calls.Load() != 1 {
		t.Fatal("TTL did not suppress repeated work")
	}
	sandbox.release = make(chan struct{})
	a.startDoctorCheck(false, true)
	select {
	case <-sandbox.entered:
	case <-time.After(time.Second):
		t.Fatal("forced re-check did not start")
	}
	a.cancelDoctorCheck()
	select {
	case e := <-events:
		if _, err := a.handleEvent(e); err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("closing did not cancel doctor work")
	}
	if a.doctor.loading || a.doctor.err != nil {
		t.Fatal("cancelled stale completion changed retained state")
	}
}

func TestTUIDoctorRepairRechecksAndPreservesQuarantinedBytes(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	var workspace string
	a.service, workspace = newTestService(t)
	node, err := a.service.NodeCreate(context.Background(), NodeCreateInput{Slug: "broken-doctor", Directory: workspace})
	if err != nil {
		t.Fatal(err)
	}
	corruptSavedNode(t, a.service.store, node)
	a.startDoctorCheck(false, true)
	if !containsSubstring(a.doctor.report.Warnings, "unreadable node metadata") {
		t.Fatal("missing corrupt-record warning")
	}
	if _, err := os.Stat(a.service.store.nodeDir(node.ID)); err != nil {
		t.Fatal("check mutated the record", err)
	}
	a.view = tuiViewSystem
	a.handleKey(vaxis.Key{Keycode: 'R', Text: "R", EventType: vaxis.EventRelease})
	if _, err := os.Stat(a.service.store.nodeDir(node.ID)); err != nil {
		t.Fatal("key release repaired metadata")
	}
	a.handleKey(vaxis.Key{Keycode: 'R', Text: "R"})
	if containsSubstring(a.doctor.report.Warnings, "unreadable node metadata") || containsSubstring(a.doctor.report.Warnings, "was moved") {
		t.Fatal("repaired record counted as an open warning")
	}
	if _, err := os.Stat(a.service.store.nodeDir(node.ID)); !os.IsNotExist(err) {
		t.Fatal("repair did not quarantine", err)
	}
	entries, err := os.ReadDir(a.service.store.quarantineRoot())
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	content, err := os.ReadFile(a.service.store.quarantineRoot() + "/" + entries[0].Name() + "/node.yaml")
	if err != nil || string(content) != corruptNodeYAML {
		t.Fatal("quarantine changed saved bytes", err)
	}
	if !slices.ContainsFunc(a.messages.Entries(), func(m tuiMessage) bool { return strings.Contains(m.Text, "nothing was deleted") }) {
		t.Fatal("repair result was not retained in Activity")
	}
}

func TestTUIDoctorQueuedRepairRunsOnceAndRecheckFailureStaysUnknown(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.service, _ = newTestService(t)
	sandbox := &blockedDoctorSandbox{SandboxClient: a.service.sandbox, entered: make(chan struct{}, 4), release: make(chan struct{})}
	a.service.sandbox = sandbox
	events := make(chan vaxis.Event, 16)
	a.postEvent = func(e vaxis.Event) { events <- e }
	a.startDoctorCheck(false, true)
	select {
	case <-sandbox.entered:
	case <-time.After(time.Second):
		t.Fatal("check did not start")
	}
	for range 4 {
		a.startDoctorCheck(true, true)
	}
	if !a.doctor.queuedRepair || sandbox.calls.Load() != 1 {
		t.Fatal("repair was lost or overlapped the active check")
	}
	close(sandbox.release)
	for a.doctor.loading {
		select {
		case event := <-events:
			if _, err := a.handleEvent(event); err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("queued repair did not finish")
		}
	}
	if sandbox.calls.Load() != 3 || a.doctor.queuedRepair {
		t.Fatal("queued repair ran more than once", sandbox.calls.Load())
	}
	awaitAsyncTUIState(t, a, events, func() bool { return !a.refreshInFlight }, "post-repair fleet refresh")
	a.doctor.generation++
	a.applyDoctorData(tuiDoctorDataEvent{generation: a.doctor.generation, repair: true, err: errors.New("recheck unavailable")})
	if !strings.Contains(a.status, "re-check unavailable") || !strings.Contains(a.doctorBadge(), "?") {
		t.Fatal("repair completion claimed a known remaining count after failed re-check", a.status)
	}
	awaitAsyncTUIState(t, a, events, func() bool { return !a.refreshInFlight }, "failed re-check fleet refresh")
}

func TestTUIDoctorRowsAndDetailsRemainReachable(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		a, _ := newTestManager(t, size[0], size[1])
		a.view = tuiViewSystem
		a.doctor.checkedAt = time.Now()
		for i := range 30 {
			a.doctor.report.Checks = append(a.doctor.report.Checks, DoctorCheck{Name: strings.Repeat("x", i+1), Status: "ok", Message: "healthy"})
		}
		a.doctor.report.Warnings = []string{strings.Repeat("long warning ", 100) + "final evidence"}
		a.moveDoctorSelection(30)
		a.draw()
		if !strings.Contains(renderedScreenText(t, a.vx, size[0], size[1]), "long warning") {
			t.Fatal("selected warning is not visible")
		}
		a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
		v, ok := a.overlay.(*tuiTextView)
		if !ok {
			t.Fatal("Enter did not open details")
		}
		a.draw()
		a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEnd})
		a.draw()
		if !strings.Contains(renderedScreenText(t, a.vx, size[0], size[1]), "final evidence") {
			t.Fatal("details clipped long findings", v.scroll)
		}
		a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEsc})
		a.draw()
		for _, control := range a.doctor.controls {
			if control.action == "details" {
				a.handleMouse(vaxis.Mouse{Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress, Col: control.rect.col, Row: control.rect.row})
				break
			}
		}
		if _, ok := a.overlay.(*tuiTextView); !ok {
			t.Fatal("mouse details action is unreachable")
		}
	}
}
