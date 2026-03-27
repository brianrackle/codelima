package codelima

import (
	"os/exec"
	"reflect"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/widgets/term"
)

const tuiEmbeddedTermEnv = "xterm-256color"

// Advertise a conservative terminal type inside the embedded shell session.
// The richer xterm-kitty terminfo target is not fully emulated by the Vaxis
// fallback widget and can provoke incompatible redraw behavior in interactive
// programs such as apt/dpkg progress views.
func newTUITerminal(nodeID string, postEvent func(vaxis.Event)) tuiTerminal {
	if terminal, err := newGhosttyTUITerminal(nodeID, postEvent); err == nil {
		return terminal
	}
	return newTUIVaxisTerminal(nodeID, postEvent)
}

func newTUIVaxisTerminal(nodeID string, postEvent func(vaxis.Event)) tuiTerminal {
	model := term.New()
	model.TERM = tuiEmbeddedTermEnv
	model.Attach(func(event vaxis.Event) {
		switch event := event.(type) {
		case term.EventClosed:
			postEvent(tuiTerminalClosedEvent{NodeID: nodeID, Err: event.Error})
		case term.EventPanic:
			postEvent(tuiTerminalErrorEvent{NodeID: nodeID, Err: error(event)})
		default:
			postEvent(event)
		}
	})
	return &vaxisTUITerminal{model: model}
}

type vaxisTUITerminal struct {
	model       *term.Model
	started     bool
	pendingCols int
	pendingRows int
}

func (t *vaxisTUITerminal) Start(cmd *exec.Cmd) error {
	var err error
	if t.pendingCols > 0 && t.pendingRows > 0 {
		err = t.model.StartWithSize(cmd, t.pendingCols, t.pendingRows)
	} else {
		err = t.model.Start(cmd)
	}
	if err != nil {
		return err
	}
	t.started = true
	return nil
}

func (t *vaxisTUITerminal) Resize(width, height int) {
	if t == nil || t.model == nil || width <= 0 || height <= 0 {
		return
	}

	t.pendingCols = width
	t.pendingRows = height
	if t.started {
		t.model.Resize(width, height)
	}
}

func (t *vaxisTUITerminal) Update(event vaxis.Event) {
	t.model.Update(event)
}

func (t *vaxisTUITerminal) Draw(win vaxis.Window) {
	t.model.Draw(win)
}

func (t *vaxisTUITerminal) Close() {
	t.model.Close()
}

func (t *vaxisTUITerminal) Focus() {
	t.model.Focus()
}

func (t *vaxisTUITerminal) Blur() {
	t.model.Blur()
}

func (t *vaxisTUITerminal) String() string {
	return t.model.String()
}

func (t *vaxisTUITerminal) TermEnv() string {
	return t.model.TERM
}

func (t *vaxisTUITerminal) HyperlinkAt(int, int) (string, bool) {
	return "", false
}

func (t *vaxisTUITerminal) CapturesMouse() bool {
	if t == nil || t.model == nil {
		return false
	}

	value := reflect.ValueOf(t.model)
	if !value.IsValid() || value.IsNil() {
		return false
	}

	mode := value.Elem().FieldByName("mode")
	if !mode.IsValid() {
		return false
	}

	return vaxisTerminalModeField(mode, "mouseButtons") ||
		vaxisTerminalModeField(mode, "mouseDrag") ||
		vaxisTerminalModeField(mode, "mouseMotion") ||
		vaxisTerminalModeField(mode, "mouseSGR")
}

func vaxisTerminalModeField(mode reflect.Value, name string) bool {
	field := mode.FieldByName(name)
	if !field.IsValid() || field.Kind() != reflect.Bool {
		return false
	}
	return field.Bool()
}
