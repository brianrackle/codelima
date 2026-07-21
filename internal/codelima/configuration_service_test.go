package codelima

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfigurationIsPersistedEditableAndProtected(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.ConfigurationCreate(context.Background(), ConfigurationCreateInput{Slug: "derived"})
	if err != nil {
		t.Fatalf("ConfigurationCreate() error = %v", err)
	}
	if created.VCPUs != 1 || created.MemoryMiB != 1024 || created.DiskMiB != 10240 {
		t.Fatalf("new configuration did not copy default: %+v", created)
	}
	if got := strings.Join(created.Environments, ","); got != "codex,claude-code" {
		t.Fatalf("new configuration did not copy default coding-agent environments: %q", got)
	}
	defaultConfiguration, err := service.ConfigurationShow(context.Background(), DefaultConfigurationSlug)
	if err != nil {
		t.Fatal(err)
	}
	memory := uint32(8192)
	updatedDefault, err := service.ConfigurationUpdate(context.Background(), defaultConfiguration.ID, ConfigurationUpdateInput{MemoryMiB: &memory})
	if err != nil || updatedDefault.MemoryMiB != 8192 {
		t.Fatalf("default update = %+v, %v", updatedDefault, err)
	}
	unchanged, err := service.ConfigurationShow(context.Background(), created.ID)
	if err != nil || unchanged.MemoryMiB != 1024 {
		t.Fatalf("existing configuration changed with default: %+v, %v", unchanged, err)
	}
	if _, err := service.ConfigurationUpdate(context.Background(), defaultConfiguration.ID, ConfigurationUpdateInput{Slug: "renamed"}); err == nil {
		t.Fatalf("default configuration rename succeeded")
	}
	if _, err := service.ConfigurationDelete(context.Background(), defaultConfiguration.ID); err == nil {
		t.Fatalf("default configuration delete succeeded")
	}
}

func TestLegacyDefaultConfigurationSlugCannotBeReused(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	if _, err := service.ConfigurationCreate(context.Background(), ConfigurationCreateInput{Slug: "default"}); err == nil {
		t.Fatal("created a configuration with the retired default slug")
	}
	configuration, err := service.ConfigurationCreate(context.Background(), ConfigurationCreateInput{Slug: "custom"})
	if err != nil {
		t.Fatalf("ConfigurationCreate(custom) error = %v", err)
	}
	if _, err := service.ConfigurationUpdate(context.Background(), configuration.ID, ConfigurationUpdateInput{Slug: "default"}); err == nil {
		t.Fatal("renamed a configuration to the retired default slug")
	}
}

func TestNodeFreezesConfigurationValuesAtCreation(t *testing.T) {
	service, directory := newTestService(t)
	vcpus, memory, disk := uint8(6), uint32(6144), uint32(30720)
	configuration, err := service.ConfigurationCreate(context.Background(), ConfigurationCreateInput{Slug: "custom", VCPUs: &vcpus, MemoryMiB: &memory, DiskMiB: &disk, BootstrapCommands: []string{"printf configured"}})
	if err != nil {
		t.Fatal(err)
	}
	node, err := service.NodeCreate(context.Background(), NodeCreateInput{Configuration: configuration.ID, Directory: directory, Slug: "worker"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	newMemory := uint32(12288)
	if _, err := service.ConfigurationUpdate(context.Background(), configuration.ID, ConfigurationUpdateInput{MemoryMiB: &newMemory, BootstrapCommands: []string{"printf changed"}}); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.NodeShow(context.Background(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MemoryMiB != 6144 || strings.Join(loaded.Environments, ",") != "codex,claude-code" || len(loaded.BootstrapCommands) == 0 || loaded.BootstrapCommands[len(loaded.BootstrapCommands)-1] != "printf configured" {
		t.Fatalf("node did not retain frozen values: %+v", loaded)
	}
}

func TestDirectoryScopingAllowsMultipleNodesAndRejectsPrefixCollisions(t *testing.T) {
	service, root := newTestService(t)
	child := filepath.Join(root, "child")
	prefixCollision := root + "-other"
	for _, directory := range []string{child, prefixCollision} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, input := range []NodeCreateInput{
		{Directory: root, Slug: "root-a"},
		{Directory: root, Slug: "root-b"},
		{Directory: child, Slug: "child"},
		{Directory: prefixCollision, Slug: "collision"},
	} {
		if _, err := service.NodeCreate(context.Background(), input); err != nil {
			t.Fatalf("NodeCreate(%s) error = %v", input.Slug, err)
		}
	}
	scoped, err := service.NodeListByDirectoryRoot(context.Background(), root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 3 || scoped[0].Slug != "root-a" || scoped[1].Slug != "root-b" || scoped[2].Slug != "child" {
		t.Fatalf("directory scope = %+v", scoped)
	}
}

func TestNodeCloneKeepsDirectoryConfigurationAndFrozenResources(t *testing.T) {
	service, directory := newTestService(t)
	source, err := service.NodeCreate(context.Background(), NodeCreateInput{Directory: directory, Slug: "source"})
	if err != nil {
		t.Fatal(err)
	}
	cloned, err := service.NodeClone(context.Background(), NodeCloneInput{SourceNode: source.ID, NodeSlug: "clone"})
	if err != nil {
		t.Fatalf("NodeClone() error = %v", err)
	}
	if cloned.DirectoryPath != source.DirectoryPath || cloned.ConfigurationID != source.ConfigurationID || cloned.ConfigurationSlug != source.ConfigurationSlug || cloned.MemoryMiB != source.MemoryMiB || cloned.ParentNodeID != source.ID {
		t.Fatalf("clone changed frozen association: source=%+v clone=%+v", source, cloned)
	}
}

func TestConfigurationDeleteIsBlockedWhileReferenced(t *testing.T) {
	service, directory := newTestService(t)
	configuration, err := service.ConfigurationCreate(context.Background(), ConfigurationCreateInput{Slug: "temporary"})
	if err != nil {
		t.Fatal(err)
	}
	node, err := service.NodeCreate(context.Background(), NodeCreateInput{Configuration: configuration.ID, Directory: directory, Slug: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfigurationDelete(context.Background(), configuration.ID); err == nil {
		t.Fatalf("referenced configuration delete succeeded")
	}
	if _, err := service.NodeDelete(context.Background(), node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfigurationDelete(context.Background(), configuration.ID); err != nil {
		t.Fatalf("configuration delete after node delete = %v", err)
	}
}
