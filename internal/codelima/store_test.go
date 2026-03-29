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
	cfg.LimaCommands.Start = []string{"{{binary}} start {{instance_name}} --vm-type=vz"}
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
	if !strings.Contains(output, "#     workspace_seed_prepare:") || !strings.Contains(output, `sudo rm -rf {{target_path}} && sudo mkdir -p {{target_parent}} && sudo chown "$(id -un)":"$(id -gn)" {{target_parent}}`) {
		t.Fatalf("expected project metadata to include the default workspace seed prepare command example, got %s", output)
	}
	if !strings.Contains(output, "#     start:") || !strings.Contains(output, "{{binary}} start {{instance_name}} --vm-type=vz") {
		t.Fatalf("expected project metadata to include the global default start command example, got %s", output)
	}
	if !strings.Contains(output, "#     bootstrap: []") {
		t.Fatalf("expected project metadata to include the bootstrap command example, got %s", output)
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
	if !strings.Contains(output, "#     create:") || !strings.Contains(output, "{{binary}} create -y --name {{instance_name}} --cpus=2 --memory=4 --disk=20 {{template_path}}") {
		t.Fatalf("expected node metadata to include the effective create command example, got %s", output)
	}
	if !strings.Contains(output, "#     workspace_seed_prepare:") || !strings.Contains(output, `sudo rm -rf {{target_path}} && sudo mkdir -p {{target_parent}} && sudo chown "$(id -un)":"$(id -gn)" {{target_parent}}`) {
		t.Fatalf("expected node metadata to include the workspace seed prepare example, got %s", output)
	}
}

func TestSaveNodeOmitsRuntimeStatusFromNodeMetadata(t *testing.T) {
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
		DefaultRuntime:      RuntimeVM,
		DefaultProvider:     ProviderLima,
		DefaultLimaTemplate: "template:default",
		CreatedAt:           time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject() error = %v", err)
	}

	now := time.Now().UTC()
	node := Node{
		ID:               newID(),
		Slug:             "root-node",
		ProjectID:        project.ID,
		Runtime:          RuntimeVM,
		Provider:         ProviderLima,
		LimaInstanceName: "root-root-node-12345678",
		Status:           NodeStatusRunning,
		AgentProfileName: "codex-cli",
		CreatedAt:        now,
		UpdatedAt:        now,
		LastReconciledAt: &now,
		LastRuntimeObservation: &RuntimeObservation{
			Name:   "root-root-node-12345678",
			Exists: true,
			Status: "running",
		},
	}

	if err := store.SaveNode(node, BootstrapState{}, nil); err != nil {
		t.Fatalf("SaveNode() error = %v", err)
	}

	data, err := os.ReadFile(store.nodePath(node.ID))
	if err != nil {
		t.Fatalf("ReadFile(node.yaml) error = %v", err)
	}

	output := string(data)
	for _, unexpected := range []string{
		"\nstatus:",
		"\nlast_reconciled_at:",
		"\nlast_runtime_observation:",
		"\nlifecycle_state:",
	} {
		if strings.Contains(output, unexpected) {
			t.Fatalf("expected node metadata to omit %q, got %s", unexpected, output)
		}
	}
}

func TestNodeByIDLoadsLegacyNodeStatusIntoLifecycleState(t *testing.T) {
	t.Parallel()

	home := filepath.Join(t.TempDir(), ".codelima")
	cfg := DefaultConfig(home)
	store := NewStore(cfg)
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}

	project := Project{
		ID:                  "project-1",
		Slug:                "root",
		WorkspacePath:       "/workspace/root",
		AgentProfileName:    "codex-cli",
		EnvironmentConfigs:  []string{},
		DefaultRuntime:      RuntimeVM,
		DefaultProvider:     ProviderLima,
		DefaultLimaTemplate: "template:default",
		CreatedAt:           time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject() error = %v", err)
	}

	nodeID := "node-1"
	legacyNode := `id: node-1
slug: root-node
project_id: project-1
runtime: vm
provider: lima
lima_instance_name: root-root-node-12345678
status: created
agent_profile_name: codex-cli
bootstrap_commands: []
generated_template_path: /tmp/node-1/instance.lima.yaml
workspace_seeded: false
bootstrap_completed: false
created_at: 2026-03-28T00:00:00Z
updated_at: 2026-03-28T00:00:00Z
`
	if err := os.MkdirAll(filepath.Dir(store.nodePath(nodeID)), 0o755); err != nil {
		t.Fatalf("MkdirAll(node dir) error = %v", err)
	}
	if err := os.WriteFile(store.nodePath(nodeID), []byte(legacyNode), 0o644); err != nil {
		t.Fatalf("WriteFile(node.yaml) error = %v", err)
	}

	node, err := store.NodeByID(nodeID)
	if err != nil {
		t.Fatalf("NodeByID() error = %v", err)
	}

	if node.Status != NodeStatusCreated {
		t.Fatalf("expected legacy status to load as created, got %q", node.Status)
	}
	if node.LifecycleState != NodeStatusCreated {
		t.Fatalf("expected legacy status to populate lifecycle state, got %q", node.LifecycleState)
	}
}
