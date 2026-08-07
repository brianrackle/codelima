package codelima

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
)

// fakeHandoffTerminal is a daemon terminal that can be quiesced, handed off and
// adopted without a renderer worker process. Every observed field is guarded
// because publisher goroutines and simulated runtime callbacks touch these
// terminals concurrently under -race.
type fakeHandoffTerminal struct {
	mu         sync.Mutex
	pty        *os.File
	peer       *os.File
	closed     bool
	released   bool
	rolledBack bool
	activated  bool
}

func newFakeHandoffTerminal() *fakeHandoffTerminal { return &fakeHandoffTerminal{} }

// adoptFakeHandoffTerminal is the importing-side counterpart: it takes ownership
// of the transferred descriptor the way an adopted runtime does.
func adoptFakeHandoffTerminal(pty *os.File) *fakeHandoffTerminal {
	return &fakeHandoffTerminal{pty: pty}
}

func (*fakeHandoffTerminal) Start(*exec.Cmd) error               { return nil }
func (*fakeHandoffTerminal) Resize(int, int)                     {}
func (*fakeHandoffTerminal) Update(vaxis.Event)                  {}
func (*fakeHandoffTerminal) Draw(vaxis.Window)                   {}
func (*fakeHandoffTerminal) Focus()                              {}
func (*fakeHandoffTerminal) Blur()                               {}
func (*fakeHandoffTerminal) String() string                      { return "" }
func (*fakeHandoffTerminal) TermEnv() string                     { return tuiEmbeddedTermEnv }
func (*fakeHandoffTerminal) HyperlinkAt(int, int) (string, bool) { return "", false }
func (*fakeHandoffTerminal) CapturesMouse() bool                 { return false }
func (*fakeHandoffTerminal) ReadVisible(ReadFormat) ReadResult   { return ReadResult{} }
func (*fakeHandoffTerminal) ReadRecent(ReadFormat) ReadResult    { return ReadResult{} }
func (*fakeHandoffTerminal) Snapshot() SnapshotResult            { return SnapshotResult{} }
func (*fakeHandoffTerminal) Scroll(int)                          {}
func (*fakeHandoffTerminal) SendInput([]byte)                    {}

func (t *fakeHandoffTerminal) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	if t.pty != nil {
		_ = t.pty.Close()
		t.pty = nil
	}
	if t.peer != nil {
		_ = t.peer.Close()
		t.peer = nil
	}
}

func (t *fakeHandoffTerminal) BeginHandoff() handoffTerminalState {
	read, write, err := os.Pipe()
	if err != nil {
		return handoffTerminalState{Err: err}
	}
	t.mu.Lock()
	t.pty, t.peer = read, write
	t.mu.Unlock()
	return handoffTerminalState{
		PTY:      read,
		ChildPID: os.Getpid(),
		Cols:     80,
		Rows:     24,
		Replay:   []byte("handoff-replay"),
	}
}

func (t *fakeHandoffTerminal) ReleaseAfterHandoff() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.released = true
	if t.peer != nil {
		_ = t.peer.Close()
		t.peer = nil
	}
}

func (t *fakeHandoffTerminal) RollbackHandoff() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rolledBack = true
	return nil
}

func (t *fakeHandoffTerminal) ActivateAfterHandoff() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.activated = true
}

// newDaemonTestRoot returns a unique directory under the repository tmp tree,
// removed when the test ends. Daemon tests cannot use t.TempDir, whose paths are
// long enough to exceed the Unix socket limit once a daemon or handoff socket is
// appended. They must not key the name on newID either: newID returns a UUIDv7,
// whose leading characters are a coarse timestamp shared by everything created
// in the same minute, so two tests would share a root and then delete each
// other's while it was still in use.
func newDaemonTestRoot(t *testing.T, prefix string) string {
	t.Helper()
	parent := filepath.Join("..", "..", "tmp")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	relative, err := os.MkdirTemp(parent, prefix)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(relative)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

// newHandoffTestService builds a service whose metadata root is short enough for
// a Unix socket path on every supported platform; the live-update handshake
// binds one inside the daemon home.
func newHandoffTestService(t *testing.T) (*Service, string) {
	t.Helper()
	root := newDaemonTestRoot(t, "hof-")
	home := filepath.Join(root, "home")
	workspace := filepath.Join(root, "work")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig(home)
	cfg.MetadataRoot = home
	cfg.AgentProfilesDir = filepath.Join(home, "_config", "agent-profiles")
	service := NewService(cfg, newFakeSandbox(), strings.NewReader(""), ioDiscard{}, ioDiscard{})
	service.localTerminals = true
	if err := service.ensureDirectories(); err != nil {
		t.Fatal(err)
	}
	return service, workspace
}

func waitForHandoff(t *testing.T, timeout time.Duration, fn func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func readPersistedSession(t *testing.T, path string) daemon.Session {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted session: %v", err)
	}
	var session daemon.Session
	if err := json.Unmarshal(data, &session); err != nil {
		t.Fatalf("decode persisted session: %v", err)
	}
	return session
}

// updateHandoffHarness drives daemonHost.update from the successor's side. The
// real update spawns the new binary and waits for it on a Unix socket, so the
// stub binary only records its argv and the test itself plays the importing
// daemon over the real handoff protocol.
type updateHandoffHarness struct {
	service  *Service
	host     *daemonHost
	node     Node
	binary   string
	argsPath string
	session  string
	socket   string
}

func newUpdateHandoffHarness(t *testing.T) *updateHandoffHarness {
	t.Helper()
	service, workspace := newHandoffTestService(t)
	now := time.Now().UTC()
	node := Node{
		ID:            newID(),
		Slug:          "handoff-node",
		SandboxName:   "handoff-node",
		DirectoryPath: workspace,
		Status:        NodeStatusStopped,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := service.store.SaveNode(node, BootstrapState{}); err != nil {
		t.Fatalf("SaveNode() error = %v", err)
	}

	paths := daemon.HomePaths(service.cfg.MetadataRoot)
	argsPath := filepath.Join(filepath.Dir(service.cfg.MetadataRoot), "import-argv")
	binary := filepath.Join(filepath.Dir(service.cfg.MetadataRoot), "codelima-successor")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\n", argsPath)
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	host := newDaemonHost(service)
	host.terminalFactory = func(string, func(vaxis.Event)) tuiTerminal { return newFakeHandoffTerminal() }
	host.prepareReplacement = func() error { return nil }
	host.resumeReplacement = func() error { return nil }
	host.stopServer = func() {}
	host.terminalCloseTimeout = time.Second
	t.Cleanup(func() { _ = host.Close() })

	return &updateHandoffHarness{
		service:  service,
		host:     host,
		node:     node,
		binary:   binary,
		argsPath: argsPath,
		session:  paths.Session,
		socket:   filepath.Join(paths.Dir, fmt.Sprintf("handoff-%d.sock", os.Getpid())),
	}
}

func (h *updateHandoffHarness) openTab(t *testing.T, terminalID string) daemon.TerminalState {
	t.Helper()
	state, err := h.host.open(context.Background(), terminalOpenParams{
		Target: "node:" + h.node.ID,
		Kind:   "node-host-shell",
		Cols:   80,
		Rows:   24,
	}, terminalID)
	if err != nil {
		t.Fatalf("terminal.open(%s) error = %v", terminalID, err)
	}
	return state
}

// startUpdate runs the live update on its own goroutine, because the caller
// plays the successor daemon it is waiting for.
func (h *updateHandoffHarness) startUpdate() <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := h.host.update(h.binary)
		done <- err
	}()
	return done
}

// waitForHandoffToken reads the token the update generated out of the stub
// binary's recorded argv, then connects to the handoff socket.
func (h *updateHandoffHarness) dialSuccessor(t *testing.T) (*daemon.HandoffConnection, string) {
	t.Helper()
	var token string
	waitForHandoff(t, 10*time.Second, func() bool {
		file, err := os.Open(h.argsPath)
		if err != nil {
			return false
		}
		defer func() { _ = file.Close() }()
		var argv []string
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			argv = append(argv, scanner.Text())
		}
		index := slices.Index(argv, "--token")
		if index < 0 || index+1 >= len(argv) {
			return false
		}
		token = argv[index+1]
		return token != ""
	}, "the successor binary to receive a handoff token")

	var peer *daemon.HandoffConnection
	waitForHandoff(t, 10*time.Second, func() bool {
		connection, err := daemon.DialHandoff(h.socket)
		if err != nil {
			return false
		}
		peer = connection
		return true
	}, "the handoff listener to accept a connection")
	t.Cleanup(func() { _ = peer.Close() })
	return peer, token
}

// receiveHandoff plays the importing daemon up to the point where the manifest,
// replay and descriptors have all been received.
func (h *updateHandoffHarness) receiveHandoff(t *testing.T, peer *daemon.HandoffConnection, token string) daemon.HandoffManifest {
	t.Helper()
	if err := peer.WriteJSON(daemon.HandoffMessage{Type: "hello", Token: token}, nil); err != nil {
		t.Fatalf("write handoff hello: %v", err)
	}
	manifest, err := peer.ReadManifest()
	if err != nil {
		t.Fatalf("read handoff manifest: %v", err)
	}
	for index := range manifest.Runtimes {
		runtimeState := &manifest.Runtimes[index]
		replay, readErr := peer.ReadReplay(runtimeState.TerminalID, runtimeState.ReplaySize)
		if readErr != nil {
			t.Fatalf("read handoff replay: %v", readErr)
		}
		runtimeState.Replay = replay
	}
	received := 0
	for received < len(manifest.Runtimes) {
		message, fds, readErr := peer.ReadMessage()
		if readErr != nil {
			t.Fatalf("read handoff fd batch: %v", readErr)
		}
		if message.Type != "fds" || len(fds) != len(message.TerminalIDs) {
			daemon.CloseHandoffFDs(fds)
			t.Fatalf("handoff fd batch = %#v with %d descriptors", message, len(fds))
		}
		daemon.CloseHandoffFDs(fds)
		received += len(fds)
	}
	return manifest
}

// A live update must not leave session.json describing an empty tab set. The old
// daemon deletes every handed-off terminal when the successor commits, and its
// own shutdown persist used to run afterwards: a crash in that window lost every
// tab even though the tabs themselves survived the handoff.
func TestDaemonLiveUpdateKeepsSessionThroughOldDaemonShutdown(t *testing.T) {
	harness := newUpdateHandoffHarness(t)
	first := harness.openTab(t, "term-first")
	second := harness.openTab(t, "term-second")
	want := []string{first.TerminalID, second.TerminalID}
	if got := terminalStateIDs(readPersistedSession(t, harness.session).Terminals); !slices.Equal(got, want) {
		t.Fatalf("session before update = %v, want %v", got, want)
	}

	done := harness.startUpdate()
	peer, token := harness.dialSuccessor(t)
	manifest := harness.receiveHandoff(t, peer, token)
	if got := terminalStateIDs(manifest.Session.Terminals); !slices.Equal(got, want) {
		t.Fatalf("handoff manifest = %v, want every tab %v", got, want)
	}
	if err := peer.WriteJSON(daemon.HandoffMessage{Type: "ready"}, nil); err != nil {
		t.Fatalf("write handoff ready: %v", err)
	}
	message, err := peer.ReadControlMessage()
	if err != nil || message.Type != "commit" {
		t.Fatalf("handoff commit = %#v, %v", message, err)
	}
	// The successor persists the adopted tab set before it reports the commit;
	// runDaemonImport does this for real, and this stands in for it here.
	data, err := json.MarshalIndent(daemon.Session{Version: daemon.SessionVersion, Terminals: manifest.Session.Terminals}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(harness.session, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := peer.WriteJSON(daemon.HandoffMessage{Type: "committed", PID: os.Getpid()}, nil); err != nil {
		t.Fatalf("write handoff committed: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon.update error = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("daemon.update did not finish after the successor committed")
	}

	// The crash window: the replaced daemon now shuts down. Its recovery intent
	// belongs to the successor, so shutdown must leave that file alone.
	if err := harness.host.Close(); err != nil {
		t.Fatalf("daemonHost.Close() error = %v", err)
	}
	if got := terminalStateIDs(readPersistedSession(t, harness.session).Terminals); !slices.Equal(got, want) {
		t.Fatalf("session after the replaced daemon exited = %v, want the handed-off tabs %v", got, want)
	}

	// Any late mutation on the replaced daemon is refused rather than persisted.
	if err := harness.host.close(first.TerminalID); err == nil {
		t.Fatal("terminal.close succeeded on a daemon that was already replaced")
	}
	if got := terminalStateIDs(readPersistedSession(t, harness.session).Terminals); !slices.Equal(got, want) {
		t.Fatalf("session after a late mutation = %v, want %v", got, want)
	}
}

// terminal.open and daemon.update are both control-class requests and dispatch
// concurrently, so an open can arrive while an update is quiescing. The update
// barrier holds it until the update resolves; because this update commits, the
// open is rejected cleanly instead of creating a tab in a daemon that has
// already handed everything over.
func TestDaemonLiveUpdateBarrierRejectsTerminalOpenAfterCommit(t *testing.T) {
	harness := newUpdateHandoffHarness(t)
	existing := harness.openTab(t, "term-existing")

	done := harness.startUpdate()
	peer, token := harness.dialSuccessor(t)

	opened := make(chan error, 1)
	go func() {
		_, err := harness.host.open(context.Background(), terminalOpenParams{
			Target: "node:" + harness.node.ID,
			Kind:   "node-host-shell",
			Cols:   80,
			Rows:   24,
		}, "term-late")
		opened <- err
	}()
	select {
	case err := <-opened:
		t.Fatalf("terminal.open resolved during a live update (error = %v); the tab would be outside the manifest", err)
	case <-time.After(250 * time.Millisecond):
	}

	manifest := harness.receiveHandoff(t, peer, token)
	if got := terminalStateIDs(manifest.Session.Terminals); !slices.Equal(got, []string{existing.TerminalID}) {
		t.Fatalf("handoff manifest = %v, want only %v", got, existing.TerminalID)
	}
	if err := peer.WriteJSON(daemon.HandoffMessage{Type: "ready"}, nil); err != nil {
		t.Fatalf("write handoff ready: %v", err)
	}
	message, err := peer.ReadControlMessage()
	if err != nil || message.Type != "commit" {
		t.Fatalf("handoff commit = %#v, %v", message, err)
	}
	if err := peer.WriteJSON(daemon.HandoffMessage{Type: "committed", PID: os.Getpid()}, nil); err != nil {
		t.Fatalf("write handoff committed: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon.update error = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("daemon.update did not finish after the successor committed")
	}

	select {
	case err := <-opened:
		var rpcErr *daemon.RPCError
		if !errors.As(err, &rpcErr) {
			t.Fatalf("held terminal.open error = %T %v, want a typed daemon error", err, err)
		}
		if rpcErr.Category != "PreconditionFailed" || rpcErr.Fields["daemon_replaced"] != true {
			t.Fatalf("held terminal.open error = %#v, want a retryable replacement rejection", rpcErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("terminal.open stayed blocked after the live update resolved")
	}
	if states := harness.host.list(); len(states) != 0 {
		t.Fatalf("replaced daemon still owns %v", terminalStateIDs(states))
	}
}

// The other half of the barrier contract: a failed update leaves this daemon in
// charge, so the held terminal.open is served rather than rejected. Neither
// outcome can lose the tab.
func TestDaemonLiveUpdateBarrierServesTerminalOpenAfterFailedUpdate(t *testing.T) {
	harness := newUpdateHandoffHarness(t)
	existing := harness.openTab(t, "term-existing")

	done := harness.startUpdate()
	peer, token := harness.dialSuccessor(t)

	opened := make(chan error, 1)
	go func() {
		_, err := harness.host.open(context.Background(), terminalOpenParams{
			Target: "node:" + harness.node.ID,
			Kind:   "node-host-shell",
			Cols:   80,
			Rows:   24,
		}, "term-late")
		opened <- err
	}()
	select {
	case err := <-opened:
		t.Fatalf("terminal.open resolved during a live update (error = %v)", err)
	case <-time.After(250 * time.Millisecond):
	}

	harness.receiveHandoff(t, peer, token)
	if err := peer.WriteJSON(daemon.HandoffMessage{Type: "failed", Error: "injected import failure"}, nil); err != nil {
		t.Fatalf("write handoff failure: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("daemon.update succeeded after the successor refused the handoff")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("daemon.update did not roll back after the successor refused the handoff")
	}

	select {
	case err := <-opened:
		if err != nil {
			t.Fatalf("held terminal.open error = %v, want the tab to be served by the surviving daemon", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("terminal.open stayed blocked after the live update failed")
	}
	want := []string{existing.TerminalID, "term-late"}
	if got := terminalStateIDs(harness.host.list()); !slices.Equal(got, want) {
		t.Fatalf("tabs after a failed update = %v, want %v", got, want)
	}
	if got := terminalStateIDs(readPersistedSession(t, harness.session).Terminals); !slices.Equal(got, want) {
		t.Fatalf("session after a failed update = %v, want %v", got, want)
	}
}

// handoffFDs opens one pipe per terminal and returns duplicated descriptors
// suitable for an SCM_RIGHTS transfer or for direct adoption. The duplicates are
// owned by the caller: adoption takes them over, while a sender closes its copy
// once the descriptors are on the wire.
func handoffFDs(t *testing.T, ids []string) map[string]int {
	t.Helper()
	fds := map[string]int{}
	for _, id := range ids {
		read, write, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = read.Close(); _ = write.Close() })
		duplicate, err := syscall.Dup(int(read.Fd()))
		if err != nil {
			t.Fatal(err)
		}
		fds[id] = duplicate
	}
	return fds
}

func handoffManifestFor(ids []string, token string) daemon.HandoffManifest {
	manifest := daemon.HandoffManifest{
		Version:       daemon.HandoffVersion,
		BinaryVersion: Version,
		Token:         token,
		Session:       daemon.Session{Version: daemon.SessionVersion},
	}
	createdAt := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	for index, id := range ids {
		manifest.Session.Terminals = append(manifest.Session.Terminals, daemon.TerminalState{
			TerminalID: id,
			TabID:      "node:handoff#" + id,
			Target:     "node:handoff",
			Kind:       "node-host-shell",
			Label:      id,
			CreatedAt:  createdAt.Add(time.Duration(index) * time.Second),
			Cols:       80,
			Rows:       24,
		})
		manifest.Runtimes = append(manifest.Runtimes, daemon.HandoffRuntime{
			TerminalID: id,
			ChildPID:   os.Getpid(),
			Cols:       80,
			Rows:       24,
			ReplaySize: len("handoff-replay"),
		})
	}
	return manifest
}

// newHandoffCallbackAdopter builds an adoption seam whose runtimes behave like a
// freshly started renderer: they deliver callbacks immediately and keep
// delivering for as long as the terminal lives. Adopting one only returns once
// it is really delivering, so every entry published after it is written while
// callbacks are in flight. interval paces the delivery loop, and probe, when
// set, additionally reads the host state those callbacks race against.
func newHandoffCallbackAdopter(
	stop <-chan struct{},
	callbacks *sync.WaitGroup,
	interval time.Duration,
	probe func(),
) handoffAdopter {
	return func(_ string, post func(vaxis.Event), pty *os.File, _, cols, rows int, _ []byte, _ bool) (daemonTerminal, error) {
		// The production adopter refuses a runtime it cannot drive. A fake that
		// accepted one would hide a manifest or descriptor mismatch instead of
		// failing the handoff the way the real import does.
		if pty == nil || cols <= 0 || rows <= 0 {
			return nil, fmt.Errorf("adopt handoff terminal with invalid %dx%d runtime", cols, rows)
		}
		callbacks.Add(1)
		delivering := make(chan struct{})
		var delivered sync.Once
		go func() {
			defer callbacks.Done()
			for {
				post(vaxis.Redraw{})
				if probe != nil {
					probe()
				}
				delivered.Do(func() { close(delivering) })
				select {
				case <-stop:
					return
				default:
				}
				if interval > 0 {
					select {
					case <-stop:
						return
					case <-time.After(interval):
					}
				}
			}
		}()
		<-delivering
		return adoptFakeHandoffTerminal(pty), nil
	}
}

// Adoption starts runtimes that deliver callbacks immediately, and those
// callbacks read the same terminal map the import is filling in. Populating that
// map without the host lock is a concurrent map write; this test drives the
// callbacks during adoption so the race detector sees the overlap.
func TestDaemonImportAdoptionIsRaceFreeUnderRuntimeCallbacks(t *testing.T) {
	service, _ := newHandoffTestService(t)
	host := newDaemonHost(service)

	// Stands in for server.Broadcast, which production wires before the first
	// entry exists precisely because publisher goroutines read it unguarded.
	var published atomic.Bool
	host.broadcast = func(string, any) { published.Store(true) }

	ids := make([]string, 0, 12)
	for index := range cap(ids) {
		ids = append(ids, fmt.Sprintf("term-%02d", index))
	}
	manifest := handoffManifestFor(ids, "token")
	for index := range manifest.Runtimes {
		manifest.Runtimes[index].Replay = []byte("handoff-replay")
	}
	fds := handoffFDs(t, ids)

	stop := make(chan struct{})
	var callbacks sync.WaitGroup
	// The probe reads the terminal map the adoption is filling in, which is the
	// access the callbacks race against.
	adopt := newHandoffCallbackAdopter(stop, &callbacks, 0, func() { host.list() })

	imported, err := host.adoptHandoffTerminals(manifest, fds, adopt)
	close(stop)
	callbacks.Wait()
	if err != nil {
		t.Fatalf("adoptHandoffTerminals() error = %v", err)
	}
	if len(imported) != len(ids) {
		t.Fatalf("adopted %d terminals, want %d", len(imported), len(ids))
	}
	if len(fds) != 0 {
		t.Fatalf("adoption left %d descriptors unclaimed", len(fds))
	}
	if got := terminalStateIDs(host.list()); !slices.Equal(got, ids) {
		t.Fatalf("adopted tab order = %v, want the manifest order %v", got, ids)
	}
	waitForHandoff(t, 5*time.Second, published.Load, "an adopted terminal to publish through the wired broadcast")
	if err := host.Close(); err != nil {
		t.Fatalf("daemonHost.Close() error = %v", err)
	}
}

// The importing daemon owns the tab set the moment it reports the commit, so it
// must have written session.json before that message goes out: the predecessor
// may exit immediately afterwards, and nothing else rewrites that file until the
// next mutation.
func TestDaemonImportPersistsSessionBeforeSignalingCommit(t *testing.T) {
	service, _ := newHandoffTestService(t)
	paths := daemon.HomePaths(service.cfg.MetadataRoot)
	socketPath := filepath.Join(paths.Dir, "import-handoff.sock")
	listener, err := daemon.ListenHandoff(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close(); _ = os.Remove(socketPath) }()

	ids := []string{"term-alpha", "term-beta"}
	token := "import-token"
	manifest := handoffManifestFor(ids, token)
	fds := handoffFDs(t, ids)

	stop := make(chan struct{})
	var callbacks sync.WaitGroup
	// Paced delivery here: these callbacks run for the whole test, alongside a
	// real daemon server that must stay responsive enough to answer a ping.
	adopt := newHandoffCallbackAdopter(stop, &callbacks, time.Millisecond, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	imported := make(chan error, 1)
	go func() { imported <- runDaemonImport(ctx, service, socketPath, token, adopt) }()
	t.Cleanup(func() {
		close(stop)
		callbacks.Wait()
	})

	_ = listener.SetDeadline(time.Now().Add(15 * time.Second))
	conn, err := listener.AcceptUnix()
	if err != nil {
		t.Fatalf("accept handoff: %v", err)
	}
	defer func() { _ = conn.Close() }()
	peer := daemon.NewHandoffConnection(conn, daemon.HandoffFramingLengthPrefixed)
	message, err := peer.ReadControlMessage()
	if err != nil || message.Type != "hello" || message.Token != token {
		t.Fatalf("handoff hello = %#v, %v", message, err)
	}
	if err := peer.WriteJSON(manifest, nil); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	for _, id := range ids {
		if err := peer.WriteReplay(id, []byte("handoff-replay")); err != nil {
			t.Fatalf("write replay: %v", err)
		}
	}
	batch := make([]int, 0, len(ids))
	for _, id := range ids {
		batch = append(batch, fds[id])
	}
	if err := peer.WriteJSON(daemon.HandoffMessage{Type: "fds", TerminalIDs: ids}, batch); err != nil {
		t.Fatalf("write fds: %v", err)
	}
	// The receiver holds its own descriptors now; the sender's copies are done.
	daemon.CloseHandoffFDs(batch)
	message, err = peer.ReadControlMessage()
	if err != nil || message.Type != "ready" {
		t.Fatalf("handoff ready = %#v, %v", message, err)
	}
	if err := peer.WriteJSON(daemon.HandoffMessage{Type: "commit", Token: token}, nil); err != nil {
		t.Fatalf("write commit: %v", err)
	}
	message, err = peer.ReadControlMessage()
	if err != nil || message.Type != "committed" {
		t.Fatalf("handoff committed = %#v, %v", message, err)
	}

	// This is the crash window the predecessor is released into.
	if got := terminalStateIDs(readPersistedSession(t, paths.Session).Terminals); !slices.Equal(got, ids) {
		t.Fatalf("session at commit = %v, want the adopted tabs %v", got, ids)
	}
	if _, err := daemonclient.Ping(ctx, service.cfg.MetadataRoot, Version); err != nil {
		t.Fatalf("imported daemon is not serving at commit: %v", err)
	}

	cancel()
	select {
	case err := <-imported:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("runDaemonImport() error = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runDaemonImport did not exit after cancellation")
	}
}
