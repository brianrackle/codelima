package codelima

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRuntimeCommandsFileAcceptsWrappedAndBareYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	wrappedPath := filepath.Join(dir, "wrapped.yaml")
	barePath := filepath.Join(dir, "bare.yaml")

	if err := os.WriteFile(wrappedPath, []byte("runtime_commands:\n  start:\n    - \"{{binary}} start {{sandbox_name}} --vm-type=vz\"\n    - \"{{binary}} start {{sandbox_name}} --tty=false\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(wrapped) error = %v", err)
	}
	if err := os.WriteFile(barePath, []byte("start: \"{{binary}} start {{sandbox_name}} --tty=false\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(bare) error = %v", err)
	}

	wrapped, err := loadRuntimeCommandsFile(wrappedPath)
	if err != nil {
		t.Fatalf("loadRuntimeCommandsFile(wrapped) error = %v", err)
	}
	if got := len(wrapped.Start); got != 2 {
		t.Fatalf("expected wrapped file to load both start overrides, got %v", wrapped.Start)
	}
	if wrapped.Start[0] != "{{binary}} start {{sandbox_name}} --vm-type=vz" || wrapped.Start[1] != "{{binary}} start {{sandbox_name}} --tty=false" {
		t.Fatalf("expected wrapped file start override, got %q", wrapped.Start)
	}

	bare, err := loadRuntimeCommandsFile(barePath)
	if err != nil {
		t.Fatalf("loadRuntimeCommandsFile(bare) error = %v", err)
	}
	if got := strings.Join(bare.Start, "|"); got != "{{binary}} start {{sandbox_name}} --tty=false" {
		t.Fatalf("expected bare file start override, got %q", bare.Start)
	}
}

func TestRuntimeCommandsAllowLifecycleCustomization(t *testing.T) {
	t.Parallel()
	node := Node{RuntimeCommands: RuntimeCommandTemplates{Start: []string{"{{binary}} start --custom {{sandbox_name}}"}}}
	commands, err := resolveConfiguredRuntimeCommands("limactl", defaultRuntimeCommandTemplates(), node.RuntimeCommands, runtimeCommandStart, map[string]string{"sandbox_name": shellQuote("demo")})
	if err != nil {
		t.Fatalf("resolveConfiguredRuntimeCommands() error = %v", err)
	}
	if got := strings.Join(commands, "|"); got != "'limactl' start --custom 'demo'" {
		t.Fatalf("custom start commands = %q", got)
	}
}

func TestDefaultRuntimeCommandsUseLima(t *testing.T) {
	t.Parallel()
	defaults := defaultRuntimeCommandTemplates()
	for name, commands := range map[string][]string{
		"version": defaults.Version, "list": defaults.List, "create": defaults.Create,
		"start": defaults.Start, "stop": defaults.Stop, "delete": defaults.Delete,
		"clone": defaults.Clone, "copy": defaults.Copy, "shell_exec": defaults.ShellExec,
	} {
		if len(commands) == 0 || !strings.Contains(commands[0], "{{binary}}") {
			t.Fatalf("%s defaults = %#v", name, commands)
		}
	}
	prepare := strings.Join(defaults.WorkspaceSeedPrepare, "\n")
	if !strings.Contains(prepare, "SUDO_USER") || !strings.Contains(prepare, `chown "$owner:$group"`) {
		t.Fatalf("workspace seed preparation must return the target parent to Lima's SSH user: %q", prepare)
	}
}

func TestRuntimeCommandsAreBuiltInTracksOverrides(t *testing.T) {
	t.Parallel()
	// The loaded settings always carry the defaults merged in, so an untouched
	// home must still read as built-in.
	loaded := RuntimeCommandTemplates{}.ApplyDefaults(defaultRuntimeCommandTemplates())
	for _, kind := range []runtimeCommandKind{runtimeCommandVersion, runtimeCommandList, runtimeCommandStart, runtimeCommandShellExec} {
		if !runtimeCommandsAreBuiltIn(loaded, RuntimeCommandTemplates{}, kind) {
			t.Fatalf("%s must resolve to the built-in definition", kind)
		}
	}

	global := loaded
	global.List = []string{"{{binary}} list --json | tee /tmp/list.json"}
	if runtimeCommandsAreBuiltIn(global, RuntimeCommandTemplates{}, runtimeCommandList) {
		t.Fatal("settings override must not read as built-in")
	}
	if !runtimeCommandsAreBuiltIn(global, RuntimeCommandTemplates{}, runtimeCommandStart) {
		t.Fatal("an unrelated override must not change other kinds")
	}

	node := RuntimeCommandTemplates{Start: []string{"{{binary}} start --custom {{sandbox_name}}"}}
	if runtimeCommandsAreBuiltIn(loaded, node, runtimeCommandStart) {
		t.Fatal("node override must not read as built-in")
	}
	if got := effectiveRuntimeCommandTemplates(global, node, runtimeCommandStart); strings.Join(got, "|") != node.Start[0] {
		t.Fatalf("node metadata must win the precedence chain, got %q", got)
	}
	if got := effectiveRuntimeCommandTemplates(global, node, runtimeCommandList); strings.Join(got, "|") != global.List[0] {
		t.Fatalf("settings must win over the built-in defaults, got %q", got)
	}
}

func TestDefaultRuntimeStartCanEnableNestedVirtualization(t *testing.T) {
	t.Parallel()
	commands, err := resolveConfiguredRuntimeCommands("limactl", defaultRuntimeCommandTemplates(), RuntimeCommandTemplates{}, runtimeCommandStart, map[string]string{
		"sandbox_name":               shellQuote("demo"),
		"nested_virtualization_flag": " --nested-virt",
	})
	if err != nil {
		t.Fatalf("resolveConfiguredRuntimeCommands() error = %v", err)
	}
	if got := strings.Join(commands, "|"); got != "'limactl' start -y 'demo' --nested-virt" {
		t.Fatalf("nested start commands = %q", got)
	}
}
