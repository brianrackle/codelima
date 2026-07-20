// Package atomicfile is the single durable-write primitive shared by the
// codelima store and the daemon: temp file in the target directory, write,
// chmod, fsync, rename, then a best-effort fsync of the parent directory.
// Both packages must use it so identity files and metadata get identical
// crash-durability semantics.
package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// WriteFile atomically replaces path with data at mode. The parent directory
// is created when missing. A crash cannot leave the destination pointing at a
// zero-length or partially written inode.
func WriteFile(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tempFile, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}

	tempPath := tempFile.Name()
	success := false
	defer func() {
		if success {
			return
		}

		_ = os.Remove(tempPath)
	}()

	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return err
	}

	if err := tempFile.Chmod(mode); err != nil {
		_ = tempFile.Close()
		return err
	}

	// Flush the temp file's data to stable storage before the rename so a
	// crash cannot leave the destination pointing at a zero-length or
	// partially written inode.
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return err
	}

	if err := tempFile.Close(); err != nil {
		return err
	}

	if err := os.Rename(tempPath, path); err != nil {
		return err
	}

	// fsync the parent directory so the rename itself is durable. Best-effort:
	// some platforms/filesystems reject directory fsync (macOS may) — those are
	// non-fatal.
	if err := fsyncDir(filepath.Dir(path)); err != nil {
		return err
	}

	success = true
	return nil
}

// fsyncDir flushes a directory entry to stable storage. Directory fsync is not
// supported everywhere; EINVAL/ENOTSUP/EBADF are tolerated as non-fatal so the
// write still succeeds on filesystems (or platforms) that reject it.
func fsyncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		if tolerableDirSyncError(err) {
			return nil
		}
		return err
	}
	defer func() {
		_ = handle.Close()
	}()

	if err := handle.Sync(); err != nil {
		if tolerableDirSyncError(err) {
			return nil
		}
		return err
	}

	return nil
}

func tolerableDirSyncError(err error) bool {
	return errors.Is(err, syscall.EINVAL) ||
		errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.EBADF)
}
