package codelima

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"go.rockorager.dev/vaxis"
)

func TestTUIRecipeCatalogUsesGlobalNodesAndRecordedExecution(t *testing.T) {
	a, _ := testCreateForm(t)
	s, ctx := a.service, context.Background()
	env, err := s.EnvironmentConfigCreate(ctx, EnvironmentConfigCreateInput{Slug: "recipe-env", BootstrapCommands: []string{"echo before"}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.ConfigurationCreate(ctx, ConfigurationCreateInput{Slug: "recipe-template", Environments: []string{env.Slug}})
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.NodeCreate(ctx, NodeCreateInput{Slug: "outside-scope", Configuration: c.ID, Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.LoadBootstrapState(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	now, exit := time.Now(), 127
	b.Steps[0].State, b.Steps[0].StartedAt, b.Steps[0].FinishedAt = BootstrapStepFailed, &now, &now
	b.Steps[0].ExecutionNodeID, b.Steps[0].ExitCode = n.ID, &exit
	if err := s.store.SaveNode(n, b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnvironmentConfigUpdate(ctx, env.ID, EnvironmentConfigUpdateInput{BootstrapCommands: []string{"echo after"}}); err != nil {
		t.Fatal(err)
	}
	a.view, a.treeWorkspaceRoot = tuiViewTemplates, "/unrelated-scope"
	a.loadManagerData(false)
	data := a.managerData.recipes
	if len(data.nodes) != 1 || len(data.specs[n.ID].Differences) == 0 {
		t.Fatal("catalog lost global usage or frozen drift", data)
	}
	run := data.runs[env.ID]
	if run.node.ID != n.ID || run.step.ExitCode == nil || *run.step.ExitCode != 127 {
		t.Fatal("last run is not execution evidence", run)
	}
	if len(a.templateNodes(c.ID)) != 1 || len(a.environmentTemplates(env.Slug)) != 1 {
		t.Fatal("global usage omitted a reference")
	}
	if _, err := s.ConfigurationDelete(ctx, c.ID); err == nil || !strings.Contains(err.Error(), n.Slug) {
		t.Fatal("template deletion did not name its user", err)
	}
	if _, err := s.EnvironmentConfigDelete(ctx, env.ID); err == nil || !strings.Contains(err.Error(), c.Slug) {
		t.Fatal("environment deletion did not name its user", err)
	}
	// Retained execution from a deleted source remains evidence, but a clone
	// inheriting that receipt must not claim to have executed it itself.
	n.DeletedAt = &now
	if err := s.store.SaveNode(n, b); err != nil {
		t.Fatal(err)
	}
	clone, err := s.NodeCreate(ctx, NodeCreateInput{Slug: "unexecuted-clone", Configuration: c.ID, Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SaveNode(clone, b); err != nil {
		t.Fatal(err)
	}
	data, err = s.readRecipeCatalog(ctx, []EnvironmentConfig{env})
	if err != nil || len(data.nodes) != 1 || data.nodes[0].ID != clone.ID || data.runs[env.ID].node.ID != n.ID {
		t.Fatal("deleted usage or inherited execution was misattributed", data, err)
	}
}

func TestTUIRecipeUnavailableMetadataDoesNotClaimZeroUsage(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	a.view = tuiViewTemplates
	a.managerData.configurations = []Configuration{{ID: "c", Slug: "recipe"}}
	a.managerData.environments = []EnvironmentConfig{{ID: "e", Slug: "environment"}}
	a.managerData.err = errors.New("metadata unavailable")
	for _, environment := range []bool{false, true} {
		a.environmentsView = environment
		a.draw()
		text := renderedScreenText(t, a.vx, 150, 38)
		for _, misleading := range []string{"USED BY 0 NODES", "no templates", "0 templates", "no recorded execution"} {
			if strings.Contains(text, misleading) {
				t.Fatalf("missing metadata was reported as %q\n%s", misleading, text)
			}
		}
	}
}

func TestTUIRecipeCommandActionsRequireDetailSelectionAndClampAfterRefresh(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	a.view, a.environmentsView = tuiViewTemplates, true
	env := EnvironmentConfig{ID: "e", Slug: "env", BootstrapCommands: []string{"first", "second", "third"}}
	a.managerData.environments = []EnvironmentConfig{env}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyDown, Modifiers: vaxis.ModShift})
	if a.overlay != nil {
		t.Fatal("list focus unexpectedly reordered a command")
	}
	a.recipes.details, a.recipes.command = true, 2
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyUp, Modifiers: vaxis.ModShift, EventType: vaxis.EventRelease})
	if a.overlay != nil {
		t.Fatal("key release reordered a command")
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyUp, Modifiers: vaxis.ModShift})
	e, ok := a.overlay.(*tuiEnvironmentEditor)
	if !ok || strings.Join(e.commands(), ",") != "first,third,second" || env.BootstrapCommands[2] != "third" {
		t.Fatal("reorder did not preserve a separate draft")
	}
	a.overlay = nil
	env.BootstrapCommands = []string{"first"}
	a.applyManagerData(tuiManagerDataEvent{view: tuiViewTemplates, data: tuiManagerData{environments: []EnvironmentConfig{env}}})
	if a.recipes.command != 0 {
		t.Fatal("refresh left the selected command outside the new list", a.recipes.command)
	}
	a.managerData.environments = append(a.managerData.environments, EnvironmentConfig{ID: "other", Slug: "other", BootstrapCommands: []string{"only"}})
	a.recipes.command = 2
	a.draw()
	for _, control := range a.recipes.controls {
		if control.action == "select" && control.index == 1 {
			a.handleMouse(vaxis.Mouse{Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress, Col: control.rect.col, Row: control.rect.row})
			break
		}
	}
	if a.environmentIndex != 1 || a.recipes.command != 0 || a.recipes.details {
		t.Fatal("mouse selection retained another environment's command")
	}
}

func TestTUIRecipeSelectionSurvivesRefreshAndClampsDeletion(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.view = tuiViewTemplates
	a.managerData.configurations = []Configuration{{ID: "b", Slug: "b"}, {ID: "c", Slug: "c"}}
	a.templateIndex = 1
	a.applyManagerData(tuiManagerDataEvent{view: tuiViewTemplates, data: tuiManagerData{configurations: []Configuration{{ID: "a", Slug: "a"}, {ID: "b", Slug: "b"}, {ID: "c", Slug: "c"}}}})
	if a.templateIndex != 2 {
		t.Fatal("refresh changed selected template identity", a.templateIndex)
	}
	a.applyManagerData(tuiManagerDataEvent{view: tuiViewTemplates, data: tuiManagerData{configurations: []Configuration{{ID: "a", Slug: "a"}}}})
	if a.templateIndex != 0 {
		t.Fatal("removed selection was not clamped")
	}
}

func TestTUIRecipeDeletionRefreshesVisibleCatalog(t *testing.T) {
	for _, environments := range []bool{false, true} {
		a, _ := testCreateForm(t)
		a.view, a.environmentsView = tuiViewTemplates, environments
		ctx := context.Background()
		if environments {
			e, err := a.service.EnvironmentConfigCreate(ctx, EnvironmentConfigCreateInput{Slug: "delete-selected"})
			if err != nil {
				t.Fatal(err)
			}
			a.loadManagerData(false)
			before := len(a.managerData.environments)
			a.environmentIndex = slices.IndexFunc(a.managerData.environments, func(candidate EnvironmentConfig) bool { return candidate.ID == e.ID })
			a.openDeleteEnvironmentConfigDialog(e)
			a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEnter})
			if a.overlay != nil || len(a.managerData.environments) != before-1 || a.environmentIndex >= len(a.managerData.environments) || a.managerData.environments[a.environmentIndex].ID == e.ID {
				t.Fatal("environment deletion left a stale or invalid selected row")
			}
		} else {
			c, err := a.service.ConfigurationCreate(ctx, ConfigurationCreateInput{Slug: "delete-selected"})
			if err != nil {
				t.Fatal(err)
			}
			a.loadManagerData(false)
			before := len(a.managerData.configurations)
			a.templateIndex = slices.IndexFunc(a.managerData.configurations, func(candidate Configuration) bool { return candidate.ID == c.ID })
			a.openDeleteConfigurationDialog(c)
			a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEnter})
			if a.overlay != nil || len(a.managerData.configurations) != before-1 || a.templateIndex >= len(a.managerData.configurations) || a.managerData.configurations[a.templateIndex].ID == c.ID {
				t.Fatal("configuration deletion left a stale or invalid selected row")
			}
		}
	}
}

func TestTUIRecipeResponsiveListsDetailsAndMouse(t *testing.T) {
	for _, size := range [][2]int{{150, 38}, {80, 24}} {
		a, _ := newTestManager(t, size[0], size[1])
		a.view = tuiViewTemplates
		for i := 0; i < 40; i++ {
			a.managerData.configurations = append(a.managerData.configurations, Configuration{ID: fmt.Sprint(i), Slug: fmt.Sprintf("recipe-%02d", i), Image: "template:ubuntu", VCPUs: 2, MemoryMiB: 4096, DiskMiB: 25600})
		}
		a.templateIndex = 39
		a.draw()
		text := renderedScreenText(t, a.vx, size[0], size[1])
		for _, want := range []string{"Configurations 40", "recipe-39", "KIND", "NODES"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%v missing %q\n%s", size, want, text)
			}
		}
		a.handleKey(vaxis.Key{Keycode: vaxis.KeyRight})
		a.draw()
		text = renderedScreenText(t, a.vx, size[0], size[1])
		if !strings.Contains(text, "template:ubuntu") {
			t.Fatalf("details unreachable at %v\n%s", size, text)
		}
		a.handleKey(vaxis.Key{Keycode: vaxis.KeyLeft})
		a.draw()
		r := a.recipes.kindRects[1]
		a.handleMouse(vaxis.Mouse{Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress, Col: r.col, Row: r.row})
		if !a.environmentsView {
			t.Fatal("environment tab mouse target is missing")
		}
		a.managerData.environments = []EnvironmentConfig{{ID: "env", Slug: "env", BootstrapCommands: []string{"echo first"}}}
		a.handleKey(vaxis.Key{Keycode: vaxis.KeyRight})
		a.draw()
		text = renderedScreenText(t, a.vx, size[0], size[1])
		if !strings.Contains(text, "Shift+↑↓ order") || !strings.Contains(text, "E $EDITOR") {
			t.Fatalf("command actions hidden at %v\n%s", size, text)
		}
		a.status = "blocked deletion"
		a.draw()
		text = renderedScreenText(t, a.vx, size[0], size[1])
		if !strings.Contains(text, "blocked deletion") || !strings.Contains(text, "E $EDITOR") || strings.Contains(text, "Deleting an environment") {
			t.Fatalf("notice obscured actions or retained stale note text at %v\n%s", size, text)
		}
	}
}
