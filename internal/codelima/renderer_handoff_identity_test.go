//go:build darwin || linux

package codelima

import (
	"os"
	"testing"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"go.rockorager.dev/vaxis"
)

func TestDaemonHandoffAdoptionUsesRendererTerminalIdentityNotTabID(t *testing.T) {
	service, _ := newHandoffTestService(t)
	host := newDaemonHost(service)
	t.Cleanup(func() { _ = host.Close() })
	ids := []string{"renderer-terminal"}
	manifest := handoffManifestFor(ids, "identity-test")
	if manifest.Session.Terminals[0].TabID == ids[0] {
		t.Fatal("fixture must distinguish terminal and tab identities")
	}
	journal := newRendererJournal(1024)
	journal.AppendResize(80, 24)
	checkpoint, err := newRendererCheckpoint(rendererNativeBuildIdentity(), ids[0], 1, 1, false, terminalCheckpointState{Data: []byte("native"), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := encodeRendererRecovery(ids[0], checkpoint, journal.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	manifest.Runtimes[0].Recovery = recovery
	fds := handoffFDs(t, ids)
	t.Cleanup(func() {
		for _, fd := range fds {
			daemon.CloseHandoffFDs([]int{fd})
		}
	})
	adopt := func(identity string, _ func(vaxis.Event), pty *os.File, _, _, _ int, _ []byte, _ bool, recovery []byte) (daemonTerminal, error) {
		// The real adopter validates the same envelope before restoring the
		// native worker. A TabID here reproduces the live-update rejection.
		if _, err := decodeRendererRecovery(identity, recovery); err != nil {
			return nil, err
		}
		return adoptFakeHandoffTerminal(pty), nil
	}
	if _, err := host.adoptHandoffTerminals(manifest, fds, adopt); err != nil {
		t.Fatalf("handoff changed the renderer's identity: %v", err)
	}
	if entry, err := host.lookup(ids[0]); err != nil || entry.state.TabID != manifest.Session.Terminals[0].TabID {
		t.Fatalf("adoption lost separate tab presentation identity: entry=%+v err=%v", entry, err)
	}
}
