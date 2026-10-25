package codelima

import (
	"fmt"
	"slices"
	"strings"

	"go.rockorager.dev/vaxis"
)

func (e *tuiTemplateEditor) FooterHint() string {
	if e.dialog.confirmDiscard {
		return e.dialog.FooterHint()
	}
	if e.preview {
		return e.bindingHints("{form.preview} form · {dialog.up dialog.down}/{overlay.page_up overlay.page_down} scroll preview · {dialog.submit} save · {dialog.cancel} cancel")
	}
	switch e.dialog.FieldIndex {
	case 6:
		return e.bindingHints("{dialog.toggle} toggle · {form.reorder_up form.reorder_down} reorder · {dialog.next} field · {form.preview} preview · {dialog.submit} save")
	case 7:
		return e.bindingHints("{dialog.accept} edit commands · {dialog.next} field · {form.preview} preview · {dialog.submit} save · {dialog.cancel} cancel")
	default:
		return e.bindingHints("{dialog.next}/{dialog.up dialog.down} field · {dialog.left dialog.right} step · {dialog.accept} list · {form.preview} preview · {dialog.submit} save · {dialog.cancel} cancel")
	}
}
func (e *tuiTemplateEditor) Update(event vaxis.Event) (bool, error) {
	if e.pending {
		return false, nil
	}
	if e.dialog.confirmDiscard {
		return e.dialog.Update(event)
	}
	if mouse, ok := event.(vaxis.Mouse); ok {
		if mouse.EventType != vaxis.EventPress {
			return false, nil
		}
		if mouse.Button == vaxis.MouseWheelUp || mouse.Button == vaxis.MouseWheelDown {
			delta := 1
			if mouse.Button == vaxis.MouseWheelUp {
				delta = -1
			}
			if e.preview {
				e.previewScroll = max(0, e.previewScroll+delta)
			} else {
				e.moveFocus(delta)
			}
			return false, nil
		}
		if mouse.Button == vaxis.MouseLeftButton {
			for _, c := range e.controls {
				if c.rect.contains(mouse.Col, mouse.Row) {
					e.dialog.FieldIndex = c.field
					e.preview = false
					switch {
					case c.environment != "":
						e.environment = c.environment
						e.toggleEnvironment(c.environment)
					case c.step != 0:
						e.stepResource(c.field, c.step)
					case c.field == 7:
						e.openCommands()
					}
					break
				}
			}
		}
		return false, nil
	}
	key, ok := event.(vaxis.Key)
	if ok && key.EventType != vaxis.EventPaste {
		if !tuiKeyActivates(key, false) {
			return false, nil
		}
		switch {
		case overlayKeyAction(e, key) == "dialog.cancel":
			return e.dialog.Update(event)
		case overlayKeyAction(e, key) == "dialog.submit":
			if tuiKeyActivates(key, true) {
				e.save()
			}
			return false, nil
		case overlayKeyAction(e, key) == "form.restore":
			if tuiKeyActivates(key, true) {
				e.restoreDefaults()
			}
			return false, nil
		case overlayKeyAction(e, key) == "form.reload":
			if tuiKeyActivates(key, true) {
				e.load()
			}
			return false, nil
		case overlayKeyAction(e, key) == "form.preview":
			if tuiKeyActivates(key, true) {
				e.preview = !e.preview
			}
			return false, nil
		}
		if e.preview {
			switch {
			case overlayKeyAction(e, key) == "dialog.down":
				e.previewScroll++
			case overlayKeyAction(e, key) == "dialog.up":
				e.previewScroll = max(0, e.previewScroll-1)
			case overlayKeyAction(e, key) == "overlay.page_down":
				e.previewScroll += 10
			case overlayKeyAction(e, key) == "overlay.page_up":
				e.previewScroll = max(0, e.previewScroll-10)
			}
			return false, nil
		}
		e.dialog.Error = ""
		e.notice = ""
		switch {
		case overlayKeyAction(e, key) == "dialog.previous":
			e.dialog.FieldIndex = (e.dialog.FieldIndex + 7) % 8
			return false, nil
		case overlayKeyAction(e, key) == "dialog.next":
			e.dialog.FieldIndex = (e.dialog.FieldIndex + 1) % 8
			return false, nil
		case overlayKeyAction(e, key) == "form.reorder_up", overlayKeyAction(e, key) == "form.reorder_down":
			if e.dialog.FieldIndex == 6 {
				delta := -1
				if overlayKeyAction(e, key) == "dialog.down" || overlayKeyAction(e, key) == "form.reorder_down" {
					delta = 1
				}
				e.moveEnvironment(e.environment, delta)
			}
			return false, nil
		case overlayKeyAction(e, key) == "dialog.up", overlayKeyAction(e, key) == "dialog.down":
			delta := -1
			if overlayKeyAction(e, key) == "dialog.down" || overlayKeyAction(e, key) == "form.reorder_down" {
				delta = 1
			}
			e.moveFocus(delta)
			return false, nil
		case (overlayKeyAction(e, key) == "dialog.left" || overlayKeyAction(e, key) == "dialog.right") && e.dialog.FieldIndex >= 3 && e.dialog.FieldIndex <= 5:
			delta := -1
			if overlayKeyAction(e, key) == "dialog.right" {
				delta = 1
			}
			e.stepResource(e.dialog.FieldIndex, delta)
			return false, nil
		case overlayKeyAction(e, key) == "dialog.toggle" && e.dialog.FieldIndex == 6:
			if tuiKeyActivates(key, true) && e.environment != "" {
				e.toggleEnvironment(e.environment)
			}
			return false, nil
		case overlayKeyAction(e, key) == "dialog.accept":
			if !tuiKeyActivates(key, true) {
				return false, nil
			}
			switch e.dialog.FieldIndex {
			case 1, 2:
				e.openChoice(e.dialog.FieldIndex)
			case 7:
				e.openCommands()
			default:
				e.dialog.FieldIndex = (e.dialog.FieldIndex + 1) % 8
			}
			return false, nil
		}
	}
	if e.preview || (!e.initialized && e.dialog.FieldIndex != 0) || (e.dialog.FieldIndex == 0 && !e.creating && e.original.Slug == DefaultConfigurationSlug) {
		return false, nil
	}
	e.dialog.updateInput(event)
	return false, nil
}
func (e *tuiTemplateEditor) moveFocus(delta int) {
	if e.dialog.FieldIndex == 6 {
		rows := e.environmentRows()
		i := slices.IndexFunc(rows, func(env EnvironmentConfig) bool { return env.Slug == e.environment })
		j := i + delta
		if j >= 0 && j < len(rows) {
			e.environment = rows[j].Slug
			return
		}
	}
	e.dialog.FieldIndex = (e.dialog.FieldIndex + delta + 8) % 8
	if e.dialog.FieldIndex == 6 {
		rows := e.environmentRows()
		if len(rows) > 0 {
			i := 0
			if delta < 0 {
				i = len(rows) - 1
			}
			e.environment = rows[i].Slug
		}
	}
}
func (e *tuiTemplateEditor) openCommands() {
	e.app.overlay = newTUICommandEditor(e.app, e, e.commands(), e.setCommands)
}

type tuiTemplateImagesEvent struct {
	editor   *tuiTemplateEditor
	selector *tuiSelector
	names    []string
	err      error
}

func (e *tuiTemplateEditor) openChoice(field int) {
	key := e.dialog.Fields[field].Key
	options := []tuiSelectorOption{}
	if field == 2 {
		for _, p := range e.data.profiles {
			options = append(options, tuiSelectorOption{Label: p.Name, Value: p.Name})
		}
	}
	selector := newTUISelector(e.dialog.Fields[field].Label, []string{e.bindingHints("Choose a value, or {dialog.cancel} to type directly in the form.")}, options, []string{e.value(key)}, false, func(values []string) error {
		if len(values) > 0 {
			e.dialog.SetFieldValue(key, values[0])
		}
		return nil
	})
	selector.Parent = e
	selector.EmptyText = "Loading installed Lima templates…"
	e.app.overlay = selector
	if field != 1 {
		selector.EmptyText = e.bindingHints("No agent profiles available; {dialog.cancel} returns to the form.")
		return
	}
	if e.app.service == nil {
		selector.EmptyText = e.bindingHints("Image list unavailable; {dialog.cancel} returns to the form.")
		return
	}
	ctx, cancel := e.workContext()
	service := e.app.service
	load := func() tuiTemplateImagesEvent {
		defer cancel()
		d := tuiTemplateImagesEvent{editor: e, selector: selector}
		client, ok := service.sandbox.(*LimaClient)
		if !ok {
			d.err = fmt.Errorf("image list unavailable; type a template, path or URL")
			return d
		}
		d.names, d.err = client.TemplateNames(ctx)
		return d
	}
	if e.app.postEvent == nil {
		e.app.applyTemplateImages(load())
		return
	}
	go func() { e.app.postCompletion(load()) }()
}
func (a *vaxisTUIApp) applyTemplateImages(d tuiTemplateImagesEvent) {
	if a.overlay != d.selector || !d.editor.visible() {
		return
	}
	if d.err != nil {
		d.selector.EmptyText = d.err.Error()
		return
	}
	d.selector.EmptyText = a.keyHints("No installed templates; {dialog.cancel} to type a path or URL.")
	for _, name := range d.names {
		d.selector.Options = append(d.selector.Options, tuiSelectorOption{Label: name, Value: "template:" + strings.TrimPrefix(name, "template:")})
	}
}
