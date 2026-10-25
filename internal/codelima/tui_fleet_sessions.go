package codelima

import (
	"fmt"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
)

type tuiFleetSession struct {
	label, activity string
	kind            terminal.TerminalKind
	kindKnown       bool
}

// Progress and prompt integration provide positive evidence. Absence of a
// prompt marker is not evidence of work: unsupported shells stay unknown.
func fleetSessionActivity(metadata TerminalMetadata) string {
	switch metadata.ProgressState {
	case 1, 3:
		return "working"
	case 2:
		return "error"
	case 4:
		return "paused"
	}
	if metadata.CursorAtPrompt {
		return "at prompt"
	}
	return "unknown"
}

func (a *vaxisTUIApp) fleetSessions(node Node) []tuiFleetSession {
	var records []tuiFleetSession
	for _, key := range a.state.sessions.TargetSessionKeys(tuiTreeEntry{node: node}.key()) {
		record := tuiFleetSession{label: key, activity: "unknown"}
		if a.sessions != nil {
			if session, ok := a.sessions.Session(key); ok {
				metadata := a.terminalMetadata(key)
				record.label = terminalTabLabel(session, metadata)
				record.activity = fleetSessionActivity(metadata)
				record.kind, record.kindKnown = session.shellKind, true
			}
		}
		records = append(records, record)
	}
	return records
}

func (a *vaxisTUIApp) drawFleetSessions(win vaxis.Window, node Node, theme tuiTheme) {
	records := a.fleetSessions(node)
	width, _ := win.Size()
	tuiLine(win, 0, fmt.Sprintf("Sessions · %d", len(records)), theme.muted)
	if len(records) == 0 {
		tuiLine(win, 1, "No sessions", theme.dim)
	}
	for i, record := range records {
		if i >= 3 {
			break
		}
		statusWidth := renderedTextWidth(a.vx, record.activity)
		tuiLine(win.New(0, i+1, max(0, width-statusWidth-3), 1), 0, record.label, theme.text)
		style := theme.dim
		if record.activity == "working" {
			style = theme.amber
		}
		tuiLine(win.New(max(0, width-statusWidth), i+1, statusWidth, 1), 0, record.activity, style)
	}
}

func (impact tuiMarkedImpact) sessionWarning() string {
	text := fmt.Sprintf("%d guest sessions end on stop · %d working · %d activity unknown", impact.guestSessions, impact.guestWorking, impact.guestActivityUnknown)
	if impact.hostSessions > 0 {
		text += fmt.Sprintf(" · %d host sessions", impact.hostSessions)
	}
	if impact.unclassifiedSessions > 0 {
		text += fmt.Sprintf(" · %d unclassified", impact.unclassifiedSessions)
	}
	return text
}

func (a *vaxisTUIApp) fleetSessionSummary(node Node) string {
	working, prompt, unknown := 0, 0, 0
	first := ""
	for _, session := range a.fleetSessions(node) {
		switch session.activity {
		case "working":
			working++
			if first == "" {
				first = session.label
			}
		case "at prompt":
			prompt++
		case "unknown":
			unknown++
		}
	}
	if working > 0 {
		return fmt.Sprintf(" · %d working (%s)", working, first)
	}
	if unknown > 0 {
		return fmt.Sprintf(" · %d activity unknown", unknown)
	}
	if prompt > 0 {
		return fmt.Sprintf(" · %d at prompt", prompt)
	}
	return ""
}
