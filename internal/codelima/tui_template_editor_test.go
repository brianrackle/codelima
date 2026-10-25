package codelima

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func testTemplateEditor(t *testing.T) (*vaxisTUIApp, *tuiTemplateEditor) {
	t.Helper()
	a, _ := testCreateForm(t)
	c, err := a.service.ConfigurationCreate(context.Background(), ConfigurationCreateInput{Slug: "editor-test"})
	if err != nil {
		t.Fatal(err)
	}
	a.openUpdateConfigurationDialog(c)
	return a, a.overlay.(*tuiTemplateEditor)
}

func TestTUITemplateEditorSavesOrderedCommandsAndKeepsExistingNodesFrozen(t *testing.T) {
	a, e := testTemplateEditor(t)
	ctx := context.Background()
	profile := "name: preview-test\ninstall_commands:\n  - printf INSTALL\nvalidation_command: printf VALIDATE\nlaunch_command: sh\n"
	if err := os.WriteFile(filepath.Join(a.service.cfg.AgentProfilesDir, "preview-test.yaml"), []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"first", "second"} {
		if _, err := a.service.EnvironmentConfigCreate(ctx, EnvironmentConfigCreateInput{Slug: slug, BootstrapCommands: []string{"printf " + slug}}); err != nil {
			t.Fatal(err)
		}
	}
	e.load()
	e.dialog.SetFieldValue("agent", "preview-test")
	e.setEnvironments([]string{"first", "second"})
	e.moveEnvironment("second", -1)
	e.setCommands([]string{"printf 'one\ntwo'", "echo done"})
	e.dialog.SetFieldValue("memory", "20G")
	before, err := a.service.NodeCreate(ctx, NodeCreateInput{Slug: "before-edit", Configuration: e.original.ID, Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	e.load()
	preview, err := e.previewSteps()
	if err != nil {
		t.Fatal(err)
	}
	a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	if a.overlay == e {
		t.Fatal("save did not finish", e.dialog.Error)
	}
	after, err := a.service.NodeCreate(ctx, NodeCreateInput{Slug: "after-edit", Configuration: e.original.ID, Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	b, err := a.service.store.LoadBootstrapState(after.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview) != len(b.Steps) {
		t.Fatalf("preview=%d frozen=%d", len(preview), len(b.Steps))
	}
	if preview[0].Command != "printf INSTALL" || preview[len(preview)-1].Command != "printf VALIDATE" {
		t.Fatal("profile install/validation missing from preview", preview)
	}
	for i := range preview {
		if preview[i].Command != b.Steps[i].Command || preview[i].SourceName != b.Steps[i].SourceName {
			t.Fatalf("preview differs from frozen step %d", i)
		}
	}
	stored, err := a.service.store.NodeByID(before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.MemoryMiB != before.MemoryMiB || !slices.Equal(stored.Environments, before.Environments) {
		t.Fatal("save changed an existing node")
	}
	if after.MemoryMiB != 20*1024 || !slices.Equal(after.Environments, []string{"second", "first"}) {
		t.Fatal("new node lost edited values", after)
	}
	if !strings.Contains(a.status, "1 existing node") {
		t.Fatal("missing frozen-node count", a.status)
	}
}

func TestTUITemplateEditorClearsCommandsAndEnvironments(t *testing.T) {
	a, e := testTemplateEditor(t)
	e.setCommands([]string{})
	e.setEnvironments([]string{})
	a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	c, err := a.service.ConfigurationShow(context.Background(), e.original.ID)
	if err != nil || len(c.Environments) != 0 || len(c.BootstrapCommands) != 0 {
		t.Fatal("empty selections inherited defaults", c, err)
	}
}

func TestTUITemplateEditorCloneExplicitEmptySelections(t *testing.T) {
	a, original := testTemplateEditor(t)
	e := a.openTemplateEditor(original.original, true, nil, nil)
	e.dialog.SetFieldValue("slug", "empty-clone")
	e.setCommands([]string{})
	e.setEnvironments([]string{})
	a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	c, err := a.service.ConfigurationShow(context.Background(), "empty-clone")
	if err != nil || len(c.Environments) != 0 || len(c.BootstrapCommands) != 0 {
		t.Fatal("new template inherited cleared defaults", c, err)
	}
}

func TestTUITemplateEditorUnavailableServiceKeepsDraft(t *testing.T) {
	a, original := testTemplateEditor(t)
	a.service = nil
	e := a.openTemplateEditor(original.original, false, nil, nil)
	e.dialog.FieldIndex = 1
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEnter})
	selector, ok := a.overlay.(*tuiSelector)
	if !ok || !strings.Contains(selector.EmptyText, "unavailable") {
		t.Fatal("missing unavailable image catalog state")
	}
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEsc})
	a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	if a.overlay != e || !strings.Contains(e.dialog.Error, "unavailable") {
		t.Fatal("unavailable save lost the draft")
	}
}

func TestTUITemplateEditorStepsResourcesAndProtectsDefaultName(t *testing.T) {
	a, e := testTemplateEditor(t)
	e.dialog.FieldIndex = 3
	e.dialog.SetFieldValue("vcpus", "5")
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyRight})
	if e.value("vcpus") != "6" {
		t.Fatal("CPU step", e.value("vcpus"))
	}
	e.dialog.FieldIndex = 4
	e.dialog.SetFieldValue("memory", "20G")
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyLeft})
	if v, _ := parseTemplateSize(e.value("memory")); v != 16*1024 {
		t.Fatal("memory step", e.value("memory"))
	}
	c, err := a.service.ConfigurationShow(context.Background(), DefaultConfigurationSlug)
	if err != nil {
		t.Fatal(err)
	}
	a.openUpdateConfigurationDialog(c)
	e = a.overlay.(*tuiTemplateEditor)
	a.updateOverlay(vaxis.Key{Keycode: 'x', Text: "x"})
	if e.value("slug") != DefaultConfigurationSlug {
		t.Fatal("default name was editable")
	}
	e.dialog.SetFieldValue("memory", "99G")
	e.restoreDefaults()
	if v, _ := parseTemplateSize(e.value("memory")); v != DefaultMemoryMiB {
		t.Fatal("restore did not use built-in defaults")
	}
}

func TestTUITemplateEditorValidationDiscardAndStaleLoad(t *testing.T) {
	a, e := testTemplateEditor(t)
	e.dialog.SetFieldValue("slug", DefaultConfigurationSlug)
	a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	if a.overlay != e || e.dialog.Error == "" {
		t.Fatal("rejected edit was lost")
	}
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEsc})
	if !e.dialog.confirmDiscard {
		t.Fatal("dirty editor did not ask before discard")
	}
	a.updateOverlay(vaxis.Key{Keycode: 'n', Text: "n"})
	if a.overlay != e || e.dialog.confirmDiscard {
		t.Fatal("keep editing failed")
	}
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEsc})
	a.updateOverlay(vaxis.Key{Keycode: 'y', Text: "y"})
	a.applyTemplateEditorData(tuiTemplateEditorDataEvent{editor: e})
	if a.overlay != nil {
		t.Fatal("closed editor reopened from a late load")
	}
}

func TestTUITemplateEditorResponsivePanelsAndBootstrapInput(t *testing.T) {
	for _, size := range [][2]int{{150, 38}, {80, 24}} {
		a, e := testTemplateEditor(t)
		a.vx.Resize(vaxis.Resize{Cols: size[0], Rows: size[1]})
		e.dialog.FieldIndex = 7
		a.draw()
		text := renderedScreenText(t, a.vx, size[0], size[1])
		for _, want := range []string{"Bootstrap", "Ctrl+s"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %q at %v\n%s", want, size, text)
			}
		}
		if size[0] > 100 {
			for _, want := range []string{"New nodes will run", "When you save", "Changes"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing panel %q\n%s", want, text)
				}
			}
		}
		a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEnter})
		if _, ok := a.overlay.(*tuiCommandEditor); !ok {
			t.Fatal("Bootstrap did not open multiline editor")
		}
	}
}
