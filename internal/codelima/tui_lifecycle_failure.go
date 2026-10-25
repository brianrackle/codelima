package codelima

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

type tuiLifecycleFailure struct {
	title, detail, message string
	step                   *BootstrapStep
}

func (a *vaxisTUIApp) lifecycleFailure() tuiLifecycleFailure {
	info := tuiLifecycleFailure{title: "Startup failed", message: "Inspect Logs and the frozen steps in Spec before retrying."}
	nodeID := a.state.selectedEntry().node.ID
	operation := NodeOperation{}
	if a.lifecycle.nodeID == nodeID {
		operation = a.lifecycle.operation
	}
	if operation.State == "interrupted" {
		info.title = "Startup interrupted"
		info.message = "Execution ended without a known finish; inspect output before starting again."
		return info
	}
	if operation.State == "failed" {
		info.message = plainLogText(operation.Error)
		for _, phase := range initialNodeOperationPhases("start") {
			if phase.ID == operation.CurrentPhase {
				info.title = phase.Label + " failed"
			}
		}
		// A new boot/import/storage failure must not be blamed on a previous
		// bootstrap attempt that still has useful, but older, step evidence.
		if operation.CurrentPhase != "" && operation.CurrentPhase != "bootstrap" && operation.CurrentPhase != "validation" {
			return info
		}
	}
	if a.spec.nodeID != nodeID || !a.spec.loaded || a.spec.snapshot.BootstrapError != nil {
		return info
	}
	steps := a.spec.snapshot.Bootstrap.Steps
	for i := range steps {
		step := &steps[i]
		if step.State != BootstrapStepFailed {
			continue
		}
		if operation.State == "failed" && !operation.CreatedAt.IsZero() && (step.StartedAt == nil || step.StartedAt.Before(operation.CreatedAt)) {
			continue
		}
		info.step = step
		if step.Kind == "validation" {
			info.title = "Agent validation failed"
		} else {
			total := 0
			for _, candidate := range steps {
				if candidate.Kind == "bootstrap" {
					total++
				}
			}
			info.title = fmt.Sprintf("Bootstrap failed at step %d of %d", i+1, total)
		}
		info.detail = coalesce(step.SourceName, step.SourceKind)
		if step.ExitCode != nil {
			info.detail += fmt.Sprintf(" · exit %d", *step.ExitCode)
		}
		if step.DurationMillis != nil {
			info.detail += " · after " + (time.Duration(*step.DurationMillis) * time.Millisecond).Round(time.Millisecond).String()
		}
		info.message = plainLogText(step.Error)
		return info
	}
	return info
}

func lifecycleVMState(node Node) string {
	if node.LastRuntimeObservation == nil {
		return "VM state unavailable"
	}
	switch {
	case node.LastRuntimeObservation.Status == ObservationRunning:
		return fmt.Sprintf("VM running · holds %d vCPU · %s", node.VCPUs, formatTUIResourceSize(node.MemoryMiB))
	case !node.LastRuntimeObservation.Exists || node.LastRuntimeObservation.Status == ObservationStopped:
		return "VM stopped · allocation released"
	default:
		return "VM " + string(node.LastRuntimeObservation.Status)
	}
}

func (a *vaxisTUIApp) drawLifecycleFailure(win vaxis.Window, record tuiFleetRecord, theme tuiTheme) {
	width, height := win.Size()
	info := a.lifecycleFailure()
	bannerHeight := min(7, max(3, height-5))
	banner := border.All(win.New(0, 0, width, bannerHeight), theme.coral)
	tuiLine(banner, 0, "✕ "+info.title, theme.coral)
	if info.step != nil {
		tuiLine(banner, 1, "$ "+plainLogText(info.step.Command), theme.text)
	}
	tuiLine(banner, 2, info.detail, theme.amber)
	tuiLine(banner, 3, info.message, theme.coral)
	tuiLine(banner, 4, "Review output before retrying; a command may have partial effects.", theme.dim)
	lower := win.New(0, bannerHeight, width, max(1, height-bannerHeight))
	lowerWidth, lowerHeight := lower.Size()
	wide := width >= 118 && height >= 22
	stateWidth, stateCol := lowerWidth, 0
	if wide {
		leftWidth := (lowerWidth - 2) / 2
		stepsBox := border.All(lower.New(0, 0, leftWidth, lowerHeight), theme.border)
		tuiLine(stepsBox, 0, "Start, step by step", theme.muted)
		row := 1
		if a.lifecycle.nodeID == record.Node.ID {
			for _, phase := range a.lifecycle.operation.Phases {
				if row >= lowerHeight-2 {
					break
				}
				icon, style := lifecyclePhaseStyle(phase.State, theme)
				tuiLine(stepsBox, row, fmt.Sprintf("%s %-18s %6s", icon, phase.Label, lifecyclePhaseDuration(phase)), style)
				row++
			}
		}
		if a.spec.nodeID == record.Node.ID && a.spec.loaded {
			if row > 1 && row < lowerHeight-2 {
				tuiLine(stepsBox, row, "Recorded bootstrap attempts", theme.dim)
				row++
			}
			for i, step := range a.spec.snapshot.Bootstrap.Steps {
				if row >= lowerHeight-2 {
					break
				}
				icon, style := lifecyclePhaseStyle(string(step.State), theme)
				tuiLine(stepsBox, row, fmt.Sprintf("%s %d · %s · %s", icon, i+1, step.SourceName, plainLogText(step.Command)), style)
				row++
			}
		}
		stateWidth, stateCol = lowerWidth-leftWidth-2, leftWidth+2
	}
	stateHeight := lowerHeight
	if wide {
		stateHeight = min(6, lowerHeight)
	}
	state := border.All(lower.New(stateCol, 0, stateWidth, stateHeight), theme.border)
	tuiLine(state, 0, "What's still running", theme.muted)
	tuiLine(state, 1, lifecycleVMState(record.Node), theme.text)
	portState := "ports held until ready"
	if !record.Node.DaemonPortForwarding && len(record.Node.Ports) != 0 {
		portState = "legacy ports · stop/start to migrate"
	}
	tuiLine(state, 2, "node "+string(record.Node.Status)+" · "+portState, theme.coral)
	if nodeGuestShellReady(record.Node) {
		tuiLine(state, 3, "x guest shell allowed for inspection", theme.text)
	} else {
		tuiLine(state, 3, "Guest inspection needs an available running VM.", theme.dim)
	}
	if !wide {
		tuiLine(state, 4, a.lifecycleRetryHint()+" · e environment · E template · l logs", theme.muted)
		return
	}
	actions := border.All(lower.New(stateCol, stateHeight+1, stateWidth, max(1, lowerHeight-stateHeight-1)), theme.mint)
	tuiLine(actions, 0, "Actions", theme.muted)
	for i, line := range []string{a.lifecycleRetryHint(), a.keyHints("{node.shell} shell in to inspect"), a.keyHints("{failure.edit_environment} edit source environment · future nodes"), a.keyHints("{failure.edit_template} edit source template · future nodes"), a.keyHints("{node.toggle} stop/start VM · {node.delete} delete node"), a.keyHints("{node.logs} captured output · {navigation.next_tab} Spec for full steps")} {
		style := theme.text
		switch {
		case i == 1 && !nodeGuestShellReady(record.Node):
			line, style = "x inspection unavailable · VM not ready", theme.dim
		case i == 2 && (info.step == nil || info.step.SourceKind != "environment" || info.step.SourceID == ""):
			line, style = "e source environment unavailable", theme.dim
		case i == 3 && a.spec.snapshot.Template == nil:
			line, style = "E source template unavailable", theme.dim
		}
		tuiLine(actions, i+1, line, style)
	}
}

func (a *vaxisTUIApp) lifecycleRetryHint() string {
	if a.state.selectedEntry().node.Status == NodeStatusFailed && a.lifecycleFailure().step != nil {
		return a.keyHints("{node.restart} retry failed step")
	}
	return a.keyHints("{node.toggle} start/stop VM")
}

func (a *vaxisTUIApp) handleFailureAction(action string) bool {
	if a.detailTab != tuiDetailOverview || !a.nodeLifecycleFailed() {
		return false
	}
	info := a.lifecycleFailure()
	switch {
	case action == "node.shell" && !nodeIsRunning(a.state.selectedEntry().node):
		a.setStatus(slog.LevelWarn, "The VM must be running before opening an inspection shell")
	case action == "node.logs" && info.step != nil:
		a.openSpecCommandLogs(info.step.Command, info.step.AttemptID)
	case action == "failure.edit_template":
		if a.spec.nodeID != a.state.selectedEntry().node.ID || a.spec.snapshot.Template == nil {
			a.setStatus(slog.LevelWarn, "The source template is unavailable")
			return true
		}
		a.openUpdateConfigurationDialog(*a.spec.snapshot.Template)
	case action == "failure.edit_environment":
		if info.step == nil || info.step.SourceKind != "environment" || info.step.SourceID == "" || a.service == nil {
			a.setStatus(slog.LevelWarn, "No source environment is available for this failure")
			return true
		}
		id := info.step.SourceID
		if err := a.startMetadataOperation("Loading source environment", "environments", func(ctx context.Context, service *Service) (tuiOperationResult, error) {
			environment, err := service.EnvironmentConfigShow(ctx, id)
			if err != nil {
				return tuiOperationResult{}, err
			}
			return tuiOperationResult{Apply: func(a *vaxisTUIApp) error { a.openEnvironmentConfigCommandMenu(environment); return nil }}, nil
		}); err != nil {
			a.setStatus(slog.LevelError, err.Error())
		}
	default:
		return false
	}
	return true
}
