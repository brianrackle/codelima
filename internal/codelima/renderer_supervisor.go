//go:build cgo && (darwin || linux)

package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	defaultRendererCommandTimeout = 2 * time.Second
	defaultRendererQueueFrames    = 128
	defaultRendererHealthInterval = time.Second
	maxRendererRestarts           = 5
	rendererRestartWindow         = time.Minute
	rendererWorkerExecutableName  = "codelima-renderer-worker"

	// Restart policy (STABILITY_PLAN.md section 13). Degraded is a cooldown,
	// never a latch: an exhausted budget retries on rendererRestartCooldown and
	// a renderer that stays healthy for rendererHealthyResetPeriod earns its
	// full budget back.
	rendererRestartCooldown     = 30 * time.Second
	rendererHealthyResetPeriod  = time.Minute
	rendererRestartBackoffStep  = 25 * time.Millisecond
	rendererDegradedNoticeEvery = time.Second
	rendererSupervisorTick      = 100 * time.Millisecond

	// The init deadline has to cover replaying the retained journal, otherwise a
	// large journal makes every restart attempt time out and burns the whole
	// budget in about a second.
	rendererInitDeadlinePerByte = 2 * time.Microsecond
	maxRendererInitDeadline     = 15 * time.Second

	// rendererReadDeadlineFactor widens the command budget for on-demand read
	// variants, which render the whole viewport or the retained scrollback.
	rendererReadDeadlineFactor = 4

	rendererStateStarting   = "starting"
	rendererStateReady      = "ready"
	rendererStateRestarting = "restarting"
	rendererStateDegraded   = "degraded"
	rendererStateClosed     = "closed"
)

var (
	errRendererLinkClosed        = errors.New("renderer link closed")
	errRendererOutboundQueueFull = errors.New("renderer outbound queue full")
	errRendererDegraded          = errors.New("renderer is degraded and will be retried")
	errRendererUnavailable       = errors.New("renderer is being replaced")
)

// rendererRestartPolicy is the terminal-local restart budget. Every duration is
// injectable so fault-injection tests can drive the whole state machine without
// waiting on wall-clock cooldowns.
type rendererRestartPolicy struct {
	MaxRestarts    int
	Window         time.Duration
	Cooldown       time.Duration
	HealthyReset   time.Duration
	BackoffStep    time.Duration
	DegradedNotice time.Duration
	SendGrace      time.Duration
}

func defaultRendererRestartPolicy() rendererRestartPolicy {
	return rendererRestartPolicy{
		MaxRestarts:    maxRendererRestarts,
		Window:         rendererRestartWindow,
		Cooldown:       rendererRestartCooldown,
		HealthyReset:   rendererHealthyResetPeriod,
		BackoffStep:    rendererRestartBackoffStep,
		DegradedNotice: rendererDegradedNoticeEvery,
		SendGrace:      defaultRendererCommandTimeout,
	}
}

func (p rendererRestartPolicy) normalized(commandTimeout time.Duration) rendererRestartPolicy {
	defaults := defaultRendererRestartPolicy()
	if p.MaxRestarts <= 0 {
		p.MaxRestarts = defaults.MaxRestarts
	}
	if p.Window <= 0 {
		p.Window = defaults.Window
	}
	if p.Cooldown <= 0 {
		p.Cooldown = defaults.Cooldown
	}
	if p.HealthyReset <= 0 {
		p.HealthyReset = defaults.HealthyReset
	}
	if p.BackoffStep < 0 {
		p.BackoffStep = defaults.BackoffStep
	}
	if p.DegradedNotice <= 0 {
		p.DegradedNotice = defaults.DegradedNotice
	}
	if p.SendGrace <= 0 {
		p.SendGrace = commandTimeout
	}
	return p
}

type rendererProcessOptions struct {
	Executable     string
	Args           []string
	Env            []string
	CommandTimeout time.Duration
	QueueFrames    int
	Restart        rendererRestartPolicy
}

func defaultRendererProcessOptions() rendererProcessOptions {
	return rendererProcessOptions{
		CommandTimeout: defaultRendererCommandTimeout,
		QueueFrames:    defaultRendererQueueFrames,
		Restart:        defaultRendererRestartPolicy(),
	}
}

// rendererInitDeadline scales the startup budget with the journal a replacement
// renderer has to replay before it can answer. A fixed deadline turns a large
// journal into a guaranteed restart-budget wipeout.
func rendererInitDeadline(base time.Duration, journalBytes int) time.Duration {
	if base <= 0 {
		base = defaultRendererCommandTimeout
	}
	if journalBytes < 0 {
		journalBytes = 0
	}
	deadline := base + time.Duration(journalBytes)*rendererInitDeadlinePerByte
	if deadline > maxRendererInitDeadline {
		deadline = maxRendererInitDeadline
	}
	return deadline
}

type rendererSupervisorStatus struct {
	Generation       uint64
	PID              int
	State            string
	LastProgressAt   time.Time
	RestartCount     int
	JournalBytes     int
	PartialRecovery  bool
	LastError        string
	OutboundDepth    int
	PendingRequests  int
	OldestPending    time.Duration
	CurrentOperation string
}

type rendererSupervisor struct {
	terminalID string
	journal    *rendererJournal
	options    rendererProcessOptions
	policy     rendererRestartPolicy

	onSnapshot func(uint64, rendererPublishedState, bool)
	onPTYWrite func(uint64, uint64, uint32, []byte)
	onEvent    func(uint64, rendererWorkerFrame)
	onRestart  func()

	mu            sync.Mutex
	stateWake     chan struct{}
	link          *rendererLink
	generation    uint64
	replayThrough uint64
	acceptFrames  bool
	closed        bool
	degraded      bool
	degradedAt    time.Time
	cleanStarted  bool
	forceRestart  bool
	// protocolMismatch latches the one renderer failure that is a property of
	// the two binaries on disk rather than of this attempt. It suppresses the
	// automatic restart path entirely (see planRestartLocked) so a stale worker
	// cannot spin the budget, and is cleared by a successful start.
	protocolMismatch bool
	healthySince     time.Time
	lastNotice       time.Time
	restarts         []time.Time
	lastError        string
	lastProbe        time.Time
	restartWake      chan struct{}
	shutdown         chan struct{}
	done             chan struct{}
	status           atomic.Pointer[rendererSupervisorStatus]
}

func newRendererSupervisor(
	terminalID string,
	journal *rendererJournal,
	options rendererProcessOptions,
	onSnapshot func(uint64, rendererPublishedState, bool),
	onPTYWrite func(uint64, uint64, uint32, []byte),
	onEvent func(uint64, rendererWorkerFrame),
	onRestart func(),
) *rendererSupervisor {
	if options.CommandTimeout <= 0 {
		options.CommandTimeout = defaultRendererCommandTimeout
	}
	if options.QueueFrames <= 0 {
		options.QueueFrames = defaultRendererQueueFrames
	}
	supervisor := &rendererSupervisor{
		terminalID:  terminalID,
		journal:     journal,
		options:     options,
		policy:      options.Restart.normalized(options.CommandTimeout),
		onSnapshot:  onSnapshot,
		onPTYWrite:  onPTYWrite,
		onEvent:     onEvent,
		onRestart:   onRestart,
		stateWake:   make(chan struct{}),
		restartWake: make(chan struct{}, 1),
		shutdown:    make(chan struct{}),
		done:        make(chan struct{}),
	}
	supervisor.publishStatus(rendererStateStarting, 0)
	go supervisor.run()
	return supervisor
}

// wakeLocked releases every caller parked on a supervisor state change. The
// broadcast is a closed-channel generation rather than a sync.Cond so waiters
// can bound their wait (invariant I4: the PTY read pump never waits forever).
func (s *rendererSupervisor) wakeLocked() {
	close(s.stateWake)
	s.stateWake = make(chan struct{})
}

func (s *rendererSupervisor) Start(ctx context.Context, cols, rows int) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errTerminalClosed
	}
	s.mu.Unlock()
	return s.startRenderer(ctx, cols, rows)
}

func (s *rendererSupervisor) startRenderer(ctx context.Context, cols, rows int) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errTerminalClosed
	}
	s.generation++
	generation := s.generation
	s.mu.Unlock()
	link, err := startRendererLink(s.options, generation, s.handleFrame)
	if err != nil {
		s.mu.Lock()
		s.lastError = err.Error()
		s.publishStatusLocked(rendererStateRestarting, 0)
		s.mu.Unlock()
		return err
	}
	s.mu.Lock()
	if s.closed || generation != s.generation {
		s.mu.Unlock()
		link.Fail(errors.New("renderer generation superseded before startup"))
		return errors.New("renderer generation superseded before startup")
	}
	s.acceptFrames = true
	s.mu.Unlock()
	journal := s.journal.Snapshot()
	params := rendererInitParams{
		TerminalID: s.terminalID,
		Cols:       cols,
		Rows:       rows,
		Journal:    journal.Events,
	}
	initCtx, cancel := context.WithTimeout(ctx, rendererInitDeadline(s.options.CommandTimeout, journal.Bytes))
	defer cancel()
	// CallResult, not Call: the init reply is the version handshake, and the
	// link is not ready until the worker's protocol version has been checked
	// against this binary's. Everything after this point interprets the
	// worker's frames, so the check has to happen before the link is installed.
	raw, err := link.CallResult(initCtx, "init", params)
	if err == nil {
		err = verifyRendererProtocol(link.Executable(), raw)
	}
	if err != nil {
		link.Fail(err)
		mismatch := errors.Is(err, errRendererProtocolMismatch)
		s.mu.Lock()
		if generation == s.generation {
			s.acceptFrames = false
		}
		if mismatch {
			s.protocolMismatch = true
		}
		s.lastError = err.Error()
		s.publishStatusLocked(rendererStateRestarting, 0)
		s.wakeLocked()
		s.mu.Unlock()
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || generation != s.generation {
		link.Fail(errors.New("renderer generation superseded during startup"))
		return errors.New("renderer generation superseded during startup")
	}
	s.link = link
	if len(journal.Events) > 0 {
		s.replayThrough = journal.Events[len(journal.Events)-1].ID
	}
	now := time.Now()
	s.lastProbe = now
	s.healthySince = now
	s.degraded = false
	s.protocolMismatch = false
	s.lastError = ""
	s.publishStatusLocked(rendererStateReady, link.PID())
	s.wakeLocked()
	return nil
}

// SendOutput hands one journaled PTY event to the renderer. It never waits on a
// renderer that cannot make progress (invariant I4): a degraded renderer is
// reported immediately, and while a replacement is starting the caller is held
// for at most the send grace. The journal already owns the bytes, so a rejected
// send is replayed by the next renderer generation instead of being lost -- the
// PTY read pump must keep draining so the shell never blocks on write.
func (s *rendererSupervisor) SendOutput(event rendererJournalEvent) error {
	deadline := time.Now().Add(s.policy.SendGrace)
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return errTerminalClosed
		}
		if event.ID <= s.replayThrough {
			s.mu.Unlock()
			return nil
		}
		if s.degraded {
			err := s.noticeLocked(errRendererDegraded)
			s.mu.Unlock()
			return err
		}
		link := s.link
		sendable := link != nil && s.acceptFrames
		wake := s.stateWake
		s.mu.Unlock()

		if sendable {
			err := link.NotifyOutput(s.shutdown, rendererOutputParams{EventID: event.ID, Data: event.Data})
			if err == nil {
				return nil
			}
			if !errors.Is(err, errRendererLinkClosed) {
				return err
			}
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			s.mu.Lock()
			err := s.noticeLocked(errRendererUnavailable)
			s.mu.Unlock()
			return err
		}
		timer := time.NewTimer(remaining)
		select {
		case <-wake:
		case <-timer.C:
		case <-s.shutdown:
			timer.Stop()
			return errTerminalClosed
		}
		timer.Stop()
	}
}

// noticeLocked rate-limits the "renderer is not accepting output" error so a
// chatty shell cannot flood the event queue with one error per PTY read while
// the renderer is down. The durable signal is the published degraded status;
// this is only the visible nudge.
func (s *rendererSupervisor) noticeLocked(cause error) error {
	now := time.Now()
	if now.Sub(s.lastNotice) < s.policy.DegradedNotice {
		return nil
	}
	s.lastNotice = now
	return s.describeLocked(cause)
}

func (s *rendererSupervisor) describeLocked(cause error) error {
	if s.lastError == "" {
		return cause
	}
	return fmt.Errorf("%w: %s", cause, s.lastError)
}

func (s *rendererSupervisor) TryResize(event rendererJournalEvent) error {
	return s.trySend("resize", rendererResizeParams{EventID: event.ID, Cols: event.Cols, Rows: event.Rows})
}

func (s *rendererSupervisor) TryUpdate(event rendererInputEvent) error {
	return s.trySend("update", rendererUpdateParams{Event: event})
}

func (s *rendererSupervisor) TryFocus(focused bool) error {
	return s.trySend("focus", rendererInputEvent{Type: "focus", Focused: focused})
}

func (s *rendererSupervisor) TryScroll(delta int) error {
	return s.trySend("scroll", rendererInputEvent{Type: "scroll", Delta: delta})
}

func (s *rendererSupervisor) RequestSnapshot() error {
	return s.trySend("snapshot", nil)
}

// Read fetches one read variant from the renderer on demand. Variants other
// than the visible plain text are no longer recomputed and pushed with every
// published screen, so this is the only place their cost is paid -- once per
// terminal.read that actually wants one.
func (s *rendererSupervisor) Read(source ReadSource, format ReadFormat) (ReadResultDTO, error) {
	s.mu.Lock()
	link := s.link
	var reason error
	switch {
	case s.closed:
		reason = errTerminalClosed
	case s.degraded:
		reason = s.describeLocked(errRendererDegraded)
	case link == nil:
		reason = s.describeLocked(errRendererUnavailable)
	}
	s.mu.Unlock()
	if reason != nil {
		return ReadResultDTO{}, reason
	}
	// A recent read walks the retained scrollback one row at a time, so it gets
	// a more generous budget than a control call; the health probe, not this
	// deadline, is what detects a wedged renderer.
	ctx, cancel := context.WithTimeout(context.Background(), rendererReadDeadlineFactor*s.options.CommandTimeout)
	defer cancel()
	raw, err := link.CallResult(ctx, "read", rendererReadRequest(source, format))
	if err != nil {
		return ReadResultDTO{}, err
	}
	var result ReadResultDTO
	if err := json.Unmarshal(raw, &result); err != nil {
		return ReadResultDTO{}, err
	}
	return result, nil
}

// trySend delivers one semantic input or control notification, and is the whole
// reason the callers are spelled Try*: it must return promptly with an answer,
// never park the caller. Its callers are daemon RPC handlers
// (terminal.send_event, terminal.resize, terminal.focus) and the snapshot
// publisher -- goroutines that are either holding a client's request open or
// driving every terminal's publication -- so a caller that blocks here turns one
// slow renderer into a stalled client or a stalled publisher.
//
// It has two ways of not making progress and both are answered, not waited out:
//
//   - The supervisor mutex. Bounded and harmless: every critical section under
//     s.mu is pure bookkeeping, no I/O and no callbacks (the init call, the
//     close call, the restart backoff and notifyStale all run unlocked), so the
//     wait is a few instructions of another goroutine's bookkeeping.
//   - The link's outbound queue. NOT harmless: Notify parks until the writer
//     drains, which against a wedged worker lasts until the write deadline
//     expires and the link fails. TryNotify refuses instead, which matches what
//     the request path has always done (enqueue reports
//     errRendererOutboundQueueFull rather than queueing) and what the degraded
//     and being-replaced branches below already do: an explicit error the caller
//     can surface, rather than a silent drop or a blocked caller.
//
// Journaled PTY output deliberately does NOT come through here -- see
// SendOutput, whose bounded backpressure is the intended behaviour for a
// producer outrunning its renderer.
func (s *rendererSupervisor) trySend(method string, params any) error {
	s.mu.Lock()
	link := s.link
	closed := s.closed
	degraded := s.degraded
	var reason error
	switch {
	case closed:
		reason = errTerminalClosed
	case degraded:
		reason = s.describeLocked(errRendererDegraded)
	case link == nil:
		reason = s.describeLocked(errRendererUnavailable)
	}
	s.mu.Unlock()
	if reason != nil {
		return reason
	}
	return link.TryNotify(method, params)
}

// Restart forces an immediate renderer replacement, bypassing both the restart
// budget and the degraded cooldown. It is the manual renderer restart
// STABILITY_PLAN.md section 13 requires, and the degraded cooldown uses it to
// re-arm itself so no operator action is needed for recovery.
func (s *rendererSupervisor) Restart() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.forceRestart = true
	s.mu.Unlock()
	select {
	case s.restartWake <- struct{}{}:
	default:
	}
}

func (s *rendererSupervisor) handleFrame(frame rendererWorkerFrame) {
	s.mu.Lock()
	current := s.generation
	accept := s.acceptFrames && !s.closed
	s.mu.Unlock()
	if frame.Generation != current || !accept {
		return
	}
	switch frame.Type {
	case rendererFrameSnapshot:
		var state rendererPublishedState
		if json.Unmarshal(frame.Result, &state) == nil && s.onSnapshot != nil {
			// Stats, never Snapshot: this runs on every inbound frame and only
			// the partial-recovery flag is wanted, so deep-copying the retained
			// journal here would memcpy up to the whole retention budget per
			// published screen while the PTY pump waits on the same mutex.
			s.onSnapshot(frame.Generation, state, s.journal.Stats().Partial)
		}
	case rendererFramePTYWrite:
		var data []byte
		if json.Unmarshal(frame.Result, &data) == nil && s.onPTYWrite != nil {
			s.onPTYWrite(frame.Generation, frame.EventID, frame.Ordinal, data)
		}
	case rendererFrameEvent:
		if s.onEvent != nil {
			s.onEvent(frame.Generation, frame)
		}
	}
	s.publishStatus(rendererStateReady, 0)
}

func (s *rendererSupervisor) requestRestart(err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if !s.acceptFrames {
		s.mu.Unlock()
		return
	}
	if err != nil {
		s.lastError = err.Error()
	}
	s.acceptFrames = false
	s.publishStatusLocked(rendererStateRestarting, 0)
	s.wakeLocked()
	s.mu.Unlock()
	s.notifyStale()
	select {
	case s.restartWake <- struct{}{}:
	default:
	}
}

// notifyStale tells the terminal that its cached screen is no longer being
// updated. It must never run under the supervisor mutex: the callback repaints.
func (s *rendererSupervisor) notifyStale() {
	if s.onRestart != nil {
		s.onRestart()
	}
}

func (s *rendererSupervisor) run() {
	ticker := time.NewTicker(s.tickInterval())
	defer ticker.Stop()
	defer close(s.done)
	for {
		select {
		case <-s.shutdown:
			return
		case <-s.restartWake:
			s.restart()
		case <-ticker.C:
			if s.tick() {
				return
			}
		}
	}
}

func (s *rendererSupervisor) tickInterval() time.Duration {
	interval := rendererSupervisorTick
	if s.policy.Cooldown < interval {
		interval = max(s.policy.Cooldown, time.Millisecond)
	}
	return interval
}

// tick advances the supervisor's time-driven policy: liveness probing, the
// sustained-health budget reset, and the degraded cooldown retry. It reports
// whether the supervisor is closed and the run loop should exit.
func (s *rendererSupervisor) tick() bool {
	now := time.Now()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return true
	}
	link := s.link
	probeDue := link != nil && now.Sub(s.lastProbe) >= defaultRendererHealthInterval
	if probeDue {
		s.lastProbe = now
	}
	// A renderer that has run cleanly for a sustained healthy period earns its
	// whole budget back, including another poison-journal escape.
	if link != nil && s.acceptFrames && len(s.restarts) > 0 &&
		now.Sub(s.healthySince) >= s.policy.HealthyReset {
		s.restarts = nil
		s.cleanStarted = false
		s.publishStatusLocked(rendererStateReady, 0)
	}
	cooldownDue := s.degraded && now.Sub(s.degradedAt) >= s.policy.Cooldown
	s.mu.Unlock()

	if cooldownDue {
		// Degraded is a cooldown, not a latch (invariant I2).
		s.Restart()
		return false
	}
	if link == nil {
		return false
	}
	if probeDue {
		if _, err := link.TryCall("health", nil); err != nil {
			s.requestRestart(err)
			return false
		}
	}
	if err := link.HealthError(s.options.CommandTimeout); err != nil {
		s.requestRestart(err)
		return false
	}
	select {
	case <-link.Done():
		s.requestRestart(link.Failure())
	default:
	}
	return false
}

// rendererRestartPlan is the decision the restart budget produces for one
// attempt.
type rendererRestartPlan struct {
	// allowed is false only when the budget is spent and every escape has
	// already been tried; the supervisor then degrades and waits out the
	// cooldown.
	allowed bool
	// charge records the attempt against the rolling budget. Forced restarts
	// (cooldown retry, manual restart) and the one poison-journal escape are
	// deliberately uncharged.
	charge bool
	// clean drops the retained journal before replaying, because replaying it
	// is the only thing every failed attempt had in common.
	clean bool
}

func (s *rendererSupervisor) planRestartLocked(now time.Time, journal rendererJournalStats) rendererRestartPlan {
	cutoff := now.Add(-s.policy.Window)
	kept := s.restarts[:0]
	for _, at := range s.restarts {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	s.restarts = kept

	forced := s.forceRestart
	s.forceRestart = false

	if s.protocolMismatch {
		// A version mismatch is permanent, not transient: the worker executable
		// beside this binary is from another build, and the next spawn resolves
		// the same file and gets the same answer. Automatic restarts therefore
		// stop here -- degraded-with-cause immediately, budget untouched -- so a
		// stale install cannot burn five restarts per minute forever while the
		// terminal ends up degraded anyway.
		//
		// A forced attempt still gets exactly one uncharged retry. That is the
		// degraded cooldown and the operator's manual RestartRenderer, and it is
		// the only way the answer can change without a new daemon process: the
		// worker binary on disk may have been replaced since the last attempt.
		if !forced {
			return rendererRestartPlan{}
		}
		return rendererRestartPlan{allowed: true}
	}

	exhausted := len(s.restarts) >= s.policy.MaxRestarts
	poisonEscape := exhausted && journal.Events > 0 && !s.cleanStarted
	switch {
	case poisonEscape:
		return rendererRestartPlan{allowed: true, clean: true}
	case forced:
		return rendererRestartPlan{allowed: true}
	case exhausted:
		return rendererRestartPlan{}
	default:
		return rendererRestartPlan{allowed: true, charge: true}
	}
}

func (s *rendererSupervisor) restart() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	plan := s.planRestartLocked(now, s.journal.Stats())
	if !plan.allowed {
		s.enterDegradedLocked(now)
		s.mu.Unlock()
		s.notifyStale()
		return
	}
	if plan.charge {
		s.restarts = append(s.restarts, now)
	}
	if plan.clean {
		s.cleanStarted = true
	}
	attempt := len(s.restarts)
	old := s.link
	s.link = nil
	s.acceptFrames = false
	s.degraded = false
	s.publishStatusLocked(rendererStateRestarting, 0)
	s.wakeLocked()
	s.mu.Unlock()
	if old != nil {
		old.Fail(errors.New("renderer replaced by supervisor"))
	}
	// A forced or cooled-down restart has no requestRestart ahead of it, so the
	// cached screen has to be marked stale here too: the terminal stays visible,
	// it just stops being current.
	s.notifyStale()
	if plan.clean {
		// Every attempt so far died replaying this journal. A blank but live
		// terminal beats a dead one, so drop the history and start clean.
		s.journal.Reset()
	}

	if backoff := time.Duration(attempt-1) * s.policy.BackoffStep; backoff > 0 {
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-s.shutdown:
			timer.Stop()
			return
		}
	}

	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	journal := s.journal.Stats()
	cols, rows := journal.Cols, journal.Rows
	if cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	ctx, cancel := s.shutdownContext()
	defer cancel()
	if err := s.startRenderer(ctx, cols, rows); err != nil {
		select {
		case s.restartWake <- struct{}{}:
		default:
		}
	}
}

// shutdownContext bounds a restart attempt by terminal close. Init deadlines
// now scale with the journal, so Close must not have to wait one out.
func (s *rendererSupervisor) shutdownContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-s.shutdown:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// enterDegradedLocked parks the terminal in stale-but-alive mode: the shell and
// journal keep running, the last snapshot stays visible, output and input are
// rejected with a visible error, and the cooldown re-arms a retry.
func (s *rendererSupervisor) enterDegradedLocked(now time.Time) {
	if s.link != nil {
		s.link.Fail(errors.New("renderer restart budget exhausted"))
		s.link = nil
	}
	s.acceptFrames = false
	s.degraded = true
	s.degradedAt = now
	// The transition itself is always worth one error, whatever the notice
	// throttle was doing during the restart storm.
	s.lastNotice = time.Time{}
	if s.lastError == "" {
		s.lastError = "renderer restart budget exhausted"
	}
	s.publishStatusLocked(rendererStateDegraded, 0)
	s.wakeLocked()
}

func (s *rendererSupervisor) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		<-s.done
		return
	}
	s.closed = true
	s.acceptFrames = false
	s.degraded = false
	close(s.shutdown)
	s.wakeLocked()
	link := s.link
	s.link = nil
	s.publishStatusLocked(rendererStateClosed, 0)
	s.mu.Unlock()
	if link != nil {
		ctx, cancel := context.WithTimeout(context.Background(), s.options.CommandTimeout)
		_ = link.Call(ctx, "close", nil)
		cancel()
		link.Fail(errTerminalClosed)
	}
	<-s.done
}

func (s *rendererSupervisor) Status() rendererSupervisorStatus {
	if status := s.status.Load(); status != nil {
		return *status
	}
	return rendererSupervisorStatus{State: "unknown"}
}

// publishStatus republishes the status from outside the supervisor mutex. The
// last error is owned by the paths that produce one (startRenderer,
// requestRestart, enterDegradedLocked); this entry point only restates the
// current state.
func (s *rendererSupervisor) publishStatus(state string, pid int) {
	s.mu.Lock()
	s.publishStatusLocked(state, pid)
	s.mu.Unlock()
}

// publishStatusLocked republishes the supervisor status snapshot. It runs on
// every inbound renderer frame, so it reads the journal through Stats() -- the
// status only reports retained bytes and the partial-recovery flag, and a deep
// journal copy here cost roughly one journal-sized memcpy per PTY frame while
// holding the journal mutex the PTY read pump needs to append.
func (s *rendererSupervisor) publishStatusLocked(state string, pid int) {
	journal := s.journal.Stats()
	var linkStats rendererLinkStats
	if pid == 0 && s.link != nil {
		pid = s.link.PID()
	}
	if s.link != nil {
		linkStats = s.link.Stats()
	}
	s.status.Store(&rendererSupervisorStatus{
		Generation:       s.generation,
		PID:              pid,
		State:            state,
		LastProgressAt:   time.Now().UTC(),
		RestartCount:     len(s.restarts),
		JournalBytes:     journal.Bytes,
		PartialRecovery:  journal.Partial,
		LastError:        s.lastError,
		OutboundDepth:    linkStats.OutboundDepth,
		PendingRequests:  linkStats.PendingRequests,
		OldestPending:    linkStats.OldestPending,
		CurrentOperation: linkStats.CurrentOperation,
	})
}

type rendererLinkStats struct {
	OutboundDepth    int
	PendingRequests  int
	OldestPending    time.Duration
	CurrentOperation string
}

type rendererLink struct {
	generation uint64
	conn       net.Conn
	command    *exec.Cmd
	// executable is the worker path this link actually spawned, retained so a
	// protocol-version mismatch can name the stale file instead of leaving the
	// operator to guess which of the two binaries is wrong.
	executable string
	onFrame    func(rendererWorkerFrame)

	outbound chan rendererWorkerFrame
	control  chan rendererWorkerFrame
	done     chan struct{}
	stop     chan struct{}
	failOnce sync.Once

	nextID  atomic.Uint64
	mu      sync.Mutex
	pending map[uint64]rendererPending
	waiters map[uint64]chan rendererCallOutcome
	failure error
}

// rendererCallOutcome is one completed request/response exchange. Calls that
// only need success keep using Call; Read needs the payload, so the waiter
// carries both.
type rendererCallOutcome struct {
	result json.RawMessage
	err    error
}

func startRendererLink(options rendererProcessOptions, generation uint64, onFrame func(rendererWorkerFrame)) (*rendererLink, error) {
	executable := options.Executable
	if executable == "" {
		var err error
		executable, err = resolveRendererWorkerExecutable()
		if err != nil {
			return nil, err
		}
	}
	args := options.Args
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("create renderer control socket: %w", err)
	}
	unix.CloseOnExec(pair[0])
	unix.CloseOnExec(pair[1])
	parentFile := os.NewFile(uintptr(pair[0]), "codelima-renderer-parent")
	childFile := os.NewFile(uintptr(pair[1]), "codelima-renderer-child")
	command := exec.Command(executable, args...)
	command.ExtraFiles = []*os.File{childFile}
	command.Env = append(os.Environ(), options.Env...)
	command.Stderr = os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		_ = parentFile.Close()
		_ = childFile.Close()
		return nil, fmt.Errorf("start renderer worker: %w", err)
	}
	_ = childFile.Close()
	conn, err := net.FileConn(parentFile)
	_ = parentFile.Close()
	if err != nil {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
		return nil, fmt.Errorf("open renderer control socket: %w", err)
	}
	link := &rendererLink{
		generation: generation,
		conn:       conn,
		command:    command,
		executable: executable,
		onFrame:    onFrame,
		outbound:   make(chan rendererWorkerFrame, options.QueueFrames),
		control:    make(chan rendererWorkerFrame, options.QueueFrames),
		done:       make(chan struct{}),
		stop:       make(chan struct{}),
		pending:    make(map[uint64]rendererPending),
		waiters:    make(map[uint64]chan rendererCallOutcome),
	}
	go link.writer()
	go link.reader()
	go func() {
		err := command.Wait()
		link.Fail(fmt.Errorf("renderer worker exited: %w", err))
	}()
	return link, nil
}

func resolveRendererWorkerExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve CodeLima executable: %w", err)
	}
	renderer := rendererWorkerPathBeside(executable)
	info, err := os.Stat(renderer)
	if err != nil {
		return "", fmt.Errorf("locate renderer worker %q: %w", renderer, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("renderer worker %q is not executable", renderer)
	}
	return renderer, nil
}

func rendererWorkerPathBeside(executable string) string {
	return filepath.Join(filepath.Dir(executable), rendererWorkerExecutableName)
}

// Executable reports the worker path this link spawned.
func (l *rendererLink) Executable() string {
	if l == nil {
		return rendererWorkerExecutableName
	}
	return l.executable
}

func (l *rendererLink) PID() int {
	if l == nil || l.command == nil || l.command.Process == nil {
		return 0
	}
	return l.command.Process.Pid
}

func (l *rendererLink) TryCall(method string, params any) (uint64, error) {
	return l.enqueue(method, params, nil)
}

// Notify queues one fire-and-forget request, waiting for room in the outbound
// queue. Waiting is the point: this is the journaled-PTY-output path, where a
// producer outrunning its renderer must be pushed back on rather than dropped.
// Every other caller wants TryNotify.
func (l *rendererLink) Notify(stop <-chan struct{}, method string, params any) error {
	frame, err := l.notifyFrame(method, params)
	if err != nil {
		return err
	}
	select {
	case l.outbound <- frame:
		return nil
	case <-l.stop:
		return errRendererLinkClosed
	case <-stop:
		return errTerminalClosed
	}
}

// TryNotify queues one fire-and-forget request without ever waiting. A full
// outbound queue is reported as errRendererOutboundQueueFull, the same answer
// the request path gives, so a caller that must not park (see trySend) always
// gets one.
func (l *rendererLink) TryNotify(method string, params any) error {
	frame, err := l.notifyFrame(method, params)
	if err != nil {
		return err
	}
	select {
	case l.outbound <- frame:
		return nil
	case <-l.stop:
		return errRendererLinkClosed
	default:
		return errRendererOutboundQueueFull
	}
}

func (l *rendererLink) notifyFrame(method string, params any) (rendererWorkerFrame, error) {
	raw, err := marshalRendererParams(params)
	if err != nil {
		return rendererWorkerFrame{}, err
	}
	return rendererWorkerFrame{
		Type:       rendererFrameRequest,
		ID:         l.nextID.Add(1),
		Generation: l.generation,
		NoReply:    true,
		Method:     method,
		Params:     raw,
	}, nil
}

func (l *rendererLink) NotifyOutput(stop <-chan struct{}, params rendererOutputParams) error {
	return l.Notify(stop, "output", params)
}

func (l *rendererLink) Call(ctx context.Context, method string, params any) error {
	_, err := l.CallResult(ctx, method, params)
	return err
}

func (l *rendererLink) CallResult(ctx context.Context, method string, params any) (json.RawMessage, error) {
	waiter := make(chan rendererCallOutcome, 1)
	_, err := l.enqueue(method, params, waiter)
	if err != nil {
		return nil, err
	}
	select {
	case outcome := <-waiter:
		return outcome.result, outcome.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.done:
		return nil, l.Failure()
	}
}

func (l *rendererLink) enqueue(method string, params any, waiter chan rendererCallOutcome) (uint64, error) {
	raw, err := marshalRendererParams(params)
	if err != nil {
		return 0, err
	}
	id := l.nextID.Add(1)
	frame := rendererWorkerFrame{
		Type:       rendererFrameRequest,
		ID:         id,
		Generation: l.generation,
		Method:     method,
		Params:     raw,
	}
	l.mu.Lock()
	l.pending[id] = rendererPending{StartedAt: time.Now(), Method: method}
	if waiter != nil {
		l.waiters[id] = waiter
	}
	l.mu.Unlock()
	select {
	case l.control <- frame:
		return id, nil
	case <-l.stop:
		l.removePending(id)
		return 0, errRendererLinkClosed
	default:
		l.removePending(id)
		return 0, errRendererOutboundQueueFull
	}
}

func (l *rendererLink) writer() {
	for {
		var frame rendererWorkerFrame
		select {
		case frame = <-l.control:
		default:
			select {
			case frame = <-l.control:
			case frame = <-l.outbound:
			case <-l.stop:
				return
			}
		}
		_ = l.conn.SetWriteDeadline(time.Now().Add(defaultRendererCommandTimeout))
		if err := writeRendererFrame(l.conn, frame); err != nil {
			l.Fail(fmt.Errorf("write renderer frame: %w", err))
			return
		}
	}
}

func (l *rendererLink) reader() {
	for {
		frame, err := readRendererFrame(l.conn)
		if err != nil {
			l.Fail(fmt.Errorf("read renderer frame: %w", err))
			return
		}
		if frame.Generation != l.generation {
			continue
		}
		if frame.Type == rendererFrameResponse {
			l.mu.Lock()
			delete(l.pending, frame.ID)
			waiter := l.waiters[frame.ID]
			delete(l.waiters, frame.ID)
			l.mu.Unlock()
			if l.onFrame != nil {
				l.onFrame(frame)
			}
			if waiter != nil {
				outcome := rendererCallOutcome{result: frame.Result}
				if frame.Error != "" {
					outcome.err = errors.New(frame.Error)
				}
				waiter <- outcome
			}
			continue
		}
		if l.onFrame != nil {
			l.onFrame(frame)
		}
	}
}

func (l *rendererLink) HealthError(timeout time.Duration) error {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, pending := range l.pending {
		if now.Sub(pending.StartedAt) > timeout {
			return fmt.Errorf("renderer request %d exceeded %s", id, timeout)
		}
	}
	return nil
}

func (l *rendererLink) Stats() rendererLinkStats {
	now := time.Now()
	stats := rendererLinkStats{OutboundDepth: len(l.control) + len(l.outbound)}
	l.mu.Lock()
	defer l.mu.Unlock()
	stats.PendingRequests = len(l.pending)
	for _, pending := range l.pending {
		age := now.Sub(pending.StartedAt)
		if age > stats.OldestPending {
			stats.OldestPending = age
			stats.CurrentOperation = pending.Method
		}
	}
	return stats
}

func (l *rendererLink) Done() <-chan struct{} {
	return l.done
}

func (l *rendererLink) Failure() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failure == nil {
		return errors.New("renderer link closed")
	}
	return l.failure
}

func (l *rendererLink) Fail(err error) {
	l.failOnce.Do(func() {
		close(l.stop)
		_ = l.conn.Close()
		if l.command != nil && l.command.Process != nil {
			_ = l.command.Process.Kill()
		}
		l.mu.Lock()
		l.failure = err
		for id, waiter := range l.waiters {
			delete(l.waiters, id)
			if waiter != nil {
				waiter <- rendererCallOutcome{err: err}
			}
		}
		l.pending = map[uint64]rendererPending{}
		l.mu.Unlock()
		close(l.done)
	})
}

type rendererPending struct {
	StartedAt time.Time
	Method    string
}

func (l *rendererLink) removePending(id uint64) {
	l.mu.Lock()
	delete(l.pending, id)
	delete(l.waiters, id)
	l.mu.Unlock()
}
