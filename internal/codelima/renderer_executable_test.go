//go:build darwin || linux

package codelima

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brianrackle/codelima/internal/testutil"
)

func TestRendererWorkerResolvesBesideSymlinkTarget(t *testing.T) {
	root := testutil.TempDir(t, "renderer-symlink-")
	actualDir := filepath.Join(root, "libexec", "bin")
	publicDir := filepath.Join(root, "bin")
	for _, dir := range []string{actualDir, publicDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	actual := filepath.Join(actualDir, "codelima")
	worker := filepath.Join(actualDir, rendererWorkerExecutableName)
	for _, path := range []string{actual, worker} {
		if err := os.WriteFile(path, []byte("executable fixture"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	public := filepath.Join(publicDir, "codelima")
	if err := os.Symlink("../libexec/bin/codelima", public); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(worker)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{actual, public} {
		got, err := resolveRendererWorkerBeside(input)
		if err != nil || got != want {
			t.Fatalf("resolve worker beside %s = %s, %v; want %s", input, got, err, want)
		}
	}
	if err := os.Chmod(worker, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRendererWorkerBeside(public); err == nil {
		t.Fatal("non-executable worker accepted")
	}
	if _, err := resolveRendererWorkerBeside(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing main executable accepted")
	}
}
