//go:build cgo && (darwin || linux)

package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/creack/pty"
)

const (
	rendererFaultWorkerEnv    = "CODELIMA_TEST_RENDERER_FAULT_MODE"
	rendererFaultReadyEnv     = "CODELIMA_TEST_RENDERER_FAULT_READY_FILE"
	rendererFaultInitDelayEnv = "CODELIMA_TEST_RENDERER_FAULT_INIT_DELAY"
	// rendererFaultProtocolEnv overrides the protocol version the fault worker
	// announces in its init reply, which is how on-disk binary skew is
	// reproduced without building a second binary. Unset means "agree with this
	// build"; rendererFaultProtocolAbsent reproduces a worker from before the
	// protocol was versioned, which announced nothing at all.
	rendererFaultProtocolEnv    = "CODELIMA_TEST_RENDERER_FAULT_PROTOCOL"
	rendererFaultProtocolAbsent = "absent"

	// rendererFaultServe answers every request; the interesting fault is then
	// whatever the init delay does to the caller's startup budget.
	rendererFaultServe = "serve"
	// rendererFaultExit never serves the control socket: the worker dies before
	// answering init, which is what a renderer that cannot start looks like.
	rendererFaultExit = "exit"
	// rendererFaultExitUntilReady dies until the ready file exists, so a test
	// can decide when a restart is allowed to succeed.
	rendererFaultExitUntilReady = "exit_until_ready"
	// rendererFaultPoison serves normally but dies whenever it is asked to
	// replay a journal: a journal whose replay always kills the renderer.
	rendererFaultPoison = "poison"
)

// TestRendererFaultWorkerProcess is the fault-injection renderer worker. It
// speaks the worker protocol directly instead of driving libghostty so a test
// can choose exactly how a renderer process fails.
func TestRendererFaultWorkerProcess(t *testing.T) {
	mode := os.Getenv(rendererFaultWorkerEnv)
	if mode == "" {
		return
	}
	os.Exit(runRendererFaultWorker(mode, os.Getenv(rendererFaultReadyEnv)))
}

func runRendererFaultWorker(mode, readyFile string) int {
	switch mode {
	case rendererFaultExit:
		return 1
	case rendererFaultExitUntilReady:
		if _, err := os.Stat(readyFile); err != nil {
			return 1
		}
	}
	file := os.NewFile(rendererWorkerFD, "codelima-renderer-control")
	if file == nil {
		return 2
	}
	conn, err := net.FileConn(file)
	_ = file.Close()
	if err != nil {
		return 2
	}
	defer func() { _ = conn.Close() }()
	for {
		frame, err := readRendererFrame(conn)
		if err != nil {
			return 0
		}
		if frame.Type != rendererFrameRequest {
			return 3
		}
		response := rendererWorkerFrame{
			Type:       rendererFrameResponse,
			ID:         frame.ID,
			Generation: frame.Generation,
		}
		switch frame.Method {
		case "init":
			var params rendererInitParams
			if err := json.Unmarshal(frame.Params, &params); err != nil {
				return 4
			}
			if mode == rendererFaultPoison && len(params.Journal) > 0 {
				return 5
			}
			// Stands in for the replay work a real renderer does before it can
			// answer init.
			if delay, err := time.ParseDuration(os.Getenv(rendererFaultInitDelayEnv)); err == nil {
				time.Sleep(delay)
			}
			response.Result, _ = json.Marshal(rendererFaultInitReply(os.Getenv(rendererFaultProtocolEnv)))
		case "close":
			response.Result, _ = json.Marshal(map[string]bool{"closed": true})
			if frame.ID != 0 && !frame.NoReply {
				_ = writeRendererFrame(conn, response)
			}
			return 0
		default:
			response.Result, _ = json.Marshal(map[string]bool{"ok": true})
		}
		if frame.ID == 0 || frame.NoReply {
			continue
		}
		if err := writeRendererFrame(conn, response); err != nil {
			return 6
		}
	}
}

// rendererFaultInitReply builds the init reply for the announced-version knob.
// The absent case marshals the exact payload the pre-versioning worker sent, so
// the supervisor is tested against the real historical bytes rather than a
// struct with a zeroed field.
func rendererFaultInitReply(announce string) any {
	switch announce {
	case "":
		return rendererInitResult{Protocol: rendererWorkerProtocolVersion, Ready: true}
	case rendererFaultProtocolAbsent:
		return map[string]bool{"ready": true}
	default:
		version, err := strconv.Atoi(announce)
		if err != nil {
			return map[string]string{"protocol": announce}
		}
		return rendererInitResult{Protocol: version, Ready: true}
	}
}

func rendererFaultWorkerOptions(t *testing.T, mode, readyFile string) rendererProcessOptions {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := []string{rendererFaultWorkerEnv + "=" + mode}
	if readyFile != "" {
		env = append(env, rendererFaultReadyEnv+"="+readyFile)
	}
	return rendererProcessOptions{
		Executable:     executable,
		Args:           []string{"-test.run=^TestRendererFaultWorkerProcess$"},
		Env:            env,
		CommandTimeout: 500 * time.Millisecond,
		QueueFrames:    16,
	}
}

func rendererGhosttyWorkerOptions(t *testing.T) rendererProcessOptions {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	options := defaultRendererProcessOptions()
	options.Executable = executable
	options.Args = []string{"-test.run=^TestRendererWorkerProcess$"}
	options.Env = []string{rendererWorkerHelperEnv + "=1"}
	return options
}

// rendererProtocolWorkerOptions builds fault-worker options that serve normally
// but announce the given protocol version, standing in for an out-of-date
// codelima-renderer-worker sitting beside an upgraded binary.
func rendererProtocolWorkerOptions(t *testing.T, announce string) rendererProcessOptions {
	t.Helper()

	options := rendererFaultWorkerOptions(t, rendererFaultServe, "")
	if announce != "" {
		options.Env = append(options.Env, rendererFaultProtocolEnv+"="+announce)
	}
	options.Restart = rendererRestartPolicy{
		MaxRestarts: 5,
		Window:      time.Minute,
		// Long enough that no cooldown retry interleaves with the assertions:
		// the point of these tests is what happens *without* the clock helping.
		Cooldown:       time.Hour,
		HealthyReset:   time.Minute,
		BackoffStep:    time.Millisecond,
		DegradedNotice: time.Nanosecond,
		SendGrace:      100 * time.Millisecond,
	}
	return options
}

// TestRendererProtocolHandshakeAcceptsAMatchingWorker pins the working side of
// the version handshake: a worker announcing this build's version is installed
// as ready, and its version does not leak into the link's error surface.
func TestRendererProtocolHandshakeAcceptsAMatchingWorker(t *testing.T) {
	options := rendererProtocolWorkerOptions(t, "")
	journal := newRendererJournal(defaultRendererJournalBytes)
	supervisor := newRendererSupervisor("protocol-match", journal, options, nil, nil, nil, nil)
	t.Cleanup(supervisor.Close)

	if err := supervisor.Start(context.Background(), 80, 24); err != nil {
		t.Fatalf("Start with a matching worker protocol = %v", err)
	}
	status := supervisor.Status()
	if status.State != rendererStateReady || status.PID <= 0 || status.LastError != "" {
		t.Fatalf("matched-handshake status = %#v, want a live ready renderer with no error", status)
	}
	if err := supervisor.TryUpdate(rendererInputEvent{Type: "key", Text: "x"}); err != nil {
		t.Fatalf("input to a matched renderer = %v", err)
	}
}

// TestRendererProtocolMismatchDegradesWithoutBurningTheRestartBudget covers the
// failure the version field exists for: on-disk binary skew after an upgrade.
//
// A mismatch is permanent, not transient, so it must not be treated like a
// crashing renderer. Three things are asserted: the error names the mismatch
// distinctly, the restart budget is never charged (an automatic retry loop
// would spend five restarts a minute forever to keep rediscovering the same
// two files), and invariant I4 survives -- the PTY pump still gets an immediate
// answer and the journal keeps absorbing output.
//
// The absent case is the upgrade path that actually happens: a worker built
// before the protocol was versioned sends no version at all, and "missing"
// has to mean "mismatch", not "assume compatible".
func TestRendererProtocolMismatchDegradesWithoutBurningTheRestartBudget(t *testing.T) {
	for _, test := range []struct {
		name     string
		announce string
		want     string
	}{
		{name: "newer worker", announce: "99", want: "announced version 99"},
		{name: "older worker", announce: "1", want: "announced version 1"},
		{name: "worker predating the version field", announce: rendererFaultProtocolAbsent, want: "no version at all"},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := rendererProtocolWorkerOptions(t, test.announce)
			journal := newRendererJournal(defaultRendererJournalBytes)
			supervisor := newRendererSupervisor("protocol-"+test.announce, journal, options, nil, nil, nil, nil)
			t.Cleanup(supervisor.Close)

			err := supervisor.Start(context.Background(), 80, 24)
			if !errors.Is(err, errRendererProtocolMismatch) {
				t.Fatalf("Start against a %s worker = %v, want %v", test.name, err, errRendererProtocolMismatch)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("mismatch error %q does not report %q", err, test.want)
			}
			if !strings.Contains(err.Error(), rendererWorkerExecutableName) {
				t.Fatalf("mismatch error %q does not name the worker executable", err)
			}

			// The automatic path: one forced attempt is allowed (the worker file
			// on disk could have been replaced), and then it parks rather than
			// looping. Neither charges the budget.
			supervisor.Restart()
			waitForCondition(t, 20*time.Second, func() bool {
				return supervisor.Status().State == rendererStateDegraded
			}, "renderer degraded on a protocol mismatch")
			status := supervisor.Status()
			if status.RestartCount != 0 {
				t.Fatalf("mismatch charged %d restarts, want the budget untouched", status.RestartCount)
			}
			if !strings.Contains(status.LastError, errRendererProtocolMismatch.Error()) {
				t.Fatalf("degraded cause = %q, want the protocol mismatch", status.LastError)
			}
			settled := status.Generation

			// I4: the PTY read pump must keep draining into the journal, and it
			// must get an immediate answer rather than a wait.
			baseline := journal.Stats().Bytes
			for index := range 16 {
				event := journal.AppendOutput([]byte("output during mismatch\r\n"))
				started := time.Now()
				sendErr := supervisor.SendOutput(event)
				if elapsed := time.Since(started); elapsed > options.Restart.SendGrace {
					t.Fatalf("SendOutput %d blocked for %s against a mismatched renderer", index, elapsed)
				}
				if sendErr != nil && !errors.Is(sendErr, errRendererDegraded) && !errors.Is(sendErr, errRendererUnavailable) {
					t.Fatalf("SendOutput %d error = %v, want a degraded renderer error", index, sendErr)
				}
			}
			if grown := journal.Stats().Bytes - baseline; grown <= 0 {
				t.Fatalf("journal absorbed %d bytes during a mismatch, want the output retained", grown)
			}
			if err := supervisor.TryUpdate(rendererInputEvent{Type: "key", Text: "x"}); !errors.Is(err, errRendererDegraded) {
				t.Fatalf("input to a mismatched renderer = %v, want %v", err, errRendererDegraded)
			}

			// Nothing above may have restarted a spawn loop behind the scenes:
			// the budget stays untouched and no new generation is created.
			time.Sleep(300 * time.Millisecond)
			if final := supervisor.Status(); final.RestartCount != 0 || final.Generation != settled ||
				final.State != rendererStateDegraded {
				t.Fatalf("mismatch looped: status = %#v, want degraded at generation %d", final, settled)
			}
		})
	}
}

// TestLiveUpdateAlwaysRespawnsTheRendererWorker is the evidence behind the
// adoption comment on adoptIsolatedDaemonTerminal: a live update cannot carry a
// renderer worker across binaries, so the spawn-time handshake is the only
// version check adoption needs.
//
// It pins both halves. BeginHandoff releases the outgoing daemon's renderer, so
// there is nothing left to adopt; and adoption resolves a worker beside the
// *running* executable, which in a real update is the new binary.
func TestLiveUpdateAlwaysRespawnsTheRendererWorker(t *testing.T) {
	options := rendererProtocolWorkerOptions(t, "")
	terminal := newIsolatedDaemonTerminalWithOptions("handoff-renderer", func(vaxis.Event) {}, options)
	if err := terminal.Start(exec.Command("/bin/sh", "-c", "sleep 30")); err != nil {
		t.Fatalf("start terminal: %v", err)
	}
	t.Cleanup(terminal.Close)
	if status := terminal.RendererStatus(); status.State != rendererStateReady || status.PID <= 0 {
		t.Fatalf("renderer status before handoff = %#v, want a live renderer", status)
	}

	state := terminal.BeginHandoff()
	if state.Err != nil {
		t.Fatalf("BeginHandoff() = %#v", state)
	}
	if state.PTY != nil {
		_ = state.PTY.Close()
	}
	// handoffTerminalState carries a PTY, a child PID, geometry and replay
	// bytes; the renderer is gone rather than handed over, which is why an
	// adopted terminal can never inherit a worker older than its own binary.
	terminal.mu.Lock()
	adopted := terminal.renderer
	terminal.mu.Unlock()
	if adopted != nil {
		t.Fatal("BeginHandoff left a renderer behind for the importer to adopt")
	}
	if status := terminal.RendererStatus(); status.State != "unavailable" {
		t.Fatalf("renderer status after handoff = %#v, want no renderer", status)
	}

	// The importer's own adoption path resolves the worker beside the running
	// executable at adopt time -- the new binary during a real update, and the
	// test binary here, which has no worker beside it.
	ptyFile, ttyFile, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ttyFile.Close() }()
	_, adoptErr := adoptIsolatedDaemonTerminal("adopted", func(vaxis.Event) {}, ptyFile, 0, 80, 24, state.Replay, state.ReplayPartial)
	if adoptErr == nil {
		t.Fatal("adoption reused a renderer instead of resolving one beside the running binary")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if want := rendererWorkerPathBeside(executable); !strings.Contains(adoptErr.Error(), want) {
		t.Fatalf("adoption error = %v, want a spawn of the worker at %s", adoptErr, want)
	}
}

// TestSupervisorTrySendNeverBlocksOnAFullOutboundQueue pins the Try* contract.
// Notify parks until the writer drains, which is right for journaled PTY output
// (bounded backpressure) and wrong for everything spelled Try*: those callers
// are daemon RPC handlers and the snapshot publisher, which must come back with
// an answer instead of inheriting a wedged worker's stall.
func TestSupervisorTrySendNeverBlocksOnAFullOutboundQueue(t *testing.T) {
	t.Parallel()

	// A link with no writer goroutine and a one-frame queue: the second send
	// has nowhere to go and can only either park or refuse.
	link := &rendererLink{
		generation: 1,
		outbound:   make(chan rendererWorkerFrame, 1),
		control:    make(chan rendererWorkerFrame, 1),
		done:       make(chan struct{}),
		stop:       make(chan struct{}),
		pending:    map[uint64]rendererPending{},
		waiters:    map[uint64]chan rendererCallOutcome{},
	}
	supervisor := &rendererSupervisor{
		terminalID: "try-send",
		journal:    newRendererJournal(defaultRendererJournalBytes),
		options:    defaultRendererProcessOptions(),
		policy:     defaultRendererRestartPolicy(),
		stateWake:  make(chan struct{}),
		shutdown:   make(chan struct{}),
		link:       link,
	}

	if err := supervisor.TryScroll(1); err != nil {
		t.Fatalf("first TryScroll = %v, want the frame queued", err)
	}
	for name, send := range map[string]func() error{
		"TryScroll":       func() error { return supervisor.TryScroll(1) },
		"TryFocus":        func() error { return supervisor.TryFocus(true) },
		"TryUpdate":       func() error { return supervisor.TryUpdate(rendererInputEvent{Type: "key", Text: "x"}) },
		"TryResize":       func() error { return supervisor.TryResize(rendererJournalEvent{ID: 1, Cols: 80, Rows: 24}) },
		"RequestSnapshot": supervisor.RequestSnapshot,
	} {
		done := make(chan error, 1)
		go func() { done <- send() }()
		select {
		case err := <-done:
			if !errors.Is(err, errRendererOutboundQueueFull) {
				t.Fatalf("%s on a full queue = %v, want %v", name, err, errRendererOutboundQueueFull)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s blocked on a full outbound queue", name)
		}
	}

	// The PTY output path keeps its bounded backpressure: it is allowed to wait,
	// and does, which is why it is deliberately not routed through trySend.
	blocked := make(chan error, 1)
	go func() {
		blocked <- link.Notify(supervisor.shutdown, "output", rendererOutputParams{EventID: 2, Data: []byte("x")})
	}()
	select {
	case err := <-blocked:
		t.Fatalf("Notify returned %v instead of applying backpressure", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(supervisor.shutdown)
	if err := <-blocked; !errors.Is(err, errTerminalClosed) {
		t.Fatalf("Notify released with %v, want %v", err, errTerminalClosed)
	}
}

// TestRendererRestartBudgetExhaustionKeepsThePTYPumpAlive covers finding 1f and
// invariant I4: an exhausted restart budget must leave the terminal
// stale-but-alive, never blocked. SendOutput has to fail fast so the PTY read
// pump keeps draining into the journal, and input has to be rejected with an
// error instead of being silently dropped or queued forever.
func TestRendererRestartBudgetExhaustionKeepsThePTYPumpAlive(t *testing.T) {
	options := rendererFaultWorkerOptions(t, rendererFaultExit, "")
	options.Restart = rendererRestartPolicy{
		MaxRestarts: 2,
		Window:      time.Minute,
		// Long enough that the cooldown retry cannot interleave with the
		// degraded-state assertions below.
		Cooldown:       3 * time.Second,
		HealthyReset:   time.Minute,
		BackoffStep:    time.Millisecond,
		DegradedNotice: time.Nanosecond,
		SendGrace:      100 * time.Millisecond,
	}
	journal := newRendererJournal(defaultRendererJournalBytes)
	supervisor := newRendererSupervisor("budget-exhausted", journal, options, nil, nil, nil, nil)
	t.Cleanup(supervisor.Close)

	if err := supervisor.Start(context.Background(), 80, 24); err == nil {
		t.Fatal("fault renderer worker started successfully")
	}
	// A health failure would normally kick the supervisor loop; here the manual
	// restart entry point does it.
	supervisor.Restart()
	waitForCondition(t, 20*time.Second, func() bool {
		return supervisor.Status().State == rendererStateDegraded
	}, "renderer degraded after exhausting its restart budget")
	if status := supervisor.Status(); status.RestartCount != options.Restart.MaxRestarts || status.Generation < 3 {
		t.Fatalf("degraded renderer status = %#v, want the whole restart budget spent", status)
	}

	baseline := journal.Stats().Bytes
	var degradedErrors int
	for index := range 32 {
		event := journal.AppendOutput([]byte("degraded output\r\n"))
		started := time.Now()
		err := supervisor.SendOutput(event)
		elapsed := time.Since(started)
		if elapsed > options.Restart.SendGrace {
			t.Fatalf("SendOutput %d blocked for %s while degraded", index, elapsed)
		}
		if err == nil {
			continue
		}
		if !errors.Is(err, errRendererDegraded) && !errors.Is(err, errRendererUnavailable) {
			t.Fatalf("SendOutput %d error = %v, want a degraded renderer error", index, err)
		}
		degradedErrors++
	}
	if degradedErrors == 0 {
		t.Fatal("a degraded renderer accepted output without surfacing an error")
	}
	if grown := journal.Stats().Bytes - baseline; grown <= 0 {
		t.Fatalf("journal absorbed %d bytes while degraded, want the output retained", grown)
	}

	if err := supervisor.TryUpdate(rendererInputEvent{Type: "key", Text: "x"}); !errors.Is(err, errRendererDegraded) {
		t.Fatalf("degraded renderer input error = %v, want %v", err, errRendererDegraded)
	}
	if err := supervisor.RequestSnapshot(); !errors.Is(err, errRendererDegraded) {
		t.Fatalf("degraded renderer snapshot error = %v, want %v", err, errRendererDegraded)
	}
	if status := supervisor.Status(); status.State != rendererStateDegraded || status.LastError == "" {
		t.Fatalf("degraded renderer status = %#v, want a degraded state with a cause", status)
	}
}

// TestDegradedRendererThrottlesRepeatedOutputErrors pins the production notice
// policy: entering degraded always surfaces an error, and the errors that
// follow are rate-limited so a chatty shell cannot turn one dead renderer into
// an event-queue flood. Throttled sends still return immediately, and the bytes
// stay in the journal for the next renderer generation.
func TestDegradedRendererThrottlesRepeatedOutputErrors(t *testing.T) {
	options := rendererFaultWorkerOptions(t, rendererFaultExit, "")
	options.Restart = rendererRestartPolicy{
		MaxRestarts:    2,
		Window:         time.Minute,
		Cooldown:       3 * time.Second,
		HealthyReset:   time.Minute,
		BackoffStep:    time.Millisecond,
		DegradedNotice: time.Minute,
		SendGrace:      100 * time.Millisecond,
	}
	journal := newRendererJournal(defaultRendererJournalBytes)
	supervisor := newRendererSupervisor("degraded-throttle", journal, options, nil, nil, nil, nil)
	t.Cleanup(supervisor.Close)

	if err := supervisor.Start(context.Background(), 80, 24); err == nil {
		t.Fatal("fault renderer worker started successfully")
	}
	supervisor.Restart()
	waitForCondition(t, 20*time.Second, func() bool {
		return supervisor.Status().State == rendererStateDegraded
	}, "renderer degraded after exhausting its restart budget")

	if err := supervisor.SendOutput(journal.AppendOutput([]byte("first\r\n"))); !errors.Is(err, errRendererDegraded) {
		t.Fatalf("first degraded SendOutput error = %v, want %v", err, errRendererDegraded)
	}
	for index := range 128 {
		started := time.Now()
		err := supervisor.SendOutput(journal.AppendOutput([]byte("more\r\n")))
		if elapsed := time.Since(started); elapsed > options.Restart.SendGrace {
			t.Fatalf("throttled SendOutput %d blocked for %s", index, elapsed)
		}
		if err != nil {
			t.Fatalf("throttled SendOutput %d error = %v, want the notice suppressed", index, err)
		}
	}
	if stats := journal.Stats(); stats.Events != 129 {
		t.Fatalf("journal retained %d events, want every rejected write journaled", stats.Events)
	}
}

// TestDegradedRendererRecoversOnCooldownAndRearmsItsBudget covers invariant I2:
// degraded is a cooldown, not a latch. Nothing but the supervisor's own timer
// is allowed to bring the renderer back, and a sustained healthy period must
// restore the full restart budget.
func TestDegradedRendererRecoversOnCooldownAndRearmsItsBudget(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "renderer-ready")
	options := rendererFaultWorkerOptions(t, rendererFaultExitUntilReady, ready)
	options.Restart = rendererRestartPolicy{
		MaxRestarts:    2,
		Window:         time.Minute,
		Cooldown:       100 * time.Millisecond,
		HealthyReset:   300 * time.Millisecond,
		BackoffStep:    time.Millisecond,
		DegradedNotice: time.Nanosecond,
		SendGrace:      100 * time.Millisecond,
	}
	journal := newRendererJournal(defaultRendererJournalBytes)
	var staleMarks atomic.Int32
	supervisor := newRendererSupervisor(
		"degraded-recovery",
		journal,
		options,
		nil,
		nil,
		nil,
		func() { staleMarks.Add(1) },
	)
	t.Cleanup(supervisor.Close)

	if err := supervisor.Start(context.Background(), 100, 30); err == nil {
		t.Fatal("fault renderer worker started successfully")
	}
	supervisor.Restart()
	waitForCondition(t, 20*time.Second, func() bool {
		return supervisor.Status().State == rendererStateDegraded
	}, "renderer degraded after exhausting its restart budget")
	if staleMarks.Load() == 0 {
		t.Fatal("a degraded renderer never marked the cached screen stale")
	}

	if err := os.WriteFile(ready, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, 20*time.Second, func() bool {
		return supervisor.Status().State == rendererStateReady
	}, "degraded renderer recovering on its own cooldown")

	event := journal.AppendOutput([]byte("recovered\r\n"))
	if err := supervisor.SendOutput(event); err != nil {
		t.Fatalf("SendOutput after recovery = %v", err)
	}
	waitForCondition(t, 20*time.Second, func() bool {
		return supervisor.Status().RestartCount == 0
	}, "restart budget re-armed after a sustained healthy period")
	if status := supervisor.Status(); status.State != rendererStateReady || status.PID <= 0 {
		t.Fatalf("recovered renderer status = %#v, want a live ready renderer", status)
	}
}

// TestPoisonJournalFallsBackToAClearedJournalRenderer covers the poison-journal
// escape: when every replacement dies replaying the same journal, the
// supervisor drops the journal and brings up a blank but live renderer instead
// of parking the terminal in degraded forever.
func TestPoisonJournalFallsBackToAClearedJournalRenderer(t *testing.T) {
	options := rendererFaultWorkerOptions(t, rendererFaultPoison, "")
	options.Restart = rendererRestartPolicy{
		MaxRestarts:    2,
		Window:         time.Minute,
		Cooldown:       10 * time.Second,
		HealthyReset:   time.Minute,
		BackoffStep:    time.Millisecond,
		DegradedNotice: time.Nanosecond,
		SendGrace:      100 * time.Millisecond,
	}
	journal := newRendererJournal(defaultRendererJournalBytes)
	journal.AppendResize(120, 40)
	journal.AppendOutput([]byte("poisonous replay"))
	supervisor := newRendererSupervisor("poison-journal", journal, options, nil, nil, nil, nil)
	t.Cleanup(supervisor.Close)

	if err := supervisor.Start(context.Background(), 120, 40); err == nil {
		t.Fatal("poison journal replay started successfully")
	}
	supervisor.Restart()
	waitForCondition(t, 20*time.Second, func() bool {
		return supervisor.Status().State == rendererStateReady
	}, "cleared-journal renderer after a poisonous replay")

	stats := journal.Stats()
	if stats.Events != 0 || stats.Bytes != 0 {
		t.Fatalf("journal after the poison escape = %#v, want it cleared", stats)
	}
	if !stats.Partial {
		t.Fatal("cleared journal did not report partial recovery")
	}
	if stats.Cols != 120 || stats.Rows != 40 {
		t.Fatalf("journal geometry = %dx%d, want 120x40 preserved across the reset", stats.Cols, stats.Rows)
	}
	if status := supervisor.Status(); !status.PartialRecovery || status.PID <= 0 {
		t.Fatalf("renderer status after the poison escape = %#v, want a live partial renderer", status)
	}
}

// TestSIGKILLedRendererWorkerRestartsAndReplaysJournal covers the hard-kill
// case: the cooperative-hang test only proves the supervisor notices a wedged
// renderer. A renderer that dies instantly must be replaced, must replay the
// journal, and must not disturb the shell.
func TestSIGKILLedRendererWorkerRestartsAndReplaysJournal(t *testing.T) {
	terminal := newIsolatedDaemonTerminalWithOptions("sigkill", func(vaxis.Event) {}, rendererGhosttyWorkerOptions(t))
	if err := terminal.Start(exec.Command("/bin/cat")); err != nil {
		t.Fatalf("start terminal: %v", err)
	}
	t.Cleanup(terminal.Close)

	terminal.SendInput([]byte("first-marker\n"))
	waitForCondition(t, 10*time.Second, func() bool {
		return strings.Contains(terminal.ReadVisible(ReadText).Text, "first-marker")
	}, "first marker on the original renderer")

	terminal.mu.Lock()
	shellPID := terminal.childPID
	terminal.mu.Unlock()
	rendererPID := terminal.RendererStatus().PID
	if shellPID <= 0 || rendererPID <= 0 {
		t.Fatalf("terminal has no shell (%d) or renderer (%d) process", shellPID, rendererPID)
	}
	if err := syscall.Kill(rendererPID, syscall.SIGKILL); err != nil {
		t.Fatalf("SIGKILL renderer %d: %v", rendererPID, err)
	}

	waitForCondition(t, 20*time.Second, func() bool {
		status := terminal.RendererStatus()
		return status.State == rendererStateReady && status.Generation >= 2 && status.PID != rendererPID
	}, "renderer replacement after SIGKILL")

	terminal.SendInput([]byte("second-marker\n"))
	waitForCondition(t, 20*time.Second, func() bool {
		text := terminal.ReadVisible(ReadText).Text
		return strings.Contains(text, "first-marker") && strings.Contains(text, "second-marker")
	}, "journal replay and live output on the replacement renderer")
	if snapshot := terminal.Snapshot().Snapshot; snapshot.Stale {
		t.Fatal("replacement renderer left the cached screen marked stale")
	}

	if err := syscall.Kill(shellPID, 0); err != nil {
		t.Fatalf("shell %d did not survive the renderer SIGKILL: %v", shellPID, err)
	}
	terminal.mu.Lock()
	preservedPID := terminal.childPID
	terminal.mu.Unlock()
	if preservedPID != shellPID {
		t.Fatalf("shell PID changed across the renderer SIGKILL: %d -> %d", shellPID, preservedPID)
	}
}

// TestHandoffRollbackReplaysALargeJournalWithinItsBudget covers the adoption and
// rollback startup budgets: both used to hand the renderer a fixed
// CommandTimeout, so a terminal with real scrollback could never finish its
// replay in time and the rollback failed with a live shell still attached.
func TestHandoffRollbackReplaysALargeJournalWithinItsBudget(t *testing.T) {
	const (
		journalBytes  = 256 * 1024
		initDelay     = 500 * time.Millisecond
		commandBudget = 300 * time.Millisecond
	)
	options := rendererFaultWorkerOptions(t, rendererFaultServe, "")
	options.Env = append(options.Env, rendererFaultInitDelayEnv+"="+initDelay.String())
	options.CommandTimeout = commandBudget

	terminal := newIsolatedDaemonTerminalWithOptions("rollback-budget", func(vaxis.Event) {}, options)
	terminal.journal.AppendOutput(make([]byte, journalBytes))
	if deadline := rendererInitDeadline(commandBudget, terminal.journal.Stats().Bytes); deadline <= initDelay {
		t.Fatalf("test journal is too small: init deadline %s does not exceed the %s replay", deadline, initDelay)
	}
	if err := terminal.Start(exec.Command("/bin/sh", "-c", "sleep 30")); err != nil {
		t.Fatalf("start terminal with a %d-byte journal: %v", journalBytes, err)
	}
	t.Cleanup(terminal.Close)

	state := terminal.BeginHandoff()
	if state.Err != nil {
		t.Fatalf("BeginHandoff() = %#v", state)
	}
	if state.PTY != nil {
		_ = state.PTY.Close()
	}
	if err := terminal.RollbackHandoff(); err != nil {
		t.Fatalf("RollbackHandoff with a %d-byte journal: %v", journalBytes, err)
	}
	if status := terminal.RendererStatus(); status.State != rendererStateReady {
		t.Fatalf("renderer status after rollback = %#v, want ready", status)
	}
}

// TestRendererStartContextScalesWithTheJournal pins the shared startup budget
// used by adoption and rollback. Neither call site can be pointed at a fake
// worker end to end -- adoptIsolatedDaemonTerminal hardcodes the production
// process options and its signature belongs to the daemon host -- so the budget
// itself is asserted here and exercised end to end through rollback above.
func TestRendererStartContextScalesWithTheJournal(t *testing.T) {
	t.Parallel()

	base := defaultRendererCommandTimeout
	budgetOf := func(journal *rendererJournal) time.Duration {
		ctx, cancel := rendererStartContext(base, journal)
		defer cancel()
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("renderer start context has no deadline")
		}
		return time.Until(deadline)
	}

	if budget := budgetOf(nil); budget > base {
		t.Fatalf("budget without a journal = %s, want at most %s", budget, base)
	}
	empty := newRendererJournal(defaultRendererJournalBytes)
	if budget := budgetOf(empty); budget > base {
		t.Fatalf("empty journal budget = %s, want at most %s", budget, base)
	}
	loaded := newRendererJournal(defaultRendererJournalBytes)
	loaded.AppendOutput(make([]byte, defaultRendererJournalBytes))
	if budget := budgetOf(loaded); budget <= base {
		t.Fatalf("full journal budget = %s, want more than the fixed %s", budget, base)
	}
}

// TestRendererInitDeadlineScalesWithJournalSize covers the fixed-2s init
// deadline that made every restart attempt fail identically once a terminal had
// accumulated scrollback.
func TestRendererInitDeadlineScalesWithJournalSize(t *testing.T) {
	t.Parallel()

	base := defaultRendererCommandTimeout
	if got := rendererInitDeadline(base, 0); got != base {
		t.Fatalf("empty journal init deadline = %s, want %s", got, base)
	}
	small := rendererInitDeadline(base, 64*1024)
	large := rendererInitDeadline(base, defaultRendererJournalBytes)
	if small <= base || large <= small {
		t.Fatalf("init deadlines did not scale with the journal: %s, %s, %s", base, small, large)
	}
	if capped := rendererInitDeadline(base, 1<<30); capped != maxRendererInitDeadline {
		t.Fatalf("init deadline for an oversized journal = %s, want the %s cap", capped, maxRendererInitDeadline)
	}
}
