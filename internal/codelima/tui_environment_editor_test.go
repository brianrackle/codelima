package codelima

import (
	"context"
	"slices"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func TestTUIEnvironmentEditorPreservesDraftOnFailureAndCommandBoundaries(t *testing.T) {
	a, _ := testCreateForm(t)
	e := a.openEnvironmentEditor(EnvironmentConfig{}, true)
	e.dialog.SetFieldValue("slug", "codex")
	e.setCommands([]string{"printf 'one\ntwo'\n\tprintf END", "printf tail"})
	a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	if a.overlay != e || e.dialog.Error == "" {
		t.Fatal("duplicate discarded the environment draft")
	}
	e.dialog.SetFieldValue("slug", "new-recipe-env")
	a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	stored, err := a.service.EnvironmentConfigShow(context.Background(), "new-recipe-env")
	if err != nil || !slices.Equal(stored.BootstrapCommands, e.commands()) {
		t.Fatal("save changed command boundaries", stored, err)
	}
	e = a.openEnvironmentEditor(stored, false)
	e.openCommands(0)
	child := a.overlay.(*tuiCommandEditor)
	_, _ = child.Update(vaxis.Key{Keycode: 'x', Modifiers: vaxis.ModCtrl})
	_, _ = child.Update(vaxis.Key{Keycode: 'x', Modifiers: vaxis.ModCtrl})
	a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	stored, err = a.service.EnvironmentConfigShow(context.Background(), stored.ID)
	if err != nil || len(stored.BootstrapCommands) != 0 {
		t.Fatal("clear did not persist", stored, err)
	}
}

func TestTUIEnvironmentEditorRestoreDiscardAndLateCompletion(t *testing.T) {
	a, _ := testCreateForm(t)
	env, err := a.service.EnvironmentConfigShow(context.Background(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	e := a.openEnvironmentEditor(env, false)
	e.dialog.FieldIndex = 0
	a.updateOverlay(vaxis.Key{Keycode: 'u', Modifiers: vaxis.ModCtrl})
	a.updateOverlay(vaxis.Key{Keycode: 'x', Text: "x"})
	if e.dialog.Fields[0].rawValue() != env.Slug {
		t.Fatal("editing renamed an environment without updating its references")
	}
	e.setCommands([]string{"edited"})
	e.restoreDefaults()
	if !slices.Equal(e.commands(), env.BootstrapCommands) {
		t.Fatal("restore differs from shipped commands")
	}
	e.setCommands([]string{"unsaved"})
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEsc})
	a.updateOverlay(vaxis.Key{Keycode: 'n', Text: "n"})
	if a.overlay != e || e.dialog.confirmDiscard {
		t.Fatal("keep editing lost the draft")
	}
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEsc})
	a.updateOverlay(vaxis.Key{Keycode: 'y', Text: "y"})
	a.applyEnvironmentSaved(tuiEnvironmentSavedEvent{editor: e, environment: env})
	if a.overlay != nil {
		t.Fatal("late completion reopened a closed environment")
	}
	if !strings.Contains(environmentKind(EnvironmentConfig{Slug: "codex", BootstrapCommands: []string{"changed"}}), "•") {
		t.Fatal("modified built-in is not marked")
	}
}
