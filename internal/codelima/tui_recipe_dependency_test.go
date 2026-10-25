package codelima

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func testRecipeDependency(t *testing.T, width, height int) (*vaxisTUIApp, EnvironmentConfig) {
	t.Helper()
	a, _ := newTestManager(t, width, height)
	a.service, _ = newTestService(t)
	env, err := a.service.EnvironmentConfigCreate(context.Background(), EnvironmentConfigCreateInput{
		Slug: "rust-tools", BootstrapCommands: []string{"apt-get update && apt-get install -y build-essential", "curl -fsSL https://example.invalid/rust | sh", "rustup component add clippy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.view, a.environmentsView = tuiViewTemplates, true
	a.loadManagerData(false)
	a.environmentIndex = slices.IndexFunc(a.managerData.environments, func(e EnvironmentConfig) bool { return e.ID == env.ID })
	a.recipes.details, a.recipes.command = true, 1
	return a, env
}

func TestTUIRecipeDependencyWarningOpensReviewableDraftAndSavesOnlyExplicitly(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			a, env := testRecipeDependency(t, size[0], size[1])
			a.draw()
			text := renderedScreenText(t, a.vx, size[0], size[1])
			for _, want := range []string{"uses curl", "static check", "add curl to line 1"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q at %v\n%s", want, size, text)
				}
			}
			a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter, EventType: vaxis.EventRelease})
			if a.overlay != nil {
				t.Fatal("key release applied a dependency edit")
			}
			a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
			child, ok := a.overlay.(*tuiCommandEditor)
			if !ok || child.index != 0 {
				t.Fatal("warning did not open the proposed install command")
			}
			parent, ok := overlayParent(child).(*tuiEnvironmentEditor)
			if !ok || !parent.dialog.dirty() || !strings.HasSuffix(parent.commands()[0], "build-essential curl") {
				t.Fatal("fix did not remain an unsaved environment draft")
			}
			stored, err := a.service.EnvironmentConfigShow(context.Background(), env.ID)
			if err != nil || !slices.Equal(stored.BootstrapCommands, env.BootstrapCommands) {
				t.Fatal("opening the fix persisted it", stored, err)
			}
			a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEsc})
			a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEsc})
			a.updateOverlay(vaxis.Key{Keycode: 'y', Text: "y"})
			stored, err = a.service.EnvironmentConfigShow(context.Background(), env.ID)
			if err != nil || !slices.Equal(stored.BootstrapCommands, env.BootstrapCommands) || a.overlay != nil {
				t.Fatal("discard changed the saved environment", stored, err)
			}
			a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
			a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
			a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
			stored, err = a.service.EnvironmentConfigShow(context.Background(), env.ID)
			if err != nil || !strings.HasSuffix(stored.BootstrapCommands[0], "build-essential curl") || len(analyzeEnvironmentDependencies(stored.BootstrapCommands).Warnings) != 0 {
				t.Fatal("explicit save lost the proposed fix", stored, err)
			}
		})
	}
}

func TestTUIRecipeDependencyMouseAndCurrentProviderDefinitions(t *testing.T) {
	a, env := testRecipeDependency(t, 150, 38)
	_, rows := a.recipeDetailRows(a.managerTheme())
	var text string
	for _, row := range rows {
		text += row.text + "\n"
	}
	if !strings.Contains(text, "codex, claude-code") {
		t.Fatal("current provider evidence is missing", text)
	}
	if _, err := a.service.EnvironmentConfigUpdate(context.Background(), "codex", EnvironmentConfigUpdateInput{BootstrapCommands: []string{"printf modified"}}); err != nil {
		t.Fatal(err)
	}
	a.loadManagerData(false)
	_, rows = a.recipeDetailRows(a.managerTheme())
	for _, row := range rows {
		if strings.Contains(row.text, "Can run after:") && strings.Contains(row.text, "codex") {
			t.Fatal("edited built-in retained invented install evidence", row.text)
		}
	}
	a.draw()
	clicked := false
	for _, control := range a.recipes.controls {
		if control.action == "dependency" {
			a.handleMouse(vaxis.Mouse{Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress, Col: control.rect.col, Row: control.rect.row})
			clicked = true
			break
		}
	}
	if !clicked {
		t.Fatal("dependency suggestion has no mouse action")
	}
	child, ok := a.overlay.(*tuiCommandEditor)
	if !ok || overlayParent(child).(*tuiEnvironmentEditor).original.ID != env.ID {
		t.Fatal("mouse suggestion targeted a different environment")
	}
}

func TestTUIRecipeDependencyWithoutSafeSuggestionKeepsNormalEditing(t *testing.T) {
	for _, command := range []string{"curl --version", "if", "#" + strings.Repeat("x", 64<<10)} {
		a, env := testRecipeDependency(t, 80, 24)
		env, err := a.service.EnvironmentConfigUpdate(context.Background(), env.ID, EnvironmentConfigUpdateInput{BootstrapCommands: []string{command}})
		if err != nil {
			t.Fatal(err)
		}
		a.loadManagerData(false)
		a.draw()
		for _, control := range a.recipes.controls {
			if control.action == "dependency" {
				t.Fatal("offered an edit without a recognized earlier install")
			}
		}
		if command != "curl --version" && !strings.Contains(renderedScreenText(t, a.vx, 80, 24), "Static check unavailable") {
			t.Fatal("unsupported analysis silently claimed completeness")
		}
		a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
		child, ok := a.overlay.(*tuiCommandEditor)
		if !ok {
			t.Fatal("advisory check blocked normal command editing")
		}
		parent := overlayParent(child).(*tuiEnvironmentEditor)
		if parent.dialog.dirty() || !slices.Equal(parent.commands(), env.BootstrapCommands) {
			t.Fatal("normal editing invented a dependency fix")
		}
	}
}
