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
		"workspace_path",
		"runtime",
		"vm_status",
		"agent",
		"design",
		"node-uuid",
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
	if !strings.Contains(output, "node create|list|cleanup-incomplete|show|start|stop|clone|delete|status|logs|shell") {
		t.Fatalf("expected help output to include the incomplete-node cleanup command, got %q", output)
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
