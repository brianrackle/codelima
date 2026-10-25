package codelima

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"go.rockorager.dev/vaxis"
)

type tuiEnvironmentRun struct {
	node  Node
	step  BootstrapStep
	index int
	at    time.Time
}
type tuiRecipeCatalog struct {
	nodes          []Node
	specs          map[string]nodeSpecSnapshot
	runs           map[string]tuiEnvironmentRun
	incompleteRuns map[string]bool
}

func (s *Service) readRecipeCatalog(ctx context.Context, environments []EnvironmentConfig) (tuiRecipeCatalog, error) {
	d := tuiRecipeCatalog{specs: map[string]nodeSpecSnapshot{}, runs: map[string]tuiEnvironmentRun{}, incompleteRuns: map[string]bool{}}
	// Include deleted nodes for historical execution evidence, but never count
	// them as current users of a template. Do not consult the scoped fleet.
	nodes, err := s.store.ListNodes(true)
	if err != nil {
		return d, err
	}
	for _, n := range nodes {
		if err := ctx.Err(); err != nil {
			return d, err
		}
		var b BootstrapState
		var readErr error
		if n.DeletedAt == nil {
			d.nodes = append(d.nodes, n)
			spec, err := s.nodeRecipeSpec(ctx, n)
			if err != nil {
				return d, err
			}
			d.specs[n.ID] = spec
			b, readErr = spec.Bootstrap, spec.BootstrapError
		} else {
			b, readErr = s.store.LoadBootstrapState(n.ID)
		}
		if readErr != nil {
			for _, env := range environments {
				if slices.Contains(n.Environments, env.Slug) {
					d.incompleteRuns[env.ID] = true
				}
			}
			continue
		}
		for i, step := range b.Steps {
			if step.SourceKind != "environment" || step.StartedAt == nil || step.ExecutionNodeID != n.ID {
				continue
			}
			id := step.SourceID
			if id == "" {
				for _, env := range environments {
					if env.Slug == step.SourceName {
						id = env.ID
						break
					}
				}
			}
			at := *step.StartedAt
			if step.FinishedAt != nil {
				at = *step.FinishedAt
			}
			previous, ok := d.runs[id]
			if !ok || at.After(previous.at) || at.Equal(previous.at) && i > previous.index {
				d.runs[id] = tuiEnvironmentRun{n, step, i, at}
			}
		}
	}
	return d, nil
}

type tuiRecipeControl struct {
	rect   tuiRect
	action string
	index  int
}
type tuiRecipesState struct {
	configurationID, environmentID string
	details                        bool
	listScroll                     [2]int
	detailScroll                   int
	command                        int
	followCommand                  bool
	kindRects                      [2]tuiRect
	listRect, detailRect           tuiRect
	controls                       []tuiRecipeControl
}

func (a *vaxisTUIApp) rememberRecipeSelection() {
	if a.templateIndex >= 0 && a.templateIndex < len(a.managerData.configurations) {
		a.recipes.configurationID = a.managerData.configurations[a.templateIndex].ID
	}
	if a.environmentIndex >= 0 && a.environmentIndex < len(a.managerData.environments) {
		a.recipes.environmentID = a.managerData.environments[a.environmentIndex].ID
	}
}
func (a *vaxisTUIApp) restoreRecipeSelection() {
	if i := slices.IndexFunc(a.managerData.configurations, func(c Configuration) bool { return c.ID == a.recipes.configurationID }); i >= 0 {
		a.templateIndex = i
	}
	if i := slices.IndexFunc(a.managerData.environments, func(e EnvironmentConfig) bool { return e.ID == a.recipes.environmentID }); i >= 0 {
		a.environmentIndex = i
	}
	a.templateIndex = max(0, min(a.templateIndex, len(a.managerData.configurations)-1))
	a.environmentIndex = max(0, min(a.environmentIndex, len(a.managerData.environments)-1))
	a.rememberRecipeSelection()
}
func (a *vaxisTUIApp) templateNodes(id string) []Node {
	var nodes []Node
	for _, n := range a.managerData.recipes.nodes {
		if n.ConfigurationID == id {
			nodes = append(nodes, n)
		}
	}
	slices.SortFunc(nodes, func(a, b Node) int { return strings.Compare(a.Slug, b.Slug) })
	return nodes
}
func (a *vaxisTUIApp) environmentTemplates(slug string) []Configuration {
	var templates []Configuration
	for _, c := range a.managerData.configurations {
		if slices.Contains(c.Environments, slug) {
			templates = append(templates, c)
		}
	}
	return templates
}
func builtInEnvironment(slug string) (EnvironmentConfig, bool) {
	for _, e := range builtInEnvironmentConfigs() {
		if e.Slug == slug {
			return EnvironmentConfig{Slug: e.Slug, BootstrapCommands: slices.Clone(e.BootstrapCommands)}, true
		}
	}
	return EnvironmentConfig{}, false
}
func environmentKind(e EnvironmentConfig) string {
	if builtin, ok := builtInEnvironment(e.Slug); ok {
		if !slices.Equal(e.BootstrapCommands, builtin.BootstrapCommands) {
			return "built-in •"
		}
		return "built-in"
	}
	return "custom"
}

func (a *vaxisTUIApp) handleRecipeKey(key vaxis.Key) bool {
	if a.view != tuiViewTemplates {
		return false
	}
	s := &a.recipes
	if a.environmentsView && (key.Matches(vaxis.KeyUp, vaxis.ModShift) || key.Matches(vaxis.KeyDown, vaxis.ModShift)) {
		if s.details && tuiKeyActivates(key, true) && a.environmentIndex < len(a.managerData.environments) {
			env := a.managerData.environments[a.environmentIndex]
			delta := 1
			if key.Keycode == vaxis.KeyUp {
				delta = -1
			}
			if s.command+delta >= 0 && s.command+delta < len(env.BootstrapCommands) {
				e := a.openEnvironmentEditor(env, false)
				e.setCommands(moveCommand(env.BootstrapCommands, s.command, delta))
				s.command += delta
			}
		}
		return true
	}
	if key.Matches(vaxis.KeyRight) || key.Matches(vaxis.KeyLeft) || key.Matches(vaxis.KeyF02) {
		if !tuiKeyActivates(key, true) {
			return true
		}
		if key.Keycode == vaxis.KeyF02 {
			s.details = !s.details
		} else {
			s.details = key.Keycode != vaxis.KeyLeft
		}
		return true
	}
	if key.Matches(vaxis.KeyEsc) && s.details {
		if tuiKeyActivates(key, true) {
			s.details = false
		}
		return true
	}
	if key.Matches(vaxis.KeyPgDown) || key.Matches(vaxis.KeyPgUp) {
		if !tuiKeyActivates(key, false) {
			return true
		}
		delta := 10
		if key.Keycode == vaxis.KeyPgUp {
			delta = -10
		}
		if s.details {
			s.followCommand = false
			s.detailScroll = max(0, s.detailScroll+delta)
		} else {
			a.moveRecipeSelection(delta)
		}
		return true
	}
	return false
}
func (a *vaxisTUIApp) moveRecipeSelection(delta int) {
	if a.recipes.details {
		if a.environmentsView && a.environmentIndex < len(a.managerData.environments) {
			a.recipes.command = max(0, min(a.recipes.command+delta, len(a.managerData.environments[a.environmentIndex].BootstrapCommands)-1))
			a.recipes.followCommand = true
			return
		}
		a.recipes.detailScroll = max(0, a.recipes.detailScroll+delta)
		return
	}
	if a.environmentsView {
		a.environmentIndex = max(0, min(len(a.managerData.environments)-1, a.environmentIndex+delta))
	} else {
		a.templateIndex = max(0, min(len(a.managerData.configurations)-1, a.templateIndex+delta))
	}
	a.recipes.detailScroll, a.recipes.command = 0, 0
	a.rememberRecipeSelection()
}
func (a *vaxisTUIApp) switchRecipeKind(environments bool) {
	a.environmentsView = environments
	a.recipes.details = false
	a.recipes.detailScroll, a.recipes.command = 0, 0
}
func (a *vaxisTUIApp) handleRecipeMouse(m vaxis.Mouse) bool {
	if a.view != tuiViewTemplates {
		return false
	}
	if m.EventType != vaxis.EventPress {
		return true
	}
	if m.Button == vaxis.MouseLeftButton {
		for i, r := range a.recipes.kindRects {
			if r.contains(m.Col, m.Row) {
				a.switchRecipeKind(i == 1)
				return true
			}
		}
		for _, c := range a.recipes.controls {
			if c.rect.contains(m.Col, m.Row) {
				switch c.action {
				case "select":
					if a.environmentsView {
						a.environmentIndex = c.index
					} else {
						a.templateIndex = c.index
					}
					a.recipes.details = false
					a.recipes.detailScroll, a.recipes.command = 0, 0
					a.rememberRecipeSelection()
				case "details":
					a.recipes.details = !a.recipes.details
				case "command":
					a.recipes.command = c.index
					a.recipes.details = true
				default:
					if err := a.handleTemplateAction(vaxis.Key{Keycode: []rune(c.action)[0], Text: c.action}); err != nil {
						a.setStatus(slog.LevelError, err.Error())
					}
				}
				return true
			}
		}
		if a.recipes.detailRect.contains(m.Col, m.Row) {
			a.recipes.followCommand = false
			a.recipes.details = true
		}
	}
	if m.Button == vaxis.MouseWheelUp || m.Button == vaxis.MouseWheelDown {
		delta := 1
		if m.Button == vaxis.MouseWheelUp {
			delta = -1
		}
		if a.recipes.detailRect.contains(m.Col, m.Row) {
			a.recipes.detailScroll = max(0, a.recipes.detailScroll+delta)
		} else {
			a.recipes.details = false
			a.moveRecipeSelection(delta)
		}
	}
	return true
}
func (a *vaxisTUIApp) recipeFooter() string {
	if a.environmentsView {
		if a.recipes.details {
			return "↑↓ command · Enter edit · Shift+↑↓ order · o add · D remove · E editor · ← list"
		}
		return "↑↓ select · → details · e edit · n new · c clone · d delete · Tab configs"
	}
	return "↑↓ select · → details · e edit · n new · c clone · d delete · N new node · Tab envs"
}

func environmentRunText(run tuiEnvironmentRun, incomplete bool) string {
	if run.node.ID == "" {
		if incomplete {
			return "unavailable · history could not be read"
		}
		return "no recorded execution"
	}
	text := fmt.Sprintf("%s · %s · node step %d · %s", run.step.State, run.node.Slug, run.index+1, run.at.Local().Format("Jan 2 15:04"))
	if run.step.ExitCode != nil {
		text += fmt.Sprintf(" · exit %d", *run.step.ExitCode)
	}
	if incomplete {
		text += " · history incomplete"
	}
	return text
}
