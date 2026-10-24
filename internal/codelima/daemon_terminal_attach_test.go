//go:build cgo && (darwin || linux)

package codelima

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func TestRawAttachmentStreamsLivePTYAndDetachPreservesProcess(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	terminal := newIsolatedDaemonTerminalWithOptions("raw-pty", nil, rendererProcessOptions{
		Executable: executable, Args: []string{"-test.run=^TestRendererWorkerProcess$"},
		Env: []string{rendererWorkerHelperEnv + "=1"}, CommandTimeout: time.Second,
	})
	if err := terminal.Start(exec.Command("/bin/sh", "-c", "stty -echo; printf ready; while IFS= read -r line; do printf 'received:%s\\n' \"$line\"; done")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(terminal.Close)
	waitForCondition(t, 5*time.Second, func() bool { return strings.Contains(terminal.ReadVisible(ReadText).Text, "ready") }, "initial raw screen")
	peerDone := make(chan struct{})
	initial, err := terminal.AttachRaw(context.Background(), daemon.ClientContext{ID: "owner", Done: peerDone})
	if err != nil || !strings.Contains(string(initial.Data), "ready") {
		t.Fatalf("attach = %+v, %v", initial, err)
	}
	params := daemon.TerminalAttachParams{AttachmentID: initial.AttachmentID, After: initial.Through}
	terminal.SendInput([]byte("first\n"))
	var output strings.Builder
	waitForCondition(t, 5*time.Second, func() bool {
		batch, readErr := terminal.ReadRaw("owner", params)
		if readErr != nil {
			t.Fatal(readErr)
		}
		output.Write(batch.Data)
		params.After = batch.Through
		return strings.Contains(output.String(), "received:first")
	}, "raw PTY output")
	if err := terminal.DetachRaw("owner", params); err != nil {
		t.Fatal(err)
	}
	terminal.SendInput([]byte("detached\n"))
	waitForCondition(t, 5*time.Second, func() bool { return strings.Contains(terminal.ReadVisible(ReadText).Text, "received:detached") }, "PTY surviving detach")
	second, err := terminal.AttachRaw(context.Background(), daemon.ClientContext{ID: "owner", Done: peerDone})
	if err != nil {
		t.Fatal(err)
	}
	close(peerDone)
	select {
	case <-terminal.RawAttachmentDone(second.AttachmentID):
	case <-time.After(2 * time.Second):
		t.Fatal("disconnected attachment retained its query lease")
	}
}
