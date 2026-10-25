package codelima

import (
	"fmt"
	"slices"
	"strings"
)

type tuiPaletteNodePlan struct {
	id     string
	action tuiActionID
}

func (a *vaxisTUIApp) paletteBatchCommands() []tuiPaletteCommand {
	byDirectory := map[string][]tuiPaletteNodePlan{}
	for _, node := range a.state.nodes {
		actions := a.paletteNodeActions(node)
		action := tuiActionNodeStart
		if slices.ContainsFunc(actions, func(spec tuiActionSpec) bool { return spec.ID == tuiActionNodeRetry }) {
			action = tuiActionNodeRetry
		}
		if _, err := a.paletteNodeAction(node.ID, action); err != nil {
			continue
		}
		byDirectory[node.DirectoryPath] = append(byDirectory[node.DirectoryPath], tuiPaletteNodePlan{node.ID, action})
	}
	dirs := make([]string, 0, len(byDirectory))
	for dir := range byDirectory {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	var commands []tuiPaletteCommand
	for _, dir := range dirs {
		plan := slices.Clone(byDirectory[dir])
		names := make([]string, 0, len(plan))
		for _, target := range plan {
			node, _ := a.managerNode(target.id)
			name := node.Slug
			if target.action == tuiActionNodeRetry {
				name += " (retry)"
			}
			names = append(names, name)
		}
		title := "start all in " + dir
		commands = append(commands, tuiPaletteCommand{id: "start-directory:" + dir, group: "ACTIONS", label: title, hint: fmt.Sprintf("%s · %s", countLabel(len(plan), "node", "nodes"), strings.Join(names, ", ")), run: func() error { return a.reviewPaletteBatch(title, plan) }})
	}
	if a.view == tuiViewNodes {
		targets := a.fleet.targets()
		if a.nodeDetail {
			if entry := a.state.selectedEntry(); entry.valid() {
				targets = []tuiFleetRecord{{Node: entry.node}}
			} else {
				targets = nil
			}
		}
		var plan []tuiPaletteNodePlan
		ids := make([]string, 0, len(targets))
		for _, record := range targets {
			plan = append(plan, tuiPaletteNodePlan{record.Node.ID, tuiActionNodeRestart})
			ids = append(ids, record.Node.ID)
		}
		if len(plan) > 0 {
			slices.Sort(ids)
			label := "Restart selected/marked " + pluralForm(len(plan), "node", "nodes")
			shortcut := a.keyHint("node.restart")
			if a.nodeDetail {
				label = "Restart this node"
			}
			if a.state.selectedEntry().node.Status == NodeStatusFailed && (a.nodeDetail || len(a.fleet.marks) == 0) {
				shortcut = "" // In this context r resumes bootstrap instead.
			}
			commands = append(commands, tuiPaletteCommand{id: "restart-selection:" + strings.Join(ids, ","), group: "ACTIONS", label: label, hint: fmt.Sprintf("Review %s · ends guest sessions", countLabel(len(plan), "target", "targets")), shortcut: shortcut, run: func() error { return a.reviewPaletteBatch(label, plan) }})
		}
	}
	return commands
}

// A directory or marked-set command always opens a review, even for one node.
// The review captures membership and action kinds; additions cannot enlarge it.
func (a *vaxisTUIApp) reviewPaletteBatch(title string, plan []tuiPaletteNodePlan) error {
	return a.openBatchReview(title, plan, false)
}
