package codelima

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"go.rockorager.dev/vaxis"
)

// Mutations, evidence capture and log reads have separate workers. Evidence
// remains accessible while a lifecycle request is waiting for its reply.
type tuiDaemonWorkState struct {
	lifecycle           string
	lifecycleGeneration uint64
	lifecycleCancel     context.CancelFunc
	lifecycleRun        func(context.Context, string) (string, error)
	capture             *tuiDaemonCaptureView
	captureRun          func(context.Context) (daemonDiagnosticResult, error)
	log                 *tuiDaemonLogView
}

type tuiDaemonLifecycleEvent struct {
	generation      uint64
	action, message string
	err             error
}

func (a *vaxisTUIApp) daemonWorkContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, timeout)
}

func (a *vaxisTUIApp) runSystemDaemonAction(action string) {
	a.systemPanel = tuiSystemDaemon
	switch action {
	case "details":
		a.openDaemonDetails()
	case "check":
		a.startDaemonOverview(true)
	case "update":
		a.startDaemonLifecycle(action)
	case "restart":
		if a.systemDaemon.lifecycle != "" {
			a.setStatus(slog.LevelInfo, "Daemon "+a.systemDaemon.lifecycle+" is still running")
			return
		}
		a.overlay = newTUIDialog("Restart daemon", "Restart", []string{
			"Restart ends running shell processes and interrupts unfinished operations.",
			"Saved tabs reopen as fresh shells unless daemon.restore is set to forget.",
			"Keep this TUI open until restart finishes. Update keeps live sessions.",
		}, nil, func(map[string]string) error { a.startDaemonLifecycle("restart"); return nil })
	case "log":
		a.openDaemonLog()
	case "diagnose":
		a.openDaemonCapture()
	}
}

func (a *vaxisTUIApp) startDaemonLifecycle(action string) {
	s := &a.systemDaemon
	if s.lifecycle != "" {
		a.setStatus(slog.LevelInfo, "Daemon "+s.lifecycle+" is still running")
		return
	}
	run := s.lifecycleRun
	if run == nil {
		if a.service == nil {
			return
		}
		service := a.service
		run = func(ctx context.Context, action string) (string, error) {
			if action == "restart" {
				status, err := restartDaemon(ctx, service)
				return fmt.Sprintf("Daemon restart complete · pid %d", status.PID), err
			}
			result, err := updateDaemon(ctx, service, daemonUpdateOptions{})
			if err != nil {
				return "", err
			}
			if live, _ := result["live_handoff"].(bool); live {
				return "Daemon update complete · live sessions kept", nil
			}
			return "Daemon update acknowledged · check status and sessions", nil
		}
	}
	ctx, cancel := a.daemonWorkContext(3 * time.Minute)
	s.lifecycleGeneration++
	s.lifecycle, s.lifecycleCancel = action, cancel
	generation := s.lifecycleGeneration
	a.setStatus(slog.LevelInfo, "Daemon "+action+" running…")
	load := func() tuiDaemonLifecycleEvent {
		defer cancel()
		message, err := run(ctx, action)
		return tuiDaemonLifecycleEvent{generation, action, message, err}
	}
	if a.postEvent == nil {
		a.applyDaemonLifecycle(load())
		return
	}
	go func() { a.postCompletion(load()) }()
}

func (a *vaxisTUIApp) applyDaemonLifecycle(e tuiDaemonLifecycleEvent) {
	s := &a.systemDaemon
	if e.generation != s.lifecycleGeneration {
		return
	}
	s.lifecycle, s.lifecycleCancel = "", nil
	level, message := slog.LevelInfo, e.message
	if e.err != nil {
		level, message = slog.LevelError, "Daemon "+e.action+" failed: "+e.err.Error()
		var delivery *daemonclient.DeliveryError
		if errors.As(e.err, &delivery) && delivery.Outcome == daemonclient.DeliveryOutcomeUnknown {
			message = "Daemon " + e.action + " outcome unknown · inspect status before retry: " + e.err.Error()
		}
	}
	a.setStatus(level, message)
	a.cancelDaemonOverview()
	a.startDaemonOverview(true)
}

func (a *vaxisTUIApp) cancelDaemonWork() {
	s := &a.systemDaemon
	if s.lifecycleCancel != nil {
		s.lifecycleCancel()
	}
	s.lifecycleGeneration++
	s.lifecycle, s.lifecycleCancel = "", nil
	if s.capture != nil {
		if s.capture.cancel != nil {
			s.capture.cancel()
		}
		s.capture = nil
	}
	if s.log != nil {
		if s.log.cancel != nil {
			s.log.cancel()
		}
		s.log = nil
	}
}

type tuiDaemonLogView struct {
	tuiTextView
	app           *vaxisTUIApp
	path, content string
	loading       bool
	cancel        context.CancelFunc
}

type tuiDaemonLogEvent struct {
	view *tuiDaemonLogView
	text string
	err  error
}

func (a *vaxisTUIApp) openDaemonLog() {
	if a.service == nil {
		return
	}
	v := a.systemDaemon.log
	if v == nil {
		v = &tuiDaemonLogView{app: a, path: filepath.Join(a.service.cfg.MetadataRoot, "_daemon", "daemon.log")}
		v.title, v.text = "Daemon log", "Loading daemon log…"
		a.systemDaemon.log = v
	}
	a.overlay = v
	v.load()
}

func (v *tuiDaemonLogView) load() {
	if v.loading {
		return
	}
	ctx, cancel := v.app.daemonWorkContext(5 * time.Second)
	v.loading, v.cancel = true, cancel
	path := v.path
	load := func() tuiDaemonLogEvent {
		defer cancel()
		e := tuiDaemonLogEvent{view: v}
		if e.err = ctx.Err(); e.err != nil {
			return e
		}
		data, offset, missing, err := readNodeLogTail(path, diagnosticLogLimit)
		e.err = errors.Join(err, ctx.Err())
		lines := strings.Split(string(data), "\n")
		truncated := offset > 0 || len(lines) > 2000
		lines = lines[max(0, len(lines)-2000):]
		e.text = path + "\n"
		if missing {
			e.text += "\nNo daemon log exists yet."
		} else {
			if truncated {
				e.text += "Showing bounded tail (up to 256 KiB / 2000 lines).\n"
			}
			e.text += "\n" + strings.Join(lines, "\n")
		}
		return e
	}
	if v.app.postEvent == nil {
		v.app.applyDaemonLog(load())
		return
	}
	go func() { v.app.postCompletion(load()) }()
}

func (a *vaxisTUIApp) applyDaemonLog(e tuiDaemonLogEvent) {
	if a.systemDaemon.log != e.view {
		return
	}
	v := e.view
	v.loading, v.cancel = false, nil
	if e.err != nil {
		v.text = "Log read failed: " + e.err.Error() + "\n\n" + v.content
		return
	}
	v.content, v.text = e.text, e.text
}

func (v *tuiDaemonLogView) Update(event vaxis.Event) (bool, error) {
	if key, ok := event.(vaxis.Key); ok && tuiKeyActivates(key, true) {
		switch {
		case key.MatchString("C"):
			v.load()
			return false, nil
		case key.MatchString("o"):
			if err := v.app.openFilePager(v.path); err != nil {
				v.app.setStatus(slog.LevelError, err.Error())
			}
			return false, nil
		}
	}
	return v.tuiTextView.Update(event)
}

func (v *tuiDaemonLogView) FooterHint() string {
	return "↑↓ scroll · Home/End · C reload · o pager · Esc close"
}

type tuiDaemonCaptureView struct {
	tuiTextView
	loading bool
	cancel  context.CancelFunc
}

type tuiDaemonCaptureEvent struct {
	view   *tuiDaemonCaptureView
	result daemonDiagnosticResult
	err    error
}

func (a *vaxisTUIApp) openDaemonCapture() {
	s := &a.systemDaemon
	if s.capture != nil && s.capture.loading {
		a.overlay = s.capture
		return
	}
	run := s.captureRun
	if run == nil {
		if a.service == nil {
			return
		}
		capture := newDaemonDiagnosticCapture(a.service.cfg.MetadataRoot, daemonDiagnosticOptions{SampleSeconds: 10})
		run = capture.capture
	}
	ctx, cancel := a.daemonWorkContext(2 * time.Minute)
	v := &tuiDaemonCaptureView{loading: true, cancel: cancel}
	v.title, v.text = "Diagnose terminal freeze", "Capturing local evidence…\n\nEach daemon probe has a five-second deadline.\nEsc closes this view while capture continues; D reopens it.\nThe saved bundle includes logs, local paths and terminal contents."
	s.capture, a.overlay = v, v
	load := func() tuiDaemonCaptureEvent {
		defer cancel()
		result, err := run(ctx)
		return tuiDaemonCaptureEvent{v, result, err}
	}
	if a.postEvent == nil {
		a.applyDaemonCapture(load())
		return
	}
	go func() { a.postCompletion(load()) }()
}

func (a *vaxisTUIApp) applyDaemonCapture(e tuiDaemonCaptureEvent) {
	if a.systemDaemon.capture != e.view {
		return
	}
	e.view.loading, e.view.cancel = false, nil
	e.view.text = e.result.Summary
	level, message := slog.LevelInfo, fmt.Sprintf("Diagnostic capture saved · %s · %d probe failures", e.result.Directory, e.result.FailedProbes)
	if e.err != nil {
		level, message = slog.LevelError, "Diagnostic capture incomplete: "+e.err.Error()
		if e.result.Directory != "" {
			message += " · partial evidence: " + e.result.Directory
		}
		e.view.text = message + "\n\n" + e.view.text
	}
	a.setStatus(level, message)
}
