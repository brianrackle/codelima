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
)

var (
	errRendererLinkClosed        = errors.New("renderer link closed")
	errRendererOutboundQueueFull = errors.New("renderer outbound queue full")
)

type rendererProcessOptions struct {
	Executable     string
	Args           []string
	Env            []string
	CommandTimeout time.Duration
	QueueFrames    int
}

func defaultRendererProcessOptions() rendererProcessOptions {
	return rendererProcessOptions{
		CommandTimeout: defaultRendererCommandTimeout,
		QueueFrames:    defaultRendererQueueFrames,
	}
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

	onSnapshot func(uint64, rendererPublishedState, bool)
	onPTYWrite func(uint64, uint64, uint32, []byte)
	onEvent    func(uint64, rendererWorkerFrame)
	onRestart  func()

	mu            sync.Mutex
	ready         *sync.Cond
	link          *rendererLink
	generation    uint64
	replayThrough uint64
	acceptFrames  bool
	closed        bool
	restarts      []time.Time
	lastError     string
	lastProbe     time.Time
	restartWake   chan struct{}
	shutdown      chan struct{}
	done          chan struct{}
	status        atomic.Pointer[rendererSupervisorStatus]
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
		onSnapshot:  onSnapshot,
		onPTYWrite:  onPTYWrite,
		onEvent:     onEvent,
		onRestart:   onRestart,
		restartWake: make(chan struct{}, 1),
		shutdown:    make(chan struct{}),
		done:        make(chan struct{}),
	}
	supervisor.ready = sync.NewCond(&supervisor.mu)
	supervisor.publishStatus("starting", 0, "")
	go supervisor.run()
	return supervisor
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
		s.publishStatusLocked("degraded", 0)
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
	initCtx, cancel := context.WithTimeout(ctx, s.options.CommandTimeout)
	defer cancel()
	if err := link.Call(initCtx, "init", params); err != nil {
		link.Fail(err)
		s.mu.Lock()
		if generation == s.generation {
			s.acceptFrames = false
		}
		s.lastError = err.Error()
		s.publishStatusLocked("degraded", 0)
		s.ready.Broadcast()
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
	s.lastProbe = time.Now()
	s.lastError = ""
	s.publishStatusLocked("ready", link.PID())
	s.ready.Broadcast()
	return nil
}

func (s *rendererSupervisor) SendOutput(event rendererJournalEvent) error {
	for {
		s.mu.Lock()
		for !s.closed && (s.link == nil || !s.acceptFrames) {
			s.ready.Wait()
		}
		if s.closed {
			s.mu.Unlock()
			return errTerminalClosed
		}
		if event.ID <= s.replayThrough {
			s.mu.Unlock()
			return nil
		}
		link := s.link
		s.mu.Unlock()

		err := link.NotifyOutput(s.shutdown, rendererOutputParams{EventID: event.ID, Data: event.Data})
		if err == nil {
			return nil
		}
		if !errors.Is(err, errRendererLinkClosed) {
			return err
		}

		s.mu.Lock()
		for !s.closed && s.link == link && s.acceptFrames {
			s.ready.Wait()
		}
		s.mu.Unlock()
	}
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

func (s *rendererSupervisor) trySend(method string, params any) error {
	s.mu.Lock()
	link := s.link
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errTerminalClosed
	}
	if link == nil {
		return errors.New("renderer is not connected")
	}
	return link.Notify(s.shutdown, method, params)
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
			journal := s.journal.Snapshot()
			s.onSnapshot(frame.Generation, state, journal.Partial)
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
	s.publishStatus("ready", 0, "")
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
	s.publishStatusLocked("restarting", 0)
	s.ready.Broadcast()
	onRestart := s.onRestart
	s.mu.Unlock()
	if onRestart != nil {
		onRestart()
	}
	select {
	case s.restartWake <- struct{}{}:
	default:
	}
}

func (s *rendererSupervisor) run() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	defer close(s.done)
	for {
		select {
		case <-s.shutdown:
			return
		case <-s.restartWake:
			s.restart()
		case <-ticker.C:
			s.mu.Lock()
			link := s.link
			closed := s.closed
			probeDue := link != nil && time.Since(s.lastProbe) >= defaultRendererHealthInterval
			if probeDue {
				s.lastProbe = time.Now()
			}
			s.mu.Unlock()
			if closed {
				return
			}
			if link != nil {
				if probeDue {
					if _, err := link.TryCall("health", nil); err != nil {
						s.requestRestart(err)
						continue
					}
				}
				if err := link.HealthError(s.options.CommandTimeout); err != nil {
					s.requestRestart(err)
				}
				select {
				case <-link.Done():
					s.requestRestart(link.Failure())
				default:
				}
			}
		}
	}
}

func (s *rendererSupervisor) restart() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	cutoff := now.Add(-rendererRestartWindow)
	kept := s.restarts[:0]
	for _, at := range s.restarts {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	s.restarts = kept
	if len(s.restarts) >= maxRendererRestarts {
		if s.link != nil {
			s.link.Fail(errors.New("renderer restart budget exhausted"))
			s.link = nil
		}
		s.publishStatusLocked("degraded", 0)
		s.ready.Broadcast()
		s.mu.Unlock()
		return
	}
	s.restarts = append(s.restarts, now)
	old := s.link
	s.link = nil
	s.acceptFrames = false
	s.publishStatusLocked("restarting", 0)
	s.ready.Broadcast()
	s.mu.Unlock()
	if old != nil {
		old.Fail(errors.New("renderer replaced by supervisor"))
	}

	backoff := time.Duration(len(s.restarts)-1) * 25 * time.Millisecond
	if backoff > 0 {
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-s.shutdown:
			timer.Stop()
			return
		}
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	journal := s.journal.Snapshot()
	cols, rows := 80, 24
	for _, event := range journal.Events {
		if event.Type == "resize" && event.Cols > 0 && event.Rows > 0 {
			cols, rows = event.Cols, event.Rows
		}
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), s.options.CommandTimeout)
	defer cancel()
	if err := s.startRenderer(ctx, cols, rows); err != nil {
		select {
		case s.restartWake <- struct{}{}:
		default:
		}
	}
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
	close(s.shutdown)
	s.ready.Broadcast()
	link := s.link
	s.link = nil
	s.publishStatusLocked("closed", 0)
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

func (s *rendererSupervisor) publishStatus(state string, pid int, lastError string) {
	s.mu.Lock()
	if lastError != "" {
		s.lastError = lastError
	}
	s.publishStatusLocked(state, pid)
	s.mu.Unlock()
}

func (s *rendererSupervisor) publishStatusLocked(state string, pid int) {
	journal := s.journal.Snapshot()
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
	onFrame    func(rendererWorkerFrame)

	outbound chan rendererWorkerFrame
	control  chan rendererWorkerFrame
	done     chan struct{}
	stop     chan struct{}
	failOnce sync.Once

	nextID  atomic.Uint64
	mu      sync.Mutex
	pending map[uint64]rendererPending
	waiters map[uint64]chan error
	failure error
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
		onFrame:    onFrame,
		outbound:   make(chan rendererWorkerFrame, options.QueueFrames),
		control:    make(chan rendererWorkerFrame, options.QueueFrames),
		done:       make(chan struct{}),
		stop:       make(chan struct{}),
		pending:    make(map[uint64]rendererPending),
		waiters:    make(map[uint64]chan error),
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

func (l *rendererLink) PID() int {
	if l == nil || l.command == nil || l.command.Process == nil {
		return 0
	}
	return l.command.Process.Pid
}

func (l *rendererLink) TryCall(method string, params any) (uint64, error) {
	return l.enqueue(method, params, nil)
}

func (l *rendererLink) Notify(stop <-chan struct{}, method string, params any) error {
	raw, err := marshalRendererParams(params)
	if err != nil {
		return err
	}
	id := l.nextID.Add(1)
	frame := rendererWorkerFrame{
		Type:       rendererFrameRequest,
		ID:         id,
		Generation: l.generation,
		NoReply:    true,
		Method:     method,
		Params:     raw,
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

func (l *rendererLink) NotifyOutput(stop <-chan struct{}, params rendererOutputParams) error {
	return l.Notify(stop, "output", params)
}

func (l *rendererLink) Call(ctx context.Context, method string, params any) error {
	waiter := make(chan error, 1)
	_, err := l.enqueue(method, params, waiter)
	if err != nil {
		return err
	}
	select {
	case err := <-waiter:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-l.done:
		return l.Failure()
	}
}

func (l *rendererLink) enqueue(method string, params any, waiter chan error) (uint64, error) {
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
				if frame.Error != "" {
					waiter <- errors.New(frame.Error)
				} else {
					waiter <- nil
				}
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
				waiter <- err
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
