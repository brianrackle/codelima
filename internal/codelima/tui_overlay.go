package codelima

import (
	"errors"
	"strings"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/widgets/border"
	"git.sr.ht/~rockorager/vaxis/widgets/textinput"
)

type tuiDialogField struct {
	Key      string
	Label    string
	Required bool
	Input    *textinput.Model
}

type tuiDialog struct {
	Title       string
	Description []string
	SubmitLabel string
	Fields      []tuiDialogField
	FieldIndex  int
	Error       string
	OnSubmit    func(values map[string]string) error
}

func newTUIDialog(title string, submitLabel string, description []string, fields []tuiDialogField, onSubmit func(values map[string]string) error) *tuiDialog {
	return &tuiDialog{
		Title:       title,
		Description: append([]string(nil), description...),
		SubmitLabel: submitLabel,
		Fields:      fields,
		OnSubmit:    onSubmit,
	}
}

func newTUIInputField(key, label, value string, required bool) tuiDialogField {
	input := textinput.New()
	input.SetContent(value)
	return tuiDialogField{
		Key:      key,
		Label:    label,
		Required: required,
		Input:    input,
	}
}

func (d *tuiDialog) Update(event vaxis.Event) (completed bool, cancelled bool, err error) {
	switch event := event.(type) {
	case vaxis.PasteStartEvent, vaxis.PasteEndEvent:
		if len(d.Fields) == 0 {
			return false, false, nil
		}
		d.Fields[d.FieldIndex].Input.Update(event)
		return false, false, nil
	case vaxis.Key:
		switch {
		case isOverlayCancelKey(event):
			return false, true, nil
		case event.MatchString("Tab"), event.MatchString("Down"):
			if len(d.Fields) > 0 {
				d.FieldIndex = (d.FieldIndex + 1) % len(d.Fields)
			}
			return false, false, nil
		case event.MatchString("Up"):
			if len(d.Fields) > 0 {
				d.FieldIndex--
				if d.FieldIndex < 0 {
					d.FieldIndex = len(d.Fields) - 1
				}
			}
			return false, false, nil
		case event.MatchString("Enter"):
			values, validationErr := d.Values()
			if validationErr != nil {
				d.Error = validationErr.Error()
				return false, false, nil
			}
			if d.OnSubmit == nil {
				return true, false, nil
			}
			if err := d.OnSubmit(values); err != nil {
				d.Error = err.Error()
				return false, false, nil
			}
			return true, false, nil
		default:
			if len(d.Fields) == 0 {
				return false, false, nil
			}
			d.Error = ""
			d.Fields[d.FieldIndex].Input.Update(event)
			return false, false, nil
		}
	default:
		return false, false, nil
	}
}

func (d *tuiDialog) Values() (map[string]string, error) {
	values := make(map[string]string, len(d.Fields))
	for _, field := range d.Fields {
		value := strings.TrimSpace(field.Input.String())
		if field.Required && value == "" {
			return nil, errors.New(field.Label + " is required")
		}
		values[field.Key] = value
	}
	return values, nil
}

func (d *tuiDialog) Draw(win vaxis.Window, headerStyle, mutedStyle, errorStyle vaxis.Style) {
	body := border.All(win, mutedStyle)
	body.Println(0, vaxis.Segment{Text: d.Title, Style: headerStyle})

	row := 1
	for _, line := range d.Description {
		body.Println(row, vaxis.Segment{Text: line, Style: mutedStyle})
		row++
	}

	if d.Error != "" {
		body.Println(row, vaxis.Segment{Text: d.Error, Style: errorStyle})
		row++
	}

	for index, field := range d.Fields {
		body.Println(row, vaxis.Segment{Text: field.Label, Style: mutedStyle})
		row++
		inputWin := body.New(0, row, -1, 1)
		field.Input.HideCursor = index != d.FieldIndex
		field.Input.Draw(inputWin)
		row += 2
	}

	body.Println(row, vaxis.Segment{Text: "Enter submit  Tab/Up/Down move  Esc cancel  Ctrl+c quit", Style: mutedStyle})
}

type tuiMenuEntry struct {
	Key    rune
	Label  string
	Action func() error
}

type tuiMenu struct {
	Title       string
	Description []string
	Entries     []tuiMenuEntry
}

func (m *tuiMenu) Update(event vaxis.Event) (completed bool, cancelled bool, err error) {
	key, ok := event.(vaxis.Key)
	if !ok {
		return false, false, nil
	}
	if isOverlayCancelKey(key) {
		return false, true, nil
	}

	pressed := []rune(strings.ToLower(key.Text))
	if len(pressed) == 0 {
		return false, false, nil
	}
	for _, entry := range m.Entries {
		if entry.Key != pressed[0] {
			continue
		}
		if entry.Action != nil {
			if err := entry.Action(); err != nil {
				return false, false, err
			}
		}
		return true, false, nil
	}

	return false, false, nil
}

func (m *tuiMenu) Draw(win vaxis.Window, headerStyle, mutedStyle vaxis.Style) {
	body := border.All(win, mutedStyle)
	body.Println(0, vaxis.Segment{Text: m.Title, Style: headerStyle})

	row := 1
	for _, line := range m.Description {
		body.Println(row, vaxis.Segment{Text: line, Style: mutedStyle})
		row++
	}

	for _, entry := range m.Entries {
		body.Println(row, vaxis.Segment{Text: "[" + string(entry.Key) + "] " + entry.Label})
		row++
	}

	body.Println(row+1, vaxis.Segment{Text: "Press a key to choose or Esc to cancel. Ctrl+c quits.", Style: mutedStyle})
}

func isOverlayCancelKey(key vaxis.Key) bool {
	return key.Matches(vaxis.KeyEsc) || key.Matches('[', vaxis.ModCtrl)
}
