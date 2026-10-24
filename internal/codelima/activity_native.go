package codelima

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/hostinfo"
)

const nativeActivityQueueLimit = 256

// Observers enqueue bounded immutable transitions, never journal I/O. One
// worker persists them after Server.Run owns the process lock. The host update
// barrier fences every write across handoff; a retry retains its original ID.
type daemonActivityEvents struct {
	host       *daemonHost
	mu         sync.Mutex
	pending    []activityMessageRequest
	dropped    uint64
	routes     forwardingActivitySnapshot
	cpuOver    bool
	memoryOver bool
	flushMu    sync.Mutex
	writeError bool
	owned      atomic.Bool
	wake       chan struct{}
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

func newDaemonActivityEvents(host *daemonHost) *daemonActivityEvents {
	return &daemonActivityEvents{host: host, wake: make(chan struct{}, 1)}
}

func (h *daemonHost) startActivityEvents(ctx context.Context) {
	e := h.activityEvents
	if !e.owned.CompareAndSwap(false, true) {
		return
	}
	ctx, e.cancel = context.WithCancel(ctx)
	e.wg.Add(2)
	go e.run(ctx)
	go e.observeHost(ctx)
}

func (e *daemonActivityEvents) notify() {
	if e == nil {
		return
	}
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *daemonActivityEvents) enqueue(request activityMessageRequest) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.enqueueLocked(request)
}

func (e *daemonActivityEvents) enqueueLocked(request activityMessageRequest) {
	if len(e.pending) >= nativeActivityQueueLimit {
		e.dropped++
		return
	}
	if request.ID == "" {
		request.ID = newID()
	}
	request.Text = boundedOperationText(plainLogText(request.Text), 4096)
	request.NodeSlug = boundedOperationText(plainLogText(request.NodeSlug), 128)
	request, err := normalizeActivityMessage(request)
	if err != nil {
		e.host.service.log().Warn("native Activity event rejected", "error", err)
		return
	}
	e.pending = append(e.pending, request)
	e.notify()
}

func (e *daemonActivityEvents) run(ctx context.Context) {
	defer e.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-e.wake:
		}
		e.flush()
	}
}

func (e *daemonActivityEvents) observeHost(ctx context.Context) {
	defer e.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if e.host.serving.Load() && e.host.updateGate.TryRLock() {
			if !e.host.replaced.Load() {
				sampleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				nodes, err := e.host.service.NodeList(sampleCtx, false)
				cancel()
				if err == nil {
					e.observeCapacity(hostinfo.Read(""), nodes)
				}
			}
			e.host.updateGate.RUnlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// flush uses TryRLock so a writer waiting for update completion cannot prevent
// shutdown. It never holds the producer mutex while opening or writing files.
func (e *daemonActivityEvents) flush() bool {
	e.flushMu.Lock()
	defer e.flushMu.Unlock()
	h := e.host
	if !e.owned.Load() || !h.serving.Load() || !h.updateGate.TryRLock() {
		return false
	}
	defer h.updateGate.RUnlock()
	if h.replaced.Load() {
		return false
	}
	progress := false
	for range 64 {
		e.mu.Lock()
		if e.dropped > 0 && len(e.pending) < nativeActivityQueueLimit {
			dropped := e.dropped
			e.dropped = 0
			e.enqueueLocked(activityMessageRequest{Source: "daemon", Level: slog.LevelWarn, Text: fmt.Sprintf("%d native Activity events were omitted while the persistence queue was full", dropped)})
		}
		if len(e.pending) == 0 {
			e.mu.Unlock()
			return progress
		}
		request := e.pending[0]
		e.mu.Unlock()
		journal, err := h.activityJournal()
		var message ActivityMessage
		if err == nil {
			message, err = journal.Append(request)
		}
		if err != nil {
			if !e.writeError {
				h.service.log().Warn("native Activity persistence unavailable; retaining bounded pending events", "error", err)
			}
			e.writeError = true
			return progress
		}
		e.writeError = false
		e.mu.Lock()
		e.pending[0] = activityMessageRequest{}
		e.pending = e.pending[1:]
		e.mu.Unlock()
		progress = true
		h.broadcast(daemon.EventActivityMessage, activityMessageAck{Message: message, Epoch: journal.epoch})
	}
	return progress
}

func (e *daemonActivityEvents) Close() {
	if e == nil || e.cancel == nil {
		return
	}
	e.cancel()
	e.wg.Wait()
	for range nativeActivityQueueLimit/64 + 2 {
		if !e.flush() {
			break
		}
	}
	e.owned.Store(false)
}

func (e *daemonActivityEvents) observeCapacity(capacity hostinfo.Snapshot, nodes []Node) {
	a := summarizeHostAllocations(nodes)
	e.mu.Lock()
	defer e.mu.Unlock()
	observe := func(over *bool, used, total uint64, name, detail string) {
		if total == 0 || (used <= total && a.UnknownNodes > 0) {
			return
		}
		next := used > total
		if next == *over {
			return
		}
		*over = next
		level, text := slog.LevelInfo, name+" allocation within capacity"
		if next {
			level, text = slog.LevelWarn, name+" overcommitted"
		}
		e.enqueueLocked(activityMessageRequest{Source: "host", Level: level, Text: text + " · " + detail})
	}
	if capacity.CPUs > 0 {
		observe(&e.cpuOver, a.AllocatedCPUs, uint64(capacity.CPUs), "vCPU", fmt.Sprintf("%d of %d allocated", a.AllocatedCPUs, capacity.CPUs))
	}
	if capacity.MemoryBytes != nil {
		observe(&e.memoryOver, a.AllocatedMemoryBytes, *capacity.MemoryBytes, "memory", fmt.Sprintf("%.1f of %.1f GiB allocated", float64(a.AllocatedMemoryBytes)/(1<<30), float64(*capacity.MemoryBytes)/(1<<30)))
	}
}
