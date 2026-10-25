package codelima

import (
	"fmt"
	"slices"
	"time"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

func (a *vaxisTUIApp) drawNodeLifecycle(win vaxis.Window, record tuiFleetRecord, theme tuiTheme) bool {
	if !a.nodeStarting() {
		if a.nodeLifecycleFailed() {
			a.drawLifecycleFailure(win, record, theme)
			return true
		}
		return false
	}
	operation, _ := a.activeStartOperation()
	width, height := win.Size()
	wide := width >= 118 && height >= 22
	progressWidth, progressHeight := width, min(max(10, len(operation.Phases)+3), max(4, height-5))
	if wide {
		progressWidth, progressHeight = (width-2)/2, height
	}
	progress := border.All(win.New(0, 0, progressWidth, progressHeight), theme.mint)
	title := "Starting " + record.Node.Slug
	if operation.Request.Kind == "restart" {
		title = "Restarting " + record.Node.Slug
	}
	if operation.State == "canceling" {
		title = "Canceling startup · stopping VM"
	}
	tuiLine(progress, 0, title, theme.mint)
	row, completed := 1, 0
	visiblePhases := operation.Phases
	if !wide && len(visiblePhases) > progressHeight-3 {
		count := max(1, progressHeight-3)
		active := slices.IndexFunc(visiblePhases, func(phase NodeOperationPhase) bool { return phase.ID == operation.CurrentPhase })
		start := max(0, active-count+1)
		visiblePhases = visiblePhases[start:min(len(visiblePhases), start+count)]
		tuiLine(progress, 0, fmt.Sprintf("%s · phases %d–%d/%d", title, start+1, start+len(visiblePhases), len(operation.Phases)), theme.mint)
	}
	for _, phase := range operation.Phases {
		if phase.State == "succeeded" || phase.State == "skipped" {
			completed++
		}
	}
	for _, phase := range visiblePhases {
		if row >= progressHeight-2 {
			break
		}
		icon, style := lifecyclePhaseStyle(phase.State, theme)
		text := fmt.Sprintf("%s %-18s %6s  %s", icon, phase.Label, lifecyclePhaseDuration(phase), plainLogText(phase.Detail))
		tuiLine(progress, row, text, style)
		row++
		if wide && phase.ID == "bootstrap" && a.spec.nodeID == record.Node.ID && a.spec.loaded && phase.State != "pending" {
			for i, step := range a.spec.snapshot.Bootstrap.Steps {
				if step.Kind != "bootstrap" || row >= progressHeight-6 {
					continue
				}
				icon, style := lifecyclePhaseStyle(string(step.State), theme)
				tuiLine(progress, row, fmt.Sprintf("  %s %d · %s · %s", icon, i+1, step.SourceName, plainLogText(step.Command)), style)
				row++
			}
		}
	}
	if len(operation.Phases) == 0 {
		tuiLine(progress, 1, "Waiting for observed phase data…", theme.dim)
	}
	if wide && !operation.CreatedAt.IsZero() {
		tuiLine(progress, progressHeight-4, fmt.Sprintf("%d/%s complete · %s elapsed", completed, countLabel(len(operation.Phases), "phase", "phases"), time.Since(operation.CreatedAt).Truncate(time.Second)), theme.amber)
		tuiLine(progress, progressHeight-3, "Durations reflect observed work.", theme.dim)
	}
	if wide {
		output := win.New(progressWidth+2, 0, width-progressWidth-2, height-8)
		drawLifecycleOutput(output, operation, theme)
		actions := border.All(win.New(progressWidth+2, height-7, width-progressWidth-2, 7), theme.border)
		tuiLine(actions, 0, "While it starts", theme.muted)
		if operation.State == "canceling" {
			tuiLine(actions, 1, "Waiting for VM shutdown", theme.amber)
			tuiLine(actions, 3, "Attachment is unavailable during cancellation.", theme.dim)
		} else {
			tuiLine(actions, 1, "x attach when ready · Esc cancels queue", theme.text)
			tuiLine(actions, 3, "s cancel start and stop the VM", theme.amber)
		}
		tuiLine(actions, 2, "T host shell · l full logs", theme.text)
		tuiLine(actions, 4, "Esc back · operation continues", theme.dim)
	} else {
		drawLifecycleOutput(win.New(0, progressHeight, width, max(1, height-progressHeight)), operation, theme)
	}
	return true
}

func drawLifecycleOutput(win vaxis.Window, record NodeOperation, theme tuiTheme) {
	_, height := win.Size()
	box := border.All(win, theme.border)
	tuiLine(box, 0, "Output · following · l full logs", theme.muted)
	lines := record.Output
	if record.OutputPartial != "" {
		lines = append(slices.Clone(lines), record.OutputPartial)
	}
	count := max(0, height-3)
	for i, line := range lines[max(0, len(lines)-count):] {
		tuiLine(box, i+1, plainLogText(line), theme.text)
	}
	if len(lines) == 0 {
		tuiLine(box, 1, "Awaiting output…", theme.dim)
	}
}

func lifecyclePhaseStyle(state string, theme tuiTheme) (string, vaxis.Style) {
	switch state {
	case "succeeded":
		return "✓", theme.mint
	case "running":
		return "◐", theme.amber
	case "failed":
		return "✕", theme.coral
	case "skipped":
		return "–", theme.dim
	case "canceled", "interrupted":
		return "!", theme.amber
	default:
		return "○", theme.dim
	}
}

func lifecyclePhaseDuration(phase NodeOperationPhase) string {
	if phase.State == "skipped" {
		return "skip"
	}
	if phase.StartedAt == nil {
		return "—"
	}
	end := time.Now()
	if phase.FinishedAt != nil {
		end = *phase.FinishedAt
	} else if phase.State != "running" {
		return "—"
	}
	return max(time.Duration(0), end.Sub(*phase.StartedAt)).Truncate(time.Second).String()
}
