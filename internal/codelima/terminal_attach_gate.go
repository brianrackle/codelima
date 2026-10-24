package codelima

import (
	"errors"
	"slices"
	"sync"
	"time"
)

const rawAttachmentResponseLimit = 256 << 10
const rawAttachmentIdleTimeout = 15 * time.Second

type rawAttachmentReply struct {
	id      uint64
	data    []byte
	deliver func([]byte)
}

// rawAttachmentGate assigns query replies to either the native renderer or
// the attached physical terminal. Capture runs outside this mutex; replies
// during capture are held until its exact output watermark is known. All
// writes are ordered under the gate and enqueue into the existing PTY writer.
type rawAttachmentGate struct {
	mu                           sync.Mutex
	write                        func([]byte)
	owner, id                    string
	pending                      bool
	start, ack, sent, suppressed uint64
	held                         []rawAttachmentReply
	heldBytes                    int
	deadline                     time.Time
	done                         chan struct{}
}

func (g *rawAttachmentGate) begin(owner, id string, start uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.id != "" {
		return errors.New("this session is already attached; detach its current terminal first")
	}
	g.owner, g.id, g.start, g.pending = owner, id, start, true
	g.ack, g.sent = start, start
	g.deadline = time.Now().Add(rawAttachmentIdleTimeout)
	g.done = make(chan struct{})
	return nil
}

func (g *rawAttachmentGate) commit(id string, through uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.id != id || !g.pending || through < g.start {
		return errors.New("terminal attachment changed during capture")
	}
	g.pending = false
	g.ack, g.sent = through, through
	retained := g.held[:0]
	for _, reply := range g.held {
		if reply.id <= through {
			reply.deliver(reply.data)
			g.heldBytes -= len(reply.data)
		} else {
			retained = append(retained, reply)
		}
	}
	g.held = retained
	return nil
}

func (g *rawAttachmentGate) response(id uint64, data []byte) {
	g.effect(id, data, g.write)
}

func (g *rawAttachmentGate) effect(id uint64, data []byte, deliver func([]byte)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if id == 0 || id&rendererInputEventBit != 0 {
		deliver(data)
		return
	}
	if id <= g.suppressed {
		return
	}
	if g.id == "" || (g.pending && id <= g.start) {
		deliver(data)
		return
	}
	if !g.pending && id <= g.ack {
		return
	}
	if len(data) > rawAttachmentResponseLimit-g.heldBytes {
		// A hostile query flood must not grow daemon memory or block the PTY.
		// End the attachment explicitly; the next poll reports the lost lease.
		g.endLocked()
		deliver(data)
		return
	}
	g.held = append(g.held, rawAttachmentReply{id: id, data: slices.Clone(data), deliver: deliver})
	g.heldBytes += len(data)
}

func (g *rawAttachmentGate) validateLocked(owner, id string) error {
	if g.id == "" || g.id != id || g.owner != owner || g.pending {
		return errors.New("terminal attachment is no longer active")
	}
	g.deadline = time.Now().Add(rawAttachmentIdleTimeout)
	return nil
}

func (g *rawAttachmentGate) delivered(owner, id string, through uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.validateLocked(owner, id); err != nil {
		return err
	}
	g.sent = max(g.sent, through)
	return nil
}

func (g *rawAttachmentGate) acknowledge(owner, id string, through uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.validateLocked(owner, id); err != nil {
		return err
	}
	if through < g.ack || through > g.sent {
		return errors.New("invalid terminal attachment output cursor")
	}
	g.ack = through
	g.suppressed = max(g.suppressed, through)
	retained := g.held[:0]
	for _, reply := range g.held {
		if reply.id > through {
			retained = append(retained, reply)
		} else {
			g.heldBytes -= len(reply.data)
		}
	}
	g.held = retained
	return nil
}

func (g *rawAttachmentGate) end(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.id == id && id != "" {
		g.endLocked()
	}
}

func (g *rawAttachmentGate) endLocked() {
	for _, reply := range g.held {
		// Pending capture has shown no live output to the host. Otherwise only
		// acknowledged output belongs to it; restore native replies for the rest.
		if g.pending || reply.id > g.ack {
			reply.deliver(reply.data)
		}
	}
	if !g.pending {
		g.suppressed = max(g.suppressed, g.ack)
	}
	g.held, g.heldBytes = nil, 0
	g.id, g.owner, g.pending = "", "", false
	close(g.done)
}

func (g *rawAttachmentGate) expired(id string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.id == id && now.After(g.deadline)
}
