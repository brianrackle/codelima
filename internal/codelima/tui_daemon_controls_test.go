package codelima

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"go.rockorager.dev/vaxis"
)

func TestTUIDaemonControlRestartRequiresConfirmationAndDoesNotBlockNavigation(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.view, a.systemPanel = tuiViewSystem, tuiSystemDaemon
	events := make(chan vaxis.Event, 4)
	a.postEvent = func(e vaxis.Event) { events <- e }
	started, release := make(chan string, 2), make(chan struct{})
	a.systemDaemon.lifecycleRun = func(ctx context.Context, action string) (string, error) {
		if _, ok := ctx.Deadline(); !ok {
			return "", errors.New("missing action deadline")
		}
		started <- action
		select {
		case <-release:
			return "restart complete", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	a.handleKey(vaxis.Key{Keycode: 'S', Text: "S"})
	d, ok := a.overlay.(*tuiDialog)
	if !ok || !strings.Contains(strings.Join(d.Description, " "), "shell") {
		t.Fatal("restart lacks its impact confirmation")
	}
	select {
	case <-started:
		t.Fatal("opening confirmation restarted daemon")
	default:
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEsc})
	select {
	case <-started:
		t.Fatal("dismissing confirmation restarted daemon")
	default:
	}
	a.runSystemDaemonAction("restart")
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
	select {
	case action := <-started:
		if action != "restart" {
			t.Fatal(action)
		}
	case <-time.After(time.Second):
		t.Fatal("confirmed restart did not start")
	}
	a.runSystemDaemonAction("update")
	select {
	case <-started:
		t.Fatal("overlapping lifecycle action admitted")
	default:
	}
	a.handleKey(vaxis.Key{Keycode: '1', Text: "1"})
	if a.view != tuiViewNodes {
		t.Fatal("lifecycle worker blocked navigation")
	}
	close(release)
	handleDaemonControlTestEvent(t, a, events)
	if a.view != tuiViewNodes || !strings.Contains(a.status, "restart complete") {
		t.Fatal("completion stole navigation or was lost", a.status)
	}
}

func TestTUIDaemonControlUnknownReplyAndCancellationAreExplicit(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.view = tuiViewSystem
	a.systemDaemon.lifecycleRun = func(context.Context, string) (string, error) {
		return "", &daemonclient.DeliveryError{Outcome: daemonclient.DeliveryOutcomeUnknown, Err: errors.New("reply lost")}
	}
	a.runSystemDaemonAction("update")
	if !strings.Contains(a.status, "outcome unknown") {
		t.Fatal(a.status)
	}
	events := make(chan vaxis.Event, 1)
	a.postEvent = func(e vaxis.Event) { events <- e }
	started := make(chan struct{})
	a.systemDaemon.lifecycleRun = func(ctx context.Context, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	a.runSystemDaemonAction("update")
	<-started
	a.cancelDaemonWork()
	a.status = "after shutdown"
	handleDaemonControlTestEvent(t, a, events)
	if a.status != "after shutdown" {
		t.Fatal("late completion changed shutdown state")
	}
}

func TestTUIDaemonControlCaptureContinuesWhenClosedAndDoesNotReplaceLogView(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.service, _ = newTestService(t)
	a.view, a.systemPanel = tuiViewSystem, tuiSystemDaemon
	events := make(chan vaxis.Event, 4)
	a.postEvent = func(e vaxis.Event) { events <- e }
	release := make(chan struct{})
	var calls atomic.Int32
	a.systemDaemon.captureRun = func(context.Context) (daemonDiagnosticResult, error) {
		calls.Add(1)
		<-release
		return daemonDiagnosticResult{Directory: "/local/evidence", Summary: "captured evidence"}, nil
	}
	a.handleKey(vaxis.Key{Keycode: 'D', Text: "D"})
	first := a.overlay
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEsc})
	a.runSystemDaemonAction("diagnose")
	if a.overlay != first {
		t.Fatal("duplicate capture replaced the running one")
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEsc})
	a.handleKey(vaxis.Key{Keycode: 'L', Text: "L"})
	log, ok := a.overlay.(*tuiDaemonLogView)
	if !ok {
		t.Fatal("log unavailable during capture")
	}
	close(release)
	handleDaemonControlTestEvent(t, a, events)
	handleDaemonControlTestEvent(t, a, events)
	if calls.Load() != 1 || a.overlay != log {
		t.Fatal("capture duplicated or replaced a foreground view")
	}
	if !strings.Contains(a.status, "/local/evidence") {
		t.Fatal("background result path was lost", a.status)
	}
}

func TestTUIDaemonControlLogReloadAndAllControlsRemainReachable(t *testing.T) {
	for _, size := range [][2]int{{150, 38}, {80, 24}} {
		a, _ := newTestManager(t, size[0], size[1])
		a.service, _ = newTestService(t)
		a.view, a.systemPanel = tuiViewSystem, tuiSystemDaemon
		a.draw()
		screen := renderedScreenText(t, a.vx, size[0], size[1])
		for _, want := range []string{"u update, keep sessions", "S restart", "L daemon log", "D diagnose freeze"} {
			if !strings.Contains(screen, want) {
				t.Fatalf("missing %q at %v:\n%s", want, size, screen)
			}
		}
		logPath := filepath.Join(a.service.cfg.MetadataRoot, "_daemon", "daemon.log")
		if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(logPath, []byte("first evidence\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, c := range a.systemDaemon.controls {
			if c.action == "log" {
				a.handleMouse(vaxis.Mouse{EventType: vaxis.EventPress, Button: vaxis.MouseLeftButton, Col: c.rect.col, Row: c.rect.row})
			}
		}
		v, ok := a.overlay.(*tuiDaemonLogView)
		if !ok || !strings.Contains(v.text, "first evidence") {
			t.Fatal("daemon log did not load")
		}
		if err := os.WriteFile(logPath, []byte(strings.Repeat("line\n", 2200)+"new tail 世界\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		a.handleKey(vaxis.Key{Keycode: 'C', Text: "C"})
		a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnd})
		a.draw()
		// The cell dump includes a blank continuation cell after wide glyphs.
		if !strings.Contains(renderedScreenText(t, a.vx, size[0], size[1]), "new tail 世 界") {
			t.Fatalf("log reload/scroll lost the latest evidence at %v:\n%s", size, renderedScreenText(t, a.vx, size[0], size[1]))
		}
		if strings.Contains(v.text, "first evidence") {
			t.Fatal("reloaded log retained replaced content")
		}
		a.openKeyHelp()
		p := a.overlay.(*tuiPalette)
		p.input.SetContent("System")
		p.filter()
		a.draw()
		for _, want := range []string{"update daemon, keep sessions", "restart daemon; ends live shell processes", "open daemon log", "diagnose freeze; save local evidence"} {
			if !strings.Contains(renderedScreenText(t, a.vx, size[0], size[1]), want) {
				t.Fatalf("daemon help clipped %q at %v", want, size)
			}
		}
	}
}

func handleDaemonControlTestEvent(t *testing.T, a *vaxisTUIApp, events <-chan vaxis.Event) {
	t.Helper()
	select {
	case e := <-events:
		if _, err := a.handleEvent(e); err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("action completion not delivered")
	}
}
