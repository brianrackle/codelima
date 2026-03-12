package codelima

import (
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

type LockSet struct {
	files []*os.File
}

func acquireLocks(root string, keys ...string) (*LockSet, error) {
	lockDir := filepath.Join(root, "_locks")
	if err := ensureDir(lockDir); err != nil {
		return nil, err
	}

	sortedKeys := append([]string(nil), keys...)
	sort.Strings(sortedKeys)

	lockSet := &LockSet{}
	for _, key := range sortedKeys {
		lockPath := filepath.Join(lockDir, key+".lock")
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			_ = lockSet.Close()
			return nil, err
		}

		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
			_ = file.Close()
			_ = lockSet.Close()
			return nil, err
		}

		lockSet.files = append(lockSet.files, file)
	}

	return lockSet, nil
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
