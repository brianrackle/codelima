package codelima

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"
)

// lockRetryInterval paces the non-blocking flock retry loop in acquireLocks.
const lockRetryInterval = 25 * time.Millisecond

// lockKey names one flock domain under CODELIMA_HOME/_locks. Lock domains are
// a closed set: free-typed strings would let a typo mint a fresh lock file and
// silently destroy mutual exclusion.
type lockKey string

const (
	lockNodes          lockKey = "nodes"
	lockConfigurations lockKey = "configurations"
	lockEnvironments   lockKey = "environments"
)

type LockSet struct {
	files []*os.File
}

// acquireLocks takes the given lock domains in sorted order so every caller
// acquires overlapping sets in the same sequence (deadlock hygiene). Callers
// must acquire all the locks an operation needs in ONE call — nesting
// acquireLocks calls reintroduces ordering deadlocks. Acquisition polls with
// LOCK_NB so a cancelled context stops waiting on a stuck holder instead of
// blocking forever.
func acquireLocks(ctx context.Context, root string, keys ...lockKey) (*LockSet, error) {
	lockDir := filepath.Join(root, "_locks")
	if err := ensureDir(lockDir); err != nil {
		return nil, err
	}

	sortedKeys := slices.Clone(keys)
	slices.Sort(sortedKeys)

	lockSet := &LockSet{}
	for _, key := range sortedKeys {
		lockPath := filepath.Join(lockDir, string(key)+".lock")
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			_ = lockSet.Close()
			return nil, err
		}

		if err := flockWithContext(ctx, file); err != nil {
			_ = file.Close()
			_ = lockSet.Close()
			return nil, err
		}

		lockSet.files = append(lockSet.files, file)
	}

	return lockSet, nil
}

func flockWithContext(ctx context.Context, file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lockRetryInterval):
		}
	}
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
