package codelima

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.rockorager.dev/vaxis"
)

// The dialog owns editable values and discard confirmation. This presentation
// owns domain validation and immutable asynchronous observations, on the UI loop.
type tuiCreateNodeForm struct {
	tuiOverlayKeys
	app          *vaxisTUIApp
	dialog       *tuiDialog
	nodes        []Node
	templates    []Configuration
	loaded       bool
	dataInFlight bool
	dataError    error
	dataAt       time.Time
	dataSerial   uint64
	checkedPath  string
	pathError    error
	pathInFlight bool
	pending      bool
	request      *NodeOperationRequest
	attach       bool
	fieldRects   []tuiRect
	submitRect   tuiRect
	firstField   int
}

type tuiCreateDataEvent struct {
	form      *tuiCreateNodeForm
	serial    uint64
	nodes     []Node
	templates []Configuration
	err       error
}

type tuiCreatePathEvent struct {
	form *tuiCreateNodeForm
	path string
	err  error
}

func (a *vaxisTUIApp) newCreateNodeForm(directory, configuration string) *tuiCreateNodeForm {
	f := &tuiCreateNodeForm{app: a}
	f.dialog = newTUIDialog("New node", "Create", nil, []tuiDialogField{
		newTUIDefaultInputField("slug", "Name", slugify(filepath.Base(directory)), true),
		newTUIDefaultInputField("directory", "Directory", directory, true),
		newTUIValueSelectorField("configuration", "Template", configuration, nil, nil),
		newTUIValueSelectorField("workspace_mode", "Workspace", DefaultWorkspaceMode, workspaceModeDisplay, nil),
		newTUIValueSelectorField("import_host_auth", "Credentials", hostAuthImportChoice(a.managerSettings().ImportHostAuth), hostAuthImportDisplay, nil),
		newTUIInputField("ports", "Ports", "", false),
		newTUIValueSelectorField("start", "After create", "true", nil, nil),
		newTUIValueSelectorField("attach", "Shell", "false", nil, nil),
	}, func(map[string]string) error { return f.submit() })
	f.dialog.Fields[1].Activate = func() error { f.browseDirectory(f.value("directory")); return nil }
	f.dialog.Fields[2].Activate = func() error { f.openTemplatePicker(); return nil }
	f.dialog.Fields[3].Activate = func() error {
		return a.openWorkspaceModeSelector(f.value("workspace_mode"), func(value string) error {
			f.dialog.SetFieldValue("workspace_mode", value)
			return nil
		})
	}
	for _, index := range []int{4, 6, 7} {
		f.dialog.Fields[index].Activate = func() error { f.toggle(index); return nil }
	}
	return f
}

func (f *tuiCreateNodeForm) value(key string) string {
	for _, field := range f.dialog.Fields {
		if field.Key == key {
			return field.rawValue()
		}
	}
	return ""
}

func (f *tuiCreateNodeForm) visible() bool {
	for overlay := f.app.overlay; overlay != nil; overlay = overlayParent(overlay) {
		if overlay == f {
			return true
		}
	}
	return false
}

func (f *tuiCreateNodeForm) workContext() (context.Context, context.CancelFunc) {
	ctx := f.app.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, 10*time.Second)
}

func (f *tuiCreateNodeForm) loadData() {
	if f.dataInFlight || f.app.service == nil {
		return
	}
	f.dataInFlight = true
	ctx, cancel := f.workContext()
	service := f.app.service
	serial := f.dataSerial
	load := func() tuiCreateDataEvent {
		defer cancel()
		e := tuiCreateDataEvent{form: f, serial: serial}
		e.templates, e.err = service.ConfigurationList(ctx, false)
		if e.err == nil {
			e.nodes, e.err = service.store.ListNodes(false)
		}
		return e
	}
	if f.app.postEvent == nil {
		f.app.applyCreateData(load())
		return
	}
	go func() { f.app.postCompletion(load()) }()
}

func (a *vaxisTUIApp) applyCreateData(e tuiCreateDataEvent) {
	f := e.form
	if !f.visible() {
		return
	}
	f.dataInFlight, f.dataAt, f.dataError = false, time.Now(), e.err
	if e.serial != f.dataSerial {
		f.loadData()
		return
	}
	if e.err == nil {
		f.nodes, f.templates, f.loaded = e.nodes, e.templates, true
	}
}

func (f *tuiCreateNodeForm) checkPath() {
	path := f.value("directory")
	if f.pathInFlight || f.checkedPath == path || f.app.service == nil {
		return
	}
	f.pathInFlight = true
	service := f.app.service
	load := func() tuiCreatePathEvent {
		_, err := service.resolveNodeDirectoryPath(path)
		return tuiCreatePathEvent{form: f, path: path, err: err}
	}
	if f.app.postEvent == nil {
		f.app.applyCreatePath(load())
		return
	}
	go func() { f.app.postCompletion(load()) }()
}

func (a *vaxisTUIApp) applyCreatePath(e tuiCreatePathEvent) {
	f := e.form
	if !f.visible() {
		return
	}
	f.pathInFlight = false
	if f.value("directory") == e.path {
		f.checkedPath, f.pathError = e.path, e.err
	} else {
		f.checkPath()
	}
}

func (f *tuiCreateNodeForm) template() (Configuration, bool) {
	for _, c := range f.templates {
		if c.Slug == f.value("configuration") || c.ID == f.value("configuration") {
			return c, true
		}
	}
	return Configuration{}, false
}

func (f *tuiCreateNodeForm) nameUsed(name string) bool {
	for _, node := range f.nodes {
		if node.Slug == name && node.DeletedAt == nil {
			return true
		}
	}
	return false
}

func (f *tuiCreateNodeForm) suggestion() string {
	name := f.value("slug")
	if name == "" || slugify(name) != name || !f.nameUsed(name) {
		return ""
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d", name, suffix)
		if !f.nameUsed(candidate) {
			return candidate
		}
	}
}

func (f *tuiCreateNodeForm) fieldError(key string) string {
	value := f.value(key)
	switch key {
	case "slug":
		if value == "" || slugify(value) != value {
			return "use a lowercase slug"
		}
		if !f.loaded {
			return "checking names…"
		}
		if f.nameUsed(value) {
			return f.bindingHints("already used · {dialog.next} accepts ") + f.suggestion()
		}
	case "directory":
		if value == "" {
			return "directory is required"
		}
		if f.checkedPath != value {
			return "checking directory…"
		}
		if f.pathError != nil {
			return f.pathError.Error()
		}
	case "configuration":
		if !f.loaded {
			return "loading templates…"
		}
		if _, ok := f.template(); !ok {
			return "choose an available template"
		}
	case "ports":
		if _, err := validatePorts(parseCommaSeparatedValues(value)); err != nil {
			return err.Error()
		}
	}
	return ""
}

func (f *tuiCreateNodeForm) validationError() string {
	if f.dataError != nil {
		return "Cannot check names/templates: " + f.dataError.Error()
	}
	for _, field := range f.dialog.Fields {
		if err := f.fieldError(field.Key); err != "" {
			return field.Label + ": " + err
		}
	}
	return ""
}

func (f *tuiCreateNodeForm) toggle(index int) {
	field := &f.dialog.Fields[index]
	switch field.Key {
	case "workspace_mode":
		if field.Value == WorkspaceModeMounted {
			field.Value = WorkspaceModeCopy
		} else {
			field.Value = WorkspaceModeMounted
		}
	case "import_host_auth":
		field.Value = hostAuthImportChoice(field.Value != hostAuthImportOn)
	case "start", "attach":
		if field.Key == "attach" && f.value("start") != "true" {
			return
		}
		if field.Value == "true" {
			field.Value = "false"
		} else {
			field.Value = "true"
		}
		if f.value("start") != "true" {
			f.dialog.SetFieldValue("attach", "false")
		}
	}
}

func (a *vaxisTUIApp) visibleCreateForm() *tuiCreateNodeForm {
	for overlay := a.overlay; overlay != nil; overlay = overlayParent(overlay) {
		if f, ok := overlay.(*tuiCreateNodeForm); ok {
			return f
		}
	}
	return nil
}

func (f *tuiCreateNodeForm) Update(event vaxis.Event) (bool, error) {
	if f.dialog.confirmDiscard {
		return f.dialog.Update(event)
	}
	defer f.checkPath()
	if key, ok := event.(vaxis.Key); ok && key.EventType != vaxis.EventPaste && tuiKeyActivates(key, false) {
		if f.pending || f.request != nil {
			if overlayKeyAction(f, key) == "dialog.cancel" && tuiKeyActivates(key, true) {
				return true, nil // Accepted work, if any, still belongs to the daemon.
			}
			if !f.pending && overlayKeyAction(f, key) == "dialog.submit" && tuiKeyActivates(key, true) {
				f.dialog.Error = ""
				if err := f.submit(); err != nil {
					f.dialog.Error = err.Error()
				}
			}
			return false, nil
		}
		switch {
		case overlayKeyAction(f, key) == "dialog.submit":
			if tuiKeyActivates(key, true) {
				if err := f.submit(); err != nil {
					f.dialog.Error = err.Error()
				}
			}
			return false, nil
		case overlayKeyAction(f, key) == "dialog.previous":
			f.dialog.FieldIndex = (f.dialog.FieldIndex + len(f.dialog.Fields) - 1) % len(f.dialog.Fields)
			return false, nil
		case overlayKeyAction(f, key) == "dialog.next" && f.dialog.FieldIndex == 0:
			if suggestion := f.suggestion(); suggestion != "" {
				f.dialog.SetFieldValue("slug", suggestion)
			}
		case overlayKeyAction(f, key) == "dialog.toggle" && f.dialog.Fields[f.dialog.FieldIndex].Input == nil:
			if tuiKeyActivates(key, true) {
				f.toggle(f.dialog.FieldIndex)
			}
			return false, nil
		case overlayKeyAction(f, key) == "dialog.accept":
			if !tuiKeyActivates(key, true) {
				return false, nil
			}
			field := &f.dialog.Fields[f.dialog.FieldIndex]
			if field.Activate != nil {
				if err := field.Activate(); err != nil {
					f.dialog.Error = err.Error()
				}
			} else {
				f.dialog.FieldIndex = (f.dialog.FieldIndex + 1) % len(f.dialog.Fields)
			}
			return false, nil
		}
	}
	if f.pending || f.request != nil {
		return false, nil
	}
	if mouse, ok := event.(vaxis.Mouse); ok && mouse.EventType == vaxis.EventPress {
		if mouse.Button == vaxis.MouseLeftButton {
			if f.submitRect.contains(mouse.Col, mouse.Row) {
				if err := f.submit(); err != nil {
					f.dialog.Error = err.Error()
				}
				return false, nil
			}
			for i, rect := range f.fieldRects {
				if rect.contains(mouse.Col, mouse.Row) {
					f.dialog.FieldIndex = i
					field := &f.dialog.Fields[i]
					if field.Key == "configuration" {
						f.openTemplatePicker()
					} else if field.Input == nil {
						f.toggle(i)
					}
					return false, nil
				}
			}
		}
		if mouse.Button == vaxis.MouseWheelDown {
			f.dialog.FieldIndex = min(len(f.dialog.Fields)-1, f.dialog.FieldIndex+1)
		}
		if mouse.Button == vaxis.MouseWheelUp {
			f.dialog.FieldIndex = max(0, f.dialog.FieldIndex-1)
		}
		return false, nil
	}
	return f.dialog.Update(event)
}

func (f *tuiCreateNodeForm) FooterHint() string {
	if f.dialog.confirmDiscard {
		return f.dialog.FooterHint()
	}
	if f.pending {
		return f.bindingHints("Submitting… · {dialog.cancel} close; accepted creation continues")
	}
	if f.request != nil {
		return f.bindingHints("Admission unconfirmed · {dialog.submit} retry same request · {dialog.cancel} close")
	}
	return f.bindingHints("{dialog.next}/{dialog.up dialog.down} fields · {dialog.accept} choices · {dialog.toggle} toggle · {dialog.submit} create · {dialog.cancel} cancel")
}

func (f *tuiCreateNodeForm) cliPreview() string {
	args := []string{"codelima node create --slug", shellQuote(f.value("slug")), "--configuration", shellQuote(f.value("configuration")), "--directory", shellQuote(f.value("directory"))}
	if f.value("workspace_mode") == WorkspaceModeCopy {
		args = append(args, "--workspace-mode copy")
	}
	if f.value("import_host_auth") != hostAuthImportOn {
		args = append(args, "--no-import-auth")
	}
	for _, port := range parseCommaSeparatedValues(f.value("ports")) {
		args = append(args, "--port", shellQuote(port))
	}
	if f.value("start") == "true" {
		args = append(args, "--start")
	}
	return strings.Join(args, " ")
}
