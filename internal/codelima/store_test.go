package codelima

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
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
	wantSlugs := []string{"xsmall", "small", "medium", "large", "xlarge"}
	wantResources := map[string][3]uint32{
		"xsmall": {1, 1 * 1024, 10 * 1024},
		"small":  {2, 4 * 1024, 25 * 1024},
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

func TestSeedAndRepairMigratesUntouchedLegacySmallAndAddsXSmall(t *testing.T) {
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
	small.VCPUs = 1
	small.MemoryMiB = 1 * 1024
	small.DiskMiB = 10 * 1024
	if err := store.SaveConfiguration(small); err != nil {
		t.Fatalf("SaveConfiguration(legacy small) error = %v", err)
	}

	if xsmall, err := store.ConfigurationByIDOrSlug("xsmall"); err == nil {
		if err := os.RemoveAll(store.configurationDir(xsmall.ID)); err != nil {
			t.Fatalf("RemoveAll(xsmall) error = %v", err)
		}
		if err := os.Remove(store.configurationSlugIndexPath(xsmall.Slug)); err != nil {
			t.Fatalf("Remove(xsmall index) error = %v", err)
		}
	} else if !IsNotFound(err) {
		t.Fatalf("ConfigurationByIDOrSlug(xsmall) error = %v", err)
	}
	if err := os.WriteFile(store.seedVersionPath(), []byte("3\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(seed.version) error = %v", err)
	}

	if err := store.seedAndRepair(time.Now().UTC(), false); err != nil {
		t.Fatalf("seedAndRepair() error = %v", err)
	}
	migratedSmall, err := store.ConfigurationByIDOrSlug(DefaultConfigurationSlug)
	if err != nil {
		t.Fatalf("ConfigurationByIDOrSlug(small) after repair error = %v", err)
	}
	if migratedSmall.ID != small.ID || migratedSmall.VCPUs != 2 || migratedSmall.MemoryMiB != 4*1024 || migratedSmall.DiskMiB != 25*1024 {
		t.Fatalf("legacy small was not migrated in place: %+v", migratedSmall)
	}
	xsmall, err := store.ConfigurationByIDOrSlug("xsmall")
	if err != nil {
		t.Fatalf("ConfigurationByIDOrSlug(xsmall) after repair error = %v", err)
	}
	if xsmall.VCPUs != 1 || xsmall.MemoryMiB != 1*1024 || xsmall.DiskMiB != 10*1024 {
		t.Fatalf("xsmall was not seeded with legacy small resources: %+v", xsmall)
	}
	seedMarker, err := os.ReadFile(store.seedVersionPath())
	if err != nil || string(seedMarker) != seedRevision+"\n" {
		t.Fatalf("seed marker = %q, %v", seedMarker, err)
	}
}

func TestSeedAndRepairRunsOnceOnAHomeStampedWithThePreviousRevision(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}

	// A home stamped with the previous revision must re-run the pass exactly
	// once and re-stamp, which is what carries retired-settings-key removal to
	// homes that were already current.
	if err := os.WriteFile(store.seedVersionPath(), []byte("6\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(seed.version) error = %v", err)
	}
	settingsPath := filepath.Join(home, "_config", "settings.yaml")
	if err := os.WriteFile(settingsPath, []byte("daemon:\n  autostart: false\n  virtiofs_reclaim_threshold_percent: 75\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(settings.yaml) error = %v", err)
	}

	if err := store.seedAndRepair(time.Now().UTC(), false); err != nil {
		t.Fatalf("seedAndRepair() error = %v", err)
	}
	marker, err := os.ReadFile(store.seedVersionPath())
	if err != nil || string(marker) != seedRevision+"\n" {
		t.Fatalf("seed marker = %q, %v", marker, err)
	}
	settings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(settings.yaml) error = %v", err)
	}
	if strings.Contains(string(settings), "virtiofs_reclaim_threshold_percent") {
		t.Fatalf("the retired settings key survived the revision bump:\n%s", settings)
	}
}

func TestSeedAndRepairPreservesCustomizedLegacySmall(t *testing.T) {
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
	small.Image = "template:custom"
	small.VCPUs = 1
	small.MemoryMiB = 1 * 1024
	small.DiskMiB = 10 * 1024
	if err := store.SaveConfiguration(small); err != nil {
		t.Fatalf("SaveConfiguration(custom small) error = %v", err)
	}
	if err := os.WriteFile(store.seedVersionPath(), []byte("3\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(seed.version) error = %v", err)
	}

	if err := store.seedAndRepair(time.Now().UTC(), false); err != nil {
		t.Fatalf("seedAndRepair() error = %v", err)
	}
	preserved, err := store.ConfigurationByIDOrSlug(DefaultConfigurationSlug)
	if err != nil {
		t.Fatalf("ConfigurationByIDOrSlug(small) after repair error = %v", err)
	}
	if preserved.Image != "template:custom" || preserved.VCPUs != 1 || preserved.MemoryMiB != 1*1024 || preserved.DiskMiB != 10*1024 {
		t.Fatalf("customized legacy small was overwritten: %+v", preserved)
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

// corruptNodeYAML is invalid YAML at the top level, which is what a torn write
// or a truncated copy of node.yaml looks like in practice.
const corruptNodeYAML = "id: [unterminated\n  \tnot: yaml\n"

func saveTestNode(t *testing.T, store *Store, slug string) Node {
	t.Helper()

	now := time.Now().UTC().Round(0)
	node := Node{
		ID:                 newID(),
		Slug:               slug,
		DirectoryPath:      t.TempDir(),
		Runtime:            RuntimeVM,
		Provider:           ProviderLima,
		SandboxName:        slug,
		Image:              "example/image:1",
		VCPUs:              2,
		MemoryMiB:          2048,
		DiskMiB:            20480,
		Environments:       []string{"codex"},
		Status:             NodeStatusCreated,
		AgentProfileName:   "codex-cli",
		BootstrapCommands:  []string{"true"},
		WorkspaceMode:      WorkspaceModeCopy,
		GuestWorkspacePath: "/home/codelima/workspace",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := store.SaveNode(node, BootstrapState{AgentProfileName: "codex-cli"}); err != nil {
		t.Fatalf("SaveNode(%s) error = %v", slug, err)
	}
	return node
}

// corruptSavedNode damages a node that was saved normally, so its by-slug and
// by-instance index entries stay in place — the realistic shape of the failure.
func corruptSavedNode(t *testing.T, store *Store, node Node) {
	t.Helper()

	if err := os.WriteFile(store.nodePath(node.ID), []byte(corruptNodeYAML), 0o644); err != nil {
		t.Fatalf("WriteFile(corrupt node.yaml) error = %v", err)
	}
}

func isMetadataCorruption(err error) bool {
	var appErr *AppError
	return errors.As(err, &appErr) && appErr.Category == CategoryMetadataCorruption
}

func TestListNodesSkipsCorruptRecordsAndWarns(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}

	first := saveTestNode(t, store, "healthy-one")
	broken := saveTestNode(t, store, "broken")
	second := saveTestNode(t, store, "healthy-two")
	corruptSavedNode(t, store, broken)

	var logs bytes.Buffer
	store.SetLogger(newTextLogger(&logs, slog.LevelDebug))

	nodes, err := store.ListNodes(false)
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("ListNodes() returned %d nodes, want the 2 healthy ones: %+v", len(nodes), nodes)
	}
	for _, node := range nodes {
		if node.ID == broken.ID {
			t.Fatalf("corrupt record was returned as a node: %+v", node)
		}
	}
	if nodes[0].ID != first.ID || nodes[1].ID != second.ID {
		t.Fatalf("healthy nodes were reordered or replaced: %+v", nodes)
	}
	if !strings.Contains(logs.String(), "skipping unreadable node metadata") || !strings.Contains(logs.String(), broken.ID) {
		t.Fatalf("expected a skip warning naming the corrupt record, logs = %q", logs.String())
	}

	corrupt, err := store.CorruptNodeRecords()
	if err != nil {
		t.Fatalf("CorruptNodeRecords() error = %v", err)
	}
	if len(corrupt) != 1 || corrupt[0].NodeID != broken.ID {
		t.Fatalf("CorruptNodeRecords() = %+v, want only %s", corrupt, broken.ID)
	}
	warnings, err := store.CorruptNodeWarnings()
	if err != nil {
		t.Fatalf("CorruptNodeWarnings() error = %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "doctor --repair") {
		t.Fatalf("CorruptNodeWarnings() = %#v", warnings)
	}

	// Sibling list paths share the same enumeration, so a corrupt node must not
	// break configuration listings either.
	if _, err := store.ListConfigurations(false); err != nil {
		t.Fatalf("ListConfigurations() error = %v", err)
	}
}

func TestPointLookupsForACorruptNodeFailWithMetadataCorruption(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}
	saveTestNode(t, store, "healthy")
	broken := saveTestNode(t, store, "broken")
	corruptSavedNode(t, store, broken)

	for name, lookup := range map[string]func() (Node, error){
		"NodeByID":          func() (Node, error) { return store.NodeByID(broken.ID) },
		"NodeByIDOrSlug/id": func() (Node, error) { return store.NodeByIDOrSlug(broken.ID) },
		"NodeByIDOrSlug":    func() (Node, error) { return store.NodeByIDOrSlug("broken") },
		"NodeBySandboxName": func() (Node, error) { return store.NodeBySandboxName("broken") },
	} {
		if _, err := lookup(); !isMetadataCorruption(err) {
			t.Fatalf("%s for a corrupt record = %v, want MetadataCorruption", name, err)
		} else if IsNotFound(err) {
			t.Fatalf("%s pretended the corrupt record was absent", name)
		}
	}

	// An empty node.yaml is valid YAML that unmarshals to the zero value; it is
	// corruption, not a node with no identity.
	if err := os.WriteFile(store.nodePath(broken.ID), nil, 0o644); err != nil {
		t.Fatalf("WriteFile(empty node.yaml) error = %v", err)
	}
	if _, err := store.NodeByID(broken.ID); !isMetadataCorruption(err) {
		t.Fatalf("NodeByID(empty record) = %v, want MetadataCorruption", err)
	}
}

// The daemon forwarder lists nodes once a second and every TUI window polls on
// top of it, so an unchanged home must cost stats and nothing else. A changed
// record must cost exactly one re-parse — its own — and its new content must be
// visible immediately, which is also the write-then-list staleness guarantee.
func TestListNodesServesUnchangedRecordsFromTheParseCache(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}

	first := saveTestNode(t, store, "cache-one")
	saveTestNode(t, store, "cache-two")

	if _, err := store.ListNodes(false); err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	warm := store.parsedRecords.Load()
	if warm == 0 {
		t.Fatalf("expected the first list to parse the node records")
	}

	for pass := range 3 {
		nodes, err := store.ListNodes(false)
		if err != nil {
			t.Fatalf("ListNodes() pass %d error = %v", pass, err)
		}
		if len(nodes) != 2 {
			t.Fatalf("ListNodes() pass %d returned %d nodes, want 2", pass, len(nodes))
		}
	}
	if repeated := store.parsedRecords.Load() - warm; repeated != 0 {
		t.Fatalf("three repeated lists re-parsed %d records, want 0", repeated)
	}

	first.Slug = "cache-one-renamed"
	first.UpdatedAt = time.Now().UTC().Round(0)
	if err := store.SaveNode(first, BootstrapState{AgentProfileName: "codex-cli"}); err != nil {
		t.Fatalf("SaveNode(renamed) error = %v", err)
	}

	beforeChangedList := store.parsedRecords.Load()
	nodes, err := store.ListNodes(false)
	if err != nil {
		t.Fatalf("ListNodes() after save error = %v", err)
	}
	if parsed := store.parsedRecords.Load() - beforeChangedList; parsed != 1 {
		t.Fatalf("one changed record cost %d parses, want exactly 1", parsed)
	}
	var renamed bool
	for _, node := range nodes {
		if node.ID == first.ID {
			renamed = node.Slug == "cache-one-renamed"
		}
	}
	if !renamed {
		t.Fatalf("list after save served the previous record: %+v", nodes)
	}
}

// TestListNodesDetectsARewriteWithTheSameSizeAndTimestamp pins the leg of the
// cache-validity rule that does not depend on the clock. Every metadata write
// renames a fresh inode over the destination, so a record rewritten inside one
// filesystem timestamp tick — the hazard a size+mtime stamp alone would miss —
// is still detected.
func TestListNodesDetectsARewriteWithTheSameSizeAndTimestamp(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}
	node := saveTestNode(t, store, "cache-stamp")
	if _, err := store.ListNodes(false); err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}

	path := store.nodePath(node.ID)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(node.yaml) error = %v", err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(node.yaml) error = %v", err)
	}
	rewritten := bytes.Replace(original, []byte("slug: cache-stamp\n"), []byte("slug: cache-stamq\n"), 1)
	if len(rewritten) != len(original) || bytes.Equal(rewritten, original) {
		t.Fatalf("test rewrite must change the content without changing its size (%d vs %d)", len(rewritten), len(original))
	}
	// atomicWriteFile, not the Store, so nothing invalidates the entry: only
	// the file identity check can catch this.
	if err := atomicWriteFile(path, rewritten, 0o644); err != nil {
		t.Fatalf("atomicWriteFile(node.yaml) error = %v", err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatalf("Chtimes(node.yaml) error = %v", err)
	}

	nodes, err := store.ListNodes(false)
	if err != nil {
		t.Fatalf("ListNodes() after rewrite error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].Slug != "cache-stamq" {
		t.Fatalf("list served a cached record after a same-size, same-mtime rewrite: %+v", nodes)
	}
}

// A record that cannot be parsed is never cached as a failure: it is re-read on
// every pass, so repairing the file is picked up on the next list rather than
// after some expiry.
func TestListNodesRecoversARepairedCorruptRecord(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}
	healthy := saveTestNode(t, store, "cache-healthy")
	broken := saveTestNode(t, store, "cache-broken")

	if nodes, err := store.ListNodes(false); err != nil || len(nodes) != 2 {
		t.Fatalf("ListNodes() = %+v, %v; want both nodes", nodes, err)
	}
	original, err := os.ReadFile(store.nodePath(broken.ID))
	if err != nil {
		t.Fatalf("ReadFile(node.yaml) error = %v", err)
	}

	corruptSavedNode(t, store, broken)
	nodes, err := store.ListNodes(false)
	if err != nil {
		t.Fatalf("ListNodes() with a corrupt record error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != healthy.ID {
		t.Fatalf("ListNodes() = %+v, want only the healthy node", nodes)
	}
	if corrupt, err := store.CorruptNodeRecords(); err != nil || len(corrupt) != 1 {
		t.Fatalf("CorruptNodeRecords() = %+v, %v; want the broken record", corrupt, err)
	}

	if err := os.WriteFile(store.nodePath(broken.ID), original, 0o644); err != nil {
		t.Fatalf("WriteFile(repaired node.yaml) error = %v", err)
	}
	nodes, err = store.ListNodes(false)
	if err != nil {
		t.Fatalf("ListNodes() after repair error = %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("a repaired record stayed skipped: %+v", nodes)
	}
}

// The store is written and read from the same process by the daemon (forwarder
// plus RPC handlers), so a save must never be followed by a stale list, no
// matter how many of them land inside one filesystem timestamp tick.
// TestSaveNodePersistsBootstrapCompletionBeforeTheNodeRecord pins the
// inter-file ordering inside SaveNode. Each write is atomic on its own; the
// hazard is the sequence. bootstrap.json is the record that stops NodeStart
// re-running every install command, so it must be durable before node.yaml
// starts claiming the node is provisioned — otherwise a crash in between
// re-runs a completed bootstrap on the user's node.
//
// The crash is simulated by making the node.yaml write fail: atomicfile renames
// its temp file over the destination, and a destination that is a directory
// makes that rename fail while every other write in the same directory still
// succeeds.
func TestSaveNodePersistsBootstrapCompletionBeforeTheNodeRecord(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}
	node := saveTestNode(t, store, "bootstrap-order")

	// Stand in for a crash at the node.yaml write: atomicfile renames its temp
	// file over the destination, and a destination that is a directory makes
	// exactly that rename fail while every other write in the same directory
	// still succeeds.
	if err := os.Remove(store.nodePath(node.ID)); err != nil {
		t.Fatalf("Remove(node.yaml) error = %v", err)
	}
	if err := os.Mkdir(store.nodePath(node.ID), 0o755); err != nil {
		t.Fatalf("Mkdir(node.yaml) error = %v", err)
	}

	completedAt := time.Now().UTC().Round(0)
	node.BootstrapCompleted = true
	node.BootstrapCompletedAt = &completedAt
	node.Status = NodeStatusRunning
	completed := BootstrapState{AgentProfileName: "codex-cli", Completed: true, CompletedAt: &completedAt}
	if err := store.SaveNode(node, completed); err == nil {
		t.Skip("this platform lets the node.yaml write succeed over a directory")
	}

	stored, err := store.LoadBootstrapState(node.ID)
	if err != nil {
		t.Fatalf("LoadBootstrapState() error = %v", err)
	}
	if !stored.Completed {
		t.Fatal("bootstrap completion was lost by a save that failed at node.yaml: the next start would re-run the whole install on a provisioned node")
	}
}

func TestSaveNodeThenListNeverServesThePreviousRecord(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}
	node := saveTestNode(t, store, "cache-write-read")

	// Persisted lifecycle states only: running/stopped are derived from the
	// runtime at reconcile time and never round-trip through node.yaml.
	for _, status := range []NodeStatus{NodeStatusProvisioning, NodeStatusRegistering, NodeStatusFailed, NodeStatusCreated} {
		node.Status = status
		node.LifecycleState = status
		if err := store.SaveNode(node, BootstrapState{AgentProfileName: "codex-cli"}); err != nil {
			t.Fatalf("SaveNode(%s) error = %v", status, err)
		}
		nodes, err := store.ListNodes(false)
		if err != nil {
			t.Fatalf("ListNodes() error = %v", err)
		}
		if len(nodes) != 1 || nodes[0].Status != status {
			t.Fatalf("list after saving %s = %+v", status, nodes)
		}
	}
}

// Callers mutate the records a list hands back — Service.NodeList writes the
// hydrated configuration slug straight into them — so a cached record must
// never be shared with them.
func TestListNodesHandsOutRecordsCallersCannotWriteBackIntoTheCache(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	store := NewStore(DefaultConfig(home))
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}
	saveTestNode(t, store, "cache-isolation")

	nodes, err := store.ListNodes(false)
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	nodes[0].ConfigurationSlug = "hydrated"
	nodes[0].Environments[0] = "mutated"

	reread, err := store.ListNodes(false)
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if reread[0].ConfigurationSlug != "" || reread[0].Environments[0] != "codex" {
		t.Fatalf("a caller's edits reached the cached record: %+v", reread[0])
	}
}

// The forwarder's once-per-second node.list must cost no metadata parsing at
// all: this is the composed path (node records plus the configuration record
// each node's slug is hydrated from).
func TestNodeListPollingReparsesNothingWhileMetadataIsUnchanged(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)

	for _, slug := range []string{"poll-one", "poll-two"} {
		if _, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: slug}); err != nil {
			t.Fatalf("NodeCreate(%s) error = %v", slug, err)
		}
	}
	if _, err := service.NodeList(ctx, false); err != nil {
		t.Fatalf("NodeList() error = %v", err)
	}

	warm := service.store.parsedRecords.Load()
	for poll := range 3 {
		nodes, err := service.NodeList(ctx, false)
		if err != nil {
			t.Fatalf("NodeList() poll %d error = %v", poll, err)
		}
		if len(nodes) != 2 {
			t.Fatalf("NodeList() poll %d returned %d nodes, want 2", poll, len(nodes))
		}
		for _, node := range nodes {
			if node.ConfigurationSlug == "" {
				t.Fatalf("NodeList() poll %d lost the hydrated configuration slug: %+v", poll, node)
			}
		}
	}
	if repeated := service.store.parsedRecords.Load() - warm; repeated != 0 {
		t.Fatalf("three node.list polls re-parsed %d metadata files, want 0", repeated)
	}
}

func TestNodeListToleratesACorruptNodeRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)

	healthy, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "healthy"})
	if err != nil {
		t.Fatalf("NodeCreate(healthy) error = %v", err)
	}
	broken, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "broken"})
	if err != nil {
		t.Fatalf("NodeCreate(broken) error = %v", err)
	}
	corruptSavedNode(t, service.store, broken)

	// SetLogger must reach the Store too, or a skipped record is invisible in
	// CLI mode: the Store's own fallback sink is the package logger, which is
	// discard outside the TUI.
	var logs bytes.Buffer
	service.SetLogger(newTextLogger(&logs, slog.LevelDebug), slog.LevelDebug)

	// This is the path the TUI, the daemon forwarder, and `node list` all take.
	nodes, err := service.NodeList(ctx, true)
	if err != nil {
		t.Fatalf("NodeList() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != healthy.ID {
		t.Fatalf("NodeList() = %+v, want only the healthy node", nodes)
	}
	if !strings.Contains(logs.String(), "skipping unreadable node metadata") || !strings.Contains(logs.String(), broken.ID) {
		t.Fatalf("the skipped record never reached the service logger, logs = %q", logs.String())
	}

	// Mutating paths run seed-and-repair, which must not abort either.
	if err := service.seedAndRepair(ctx, true); err != nil {
		t.Fatalf("seedAndRepair(force) with a corrupt record error = %v", err)
	}
}

func TestDoctorReportsCorruptNodeRecordsWithoutRepair(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)

	broken, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "broken"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	corruptSavedNode(t, service.store, broken)

	report, err := service.Doctor(ctx, false)
	if err != nil {
		t.Fatalf("Doctor(false) error = %v", err)
	}
	if !containsSubstring(report.Warnings, "unreadable node metadata") {
		t.Fatalf("expected doctor to report the corrupt record, warnings = %#v", report.Warnings)
	}
	if _, statErr := os.Stat(service.store.nodeDir(broken.ID)); statErr != nil {
		t.Fatalf("doctor without --repair moved the corrupt record: %v", statErr)
	}
}

func TestDoctorRepairQuarantinesCorruptNodeRecords(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	store := service.store

	healthy, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "healthy"})
	if err != nil {
		t.Fatalf("NodeCreate(healthy) error = %v", err)
	}
	broken, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "broken"})
	if err != nil {
		t.Fatalf("NodeCreate(broken) error = %v", err)
	}
	brokenEvents := store.nodeEventsPath(broken.ID)
	if _, statErr := os.Stat(brokenEvents); statErr != nil {
		t.Fatalf("expected the broken node to have an events log: %v", statErr)
	}
	corruptSavedNode(t, store, broken)

	report, err := service.Doctor(ctx, true)
	if err != nil {
		t.Fatalf("Doctor(true) error = %v", err)
	}
	quarantineCheck := ""
	for _, check := range report.Checks {
		if check.Name == "corrupt_records" {
			quarantineCheck = check.Message
		}
	}
	if !strings.Contains(quarantineCheck, "quarantined 1") {
		t.Fatalf("corrupt_records check = %q, checks = %#v", quarantineCheck, report.Checks)
	}
	if !containsSubstring(report.Warnings, "nothing was deleted") {
		t.Fatalf("expected doctor --repair to report the move, warnings = %#v", report.Warnings)
	}

	if _, statErr := os.Stat(store.nodeDir(broken.ID)); !os.IsNotExist(statErr) {
		t.Fatalf("corrupt record was left in nodes/: %v", statErr)
	}
	entries, err := os.ReadDir(store.quarantineRoot())
	if err != nil {
		t.Fatalf("ReadDir(_quarantine) error = %v", err)
	}
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), "-"+broken.ID) {
		t.Fatalf("_quarantine contents = %+v, want one <timestamp>-%s directory", entries, broken.ID)
	}
	quarantined := filepath.Join(store.quarantineRoot(), entries[0].Name())
	for _, name := range []string{"node.yaml", "events.jsonl", "quarantine.yaml"} {
		if _, statErr := os.Stat(filepath.Join(quarantined, name)); statErr != nil {
			t.Fatalf("quarantined record is missing %s: %v", name, statErr)
		}
	}
	data, err := os.ReadFile(filepath.Join(quarantined, "node.yaml"))
	if err != nil || string(data) != corruptNodeYAML {
		t.Fatalf("quarantined node.yaml was rewritten: %q, %v", data, err)
	}

	// Indexes must not point at a directory that is no longer there.
	if _, statErr := os.Stat(store.nodeSlugIndexPath("broken")); !os.IsNotExist(statErr) {
		t.Fatalf("by-slug index still claims the quarantined node: %v", statErr)
	}
	if _, statErr := os.Stat(store.nodeInstanceIndexPath(broken.SandboxName)); !os.IsNotExist(statErr) {
		t.Fatalf("by-instance index still claims the quarantined node: %v", statErr)
	}
	if _, err := store.NodeByIDOrSlug("broken"); !IsNotFound(err) {
		t.Fatalf("NodeByIDOrSlug(quarantined) = %v, want NotFound", err)
	}

	// The healthy node and its indexes are untouched.
	if _, err := store.NodeByIDOrSlug("healthy"); err != nil {
		t.Fatalf("NodeByIDOrSlug(healthy) after repair error = %v", err)
	}
	if _, statErr := os.Stat(store.nodeSlugIndexPath("healthy")); statErr != nil {
		t.Fatalf("healthy by-slug index was removed: %v", statErr)
	}
	if _, statErr := os.Stat(store.nodeInstanceIndexPath(healthy.SandboxName)); statErr != nil {
		t.Fatalf("healthy by-instance index was removed: %v", statErr)
	}

	nodes, err := store.ListNodes(true)
	if err != nil {
		t.Fatalf("ListNodes() after repair error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != healthy.ID {
		t.Fatalf("ListNodes() after repair = %+v, want only the healthy node", nodes)
	}
	remaining, err := store.CorruptNodeRecords()
	if err != nil {
		t.Fatalf("CorruptNodeRecords() after repair error = %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("corrupt records remain after repair: %+v", remaining)
	}

	// A second repair is a no-op rather than a second move.
	second, err := service.Doctor(ctx, true)
	if err != nil {
		t.Fatalf("Doctor(true) second run error = %v", err)
	}
	for _, check := range second.Checks {
		if check.Name == "corrupt_records" && !strings.Contains(check.Message, "quarantined 0") {
			t.Fatalf("second repair quarantined again: %#v", check)
		}
	}
}

func TestFreshHomeWithFinderJunkStillInitializes(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".DS_Store"), []byte("finder junk"), 0o644); err != nil {
		t.Fatalf("WriteFile(.DS_Store) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".localized"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile(.localized) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, "nodes"), 0o755); err != nil {
		t.Fatalf("MkdirAll(nodes) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "nodes", ".DS_Store"), []byte("finder junk"), 0o644); err != nil {
		t.Fatalf("WriteFile(nodes/.DS_Store) error = %v", err)
	}

	if err := NewStore(DefaultConfig(home)).EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() with Finder junk error = %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(home, "_config", "schema.version"))
	if err != nil || string(marker) != "4\n" {
		t.Fatalf("schema marker = %q, %v", marker, err)
	}
}

func TestHomeWithAnUnknownFileIsStillRejected(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".DS_Store"), []byte("finder junk"), 0o644); err != nil {
		t.Fatalf("WriteFile(.DS_Store) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "someone-elses-data.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatalf("WriteFile(unknown) error = %v", err)
	}

	err := NewStore(DefaultConfig(home)).EnsureLayout()
	if err == nil || !strings.Contains(err.Error(), "unrecognized home layout") {
		t.Fatalf("expected an unrecognized-home rejection, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "_config", "schema.version")); !os.IsNotExist(statErr) {
		t.Fatalf("rejection still adopted the home: %v", statErr)
	}
}

func TestNestedUnknownFileIsStillRejectedAlongsideFinderJunk(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "nodes"), 0o755); err != nil {
		t.Fatalf("MkdirAll(nodes) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "nodes", ".DS_Store"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile(nodes/.DS_Store) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "nodes", "mystery.bin"), []byte("?"), 0o644); err != nil {
		t.Fatalf("WriteFile(nodes/mystery.bin) error = %v", err)
	}

	err := NewStore(DefaultConfig(home)).EnsureLayout()
	if err == nil || !strings.Contains(err.Error(), "unrecognized home layout") {
		t.Fatalf("expected an unrecognized-home rejection, got %v", err)
	}
}
