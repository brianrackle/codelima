package codelima

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"go.rockorager.dev/vaxis"
)

type tuiEnvironmentEditor struct {
	tuiOverlayKeys
	app               *vaxisTUIApp
	dialog            *tuiDialog
	original          EnvironmentConfig
	creating, pending bool
	controls          []tuiRecipeControl
}
type tuiEnvironmentSavedEvent struct {
	editor      *tuiEnvironmentEditor
	environment EnvironmentConfig
	used        int
	err         error
}

func (a *vaxisTUIApp) openEnvironmentEditor(env EnvironmentConfig, creating bool) *tuiEnvironmentEditor {
	e := &tuiEnvironmentEditor{app: a, original: env, creating: creating}
	slug, title := env.Slug, "Edit environment "+env.Slug
	if creating {
		title = "New environment"
		slug = ""
		if env.ID != "" {
			slug = env.Slug + "-copy"
		}
	}
	e.dialog = newTUIDialog(title, "Save", nil, []tuiDialogField{newTUIInputField("slug", "Name", slug, true), newTUISelectorField("commands", "Commands", "", false, nil)}, nil)
	e.dialog.Fields[1].Activate = func() error { e.openCommands(a.recipes.command); return nil }
	e.setCommands(env.BootstrapCommands)
	e.dialog.initialValues["commands"] = e.dialog.Fields[1].Value
	if !creating {
		e.dialog.Fields[0] = tuiDialogField{Key: "slug", Label: "Name", Value: slug, Required: true}
		e.dialog.FieldIndex = 1
	}
	a.overlay = e
	return e
}
func (e *tuiEnvironmentEditor) PreferredSize() (int, int) { return 100, 24 }
func (e *tuiEnvironmentEditor) FooterHint() string {
	if e.dialog.confirmDiscard {
		return e.dialog.FooterHint()
	}
	return e.bindingHints("{dialog.next} field · {dialog.accept} edit commands · {dialog.submit} save · {form.restore} restore built-in · {dialog.cancel} cancel")
}
func (e *tuiEnvironmentEditor) commands() []string {
	commands := []string{}
	_ = json.Unmarshal([]byte(e.dialog.Fields[1].Value), &commands)
	return commands
}
func (e *tuiEnvironmentEditor) setCommands(commands []string) {
	if commands == nil {
		commands = []string{}
	}
	data, _ := json.Marshal(commands)
	e.dialog.SetFieldValue("commands", string(data))
}
func (e *tuiEnvironmentEditor) visible() bool {
	for o := e.app.overlay; o != nil; o = overlayParent(o) {
		if o == e {
			return true
		}
	}
	return false
}
func (e *tuiEnvironmentEditor) openCommands(index int) {
	child := newTUICommandEditor(e.app, e, e.commands(), e.setCommands)
	child.index = max(0, min(index, len(child.buffers)-1))
	e.app.overlay = child
}
func (e *tuiEnvironmentEditor) restoreDefaults() {
	if builtin, ok := builtInEnvironment(e.original.Slug); ok && !e.creating {
		e.setCommands(builtin.BootstrapCommands)
	}
}
func (e *tuiEnvironmentEditor) Update(event vaxis.Event) (bool, error) {
	if e.pending {
		return false, nil
	}
	if e.dialog.confirmDiscard {
		return e.dialog.Update(event)
	}
	if m, ok := event.(vaxis.Mouse); ok {
		if m.EventType == vaxis.EventPress && m.Button == vaxis.MouseLeftButton {
			for _, c := range e.controls {
				if c.rect.contains(m.Col, m.Row) {
					switch c.action {
					case "name":
						e.dialog.FieldIndex = 0
					case "commands":
						e.dialog.FieldIndex = 1
						e.openCommands(c.index)
					case "save":
						e.save()
					case "cancel":
						return e.dialog.cancel()
					}
					break
				}
			}
		}
		return false, nil
	}
	if key, ok := event.(vaxis.Key); ok && key.EventType != vaxis.EventPaste {
		if !tuiKeyActivates(key, false) {
			return false, nil
		}
		if overlayKeyAction(e, key) == "dialog.submit" {
			if tuiKeyActivates(key, true) {
				e.save()
			}
			return false, nil
		}
		if overlayKeyAction(e, key) == "form.restore" {
			if tuiKeyActivates(key, true) {
				e.restoreDefaults()
			}
			return false, nil
		}
		if overlayKeyAction(e, key) == "dialog.accept" && e.dialog.FieldIndex == 0 {
			if tuiKeyActivates(key, true) {
				e.dialog.FieldIndex = 1
				e.openCommands(0)
			}
			return false, nil
		}
		if !e.creating && e.dialog.FieldIndex == 0 && key.Text != "" && overlayKeyAction(e, key) != "dialog.cancel" {
			return false, nil
		}
	}
	if !e.creating && e.dialog.FieldIndex == 0 {
		if _, ok := event.(vaxis.PasteStartEvent); ok {
			return false, nil
		}
		if key, ok := event.(vaxis.Key); ok && key.EventType == vaxis.EventPaste {
			return false, nil
		}
	}
	return e.dialog.Update(event)
}
func (e *tuiEnvironmentEditor) save() {
	slug := e.dialog.Fields[0].rawValue()
	if slug == "" || slugify(slug) != slug {
		e.dialog.Error = "Name must be a unique lowercase slug"
		return
	}
	if !e.creating && slug != e.original.Slug {
		e.dialog.Error = "Clone an environment to use a different name"
		return
	}
	if e.app.service == nil {
		e.dialog.Error = "Environment service unavailable"
		return
	}
	commands := e.commands()
	creating, id := e.creating, e.original.ID
	service := e.app.service
	ctx := e.app.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	e.pending = true
	e.dialog.Error = ""
	save := func() tuiEnvironmentSavedEvent {
		defer cancel()
		result := tuiEnvironmentSavedEvent{editor: e, used: -1}
		if creating {
			result.environment, result.err = service.EnvironmentConfigCreate(ctx, EnvironmentConfigCreateInput{Slug: slug, BootstrapCommands: commands})
		} else {
			result.environment, result.err = service.EnvironmentConfigUpdate(ctx, id, EnvironmentConfigUpdateInput{BootstrapCommands: commands, ClearBootstrapCommands: len(commands) == 0})
		}
		if result.err == nil {
			if templates, err := service.ConfigurationList(ctx, false); err == nil {
				result.used = 0
				for _, c := range templates {
					for _, name := range c.Environments {
						if name == slug {
							result.used++
							break
						}
					}
				}
			}
		}
		return result
	}
	if e.app.postEvent == nil {
		e.app.applyEnvironmentSaved(save())
		return
	}
	go func() { e.app.postCompletion(save()) }()
}
func (a *vaxisTUIApp) applyEnvironmentSaved(event tuiEnvironmentSavedEvent) {
	e := event.editor
	e.pending = false
	if !e.visible() {
		return
	}
	if event.err != nil {
		e.dialog.Error = event.err.Error()
		return
	}
	a.overlay = nil
	a.environmentSelectID = event.environment.ID
	message := "Saved environment " + event.environment.Slug + " · existing nodes keep frozen commands"
	if event.used >= 0 {
		message += fmt.Sprintf(" · used by %d templates", event.used)
	}
	a.setStatus(slog.LevelInfo, message)
	a.loadManagerData(false)
}
func (e *tuiEnvironmentEditor) Draw(win vaxis.Window, _, _, _ vaxis.Style) {
	t := e.app.managerTheme().modal()
	tuiFill(win, t.raised)
	title := e.dialog.Title
	if e.dialog.dirty() {
		title += " · unsaved"
	}
	body := templatePanel(win, title, t.mint, t)
	w, h := body.Size()
	x, y := body.Origin()
	e.controls = nil
	tuiLine(body.New(1, 1, w-2, 1), 0, "Ordered command blocks · run once as root · existing nodes stay frozen", t.dim)
	tuiLine(body.New(1, 3, 12, 1), 0, "Name", t.muted)
	f := &e.dialog.Fields[0]
	if f.Input != nil {
		f.Input.Content = t.text
		f.Input.HideCursor = e.dialog.FieldIndex != 0 || e.pending
		f.Input.Draw(body.New(14, 3, max(1, w-16), 1))
	} else {
		tuiLine(body.New(14, 3, max(1, w-16), 1), 0, f.Value+" · clone to use a new name", t.text)
	}
	e.controls = append(e.controls, tuiRecipeControl{tuiRect{col: x + 1, row: y + 3, width: w - 2, height: 1}, "name", 0})
	tuiLine(body.New(1, 5, w-2, 1), 0, e.bindingHints("Commands · {dialog.accept} opens the multiline editor"), t.mint)
	commands := e.commands()
	visible := max(1, h-10)
	if len(commands) == 0 {
		tuiLine(body.New(1, 6, w-2, 1), 0, e.bindingHints("(none) · click or press {dialog.accept} to add"), t.dim)
	}
	for i, command := range commands[:min(len(commands), visible)] {
		tuiLine(body.New(1, 6+i, w-2, 1), 0, fmt.Sprintf("%d │ %s", i+1, templateCommandLine(command)), t.text)
	}
	for i := 0; i < max(1, min(len(commands), visible)); i++ {
		e.controls = append(e.controls, tuiRecipeControl{tuiRect{col: x + 1, row: y + 6 + i, width: w - 2, height: 1}, "commands", i})
	}
	status := e.bindingHintsFormat("%d command blocks · {dialog.submit} saves this environment", len(commands))
	style := t.muted
	if e.pending {
		status, style = "Saving environment…", t.amber
	}
	if e.dialog.Error != "" {
		status, style = e.dialog.Error, t.coral
	}
	if e.dialog.confirmDiscard {
		status, style = e.bindingHints("Discard unsaved changes? {discard.confirm} discard · {discard.cancel} keep editing"), t.coral
	}
	tuiLine(body.New(1, h-3, w-2, 1), 0, status, style)
	col := 1
	for _, c := range []struct {
		label, action string
	}{{e.bindingHints("{dialog.submit} save"), "save"}, {e.bindingHints("{dialog.cancel} cancel"), "cancel"}} {
		width := min(max(0, (w-4)/2), renderedTextWidth(e.app.vx, c.label))
		tuiLine(body.New(col, h-1, width, 1), 0, c.label, t.mint)
		e.controls = append(e.controls, tuiRecipeControl{tuiRect{col: x + col, row: y + h - 1, width: width, height: 1}, c.action, 0})
		col += width + 2
	}
}
