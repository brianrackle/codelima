package codelima

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer collects log records written from the goroutine under test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestLockPathsOrderGlobalDomainsBeforeNodesAndDeduplicate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	paths, err := lockPaths(root, []lockKey{lockNodes, lockEnvironments, lockNodes, lockConfigurations}, []string{"node-b", "node-a", "node-b"})
	if err != nil {
		t.Fatalf("lockPaths() error = %v", err)
	}

	want := []string{
		filepath.Join(root, "_locks", "configurations.lock"),
		filepath.Join(root, "_locks", "environments.lock"),
		filepath.Join(root, "_locks", "nodes.lock"),
		filepath.Join(root, "_locks", "nodes", "node-a.lock"),
		filepath.Join(root, "_locks", "nodes", "node-b.lock"),
	}
	if len(paths) != len(want) {
		t.Fatalf("lockPaths() = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("lockPaths()[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

func TestNodeLockBaseNameRejectsPathEscapes(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "   ", ".", "..", "../escape", `sub\dir`, "nodes/child"} {
		if _, err := nodeLockBaseName(value); err == nil {
			t.Fatalf("nodeLockBaseName(%q) accepted a name that can escape _locks/nodes", value)
		}
	}
	name, err := nodeLockBaseName(" node-01 ")
	if err != nil {
		t.Fatalf("nodeLockBaseName(valid) error = %v", err)
	}
	if name != "node-01" {
		t.Fatalf("nodeLockBaseName() = %q, want %q", name, "node-01")
	}
}

func TestPerNodeLocksSerializeOneNodeAndParallelizeOthers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ctx := context.Background()

	held, err := acquireLockSet(ctx, root, nil, nil, []string{"node-a"})
	if err != nil {
		t.Fatalf("acquireLockSet(node-a) error = %v", err)
	}
	defer held.release()

	// An independent node is unaffected by a lifecycle operation on node-a.
	other, err := acquireLockSet(ctx, root, nil, nil, []string{"node-b"})
	if err != nil {
		t.Fatalf("acquireLockSet(node-b) error = %v", err)
	}
	other.release()

	// The global nodes domain is likewise unaffected: a per-node metadata phase
	// must never wedge cross-node reads.
	global, err := acquireLockSet(ctx, root, nil, []lockKey{lockNodes}, nil)
	if err != nil {
		t.Fatalf("acquireLockSet(global nodes) error = %v", err)
	}
	global.release()

	// The same node does serialize.
	blockedCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if _, err := acquireLockSet(blockedCtx, root, nil, nil, []string{"node-a"}); err == nil {
		t.Fatal("expected a second acquisition of node-a's metadata lock to block")
	}
}

func TestFlockWithContextStopsWaitingWhenContextIsCancelled(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	held, err := acquireLockSet(context.Background(), root, nil, []lockKey{lockNodes}, nil)
	if err != nil {
		t.Fatalf("acquireLockSet() error = %v", err)
	}
	defer held.release()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, waitErr := acquireLockSet(ctx, root, nil, []lockKey{lockNodes}, nil)
		errCh <- waitErr
	}()

	// Let the waiter enter its retry loop, then cancel it.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case waitErr := <-errCh:
		if waitErr == nil {
			t.Fatal("expected the cancelled acquisition to fail")
		}
		if !strings.Contains(waitErr.Error(), context.Canceled.Error()) {
			t.Fatalf("acquireLockSet(cancelled) error = %v, want context cancellation", waitErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled acquisition kept waiting on the lock")
	}
}

func TestFlockWithContextAnnouncesLongWaits(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	held, err := acquireLockSet(context.Background(), root, nil, []lockKey{lockNodes}, nil)
	if err != nil {
		t.Fatalf("acquireLockSet() error = %v", err)
	}
	defer held.release()

	sink := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(sink, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx, cancel := context.WithTimeout(context.Background(), lockWaitAnnounceInterval+400*time.Millisecond)
	defer cancel()
	if _, err := acquireLockSet(ctx, root, logger, []lockKey{lockNodes}, nil); err == nil {
		t.Fatal("expected the contended acquisition to time out")
	}

	logged := sink.String()
	if !strings.Contains(logged, "waiting for lock") {
		t.Fatalf("expected a waiting-for-lock record, got %q", logged)
	}
	if !strings.Contains(logged, "nodes.lock") {
		t.Fatalf("expected the record to name the lock, got %q", logged)
	}
	if strings.Contains(logged, root) {
		t.Fatalf("lock diagnostics leaked the home path: %q", logged)
	}
}

func TestFlockWaitAnnouncementIsSilentForUncontendedLocks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sink := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(sink, &slog.HandlerOptions{Level: slog.LevelDebug}))

	lockSet, err := acquireLockSet(context.Background(), root, logger, []lockKey{lockNodes}, []string{"node-a"})
	if err != nil {
		t.Fatalf("acquireLockSet() error = %v", err)
	}
	lockSet.release()

	if logged := sink.String(); logged != "" {
		t.Fatalf("expected an uncontended acquisition to log nothing, got %q", logged)
	}
}

func TestNodeOperationTokenIsExclusiveAndReleasable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	first, ok, err := tryAcquireNodeOperation(root, "node-a")
	if err != nil || !ok {
		t.Fatalf("tryAcquireNodeOperation(first) = %v, %v", ok, err)
	}

	// A second claim must fail immediately rather than wait for the first.
	deadline := time.Now().Add(time.Second)
	second, ok, err := tryAcquireNodeOperation(root, "node-a")
	if err != nil {
		t.Fatalf("tryAcquireNodeOperation(second) error = %v", err)
	}
	if ok {
		second.release()
		t.Fatal("expected the second lifecycle claim on the same node to be refused")
	}
	if time.Now().After(deadline) {
		t.Fatal("a refused lifecycle claim blocked instead of failing fast")
	}

	// A different node is never affected.
	otherNode, ok, err := tryAcquireNodeOperation(root, "node-b")
	if err != nil || !ok {
		t.Fatalf("tryAcquireNodeOperation(node-b) = %v, %v", ok, err)
	}
	otherNode.release()

	claimed, err := nodeOperationClaimed(root, "node-a")
	if err != nil {
		t.Fatalf("nodeOperationClaimed() error = %v", err)
	}
	if !claimed {
		t.Fatal("expected node-a to report a live lifecycle claim")
	}

	// Releasing is what a process exit does for free; the claim then reads as
	// stale rather than live, which is the crash-recovery signal.
	first.release()
	claimed, err = nodeOperationClaimed(root, "node-a")
	if err != nil {
		t.Fatalf("nodeOperationClaimed(after release) error = %v", err)
	}
	if claimed {
		t.Fatal("expected a released lifecycle claim to read as stale")
	}

	reclaimed, ok, err := tryAcquireNodeOperation(root, "node-a")
	if err != nil || !ok {
		t.Fatalf("tryAcquireNodeOperation(after release) = %v, %v", ok, err)
	}
	reclaimed.release()
}

func TestNodeOperationClaimedNeverWritesToTheHome(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	claimed, err := nodeOperationClaimed(root, "node-a")
	if err != nil {
		t.Fatalf("nodeOperationClaimed() error = %v", err)
	}
	if claimed {
		t.Fatal("expected an unknown node to report no lifecycle claim")
	}
	if _, err := os.Stat(filepath.Join(root, "_locks")); !os.IsNotExist(err) {
		t.Fatalf("nodeOperationClaimed created lock state: stat error = %v", err)
	}
}
