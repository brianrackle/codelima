package codelima

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTUIRecipeExternalEditorPreservesBlocksAndCleansScratch(t *testing.T) {
	root := t.TempDir()
	want := "printf '界\none'\n\tprintf two\n"
	got, err := editRecipeCommand(root, `editor --flag "two words"`, "old", func(args []string) error {
		if len(args) != 4 || args[2] != "two words" {
			t.Fatal("editor arguments changed", args)
		}
		if !strings.HasPrefix(args[3], root+string(os.PathSeparator)) {
			t.Fatal("scratch escaped owned storage")
		}
		return os.WriteFile(args[3], []byte(want), 0600)
	})
	if err != nil || got != want {
		t.Fatal("editor changed script bytes", got, err)
	}
	_, err = editRecipeCommand(root, "editor", "old", func([]string) error { return errors.New("editor failed") })
	if err == nil {
		t.Fatal("editor failure was ignored")
	}
	entries, err := os.ReadDir(filepath.Join(root, "_tmp"))
	if err != nil || len(entries) != 0 {
		t.Fatal("editor scratch was retained", entries, err)
	}
}
