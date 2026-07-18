package codelima

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestHelpAdvertisesConfigurationModelWithoutProjects(t *testing.T) {
	text := usage()
	for _, want := range []string{"settings show", "configuration create|list|show|update|delete|clone", "nodes in that directory and its descendants"} {
		if !strings.Contains(text, want) {
			t.Fatalf("usage missing %q:\n%s", want, text)
		}
	}
	for _, removed := range []string{"project create", "project tree", "config show"} {
		if strings.Contains(text, removed) {
			t.Fatalf("usage still exposes %q:\n%s", removed, text)
		}
	}
}

func TestRunHelpDoesNotInitializeHome(t *testing.T) {
	home := t.TempDir() + "/unused"
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"--home", home, "--help"}, strings.NewReader(""), &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("Run() code = %d, stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("help initialized CODELIMA_HOME")
	}
}

func TestDispatchRejectsRemovedProjectAndConfigGroups(t *testing.T) {
	service, _ := newTestService(t)
	for _, args := range [][]string{{"project", "list"}, {"config", "show"}} {
		if _, err := dispatch(context.Background(), service, args); err == nil || !strings.Contains(err.Error(), "unknown command group") {
			t.Fatalf("dispatch(%v) error = %v", args, err)
		}
	}
}

func TestConfigurationCLIUsesHumanResourceSizes(t *testing.T) {
	service, _ := newTestService(t)
	createdAny, err := dispatchConfiguration(service, []string{"create", "--slug", "large", "--vcpus", "8", "--memory", "8GiB", "--disk", "40GiB", "--environment", "codex"})
	if err != nil {
		t.Fatalf("configuration create error = %v", err)
	}
	created := createdAny.(Configuration)
	if created.VCPUs != 8 || created.MemoryMiB != 8192 || created.DiskMiB != 40960 || len(created.Environments) != 1 || created.Environments[0] != "codex" {
		t.Fatalf("unexpected configuration: %+v", created)
	}
	clonedAny, err := dispatchConfiguration(service, []string{"clone", "large", "--slug", "large-copy"})
	if err != nil {
		t.Fatalf("configuration clone error = %v", err)
	}
	cloned := clonedAny.(Configuration)
	if cloned.ID == created.ID || cloned.MemoryMiB != created.MemoryMiB {
		t.Fatalf("clone did not copy configuration: %+v", cloned)
	}
}

func TestNodeCreateCLIDefaultsDirectoryConfigurationAndMountedWorkspace(t *testing.T) {
	service, _ := newTestService(t)
	workspace, err := canonicalPath(".")
	if err != nil {
		t.Fatal(err)
	}
	createdAny, err := dispatchNode(context.Background(), service, []string{"create", "--slug", "worker"})
	if err != nil {
		t.Fatalf("node create error = %v", err)
	}
	created := createdAny.(Node)
	if created.DirectoryPath != workspace || created.ConfigurationSlug != DefaultConfigurationSlug || created.ConfigurationID == "" {
		t.Fatalf("node defaults not resolved: %+v", created)
	}
	if created.VCPUs != 2 || created.MemoryMiB != 4096 || created.DiskMiB != 20480 {
		t.Fatalf("default resources not frozen: %+v", created)
	}
	if created.WorkspaceMode != WorkspaceModeMounted || created.WorkspaceMountPath != workspace {
		t.Fatalf("default mounted workspace not resolved: %+v", created)
	}

	copyAny, err := dispatchNode(context.Background(), service, []string{"create", "--slug", "worker-copy", "--workspace-mode", WorkspaceModeCopy})
	if err != nil {
		t.Fatalf("copy-mode node create error = %v", err)
	}
	copyNode := copyAny.(Node)
	if copyNode.WorkspaceMode != WorkspaceModeCopy || copyNode.WorkspaceMountPath != "" {
		t.Fatalf("explicit copy workspace not preserved: %+v", copyNode)
	}
}

func TestNodeCLIRequiresExplicitSlugs(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := dispatchNode(context.Background(), service, []string{"create"}); err == nil || !strings.Contains(err.Error(), "--slug") {
		t.Fatalf("node create error = %v", err)
	}
	if _, err := dispatchNode(context.Background(), service, []string{"clone", "source"}); err == nil || !strings.Contains(err.Error(), "--slug") {
		t.Fatalf("node clone error = %v", err)
	}
}

func TestWriteSuccessRendersConfigurationAndDirectoryBoundNodes(t *testing.T) {
	var output bytes.Buffer
	writeSuccess(&output, false, []Configuration{{ID: "config-id", Slug: "default", Image: "image", AgentProfileName: "codex-cli", VCPUs: 2, MemoryMiB: 4096, DiskMiB: 20480}})
	if text := output.String(); !strings.Contains(text, "memory_mib") || !strings.Contains(text, "20480") {
		t.Fatalf("configuration table = %q", text)
	}
	output.Reset()
	writeSuccess(&output, false, []Node{{ID: "node-id", Slug: "worker", ConfigurationSlug: "default", DirectoryPath: "/work/repo", Runtime: RuntimeVM, Status: NodeStatusCreated, AgentProfileName: "codex-cli"}})
	if text := output.String(); !strings.Contains(text, "configuration") || !strings.Contains(text, "/work/repo") || strings.Contains(text, "project") {
		t.Fatalf("node table = %q", text)
	}
	output.Reset()
	writeSuccess(&output, false, Node{ID: "node-id", Slug: "worker", ConfigurationID: "config-id", ConfigurationSlug: "default", DirectoryPath: "/work/repo"})
	if text := output.String(); !strings.Contains(text, "configuration_slug: default") {
		t.Fatalf("node record = %q", text)
	}
}
