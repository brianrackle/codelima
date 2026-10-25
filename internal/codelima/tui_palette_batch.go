package codelima

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
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
		commands = append(commands, tuiPaletteCommand{id: "start-directory:" + dir, group: "ACTIONS", label: title, hint: fmt.Sprintf("%d nodes · %s", len(plan), strings.Join(names, ", ")), run: func() error { return a.reviewPaletteBatch(title, plan) }})
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
			label := "Restart selected/marked nodes"
			shortcut := "r"
			if a.nodeDetail {
				label = "Restart this node"
			}
			if a.state.selectedEntry().node.Status == NodeStatusFailed && (a.nodeDetail || len(a.fleet.marks) == 0) {
				shortcut = "" // In this context r resumes bootstrap instead.
			}
			commands = append(commands, tuiPaletteCommand{id: "restart-selection:" + strings.Join(ids, ","), group: "ACTIONS", label: label, hint: fmt.Sprintf("Review %d targets · ends guest sessions", len(plan)), shortcut: shortcut, run: func() error { return a.reviewPaletteBatch(label, plan) }})
		}
	}
	return commands
}

// A directory or marked-set command always opens a review, even for one node.
// The review captures membership and action kinds; additions cannot enlarge it.
func (a *vaxisTUIApp) reviewPaletteBatch(title string, plan []tuiPaletteNodePlan) error {
	plan = slices.Clone(plan)
	keys := make([]string, 0, len(plan))
	lines := make([]string, 0, len(plan)+2)
	for _, target := range plan {
		entry, err := a.paletteNodeAction(target.id, target.action)
		if err != nil {
			return err
		}
		keys = append(keys, terminal.NodeTarget(target.id).String())
		lines = append(lines, strings.TrimPrefix(string(target.action), "node.")+" "+entry.node.Slug+" · "+a.fleetStatus(entry.node))
	}
	lines = append(lines, "Only these captured nodes will run. Existing marks are preserved.", "Starts run sequentially; failed nodes resume their failed bootstrap step.")
	if slices.ContainsFunc(plan, func(p tuiPaletteNodePlan) bool { return p.action == tuiActionNodeRestart }) {
		lines[len(lines)-1] = "Restart ends guest sessions. Completed bootstrap is kept; failed setup reruns."
	}
	a.overlay = newTUIDialog(title, "Run", lines, nil, func(map[string]string) error {
		// Validate the whole review before starting its first operation.
		for _, target := range plan {
			if _, err := a.paletteNodeAction(target.id, target.action); err != nil {
				return err
			}
		}
		return a.startOperation(tuiOperationRequest{Title: title, ResourceKeys: keys, EntryKeys: keys, Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
			for _, target := range plan {
				if _, err := runTUINodeAction(ctx, service, target.action, target.id); err != nil {
					return tuiOperationResult{}, fmt.Errorf("%s %s: %w", target.action, target.id, err)
				}
			}
			return tuiOperationResult{ReloadData: true, Status: fmt.Sprintf("Completed %d node actions", len(plan))}, nil
		}})
	})
	return nil
}
