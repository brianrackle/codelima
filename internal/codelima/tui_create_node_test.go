package codelima

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"go.rockorager.dev/vaxis"
)

func testCreateForm(t *testing.T) (*vaxisTUIApp, *tuiCreateNodeForm) {
	t.Helper()
	a, _ := newTestManager(t, 150, 38)
	a.service, _ = newTestService(t)
	a.ctx = context.Background()
	if err := a.service.ensureReadyForWrite(a.ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.openCreateNodeDialogWithDefaults(t.TempDir(), DefaultConfigurationSlug); err != nil {
		t.Fatal(err)
	}
	form, ok := a.overlay.(*tuiCreateNodeForm)
	if !ok {
		t.Fatal("create form is unavailable")
	}
	form.dialog.SetFieldValue("slug", "new-node")
	return a, form
}

func TestTUICreateFormFitWarningsDoNotBlockValidInput(t *testing.T) {
	a, f := testCreateForm(t)
	memory, disk := uint64(1), uint64(1)
	a.host.AllocationsKnown, a.host.Capacity.CPUs = true, 1
	a.host.Capacity.MemoryBytes, a.host.Capacity.DiskAvailableBytes = &memory, &disk
	c, _ := f.template()
	fit := createHostFit(a.host, c)
	if !fit[0].warning || !fit[1].shortfall || !fit[2].warning || f.validationError() != "" {
		t.Fatal("capacity shortfalls were hidden or blocked a valid form", fit, f.validationError())
	}
	a.host.UnknownNodes = 1
	if fit := createHostFit(a.host, c); !strings.Contains(fit[0].text, "unavailable") || !strings.Contains(fit[1].text, "unavailable") {
		t.Fatal("unknown allocation was reported as free capacity", fit)
	}
}

func TestTUICreateFormRetainsAmbiguousAdmissionIdentityAndPayload(t *testing.T) {
	a, f := testCreateForm(t)
	input := &NodeCreateInput{Slug: "new-node", Directory: f.value("directory"), Start: true}
	request := NodeOperationRequest{ID: newID(), Kind: "create", Origin: "tui", Create: input}
	f.pending, f.request = true, &request
	a.applyCreateAdmission(tuiCreateAdmissionEvent{form: f, request: request, err: errors.New("lost reply")})
	_, _ = f.Update(vaxis.Key{Keycode: 'x', Text: "x"})
	if f.request == nil || f.request.ID != request.ID || f.value("slug") != "new-node" || f.pending {
		t.Fatal("ambiguous admission lost its identity or allowed a different payload")
	}
	a.applyCreateAdmission(tuiCreateAdmissionEvent{form: f, request: request, err: errors.New("slug reserved"), definitive: true})
	_, _ = f.Update(vaxis.Key{Keycode: 'x', Text: "x"})
	if f.request != nil || f.value("slug") == "new-node" || a.overlay != f {
		t.Fatal("definitive rejection did not restore editable values")
	}
}

func TestTUICreateFormAdmitsFirstStartWithoutWaitingForBoot(t *testing.T) {
	a, f := testCreateForm(t)
	gate := createStartSandbox{SandboxClient: a.service.sandbox, entered: make(chan Node, 1), release: make(chan struct{})}
	a.service.sandbox = gate
	home, _ := testCLIOperationServer(t, a.service, Version)
	client, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	a.service = a.service.withIO(a.service.stdout, a.service.stderr)
	a.service.daemonClient = client
	events := make(chan vaxis.Event, 32)
	a.postEvent = func(event vaxis.Event) { events <- event }
	f.dialog.SetFieldValue("import_host_auth", hostAuthImportOff)
	f.dialog.SetFieldValue("attach", "true")
	if err := f.submit(); err != nil {
		t.Fatal(err)
	}
	var e tuiCreateAdmissionEvent
	select {
	case event := <-events:
		e = event.(tuiCreateAdmissionEvent)
	case <-time.After(3 * time.Second):
		t.Fatal("form waited for boot instead of admission")
	}
	if e.err != nil || e.record.ID == "" || !e.request.Create.Start || e.record.State != "running" {
		t.Fatal("incorrect admission", e)
	}
	a.applyCreateAdmission(e)
	if a.overlay != nil || a.createIntent == nil || !a.createIntent.attach || a.createIntent.operationID != e.record.ID {
		t.Fatal("form did not close into receipt-bound intent")
	}
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("accepted creation did not boot")
	}
	a.managerGeneration++ // Leaving while it runs revokes only transient navigation.
	a.pollCreateIntent()
	if a.createIntent != nil {
		t.Fatal("navigation retained automatic attachment intent")
	}
	var record NodeOperation
	if err := client.Call(context.Background(), "operation.get", map[string]string{"id": e.record.ID}, &record); err != nil || record.State != "running" {
		t.Fatal("navigation canceled daemon work", record, err)
	}
	close(gate.release)
}

func TestTUICreateFormRendersWideAndScrollsCompactFields(t *testing.T) {
	for _, size := range [][2]int{{150, 38}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			a, _ := newTestManager(t, size[0], size[1])
			a.service, _ = newTestService(t)
			if err := a.openCreateNodeDialogWithDefaults(t.TempDir(), DefaultConfigurationSlug); err != nil {
				t.Fatal(err)
			}
			f := a.overlay.(*tuiCreateNodeForm)
			a.draw()
			text := renderedScreenText(t, a.vx, size[0], size[1])
			for _, label := range []string{"New node", "Name", "Directory", "FIT ON THIS HOST"} {
				if !strings.Contains(text, label) {
					t.Fatalf("missing %q\n%s", label, text)
				}
			}
			f.dialog.FieldIndex = 7
			a.draw()
			text = renderedScreenText(t, a.vx, size[0], size[1])
			for _, label := range []string{"Ports", "After create", "attach when ready"} {
				if !strings.Contains(text, label) {
					t.Fatalf("unreachable %q\n%s", label, text)
				}
			}
		})
	}
}

func TestTUICreateFormValidatesGlobalNamesAndAcceptsSuggestion(t *testing.T) {
	_, f := testCreateForm(t)
	f.nodes = []Node{{Slug: "new-node"}, {Slug: "new-node-2"}}
	if got := f.fieldError("slug"); !strings.Contains(got, "already used") {
		t.Fatal("duplicate global name was accepted", got)
	}
	_, _ = f.Update(vaxis.Key{Keycode: vaxis.KeyTab})
	if f.value("slug") != "new-node-3" || f.dialog.FieldIndex != 1 {
		t.Fatal("Tab did not accept the first available suggestion and advance")
	}
	f.dialog.SetFieldValue("slug", "Has Spaces")
	if f.fieldError("slug") == "" {
		t.Fatal("invalid slug was accepted")
	}
	f.dialog.SetFieldValue("ports", "3000:80,3000:81")
	if f.fieldError("ports") == "" {
		t.Fatal("duplicate host port was accepted")
	}
	f.dialog.SetFieldValue("ports", "3000:80, 5432:5432")
	if f.fieldError("ports") != "" {
		t.Fatal("valid mappings were rejected")
	}
}

func TestTUICreateFormLaunchChoicesAndDirtyDiscard(t *testing.T) {
	_, f := testCreateForm(t)
	if f.value("start") != "true" || f.value("attach") != "false" {
		t.Fatal("launch defaults differ from the design")
	}
	f.dialog.FieldIndex = 7
	_, _ = f.Update(vaxis.Key{Keycode: ' ', Text: " "})
	if f.value("attach") != "true" {
		t.Fatal("Space did not enable attachment")
	}
	f.dialog.FieldIndex = 6
	_, _ = f.Update(vaxis.Key{Keycode: ' ', Text: " "})
	if f.value("start") != "false" || f.value("attach") != "false" {
		t.Fatal("disabling startup retained attachment")
	}
	f.dialog.FieldIndex = 7
	_, _ = f.Update(vaxis.Key{Keycode: ' ', Text: " "})
	if f.value("attach") != "false" {
		t.Fatal("attachment can be enabled without startup")
	}
	_, _ = f.Update(vaxis.Key{Keycode: vaxis.KeyTab, Modifiers: vaxis.ModShift})
	if f.dialog.FieldIndex != 6 {
		t.Fatal("Shift+Tab did not move backward")
	}
	if done, _ := f.Update(vaxis.Key{Keycode: vaxis.KeyEsc}); done || !f.dialog.confirmDiscard {
		t.Fatal("dirty form closed without confirmation")
	}
	if done, _ := f.Update(vaxis.Key{Keycode: 'n', Text: "n"}); done || f.dialog.confirmDiscard {
		t.Fatal("keep editing did not preserve the form")
	}
}

func TestTUICreateFormMouseControlsReuseFieldActions(t *testing.T) {
	a, f := testCreateForm(t)
	a.draw()
	click := func(index int) {
		r := f.fieldRects[index]
		a.handleMouse(vaxis.Mouse{Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress, Col: r.col + 15, Row: r.row})
	}
	click(6)
	if f.value("start") != "false" {
		t.Fatal("click did not toggle startup")
	}
	click(7)
	if f.value("attach") != "false" {
		t.Fatal("mouse enabled attachment while startup is disabled")
	}
	click(3)
	if f.value("workspace_mode") != WorkspaceModeCopy {
		t.Fatal("mouse did not toggle workspace")
	}
	click(2)
	if _, ok := a.overlay.(*tuiTemplatePicker); !ok {
		t.Fatal("mouse did not open template choices")
	}
}

func TestTUICreateFormPortsReplaceTheirPlaceholder(t *testing.T) {
	a, f := testCreateForm(t)
	f.dialog.FieldIndex = 5
	f.dialog.SetFieldValue("ports", "123:abc")
	a.draw()
	text := renderedScreenText(t, a.vx, 150, 38)
	if !strings.Contains(text, "123:abc") || strings.Contains(text, "HOST:GUEST, … (optional)") {
		t.Fatalf("ports retained a placeholder in front of typed input\n%s", text)
	}
}

func TestTUICreateStartingKeepsEightPhasesAndActivePhaseVisible(t *testing.T) {
	for _, height := range []int{24, 18} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			a := testActivityApp(t, 80, height)
			a.view, a.nodeDetail = tuiViewNodes, true
			node := a.state.selectedEntry().node
			phases := append(initialNodeOperationPhases("create"), initialNodeOperationPhases("start")...)
			op := NodeOperation{ID: "created-start", NodeID: node.ID, State: "running", CurrentPhase: "validation", Request: NodeOperationRequest{Kind: "create", Create: &NodeCreateInput{Start: true}}, Phases: phases}
			a.lifecycle = tuiLifecycleState{nodeID: node.ID, loaded: true, operation: op}
			a.activity.page.Running = []NodeOperation{op}
			a.draw()
			text := renderedScreenText(t, a.vx, 80, height)
			if !strings.Contains(text, "Validate agent") {
				t.Fatalf("active phase was clipped\n%s", text)
			}
			if height == 24 && (!strings.Contains(text, "Create VM") || !strings.Contains(text, "Ready")) {
				t.Fatalf("eight-phase creation did not fit compact layout\n%s", text)
			}
		})
	}
}

func TestTUICreateFormSlowChecksAreFenced(t *testing.T) {
	a, f := testCreateForm(t)
	events := make(chan vaxis.Event, 8)
	a.postEvent = func(event vaxis.Event) { events <- event }
	oldPath := f.value("directory")
	f.pathInFlight = true
	f.dialog.SetFieldValue("directory", filepath.Join(oldPath, "missing"))
	a.applyCreatePath(tuiCreatePathEvent{form: f, path: oldPath})
	if f.fieldError("directory") == "" {
		t.Fatal("old successful directory check validated a newer path")
	}
	var result tuiCreatePathEvent
	select {
	case event := <-events:
		result = event.(tuiCreatePathEvent)
	case <-time.After(3 * time.Second):
		t.Fatal("latest directory was not checked after the old check finished")
	}
	a.overlay = nil
	a.applyCreatePath(result)
	if f.checkedPath == result.path {
		t.Fatal("a closed form accepted a late result")
	}
}

func TestTUICreateFormInvalidSubmitPreservesValues(t *testing.T) {
	a, f := testCreateForm(t)
	f.dialog.SetFieldValue("slug", "bad slug")
	_, _ = f.Update(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	if a.overlay != f || f.value("slug") != "bad slug" || f.dialog.Error == "" {
		t.Fatal("invalid submission closed or erased the form")
	}
	nodes, err := a.service.NodeList(context.Background(), false)
	if err != nil || len(nodes) != 0 {
		t.Fatal("invalid form created a node", nodes, err)
	}
}

func TestNodeCreateDirectoryRejectsMetadataSymlinkAliases(t *testing.T) {
	s, _ := newTestService(t)
	if err := s.ensureReadyForWrite(context.Background()); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "metadata")
	if err := os.Symlink(s.cfg.MetadataRoot, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := s.resolveNodeDirectoryPath(alias); err == nil {
		t.Fatal("a workspace symlink into metadata was accepted")
	}
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(workspace, outside); err != nil {
		t.Fatal(err)
	}
	if got, err := s.resolveNodeDirectoryPath(outside); err != nil || got != outside {
		t.Fatal("legitimate workspace alias lost its chosen path", got, err)
	}
}
