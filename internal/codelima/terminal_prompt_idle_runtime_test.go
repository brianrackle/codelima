//go:build cgo && (darwin || linux)

package codelima

import (
	"os/exec"
	"testing"
	"time"

	"go.rockorager.dev/vaxis"
)

func TestIsolatedTerminalPromptIdleCountsOnlyAcceptedInput(t *testing.T) {
	term := newLazyVariantTestTerminal(t, "prompt-input")
	if err := term.Start(exec.Command("/bin/sh", "-c", "sleep 30")); err != nil {
		t.Fatal(err)
	}
	baseline := time.Now().Add(-time.Minute).UnixMilli()
	term.lastInput.Store(baseline)
	term.SendInput(nil)
	term.applyRendererPTYWrite(1, 1, 1, []byte("query-response"))
	if got := term.LastInputUnixMilli(); got != baseline {
		t.Fatal("protocol/empty write counted as user input", got)
	}
	for _, send := range []func(){
		func() { term.SendInput([]byte("raw")) },
		func() { term.applyRendererPTYWrite(1, rendererInputEventID(42), 1, []byte("encoded")) },
		func() { term.Update(vaxis.Key{Keycode: 'a', Text: "a"}) },
		func() {
			if err := term.Paste("paste"); err != nil {
				t.Fatal(err)
			}
		},
	} {
		term.lastInput.Store(baseline)
		send()
		waitForCondition(t, 3*time.Second, func() bool { return term.LastInputUnixMilli() > baseline }, "accepted input timestamp")
	}
	term.lastInput.Store(baseline)
	term.applyRendererPTYWrite(1, rendererInputEventID(42), 1, []byte("duplicate"))
	if got := term.LastInputUnixMilli(); got != baseline {
		t.Fatal("replayed input counted twice", got)
	}
	term.Close()
	term.SendInput([]byte("closed"))
	if got := term.LastInputUnixMilli(); got != baseline {
		t.Fatal("closed writer counted input", got)
	}
}
