package codelima

import (
	"bytes"
	"context"
	"flag"
	"os"
	"strings"
	"testing"
)

func TestHelpAdvertisesConfigurationModelWithoutProjects(t *testing.T) {
	text := usage()
	for _, want := range []string{"settings show", "environment create|list|show|update|delete", "configuration create|list|show|update|delete|clone", "nodes in that directory and its descendants"} {
		if !strings.Contains(text, want) {
			t.Fatalf("usage missing %q:\n%s", want, text)
		}
	}
	for _, removed := range []string{"project create", "project tree", "config show", "environment config"} {
		if strings.Contains(text, removed) {
			t.Fatalf("usage still exposes %q:\n%s", removed, text)
		}
	}
}

func TestEnvironmentHelpUsesEnvironmentTerminology(t *testing.T) {
	group := findCLIGroup("environment")
	if group == nil {
		t.Fatal("environment command group not found")
	}

	texts := []string{groupHelp(group)}
	for _, command := range commandsForGroup(group.Name) {
		texts = append(texts, commandHelp(command))
	}
	for _, text := range texts {
		if strings.Contains(strings.ToLower(text), "environment config") {
			t.Fatalf("environment help uses legacy config terminology:\n%s", text)
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

func TestDispatchKeepsMissingAndUnknownCommandMessages(t *testing.T) {
	service, _ := newTestService(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"node"}, "missing node command"},
		{[]string{"node", "bogus"}, "unknown node command"},
		{[]string{"environment"}, "missing environment command"},
		{[]string{"environment", "bogus"}, "unknown environment command"},
		{[]string{"configuration"}, "missing configuration command"},
		{[]string{"configuration", "bogus"}, "unknown configuration command"},
		{[]string{"daemon"}, "missing daemon command"},
		{[]string{"daemon", "bogus"}, "unknown daemon command"},
		{[]string{"terminal"}, "missing terminal command"},
		{[]string{"terminal", "bogus"}, "unknown terminal command"},
		{[]string{"settings", "bogus"}, "unknown settings command"},
		{[]string{"shell"}, "shell requires <node>"},
		{[]string{"node", "shell"}, "node shell requires <node>"},
		{[]string{"node", "show"}, "node show requires <node>"},
		{[]string{"terminal", "open"}, "terminal open requires <target>"},
	}
	for _, tc := range cases {
		if _, err := dispatch(context.Background(), service, tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("dispatch(%v) error = %v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestRunGroupHelpPrintsCommandTable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"--home", t.TempDir(), "node", "--help"}, strings.NewReader(""), &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("Run(node --help) code = %d, stderr=%s", code, stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{"codelima node <command>", "Commands:", "cleanup-incomplete", "clone a node"} {
		if !strings.Contains(text, want) {
			t.Fatalf("group help missing %q:\n%s", want, text)
		}
	}
}

func TestRunCommandHelpPrintsFlagUsage(t *testing.T) {
	for _, helpFlag := range []string{"--help", "-h"} {
		var stdout, stderr bytes.Buffer
		if code := Run(context.Background(), []string{"--home", t.TempDir(), "node", "create", helpFlag}, strings.NewReader(""), &stdout, &stderr); code != ExitSuccess {
			t.Fatalf("Run(node create %s) code = %d, stderr=%s", helpFlag, code, stderr.String())
		}
		text := stdout.String()
		for _, want := range []string{"codelima node create [flags]", "-slug", "unique lowercase identifier for the new node"} {
			if !strings.Contains(text, want) {
				t.Fatalf("command help missing %q:\n%s", want, text)
			}
		}
	}
}

func TestCommandTableCoversEveryGroupAndHelp(t *testing.T) {
	for i := range cliGroups {
		group := &cliGroups[i]
		commands := commandsForGroup(group.Name)
		if len(commands) == 0 {
			t.Fatalf("group %q has no commands", group.Name)
		}
		if text := groupHelp(group); !strings.Contains(text, "Usage:") {
			t.Fatalf("group %q help = %q", group.Name, text)
		}
		for _, cmd := range commands {
			if cmd.Summary == "" {
				t.Fatalf("command %q has no summary", cmd.fullName())
			}
			newCommandFlagSet(cmd).VisitAll(func(f *flag.Flag) {
				if f.Usage == "" {
					t.Fatalf("flag --%s on %q has no usage string", f.Name, cmd.fullName())
				}
			})
		}
	}
	for _, cmd := range cliCommands {
		if findCLIGroup(cmd.Group) == nil {
			t.Fatalf("command %q references unknown group", cmd.fullName())
		}
	}
}

func TestConfigurationCLIUsesHumanResourceSizes(t *testing.T) {
	service, _ := newTestService(t)
	createdAny, err := dispatchConfiguration(context.Background(), service, []string{"create", "--slug", "custom-size", "--vcpus", "8", "--memory", "8GiB", "--disk", "40GiB", "--environment", "codex"})
	if err != nil {
		t.Fatalf("configuration create error = %v", err)
	}
	created := createdAny.(Configuration)
	if created.VCPUs != 8 || created.MemoryMiB != 8192 || created.DiskMiB != 40960 || len(created.Environments) != 1 || created.Environments[0] != "codex" {
		t.Fatalf("unexpected configuration: %+v", created)
	}
	clonedAny, err := dispatchConfiguration(context.Background(), service, []string{"clone", "custom-size", "--slug", "custom-size-copy"})
	if err != nil {
		t.Fatalf("configuration clone error = %v", err)
	}
	cloned := clonedAny.(Configuration)
	if cloned.ID == created.ID || cloned.MemoryMiB != created.MemoryMiB {
		t.Fatalf("clone did not copy configuration: %+v", cloned)
	}
	// The positional target may also trail the flags (fs.Arg(0) fallback).
	updatedAny, err := dispatchConfiguration(context.Background(), service, []string{"update", "--vcpus", "4", "custom-size"})
	if err != nil {
		t.Fatalf("configuration update error = %v", err)
	}
	if updated := updatedAny.(Configuration); updated.VCPUs != 4 || updated.Slug != "custom-size" || len(updated.Environments) != 1 || updated.Environments[0] != "codex" {
		t.Fatalf("trailing positional update failed: %+v", updated)
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
	if created.VCPUs != 1 || created.MemoryMiB != 1024 || created.DiskMiB != 10240 {
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
