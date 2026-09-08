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

const pkgconfTestVersion = "2.5.1"
const pkgconfReviewedSHA256 = "cd05c9589b9f86ecf044c10a2269822bc9eb001eced2582cfffd658b0a50c243"

type pkgconfInstallerFixture struct {
	script, tools, scratch, zig, commands, log, archive, goBinary string
	environment                                                   []string
}

// Substitute only the reviewed archive pin in a temporary script copy. This
// permits a tiny source fixture while exercising real checksum verification,
// tar extraction, toolchain shims, kernel locking and atomic publication. No
// test can authenticate a corrupt archive through a mocked hashing command.
func newPkgconfInstallerFixture(t *testing.T) *pkgconfInstallerFixture {
	t.Helper()
	root := testutil.TempDir(t, "pkgconf-installer-")
	f := &pkgconfInstallerFixture{
		script: filepath.Join(root, "scripts", "install_pkgconf.sh"),
		tools:  filepath.Join(root, "tools with spaces"), scratch: filepath.Join(root, "scratch"),
		zig: filepath.Join(root, "zig with spaces", "zig"), commands: filepath.Join(root, "commands"),
		log: filepath.Join(root, "calls.log"), archive: filepath.Join(root, "fixture.tar.xz"),
		goBinary: releaseGoBinary(t),
	}
	source := filepath.Join(root, "source", "pkgconf-"+pkgconfTestVersion)
	installerWrite(t, filepath.Join(source, "configure"), pkgconfFixtureConfigure, 0o755)
	installerWrite(t, filepath.Join(source, "COPYING"), "fixture license\n", 0o644)
	archive := exec.Command("tar", "-C", filepath.Dir(source), "-cJf", f.archive, filepath.Base(source))
	if output, err := archive.CombinedOutput(); err != nil {
		t.Fatalf("create fixture archive: %v, %s", err, output)
	}
	data, err := os.ReadFile(f.archive)
	if err != nil {
		t.Fatal(err)
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256(data))
	script, err := os.ReadFile(installerScript(t, "install_pkgconf.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), pkgconfReviewedSHA256) {
		t.Fatal("installer no longer uses the reviewed pkgconf source pin; review the fixture pin")
	}
	installerWrite(t, f.script, strings.ReplaceAll(string(script), pkgconfReviewedSHA256, checksum), 0o755)
	for _, name := range []string{"tooling_lock.go", "pkgconf_toolchain.sh"} {
		data, err := os.ReadFile(installerScript(t, name))
		if err != nil {
			t.Fatal(err)
		}
		installerWrite(t, filepath.Join(filepath.Dir(f.script), name), string(data), 0o755)
	}
	// Do not inherit PATH: /usr/bin commonly contains pkg-config on developer
	// machines, concealing the fresh-Mac bootstrap regression.
	for _, name := range []string{"sh", "env", "dirname", "basename", "uname", "tr", "mkdir", "mktemp", "rm", "mv", "cp", "ln", "cat", "awk", "shasum", "tar", "chmod", "sed", "sleep", "sort", "readlink"} {
		command, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("installer test needs %s: %v", name, err)
		}
		if err := os.MkdirAll(f.commands, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(command, filepath.Join(f.commands, name)); err != nil {
			t.Fatal(err)
		}
	}
	// GNU tar delegates xz compression/decompression, while BSD tar embeds it.
	if xz, err := exec.LookPath("xz"); err == nil {
		if err := os.Symlink(xz, filepath.Join(f.commands, "xz")); err != nil {
			t.Fatal(err)
		}
	}
	installerWrite(t, f.zig, pkgconfFixtureZig, 0o755)
	installerWrite(t, filepath.Join(f.commands, "make"), pkgconfFixtureMake, 0o755)
	installerWrite(t, filepath.Join(f.commands, "curl"), pkgconfFixtureCurl, 0o755)
	if err := os.MkdirAll(f.scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	f.environment = append(os.Environ(), "PATH="+f.commands, "CGO_ENABLED=0", "CODELIMA_TOOLING_GO="+f.goBinary, "TMPDIR="+f.scratch,
		"PKG_CONFIG="+filepath.Join(f.tools, "bin", "pkg-config"),
		"PKGCONF_FIXTURE_ARCHIVE="+f.archive, "PKGCONF_FIXTURE_LOG="+f.log, "PKGCONF_FIXTURE_VERSION="+pkgconfTestVersion)
	return f
}

func (f *pkgconfInstallerFixture) command(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, filepath.Join(f.commands, "sh"), f.script, pkgconfTestVersion, f.tools, f.scratch, f.zig)
	cmd.Env = f.environment
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

func (f *pkgconfInstallerFixture) assertPublished(t *testing.T) string {
	t.Helper()
	public := filepath.Join(f.tools, "bin", "pkg-config")
	info, err := os.Lstat(public)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("pkg-config is not an atomically published symlink: %v", err)
	}
	target, err := filepath.EvalSymlinks(public)
	if err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Dir(filepath.Dir(target))
	if filepath.Dir(prefix) != filepath.Join(f.tools, "pkgconf") || !strings.HasPrefix(filepath.Base(prefix), pkgconfTestVersion+"-") || filepath.Base(target) != "pkgconf" {
		t.Fatalf("publication escaped immutable pkgconf prefix: %s", target)
	}
	if stamp, err := os.ReadFile(filepath.Join(prefix, ".build-stamp")); err != nil || len(strings.TrimSpace(string(stamp))) == 0 {
		t.Fatalf("published prefix lacks verification stamp: %v", err)
	}
	cmd := exec.Command(public, "--version")
	cmd.Env = f.environment
	if output, err := cmd.CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != pkgconfTestVersion {
		t.Fatalf("published pkg-config does not run: %v, %s", err, output)
	}
	assertInstallerLockReleased(t, filepath.Join(f.tools, "pkgconf", ".install.lock"))
	f.assertNoStaging(t)
	return target
}

func (f *pkgconfInstallerFixture) assertNoStaging(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.tools, "pkgconf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".install.") && entry.Name() != ".install.lock" {
			t.Errorf("installer retained temporary stage %s", entry.Name())
		}
	}
}

func (f *pkgconfInstallerFixture) calls(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPkgconfInstallerBootstrapsWithoutPkgConfigOnPATH(t *testing.T) {
	t.Parallel()
	f := newPkgconfInstallerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if output, err := f.command(ctx).CombinedOutput(); err != nil {
		t.Fatalf("fresh pkg-config bootstrap: %v, %s", err, output)
	}
	f.assertPublished(t)
	for _, call := range []string{"download\n", "configure\n", "cc\n", "ar\n", "ranlib\n", "build\n"} {
		if count := strings.Count(f.calls(t), call); count != 1 {
			t.Errorf("bootstrap %q count=%d, calls=%q", call, count, f.calls(t))
		}
	}
}

func TestPkgconfInstallerRejectsCorruptCacheBeforeBuildOrPublication(t *testing.T) {
	t.Parallel()
	f := newPkgconfInstallerFixture(t)
	installerWrite(t, filepath.Join(f.tools, "cache", "pkgconf-"+pkgconfTestVersion+".tar.xz"), "corrupt cached archive", 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := f.command(ctx).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "checksum mismatch") {
		t.Fatalf("corrupt cache result: %v, %s", err, output)
	}
	if _, err := os.Lstat(filepath.Join(f.tools, "bin", "pkg-config")); !os.IsNotExist(err) {
		t.Fatalf("corrupt cache published binary: %v", err)
	}
	if calls := f.calls(t); strings.Contains(calls, "configure") || strings.Contains(calls, "build") || strings.Contains(calls, "download") {
		t.Fatalf("corrupt cache was used or silently replaced: %s", calls)
	}
	assertInstallerLockReleased(t, filepath.Join(f.tools, "pkgconf", ".install.lock"))
	f.assertNoStaging(t)
}

func TestPkgconfInstallerFailedBuildCannotPublishPartialExecutable(t *testing.T) {
	t.Parallel()
	f := newPkgconfInstallerFixture(t)
	f.environment = append(f.environment, "PKGCONF_FIXTURE_FAIL=1")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := f.command(ctx).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "fixture build failed") {
		t.Fatalf("failed build result: %v, %s", err, output)
	}
	if _, err := os.Lstat(filepath.Join(f.tools, "bin", "pkg-config")); !os.IsNotExist(err) {
		t.Fatalf("failed build published binary: %v", err)
	}
	entries, err := filepath.Glob(filepath.Join(f.tools, "pkgconf", pkgconfTestVersion+"-*"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed build retained immutable prefix: %v, %v", entries, err)
	}
	assertInstallerLockReleased(t, filepath.Join(f.tools, "pkgconf", ".install.lock"))
	f.assertNoStaging(t)
}

func TestPkgconfInstallerConcurrentBootstrapAndOfflineReuseBuildOnce(t *testing.T) {
	t.Parallel()
	f := newPkgconfInstallerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	results := make(chan error, 3)
	for range 3 {
		go func() {
			output, err := f.command(ctx).CombinedOutput()
			if err != nil {
				err = fmt.Errorf("concurrent installer: %w, %s", err, output)
			}
			results <- err
		}()
	}
	for range 3 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	target := f.assertPublished(t)
	before := f.calls(t)
	if strings.Count(before, "download\n") != 1 || strings.Count(before, "configure\n") != 1 || strings.Count(before, "build\n") != 1 {
		t.Fatalf("concurrent bootstrap did duplicate work: %q", before)
	}
	// Recovery of a missing public link must reuse the immutable verified
	// prefix, even if the network and build command are unavailable.
	if err := os.Remove(filepath.Join(f.tools, "bin", "pkg-config")); err != nil {
		t.Fatal(err)
	}
	f.environment = append(f.environment, "PKGCONF_FIXTURE_OFFLINE=1", "PKGCONF_FIXTURE_FAIL=1")
	if output, err := f.command(ctx).CombinedOutput(); err != nil {
		t.Fatalf("offline cache reuse: %v, %s", err, output)
	}
	if got := f.assertPublished(t); got != target || f.calls(t) != before {
		t.Fatalf("offline reuse changed install or rebuilt: %s, %q", got, f.calls(t))
	}
}

func TestPkgconfInstallerFailedReplacementPreservesPreviousPublishedTool(t *testing.T) {
	t.Parallel()
	f := newPkgconfInstallerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if output, err := f.command(ctx).CombinedOutput(); err != nil {
		t.Fatalf("initial installation: %v, %s", err, output)
	}
	previous := f.assertPublished(t)
	// A changed compiler identity selects a new immutable prefix. Failure
	// before validation must leave the last verified public tool available.
	installerWrite(t, f.zig, strings.ReplaceAll(pkgconfFixtureZig, "0.16.0", "0.16.1"), 0o755)
	f.environment = append(f.environment, "PKGCONF_FIXTURE_FAIL=1")
	output, err := f.command(ctx).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "fixture build failed") {
		t.Fatalf("replacement failure: %v, %s", err, output)
	}
	if current := f.assertPublished(t); current != previous {
		t.Fatalf("failed replacement changed public executable: %s -> %s", previous, current)
	}
	entries, err := filepath.Glob(filepath.Join(f.tools, "pkgconf", pkgconfTestVersion+"-*"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("failed replacement published another immutable prefix: %v, %v", entries, err)
	}
}

const pkgconfFixtureConfigure = `#!/bin/sh
set -eu
if command -v pkg-config >/dev/null 2>&1; then
  echo 'bootstrap unexpectedly sees pkg-config' >&2
  exit 41
fi
case " $* " in *" --prefix=/ "*) ;; *) echo 'unsafe configure prefix' >&2; exit 42 ;; esac
printf 'configure\n' >> "$PKGCONF_FIXTURE_LOG"
"$CC" --version
"$AR" --version
"$RANLIB" --version
`

const pkgconfFixtureZig = `#!/bin/sh
set -eu
case "${1:-}" in
  version) printf '0.16.0\n' ;;
  cc|ar|ranlib) printf '%s\n' "$1" >> "$PKGCONF_FIXTURE_LOG" ;;
  *) echo 'unexpected Zig invocation' >&2; exit 43 ;;
esac
`

const pkgconfFixtureMake = `#!/bin/sh
set -eu
printf 'build\n' >> "$PKGCONF_FIXTURE_LOG"
printf 'partial executable\n' > pkgconf
if [ "${PKGCONF_FIXTURE_FAIL:-}" = 1 ]; then
  echo 'fixture build failed' >&2
  exit 47
fi
sleep 0.05
printf '#!/bin/sh\nprintf "%%s\\n" "%s"\n' "$PKGCONF_FIXTURE_VERSION" > pkgconf
chmod +x pkgconf
`

const pkgconfFixtureCurl = `#!/bin/sh
set -eu
if [ "${PKGCONF_FIXTURE_OFFLINE:-}" = 1 ]; then echo 'fixture network unavailable' >&2; exit 48; fi
printf 'download\n' >> "$PKGCONF_FIXTURE_LOG"
destination=
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then shift; destination="$1"; fi
  shift
done
[ -n "$destination" ]
cp "$PKGCONF_FIXTURE_ARCHIVE" "$destination"
`
