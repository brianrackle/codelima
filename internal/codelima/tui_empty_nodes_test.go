package codelima

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go.rockorager.dev/vaxis"
)

func emptyManagerFixture(t *testing.T, width, height int) *vaxisTUIApp {
	t.Helper()
	a, sessions := newTestManager(t, width, height)
	state, err := newTUIState(nil, sessions)
	if err != nil {
		t.Fatal(err)
	}
	a.state = state
	a.treeWorkspaceRoot, a.launchScope = "/work/my-api", "/work/my-api"
	a.refreshFleet()
	a.doctor.checkedAt = time.Now()
	a.doctor.report.Checks = []DoctorCheck{{Name: "lima", Status: "ok", Message: "limactl 2.1.0 is supported"}, {Name: "git", Status: "ok", Message: "git is available"}}
	a.managerData.configurations = []Configuration{{Slug: "small", VCPUs: 3, MemoryMiB: 5120, DiskMiB: 30720}}
	return a
}

func TestTUIManagerEmptyGuideUsesObservedDefaults(t *testing.T) {
	for _, size := range [][2]int{{150, 38}, {80, 24}} {
		a := emptyManagerFixture(t, size[0], size[1])
		a.draw()
		screen := renderedScreenText(t, a.vx, size[0], size[1])
		for _, want := range []string{"No nodes in /work/my-api", "Create a node", "3 vCPU", "5 GiB", "30 GiB", "mounted", "HOST CHECK", "limactl 2.1.0", "Browse templates"} {
			if !strings.Contains(screen, want) {
				t.Errorf("%dx%d missing %q:\n%s", size[0], size[1], want, screen)
			}
		}
		if strings.Contains(screen, "NAME") || strings.Contains(screen, "40s") || strings.Contains(screen, "host logins found") {
			t.Fatalf("empty guide retained the table or invented observations:\n%s", screen)
		}
	}
}

func TestTUIManagerEmptyFilteredFleetKeepsSearch(t *testing.T) {
	a := emptyManagerFixture(t, 80, 24)
	a.fleet.setFilter("nonmatching")
	a.draw()
	screen := renderedScreenText(t, a.vx, 80, 24)
	if !strings.Contains(screen, "No matching nodes") || strings.Contains(screen, "Create a node") {
		t.Fatalf("filtered result became onboarding:\n%s", screen)
	}
}

func TestTUIManagerEmptyInitialCheckErrorIsVisible(t *testing.T) {
	a := emptyManagerFixture(t, 80, 24)
	a.doctor = tuiDoctorState{generation: 1, loading: true}
	a.applyDoctorData(tuiDoctorDataEvent{generation: 1, err: errors.New("host probe unavailable")})
	a.draw()
	if reason := a.emptyNodeBlockReason(); !strings.Contains(reason, "host probe unavailable") {
		t.Fatalf("initial check error lost: %q", reason)
	}
	screen := renderedScreenText(t, a.vx, 80, 24)
	if strings.Contains(screen, "Checking host prerequisites") || !strings.Contains(screen, "host probe unavailable") {
		t.Fatalf("failed initial check still appears pending:\n%s", screen)
	}
}

func TestTUIManagerEmptyCreateDirectoryAcrossScopes(t *testing.T) {
	a := emptyManagerFixture(t, 80, 24)
	a.launchScope, a.treeWorkspaceRoot = t.TempDir(), ""
	if err := a.openCreateNodeDialog(); err != nil {
		t.Fatal(err)
	}
	if got := a.overlay.(*tuiCreateNodeForm).value("directory"); got != a.launchScope {
		t.Fatalf("all-node create directory = %q, want launch scope %q", got, a.launchScope)
	}
	explicit := t.TempDir()
	if err := a.openCreateNodeDialogWithDefaults(explicit, DefaultConfigurationSlug); err != nil {
		t.Fatal(err)
	}
	if got := a.overlay.(*tuiCreateNodeForm).value("directory"); got != explicit {
		t.Fatalf("explicit create directory = %q, want %q", got, explicit)
	}
}

func TestTUIManagerEmptyBlockedCreateAndRecovery(t *testing.T) {
	a := emptyManagerFixture(t, 80, 24)
	a.doctor.report.Checks = append(a.doctor.report.Checks, DoctorCheck{Name: "lima", Status: "fail", Message: "limactl not found on PATH"})
	a.handleKey(vaxis.Key{Keycode: 'n', Text: "n"})
	if a.overlay != nil || !strings.Contains(a.status, "limactl not found") {
		t.Fatal("blocked empty view opened creation or lost the reason")
	}
	a.doctor.report.Checks = a.doctor.report.Checks[:2]
	a.doctor.report.Warnings = []string{"optional credentials unavailable"}
	a.treeWorkspaceRoot = t.TempDir()
	a.handleKey(vaxis.Key{Keycode: 'n', Text: "n"})
	form, ok := a.overlay.(*tuiCreateNodeForm)
	if !ok || form.value("directory") != a.treeWorkspaceRoot {
		t.Fatal("healthy scoped empty view did not create in its displayed directory")
	}
}

func TestTUIManagerEmptyRecheckIsReadOnlyAndPressOnly(t *testing.T) {
	a := emptyManagerFixture(t, 80, 24)
	a.service, _ = newTestService(t)
	a.handleKey(vaxis.Key{Keycode: 'R', Text: "R"})
	generation := a.doctor.generation
	if generation == 0 {
		t.Fatal("re-check did not start")
	}
	for _, check := range a.doctor.report.Checks {
		if check.Name == "repair" {
			t.Fatal("empty guide re-check repaired metadata")
		}
	}
	a.handleKey(vaxis.Key{Keycode: 'R', Text: "R", EventType: vaxis.EventRepeat})
	if a.doctor.generation != generation {
		t.Fatal("held re-check key started another check")
	}
}

func TestTUIManagerEmptyMouseActionsAndFullErrors(t *testing.T) {
	for _, size := range [][2]int{{150, 38}, {80, 24}} {
		a := emptyManagerFixture(t, size[0], size[1])
		a.doctor.err = errors.New(strings.Repeat("host failure ", 250) + "final host evidence")
		a.draw()
		var create, doctor tuiRect
		for _, control := range a.emptyNodes.controls {
			if control.key == "n" {
				create = control.rect
			}
			if control.key == "5" {
				doctor = control.rect
			}
		}
		if create.width == 0 || doctor.width == 0 {
			t.Fatal("empty guide actions have no mouse targets")
		}
		a.handleMouse(vaxis.Mouse{Col: create.col, Row: create.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
		if a.overlay != nil || !strings.Contains(a.status, "Host checks unavailable") {
			t.Fatal("mouse create bypassed the host check")
		}
		found := false
		for range 20 {
			a.handleKey(vaxis.Key{Keycode: vaxis.KeyPgDown})
			a.draw()
			screen := renderedScreenText(t, a.vx, size[0], size[1])
			// The terminal may wrap between "final" and "host evidence".
			if strings.Contains(screen, "final") && strings.Contains(screen, "host evidence") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%dx%d full error unreachable, scroll=%d height=%d:\n%s", size[0], size[1], a.emptyNodes.scroll, a.emptyNodes.height, renderedScreenText(t, a.vx, size[0], size[1]))
		}
		a.emptyNodes.scroll = 0
		a.draw()
		a.systemPanel = tuiSystemSettings
		a.handleMouse(vaxis.Mouse{Col: doctor.col, Row: doctor.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
		if a.view != tuiViewSystem || a.systemPanel != tuiSystemDoctor {
			t.Fatal("empty guide Doctor action opened the wrong panel")
		}
	}
}
