//go:build cgo && (darwin || linux)

package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRendererHandshakeDiscardsUnverifiedStateAndPublishesReadyState(t *testing.T) {
	for _, announce := range []string{"", "99"} {
		t.Run("protocol="+announce, func(t *testing.T) {
			options := rendererProtocolWorkerOptions(t, announce)
			options.Env = append(options.Env, rendererFaultEarlySnapshotEnv+"=1")
			states := make(chan string, 4)
			supervisor := newRendererSupervisor("verified-publication", newRendererJournal(1024), options,
				func(_ uint64, state rendererPublishedState, _ bool) { states <- state.VisibleText.Text }, nil, nil, nil)
			t.Cleanup(supervisor.Close)
			err := supervisor.Start(context.Background(), 80, 24)
			if announce != "" {
				if !errors.Is(err, errRendererProtocolMismatch) {
					t.Fatalf("Start error = %v, want protocol mismatch", err)
				}
				select {
				case state := <-states:
					t.Fatalf("mismatched worker published %q", state)
				default:
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case state := <-states:
				if state != "verified" {
					t.Fatalf("first publication = %q, want state requested after verified startup", state)
				}
			case <-time.After(time.Second):
				t.Fatal("verified worker never published its initial state")
			}
		})
	}
}

func TestIsolatedPublicationRejectsOldGenerationAndOldSupervisor(t *testing.T) {
	terminal := newIsolatedDaemonTerminalWithOptions("publication-owner", nil, defaultRendererProcessOptions())
	old := terminal.newRenderer()
	t.Cleanup(old.Close)
	current := terminal.newRenderer()
	t.Cleanup(current.Close)
	old.mu.Lock()
	old.generation, old.acceptFrames = 1, true
	old.mu.Unlock()
	current.mu.Lock()
	current.generation, current.acceptFrames = 2, true
	current.mu.Unlock()
	terminal.mu.Lock()
	terminal.renderer = current
	terminal.mu.Unlock()
	state := rendererPublishedState{Snapshot: TerminalSnapshot{Cols: 1, Rows: 1, Generation: 42}}
	current.onSnapshot(1, state, false)
	if terminal.cache.Load() != nil {
		t.Fatal("old generation replaced the published screen")
	}
	old.onSnapshot(1, state, false)
	if terminal.cache.Load() != nil {
		t.Fatal("old supervisor replaced the published screen")
	}
	current.onSnapshot(2, state, false)
	if got := terminal.cache.Load(); got == nil || got.state.Snapshot.Generation != 42 {
		t.Fatalf("current publication = %#v, want generation 42", got)
	}
}

func TestRendererReplaySuppressesClipboardSideEffects(t *testing.T) {
	worker, reader := net.Pipe()
	t.Cleanup(func() { _ = worker.Close(); _ = reader.Close() })
	server := &rendererWorkerServer{conn: worker, generation: 1}
	server.replaying.Store(true)
	done := make(chan struct{})
	go func() {
		server.postEvent(tuiClipboardEvent{Text: "replayed clipboard"})
		close(done)
	}()
	_ = reader.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if frame, err := readRendererFrame(reader); err == nil {
		t.Fatalf("replay emitted external effect: %#v", frame)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("suppressed replay effect blocked the worker")
	}
	server.replaying.Store(false)
	go server.postEvent(tuiClipboardEvent{Text: "live clipboard"})
	_ = reader.SetReadDeadline(time.Now().Add(time.Second))
	frame, err := readRendererFrame(reader)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	if err := json.Unmarshal(frame.Result, &text); err != nil || frame.Event != "clipboard" || text != "live clipboard" {
		t.Fatalf("live effect = %#v, %q, %v", frame, text, err)
	}
}

func TestRendererReplacementWaitsForAdmittedPublication(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	supervisor := newRendererSupervisor("publication-fence", newRendererJournal(1024), defaultRendererProcessOptions(),
		func(uint64, rendererPublishedState, bool) { once.Do(func() { close(entered) }); <-release }, nil, nil, nil)
	t.Cleanup(supervisor.Close)
	supervisor.mu.Lock()
	supervisor.generation, supervisor.acceptFrames = 1, true
	supervisor.mu.Unlock()
	done := make(chan struct{})
	go func() {
		supervisor.handleFrame(rendererWorkerFrame{Type: rendererFrameSnapshot, Generation: 1, Result: json.RawMessage(`{}`)})
		close(done)
	}()
	<-entered
	restarted := make(chan struct{})
	go func() { supervisor.requestRestart(errors.New("test replacement")); close(restarted) }()
	select {
	case <-restarted:
		close(release)
		<-done
		t.Fatal("replacement invalidated an admitted publication before installation finished")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	<-done
	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("replacement did not resume after publication completed")
	}
}

func TestRendererReadUsesItsOwnBudgetWithoutAQueuedHealthTimeout(t *testing.T) {
	options := rendererFaultWorkerOptions(t, rendererFaultServe, "")
	options.CommandTimeout = 400 * time.Millisecond
	options.Env = append(options.Env, rendererFaultReadDelayEnv+"=1200ms")
	supervisor := newRendererSupervisor("slow-read", newRendererJournal(1024), options, nil, nil, nil, nil)
	t.Cleanup(supervisor.Close)
	if err := supervisor.Start(context.Background(), 80, 24); err != nil {
		t.Fatal(err)
	}
	value, err := supervisor.Read(ReadRecent, ReadANSI)
	if err != nil || value.Text != "read-result" {
		t.Fatalf("read within its longer budget = %#v, %v", value, err)
	}
	if status := supervisor.Status(); status.Generation != 1 || status.RestartCount != 0 {
		t.Fatalf("allowed read caused renderer replacement: %#v", status)
	}
}

func TestRendererShortRPCBehindLongReadCannotEndItsExecutionBudget(t *testing.T) {
	options := rendererFaultWorkerOptions(t, rendererFaultServe, "")
	options.CommandTimeout = 400 * time.Millisecond
	options.Env = append(options.Env, rendererFaultReadDelayEnv+"=1200ms")
	supervisor := newRendererSupervisor("read-before-short-call", newRendererJournal(1024), options, nil, nil, nil, nil)
	t.Cleanup(supervisor.Close)
	if err := supervisor.Start(context.Background(), 80, 24); err != nil {
		t.Fatal(err)
	}
	read := make(chan error, 1)
	go func() { _, err := supervisor.Read(ReadRecent, ReadANSI); read <- err }()
	waitForCondition(t, time.Second, func() bool {
		supervisor.mu.Lock()
		link := supervisor.link
		supervisor.mu.Unlock()
		return link != nil && link.Stats().CurrentOperation == "read"
	}, "long renderer read admission")
	// The caller may give up promptly. That does not imply the serial actor
	// stopped making progress: the earlier formatter still owns its budget.
	if _, err := supervisor.call("graphics", nil, 100*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("short caller deadline: %v", err)
	}
	if err := <-read; err != nil {
		t.Fatalf("queued short caller killed an allowed long read: %v", err)
	}
	if status := supervisor.Status(); status.Generation != 1 || status.RestartCount != 0 {
		t.Fatalf("read caused a false replacement: %+v", status)
	}
}

func TestRendererReadStillHasAHardDeadline(t *testing.T) {
	options := rendererFaultWorkerOptions(t, rendererFaultServe, "")
	options.CommandTimeout = 100 * time.Millisecond
	options.Env = append(options.Env, rendererFaultReadDelayEnv+"=5s")
	supervisor := newRendererSupervisor("stuck-read", newRendererJournal(1024), options, nil, nil, nil, nil)
	t.Cleanup(supervisor.Close)
	if err := supervisor.Start(context.Background(), 80, 24); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := supervisor.Read(ReadRecent, ReadANSI); err == nil {
		t.Fatal("read beyond its hard deadline succeeded")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("read deadline took %s", elapsed)
	}
	waitForCondition(t, time.Second, func() bool { return supervisor.Status().Generation > 1 }, "replacement after read deadline")
}

func TestRendererReadCancellationDetachesWaiterButRetainsDeadline(t *testing.T) {
	link := &rendererLink{
		commandTimeout: time.Second,
		control:        make(chan rendererWorkerFrame, 4), stop: make(chan struct{}), done: make(chan struct{}),
		pending: map[uint64]rendererPending{}, waiters: map[uint64]chan rendererCallOutcome{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := link.CallResult(ctx, "read", nil); done <- err }()
	frame := <-link.control
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read = %v", err)
	}
	link.mu.Lock()
	defer link.mu.Unlock()
	if len(link.waiters) != 0 {
		t.Fatal("canceled caller retained a response waiter")
	}
	if pending, ok := link.pending[frame.ID]; !ok || pending.Deadline.IsZero() {
		t.Fatal("abandoned worker operation no longer has a hard supervision deadline")
	}
}

func TestIsolatedConcurrentReadVariantsShareOneFetch(t *testing.T) {
	terminal, link := newIsolatedReadLinkFixture()
	started, release := make(chan struct{}), make(chan struct{})
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	var count atomic.Int32
	go func() {
		for {
			select {
			case <-stop:
				return
			case frame := <-link.control:
				if count.Add(1) == 1 {
					close(started)
					<-release
				}
				completeIsolatedReadFixture(link, frame.ID, ReadResultDTO{Text: "shared read", Generation: 1})
			}
		}
	}()
	const readers = 16
	results := make(chan ReadResult, readers)
	for range readers {
		go func() { results <- terminal.ReadRecent(ReadANSI) }()
	}
	<-started
	// Keep the first renderer read in flight while other callers reach the
	// cache; callers arriving after release must hit its completed memo.
	time.Sleep(25 * time.Millisecond)
	close(release)
	for range readers {
		result := <-results
		if result.Err != nil || result.Text != "shared read" {
			t.Fatalf("read result = %#v", result)
		}
	}
	if got := count.Load(); got != 1 {
		t.Fatalf("concurrent readers rendered %d variants, want one", got)
	}
}

func TestIsolatedReadDoesNotMemoizeADifferentRenderRevision(t *testing.T) {
	terminal, link := newIsolatedReadLinkFixture()
	done := make(chan ReadResult, 1)
	go func() { done <- terminal.ReadRecent(ReadANSI) }()
	frame := <-link.control
	completeIsolatedReadFixture(link, frame.ID, ReadResultDTO{Text: "new live output", Generation: 2})
	if result := <-done; result.Text != "new live output" {
		t.Fatalf("live read = %#v", result)
	}
	go func() { done <- terminal.ReadRecent(ReadANSI) }()
	select {
	case frame = <-link.control:
		completeIsolatedReadFixture(link, frame.ID, ReadResultDTO{Text: "new live output", Generation: 2})
	case result := <-done:
		t.Fatalf("different revision was reused as the old screen's memo: %#v", result)
	case <-time.After(time.Second):
		t.Fatal("read did not refetch after a revision mismatch")
	}
	<-done
}

func newIsolatedReadLinkFixture() (*isolatedDaemonTerminal, *rendererLink) {
	link := &rendererLink{
		commandTimeout: time.Second,
		control:        make(chan rendererWorkerFrame, 32), stop: make(chan struct{}), done: make(chan struct{}),
		pending: map[uint64]rendererPending{}, waiters: map[uint64]chan rendererCallOutcome{},
	}
	terminal := newIsolatedDaemonTerminalWithOptions("read-flight", nil, defaultRendererProcessOptions())
	terminal.renderer = &rendererSupervisor{link: link, acceptFrames: true, options: defaultRendererProcessOptions()}
	terminal.cache.Store(&isolatedTerminalCache{epoch: 1, state: rendererPublishedState{Snapshot: TerminalSnapshot{Generation: 1}}})
	return terminal, link
}

func completeIsolatedReadFixture(link *rendererLink, id uint64, value ReadResultDTO) {
	raw, _ := json.Marshal(value)
	link.mu.Lock()
	waiter := link.waiters[id]
	delete(link.pending, id)
	delete(link.waiters, id)
	link.mu.Unlock()
	if waiter != nil {
		waiter <- rendererCallOutcome{result: raw}
	}
}
