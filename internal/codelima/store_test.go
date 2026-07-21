package codelima

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureLayoutCreatesSchemaV4WithoutProjectStorage(t *testing.T) {
	home := t.TempDir()
	cfg := DefaultConfig(home)
	store := NewStore(cfg)
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
	if err != nil || string(marker) != "4\n" {
		t.Fatalf("schema marker = %q, %v", marker, err)
	}
	configurations, err := store.ListConfigurations(false)
	if err != nil {
		t.Fatalf("ListConfigurations() error = %v", err)
	}
	wantSlugs := []string{"small", "medium", "large", "xlarge"}
	wantResources := map[string][3]uint32{
		"small":  {1, 1 * 1024, 10 * 1024},
		"medium": {4, 8 * 1024, 50 * 1024},
		"large":  {6, 16 * 1024, 75 * 1024},
		"xlarge": {8, 32 * 1024, 100 * 1024},
	}
	if len(configurations) != len(wantSlugs) {
		t.Fatalf("configuration count = %d, want %d: %+v", len(configurations), len(wantSlugs), configurations)
	}
	for index, configuration := range configurations {
		if configuration.Slug != wantSlugs[index] {
			t.Fatalf("configuration %d slug = %q, want %q", index, configuration.Slug, wantSlugs[index])
		}
		resources := wantResources[configuration.Slug]
		if uint32(configuration.VCPUs) != resources[0] || configuration.MemoryMiB != resources[1] || configuration.DiskMiB != resources[2] {
			t.Fatalf("unexpected %s resources: %+v", configuration.Slug, configuration)
		}
		if configuration.Image != cfg.DefaultImage || configuration.AgentProfileName != cfg.DefaultAgentProfile || strings.Join(configuration.Environments, ",") != "codex,claude-code" {
			t.Fatalf("unexpected %s defaults: %+v", configuration.Slug, configuration)
		}
	}
	if _, err := store.ConfigurationByIDOrSlug("default"); !IsNotFound(err) {
		t.Fatalf("fresh home still exposes legacy default configuration: %v", err)
	}
}

func TestSeedAndRepairAddsMissingAndPreservesCustomizedAndDeletedBuiltInConfigurations(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}

	small, err := store.ConfigurationByIDOrSlug("small")
	if err != nil {
		t.Fatalf("ConfigurationByIDOrSlug(small) error = %v", err)
	}
	small.MemoryMiB = 2 * 1024
	if err := store.SaveConfiguration(small); err != nil {
		t.Fatalf("SaveConfiguration(small) error = %v", err)
	}

	medium, err := store.ConfigurationByIDOrSlug("medium")
	if err != nil {
		t.Fatalf("ConfigurationByIDOrSlug(medium) error = %v", err)
	}
	deletedAt := time.Now().UTC()
	medium.DeletedAt = &deletedAt
	medium.UpdatedAt = deletedAt
	if err := store.SaveConfiguration(medium); err != nil {
		t.Fatalf("SaveConfiguration(medium) error = %v", err)
	}
	xlarge, err := store.ConfigurationByIDOrSlug("xlarge")
	if err != nil {
		t.Fatalf("ConfigurationByIDOrSlug(xlarge) error = %v", err)
	}
	if err := os.RemoveAll(store.configurationDir(xlarge.ID)); err != nil {
		t.Fatalf("RemoveAll(xlarge) error = %v", err)
	}
	if err := os.Remove(store.configurationSlugIndexPath(xlarge.Slug)); err != nil {
		t.Fatalf("Remove(xlarge index) error = %v", err)
	}

	if err := os.WriteFile(store.seedVersionPath(), []byte("1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(seed.version) error = %v", err)
	}
	if err := store.seedAndRepair(deletedAt.Add(time.Second), false); err != nil {
		t.Fatalf("seedAndRepair() error = %v", err)
	}

	preservedSmall, err := store.ConfigurationByIDOrSlug("small")
	if err != nil {
		t.Fatalf("ConfigurationByIDOrSlug(small) after repair error = %v", err)
	}
	if preservedSmall.MemoryMiB != 2*1024 {
		t.Fatalf("customized small configuration was overwritten: %+v", preservedSmall)
	}
	preservedMedium, err := store.ConfigurationByIDOrSlug("medium")
	if err != nil {
		t.Fatalf("ConfigurationByIDOrSlug(medium) after repair error = %v", err)
	}
	if preservedMedium.DeletedAt == nil {
		t.Fatalf("deleted medium configuration was resurrected: %+v", preservedMedium)
	}
	seededXLarge, err := store.ConfigurationByIDOrSlug("xlarge")
	if err != nil {
		t.Fatalf("ConfigurationByIDOrSlug(xlarge) after repair error = %v", err)
	}
	if seededXLarge.VCPUs != 8 || seededXLarge.MemoryMiB != 32*1024 || seededXLarge.DiskMiB != 100*1024 {
		t.Fatalf("missing xlarge configuration was not reseeded: %+v", seededXLarge)
	}
}

func TestSeedAndRepairRestoresDeletedSmallAsRequiredDefault(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}

	small, err := store.ConfigurationByIDOrSlug(DefaultConfigurationSlug)
	if err != nil {
		t.Fatalf("ConfigurationByIDOrSlug(small) error = %v", err)
	}
	deletedAt := time.Now().UTC()
	small.MemoryMiB = 3 * 1024
	small.DeletedAt = &deletedAt
	small.UpdatedAt = deletedAt
	if err := store.SaveConfiguration(small); err != nil {
		t.Fatalf("SaveConfiguration(small) error = %v", err)
	}
	if err := os.WriteFile(store.seedVersionPath(), []byte("2\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(seed.version) error = %v", err)
	}

	if err := store.seedAndRepair(deletedAt.Add(time.Second), false); err != nil {
		t.Fatalf("seedAndRepair() error = %v", err)
	}
	restored, err := store.ConfigurationByIDOrSlug(DefaultConfigurationSlug)
	if err != nil {
		t.Fatalf("ConfigurationByIDOrSlug(small) after repair error = %v", err)
	}
	if restored.DeletedAt != nil {
		t.Fatalf("required small default remains deleted: %+v", restored)
	}
	if restored.MemoryMiB != 3*1024 {
		t.Fatalf("restoring small overwrote its customized values: %+v", restored)
	}
}

func TestSeedAndRepairRetiresLegacyDefaultConfigurationButKeepsItResolvableByID(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cfg := DefaultConfig(home)
	store := NewStore(cfg)
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}

	legacy, err := store.ConfigurationByIDOrSlug("default")
	if IsNotFound(err) {
		now := time.Now().UTC()
		legacy = Configuration{
			ID: newID(), Slug: "default", Image: cfg.DefaultImage, AgentProfileName: cfg.DefaultAgentProfile,
			Environments: defaultConfigurationEnvironmentSlugs(), BootstrapCommands: []string{},
			VCPUs: 2, MemoryMiB: 4 * 1024, DiskMiB: 20 * 1024, CreatedAt: now, UpdatedAt: now,
		}
		if err := store.SaveConfiguration(legacy); err != nil {
			t.Fatalf("SaveConfiguration(legacy default) error = %v", err)
		}
	} else if err != nil {
		t.Fatalf("ConfigurationByIDOrSlug(default) error = %v", err)
	}

	if err := os.WriteFile(store.seedVersionPath(), []byte("2\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(seed.version) error = %v", err)
	}
	if err := store.seedAndRepair(time.Now().UTC(), false); err != nil {
		t.Fatalf("seedAndRepair() error = %v", err)
	}

	configurations, err := store.ListConfigurations(false)
	if err != nil {
		t.Fatalf("ListConfigurations() error = %v", err)
	}
	for _, configuration := range configurations {
		if configuration.Slug == "default" {
			t.Fatalf("legacy default configuration remains in live list: %+v", configurations)
		}
	}
	retired, err := store.ConfigurationByID(legacy.ID)
	if err != nil {
		t.Fatalf("ConfigurationByID(legacy default) error = %v", err)
	}
	if retired.DeletedAt == nil {
		t.Fatalf("legacy default configuration was not retired: %+v", retired)
	}
}

func TestSchemaV3HomeIsRejectedWithoutMutation(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "_config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(configDir, "schema.version")
	if err := os.WriteFile(marker, []byte("3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nodeDir := filepath.Join(home, "nodes", "existing")
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nodePath := filepath.Join(nodeDir, "node.yaml")
	const original = "provider: microsandbox\nsandbox_name: existing\n"
	if err := os.WriteFile(nodePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	err := NewStore(DefaultConfig(home)).EnsureLayout()
	if err == nil || !strings.Contains(err.Error(), "schema v3") || !strings.Contains(err.Error(), "new directory") {
		t.Fatalf("expected actionable v3 rejection, got %v", err)
	}
	if data, readErr := os.ReadFile(marker); readErr != nil || string(data) != "3\n" {
		t.Fatalf("v3 marker was mutated: %q, %v", data, readErr)
	}
	if data, readErr := os.ReadFile(nodePath); readErr != nil || string(data) != original {
		t.Fatalf("v3 node was mutated: %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(home, "configurations")); !os.IsNotExist(statErr) {
		t.Fatalf("v3 rejection created schema-v4 directories")
	}
}

func TestEnsureLayoutPreservesExistingSmallConfigurationEnvironments(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}
	configuration, err := store.ConfigurationByIDOrSlug(DefaultConfigurationSlug)
	if err != nil {
		t.Fatalf("ConfigurationByIDOrSlug(small) error = %v", err)
	}
	configuration.Environments = []string{}
	if err := store.SaveConfiguration(configuration); err != nil {
		t.Fatalf("SaveConfiguration(small) error = %v", err)
	}
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() second error = %v", err)
	}
	preserved, err := store.ConfigurationByIDOrSlug(DefaultConfigurationSlug)
	if err != nil {
		t.Fatalf("ConfigurationByIDOrSlug(small) after repair error = %v", err)
	}
	if len(preserved.Environments) != 0 {
		t.Fatalf("expected explicitly cleared small environments to stay cleared, got %#v", preserved.Environments)
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
	configuration := Configuration{ID: newID(), Slug: "round-trip", Image: "example/image:1", AgentProfileName: "codex-cli", Environments: []string{"codex"}, BootstrapCommands: []string{"true"}, VCPUs: 8, MemoryMiB: 8192, DiskMiB: 40960, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveConfiguration(configuration); err != nil {
		t.Fatalf("SaveConfiguration() error = %v", err)
	}
	directory := t.TempDir()
	node := Node{ID: newID(), Slug: "worker", ConfigurationID: configuration.ID, DirectoryPath: directory, Runtime: RuntimeVM, Provider: ProviderLima, SandboxName: "worker", Image: configuration.Image, VCPUs: configuration.VCPUs, MemoryMiB: configuration.MemoryMiB, DiskMiB: configuration.DiskMiB, Environments: []string{"codex"}, Status: NodeStatusCreated, AgentProfileName: "codex-cli", BootstrapCommands: []string{"true"}, WorkspaceMode: WorkspaceModeCopy, GuestWorkspacePath: directory, CreatedAt: now, UpdatedAt: now}
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
