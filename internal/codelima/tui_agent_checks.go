package codelima

import "go.rockorager.dev/vaxis"

func (a *vaxisTUIApp) drawFleetAgentChecks(win vaxis.Window, segments []vaxis.Segment) int {
	width, _ := win.Size()
	total := 0
	for _, segment := range segments {
		total += renderedTextWidth(a.vx, segment.Text)
	}
	if total <= width || len(segments) < 2 {
		win.PrintTruncate(0, segments...)
		return 1
	}
	// Each agent is a name/result pair, separated from the next by a spacer.
	// Wrap whole pairs, reserving room for the result even for long profiles.
	row := 0
	for i := 0; i+1 < len(segments); i += 3 {
		name, result := segments[i], segments[i+1]
		nameWidth := min(renderedTextWidth(a.vx, name.Text), max(0, width-renderedTextWidth(a.vx, result.Text)))
		win.New(0, row, nameWidth, 1).PrintTruncate(0, name)
		win.New(nameWidth, row, max(0, width-nameWidth), 1).PrintTruncate(0, result)
		row++
	}
	return row
}

func (a *vaxisTUIApp) fleetAgentCheckSegments(node Node, theme tuiTheme) ([]vaxis.Segment, string) {
	checks := configuredFleetAgentChecks(node)
	preview := a.fleetPreviewFor(node)
	available := preview != nil && preview.AgentError == ""
	if available {
		checks = preview.AgentChecks
	}
	if len(checks) == 0 {
		return []vaxis.Segment{{Text: "—", Style: theme.dim}}, "checks unavailable"
	}
	retained := a.fleetEvidenceStale() || node.Status != NodeStatusRunning
	allLogin, allPassed, inherited, profileCheck := true, true, false, false
	allPending, allUnknown := true, true
	var segments []vaxis.Segment
	for i, check := range checks {
		if i > 0 {
			segments = append(segments, vaxis.Segment{Text: "  "})
		}
		name := check.Name
		if !check.LoginUser && check.State != BootstrapStepUnrecorded {
			name += " check"
			profileCheck = true
		}
		segments = append(segments, vaxis.Segment{Text: name + " ", Style: theme.text})
		label, style := "?", theme.dim
		switch check.State {
		case BootstrapStepSucceeded:
			label, style = "✓", theme.mint
		case BootstrapStepPending:
			label = "pending"
		case BootstrapStepRunning:
			label, style = "checking", theme.amber
		case BootstrapStepFailed:
			label, style = "failed", theme.coral
		case BootstrapStepCanceled:
			label, style = "canceled", theme.amber
		case BootstrapStepInterrupted:
			label, style = "interrupted", theme.amber
		}
		if check.Inherited {
			label, style, inherited = "↳"+label, theme.amber, true
		}
		if retained {
			style = theme.dim
		}
		segments = append(segments, vaxis.Segment{Text: label, Style: style})
		allLogin = allLogin && check.LoginUser
		allPassed = allPassed && check.State == BootstrapStepSucceeded
		allPending = allPending && check.State == BootstrapStepPending
		allUnknown = allUnknown && check.State == BootstrapStepUnrecorded
	}
	note := "recorded checks · see Spec"
	switch {
	case !available:
		note = "checks unavailable"
	case allPending:
		note = "checks pending"
	case allUnknown:
		note = "checks not recorded"
	case inherited:
		note = "↳ inherited · see Spec"
	case profileCheck:
		note = "configured profile check"
	case allLogin && allPassed:
		note = "validated as login user"
	}
	if available && a.fleetEvidenceStale() {
		note = "stale · last recorded"
	} else if available && node.Status != NodeStatusRunning && !inherited && !profileCheck && !allPending && !allUnknown {
		note = "last recorded · see Spec"
	}
	return segments, note
}
