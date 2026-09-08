package release

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/rendererbuild"
	"github.com/brianrackle/codelima/internal/testutil"
)

func installerScript(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "scripts", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func installerWrite(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func releaseGoBinary(t testing.TB) string {
	t.Helper()
	candidate := os.Getenv("CODELIMA_TOOLING_GO")
	if candidate == "" {
		candidate = "go"
	}
	path, err := exec.LookPath(candidate)
	if err != nil {
		t.Fatalf("resolve test Go toolchain (run make test): %v", err)
	}
	return path
}

func installerCommand(t testing.TB, ctx context.Context, script, tools, scratch string) *exec.Cmd {
	command := exec.CommandContext(ctx, "sh", script, "0.16.0", tools, scratch)
	command.Env = append(os.Environ(), "CODELIMA_TOOLING_GO="+releaseGoBinary(t))
	return command
}

func zigInstallerPlatform(t *testing.T) (string, string) {
	t.Helper()
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/arm64":
		return "aarch64-linux", "ea4b09bfb22ec6f6c6ceac57ab63efb6b46e17ab08d21f69f3a48b38e1534f17"
	case "linux/amd64":
		return "x86_64-linux", "70e49664a74374b48b51e6f3fdfbf437f6395d42509050588bd49abe52ba3d00"
	case "darwin/arm64":
		return "aarch64-macos", "b23d70deaa879b5c2d486ed3316f7eaa53e84acf6fc9cc747de152450d401489"
	case "darwin/amd64":
		return "x86_64-macos", "0387557ed1877bc6a2e1802c8391953baddba76081876301c522f52977b52ba7"
	default:
		t.Skip("native Zig installer supports Linux and macOS on amd64/arm64")
		return "", ""
	}
}

func TestZigInstallerRejectsCorruptCacheWithoutPublishing(t *testing.T) {
	t.Parallel()
	platform, _ := zigInstallerPlatform(t)
	root := testutil.TempDir(t, "installer-")
	tools, scratch := filepath.Join(root, "tools with spaces"), filepath.Join(root, "scratch")
	archive := filepath.Join(tools, "cache", "zig-0.16.0-"+platform+".tar.xz")
	installerWrite(t, archive, "truncated or corrupted archive", 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := installerCommand(t, ctx, installerScript(t, "install_zig.sh"), tools, scratch).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "checksum mismatch") {
		t.Fatalf("corrupt archive result = %v, %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(tools, "zig", "0.16.0")); !os.IsNotExist(err) {
		t.Fatalf("invalid installation was published: %v", err)
	}
	assertInstallerLockReleased(t, filepath.Join(tools, "zig", ".install.lock"))
	entries, err := os.ReadDir(scratch)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed install retained scratch: %v, %v", entries, err)
	}
}

func assertInstallerLockReleased(t *testing.T, path string) {
	t.Helper()
	lock, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("installer retained kernel lock: %v", err)
	}
}

func TestRendererInstallerProfileMatchesGoAndNativeInputs(t *testing.T) {
	t.Parallel()
	root := testutil.TempDir(t, "installer-profile-")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	script := installerScript(t, "renderer_build.go")
	cmd := exec.CommandContext(ctx, releaseGoBinary(t), "run", script,
		"-patches", filepath.Join(filepath.Dir(script), "patches"), "-output", root)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generate profile: %v, %s", err, output)
	}
	expected := rendererbuild.DefaultProfile().Fingerprint()
	if strings.TrimSpace(string(output)) != expected {
		t.Fatalf("generated identity = %q, want %s", output, expected)
	}
	for _, name := range []string{".native-build-id", "codelima_build.h", "codelima_build.c"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || !strings.Contains(string(data), expected) {
			t.Errorf("%s missing canonical identity: %v, %s", name, err, data)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(root, "build-profile.json"))
	if err != nil || strings.TrimSpace(string(manifest)) != string(rendererbuild.DefaultProfile().Manifest()) {
		t.Fatalf("canonical profile changed: %v, %s", err, manifest)
	}
}

func TestRendererInstallerRejectsUnreviewedPatchBeforePublishing(t *testing.T) {
	t.Parallel()
	root := testutil.TempDir(t, "installer-profile-")
	patches, outputDir := filepath.Join(root, "patches"), filepath.Join(root, "output")
	installerWrite(t, filepath.Join(patches, "ghostty-vt-codelima.patch"), "unreviewed patch content", 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, releaseGoBinary(t), "run", installerScript(t, "renderer_build.go"),
		"-patches", patches, "-output", outputDir)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "checksum differs from reviewed") {
		t.Fatalf("unreviewed patch accepted: %v, %s", err, output)
	}
	if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
		t.Fatalf("failed validation published profile: %v", err)
	}
}

func TestInstallerLockRemainsHeldBySurvivingBuildChild(t *testing.T) {
	t.Parallel()
	root := testutil.TempDir(t, "installer-lock-")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	helper := filepath.Join(root, "tooling-lock")
	build := exec.CommandContext(ctx, releaseGoBinary(t), "build", "-o", helper, installerScript(t, "tooling_lock.go"))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build lock helper: %v, %s", err, output)
	}
	lockPath := filepath.Join(root, "install.lock")
	// The shell exits while its simulated build child is still running. A lock
	// held only by the shell PID would now permit a competing installer.
	command := exec.CommandContext(ctx, helper, lockPath, "sh", "-c", "sleep 1 & exit 0")
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
		t.Fatalf("surviving build child did not retain the lock: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock was not released after build child exit: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestZigInstallerReusesVerifiedInstallConcurrentlyOffline(t *testing.T) {
	t.Parallel()
	_, checksum := zigInstallerPlatform(t)
	root := testutil.TempDir(t, "installer-")
	tools, scratch := filepath.Join(root, "tools"), filepath.Join(root, "scratch")
	installed := filepath.Join(tools, "zig", "0.16.0")
	installerWrite(t, filepath.Join(installed, "zig"), "#!/bin/sh\nprintf '0.16.0\\n'\n", 0o755)
	installerWrite(t, filepath.Join(installed, ".archive-sha256"), checksum+"\n", 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	script := installerScript(t, "install_zig.sh")
	var workers sync.WaitGroup
	for range 3 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			output, err := installerCommand(t, ctx, script, tools, scratch).CombinedOutput()
			if err != nil {
				t.Errorf("concurrent verified install: %v, %s", err, output)
			}
		}()
	}
	workers.Wait()
	entries, err := os.ReadDir(filepath.Join(tools, "cache"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("verified offline reuse touched download cache: %v, %v", entries, err)
	}
}

func TestZigInstallerPreservesUnverifiedExistingInstall(t *testing.T) {
	t.Parallel()
	zigInstallerPlatform(t)
	root := testutil.TempDir(t, "installer-")
	tools, scratch := filepath.Join(root, "tools"), filepath.Join(root, "scratch")
	installed := filepath.Join(tools, "zig", "0.16.0")
	installerWrite(t, filepath.Join(installed, "zig"), "#!/bin/sh\nprintf 'unexpected-version\\n'\n", 0o755)
	marker := filepath.Join(installed, "preserve")
	installerWrite(t, marker, "previous installation", 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := installerCommand(t, ctx, installerScript(t, "install_zig.sh"), tools, scratch).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "no matching verified stamp") {
		t.Fatalf("unverified install result = %v, %s", err, output)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "previous installation" {
		t.Fatalf("previous install was changed: %s, %v", data, err)
	}
}

func TestGhosttyInstallerPreservesCurrentOnSourceMismatch(t *testing.T) {
	t.Parallel()
	zigInstallerPlatform(t)
	root := testutil.TempDir(t, "installer-")
	tools, scratch := filepath.Join(root, "tools"), filepath.Join(root, "scratch")
	base := filepath.Join(tools, "ghostty-vt")
	const revision = "82232ecde55405559dec29c5466cb9e39938cb41"
	if err := os.MkdirAll(filepath.Join(base, "sources", revision, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	previous := filepath.Join(base, "previous")
	installerWrite(t, filepath.Join(previous, "lib", "libghostty-vt.a"), "previous archive", 0o644)
	if err := os.Symlink(previous, filepath.Join(base, "current")); err != nil {
		t.Fatal(err)
	}
	commands := filepath.Join(root, "commands")
	zig := filepath.Join(commands, "zig")
	installerWrite(t, zig, "#!/bin/sh\nprintf '0.16.0\\n'\n", 0o755)
	installerWrite(t, filepath.Join(commands, "git"), "#!/bin/sh\nprintf '1111111111111111111111111111111111111111\\n'\n", 0o755)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", installerScript(t, "install_ghostty_vt.sh"), revision, zig, tools, scratch)
	cmd.Env = append(os.Environ(), "PATH="+commands+string(os.PathListSeparator)+os.Getenv("PATH"), "CODELIMA_TOOLING_GO="+releaseGoBinary(t))
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "source revision mismatch") {
		t.Fatalf("source mismatch result = %v, %s", err, output)
	}
	if target, err := os.Readlink(filepath.Join(base, "current")); err != nil || target != previous {
		t.Fatalf("failed build changed current: %s, %v", target, err)
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".install.") && entry.Name() != ".install.lock" {
			t.Fatalf("failed build retained staging/lock: %s", entry.Name())
		}
	}
}
