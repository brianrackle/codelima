package codelima

import (
	"path/filepath"
	"strings"
	"testing"

	"git.sr.ht/~rockorager/vaxis"
)

func TestTUICreateNodeDialogDefaultsToMountedWorkspace(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	app := &vaxisTUIApp{service: service}
	if err := app.openCreateNodeDialog(); err != nil {
		t.Fatalf("openCreateNodeDialog() error = %v", err)
	}
	dialog := app.activeDialog()
	if dialog == nil || len(dialog.Fields) < 4 {
		t.Fatalf("expected create node dialog with workspace mode, got %#v", app.overlay)
	}
	field := dialog.Fields[3]
	if got := field.rawValue(); got != WorkspaceModeMounted {
		t.Fatalf("expected mounted workspace mode by default, got %q", got)
	}
	if err := field.Activate(); err != nil {
		t.Fatalf("activate workspace mode selector: %v", err)
	}
	selector := app.activeSelector()
	if selector == nil || !selector.Selected[WorkspaceModeMounted] {
		t.Fatalf("expected workspace selector to preselect mounted, got %#v", app.overlay)
	}
}

func TestTUICreateNodeDialogDefaultsSlugToCurrentDirectoryLeaf(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	app := &vaxisTUIApp{service: service}
	if err := app.openCreateNodeDialog(); err != nil {
		t.Fatalf("openCreateNodeDialog() error = %v", err)
	}
	dialog := app.activeDialog()
	if dialog == nil || len(dialog.Fields) < 2 {
		t.Fatalf("expected create node dialog with slug and directory fields, got %#v", app.overlay)
	}

	cwd, err := canonicalPath(".")
	if err != nil {
		t.Fatalf("canonicalPath(.) error = %v", err)
	}
	if got, want := dialog.Fields[0].rawValue(), slugify(filepath.Base(cwd)); got != want {
		t.Fatalf("default node slug = %q, want current directory leaf slug %q", got, want)
	}
	if got := dialog.Fields[0].Input.String(); got != "" {
		t.Fatalf("slug input content = %q, want an empty input behind the displayed default", got)
	}
	if got := dialog.Fields[1].rawValue(); got != cwd {
		t.Fatalf("default node directory = %q, want %q", got, cwd)
	}
	if got := dialog.Fields[1].Input.String(); got != "" {
		t.Fatalf("directory input content = %q, want an empty input behind the displayed default", got)
	}
}

func TestTUICreateNodeDialogReplacesMutedDefaultsWhenTyping(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	app := &vaxisTUIApp{service: service}
	if err := app.openCreateNodeDialog(); err != nil {
		t.Fatalf("openCreateNodeDialog() error = %v", err)
	}
	dialog := app.activeDialog()
	if dialog == nil {
		t.Fatalf("expected create node dialog, got %#v", app.overlay)
	}

	vx := newRenderTestVaxis(t, 100, 24)
	defer vx.Close()
	dialog.Draw(vx.Window(), vaxis.Style{}, tuiMutedStyle(), vaxis.Style{})

	cwd, err := canonicalPath(".")
	if err != nil {
		t.Fatalf("canonicalPath(.) error = %v", err)
	}
	defaultSlug := slugify(filepath.Base(cwd))
	rendered := renderedScreenText(t, vx, 100, 24)
	assertRenderedTextUsesStyle(t, vx, rendered, defaultSlug, tuiMutedStyle())
	assertRenderedTextUsesStyle(t, vx, rendered, cwd, tuiMutedStyle())

	if _, err := dialog.Update(vaxis.Key{Keycode: 'o', Text: "operator-node"}); err != nil {
		t.Fatalf("type explicit slug: %v", err)
	}
	if _, err := dialog.Update(vaxis.Key{Keycode: vaxis.KeyTab}); err != nil {
		t.Fatalf("move to directory field: %v", err)
	}
	if _, err := dialog.Update(vaxis.Key{Keycode: '/', Text: "/workspace/explicit"}); err != nil {
		t.Fatalf("type explicit directory: %v", err)
	}

	dialog.Draw(vx.Window(), vaxis.Style{}, tuiMutedStyle(), vaxis.Style{})
	rendered = renderedScreenText(t, vx, 100, 24)
	if strings.Contains(rendered, defaultSlug+"operator-node") {
		t.Fatalf("typed slug was appended to its default instead of replacing it:\n%s", rendered)
	}
	if strings.Contains(rendered, cwd+"/workspace/explicit") {
		t.Fatalf("typed directory was appended to its default instead of replacing it:\n%s", rendered)
	}
	if !strings.Contains(rendered, "operator-node") || !strings.Contains(rendered, "/workspace/explicit") {
		t.Fatalf("explicit values were not rendered after replacing defaults:\n%s", rendered)
	}
	if got := dialog.Fields[0].rawValue(); got != "operator-node" {
		t.Fatalf("submitted slug = %q, want explicit value", got)
	}
	if got := dialog.Fields[1].rawValue(); got != "/workspace/explicit" {
		t.Fatalf("submitted directory = %q, want explicit value", got)
	}
}

func assertRenderedTextUsesStyle(t *testing.T, vx *vaxis.Vaxis, rendered, text string, want vaxis.Style) {
	t.Helper()

	for row, line := range strings.Split(rendered, "\n") {
		col := strings.Index(line, text)
		if col < 0 {
			continue
		}
		if got := renderedCellStyle(t, vx, col, row); got.Foreground != want.Foreground || got.Attribute != want.Attribute {
			t.Fatalf("rendered %q style = %#v, want foreground %#v and attribute %#v", text, got, want.Foreground, want.Attribute)
		}
		return
	}
	t.Fatalf("rendered dialog does not contain %q:\n%s", text, rendered)
}

func TestNodeWorkspaceModeKeepsBlankLegacyMetadataInCopyMode(t *testing.T) {
	t.Parallel()

	if got := nodeWorkspaceMode(Node{}); got != WorkspaceModeCopy {
		t.Fatalf("expected blank legacy workspace metadata to remain copy-mode, got %q", got)
	}
}
