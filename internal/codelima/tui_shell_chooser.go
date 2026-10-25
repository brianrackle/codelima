package codelima

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
)

type tuiShellChoice struct {
	id, label, hint, unavailable string
	mode, terminalID, kind       string
	key                          rune
	host, fresh, saveDefault     bool
	warning                      bool
}

type tuiShellChooser struct {
	app                   *vaxisTUIApp
	nodeID, name, err     string
	choices               []tuiShellChoice
	sessionOrder          []string
	sessionChoices        map[string]tuiShellChoice
	index, scroll, height int
	rows                  []tuiCommandTarget
	closeRect             tuiRect
	window, tmux          string
	launchers             map[string]string
	run                   func(tuiShellChoice, bool) error
}

func (a *vaxisTUIApp) openNodeShellChooser(id string) error {
	node, err := a.managerNode(id)
	if err != nil {
		return err
	}
	h := &tuiShellChooser{app: a, nodeID: id, name: node.Slug, sessionChoices: map[string]tuiShellChoice{}}
	h.run = func(choice tuiShellChoice, save bool) error { return a.runShellChoice(id, choice, save) }
	h.refresh()
	a.overlay = h
	return nil
}

func (h *tuiShellChooser) refresh() {
	a, cfg := h.app, h.app.managerSettings()
	node, nodeErr := a.managerNode(h.nodeID)
	if nodeErr == nil {
		h.name = node.Slug
	}
	// Local launcher probes are cached for this configuration/environment.
	// Execution repeats the shared preflight before creating any session.
	tmux := os.Getenv("TMUX")
	if h.launchers == nil || h.window != cfg.Shell.Window || h.tmux != tmux {
		h.window, h.tmux = cfg.Shell.Window, tmux
		h.launchers = map[string]string{}
		for _, mode := range []string{"window", "tmux"} {
			if err := checkShellLaunch(cfg, mode); err != nil {
				h.launchers[mode] = err.Error()
			}
		}
	}
	h.choices = []tuiShellChoice{
		{id: "attach", key: vaxis.KeyEnter, label: "Attach here", hint: "suspends TUI · Ctrl+] returns · session keeps running", mode: "attach", fresh: true, saveDefault: true},
		{id: "window", key: 'w', label: "New window", hint: cfg.Shell.Window, mode: "window", fresh: true, saveDefault: true},
		{id: "tmux", key: 't', label: "tmux window", hint: "$TMUX detected · new window in this server", mode: "tmux", fresh: true, saveDefault: true},
		{id: "host", key: 'h', label: "Host shell here", hint: node.DirectoryPath + " · on this host", mode: "attach", host: true, fresh: true},
		{id: "embedded", key: 'e', label: "Embedded tab", hint: "F6 returns to manager · other keys reach the shell", mode: "embedded", fresh: true},
	}
	for i := range h.choices {
		choice := &h.choices[i]
		if reason := h.launchers[choice.mode]; reason != "" {
			choice.unavailable = reason
			choice.hint = "Launcher unavailable · Settings › shell.window"
			if choice.mode == "tmux" {
				choice.hint = "tmux unavailable · executable and $TMUX required"
			}
		}
		if choice.mode == "embedded" && cfg.Shell.Mode != "embedded" {
			choice.unavailable = "Embedded is off; enable Settings › shell.mode"
			choice.hint = "off · enable in Settings › shell.mode"
		}
	}
	current := map[string]tuiShellChoice{}
	if nodeErr == nil && a.sessions != nil {
		for _, record := range a.fleetSessions(node) {
			session, ok := a.sessions.Session(record.key)
			if !ok || session.terminalID == "" {
				continue
			}
			id := string(session.terminalID)
			if !slices.Contains(h.sessionOrder, id) {
				h.sessionOrder = append(h.sessionOrder, id)
			}
			kind := "shell"
			if record.kind == terminal.NodeHostShell {
				kind = "host"
			}
			activity := record.activity
			if record.idleSince > 0 {
				activity = fleetIdleLabel(record.idleSince, time.Now())
			}
			metadata := a.terminalMetadata(record.key)
			bell := metadata.BellCount > session.acknowledgedBells
			if bell {
				activity = "! bell · " + activity
			}
			// Activity and unacknowledged bells have their own column. Tab badges
			// include historical bells and would duplicate or overstate that evidence.
			label := terminalTabLabel(session, TerminalMetadata{Title: metadata.Title})
			c := tuiShellChoice{id: "session:" + id, terminalID: id, mode: "attach", kind: kind, label: label,
				host: record.kind == terminal.NodeHostShell, hint: activity + " · age " + overviewAge(record.createdAt, time.Now()), warning: bell || record.activity == "working"}
			if c.host {
				c.label = strings.TrimPrefix(c.label, "host:")
			}
			current[id], h.sessionChoices[id] = c, c
		}
	}
	// Ordinals belong to the opening chooser, not the latest row order. Keep
	// ended rows and append arrivals so a delayed digit never attaches another ID.
	for i, id := range h.sessionOrder {
		c, ok := current[id]
		if !ok {
			c = h.sessionChoices[id]
			c.unavailable, c.hint = "That session has ended; reopen the chooser to refresh", "ended · reopen to refresh"
		}
		if i < 9 {
			c.key = rune('1' + i)
		}
		h.choices = append(h.choices, c)
	}
	for i := range h.choices {
		c := &h.choices[i]
		switch {
		case nodeErr != nil:
			c.unavailable = nodeErr.Error()
		case a.daemonDisconnected:
			c.unavailable = "Daemon unavailable; reconnect before opening shells"
		case a.rawShellPending:
			c.unavailable = "A shell is already opening"
		case c.unavailable == "" && !c.host && (!nodeGuestShellReady(node) || a.fleetStatus(node) != "running"):
			c.unavailable = "Guest shell unavailable while " + a.fleetStatus(node)
		}
		if c.unavailable != "" && (nodeErr != nil || a.daemonDisconnected || a.rawShellPending || strings.HasPrefix(c.unavailable, "Guest shell")) {
			c.hint = c.unavailable
		}
		c.label, c.hint = plainLogText(c.label), plainLogText(c.hint)
	}
	h.index = min(max(0, h.index), len(h.choices)-1)
}

func (h *tuiShellChooser) choose(id string, save bool) (bool, error) {
	h.refresh()
	i := slices.IndexFunc(h.choices, func(c tuiShellChoice) bool { return c.id == id })
	if i < 0 {
		h.err = "This choice is no longer available"
		return false, nil
	}
	h.index = i
	c := h.choices[i]
	if c.unavailable != "" {
		h.err = c.unavailable
		return false, nil
	}
	if err := h.run(c, save && c.saveDefault); err != nil {
		h.err = err.Error()
		return false, nil
	}
	return true, nil
}

func (h *tuiShellChooser) Update(event vaxis.Event) (bool, error) {
	h.refresh()
	if mouse, ok := event.(vaxis.Mouse); ok {
		if mouse.EventType != vaxis.EventPress {
			return false, nil
		}
		switch mouse.Button {
		case vaxis.MouseWheelUp:
			h.index = max(0, h.index-1)
		case vaxis.MouseWheelDown:
			h.index = min(len(h.choices)-1, h.index+1)
		case vaxis.MouseLeftButton:
			if h.closeRect.contains(mouse.Col, mouse.Row) {
				return true, nil
			}
			for _, row := range h.rows {
				if row.rect.contains(mouse.Col, mouse.Row) {
					return h.choose(row.id, mouse.Modifiers&vaxis.ModShift != 0)
				}
			}
		}
		return false, nil
	}
	key, ok := event.(vaxis.Key)
	if !ok || !tuiKeyActivates(key, false) {
		return false, nil
	}
	switch {
	case key.Matches(vaxis.KeyUp):
		h.index = max(0, h.index-1)
	case key.Matches(vaxis.KeyDown):
		h.index = min(len(h.choices)-1, h.index+1)
	case key.Matches(vaxis.KeyPgUp):
		h.index = max(0, h.index-max(1, h.height/2))
	case key.Matches(vaxis.KeyPgDown):
		h.index = min(len(h.choices)-1, h.index+max(1, h.height/2))
	case key.Matches(vaxis.KeyHome):
		h.index = 0
	case key.Matches(vaxis.KeyEnd):
		h.index = len(h.choices) - 1
	default:
		if !tuiKeyActivates(key, true) {
			return false, nil
		}
		if isOverlayCancelKey(key) {
			return true, nil
		}
		shift := key.Modifiers&vaxis.ModShift != 0 || unicode.IsUpper(key.Keycode)
		if key.Matches(vaxis.KeyEnter) || key.Matches(vaxis.KeyEnter, vaxis.ModShift) {
			return h.choose(h.choices[h.index].id, shift)
		}
		if key.Modifiers & ^vaxis.ModShift != 0 {
			return false, nil
		}
		for _, c := range h.choices {
			if c.key != 0 && (unicode.ToLower(key.Keycode) == c.key || strings.EqualFold(key.Text, string(c.key))) {
				return h.choose(c.id, shift)
			}
		}
		if unicode.ToLower(key.Keycode) == 'a' || unicode.ToLower(key.Keycode) == 'n' {
			return h.choose("attach", shift)
		}
	}
	return false, nil
}

func (a *vaxisTUIApp) runShellChoice(nodeID string, c tuiShellChoice, save bool) error {
	if _, err := a.managerNode(nodeID); err != nil {
		return err
	}
	if save {
		if err := checkShellLaunch(a.managerSettings(), c.mode); err != nil {
			return err
		}
		return a.saveManagerSetting("shell.mode", c.mode, func(app *vaxisTUIApp) error {
			return app.startShellModeForNode(nodeID, c.mode, c.host, c.fresh, c.terminalID)
		})
	}
	return a.startShellModeForNode(nodeID, c.mode, c.host, c.fresh, c.terminalID)
}

func (h *tuiShellChooser) FooterHint() string {
	return "↑↓ choose · Enter open · w window · t tmux · h host · 1–9 existing · Esc close"
}

func (h *tuiShellChooser) defaultHint() string {
	mode := h.app.managerSettings().Shell.Mode
	for _, c := range h.choices[:5] {
		if c.mode == mode && !c.host {
			return "x does " + strings.ToLower(c.label)
		}
	}
	return fmt.Sprintf("x does %s", mode)
}
