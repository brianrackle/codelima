package codelima

import (
	"errors"
	"strings"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/widgets/border"
	"git.sr.ht/~rockorager/vaxis/widgets/textinput"
)

// tuiOverlay is the single active right-pane override (dialog, menu,
// selector, or messages view). At most one overlay is visible at a time; the
// app stores it in vaxisTUIApp.overlay. Update reports done=true when the
// overlay should be dismissed. Update errors are NON-fatal: the app surfaces
// them in the footer status and dismisses the overlay (the historical
// selector/menu policy; dialog Update errors used to abort the whole TUI).
type tuiOverlay interface {
	Update(event vaxis.Event) (done bool, err error)
	Draw(win vaxis.Window, headerStyle, mutedStyle, errorStyle vaxis.Style)
	FooterHint() string
}

type tuiDialogField struct {
	Key      string
	Label    string
	Required bool
	Input    *textinput.Model
	Value    string
	Display  func(string) string
	Activate func() error
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

func newTUISelectorField(key, label, value string, required bool, activate func() error) tuiDialogField {
	return tuiDialogField{
		Key:      key,
		Label:    label,
		Required: required,
		Value:    value,
		Display: func(value string) string {
			values := parseCommaSeparatedValues(value)
			if len(values) == 0 {
				return "none"
			}
			return strings.Join(values, ", ")
		},
		Activate: activate,
	}
}

func newTUIValueSelectorField(key, label, value string, required bool, display func(string) string, activate func() error) tuiDialogField {
	return tuiDialogField{
		Key:      key,
		Label:    label,
		Required: required,
		Value:    value,
		Display:  display,
		Activate: activate,
	}
}

func (f *tuiDialogField) rawValue() string {
	if f.Input != nil {
		return strings.TrimSpace(f.Input.String())
	}
	return strings.TrimSpace(f.Value)
}

func (f *tuiDialogField) renderedValue() string {
	value := f.rawValue()
	if f.Display != nil {
		return f.Display(value)
	}
	return value
}

func (d *tuiDialog) Update(event vaxis.Event) (done bool, err error) {
	switch event := event.(type) {
	case vaxis.PasteStartEvent, vaxis.PasteEndEvent:
		if len(d.Fields) == 0 {
			return false, nil
		}
		d.Fields[d.FieldIndex].Input.Update(event)
		return false, nil
	case vaxis.Key:
		switch {
		case isOverlayCancelKey(event):
			return true, nil
		case event.Matches('s', vaxis.ModCtrl):
			return d.submit()
		case event.MatchString("Tab"), event.MatchString("Down"):
			if len(d.Fields) > 0 {
				d.FieldIndex = (d.FieldIndex + 1) % len(d.Fields)
			}
			return false, nil
		case event.MatchString("Up"):
			if len(d.Fields) > 0 {
				d.FieldIndex--
				if d.FieldIndex < 0 {
					d.FieldIndex = len(d.Fields) - 1
				}
			}
			return false, nil
		case event.MatchString("Right") && len(d.Fields) > 0 && d.Fields[d.FieldIndex].Activate != nil:
			d.Error = ""
			if err := d.Fields[d.FieldIndex].Activate(); err != nil {
				d.Error = err.Error()
			}
			return false, nil
		case event.MatchString("Enter"):
			if len(d.Fields) > 0 && d.Fields[d.FieldIndex].Activate != nil && d.Fields[d.FieldIndex].Input == nil {
				d.Error = ""
				if err := d.Fields[d.FieldIndex].Activate(); err != nil {
					d.Error = err.Error()
				}
				return false, nil
			}
			return d.submit()
		default:
			if len(d.Fields) == 0 {
				return false, nil
			}
			if d.Fields[d.FieldIndex].Input == nil {
				return false, nil
			}
			d.Error = ""
			d.Fields[d.FieldIndex].Input.Update(event)
			return false, nil
		}
	default:
		return false, nil
	}
}

func (d *tuiDialog) submit() (done bool, err error) {
	values, validationErr := d.Values()
	if validationErr != nil {
		d.Error = validationErr.Error()
		return false, nil
	}
	if d.OnSubmit == nil {
		return true, nil
	}
	if err := d.OnSubmit(values); err != nil {
		d.Error = err.Error()
		return false, nil
	}
	return true, nil
}

func (d *tuiDialog) FooterHint() string {
	return "Tab/Up/Down move   Enter submit/open   Right choose   Ctrl+s submit   Esc cancel   Ctrl+c quit"
}

func (d *tuiDialog) Values() (map[string]string, error) {
	values := make(map[string]string, len(d.Fields))
	for _, field := range d.Fields {
		value := field.rawValue()
		if field.Required && value == "" {
			return nil, errors.New(field.Label + " is required")
		}
		values[field.Key] = value
	}
	return values, nil
}

func (d *tuiDialog) SetFieldValue(key, value string) {
	for index := range d.Fields {
		if d.Fields[index].Key != key {
			continue
		}
		if d.Fields[index].Input != nil {
			d.Fields[index].Input.SetContent(value)
		} else {
			d.Fields[index].Value = value
		}
		return
	}
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
		if field.Input != nil {
			field.Input.HideCursor = index != d.FieldIndex
			field.Input.Draw(inputWin)
		} else {
			style := vaxis.Style{}
			if index == d.FieldIndex {
				style.Attribute = vaxis.AttrReverse
			}
			inputWin.Println(0, vaxis.Segment{Text: field.renderedValue(), Style: style})
		}
		row += 2
	}

	body.Println(row, vaxis.Segment{Text: "Enter submit/open  Right choose  Tab/Up/Down move  Ctrl+s submit  Esc cancel", Style: mutedStyle})
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

func (m *tuiMenu) Update(event vaxis.Event) (done bool, err error) {
	key, ok := event.(vaxis.Key)
	if !ok {
		return false, nil
	}
	if isOverlayCancelKey(key) {
		return true, nil
	}

	pressed := []rune(strings.ToLower(key.Text))
	if len(pressed) == 0 {
		return false, nil
	}
	for _, entry := range m.Entries {
		if entry.Key != pressed[0] {
			continue
		}
		if entry.Action != nil {
			if err := entry.Action(); err != nil {
				return false, err
			}
		}
		return true, nil
	}

	return false, nil
}

func (m *tuiMenu) FooterHint() string {
	return "Press a key to choose   Esc cancel   Ctrl+c quit"
}

func (m *tuiMenu) Draw(win vaxis.Window, headerStyle, mutedStyle, _ vaxis.Style) {
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

type tuiSelectorOption struct {
	Label string
	Value string
}

type tuiSelector struct {
	Title       string
	Description []string
	Options     []tuiSelectorOption
	Index       int
	Scroll      int
	Multi       bool
	Selected    map[string]bool
	OnSubmit    func(values []string) error
	// EmptyText is rendered when Options is empty; callers supply the
	// domain-appropriate wording (it used to be a hardcoded environment-config
	// message regardless of what the selector listed).
	EmptyText string
	// Parent, when non-nil, is the overlay restored after this selector is
	// dismissed. Field pickers opened from a dialog set it so the dialog
	// reappears on completion or cancel (see vaxisTUIApp.showSelector).
	Parent tuiOverlay
}

func newTUISelector(title string, description []string, options []tuiSelectorOption, selected []string, multi bool, onSubmit func(values []string) error) *tuiSelector {
	selector := &tuiSelector{
		Title:       title,
		Description: append([]string(nil), description...),
		Options:     append([]tuiSelectorOption(nil), options...),
		Multi:       multi,
		Selected:    map[string]bool{},
		OnSubmit:    onSubmit,
	}
	for _, value := range selected {
		if strings.TrimSpace(value) == "" {
			continue
		}
		selector.Selected[value] = true
	}
	for index, option := range selector.Options {
		if selector.Selected[option.Value] {
			selector.Index = index
			break
		}
	}
	return selector
}

func (s *tuiSelector) Update(event vaxis.Event) (done bool, err error) {
	key, ok := event.(vaxis.Key)
	if !ok {
		return false, nil
	}
	if isOverlayCancelKey(key) {
		return true, nil
	}

	switch {
	case key.MatchString("Tab"), key.MatchString("Down"):
		s.move(1)
		return false, nil
	case key.MatchString("Up"):
		s.move(-1)
		return false, nil
	case s.Multi && key.Matches('u', vaxis.ModCtrl):
		s.Selected = map[string]bool{}
		return false, nil
	case s.Multi && key.Text == " ":
		s.toggleCurrent()
		return false, nil
	case key.MatchString("Enter"):
		values := s.selectedValues()
		if !s.Multi && len(values) == 0 && len(s.Options) > 0 {
			values = []string{s.Options[s.Index].Value}
		}
		if s.OnSubmit != nil {
			if err := s.OnSubmit(values); err != nil {
				return false, err
			}
		}
		return true, nil
	default:
		return false, nil
	}
}

func (s *tuiSelector) move(delta int) {
	if len(s.Options) == 0 {
		s.Index = 0
		return
	}
	s.Index += delta
	if s.Index < 0 {
		s.Index = len(s.Options) - 1
	}
	if s.Index >= len(s.Options) {
		s.Index = 0
	}
}

func (s *tuiSelector) toggleCurrent() {
	if len(s.Options) == 0 {
		return
	}
	value := s.Options[s.Index].Value
	if s.Selected[value] {
		delete(s.Selected, value)
		return
	}
	s.Selected[value] = true
}

func (s *tuiSelector) selectedValues() []string {
	if !s.Multi {
		if len(s.Options) == 0 {
			return []string{}
		}
		return []string{s.Options[s.Index].Value}
	}

	values := make([]string, 0, len(s.Selected))
	for _, option := range s.Options {
		if !s.Selected[option.Value] {
			continue
		}
		values = append(values, option.Value)
	}
	return values
}

func (s *tuiSelector) FooterHint() string {
	if s.Multi {
		return "Up/Down move   Space toggle   Enter confirm   Ctrl+u clear   Esc cancel   Ctrl+c quit"
	}
	return "Up/Down move   Enter confirm   Esc cancel   Ctrl+c quit"
}

func (s *tuiSelector) Draw(win vaxis.Window, headerStyle, mutedStyle, _ vaxis.Style) {
	body := border.All(win, mutedStyle)
	body.Println(0, vaxis.Segment{Text: s.Title, Style: headerStyle})

	row := 1
	for _, line := range s.Description {
		body.Println(row, vaxis.Segment{Text: line, Style: mutedStyle})
		row++
	}

	_, height := body.Size()
	footerRows := 2
	visibleRows := height - row - footerRows
	if visibleRows < 1 {
		visibleRows = 1
	}
	if s.Index < s.Scroll {
		s.Scroll = s.Index
	}
	if s.Index >= s.Scroll+visibleRows {
		s.Scroll = s.Index - visibleRows + 1
	}

	if len(s.Options) == 0 {
		if s.EmptyText != "" {
			body.Println(row, vaxis.Segment{Text: s.EmptyText, Style: mutedStyle})
		}
	} else {
		end := s.Scroll + visibleRows
		if end > len(s.Options) {
			end = len(s.Options)
		}
		for index := s.Scroll; index < end; index++ {
			option := s.Options[index]
			label := option.Label
			if s.Multi {
				check := "[ ]"
				if s.Selected[option.Value] {
					check = "[x]"
				}
				label = check + " " + label
			}
			if index == s.Index {
				label = "> " + label
				body.Println(row, vaxis.Segment{Text: label, Style: vaxis.Style{Attribute: vaxis.AttrReverse}})
			} else {
				body.Println(row, vaxis.Segment{Text: "  " + label})
			}
			row++
		}
	}

	help := "Up/Down move  Enter confirm  Esc cancel"
	if s.Multi {
		help = "Up/Down move  Space toggle  Enter confirm  Ctrl+u clear  Esc cancel"
	}
	body.Println(height-2, vaxis.Segment{Text: help, Style: mutedStyle})
}

func isOverlayCancelKey(key vaxis.Key) bool {
	return key.Matches(vaxis.KeyEsc) || key.Matches('[', vaxis.ModCtrl)
}
