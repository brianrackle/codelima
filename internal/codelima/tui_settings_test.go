package codelima

import (
	"strings"
	"testing"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
)

func TestTUIManagerSettingsEditPersistsAndControlsShellMode(t *testing.T) {
	app, _ := newTestManager(t, 80, 24)
	app.service = app.sessions.service
	app.view = tuiViewSystem
	for range tuiSystemSettings {
		app.handleKey(vaxis.Key{Keycode: vaxis.KeyTab})
	}
	app.draw()
	screen := renderedScreenText(t, app.vx, 80, 24)
	for _, want := range []string{"shell.mode", "shell.window", "import_host_auth", "confirm stop", "group by directory", "color"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("missing setting %q:\n%s", want, screen)
		}
	}
	app.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
	if app.activeSelector() == nil {
		t.Fatal("Enter did not open mode choices")
	}
	app.handleKey(vaxis.Key{Keycode: vaxis.KeyDown})
	app.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
	if app.managerSettings().Shell.Mode != "window" {
		t.Fatalf("mode not applied: %+v", app.managerSettings().Shell)
	}
	loaded, err := LoadConfig(app.service.cfg.MetadataRoot)
	if err != nil || loaded.Shell.Mode != "window" {
		t.Fatalf("mode did not survive reload: %+v %v", loaded.Shell, err)
	}
}

func TestTUIManagerSettingsStopConfirmationUsesPromptState(t *testing.T) {
	app, _ := newTestManager(t, 80, 24)
	node := app.state.selectedEntry().node
	if app.confirmNodeStop(node) {
		t.Fatal("no sessions should not request busy confirmation")
	}
	term := &metadataTestTerminal{fakeTUITerminal: newFakeTUITerminal()}
	putTestSession(app.sessions, terminal.NodeTarget(node.ID).String(), &tuiSession{shellKind: terminal.NodeShell}, term)
	if !app.confirmNodeStop(node) {
		t.Fatal("unknown or busy shell did not ask")
	}
	term.metadata.CursorAtPrompt = true
	if app.confirmNodeStop(node) {
		t.Fatal("idle prompt incorrectly asked")
	}
	cfg := app.managerSettings()
	cfg.ConfirmStop = "always"
	app.preferences = &cfg
	if !app.confirmNodeStop(node) {
		t.Fatal("always ignored")
	}
	cfg.ConfirmStop = "never"
	term.metadata.CursorAtPrompt = false
	if app.confirmNodeStop(node) {
		t.Fatal("never ignored")
	}
}

func TestTUIManagerSettingsLateDoctorResponseCannotRevertSave(t *testing.T) {
	app, _ := newTestManager(t, 80, 24)
	app.service = app.sessions.service
	app.view = tuiViewSystem
	before := app.managerSettings()
	if err := app.saveManagerSetting("shell.mode", "embedded", nil); err != nil {
		t.Fatal(err)
	}
	app.applyManagerData(tuiManagerDataEvent{generation: app.managerGeneration, view: tuiViewSystem, data: tuiManagerData{preferences: &before}})
	if app.managerSettings().Shell.Mode != "embedded" {
		t.Fatal("a slow doctor response reverted the saved preference")
	}
}
