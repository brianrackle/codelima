package codelima

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// TestUserSurfaceShellsRunAsTheLoginUser covers both shapes of the one
// user-facing entry into a guest. `Service.Shell` backs the `shell` verb and,
// through TerminalLaunchSpec re-entering the binary as `codelima shell
// <nodeID>`, every managed terminal; neither shape may ask for root.
//
// The explicit-command case is not an oversight. `codelima shell node -- cmd`
// is still something the user typed, so it gets the identity a user gets; a
// user who wants root types `sudo` in front of their command (ADR 129).
func TestUserSurfaceShellsRunAsTheLoginUser(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name        string
		command     []string
		interactive bool
	}{
		{name: "interactive terminal", command: nil, interactive: true},
		{name: "explicit command", command: []string{"--", "id", "-un"}, interactive: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			service, workspace := newTestService(t)
			writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

			node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "login-surface"})
			if err != nil {
				t.Fatalf("NodeCreate() error = %v", err)
			}

			fake := service.sandbox.(*fakeSandbox)
			fake.mu.Lock()
			fake.shellCalls = nil
			fake.mu.Unlock()

			if err := service.Shell(ctx, node.ID, testCase.command); err != nil {
				t.Fatalf("Shell() error = %v", err)
			}

			calls := recordedShellCalls(fake)
			if len(calls) != 1 {
				t.Fatalf("shell calls = %#v, want one", calls)
			}
			if calls[0].identity != guestLoginUser {
				t.Fatalf("shell identity = %q, want %q", calls[0].identity, guestLoginUser)
			}
			if calls[0].interactive != testCase.interactive {
				t.Fatalf("interactive = %v, want %v", calls[0].interactive, testCase.interactive)
			}
		})
	}
}

// TestServiceIssuedGuestCommandsRunAsRoot is the other half of the split. Every
// guest command a node start issues on the user's behalf — the copy-mode seed
// prepare, the frozen bootstrap and agent-install steps, and the agent
// validation probe — is provisioning, and provisioning keeps root.
//
// It asserts over the whole recorded sequence rather than named calls so a new
// service-issued command cannot be added at the login identity by accident.
func TestServiceIssuedGuestCommandsRunAsRoot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory:     workspace,
		Slug:          "service-surface",
		WorkspaceMode: WorkspaceModeCopy,
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	if _, err := service.NodeStart(ctx, node.ID); err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	fake := service.sandbox.(*fakeSandbox)
	calls := recordedShellCalls(fake)
	if len(calls) < 3 {
		t.Fatalf("node start issued %d guest commands, want the seed prepare, bootstrap, and validation at least: %#v", len(calls), calls)
	}
	for index, call := range calls {
		if call.identity != guestRootUser {
			t.Fatalf("service-issued guest command %d ran as %q, want %q: %v", index, call.identity, guestRootUser, call.command)
		}
	}

	// The same node, the same Service, one user-typed command: the split is by
	// surface, not by node or by state.
	if err := service.Shell(ctx, node.ID, []string{"--", "id", "-un"}); err != nil {
		t.Fatalf("Shell() error = %v", err)
	}
	userCalls := recordedShellCalls(fake)
	if last := userCalls[len(userCalls)-1]; last.identity != guestLoginUser {
		t.Fatalf("user shell after start ran as %q, want %q", last.identity, guestLoginUser)
	}
}

// TestCopyModeSeedLeavesTheGuestWorkspaceOwnedByTheLoginUser proves the seeded
// tree is writable by the terminal that now opens in it.
//
// The seed has two halves with two identities on purpose. The prepare command
// is root — it clears a prior tree and creates the target's parent anywhere on
// the guest filesystem — and its last act is to hand that parent to the login
// user. The tree itself is then written by `limactl cp`, which is a host-side
// runtime command that travels over Lima's SSH login and never passes through
// the Shell seam, so it has never been able to acquire root. The test runs the
// real prepare script and inspects the resulting owner rather than trusting the
// template's text.
func TestCopyModeSeedLeavesTheGuestWorkspaceOwnedByTheLoginUser(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory:     workspace,
		Slug:          "copy-seed",
		WorkspaceMode: WorkspaceModeCopy,
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	if _, err := service.NodeStart(ctx, node.ID); err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	fake := service.sandbox.(*fakeSandbox)
	prepare := recordedShellCalls(fake)[0]
	if prepare.identity != guestRootUser {
		t.Fatalf("workspace seed prepare identity = %q, want %q", prepare.identity, guestRootUser)
	}

	fake.mu.Lock()
	copyCalls := append([]fakeCopyCall(nil), fake.copyCalls...)
	fake.mu.Unlock()
	if len(copyCalls) != 1 || !copyCalls[0].recursive || copyCalls[0].targetPath != workspace {
		t.Fatalf("workspace seed copy calls = %#v, want one recursive copy to %q", copyCalls, workspace)
	}

	// Run the prepare script the guest would have run, against a throwaway tree
	// on this host, and check who owns the directory the copy writes into.
	current, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current() error = %v", err)
	}
	guestRoot := t.TempDir()
	targetPath := filepath.Join(guestRoot, "guest", "workspace")
	script, err := service.resolveWorkspaceSeedPrepareCommand(node, node.DirectoryPath, targetPath)
	if err != nil {
		t.Fatalf("resolveWorkspaceSeedPrepareCommand() error = %v", err)
	}
	command := exec.Command("sh", "-c", script)
	command.Env = append(os.Environ(), "SUDO_USER="+current.Username)
	if output, runErr := command.CombinedOutput(); runErr != nil {
		t.Fatalf("workspace seed prepare script %q failed: %v (%s)", script, runErr, output)
	}

	info, err := os.Stat(filepath.Dir(targetPath))
	if err != nil {
		t.Fatalf("Stat(seed target parent) error = %v", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("platform does not expose file ownership")
	}
	if got := strconv.FormatUint(uint64(stat.Uid), 10); got != current.Uid {
		t.Fatalf("seed target parent uid = %s, want the login user's %s: the copy could not write into it", got, current.Uid)
	}

	// Seeding is once-only, which is why an already-seeded workspace whose
	// contents were later made root-owned is repaired by hand (`sudo chown -R`
	// in the node's own terminal) rather than by a recursive chown on every
	// start. A second start must not touch the workspace at all.
	if _, err := service.NodeStart(ctx, node.ID); err != nil {
		t.Fatalf("NodeStart(second) error = %v", err)
	}
	fake.mu.Lock()
	copyCallsAfterRestart := len(fake.copyCalls)
	fake.mu.Unlock()
	if copyCallsAfterRestart != 1 {
		t.Fatalf("copy calls after a second start = %d, want the seed to stay once-only", copyCallsAfterRestart)
	}
}

func recordedShellCalls(fake *fakeSandbox) []fakeShellCall {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]fakeShellCall(nil), fake.shellCalls...)
}

// TestInteractiveShellBootstrapMakesNoRootAssumptions guards the login-shell
// script the managed terminal execs. It reaches for root exactly once, to
// repair /bin/stty, and does it through `sudo -n` with a `|| true` so a guest
// without passwordless sudo still gets a shell. Everything else it touches —
// $HOME, $SHELL, the temporary inputrc — is now the login user's.
func TestInteractiveShellBootstrapMakesNoRootAssumptions(t *testing.T) {
	t.Parallel()

	script := strings.Join(interactiveShellLaunchCommand(), " ")
	for _, line := range strings.Split(script, "\n") {
		if !strings.Contains(line, "sudo") {
			continue
		}
		if !strings.Contains(line, "sudo -n ln -sf /usr/bin/gnustty") || !strings.Contains(line, "|| true") {
			t.Fatalf("interactive shell bootstrap has an unguarded root assumption: %q", line)
		}
	}
	if strings.Contains(script, "/root") {
		t.Fatalf("interactive shell bootstrap hardcodes root's home:\n%s", script)
	}
}
