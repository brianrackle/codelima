package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTempDirUsesProjectScratchRootAndCleansUp(t *testing.T) {
	root, err := projectRoot()
	if err != nil {
		t.Fatal(err)
	}

	var dir string
	t.Run("create", func(t *testing.T) {
		dir = TempDir(t, "portable-")
		relative, err := filepath.Rel(filepath.Join(root, "tmp", "tests"), dir)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			t.Fatalf("TempDir() = %q, want path under repository tmp/tests", dir)
		}
		if err := os.WriteFile(filepath.Join(dir, "artifact"), []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	})

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("temporary directory still exists after cleanup: %v", err)
	}
}
