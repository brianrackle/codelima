package codelima

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureLayoutCreatesSchemaV3WithoutProjectStorage(t *testing.T) {
	home := t.TempDir()
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}
	for _, path := range []string{"configurations", "environments", "nodes", "_config/settings.yaml", "_config/schema.version"} {
		if _, err := os.Stat(filepath.Join(home, path)); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
	for _, removed := range []string{"projects", "environment-configs", "_index/projects"} {
		if _, err := os.Stat(filepath.Join(home, removed)); !os.IsNotExist(err) {
			t.Fatalf("removed v2 path still exists: %s", removed)
		}
	}
	marker, err := os.ReadFile(filepath.Join(home, "_config", "schema.version"))
	if err != nil || string(marker) != "3\n" {
		t.Fatalf("schema marker = %q, %v", marker, err)
	}
	configuration, err := store.ConfigurationByIDOrSlug(DefaultConfigurationSlug)
	if err != nil {
		t.Fatalf("default configuration missing: %v", err)
	}
	if configuration.VCPUs != 2 || configuration.MemoryMiB != 4096 || configuration.DiskMiB != 20480 {
		t.Fatalf("unexpected default resources: %+v", configuration)
	}
	if got := strings.Join(configuration.Environments, ","); got != "codex,claude-code" {
		t.Fatalf("expected default coding-agent environments, got %q", got)
	}
}

func TestEnsureLayoutPreservesExistingDefaultConfigurationEnvironments(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}
	configuration, err := store.ConfigurationByIDOrSlug(DefaultConfigurationSlug)
	if err != nil {
		t.Fatalf("ConfigurationByIDOrSlug(default) error = %v", err)
	}
	configuration.Environments = []string{}
	if err := store.SaveConfiguration(configuration); err != nil {
		t.Fatalf("SaveConfiguration(default) error = %v", err)
	}
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() second error = %v", err)
	}
	preserved, err := store.ConfigurationByIDOrSlug(DefaultConfigurationSlug)
	if err != nil {
		t.Fatalf("ConfigurationByIDOrSlug(default) after repair error = %v", err)
	}
	if len(preserved.Environments) != 0 {
		t.Fatalf("expected explicitly cleared default environments to stay cleared, got %#v", preserved.Environments)
	}
}

func TestSchemaV2HomeIsRejectedWithoutMutation(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "_config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(configDir, "schema.version")
	if err := os.WriteFile(marker, []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := NewStore(DefaultConfig(home)).EnsureLayout()
	if err == nil || !strings.Contains(err.Error(), "schema v2") || !strings.Contains(err.Error(), "new directory") {
		t.Fatalf("expected actionable v2 rejection, got %v", err)
	}
	data, readErr := os.ReadFile(marker)
	if readErr != nil || string(data) != "2\n" {
		t.Fatalf("v2 marker was mutated: %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(home, "configurations")); !os.IsNotExist(statErr) {
		t.Fatalf("v2 rejection mutated the home")
	}
}

func TestConfigurationAndFrozenNodeRoundTrip(t *testing.T) {
	home := t.TempDir()
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Round(0)
	configuration := Configuration{ID: newID(), Slug: "large", Image: "example/image:1", AgentProfileName: "codex-cli", Environments: []string{"codex"}, BootstrapCommands: []string{"true"}, VCPUs: 8, MemoryMiB: 8192, DiskMiB: 40960, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveConfiguration(configuration); err != nil {
		t.Fatalf("SaveConfiguration() error = %v", err)
	}
	directory := t.TempDir()
	node := Node{ID: newID(), Slug: "worker", ConfigurationID: configuration.ID, DirectoryPath: directory, Runtime: RuntimeVM, Provider: ProviderMicrosandbox, SandboxName: "worker", Image: configuration.Image, VCPUs: configuration.VCPUs, MemoryMiB: configuration.MemoryMiB, DiskMiB: configuration.DiskMiB, Environments: []string{"codex"}, Status: NodeStatusCreated, AgentProfileName: "codex-cli", BootstrapCommands: []string{"true"}, WorkspaceMode: WorkspaceModeCopy, GuestWorkspacePath: directory, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveNode(node, BootstrapState{AgentProfileName: "codex-cli"}); err != nil {
		t.Fatalf("SaveNode() error = %v", err)
	}
	loaded, err := store.NodeByID(node.ID)
	if err != nil {
		t.Fatalf("NodeByID() error = %v", err)
	}
	if loaded.ConfigurationID != configuration.ID || loaded.DirectoryPath != directory || loaded.VCPUs != 8 || loaded.MemoryMiB != 8192 || loaded.DiskMiB != 40960 {
		t.Fatalf("frozen node fields did not round trip: %+v", loaded)
	}
	data, err := os.ReadFile(store.nodePath(node.ID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "project_id") || strings.Contains(string(data), "runtime_commands") {
		t.Fatalf("v3 node metadata contains removed fields:\n%s", data)
	}
}
