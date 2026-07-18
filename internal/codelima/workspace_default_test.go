package codelima

import "testing"

func TestTUICreateNodeDialogDefaultsToMountedWorkspace(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	app := &vaxisTUIApp{service: service}
	if err := app.openCreateNodeDialog(); err != nil {
		t.Fatalf("openCreateNodeDialog() error = %v", err)
	}
	if app.dialog == nil || len(app.dialog.Fields) < 4 {
		t.Fatalf("expected create node dialog with workspace mode, got %#v", app.dialog)
	}
	field := app.dialog.Fields[3]
	if got := field.rawValue(); got != WorkspaceModeMounted {
		t.Fatalf("expected mounted workspace mode by default, got %q", got)
	}
	if err := field.Activate(); err != nil {
		t.Fatalf("activate workspace mode selector: %v", err)
	}
	if app.selector == nil || !app.selector.Selected[WorkspaceModeMounted] {
		t.Fatalf("expected workspace selector to preselect mounted, got %#v", app.selector)
	}
}

func TestNodeWorkspaceModeKeepsBlankLegacyMetadataInCopyMode(t *testing.T) {
	t.Parallel()

	if got := nodeWorkspaceMode(Node{}); got != WorkspaceModeCopy {
		t.Fatalf("expected blank legacy workspace metadata to remain copy-mode, got %q", got)
	}
}
