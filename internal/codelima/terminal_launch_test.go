package codelima

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

// expectedSelfExecutable mirrors how the Service resolves the codelima binary
// for a node shell: os.Executable + resolveCodelimaExecutablePath.
func expectedSelfExecutable(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	return resolveCodelimaExecutablePath(exe)
}

func TestTerminalLaunchSpecNodeShell(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	wantExe := expectedSelfExecutable(t)

	spec, err := service.TerminalLaunchSpec(terminal.NodeTarget("node-abc"), terminal.NodeShell, "")
	if err != nil {
		t.Fatalf("TerminalLaunchSpec(NodeShell) error = %v", err)
	}

	wantArgv := []string{wantExe, "--home", service.cfg.MetadataRoot, "shell", "node-abc"}
	if !reflect.DeepEqual(spec.Argv, wantArgv) {
		t.Fatalf("node argv = %#v, want %#v", spec.Argv, wantArgv)
	}
	if spec.Dir != "" {
		t.Fatalf("node dir = %q, want empty", spec.Dir)
	}
	if len(spec.Env) == 0 {
		t.Fatalf("expected node launch env to be populated")
	}
}

func TestTerminalLaunchSpecNodeHostShell(t *testing.T) {
	t.Parallel()

	service, workspace := newTestService(t)

	spec, err := service.TerminalLaunchSpec(terminal.NodeTarget("node-abc"), terminal.NodeHostShell, workspace)
	if err != nil {
		t.Fatalf("TerminalLaunchSpec(NodeHostShell) error = %v", err)
	}

	if got, want := strings.Join(spec.Argv, " "), strings.Join(interactiveShellLaunchCommand(), " "); got != want {
		t.Fatalf("host argv = %q, want %q", got, want)
	}
	if spec.Dir != workspace {
		t.Fatalf("host dir = %q, want %q", spec.Dir, workspace)
	}
	if len(spec.Env) == 0 {
		t.Fatalf("expected host launch env to be populated")
	}
}

func TestTerminalLaunchSpecErrors(t *testing.T) {
	t.Parallel()

	service, workspace := newTestService(t)

	filePath := filepath.Join(workspace, "a-file")
	writeFile(t, filePath, "not a dir\n")
	missingDir := filepath.Join(workspace, "does-not-exist")

	cases := []struct {
		name      string
		target    terminal.TargetKey
		kind      terminal.TerminalKind
		workspace string
	}{
		{"empty node id", terminal.NodeTarget(""), terminal.NodeShell, ""},
		{"missing directory path", terminal.NodeTarget("n"), terminal.NodeHostShell, ""},
		{"non-existent directory", terminal.NodeTarget("n"), terminal.NodeHostShell, missingDir},
		{"directory is a file", terminal.NodeTarget("n"), terminal.NodeHostShell, filePath},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := service.TerminalLaunchSpec(tc.target, tc.kind, tc.workspace)
			if err == nil {
				t.Fatalf("expected error, got spec %#v", spec)
			}
			var appErr *AppError
			if !errors.As(err, &appErr) {
				t.Fatalf("expected a typed AppError, got %T: %v", err, err)
			}
			if appErr.Category != "InvalidArgument" {
				t.Fatalf("expected InvalidArgument category, got %q", appErr.Category)
			}
		})
	}
}

// TestOpenTabsSpawnExactlyLaunchSpec pins the 2.2 contract: OpenNodeTab and
// OpenNodeHostTab spawn precisely the argv/dir the Service's TerminalLaunchSpec
// returns — no second command-building path in the store.
func TestOpenTabsSpawnExactlyLaunchSpec(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	store := newTUISessionStore(ctx, service, func(vaxis.Event) {})

	var term *fakeTUITerminal
	store.newTerminal = func(string, func(vaxis.Event)) tuiTerminal {
		term = newFakeTUITerminal()
		return term
	}

	// Node tab spawns exactly the node LaunchSpec.
	nodeSpec, err := service.TerminalLaunchSpec(terminal.NodeTarget("node-x"), terminal.NodeShell, "")
	if err != nil {
		t.Fatalf("TerminalLaunchSpec(node) error = %v", err)
	}
	if _, err := store.OpenNodeTab(Node{ID: "node-x", Slug: "node-x"}); err != nil {
		t.Fatalf("OpenNodeTab() error = %v", err)
	}
	if term == nil || term.startCmd == nil {
		t.Fatal("expected OpenNodeTab to start a terminal")
	}
	if !reflect.DeepEqual(term.startCmd.Args, nodeSpec.Argv) {
		t.Fatalf("node startCmd.Args = %#v, want %#v", term.startCmd.Args, nodeSpec.Argv)
	}
	if term.startCmd.Path != nodeSpec.Argv[0] {
		t.Fatalf("node startCmd.Path = %q, want %q", term.startCmd.Path, nodeSpec.Argv[0])
	}
	if term.startCmd.Dir != nodeSpec.Dir {
		t.Fatalf("node startCmd.Dir = %q, want %q", term.startCmd.Dir, nodeSpec.Dir)
	}

	// Node host tab spawns exactly the node host-shell LaunchSpec.
	term = nil
	hostSpec, err := service.TerminalLaunchSpec(terminal.NodeTarget("node-y"), terminal.NodeHostShell, workspace)
	if err != nil {
		t.Fatalf("TerminalLaunchSpec(node host) error = %v", err)
	}
	if _, err := store.OpenNodeHostTab(Node{ID: "node-y", Slug: "node-y", DirectoryPath: workspace}); err != nil {
		t.Fatalf("OpenNodeHostTab() error = %v", err)
	}
	if term == nil || term.startCmd == nil {
		t.Fatal("expected OpenNodeHostTab to start a terminal")
	}
	if !reflect.DeepEqual(term.startCmd.Args, hostSpec.Argv) {
		t.Fatalf("host startCmd.Args = %#v, want %#v", term.startCmd.Args, hostSpec.Argv)
	}
	if term.startCmd.Dir != hostSpec.Dir {
		t.Fatalf("host startCmd.Dir = %q, want %q", term.startCmd.Dir, hostSpec.Dir)
	}
}

// writableInGo reports whether the current process can create a file under dir.
// Used to keep the read-only-home assertions honest even when tests run as root
// (where DAC permission bits are bypassed).
func writableInGo(dir string) bool {
	probe := filepath.Join(dir, ".codelima-write-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return true
}

// shellProbeEnv returns os.Environ() with the given keys overridden (not merely
// appended) so the child shell sees exactly these values — appending duplicate
// keys is unreliable because glibc getenv resolves the first occurrence.
func shellProbeEnv(overrides map[string]string) []string {
	out := make([]string, 0, len(os.Environ())+len(overrides))
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq >= 0 {
			key := kv[:eq]
			// Drop any inherited INPUTRC so the child starts without one — these
			// tests assert whether the script sets it, and a leaked parent value
			// (this very feature exports one) would mask a skipped customization.
			if key == "INPUTRC" {
				continue
			}
			if _, ok := overrides[key]; ok {
				continue
			}
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

// reportingShell writes a tiny SHELL replacement that prints the INPUTRC it was
// launched with and exits 0, so the INPUTRC-probing logic is observable.
func reportingShell(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "reporting-shell")
	writeExecutable(t, path, "#!/bin/sh\nprintf 'INPUTRC=%s\\n' \"${INPUTRC:-<unset>}\"\nexit 0\n")
	return path
}

// TestInteractiveShellLaunchCommandToleratesReadOnlyHome is the TODO #18
// regression: a read-only $HOME must not fail the shell — the INPUTRC temp file
// falls back to a writable location, or is skipped entirely.
func TestInteractiveShellLaunchCommandToleratesReadOnlyHome(t *testing.T) {
	args := interactiveShellLaunchCommand()
	shell := reportingShell(t)

	roHome := t.TempDir()
	if err := os.Chmod(roHome, 0o555); err != nil {
		t.Fatalf("Chmod(roHome) error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(roHome, 0o755) })

	writableTmp := t.TempDir()

	cmd := exec.Command(args[0], args[1:]...)
	// A login shell resets PWD to its cwd; root it in the read-only home so
	// ${PWD}/tmp cannot become a stray writable fallback.
	cmd.Dir = roHome
	cmd.Env = shellProbeEnv(map[string]string{
		"HOME":   roHome,
		"TMPDIR": writableTmp,
		"SHELL":  shell,
	})
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("interactive shell with read-only HOME failed: %v\noutput:\n%s", err, out)
	}
	assertNoTempError(t, out)

	// When the process genuinely cannot write to HOME (non-root), the INPUTRC
	// must come from the writable fallback rather than HOME.
	if !writableInGo(roHome) {
		line := inputrcLine(t, out)
		if strings.Contains(line, roHome) {
			t.Fatalf("expected INPUTRC to avoid the read-only home, got %q", line)
		}
		if !strings.HasPrefix(strings.TrimPrefix(line, "INPUTRC="), writableTmp) {
			t.Fatalf("expected INPUTRC under the writable fallback %q, got %q", writableTmp, line)
		}
	}
}

// TestInteractiveShellLaunchCommandSkipsInputrcWhenNowhereWritable pins that
// with no writable location at all the shell still launches, simply without the
// INPUTRC customization.
func TestInteractiveShellLaunchCommandSkipsInputrcWhenNowhereWritable(t *testing.T) {
	args := interactiveShellLaunchCommand()
	shell := reportingShell(t)

	roHome := t.TempDir()
	roTmp := t.TempDir()
	for _, dir := range []string{roHome, roTmp} {
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatalf("Chmod(%s) error = %v", dir, err)
		}
		d := dir
		t.Cleanup(func() { _ = os.Chmod(d, 0o755) })
	}

	// Note: running as root bypasses the permission bits, so "nowhere writable"
	// cannot be simulated there. In that case the guarded assertion below is
	// skipped and this test only enforces the no-error contract.

	cmd := exec.Command(args[0], args[1:]...)
	// A login shell resets PWD to its cwd; root it in a read-only dir so
	// ${PWD}/tmp cannot become a stray writable fallback.
	cmd.Dir = roHome
	cmd.Env = shellProbeEnv(map[string]string{
		"HOME":   roHome,
		"TMPDIR": roTmp,
		"SHELL":  shell,
	})
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("interactive shell with no writable temp failed: %v\noutput:\n%s", err, out)
	}
	assertNoTempError(t, out)
	if !writableInGo(roHome) && !writableInGo(roTmp) {
		if line := inputrcLine(t, out); strings.TrimPrefix(line, "INPUTRC=") != "<unset>" {
			t.Fatalf("expected INPUTRC to be skipped when nowhere is writable, got %q", line)
		}
	}
}

// assertNoTempError fails if the interactive shell leaked a temp-file creation
// error (a read-only mount surfaces as "Read-only file system"; a non-writable
// dir as "Permission denied") — the fix suppresses those probes with 2>/dev/null.
func assertNoTempError(t *testing.T, out []byte) {
	t.Helper()
	for _, bad := range []string{"Read-only file system", "Permission denied", "mktemp:"} {
		if bytes.Contains(out, []byte(bad)) {
			t.Fatalf("interactive shell leaked a temp-file error %q:\n%s", bad, out)
		}
	}
}

func inputrcLine(t *testing.T, out []byte) string {
	t.Helper()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "INPUTRC=") {
			return line
		}
	}
	t.Fatalf("no INPUTRC line in output:\n%s", out)
	return ""
}
