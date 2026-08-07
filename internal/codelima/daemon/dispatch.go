package daemon

import (
	"context"
	"encoding/json"
	"sync"
)

// DeliveryClass is the scheduling class of a daemon method. A connection is a
// transport boundary, not a serialization boundary: each class runs on its own
// lane so a slow control operation can never delay input delivery or another
// terminal's requests on the same connection.
type DeliveryClass uint8

const (
	// ClassQuery is a read-only request. Queries carry no ordering
	// requirement and are dispatched concurrently under a bounded in-flight
	// budget.
	ClassQuery DeliveryClass = iota
	// ClassInput is an at-most-once ordered mutation: keys, paste, mouse and
	// scroll. Every terminal keeps its own serial lane, so input ordering is
	// preserved per terminal without ever sharing a lane with control work.
	ClassInput
	// ClassReplaceable is latest-value state: resize and focus. A newer value
	// supersedes an older queued one; every superseded request still observes
	// the winning result.
	ClassReplaceable
	// ClassOwnership is a change of the input-ownership lease. It is applied in
	// connection read order so a following input frame can never race ahead of
	// the takeover it depends on. Its handling must stay non-blocking: it runs
	// on the connection reader.
	ClassOwnership
	// ClassControl is an idempotent keyed mutation whose ordering is already
	// protected by the handler's own state lock, so it is dispatched
	// concurrently and may complete its slow work after replying.
	ClassControl
	// ClassLifecycle is control work that legitimately runs for minutes (node
	// boot, live update). It is dispatched concurrently and uses the lifecycle
	// timeout class instead of the short request timeout.
	ClassLifecycle
)

// ClassifyMethod is the single definition of daemon method scheduling and
// ownership semantics. Both the server's lane router and the client's delivery
// outcome classification read it, so the two can no longer drift.
func ClassifyMethod(method string) DeliveryClass {
	switch method {
	case "terminal.send_text", "terminal.send_keys", "terminal.send_input", "terminal.send_event", "terminal.scroll":
		return ClassInput
	case "terminal.resize", "terminal.focus":
		return ClassReplaceable
	case "input.takeover":
		return ClassOwnership
	case "terminal.open", "terminal.close", "terminal.move", "terminal.restart_renderer":
		return ClassControl
	case "node.start", "node.stop", "daemon.update":
		return ClassLifecycle
	default:
		return ClassQuery
	}
}

// MutatingInputMethod reports whether a method changes daemon-owned state and
// therefore requires the input-ownership lease.
func MutatingInputMethod(method string) bool {
	return ClassifyMethod(method) != ClassQuery
}

// maxLanesPerConnection bounds per-terminal lane growth in each class. A real
// client keeps one lane per open tab; beyond this bound further terminal ids
// share their class's connection-wide lane instead of allocating unbounded
// goroutines.
const maxLanesPerConnection = 64

// connectionLanes routes one connection's requests to delivery-class lanes.
// Every entry point is non-blocking so the connection's reader never waits on a
// handler.
type connectionLanes struct {
	server      *Server
	client      *clientConn
	ctx         context.Context
	eventSocket bool
	queueSize   int

	query   chan struct{}
	control chan struct{}

	mu     sync.Mutex
	closed bool
	input  map[string]chan Request
	latest map[string]*latestValueLane
}

func newConnectionLanes(server *Server, client *clientConn, ctx context.Context, eventSocket bool) *connectionLanes {
	return &connectionLanes{
		server:      server,
		client:      client,
		ctx:         ctx,
		eventSocket: eventSocket,
		queueSize:   server.cfg.MutationQueueSize,
		query:       make(chan struct{}, server.cfg.MaxInFlight),
		control:     make(chan struct{}, server.cfg.MaxInFlight),
		input:       map[string]chan Request{},
		latest:      map[string]*latestValueLane{},
	}
}

func (l *connectionLanes) dispatch(request Request) {
	switch ClassifyMethod(request.Method) {
	case ClassInput:
		l.dispatchInput(request)
	case ClassReplaceable:
		l.dispatchLatestValue(request)
	case ClassOwnership:
		// Applied inline on the connection reader: ownership is a read-order
		// fact, and the handler only exchanges a lease under the server lock.
		l.server.dispatchRequest(l.ctx, l.client, request, l.eventSocket)
	case ClassControl, ClassLifecycle:
		l.dispatchConcurrent(l.control, request)
	default:
		l.dispatchConcurrent(l.query, request)
	}
}

// close stops accepting new lane work. Running lanes observe the connection
// context and exit on their own; the connection is never held open waiting for
// a wedged terminal runtime.
func (l *connectionLanes) close() {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
}

func (l *connectionLanes) dispatchConcurrent(slots chan struct{}, request Request) {
	select {
	case slots <- struct{}{}:
		go func() {
			defer func() { <-slots }()
			l.server.dispatchRequest(l.ctx, l.client, request, l.eventSocket)
		}()
	default:
		l.server.writeResponse(l.client, Response{ID: request.ID, Error: &RPCError{
			Category: "PreconditionFailed",
			Message:  "too many in-flight daemon requests",
			Code:     CodePreconditionFailed,
		}})
	}
}

func (l *connectionLanes) dispatchInput(request Request) {
	lane := l.inputLane(requestTerminalKey(request.Params))
	if lane == nil {
		return
	}
	select {
	case lane <- request:
	default:
		// Terminal input is at most once and must never be silently dropped.
		// A full lane means this terminal's runtime stopped draining, so the
		// loss is reported on the offending request instead of closing a
		// connection that is still serving every other terminal.
		l.server.writeResponse(l.client, Response{ID: request.ID, Error: &RPCError{
			Category: "PreconditionFailed",
			Message:  "terminal input queue is full",
			Code:     CodePreconditionFailed,
		}})
	}
}

func (l *connectionLanes) inputLane(key string) chan Request {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	if lane, ok := l.input[key]; ok {
		return lane
	}
	if len(l.input) >= maxLanesPerConnection {
		if lane, ok := l.input[""]; ok {
			return lane
		}
		key = ""
	}
	lane := make(chan Request, l.queueSize)
	l.input[key] = lane
	go l.runInputLane(lane)
	return lane
}

func (l *connectionLanes) runInputLane(lane <-chan Request) {
	for {
		select {
		case <-l.ctx.Done():
			return
		case request := <-lane:
			l.server.dispatchRequest(l.ctx, l.client, request, l.eventSocket)
		}
	}
}

func (l *connectionLanes) dispatchLatestValue(request Request) {
	lane := l.latestValueLane(requestTerminalKey(request.Params))
	if lane == nil {
		return
	}
	if !lane.push(request) {
		// Coalescing keeps one pending value, but every superseded request
		// still owes a response, so the record of superseded ids stays
		// bounded like every other lane.
		l.server.writeResponse(l.client, Response{ID: request.ID, Error: &RPCError{
			Category: "PreconditionFailed",
			Message:  "too many in-flight daemon requests",
			Code:     CodePreconditionFailed,
		}})
	}
}

func (l *connectionLanes) latestValueLane(key string) *latestValueLane {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	if lane, ok := l.latest[key]; ok {
		return lane
	}
	if len(l.latest) >= maxLanesPerConnection {
		if lane, ok := l.latest[""]; ok {
			return lane
		}
		key = ""
	}
	lane := &latestValueLane{
		pending:  map[string]pendingLatestValue{},
		wake:     make(chan struct{}, 1),
		maxDepth: l.queueSize,
	}
	l.latest[key] = lane
	go l.runLatestValueLane(lane)
	return lane
}

func (l *connectionLanes) runLatestValueLane(lane *latestValueLane) {
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-lane.wake:
		}
		for {
			pending, ok := lane.take()
			if !ok {
				break
			}
			l.server.dispatchLatestValueRequest(l.ctx, l.client, pending, l.eventSocket)
		}
	}
}

// latestValueLane holds at most one pending request per replaceable method.
// Values that never reached the handler are superseded rather than applied, so
// a burst of window drags collapses into the geometry the client actually
// wants.
type latestValueLane struct {
	mu       sync.Mutex
	pending  map[string]pendingLatestValue
	wake     chan struct{}
	maxDepth int
}

type pendingLatestValue struct {
	request    Request
	superseded []uint64
}

func (v *latestValueLane) push(request Request) bool {
	v.mu.Lock()
	entry := pendingLatestValue{request: request}
	if previous, ok := v.pending[request.Method]; ok {
		if len(previous.superseded)+1 >= v.maxDepth {
			v.mu.Unlock()
			return false
		}
		entry.superseded = make([]uint64, 0, len(previous.superseded)+1)
		entry.superseded = append(entry.superseded, previous.superseded...)
		entry.superseded = append(entry.superseded, previous.request.ID)
	}
	v.pending[request.Method] = entry
	v.mu.Unlock()
	select {
	case v.wake <- struct{}{}:
	default:
	}
	return true
}

func (v *latestValueLane) take() (pendingLatestValue, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for method, entry := range v.pending {
		delete(v.pending, method)
		return entry, true
	}
	return pendingLatestValue{}, false
}

// requestTerminalKey names the terminal a request targets so per-terminal lanes
// stay independent. Requests without a terminal share their class's
// connection-wide lane.
func requestTerminalKey(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var target struct {
		TerminalID string `json:"terminal_id"`
	}
	if err := json.Unmarshal(params, &target); err != nil {
		return ""
	}
	return target.TerminalID
}
