package codelima

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeLima struct {
	baseTemplate []byte
	observations map[string]RuntimeObservation
	calls        []string
	shellCalls   []fakeShellCall
	failCommand  string
}

type fakeShellCall struct {
	instanceName string
	command      []string
	workdir      string
	interactive  bool
}

func newFakeLima() *fakeLima {
	return &fakeLima{
		baseTemplate: []byte("arch: aarch64\nimages: []\nmounts: []\n"),
		observations: map[string]RuntimeObservation{},
		calls:        []string{},
		shellCalls:   []fakeShellCall{},
	}
}

func (f *fakeLima) BaseTemplate(_ context.Context, _ string) ([]byte, error) {
	f.calls = append(f.calls, "template")
	return append([]byte(nil), f.baseTemplate...), nil
}

func (f *fakeLima) List(_ context.Context) ([]RuntimeObservation, error) {
	observations := make([]RuntimeObservation, 0, len(f.observations))
	for _, observation := range f.observations {
		observations = append(observations, observation)
	}
	return observations, nil
}

func (f *fakeLima) Create(_ context.Context, instanceName, _ string) error {
	f.calls = append(f.calls, "create:"+instanceName)
	f.observations[instanceName] = RuntimeObservation{Name: instanceName, Exists: true, Status: "stopped", Dir: "/fake/" + instanceName}
	return nil
}

func (f *fakeLima) Start(_ context.Context, instanceName string) error {
	f.calls = append(f.calls, "start:"+instanceName)
	observation := f.observations[instanceName]
	observation.Status = "running"
	observation.Exists = true
	observation.Name = instanceName
	f.observations[instanceName] = observation
	return nil
}

func (f *fakeLima) Stop(_ context.Context, instanceName string) error {
	f.calls = append(f.calls, "stop:"+instanceName)
	observation := f.observations[instanceName]
	observation.Status = "stopped"
	observation.Exists = true
	observation.Name = instanceName
	f.observations[instanceName] = observation
	return nil
}

func (f *fakeLima) Delete(_ context.Context, instanceName string) error {
	f.calls = append(f.calls, "delete:"+instanceName)
	delete(f.observations, instanceName)
	return nil
}

func (f *fakeLima) Clone(_ context.Context, sourceInstance, targetInstance string, options CloneOptions) error {
	f.calls = append(f.calls, "clone:"+sourceInstance+"->"+targetInstance+":"+options.MountPath)
	f.observations[targetInstance] = RuntimeObservation{Name: targetInstance, Exists: true, Status: "stopped", Dir: "/fake/" + targetInstance}
	return nil
}

func (f *fakeLima) Shell(_ context.Context, instanceName string, command []string, workdir string, interactive bool, _ ShellStreams) error {
	f.calls = append(f.calls, "shell:"+instanceName+":"+strings.Join(command, " "))
	f.shellCalls = append(f.shellCalls, fakeShellCall{
		instanceName: instanceName,
		command:      append([]string(nil), command...),
		workdir:      workdir,
		interactive:  interactive,
	})
	if f.failCommand != "" && strings.Contains(strings.Join(command, " "), f.failCommand) {
		return errors.New("forced shell failure")
	}
	return nil
}

func TestProjectCreateAndForkCapturesSnapshots(t *testing.T) {
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

	if !exists(service.store.projectPath(project.ID)) {
		t.Fatalf("expected project metadata to be written")
	}

	initialSnapshot, err := service.store.LatestSnapshot(project.ID)
	if err != nil {
		t.Fatalf("LatestSnapshot() error = %v", err)
	}

	if initialSnapshot.Kind != "initial" {
		t.Fatalf("expected initial snapshot, got %q", initialSnapshot.Kind)
	}

	childWorkspace := filepath.Join(t.TempDir(), "child")
	child, err := service.ProjectFork(ctx, ProjectForkInput{
		SourceProject: project.ID,
		Slug:          "child",
		WorkspacePath: childWorkspace,
	})
	if err != nil {
		t.Fatalf("ProjectFork() error = %v", err)
	}

	if child.ParentProjectID != project.ID {
		t.Fatalf("expected child parent project to be %q, got %q", project.ID, child.ParentProjectID)
	}

	content, err := os.ReadFile(filepath.Join(childWorkspace, "README.md"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if string(content) != "hello\n" {
		t.Fatalf("expected forked workspace content to match source, got %q", string(content))
	}
}

func TestNodeLifecycleDelegatesToLima(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")
	writeExecutable(t, filepath.Join(workspace, "script", "setup"), "#!/usr/bin/env sh\necho setup\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
		SetupCommands: []string{"./script/setup"},
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "root-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	if !containsPrefix(service.lima.(*fakeLima).calls, "create:"+node.LimaInstanceName) {
		t.Fatalf("expected limactl create delegation")
	}

	node, err = service.NodeStart(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	if node.Status != NodeStatusRunning {
		t.Fatalf("expected running status, got %q", node.Status)
	}

	if !containsCall(service.lima.(*fakeLima).calls, "shell:"+node.LimaInstanceName+":sh -lc cd "+quoted(workspace)+" && ./script/setup") {
		t.Fatalf("expected setup command delegation, calls = %v", service.lima.(*fakeLima).calls)
	}

	if !containsSubstring(service.lima.(*fakeLima).calls, "command -v sh") {
		t.Fatalf("expected validation command to run, calls = %v", service.lima.(*fakeLima).calls)
	}

	node, err = service.NodeStop(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeStop() error = %v", err)
	}

	if node.Status != NodeStatusStopped {
		t.Fatalf("expected stopped status, got %q", node.Status)
	}

	node, err = service.NodeDelete(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeDelete() error = %v", err)
	}

	if node.Status != NodeStatusTerminated {
		t.Fatalf("expected terminated status, got %q", node.Status)
	}
}

func TestNodeCloneCreatesChildProjectAndNode(t *testing.T) {
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

	node, err := service.NodeCreate(ctx, NodeCreateInput{Project: project.ID, Slug: "root-node"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	childWorkspace := filepath.Join(t.TempDir(), "child")
	childProject, childNode, err := service.NodeClone(ctx, NodeCloneInput{
		SourceNode:    node.ID,
		ProjectSlug:   "child",
		NodeSlug:      "child-node",
		WorkspacePath: childWorkspace,
	})
	if err != nil {
		t.Fatalf("NodeClone() error = %v", err)
	}

	if childProject.ParentProjectID != project.ID {
		t.Fatalf("expected child project parent id %q, got %q", project.ID, childProject.ParentProjectID)
	}

	if childNode.ParentNodeID != node.ID {
		t.Fatalf("expected child node parent id %q, got %q", node.ID, childNode.ParentNodeID)
	}

	if !containsPrefix(service.lima.(*fakeLima).calls, "clone:"+node.LimaInstanceName+"->"+childNode.LimaInstanceName) {
		t.Fatalf("expected limactl clone delegation, calls = %v", service.lima.(*fakeLima).calls)
	}
}

func TestPatchFlowApproveAndApply(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "file.txt"), "hello\n")

	parent, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "parent",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	childWorkspace := filepath.Join(t.TempDir(), "child")
	child, err := service.ProjectFork(ctx, ProjectForkInput{
		SourceProject: parent.ID,
		Slug:          "child",
		WorkspacePath: childWorkspace,
	})
	if err != nil {
		t.Fatalf("ProjectFork() error = %v", err)
	}

	writeFile(t, filepath.Join(childWorkspace, "file.txt"), "hello\nworld\n")

	proposal, err := service.PatchPropose(ctx, PatchProposeInput{
		SourceProject: child.ID,
		TargetProject: parent.ID,
	})
	if err != nil {
		t.Fatalf("PatchPropose() error = %v", err)
	}

	if proposal.Status != PatchStatusSubmitted {
		t.Fatalf("expected submitted status, got %q", proposal.Status)
	}

	proposal, err = service.PatchApprove(proposal.ID, "tester", "")
	if err != nil {
		t.Fatalf("PatchApprove() error = %v", err)
	}

	if proposal.Status != PatchStatusApproved {
		t.Fatalf("expected approved status, got %q", proposal.Status)
	}

	proposal, err = service.PatchApply(ctx, proposal.ID)
	if err != nil {
		t.Fatalf("PatchApply() error = %v", err)
	}

	if proposal.Status != PatchStatusApplied {
		t.Fatalf("expected applied status, got %q", proposal.Status)
	}

	content, err := os.ReadFile(filepath.Join(workspace, "file.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if string(content) != "hello\nworld\n" {
		t.Fatalf("expected patched parent content, got %q", string(content))
	}
}

func TestPatchApplyConflictDoesNotMutateTarget(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "file.txt"), "hello\n")

	parent, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "parent",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	childWorkspace := filepath.Join(t.TempDir(), "child")
	child, err := service.ProjectFork(ctx, ProjectForkInput{
		SourceProject: parent.ID,
		Slug:          "child",
		WorkspacePath: childWorkspace,
	})
	if err != nil {
		t.Fatalf("ProjectFork() error = %v", err)
	}

	writeFile(t, filepath.Join(workspace, "file.txt"), "parent-change\n")
	writeFile(t, filepath.Join(childWorkspace, "file.txt"), "child-change\n")

	proposal, err := service.PatchPropose(ctx, PatchProposeInput{
		SourceProject: child.ID,
		TargetProject: parent.ID,
	})
	if err != nil {
		t.Fatalf("PatchPropose() error = %v", err)
	}

	if _, err := service.PatchApprove(proposal.ID, "tester", ""); err != nil {
		t.Fatalf("PatchApprove() error = %v", err)
	}

	_, err = service.PatchApply(ctx, proposal.ID)
	if err == nil {
		t.Fatalf("expected PatchApply() conflict error")
	}

	content, readErr := os.ReadFile(filepath.Join(workspace, "file.txt"))
	if readErr != nil {
		t.Fatalf("ReadFile() error = %v", readErr)
	}

	if string(content) != "parent-change\n" {
		t.Fatalf("expected target workspace to remain unchanged, got %q", string(content))
	}
}

func TestDispatchShellAliasDelegatesToNodeShell(t *testing.T) {
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

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "root-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	if _, err := dispatch(ctx, service, []string{"shell", node.ID, "--", "uname", "-a"}); err != nil {
		t.Fatalf("dispatch(shell) error = %v", err)
	}

	if !containsCall(service.lima.(*fakeLima).calls, "shell:"+node.LimaInstanceName+":uname -a") {
		t.Fatalf("expected shell alias delegation, calls = %v", service.lima.(*fakeLima).calls)
	}

	shellCalls := service.lima.(*fakeLima).shellCalls
	if len(shellCalls) == 0 {
		t.Fatalf("expected shell call to be recorded")
	}

	lastCall := shellCalls[len(shellCalls)-1]
	if strings.Join(lastCall.command, " ") != "uname -a" {
		t.Fatalf("expected shell command to strip leading --, got %q", strings.Join(lastCall.command, " "))
	}

	if lastCall.workdir != workspace {
		t.Fatalf("expected shell workdir %q, got %q", workspace, lastCall.workdir)
	}

	if lastCall.interactive {
		t.Fatalf("expected non-interactive shell call")
	}
}

func TestShellUsesWorkspaceMountPathForInteractiveEntry(t *testing.T) {
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

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "root-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	if err := service.Shell(ctx, node.ID, nil); err != nil {
		t.Fatalf("Shell() error = %v", err)
	}

	shellCalls := service.lima.(*fakeLima).shellCalls
	if len(shellCalls) == 0 {
		t.Fatalf("expected shell call to be recorded")
	}

	lastCall := shellCalls[len(shellCalls)-1]
	if lastCall.workdir != workspace {
		t.Fatalf("expected interactive shell workdir %q, got %q", workspace, lastCall.workdir)
	}

	if !lastCall.interactive {
		t.Fatalf("expected interactive shell call")
	}

	if len(lastCall.command) != 0 {
		t.Fatalf("expected interactive shell without command, got %v", lastCall.command)
	}
}

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()

	home := filepath.Join(t.TempDir(), ".codelima")
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	cfg := DefaultConfig(home)
	cfg.MetadataRoot = home
	cfg.AgentProfilesDir = filepath.Join(home, "_config", "agent-profiles")
	service := NewService(cfg, newFakeLima(), strings.NewReader(""), ioDiscard{}, ioDiscard{})
	return service, workspace
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func writeExecutable(t *testing.T, path string, content string) {
	t.Helper()
	writeFile(t, path, content)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
}

func containsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func containsSubstring(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func containsCall(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func quoted(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
