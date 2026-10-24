package release

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/testutil"
)

func TestMiseInstallerVerifiesArchiveAndCleansScratch(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			root := testutil.TempDir(t, "mise-installer-")
			tools, scratch, commands := filepath.Join(root, "tools with spaces"), filepath.Join(root, "scratch"), filepath.Join(root, "commands")
			archive := filepath.Join(root, "fixture.tar.gz")
			installerWrite(t, filepath.Join(root, "source", "mise", "bin", "mise"), "#!/bin/sh\necho fixture\n", 0o755)
			cmd := exec.Command("tar", "-czf", archive, "-C", filepath.Join(root, "source"), "mise")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("archive: %v %s", err, out)
			}
			data, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}
			checksum := fmt.Sprintf("%x", sha256.Sum256(data))
			if corrupt {
				checksum = strings.Repeat("0", 64)
			}
			installerWrite(t, filepath.Join(commands, "curl"), `#!/bin/sh
set -eu
url= output=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) output="$2"; shift ;;
    https:*) url="$1" ;;
  esac
  shift
done
case "$url" in
  */SHASUMS256.txt)
    asset="$(cat "$MISE_FIXTURE_ASSET")"
    printf '%s  ./%s\n' "$MISE_FIXTURE_SHA" "$asset" > "$output" ;;
  *) printf '%s' "${url##*/}" > "$MISE_FIXTURE_ASSET"; cp "$MISE_FIXTURE_ARCHIVE" "$output" ;;
esac
`, 0o755)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd = exec.CommandContext(ctx, "sh", installerScript(t, "install_mise.sh"), "2026.10.18", tools, scratch)
			cmd.Env = append(os.Environ(), "PATH="+commands+string(os.PathListSeparator)+os.Getenv("PATH"), "MISE_FIXTURE_ARCHIVE="+archive, "MISE_FIXTURE_SHA="+checksum, "MISE_FIXTURE_ASSET="+filepath.Join(root, "asset"))
			out, err := cmd.CombinedOutput()
			installed := filepath.Join(tools, "mise", "2026.10.18", "bin", "mise")
			if corrupt {
				if err == nil || !strings.Contains(string(out), "checksum mismatch") {
					t.Fatalf("corruption accepted: %v %s", err, out)
				}
				if _, err = os.Stat(installed); !os.IsNotExist(err) {
					t.Fatal("unverified binary published")
				}
			} else {
				if err != nil {
					t.Fatalf("install: %v %s", err, out)
				}
				info, err := os.Stat(installed)
				if err != nil || info.Mode().Perm()&0o111 == 0 {
					t.Fatalf("missing executable: %v %v", info, err)
				}
			}
			entries, err := os.ReadDir(scratch)
			if err != nil || len(entries) != 0 {
				t.Fatalf("scratch retained: %v %v", entries, err)
			}
		})
	}
}
