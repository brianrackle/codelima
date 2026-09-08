package codelima

import "sync"

const rendererCheckpointAggregateBytes = 64 << 20

// Retained checkpoints are process-wide bounded, not just terminal-local.
// Rejection leaves an owner's previous checkpoint and raw replay fallback
// intact; capture never evicts another terminal's only useful checkpoint.
type rendererCheckpointQuota struct {
	mu          sync.Mutex
	limit, used int
	owners      map[any]int
}

var processRendererCheckpointQuota = newRendererCheckpointQuota(rendererCheckpointAggregateBytes)

func newRendererCheckpointQuota(limit int) *rendererCheckpointQuota {
	return &rendererCheckpointQuota{limit: limit, owners: make(map[any]int)}
}

func (q *rendererCheckpointQuota) reserve(owner any, size int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	previous := q.owners[owner]
	if size <= 0 || size > q.limit-(q.used-previous) {
		return false
	}
	q.used += size - previous
	q.owners[owner] = size
	return true
}

func (q *rendererCheckpointQuota) release(owner any) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.used -= q.owners[owner]
	delete(q.owners, owner)
}
