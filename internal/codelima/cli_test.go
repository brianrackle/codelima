package codelima

import (
	"bytes"
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
