package codelima

import (
	"fmt"
	"slices"
	"strings"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

// Catalogue construction reads frontend snapshots only. Callbacks revalidate
// their captured identities, then use the existing navigation/action owners.
func (a *vaxisTUIApp) paletteCommands() []tuiPaletteCommand {
	commands := []tuiPaletteCommand{}
	for i, label := range []string{"Go to nodes", "Go to templates", "Go to ports", "Go to activity", "Go to system"} {
		view := tuiView(i)
		commands = append(commands, tuiPaletteCommand{id: fmt.Sprintf("view:%d", i), group: "GO TO", label: label, shortcut: a.keyHint([]string{"view.nodes", "view.recipes", "view.ports", "view.activity", "view.system"}[i]), run: func() error { a.switchView(view); return nil }})
	}
	commands = append(commands,
		tuiPaletteCommand{id: "settings", group: "ACTIONS", label: "Edit settings", hint: "System › Settings · shell, colors, confirmations", shortcut: a.keyHint("system.open"), run: func() error { a.switchView(tuiViewSystem); a.systemPanel = tuiSystemSettings; return nil }},
		tuiPaletteCommand{id: "create", group: "ACTIONS", label: "Create node", hint: "Choose a name, directory and template", shortcut: a.keyHint("node.create"), run: a.openCreateNodeDialog},
		tuiPaletteCommand{id: "help", group: "ACTIONS", label: "Show keybindings", hint: "Search keys for every view", shortcut: a.keyHint("help.open"), run: func() error { a.openKeyHelp(); return nil }},
		tuiPaletteCommand{id: "filter:failed", group: "ACTIONS", label: "status: show only failed", hint: "applies filter status:failed", shortcut: a.keyHint("fleet.filter"), run: func() error {
			a.switchView(tuiViewNodes)
			a.fleet.setFilter("status:failed")
			a.selectFleetNode()
			return nil
		}},
	)
	for _, action := range [][4]string{{"update", "Update daemon, keep sessions", "Keep live shells", "system.update"}, {"restart", "Restart daemon", "Ends live shell processes", "system.restart"}, {"log", "Open daemon log", "Latest daemon evidence", "system.logs"}, {"diagnose", "Diagnose terminal freeze", "Save local evidence", "system.diagnose"}} {
		commands = append(commands, tuiPaletteCommand{id: "daemon:" + action[0], group: "ACTIONS", label: action[1], hint: "System · " + action[2], shortcut: a.keyHint(action[3]), run: func() error { a.switchView(tuiViewSystem); a.runSystemDaemonAction(action[0]); return nil }})
	}
	daemonHint := "Status not sampled yet"
	if !a.systemDaemon.checkedAt.IsZero() {
		s := a.systemDaemon.overview.Status
		daemonHint = fmt.Sprintf("pid %d · %s · cached", s.PID, countLabel(s.TerminalCount, "session", "sessions"))
		if a.systemDaemon.err != nil || a.daemonDisconnected {
			daemonHint = "Retained status · connection unavailable"
		}
	}
	commands = append(commands, tuiPaletteCommand{id: "system:daemon", group: "GO TO", label: "System › daemon status", hint: daemonHint, shortcut: a.keyHint("view.system"), run: func() error { a.switchView(tuiViewSystem); a.systemPanel = tuiSystemDaemon; return nil }})
	for _, node := range a.state.nodes {
		for _, action := range a.paletteNodeActions(node) {
			if !strings.HasPrefix(string(action.ID), "node.") || action.ID == tuiActionNodeCreate {
				continue
			}
			verb := strings.TrimPrefix(string(action.ID), "node.")
			hint, warning := a.paletteNodeHint(node, action.ID)
			commands = append(commands, tuiPaletteCommand{id: paletteNodeCommandID(node.ID, action.ID), group: "NODES", label: verb + " " + node.Slug, hint: hint, warning: warning, shortcut: a.paletteNodeKeyHint(action.ID), run: func() error { return a.runPaletteNodeAction(node.ID, action.ID) }})
		}
		commands = append(commands, tuiPaletteCommand{id: "node:" + node.ID, group: "GO TO", label: "Node: " + node.Slug, hint: node.DirectoryPath, shortcut: a.keyHint("node.open"), run: func() error { return a.openManagerNode(node.ID, tuiDetailOverview) }})
		commands = append(commands, tuiPaletteCommand{id: "logs:" + node.ID, group: "GO TO", label: "Logs › " + node.Slug + " › bootstrap", hint: a.fleetStatus(node) + " · " + node.DirectoryPath, shortcut: a.keyHints("{node.logs} {logs.source_previous logs.source_next}"), run: func() error {
			if err := a.openManagerNode(node.ID, tuiDetailLogs); err != nil {
				return err
			}
			a.changeLogSource(slices.Index(nodeLogSources[:], nodeLogBootstrap))
			return nil
		}})
	}
	if entry := a.state.selectedEntry(); entry.valid() {
		id := entry.node.ID
		commands = append(commands, tuiPaletteCommand{id: "chooser:" + id, group: "ACTIONS", label: "Open shell chooser", hint: entry.node.Slug + " · " + entry.node.DirectoryPath, shortcut: a.keyHint("node.shell_chooser"), run: func() error { return a.openPaletteShellChooser(id) }})
		if a.view == tuiViewNodes && !a.nodeDetail {
			commands = append(commands, tuiPaletteCommand{id: "first-port:" + id, group: "ACTIONS", label: "Open first live HTTP port", hint: entry.node.Slug, shortcut: a.keyHint("node.open_port"), run: func() error {
				node, err := a.managerNode(id)
				if err != nil {
					return err
				}
				return a.openFleetNodePort(tuiFleetRecord{Node: node, Status: a.fleetStatus(node)})
			}})
		}
	}
	commands = append(commands, a.paletteBatchCommands()...)
	if a.view == tuiViewPorts {
		for _, action := range [][2]string{{"route.open", "Open selected port in browser"}, {"route.copy", "Copy selected port address"}, {"ports.pin", "Pin selected port's bare localhost owner"}, {"ports.filter", "Filter ports"}, {"ports.refresh", "Refresh ports"}} {
			id, hint := "", "Ports"
			if action[0] != "ports.filter" && action[0] != "ports.refresh" {
				row, ok := a.portSelection()
				if !ok {
					continue
				}
				id, hint = row.ID, row.Address
			}
			commands = append(commands, tuiPaletteCommand{id: "ports:" + action[0] + ":" + id, group: "ACTIONS", label: action[1], hint: hint, shortcut: a.keyHint(action[0]), run: func() error {
				if a.view != tuiViewPorts {
					return fmt.Errorf("Ports context is no longer available")
				}
				if id != "" {
					row, ok := a.portSelection()
					if !ok || row.ID != id {
						return fmt.Errorf("selected route is no longer available")
					}
				}
				a.handlePortsAction(action[0])
				return nil
			}})
		}
	}
	return commands
}

func paletteNodeCommandID(id string, action tuiActionID) string { return string(action) + ":" + id }

func (a *vaxisTUIApp) paletteNodeAction(id string, action tuiActionID) (tuiTreeEntry, error) {
	node, err := a.managerNode(id)
	if err != nil {
		return tuiTreeEntry{}, err
	}
	entry := tuiTreeEntry{node: node}
	actions := a.paletteNodeActions(node)
	restart := action == tuiActionNodeRestart && !a.fleetDeleteRetryable(node)
	if len(actions) == 0 || !restart && !slices.ContainsFunc(actions, func(spec tuiActionSpec) bool { return spec.ID == action }) {
		return tuiTreeEntry{}, fmt.Errorf("%s is no longer available for %s (%s)", strings.TrimPrefix(string(action), "node."), node.Slug, a.fleetStatus(node))
	}
	return entry, a.ensureActionNotConflicting(tuiActionSpec{ID: action}, entry)
}

func (a *vaxisTUIApp) paletteNodeActions(node Node) []tuiActionSpec {
	if a.fleetDeleteRetryable(node) {
		return []tuiActionSpec{{ID: tuiActionNodeDelete, Label: "Review incomplete deletion", Hotkey: 'd'}}
	}
	// Receipt-backed failure can outlive provisioning metadata after an
	// interrupted start. Mirror the existing failed view's Retry meaning.
	if a.fleetReceiptFailed(node) {
		node.Status = NodeStatusFailed
	} else if nodeStatusInFlight(node.Status) || a.operationDisplayStatus(tuiTreeEntry{node: node}) != "" {
		return nil
	}
	return availableTUIActions(tuiTreeEntry{node: node})
}

func (a *vaxisTUIApp) runPaletteNodeAction(id string, action tuiActionID) error {
	entry, err := a.paletteNodeAction(id, action)
	if err != nil {
		return err
	}
	if err := a.performEntryAction(tuiActionSpec{ID: action}, entry); err != nil {
		return err
	}
	if dialog := a.activeDialog(); dialog != nil && dialog.OnSubmit != nil {
		submit := dialog.OnSubmit
		dialog.OnSubmit = func(values map[string]string) error {
			if _, err := a.paletteNodeAction(id, action); err != nil {
				return err
			}
			return submit(values)
		}
	}
	return nil
}

func (a *vaxisTUIApp) openPaletteShellChooser(id string) error {
	return a.openNodeShellChooser(id)
}

func (a *vaxisTUIApp) paletteNodeHint(node Node, action tuiActionID) (string, bool) {
	name := node.ConfigurationSlug
	if name == "" {
		name = "template unknown"
	}
	hint := a.fleetStatus(node) + " · " + name
	if action == tuiActionNodeStop || action == tuiActionNodeRestart {
		guest, unknown := 0, 0
		for _, session := range a.fleetSessions(node) {
			if !session.kindKnown {
				unknown++
			} else if session.kind == terminal.NodeShell {
				guest++
			}
		}
		if a.daemonDisconnected {
			hint += " · session impact unavailable"
		} else {
			hint += fmt.Sprintf(" · %s", countLabel(guest, "known guest session ends", "known guest sessions end"))
			if unknown > 0 {
				hint += fmt.Sprintf(" · %d unclassified", unknown)
			}
		}
	}
	if action == tuiActionNodeRetry {
		hint += " · resume failed bootstrap step"
	}
	if action == tuiActionNodeStart && a.host.AllocationsKnown && a.host.UnknownNodes == 0 && a.host.Capacity.MemoryBytes != nil {
		total, used := *a.host.Capacity.MemoryBytes, a.host.AllocatedMemoryBytes
		remaining := total - min(total, used)
		if uint64(node.MemoryMiB)<<20 > remaining {
			return hint + fmt.Sprintf(" · ⚠ needs %s; %s unallocated (cached)", createMiB(node.MemoryMiB), tuiHostBytes(&remaining)), true
		}
	}
	return hint, false
}

func (a *vaxisTUIApp) paletteNodeKeyHint(action tuiActionID) string {
	switch action {
	case tuiActionNodeStart, tuiActionNodeStop:
		return a.keyHint("node.toggle")
	case tuiActionNodeRetry, tuiActionNodeRestart:
		return a.keyHint("node.restart")
	default:
		return a.keyHint(string(action))
	}
}
