package codelima

import (
	"context"
	"time"
)

// coalescedPublisher turns edge-triggered dirty requests into a bounded
// publication cadence. Its capacity-one wakeup retains one trailing
// publication when more work arrives while publish is running.
type coalescedPublisher struct {
	interval time.Duration
	wake     chan struct{}
	publish  func()
}

func newCoalescedPublisher(interval time.Duration, publish func()) *coalescedPublisher {
	return &coalescedPublisher{
		interval: interval,
		wake:     make(chan struct{}, 1),
		publish:  publish,
	}
}

func (p *coalescedPublisher) Request() {
	if p == nil {
		return
	}
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *coalescedPublisher) Run(ctx context.Context) {
	if p == nil || p.publish == nil {
		return
	}
	var lastPublished time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		}

		if delay := time.Until(lastPublished.Add(p.interval)); delay > 0 {
			timer := time.NewTimer(delay)
			waiting := true
			for waiting {
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					return
				case <-p.wake:
					// Coalesce dirty requests while retaining the original
					// publication boundary.
				case <-timer.C:
					waiting = false
				}
			}
		}

		p.publish()
		lastPublished = time.Now()
	}
}
