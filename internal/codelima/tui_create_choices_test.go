package codelima

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func TestTUITemplatePickerFiltersSelectsAndPreservesParent(t *testing.T) {
	a, f := testCreateForm(t)
	f.dialog.SetFieldValue("ports", "5432:5432")
	f.openTemplatePicker()
	p := a.overlay.(*tuiTemplatePicker)
	a.updateOverlay(vaxis.Key{Keycode: 'm', Text: "medium"})
	if rows := p.choices(); len(rows) != 1 || rows[0].Slug != "medium" {
		t.Fatal("filter did not narrow choices", rows)
	}
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyRight})
	if !p.details {
		t.Fatal("details did not open")
	}
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEsc})
	if a.overlay != p || p.details {
		t.Fatal("details escape lost the picker")
	}
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEnter})
	if a.overlay != f || f.value("configuration") != "medium" || f.value("ports") != "5432:5432" {
		t.Fatal("selection did not restore form values")
	}
	f.openTemplatePicker()
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEsc})
	if a.overlay != f || f.value("configuration") != "medium" {
		t.Fatal("picker cancellation lost the form")
	}
}

func TestTUITemplatePickerCreatesThenReturnsWithNewTemplateSelected(t *testing.T) {
	a, f := testCreateForm(t)
	f.openTemplatePicker()
	p := a.overlay.(*tuiTemplatePicker)
	p.openNewTemplate()
	e := a.overlay.(*tuiTemplateCreateEditor)
	e.dialog.SetFieldValue("slug", DefaultConfigurationSlug)
	a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	if a.overlay != e || e.dialog.Error == "" {
		t.Fatal("failed template save dismissed the editor")
	}
	e.dialog.SetFieldValue("slug", "qa-new-template")
	a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	if a.overlay != p {
		t.Fatal("saved template did not return to picker")
	}
	rows := p.choices()
	if p.index >= len(rows) || rows[p.index].Slug != "qa-new-template" {
		t.Fatal("new template was not selected", p.selected)
	}
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEnter})
	if a.overlay != f || f.value("configuration") != "qa-new-template" {
		t.Fatal("new template cannot be chosen")
	}
}

func TestTUICreateDirectoryBrowsePreservesFormAndValidatesChoice(t *testing.T) {
	a, f := testCreateForm(t)
	dir := f.value("directory")
	child := filepath.Join(dir, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	f.browseDirectory(dir)
	s := a.activeSelector()
	if s == nil || len(s.Options) != 3 {
		t.Fatal("directory entries were not loaded", s)
	}
	s.Index = 2
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEnter})
	s = a.activeSelector()
	if s == nil || s.Description[0] != child {
		t.Fatal("directory traversal lost its parent")
	}
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEnter})
	if a.overlay != f || f.value("directory") != child || f.fieldError("directory") != "" {
		t.Fatal("selected directory did not restore a valid form")
	}
	f.browseDirectory(a.service.cfg.MetadataRoot)
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEnter})
	if a.overlay != f || !strings.Contains(f.fieldError("directory"), "CODELIMA_HOME") {
		t.Fatal("browser bypassed metadata-directory validation")
	}
}

func TestTUITemplatePickerRenderUsesObservedCapacity(t *testing.T) {
	a, f := testCreateForm(t)
	f.openTemplatePicker()
	a.draw()
	text := renderedScreenText(t, a.vx, 150, 38)
	for _, label := range []string{"NAME", "MEMORY FIT", "new template", "unavailable"} {
		if !strings.Contains(text, label) {
			t.Fatalf("missing %q\n%s", label, text)
		}
	}
	if strings.Contains(text, "16 GiB free") {
		t.Fatal("picker invented reference-board capacity")
	}
}
