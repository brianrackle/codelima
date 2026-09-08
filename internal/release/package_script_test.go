package release

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/testutil"
)

func TestPackageInstallerRejectsMismatchedRendererIdentity(t *testing.T) {
	if err := ValidateTarget(runtime.GOOS, runtime.GOARCH); err != nil {
		t.Skip(err)
	}
	root := testutil.TempDir(t, "package-profile-")
	tools := filepath.Join(root, "tools")
	installerWrite(t, filepath.Join(tools, "ghostty-vt/current/lib/libghostty-vt.a"), "unverified archive", 0o644)
	installerWrite(t, filepath.Join(tools, "ghostty-vt/current/.native-build-id"), strings.Repeat("0", 64), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "sh", installerScript(t, "package_release.sh"), "0.0.0-test", releaseGoBinary(t), tools, filepath.Join(root, "dist"))
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "installed renderer build identity differs") {
		t.Fatalf("stale native profile accepted or wrong failure: %v, %s", err, output)
	}
}
