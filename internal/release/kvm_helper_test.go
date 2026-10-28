package release

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/testutil"
)

func TestKVMHelperPreservesCommandEnvironment(t *testing.T) {
	if os.Getenv("CODELIMA_KVM_TEST") != "1" {
		t.Skip("run make test-qemu-guest on a Linux KVM host")
	}
	root := testutil.TempDir(t, "kvm-")
	home, lima := filepath.Join(root, "home with spaces"), filepath.Join(root, "lima with spaces")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", installerScript(t, "with_kvm.sh"), "sh", "-c", `
test -r /dev/kvm && test -w /dev/kvm || exit 1
printf '%s\n' "$CODELIMA_HOME" "$LIMA_HOME" "$TMPDIR" "$CODELIMA_KVM_PROBE"
id -u
`)
	command.Env = append(os.Environ(), "CODELIMA_HOME="+home, "LIMA_HOME="+lima, "TMPDIR="+root, "CODELIMA_KVM_PROBE=preserved caller value")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("KVM helper failed: %v\n%s", err, output)
	}
	want := strings.Join([]string{home, lima, root, "preserved caller value", strconv.Itoa(os.Getuid()), ""}, "\n")
	if string(output) != want {
		t.Fatalf("KVM helper changed command environment or user:\ngot %q\nwant %q", output, want)
	}
}
