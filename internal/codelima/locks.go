package codelima

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// lockRetryInterval paces the non-blocking flock retry loop in acquireLockSet.
const lockRetryInterval = 25 * time.Millisecond

// lockWaitAnnounceInterval is how long an acquisition may block before it says
// so. A silent, unbounded wait is indistinguishable from a hang, which is how a
// booting node used to look like a frozen TUI; every wait past this threshold
// names the lock it is queued behind.
const lockWaitAnnounceInterval = time.Second

// lockKey names one flock domain under CODELIMA_HOME/_locks. Lock domains are
// a closed set: free-typed strings would let a typo mint a fresh lock file and
// silently destroy mutual exclusion.
type lockKey string

const (
	lockNodes          lockKey = "nodes"
	lockConfigurations lockKey = "configurations"
	lockEnvironments   lockKey = "environments"
)

// Lock contract
//
// There are three kinds of lock in a metadata home, and they answer three
// different questions:
//
//   - Global domain locks (_locks/<domain>.lock) protect cross-node state: the
//     node/configuration/environment listings and the by-slug and by-instance
//     indexes. They are held for metadata read-modify-write only. No global
//     lock may be held across runtime work (VM create/start/stop, workspace
//     seeding, bootstrap commands) — that is what used to serialize the whole
//     tool behind one booting node.
//   - Per-node metadata locks (_locks/nodes/<id>.lock) protect one node's
//     directory (node.yaml, bootstrap.json, sandbox.ref, events.jsonl). Two
//     operations on two different nodes never contend.
//   - Per-node operation tokens (_locks/nodes/<id>.op.lock) are not mutual
//     exclusion over files at all; see nodeOperationToken.
//
// Lock order is: global domains (sorted among themselves) before per-node locks
// (sorted by node id), never the reverse. acquireLockSet takes both in one call
// so an operation's critical section cannot invent its own order. The single
// permitted nesting is a global set already held while a per-node set is taken
// (Service.seedAndRepair does exactly this: it must enumerate node ids under the
// global nodes lock before it can name the per-node locks it needs).

type LockSet struct {
	files []*os.File
}

// acquireLockSet takes global lock domains and per-node metadata locks in the
// documented order (see the lock contract above). Callers must acquire
// everything one critical section needs in ONE call — nesting acquisitions of
// the same tier reintroduces ordering deadlocks. Acquisition polls with LOCK_NB
// so a cancelled context stops waiting on a stuck holder instead of blocking
// forever, and announces itself through logger once it has waited past
// lockWaitAnnounceInterval.
func acquireLockSet(ctx context.Context, root string, logger *slog.Logger, keys []lockKey, nodeIDs []string) (*LockSet, error) {
	paths, err := lockPaths(root, keys, nodeIDs)
	if err != nil {
		return nil, err
	}

	lockSet := &LockSet{}
	for _, path := range paths {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			_ = lockSet.Close()
			return nil, err
		}

		if err := flockWithContext(ctx, file, logger, lockLabel(root, path)); err != nil {
			_ = file.Close()
			_ = lockSet.Close()
			return nil, err
		}

		lockSet.files = append(lockSet.files, file)
	}

	return lockSet, nil
}

// lockPaths resolves an acquisition request to the ordered list of lock files.
// Sorting plus de-duplication here is what makes the order a property of the
// lock layer rather than of each call site.
func lockPaths(root string, keys []lockKey, nodeIDs []string) ([]string, error) {
	lockDir := filepath.Join(root, "_locks")
	paths := make([]string, 0, len(keys)+len(nodeIDs))

	if len(keys) > 0 {
		if err := ensureDir(lockDir); err != nil {
			return nil, err
		}
		sortedKeys := slices.Clone(keys)
		slices.Sort(sortedKeys)
		sortedKeys = slices.Compact(sortedKeys)
		for _, key := range sortedKeys {
			paths = append(paths, filepath.Join(lockDir, string(key)+".lock"))
		}
	}

	if len(nodeIDs) > 0 {
		nodeLockDir := filepath.Join(lockDir, "nodes")
		if err := ensureDir(nodeLockDir); err != nil {
			return nil, err
		}
		sortedIDs := slices.Clone(nodeIDs)
		slices.Sort(sortedIDs)
		sortedIDs = slices.Compact(sortedIDs)
		for _, nodeID := range sortedIDs {
			name, err := nodeLockBaseName(nodeID)
			if err != nil {
				return nil, err
			}
			paths = append(paths, filepath.Join(nodeLockDir, name+".lock"))
		}
	}

	return paths, nil
}

// nodeLockBaseName maps a node id onto its lock file stem. Node ids are
// generated identifiers, but they reach this seam from directory names on disk,
// so a value that could escape _locks/nodes is rejected instead of silently
// locking something else.
func nodeLockBaseName(nodeID string) (string, error) {
	name := strings.TrimSpace(nodeID)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", invalidArgument("node id is not usable as a lock name", map[string]any{"node_id": nodeID})
	}
	return name, nil
}

// lockLabel renders a lock path relative to _locks so diagnostics name the lock
// ("nodes.lock", "nodes/<id>.lock") without leaking the host home path.
func lockLabel(root, path string) string {
	if rel, err := filepath.Rel(filepath.Join(root, "_locks"), path); err == nil {
		return rel
	}
	return filepath.Base(path)
}

func flockWithContext(ctx context.Context, file *os.File, logger *slog.Logger, label string) error {
	started := time.Now()
	announced := false
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			if announced {
				lockLogger(logger).Info("acquired contended lock", "lock", label, "waited", time.Since(started).String())
			}
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		if !announced && time.Since(started) >= lockWaitAnnounceInterval {
			announced = true
			lockLogger(logger).Warn("waiting for lock", "lock", label, "waited", time.Since(started).String())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lockRetryInterval):
		}
	}
}

// lockLogger resolves the sink for lock diagnostics. Service call sites pass
// their own logger; the free-function entry points fall back to the
// process-global package sink (ADR 59) rather than dropping the record.
func lockLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return packageLog()
}

// release unlocks and closes the set, discarding errors. It exists so call
// sites can `defer lockSet.release()` instead of repeating an error-ignoring
// closure; unlock failures at scope exit have no meaningful recovery.
func (l *LockSet) release() {
	_ = l.Close()
}

func (l *LockSet) Close() error {
	if l == nil {
		return nil
	}

	var firstErr error
	for _, file := range l.files {
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil && firstErr == nil {
			firstErr = err
		}

		if err := file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	l.files = nil
	return firstErr
}

// nodeOperationToken is a per-node liveness claim on a lifecycle operation. It
// is deliberately NOT mutual exclusion over metadata: it is only ever taken
// with LOCK_NB and is never waited on, so it can never make a caller block.
// Its one job is to answer "does a live process own this node's lifecycle right
// now?", because the durable in-flight status persisted in node.yaml cannot
// answer that on its own — a process killed mid-boot leaves the status behind
// but the kernel drops the token, which is exactly what turns a stranded status
// into a detectable stale claim.
type nodeOperationToken struct {
	file *os.File
}

func nodeOperationTokenPath(root, nodeID string) (string, error) {
	name, err := nodeLockBaseName(nodeID)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "_locks", "nodes", name+".op.lock"), nil
}

// tryAcquireNodeOperation claims the node's lifecycle token without waiting. It
// reports ok=false when a live process already owns the node, which callers
// turn into a fail-fast typed error rather than a queue.
func tryAcquireNodeOperation(root, nodeID string) (*nodeOperationToken, bool, error) {
	path, err := nodeOperationTokenPath(root, nodeID)
	if err != nil {
		return nil, false, err
	}
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return nil, false, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}

	return &nodeOperationToken{file: file}, true, nil
}

func (t *nodeOperationToken) release() {
	if t == nil || t.file == nil {
		return
	}
	_ = syscall.Flock(int(t.file.Fd()), syscall.LOCK_UN)
	_ = t.file.Close()
	t.file = nil
}

// nodeOperationClaimed reports whether a live process currently owns the node's
// lifecycle token. It never creates the token file, so read-only inspection
// (doctor without --repair) stays a pure read.
func nodeOperationClaimed(root, nodeID string) (bool, error) {
	path, err := nodeOperationTokenPath(root, nodeID)
	if err != nil {
		return false, err
	}
	if !exists(path) {
		return false, nil
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer func() { _ = file.Close() }()

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return true, nil
		}
		return false, err
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false, nil
}
