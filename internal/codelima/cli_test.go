package codelima

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteSuccessRendersProjectListAsTable(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	writeSuccess(&stdout, false, []Project{
		{
			ID:               "project-uuid",
			Slug:             "root",
			WorkspacePath:    "/workspace/root",
			DefaultRuntime:   RuntimeVM,
			AgentProfileName: "codex-cli",
		},
	})

	output := stdout.String()
	for _, expected := range []string{
		"slug",
		"uuid",
		"workspace_path",
		"runtime",
		"agent",
		"root",
		"project-uuid",
		"/workspace/root",
		RuntimeVM,
		"codex-cli",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, output)
		}
	}

	if strings.Contains(output, "- slug:") {
		t.Fatalf("expected table output instead of YAML, got %q", output)
	}
}

func TestWriteSuccessRendersNodeListAsTableUsingGuestWorkspacePath(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	writeSuccess(&stdout, false, []Node{
		{
			ID:                 "node-uuid",
			Slug:               "design",
			GuestWorkspacePath: "/guest/workspace",
			Runtime:            RuntimeVM,
			AgentProfileName:   "codex-cli",
			Status:             NodeStatusRunning,
			LastRuntimeObservation: &RuntimeObservation{
				Exists: true,
				Status: "running",
			},
		},
	})

	output := stdout.String()
	for _, expected := range []string{
		"slug",
		"uuid",
		"workspace_mode",
		"workspace_path",
		"runtime",
		"vm_status",
		"agent",
		"design",
		"node-uuid",
		WorkspaceModeCopy,
		"/guest/workspace",
		RuntimeVM,
		"running",
		"codex-cli",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, output)
		}
	}

	if strings.Contains(output, "- slug:") {
		t.Fatalf("expected table output instead of YAML, got %q", output)
	}
}

func TestWriteSuccessRendersNodeListAsTableUsingMountWorkspaceFallback(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	writeSuccess(&stdout, false, []Node{
		{
			ID:                 "node-uuid",
			Slug:               "design",
			WorkspaceMode:      WorkspaceModeMounted,
			WorkspaceMountPath: "/host/workspace",
			Runtime:            RuntimeVM,
			Status:             NodeStatusCreated,
			AgentProfileName:   "codex-cli",
		},
	})

	output := stdout.String()
	if !strings.Contains(output, "/host/workspace") {
		t.Fatalf("expected output to fall back to workspace mount path, got %q", output)
	}
	if !strings.Contains(output, WorkspaceModeMounted) {
		t.Fatalf("expected output to include mounted workspace mode, got %q", output)
	}

	if !strings.Contains(output, NodeStatusCreated) {
		t.Fatalf("expected output to fall back to node status, got %q", output)
	}
}

func TestRunHelpPrintsUsageAndExitsSuccess(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"--help"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != ExitSuccess {
		t.Fatalf("expected ExitSuccess, got %d", exitCode)
	}

	output := stdout.String()
	if !strings.Contains(output, "Usage:\n  codelima [--home PATH] [--json] [--log-level LEVEL]") {
		t.Fatalf("expected help output to include usage header, got %q", output)
	}
	if !strings.Contains(output, "environment create|list|show|update|delete") {
		t.Fatalf("expected help output to include the environment group, got %q", output)
	}
	if !strings.Contains(output, "node create|list|cleanup-incomplete|show|start|stop|clone|sync|delete|status|logs|shell") {
		t.Fatalf("expected help output to include the incomplete-node cleanup command, got %q", output)
	}
	if strings.Contains(output, "patch propose|list|show|approve|apply|reject") {
		t.Fatalf("expected help output to omit patch commands, got %q", output)
	}
	if !strings.Contains(output, "Running with no command opens the TUI.") {
		t.Fatalf("expected help output to describe the default TUI launch, got %q", output)
	}
	if strings.Contains(output, "\n  tui\n") {
		t.Fatalf("expected help output to omit the removed tui command, got %q", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestWriteSuccessRendersIncompleteNodeCleanupResultAsTable(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	writeSuccess(&stdout, false, IncompleteNodeCleanupResult{
		DryRun: true,
		Items: []IncompleteNodeMetadata{
			{NodeID: "partial-node", InstanceName: "root-design-12345678"},
		},
	})

	output := stdout.String()
	for _, expected := range []string{
		"node_dir",
		"instance_name",
		"action",
		"partial-node",
		"root-design-12345678",
		"would_remove",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, output)
		}
	}
}

func TestDispatchNodeCleanupIncompleteParsesApplyFlag(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)

	partialDir := filepath.Join(service.cfg.MetadataRoot, "nodes", "partial-node")
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(partial node) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(partialDir, "instance.lima.yaml"), []byte("arch: aarch64\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(template) error = %v", err)
	}

	result, err := dispatch(ctx, service, []string{"node", "cleanup-incomplete"})
	if err != nil {
		t.Fatalf("dispatch(node cleanup-incomplete) error = %v", err)
	}
	cleanupResult, ok := result.(IncompleteNodeCleanupResult)
	if !ok {
		t.Fatalf("expected IncompleteNodeCleanupResult, got %T", result)
	}
	if !cleanupResult.DryRun {
		t.Fatalf("expected dry-run dispatch result")
	}
	if !exists(partialDir) {
		t.Fatalf("expected dry-run dispatch to leave partial directory in place")
	}

	result, err = dispatch(ctx, service, []string{"node", "cleanup-incomplete", "--apply"})
	if err != nil {
		t.Fatalf("dispatch(node cleanup-incomplete --apply) error = %v", err)
	}
	cleanupResult, ok = result.(IncompleteNodeCleanupResult)
	if !ok {
		t.Fatalf("expected IncompleteNodeCleanupResult, got %T", result)
	}
	if cleanupResult.DryRun {
		t.Fatalf("expected apply dispatch result")
	}
	if exists(partialDir) {
		t.Fatalf("expected apply dispatch to remove partial directory")
	}
}

func TestDispatchNodeCreateParsesWorkspaceMode(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	value, err := dispatchNode(ctx, service, []string{
		"create",
		"--project", project.ID,
		"--slug", "mounted-node",
		"--workspace-mode", "mounted",
	})
	if err != nil {
		t.Fatalf("dispatchNode(create) error = %v", err)
	}

	node, ok := value.(Node)
	if !ok {
		t.Fatalf("expected Node result, got %T", value)
	}
	if got := nodeWorkspaceMode(node); got != WorkspaceModeMounted {
		t.Fatalf("expected mounted workspace mode, got %q", got)
	}
}

func TestDispatchNodeSyncParsesApplyFlag(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	nodeValue, err := dispatchNode(ctx, service, []string{
		"create",
		"--project", project.ID,
		"--slug", "root-node",
	})
	if err != nil {
		t.Fatalf("dispatchNode(create) error = %v", err)
	}
	node := nodeValue.(Node)

	startedValue, err := dispatchNode(ctx, service, []string{
		"start",
		node.ID,
	})
	if err != nil {
		t.Fatalf("dispatchNode(start) error = %v", err)
	}
	node = startedValue.(Node)

	fake := service.lima.(*fakeLima)
	guestRoot := fake.guestRoots[node.LimaInstanceName]
	writeFile(t, filepath.Join(guestRoot, "README.md"), "hello\nsynced\n")

	value, err := dispatchNode(ctx, service, []string{
		"sync",
		node.ID,
		"--apply",
	})
	if err != nil {
		t.Fatalf("dispatchNode(sync --apply) error = %v", err)
	}

	result, ok := value.(NodeSyncResult)
	if !ok {
		t.Fatalf("expected NodeSyncResult, got %T", value)
	}
	if result.DryRun || !result.Applied {
		t.Fatalf("expected applied sync result, got %#v", result)
	}
}

func TestDispatchNodeCreateLoadsLimaCommandsFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	commandsPath := filepath.Join(t.TempDir(), "node-create-lima.yaml")
	if err := os.WriteFile(commandsPath, []byte("start: \"{{binary}} start {{instance_name}} --tty=false\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(lima commands) error = %v", err)
	}

	value, err := dispatchNode(ctx, service, []string{
		"create",
		"--project", project.ID,
		"--slug", "configured-node",
		"--lima-commands-file", commandsPath,
	})
	if err != nil {
		t.Fatalf("dispatchNode(create with lima commands file) error = %v", err)
	}

	node, ok := value.(Node)
	if !ok {
		t.Fatalf("expected Node result, got %T", value)
	}
	if got := strings.Join(node.LimaCommands.Start, "|"); got != "{{binary}} start {{instance_name}} --tty=false" {
		t.Fatalf("expected node create to load start override, got %q", got)
	}
}

func TestDispatchNodeCloneLoadsLimaCommandsFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	sourceNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "source-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate(source-node) error = %v", err)
	}

	commandsPath := filepath.Join(t.TempDir(), "node-clone-lima.yaml")
	if err := os.WriteFile(commandsPath, []byte("clone: \"{{binary}} clone {{source_instance}} {{target_instance}} --tty=false\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(lima commands) error = %v", err)
	}

	value, err := dispatchNode(ctx, service, []string{
		"clone",
		sourceNode.ID,
		"--node-slug", "cloned-node",
		"--lima-commands-file", commandsPath,
	})
	if err != nil {
		t.Fatalf("dispatchNode(clone with lima commands file) error = %v", err)
	}

	node, ok := value.(Node)
	if !ok {
		t.Fatalf("expected Node result, got %T", value)
	}
	if got := strings.Join(node.LimaCommands.Clone, "|"); got != "{{binary}} clone {{source_instance}} {{target_instance}} --tty=false" {
		t.Fatalf("expected node clone to load clone override, got %q", got)
	}
}

func TestDispatchPatchCommandGroupIsRejected(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)

	if _, err := dispatch(ctx, service, []string{"patch", "list"}); err == nil {
		t.Fatalf("expected dispatch(patch list) to fail")
	}
}
