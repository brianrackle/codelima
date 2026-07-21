package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TempDir creates a test directory under the repository's tmp directory.
// Keeping paths short and project-rooted makes Unix-socket tests portable and
// keeps disposable test artifacts inside the checkout.
func TempDir(t testing.TB, prefix string) string {
	t.Helper()

	root, err := projectRoot()
	if err != nil {
		t.Fatal(err)
	}
	tempRoot := filepath.Join(root, "tmp", "tests")
	if err := os.MkdirAll(tempRoot, 0o755); err != nil {
		t.Fatalf("create project test temp root: %v", err)
	}
	dir, err := os.MkdirTemp(tempRoot, prefix)
	if err != nil {
		t.Fatalf("create project test temp directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove project test temp directory %q: %v", dir, err)
		}
	})
	return dir
}

func projectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve test working directory: %w", err)
	}
	for {
		info, statErr := os.Stat(filepath.Join(dir, "go.mod"))
		if statErr == nil && info.Mode().IsRegular() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("find repository root from %q", dir)
		}
		dir = parent
	}
}
