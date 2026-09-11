package codelima

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the generated shell with only the network, account lookup, privilege
// switch and system link replaced. The installer itself runs as this test user.
func TestNativeAgentInstallerExecution(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("installer success cases require an unprivileged test process")
	}
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []struct{ executable, url, interpreter string }{
		{"codex", "https://chatgpt.com/codex/install.sh", "sh"},
		{"claude", "https://claude.ai/install.sh", "bash"},
	} {
		for _, mode := range []string{"success", "download-failure", "installer-failure", "missing-binary", "root"} {
			t.Run(agent.executable+"/"+mode, func(t *testing.T) {
				root := t.TempDir()
				home := filepath.Join(root, "guest home")
				bin := filepath.Join(root, "stubs")
				if err := os.MkdirAll(bin, 0o755); err != nil {
					t.Fatal(err)
				}
				scripts := map[string]string{
					"getent": `printf '%s:x:1000:1000::%s:/bin/sh\n' "$SUDO_USER" "$TEST_GUEST_HOME"`,
					"sudo": `test "$1" = -u && test "$2" = "$TEST_GUEST_USER" && test "$3" = -H || exit 91
shift 3
exec "$@"`,
					"curl": `test "$1" = -fsSL && test "$2" = "$TEST_INSTALL_URL" && test "$3" = -o || exit 93
cat > "$4" <<'INSTALLER'
set -eu
test "$(id -u)" -ne 0
test "$HOME" = "$TEST_GUEST_HOME"
test "$(id -un)" = "$TEST_GUEST_USER"
printf executed > "$HOME/executed"
[ "$TEST_MODE" != installer-failure ] || exit 31
[ "$TEST_MODE" != missing-binary ] || exit 0
printf '#!/bin/sh\nexit 0\n' > "$HOME/.local/bin/$TEST_EXECUTABLE"
chmod +x "$HOME/.local/bin/$TEST_EXECUTABLE"
INSTALLER
[ "$TEST_MODE" != download-failure ] || exit 22`,
				}
				// The link is the sole privileged mutation after installation succeeds.
				scripts["ln"] = `test "$1" = -sfn && test "$2" = "$TEST_GUEST_HOME/.local/bin/$TEST_EXECUTABLE" && test "$3" = "/usr/local/bin/$TEST_EXECUTABLE" || exit 94
printf linked > "$TEST_GUEST_HOME/linked"`
				for name, script := range scripts {
					if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nset -eu\n"+script), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				guest := account.Username
				if mode == "root" {
					guest = "root"
				}
				cmd := exec.Command("sh", "-c", nativeAgentInstallCommand(agent.url, agent.interpreter, agent.executable))
				cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SUDO_USER="+guest,
					"TEST_GUEST_USER="+account.Username, "TEST_GUEST_HOME="+home, "TEST_INSTALL_URL="+agent.url,
					"TEST_EXECUTABLE="+agent.executable, "TEST_MODE="+mode)
				output, err := cmd.CombinedOutput()
				if (err == nil) != (mode == "success") {
					t.Fatalf("result = %v, output: %s", err, output)
				}
				if exists(filepath.Join(home, "linked")) != (mode == "success") {
					t.Fatal("system link published before successful installation")
				}
				if exists(filepath.Join(home, "executed")) != (mode != "download-failure" && mode != "root") {
					t.Fatal("unexpected installer execution")
				}
				scratch, err := filepath.Glob(filepath.Join(home, ".cache/codelima/agent-install.*"))
				if err != nil || len(scratch) != 0 {
					t.Fatalf("installer scratch survived: %v, %v", scratch, err)
				}
			})
		}
	}
}

func TestEveryLegacyAgentEnvironmentMigratesToNative(t *testing.T) {
	for slug, specs := range legacyBuiltInEnvironmentConfigs() {
		for _, spec := range specs {
			service, _ := newTestService(t)
			ctx := context.Background()
			if err := service.EnsureReady(ctx, true); err != nil {
				t.Fatal(err)
			}
			if _, err := service.EnvironmentConfigUpdate(ctx, slug, EnvironmentConfigUpdateInput{BootstrapCommands: spec.BootstrapCommands}); err != nil {
				t.Fatal(err)
			}
			writeFile(t, service.store.seedVersionPath(), "8\n")
			if err := service.EnsureReady(ctx, true); err != nil {
				t.Fatal(err)
			}
			got, err := service.EnvironmentConfigShow(ctx, slug)
			if err != nil {
				t.Fatal(err)
			}
			if containsSubstring(got.BootstrapCommands, "npm ") || !containsSubstring(got.BootstrapCommands, "install.sh") {
				t.Fatalf("legacy %s not migrated: %v", slug, got.BootstrapCommands)
			}
			if _, changed := migrateKnownBuiltInBootstrapCommands(got.BootstrapCommands); changed {
				t.Fatal("native bootstrap is not idempotent")
			}
			customized := []string{"echo custom " + strings.Join(spec.BootstrapCommands, " ")}
			if _, changed := migrateKnownBuiltInBootstrapCommands(customized); changed {
				t.Fatal("custom bootstrap changed")
			}
		}
	}
}

func TestCodexBubblewrapProvisioning(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "installed", true: "missing"}[fail], func(t *testing.T) {
			service, workspace := newTestService(t)
			ctx := context.Background()
			node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "bubblewrap"})
			if err != nil {
				t.Fatal(err)
			}
			fake := service.sandbox.(*fakeSandbox)
			if fail {
				fake.failCommand = "bwrap --version"
			}
			_, err = service.NodeStart(ctx, node.ID)
			if (err != nil) != fail {
				t.Fatalf("NodeStart error = %v, want failure %v", err, fail)
			}
			if !containsSubstring(fake.calls, "apt-get install -y ca-certificates curl git bubblewrap") || !containsSubstring(fake.calls, "bwrap --version") {
				t.Fatalf("Codex bootstrap must install and validate bubblewrap: %v", fake.calls)
			}
			bootstrap, err := service.store.LoadBootstrapState(node.ID)
			if err != nil {
				t.Fatal(err)
			}
			if bootstrap.Completed == fail {
				t.Fatalf("bootstrap completion = %v with failure %v", bootstrap.Completed, fail)
			}
		})
	}
}
