package codelima

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveProjectWritesCommentedLimaCommandTemplateWhenOverridesUnset(t *testing.T) {
	t.Parallel()

	home := filepath.Join(t.TempDir(), ".codelima")
	cfg := DefaultConfig(home)
	cfg.LimaCommands.Start = "{{binary}} start {{instance_name}} --vm-type=vz"
	store := NewStore(cfg)
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}

	project := Project{
		ID:                  newID(),
		Slug:                "root",
		WorkspacePath:       "/workspace/root",
		AgentProfileName:    "codex-cli",
		EnvironmentConfigs:  []string{},
		SetupCommands:       []string{},
		DefaultRuntime:      RuntimeVM,
		DefaultProvider:     ProviderLima,
		DefaultLimaTemplate: "template:default",
		CreatedAt:           time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}

	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject() error = %v", err)
	}

	data, err := os.ReadFile(store.projectPath(project.ID))
	if err != nil {
		t.Fatalf("ReadFile(project.yaml) error = %v", err)
	}

	output := string(data)
	if !strings.Contains(output, projectLimaCommandsTemplateComment) {
		t.Fatalf("expected project metadata to include the override template comment, got %s", output)
	}
	if !strings.Contains(output, "\n# lima_commands:\n") {
		t.Fatalf("expected project metadata to include a commented lima_commands block, got %s", output)
	}
	if !strings.Contains(output, "#     workspace_seed_prepare: sudo rm -rf {{target_path}} && sudo mkdir -p {{target_parent}} && sudo chown \"$(id -un)\":\"$(id -gn)\" {{target_parent}}") {
		t.Fatalf("expected project metadata to include the default workspace seed prepare command example, got %s", output)
	}
	if !strings.Contains(output, "#     start: '{{binary}} start {{instance_name}} --vm-type=vz'") {
		t.Fatalf("expected project metadata to include the global default start command example, got %s", output)
	}
}

func TestEnsureLayoutBackfillsProjectMetadataWithCommentedLimaCommandTemplate(t *testing.T) {
	t.Parallel()

	home := filepath.Join(t.TempDir(), ".codelima")
	cfg := DefaultConfig(home)
	store := NewStore(cfg)
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}

	projectID := "project-1"
	projectPath := store.projectPath(projectID)
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(project dir) error = %v", err)
	}
	legacyProject := `id: project-1
slug: root
workspace_path: /workspace/root
agent_profile_name: codex-cli
environment_configs: []
environment_commands: []
default_runtime: vm
default_provider: lima
default_lima_template: template:default
created_at: 2026-03-25T00:00:00Z
updated_at: 2026-03-25T00:00:00Z
`
	if err := os.WriteFile(projectPath, []byte(legacyProject), 0o644); err != nil {
		t.Fatalf("WriteFile(project.yaml) error = %v", err)
	}

	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout(second pass) error = %v", err)
	}

	data, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatalf("ReadFile(project.yaml) error = %v", err)
	}

	output := string(data)
	if !strings.Contains(output, projectLimaCommandsTemplateComment) {
		t.Fatalf("expected rewritten project metadata to include the override template comment, got %s", output)
	}
	if !strings.Contains(output, "\n# lima_commands:\n") {
		t.Fatalf("expected rewritten project metadata to include a commented lima_commands block, got %s", output)
	}

	project, err := store.ProjectByID(projectID)
	if err != nil {
		t.Fatalf("ProjectByID() error = %v", err)
	}
	if project.LimaCommands.IsZero() != true {
		t.Fatalf("expected legacy project to keep zero explicit command overrides, got %#v", project.LimaCommands)
	}
}

func TestSaveNodeWritesCommentedLimaCommandTemplateWhenOverridesUnset(t *testing.T) {
	t.Parallel()

	home := filepath.Join(t.TempDir(), ".codelima")
	cfg := DefaultConfig(home)
	store := NewStore(cfg)
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}

	project := Project{
		ID:                  newID(),
		Slug:                "root",
		WorkspacePath:       "/workspace/root",
		AgentProfileName:    "codex-cli",
		EnvironmentConfigs:  []string{},
		SetupCommands:       []string{},
		DefaultRuntime:      RuntimeVM,
		DefaultProvider:     ProviderLima,
		DefaultLimaTemplate: "template:default",
		CreatedAt:           time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}

	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject() error = %v", err)
	}

	node := Node{
		ID:                    newID(),
		Slug:                  "root-node",
		ProjectID:             project.ID,
		Runtime:               RuntimeVM,
		Provider:              ProviderLima,
		LimaInstanceName:      "root-root-node-12345678",
		Status:                NodeStatusCreated,
		AgentProfileName:      "codex-cli",
		BootstrapCommands:     []string{},
		GeneratedTemplatePath: store.nodeTemplatePath("unused"),
		CreatedAt:             time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
	if err := store.SaveNode(node, BootstrapState{}, nil); err != nil {
		t.Fatalf("SaveNode() error = %v", err)
	}

	data, err := os.ReadFile(store.nodePath(node.ID))
	if err != nil {
		t.Fatalf("ReadFile(node.yaml) error = %v", err)
	}

	output := string(data)
	if !strings.Contains(output, nodeLimaCommandsTemplateComment) {
		t.Fatalf("expected node metadata to include the node override template comment, got %s", output)
	}
	if !strings.Contains(output, "\n# lima_commands:\n") {
		t.Fatalf("expected node metadata to include a commented lima_commands block, got %s", output)
	}
	if !strings.Contains(output, "#     create: '{{binary}} create -y --name {{instance_name}} --cpus=2 --memory=4 --disk=20 {{template_path}}'") {
		t.Fatalf("expected node metadata to include the effective create command example, got %s", output)
	}
	if !strings.Contains(output, "#     workspace_seed_prepare: sudo rm -rf {{target_path}} && sudo mkdir -p {{target_parent}} && sudo chown \"$(id -un)\":\"$(id -gn)\" {{target_parent}}") {
		t.Fatalf("expected node metadata to include the workspace seed prepare example, got %s", output)
	}
}
