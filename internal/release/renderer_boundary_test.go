package release

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRendererNativeDependencyIsWorkerOnly(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native renderer supports Linux and macOS")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		command string
		native  bool
	}{
		{"codelima", false}, {"codelima-renderer-worker", true},
	} {
		t.Run(test.command, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, releaseGoBinary(t), "list", "-deps", "./cmd/"+test.command)
			command.Dir = root
			command.Env = append(os.Environ(), "CGO_ENABLED=1")
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("inspect dependency boundary: %v, %s", err, output)
			}
			got := strings.Contains(string(output), "github.com/brianrackle/codelima/internal/ghostty\n")
			if got != test.native {
				t.Fatalf("%s imports native engine = %t, want %t", test.command, got, test.native)
			}
		})
	}
}

func TestRendererPortablePackagesContainNoCGO(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, releaseGoBinary(t), "list", "-f", "{{.ImportPath}}:{{len .CgoFiles}}", "./internal/codelima", "./internal/terminalio", "./internal/terminalstate", "./internal/rendererbuild")
	command.Dir = root
	command.Env = append(os.Environ(), "CGO_ENABLED=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect portable packages: %v, %s", err, output)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if !strings.HasSuffix(line, ":0") {
			t.Errorf("portable package gained native source: %s", line)
		}
	}
}
