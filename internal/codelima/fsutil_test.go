package codelima

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func countTempFiles(t *testing.T, dir string) int {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", dir, err)
	}

	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			count++
		}
	}
	return count
}

func TestAtomicWriteFileRoundTripsContentAndMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "value.txt")
	data := []byte("durable-payload\n")

	if err := atomicWriteFile(path, data, 0o640); err != nil {
		t.Fatalf("atomicWriteFile() error = %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("content mismatch: got %q, want %q", got, data)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o640 {
		t.Fatalf("mode mismatch: got %o, want %o", perm, 0o640)
	}

	if leftover := countTempFiles(t, filepath.Dir(path)); leftover != 0 {
		t.Fatalf("expected no leftover temp files after success, found %d", leftover)
	}
}

func TestAtomicWriteFileCleansUpTempOnReadOnlyDirFailure(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root can write through directory permission bits")
	}

	dir := t.TempDir()
	roDir := filepath.Join(dir, "readonly")
	if err := os.Mkdir(roDir, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := os.Chmod(roDir, 0o500); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	// Restore writability so t.TempDir cleanup can remove the tree.
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o755) })

	path := filepath.Join(roDir, "value.txt")
	if err := atomicWriteFile(path, []byte("data"), 0o644); err == nil {
		t.Fatalf("expected atomicWriteFile to fail writing into a read-only dir")
	}

	if exists(path) {
		t.Fatalf("expected destination file not to be created on failure")
	}
	if leftover := countTempFiles(t, roDir); leftover != 0 {
		t.Fatalf("expected no leftover temp files after failure, found %d", leftover)
	}
}

// TestAtomicWriteFileCleansUpTempWhenRenameFails exercises the deferred
// temp-file cleanup on the path where the temp file is successfully created and
// written but the final rename fails (here: the destination path is an existing
// non-empty directory, so rename cannot clobber it).
func TestAtomicWriteFileCleansUpTempWhenRenameFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dest := filepath.Join(dir, "occupied")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dest, "child"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := atomicWriteFile(dest, []byte("data"), 0o644); err == nil {
		t.Fatalf("expected atomicWriteFile to fail renaming onto a non-empty directory")
	}

	if leftover := countTempFiles(t, dir); leftover != 0 {
		t.Fatalf("expected temp file to be cleaned up after rename failure, found %d", leftover)
	}
}
