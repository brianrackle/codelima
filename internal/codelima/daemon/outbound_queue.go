package daemon

import (
	"context"
	"errors"
	"sync"
	"time"
)

var errOutboundQueueClosed = errors.New("daemon outbound queue closed")

type outboundPriority uint8

const (
	outboundLow outboundPriority = iota
	outboundHigh
)

type queuedOutboundFrame struct {
	data       []byte
	enqueuedAt time.Time
}

// outboundQueue is a two-priority, count-and-byte-bounded mailbox. Producers
// never block: a client that cannot accept another frame is disposable and can
// reconstruct state after reconnecting.
type outboundQueue struct {
	mu sync.Mutex

	high  []queuedOutboundFrame
	low   []queuedOutboundFrame
	bytes int

	maxFrames int
	maxBytes  int
	closed    bool

	wake chan struct{}
	done chan struct{}
	once sync.Once
}

func newOutboundQueue(maxFrames, maxBytes int) *outboundQueue {
	if maxFrames <= 0 {
		maxFrames = 1
	}
	if maxBytes <= 0 {
		maxBytes = 1
	}
	return &outboundQueue{
		maxFrames: maxFrames,
		maxBytes:  maxBytes,
		wake:      make(chan struct{}, 1),
		done:      make(chan struct{}),
	}
}

func (q *outboundQueue) TryPush(data []byte, priority outboundPriority) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed || len(q.high)+len(q.low) >= q.maxFrames || q.bytes+len(data) > q.maxBytes {
		return false
	}
	frame := queuedOutboundFrame{data: data, enqueuedAt: time.Now()}
	if priority == outboundHigh {
		q.high = append(q.high, frame)
	} else {
		q.low = append(q.low, frame)
	}
	q.bytes += len(data)
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return true
}

func (q *outboundQueue) Pop(ctx context.Context) ([]byte, error) {
	for {
		q.mu.Lock()
		var frame queuedOutboundFrame
		switch {
		case len(q.high) > 0:
			frame = q.high[0]
			q.high[0] = queuedOutboundFrame{}
			q.high = q.high[1:]
		case len(q.low) > 0:
			frame = q.low[0]
			q.low[0] = queuedOutboundFrame{}
			q.low = q.low[1:]
		case q.closed:
			q.mu.Unlock()
			return nil, errOutboundQueueClosed
		default:
			q.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			case <-q.done:
				return nil, errOutboundQueueClosed
			case <-q.wake:
			}
			continue
		}
		q.bytes -= len(frame.data)
		q.mu.Unlock()
		return frame.data, nil
	}
}

func (q *outboundQueue) Stats() (depth, bytes int, oldestAge time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()

	depth = len(q.high) + len(q.low)
	bytes = q.bytes
	var oldest time.Time
	if len(q.high) > 0 {
		oldest = q.high[0].enqueuedAt
	}
	if len(q.low) > 0 && (oldest.IsZero() || q.low[0].enqueuedAt.Before(oldest)) {
		oldest = q.low[0].enqueuedAt
	}
	if !oldest.IsZero() {
		oldestAge = time.Since(oldest)
	}
	return depth, bytes, oldestAge
}

func (q *outboundQueue) Close() {
	q.once.Do(func() {
		q.mu.Lock()
		q.closed = true
		q.mu.Unlock()
		close(q.done)
	})
}
