package codelima

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSandbox struct {
	mu           sync.Mutex
	observations map[string]RuntimeObservation
	calls        []string
	invocations  []string
	shellCalls   []fakeShellCall
	copyCalls    []fakeCopyCall
	createErr    error
	failCommand  string
	cloneStatus  string
	listCalls    int
	listErr      error
	createGate   *fakeSandboxGate
	startGate    *fakeSandboxGate
	stopGate     *fakeSandboxGate
	deleteGate   *fakeSandboxGate
	cloneGate    *fakeSandboxGate
}

type fakeSandboxGate struct {
	entered chan string
	release chan struct{}
}

type fakeShellCall struct {
	instanceName string
	command      []string
	workdir      string
	interactive  bool
}

type fakeCopyCall struct {
	instanceName string
	sourcePath   string
	targetPath   string
	recursive    bool
}

func newFakeSandbox() *fakeSandbox {
	return &fakeSandbox{
		observations: map[string]RuntimeObservation{},
		calls:        []string{},
		invocations:  []string{},
		shellCalls:   []fakeShellCall{},
		copyCalls:    []fakeCopyCall{},
		cloneStatus:  "stopped",
	}
}

func newFakeSandboxGate() *fakeSandboxGate {
	return &fakeSandboxGate{
		entered: make(chan string, 8),
		release: make(chan struct{}),
	}
}

func (g *fakeSandboxGate) block(call string) {
	if g == nil {
		return
	}
	if g.entered != nil {
		g.entered <- call
	}
	if g.release != nil {
		<-g.release
	}
}

// recordCall appends bookkeeping entries under the fake's mutex so tests may
// exercise Service reads and mutations concurrently under -race.
func (f *fakeSandbox) recordCall(call string, invocationPrefix string, invocations []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	for _, invocation := range invocations {
		f.invocations = append(f.invocations, invocationPrefix+invocation)
	}
}

func (f *fakeSandbox) Version(context.Context) (string, error) {
	return requiredMicrosandboxVersion, nil
}

func (f *fakeSandbox) ResolveCommands(project Project, node Node, kind runtimeCommandKind, values map[string]string) ([]string, error) {
	return resolveConfiguredRuntimeCommands("msb", legacyMSBCommandTemplates(), project, node.RuntimeCommands, kind, values)
}

func (f *fakeSandbox) List(_ context.Context) ([]RuntimeObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	observations := make([]RuntimeObservation, 0, len(f.observations))
	for _, observation := range f.observations {
		observations = append(observations, observation)
	}
	return observations, nil
}

func (f *fakeSandbox) Create(_ context.Context, project Project, node Node) error {
	values, err := runtimeNodeValues(project, node)
	if err != nil {
		return err
	}
	commands, err := f.ResolveCommands(project, node, runtimeCommandCreate, values)
	f.recordCall("create:"+node.SandboxName, "create:", commands)
	if err != nil {
		return err
	}
	if f.createErr != nil {
		return f.createErr
	}
	f.createGate.block(node.SandboxName)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observations[node.SandboxName] = RuntimeObservation{Name: node.SandboxName, Exists: true, Status: "stopped", Dir: "/fake/" + node.SandboxName}
	return nil
}

func (f *fakeSandbox) Start(_ context.Context, project Project, node Node) error {
	commands, err := f.ResolveCommands(project, node, runtimeCommandStart, map[string]string{
		"sandbox_name": shellQuote(node.SandboxName),
	})
	f.recordCall("start:"+node.SandboxName, "start:", commands)
	if err != nil {
		return err
	}
	f.startGate.block(node.SandboxName)
	f.mu.Lock()
	defer f.mu.Unlock()
	observation := f.observations[node.SandboxName]
	observation.Status = "running"
	observation.Exists = true
	observation.Name = node.SandboxName
	f.observations[node.SandboxName] = observation
	return nil
}

func (f *fakeSandbox) Stop(_ context.Context, project Project, node Node) error {
	commands, err := f.ResolveCommands(project, node, runtimeCommandStop, map[string]string{
		"sandbox_name": shellQuote(node.SandboxName),
	})
	f.recordCall("stop:"+node.SandboxName, "stop:", commands)
	if err != nil {
		return err
	}
	f.stopGate.block(node.SandboxName)
	f.mu.Lock()
	defer f.mu.Unlock()
	observation := f.observations[node.SandboxName]
	observation.Status = "stopped"
	observation.Exists = true
	observation.Name = node.SandboxName
	f.observations[node.SandboxName] = observation
	return nil
}

func (f *fakeSandbox) Delete(_ context.Context, project Project, node Node) error {
	commands, err := f.ResolveCommands(project, node, runtimeCommandDelete, map[string]string{
		"sandbox_name": shellQuote(node.SandboxName),
	})
	f.recordCall("delete:"+node.SandboxName, "delete:", commands)
	if err != nil {
		return err
	}
	f.deleteGate.block(node.SandboxName)
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.observations, node.SandboxName)
	return nil
}

func (f *fakeSandbox) Clone(_ context.Context, project Project, sourceNode, targetNode Node) error {
	values, err := runtimeNodeValues(project, targetNode)
	if err != nil {
		return err
	}
	values["source_sandbox"] = shellQuote(sourceNode.SandboxName)
	values["snapshot_name"] = shellQuote(cloneSnapshotName(targetNode.SandboxName))
	commands, err := f.ResolveCommands(project, targetNode, runtimeCommandClone, values)
	f.recordCall("clone:"+sourceNode.SandboxName+"->"+targetNode.SandboxName, "clone:", commands)
	if err != nil {
		return err
	}
	f.cloneGate.block(targetNode.SandboxName)
	f.mu.Lock()
	defer f.mu.Unlock()
	status := f.cloneStatus
	if strings.TrimSpace(status) == "" {
		status = "stopped"
	}
	f.observations[targetNode.SandboxName] = RuntimeObservation{Name: targetNode.SandboxName, Exists: true, Status: status, Dir: "/fake/" + targetNode.SandboxName}
	return nil
}

func (f *fakeSandbox) CopyToGuest(_ context.Context, project Project, node Node, sourcePath, targetPath string, recursive bool) error {
	commands, err := f.ResolveCommands(project, node, runtimeCommandCopy, map[string]string{
		"source_path":  shellQuote(sourcePath),
		"target_path":  shellQuote(targetPath),
		"sandbox_name": shellQuote(node.SandboxName),
		"copy_target":  shellQuote(node.SandboxName + ":" + targetPath),
	})
	f.recordCall("copy:"+node.SandboxName+":"+sourcePath+"->"+targetPath, "copy:", commands)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.copyCalls = append(f.copyCalls, fakeCopyCall{
		instanceName: node.SandboxName,
		sourcePath:   sourcePath,
		targetPath:   targetPath,
		recursive:    recursive,
	})
	return nil
}

func (f *fakeSandbox) Shell(_ context.Context, project Project, node Node, command []string, workdir string, interactive bool, _ ShellStreams) error {
	workdirFlag := ""
	if workdir != "" {
		workdirFlag = prefixedShellFragment("--workdir", shellQuote(workdir))
	}
	kind := runtimeCommandShellExec
	values := map[string]string{
		"sandbox_name": shellQuote(node.SandboxName),
		"workdir":      shellQuote(workdir),
		"workdir_flag": workdirFlag,
		"command_args": shellCommandArgsFragment(command),
	}
	if interactive {
		kind = runtimeCommandShellLogin
		if len(command) == 0 {
			command = []string{"/bin/bash"}
		}
		values["login_command"] = shellArgsFragment(command)
	}
	resolved, err := f.ResolveCommands(project, node, kind, values)
	f.recordCall("shell:"+node.SandboxName+":"+strings.Join(command, " "), "shell:", resolved)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.shellCalls = append(f.shellCalls, fakeShellCall{
		instanceName: node.SandboxName,
		command:      append([]string(nil), command...),
		workdir:      workdir,
		interactive:  interactive,
	})
	failCommand := f.failCommand
	f.mu.Unlock()
	if failCommand != "" && strings.Contains(strings.Join(command, " "), failCommand) {
		return errors.New("forced shell failure")
	}
	return nil
}

func TestProjectCreateSkipsInitialSnapshotAndForkCapturesBaseSnapshot(t *testing.T) {
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

	if _, err := service.store.LatestSnapshot(project.ID); err == nil {
		t.Fatalf("expected project create to skip the initial snapshot")
	} else {
		var appErr *AppError
		if !As(err, &appErr) {
			t.Fatalf("expected AppError when snapshot is missing, got %T", err)
		}
		if appErr.Category != "NotFound" {
			t.Fatalf("expected NotFound when snapshot is missing, got %q", appErr.Category)
		}
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

func TestProjectAndEnvironmentConfigMetadataMutationsDoNotRequireMicrosandbox(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	fake := service.sandbox.(*fakeSandbox)
	fake.listErr = errors.New("sandbox should not be queried for metadata-only mutations")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	newWorkspace := filepath.Join(t.TempDir(), "moved-workspace")
	if err := os.MkdirAll(newWorkspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	if _, err := service.ProjectUpdate(project.ID, ProjectUpdateInput{
		WorkspacePath: &newWorkspace,
	}); err != nil {
		t.Fatalf("ProjectUpdate() error = %v", err)
	}

	config, err := service.EnvironmentConfigCreate(EnvironmentConfigCreateInput{
		Slug:              "shared-dev",
		BootstrapCommands: []string{"./script/setup"},
	})
	if err != nil {
		t.Fatalf("EnvironmentConfigCreate() error = %v", err)
	}

	if _, err := service.EnvironmentConfigUpdate(config.ID, EnvironmentConfigUpdateInput{
		BootstrapCommands: []string{"./script/setup", "direnv allow"},
	}); err != nil {
		t.Fatalf("EnvironmentConfigUpdate() error = %v", err)
	}

	if _, err := service.EnvironmentConfigDelete(config.ID); err != nil {
		t.Fatalf("EnvironmentConfigDelete() error = %v", err)
	}

	if fake.listCalls != 0 {
		t.Fatalf("expected metadata-only mutations to avoid sandbox.List, got %d calls", fake.listCalls)
	}
}

func TestNodeLifecycleCopyWorkspaceDelegatesToSandbox(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")
	writeExecutable(t, filepath.Join(workspace, "script", "setup"), "#!/usr/bin/env sh\necho setup\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:              "root",
		WorkspacePath:     workspace,
		BootstrapCommands: []string{"./script/setup"},
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Project:       project.ID,
		Slug:          "root-node",
		WorkspaceMode: WorkspaceModeCopy,
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	if node.SandboxName != "root-node" {
		t.Fatalf("expected sandbox instance name to match node slug, got %q", node.SandboxName)
	}

	if !containsPrefix(service.sandbox.(*fakeSandbox).calls, "create:"+node.SandboxName) {
		t.Fatalf("expected msb create delegation")
	}
	if !containsSubstring(service.sandbox.(*fakeSandbox).invocations, "create:'msb' create --name '"+node.SandboxName+"' --cpus 2 --memory 4G --init auto --shell /bin/bash") {
		t.Fatalf("expected create invocation to include resource flags, invocations = %v", service.sandbox.(*fakeSandbox).invocations)
	}
	if containsSubstring(service.sandbox.(*fakeSandbox).invocations, "--mount-dir") {
		t.Fatalf("expected copy-mode create to omit mount flags, invocations = %v", service.sandbox.(*fakeSandbox).invocations)
	}

	node, err = service.NodeStart(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	if node.Status != NodeStatusRunning {
		t.Fatalf("expected running status, got %q", node.Status)
	}

	if !containsCall(service.sandbox.(*fakeSandbox).calls, "shell:"+node.SandboxName+":sh -lc cd "+quoted(workspace)+" && ./script/setup") {
		t.Fatalf("expected setup command delegation, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}

	if !containsCall(service.sandbox.(*fakeSandbox).calls, "copy:"+node.SandboxName+":"+workspace+"->"+workspace) {
		t.Fatalf("expected workspace copy delegation, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}

	if !containsSubstring(service.sandbox.(*fakeSandbox).calls, "command -v sh") {
		t.Fatalf("expected validation command to run, calls = %v", service.sandbox.(*fakeSandbox).calls)
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

func TestNodeLifecycleDefaultsToMountedWorkspaceAndSkipsCopy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")
	writeExecutable(t, filepath.Join(workspace, "script", "setup"), "#!/usr/bin/env sh\necho setup\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:              "root",
		WorkspacePath:     workspace,
		BootstrapCommands: []string{"./script/setup"},
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "mounted-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	if got := nodeWorkspaceMode(node); got != WorkspaceModeMounted {
		t.Fatalf("expected mounted workspace mode, got %q", got)
	}
	if node.WorkspaceMountPath != workspace {
		t.Fatalf("expected workspace mount path %q, got %q", workspace, node.WorkspaceMountPath)
	}
	if node.GuestWorkspacePath != workspace {
		t.Fatalf("expected mounted node guest workspace path %q, got %q", workspace, node.GuestWorkspacePath)
	}
	if !containsSubstring(service.sandbox.(*fakeSandbox).invocations, "--mount-dir '"+workspace+":"+workspace+"'") {
		t.Fatalf("expected mounted create to pass a writable directory mount, invocations = %v", service.sandbox.(*fakeSandbox).invocations)
	}

	node, err = service.NodeStart(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	if !node.WorkspaceSeeded {
		t.Fatalf("expected mounted node start to mark workspace prepared")
	}
	if containsSubstring(service.sandbox.(*fakeSandbox).calls, "copy:"+node.SandboxName+":") {
		t.Fatalf("expected mounted node to skip workspace copy, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}
}

func TestNodeStartClearsCreatedLifecycleStateFromPersistedNodeMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")
	writeExecutable(t, filepath.Join(workspace, "script", "setup"), "#!/usr/bin/env sh\necho setup\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:              "root",
		WorkspacePath:     workspace,
		BootstrapCommands: []string{"./script/setup"},
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

	createdData, err := os.ReadFile(service.store.nodePath(node.ID))
	if err != nil {
		t.Fatalf("ReadFile(created node.yaml) error = %v", err)
	}
	if !strings.Contains(string(createdData), "lifecycle_state: created") {
		t.Fatalf("expected created node metadata to persist lifecycle_state: created, got %s", string(createdData))
	}

	if _, err := service.NodeStart(ctx, node.ID); err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	startedData, err := os.ReadFile(service.store.nodePath(node.ID))
	if err != nil {
		t.Fatalf("ReadFile(started node.yaml) error = %v", err)
	}
	if strings.Contains(string(startedData), "lifecycle_state: created") {
		t.Fatalf("expected started node metadata to drop lifecycle_state: created, got %s", string(startedData))
	}
}

func TestNodeStartUsesConfiguredWorkspaceSeedPrepareCommand(t *testing.T) {
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

	project.RuntimeCommands.WorkspaceSeedPrepare = []string{"echo preparing {{sandbox_name}} {{target_path}} {{target_parent}}"}
	if err := service.store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject(custom workspace seed prepare command) error = %v", err)
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Project:       project.ID,
		Slug:          "root-node",
		WorkspaceMode: WorkspaceModeCopy,
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	if _, err := service.NodeStart(ctx, node.ID); err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	if len(service.sandbox.(*fakeSandbox).shellCalls) == 0 {
		t.Fatalf("expected workspace seed prepare command to run")
	}

	firstCall := service.sandbox.(*fakeSandbox).shellCalls[0]
	if firstCall.instanceName != node.SandboxName {
		t.Fatalf("expected workspace seed prepare to target %q, got %q", node.SandboxName, firstCall.instanceName)
	}

	expected := "echo preparing " + shellQuote(node.SandboxName) + " " + shellQuote(workspace) + " " + shellQuote(filepath.Dir(workspace))
	if got := strings.Join(firstCall.command, " "); got != "sh -lc "+expected {
		t.Fatalf("expected workspace seed prepare command %q, got %q", "sh -lc "+expected, got)
	}
}

func TestNodeCreateCleansUpPartialMetadataWhenSandboxCreateFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	service.sandbox.(*fakeSandbox).createErr = errors.New("forced create failure")
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	if _, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "broken-node",
	}); err == nil {
		t.Fatalf("expected NodeCreate() to fail when sandbox create fails")
	}

	entries, err := os.ReadDir(filepath.Join(service.cfg.MetadataRoot, "nodes"))
	if err != nil {
		t.Fatalf("ReadDir(nodes) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected failed node create to remove partial metadata, found %d entries", len(entries))
	}
}

func TestProjectScopedRuntimeCommandsApplyToNodeLifecycle(t *testing.T) {
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

	project.RuntimeCommands = RuntimeCommandTemplates{
		Create:    []string{"{{binary}} create --name {{sandbox_name}} --cpus 8 --memory 16G --init auto --shell /bin/bash{{mount_flags}}{{port_flags}}{{net_flags}} {{image}}"},
		Start:     []string{"{{binary}} start {{sandbox_name}} --debug"},
		Stop:      []string{"{{binary}} stop {{sandbox_name}} --timeout 30"},
		Delete:    []string{"{{binary}} rm -f {{sandbox_name}}"},
		Clone:     []string{"{{binary}} snapshot create custom-{{sandbox_name}} --from {{source_sandbox}} --force"},
		Copy:      []string{"{{binary}} copy {{source_path}} {{copy_target}} --quiet"},
		ShellExec: []string{"{{binary}} exec{{workdir_flag}} {{sandbox_name}}{{command_args}} --debug"},
	}
	if err := service.store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject(custom commands) error = %v", err)
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Project:       project.ID,
		Slug:          "root-node",
		WorkspaceMode: WorkspaceModeCopy,
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	if _, err := service.NodeStart(ctx, node.ID); err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	childNode, err := service.NodeClone(ctx, NodeCloneInput{
		SourceNode: node.ID,
		NodeSlug:   "root-node-clone",
	})
	if err != nil {
		t.Fatalf("NodeClone() error = %v", err)
	}

	if _, err := service.NodeDelete(ctx, childNode.ID); err != nil {
		t.Fatalf("NodeDelete() error = %v", err)
	}

	invocations := service.sandbox.(*fakeSandbox).invocations
	for _, expected := range []string{
		"create:'msb' create --name ",
		"--cpus 8 --memory 16G --init auto",
		"start:'msb' start '" + node.SandboxName + "' --debug",
		"stop:'msb' stop '" + node.SandboxName + "' --timeout 30",
		"clone:'msb' snapshot create custom-'" + childNode.SandboxName + "' --from '" + node.SandboxName + "' --force",
		"copy:'msb' copy ",
		"--quiet",
		"shell:'msb' exec --workdir ",
		"--debug",
		"delete:'msb' rm -f '" + childNode.SandboxName + "'",
	} {
		if !containsSubstring(invocations, expected) {
			t.Fatalf("expected invocation containing %q, got %v", expected, invocations)
		}
	}
}

func TestPartialNodeDirectoriesDoNotBlockHealthyNodeOperations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	if err := os.MkdirAll(filepath.Join(service.cfg.MetadataRoot, "nodes", "partial-node"), 0o755); err != nil {
		t.Fatalf("MkdirAll(partial node) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(service.cfg.MetadataRoot, "nodes", "partial-node", "instance.sandbox.yaml"), []byte("arch: aarch64\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(partial template) error = %v", err)
	}

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "healthy-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	nodes, err := service.NodeList(ctx, false)
	if err != nil {
		t.Fatalf("NodeList() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != node.ID {
		t.Fatalf("expected only the healthy node to be listed, got %#v", nodes)
	}

	node, err = service.NodeStart(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}
	if node.Status != NodeStatusRunning {
		t.Fatalf("expected healthy node to reach running state, got %q", node.Status)
	}

	tree, err := service.ProjectTree(ctx, "", false)
	if err != nil {
		t.Fatalf("ProjectTree() error = %v", err)
	}
	if len(tree) != 1 || len(tree[0].Nodes) != 1 || tree[0].Nodes[0].ID != node.ID {
		t.Fatalf("expected project tree to include only the healthy node, got %#v", tree)
	}
}

func TestNodeListReconcilesRuntimeStatusInBatch(t *testing.T) {
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

	firstNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "first-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate(first) error = %v", err)
	}
	secondNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "second-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate(second) error = %v", err)
	}

	bootstrap, err := service.store.LoadBootstrapState(firstNode.ID)
	if err != nil {
		t.Fatalf("LoadBootstrapState(first) error = %v", err)
	}
	firstNode.Status = NodeStatusStopped
	firstNode.LastRuntimeObservation = &RuntimeObservation{
		Name:   firstNode.SandboxName,
		Exists: true,
		Status: "stopped",
	}
	firstNode.UpdatedAt = service.now()
	if err := service.store.SaveNode(firstNode, bootstrap); err != nil {
		t.Fatalf("SaveNode(first) error = %v", err)
	}

	bootstrap, err = service.store.LoadBootstrapState(secondNode.ID)
	if err != nil {
		t.Fatalf("LoadBootstrapState(second) error = %v", err)
	}
	secondNode.Status = NodeStatusStopped
	secondNode.LastRuntimeObservation = &RuntimeObservation{
		Name:   secondNode.SandboxName,
		Exists: true,
		Status: "stopped",
	}
	secondNode.UpdatedAt = service.now()
	if err := service.store.SaveNode(secondNode, bootstrap); err != nil {
		t.Fatalf("SaveNode(second) error = %v", err)
	}

	fake := service.sandbox.(*fakeSandbox)
	fake.observations[firstNode.SandboxName] = RuntimeObservation{
		Name:   firstNode.SandboxName,
		Exists: true,
		Status: "running",
		Dir:    "/fake/" + firstNode.SandboxName,
	}
	fake.observations[secondNode.SandboxName] = RuntimeObservation{
		Name:   secondNode.SandboxName,
		Exists: true,
		Status: "stopped",
		Dir:    "/fake/" + secondNode.SandboxName,
	}
	fake.listCalls = 0

	nodes, err := service.NodeList(ctx, false)
	if err != nil {
		t.Fatalf("NodeList() error = %v", err)
	}
	if fake.listCalls != 1 {
		t.Fatalf("expected NodeList to reconcile with one sandbox.List call, got %d", fake.listCalls)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected two nodes, got %#v", nodes)
	}

	nodeByID := map[string]Node{}
	for _, node := range nodes {
		nodeByID[node.ID] = node
	}

	if got := nodeByID[firstNode.ID].Status; got != NodeStatusRunning {
		t.Fatalf("expected first node status to reconcile to running, got %q", got)
	}
	if got := nodeByID[firstNode.ID].LastRuntimeObservation; got == nil || got.Status != "running" {
		t.Fatalf("expected first node runtime observation to reconcile to running, got %#v", got)
	}
	if got := nodeByID[secondNode.ID].Status; got != NodeStatusStopped {
		t.Fatalf("expected second node status to remain stopped, got %q", got)
	}

	storedNode, err := service.store.NodeByIDOrSlug(firstNode.ID)
	if err != nil {
		t.Fatalf("NodeByIDOrSlug(first) error = %v", err)
	}
	if storedNode.Status != "" {
		t.Fatalf("expected runtime status to stay out of persisted node metadata, got %q", storedNode.Status)
	}
	if storedNode.LastRuntimeObservation != nil {
		t.Fatalf("expected persisted node metadata to omit runtime observations, got %#v", storedNode.LastRuntimeObservation)
	}
}

func TestProjectTreeReconcilesRuntimeStatusInBatch(t *testing.T) {
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

	bootstrap, err := service.store.LoadBootstrapState(node.ID)
	if err != nil {
		t.Fatalf("LoadBootstrapState() error = %v", err)
	}
	node.Status = NodeStatusStopped
	node.LastRuntimeObservation = &RuntimeObservation{
		Name:   node.SandboxName,
		Exists: true,
		Status: "stopped",
	}
	node.UpdatedAt = service.now()
	if err := service.store.SaveNode(node, bootstrap); err != nil {
		t.Fatalf("SaveNode() error = %v", err)
	}

	fake := service.sandbox.(*fakeSandbox)
	fake.observations[node.SandboxName] = RuntimeObservation{
		Name:   node.SandboxName,
		Exists: true,
		Status: "running",
		Dir:    "/fake/" + node.SandboxName,
	}
	fake.listCalls = 0

	tree, err := service.ProjectTree(ctx, "", false)
	if err != nil {
		t.Fatalf("ProjectTree() error = %v", err)
	}
	if fake.listCalls != 1 {
		t.Fatalf("expected ProjectTree to reconcile with one sandbox.List call, got %d", fake.listCalls)
	}
	if len(tree) != 1 || len(tree[0].Nodes) != 1 {
		t.Fatalf("expected one root project with one node, got %#v", tree)
	}
	if got := tree[0].Nodes[0].Status; got != NodeStatusRunning {
		t.Fatalf("expected tree node status to reconcile to running, got %q", got)
	}
	if got := tree[0].Nodes[0].LastRuntimeObservation; got == nil || got.Status != "running" {
		t.Fatalf("expected tree node runtime observation to reconcile to running, got %#v", got)
	}
}

func TestDoctorReportsIncompleteNodeMetadataDirectories(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)

	partialDir := filepath.Join(service.cfg.MetadataRoot, "nodes", "partial-node")
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(partial node) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(partialDir, "instance.sandbox.yaml"), []byte("arch: aarch64\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(template) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(partialDir, "sandbox.ref"), []byte("project-node-12345678\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(instance ref) error = %v", err)
	}

	report, err := service.Doctor(ctx, false)
	if err != nil {
		t.Fatalf("Doctor() error = %v", err)
	}

	warningText := strings.Join(report.Warnings, "\n")
	if !strings.Contains(warningText, "incomplete node metadata directory") {
		t.Fatalf("expected doctor warning for incomplete node metadata, got %q", warningText)
	}
	if !strings.Contains(warningText, "project-node-12345678") {
		t.Fatalf("expected doctor warning to include instance name, got %q", warningText)
	}
	if !strings.Contains(warningText, "node cleanup-incomplete --apply") {
		t.Fatalf("expected doctor warning to include cleanup command, got %q", warningText)
	}
}

func TestNodeCleanupIncompleteDryRunAndApply(t *testing.T) {
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

	healthyNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "healthy-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	partialDir := filepath.Join(service.cfg.MetadataRoot, "nodes", "partial-node")
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(partial node) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(partialDir, "instance.sandbox.yaml"), []byte("arch: aarch64\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(template) error = %v", err)
	}
	instanceName := "partial-project-partial-node-12345678"
	if err := os.WriteFile(filepath.Join(partialDir, "sandbox.ref"), []byte(instanceName+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(instance ref) error = %v", err)
	}
	if err := os.WriteFile(service.store.nodeInstanceIndexPath(instanceName), []byte("partial-node\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(instance index) error = %v", err)
	}

	dryRun, err := service.NodeCleanupIncomplete(false)
	if err != nil {
		t.Fatalf("NodeCleanupIncomplete(false) error = %v", err)
	}
	if !dryRun.DryRun {
		t.Fatalf("expected dry-run result")
	}
	if len(dryRun.Items) != 1 || dryRun.Items[0].NodeID != "partial-node" {
		t.Fatalf("expected one partial node in dry-run, got %#v", dryRun.Items)
	}
	if !exists(partialDir) {
		t.Fatalf("expected dry-run to leave partial node directory in place")
	}
	if !exists(service.store.nodeInstanceIndexPath(instanceName)) {
		t.Fatalf("expected dry-run to leave instance index in place")
	}

	applied, err := service.NodeCleanupIncomplete(true)
	if err != nil {
		t.Fatalf("NodeCleanupIncomplete(true) error = %v", err)
	}
	if applied.DryRun {
		t.Fatalf("expected apply result to report DryRun=false")
	}
	if len(applied.Items) != 1 || applied.Items[0].NodeID != "partial-node" {
		t.Fatalf("expected one partial node in apply result, got %#v", applied.Items)
	}
	if exists(partialDir) {
		t.Fatalf("expected apply to remove partial node directory")
	}
	if exists(service.store.nodeInstanceIndexPath(instanceName)) {
		t.Fatalf("expected apply to remove orphaned instance index")
	}

	nodes, err := service.NodeList(ctx, false)
	if err != nil {
		t.Fatalf("NodeList() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != healthyNode.ID {
		t.Fatalf("expected cleanup to leave healthy node untouched, got %#v", nodes)
	}
}

func TestNodeCloneCreatesSiblingNodeInSameProject(t *testing.T) {
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

	childNode, err := service.NodeClone(ctx, NodeCloneInput{
		SourceNode: node.ID,
		NodeSlug:   "child-node",
	})
	if err != nil {
		t.Fatalf("NodeClone() error = %v", err)
	}

	if childNode.ProjectID != project.ID {
		t.Fatalf("expected child node project id %q, got %q", project.ID, childNode.ProjectID)
	}

	if childNode.ParentNodeID != node.ID {
		t.Fatalf("expected child node parent id %q, got %q", node.ID, childNode.ParentNodeID)
	}

	if childNode.SandboxName != "child-node" {
		t.Fatalf("expected cloned node sandbox instance name to match child node slug, got %q", childNode.SandboxName)
	}

	if childNode.GuestWorkspacePath != workspace {
		t.Fatalf("expected child node guest workspace path %q, got %q", workspace, childNode.GuestWorkspacePath)
	}

	if childNode.WorkspaceSeeded {
		t.Fatalf("expected unstarted source clone to remain unseeded")
	}

	if !containsPrefix(service.sandbox.(*fakeSandbox).calls, "clone:"+node.SandboxName+"->"+childNode.SandboxName) {
		t.Fatalf("expected msb clone delegation, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}

	projects, err := service.ProjectList(false)
	if err != nil {
		t.Fatalf("ProjectList() error = %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected clone to keep a single project, got %d", len(projects))
	}
}

func TestNodeClonePreservesMountedWorkspaceMode(t *testing.T) {
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
		Project:       project.ID,
		Slug:          "root-node",
		WorkspaceMode: WorkspaceModeMounted,
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	childNode, err := service.NodeClone(ctx, NodeCloneInput{
		SourceNode: node.ID,
		NodeSlug:   "child-node",
	})
	if err != nil {
		t.Fatalf("NodeClone() error = %v", err)
	}

	if got := nodeWorkspaceMode(childNode); got != WorkspaceModeMounted {
		t.Fatalf("expected cloned node workspace mode mounted, got %q", got)
	}
	if childNode.WorkspaceMountPath != workspace {
		t.Fatalf("expected child node mount path %q, got %q", workspace, childNode.WorkspaceMountPath)
	}
	if childNode.GuestWorkspacePath != workspace {
		t.Fatalf("expected cloned mounted node to keep guest workspace path %q, got %q", workspace, childNode.GuestWorkspacePath)
	}
}

func TestNodeCloneRejectsAgentProfileOverride(t *testing.T) {
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

	_, err = service.NodeClone(ctx, NodeCloneInput{
		SourceNode:   node.ID,
		NodeSlug:     "child-node",
		AgentProfile: "other-profile",
	})
	if err == nil {
		t.Fatalf("expected NodeClone() to reject agent profile overrides")
	}
}

func TestHomeWithLegacyPatchDirsStillLoads(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)

	if _, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "parent",
		WorkspacePath: workspace,
	}); err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	// Simulate a home written by an older binary that still had the patch
	// subsystem: a patch metadata directory and a by-status index ref.
	home := service.cfg.MetadataRoot
	writeFile(t, filepath.Join(home, "patches", "legacy-patch-id", "proposal.yaml"), "id: legacy-patch-id\nstatus: draft\n")
	writeFile(t, filepath.Join(home, "_index", "patches", "by-status", "draft", "legacy-patch-id.ref"), "legacy-patch-id\n")

	if _, err := service.ProjectList(false); err != nil {
		t.Fatalf("ProjectList() error = %v", err)
	}

	if _, err := service.NodeList(ctx, false); err != nil {
		t.Fatalf("NodeList() error = %v", err)
	}

	report, err := service.Doctor(ctx, false)
	if err != nil {
		t.Fatalf("Doctor() error = %v", err)
	}

	for _, warning := range report.Warnings {
		if strings.Contains(strings.ToLower(warning), "patch") {
			t.Fatalf("expected Doctor to ignore legacy patch metadata, got warning %q", warning)
		}
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

	if !containsCall(service.sandbox.(*fakeSandbox).calls, "shell:"+node.SandboxName+":uname -a") {
		t.Fatalf("expected shell alias delegation, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}

	shellCalls := service.sandbox.(*fakeSandbox).shellCalls
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

func TestNodeCloneInheritsSourceNodeRuntimeCommandsByDefault(t *testing.T) {
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
		RuntimeCommands: RuntimeCommandTemplates{
			Clone: []string{"{{binary}} snapshot create inherited-{{sandbox_name}} --from {{source_sandbox}} --force"},
			Start: []string{"{{binary}} start {{sandbox_name}} --vm-type=vz"},
		},
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	childNode, err := service.NodeClone(ctx, NodeCloneInput{
		SourceNode: node.ID,
		NodeSlug:   "root-node-clone",
	})
	if err != nil {
		t.Fatalf("NodeClone() error = %v", err)
	}

	if strings.Join(childNode.RuntimeCommands.Clone, "|") != strings.Join(node.RuntimeCommands.Clone, "|") {
		t.Fatalf("expected cloned node to inherit clone command override %q, got %q", node.RuntimeCommands.Clone, childNode.RuntimeCommands.Clone)
	}
	if strings.Join(childNode.RuntimeCommands.Start, "|") != strings.Join(node.RuntimeCommands.Start, "|") {
		t.Fatalf("expected cloned node to inherit start command override %q, got %q", node.RuntimeCommands.Start, childNode.RuntimeCommands.Start)
	}
	if !containsSubstring(service.sandbox.(*fakeSandbox).invocations, "snapshot create inherited-") {
		t.Fatalf("expected clone invocation to use inherited node-specific clone command override, invocations = %v", service.sandbox.(*fakeSandbox).invocations)
	}
}

func TestEnvironmentConfigLifecycleAndProjectResolution(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	config, err := service.EnvironmentConfigCreate(EnvironmentConfigCreateInput{
		Slug:              "shared-dev",
		BootstrapCommands: []string{"./script/setup", "direnv allow"},
	})
	if err != nil {
		t.Fatalf("EnvironmentConfigCreate() error = %v", err)
	}

	if got := strings.Join(config.BootstrapCommands, "|"); got != "./script/setup|direnv allow" {
		t.Fatalf("expected created commands, got %q", got)
	}

	configs, err := service.EnvironmentConfigList(false)
	if err != nil {
		t.Fatalf("EnvironmentConfigList() error = %v", err)
	}
	if !containsEnvironmentConfigSlug(configs, "shared-dev") {
		t.Fatalf("expected shared-dev to be listed, got %#v", configs)
	}

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:               "root",
		WorkspacePath:      workspace,
		EnvironmentConfigs: []string{"shared-dev"},
		BootstrapCommands:  []string{"make init"},
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	if got := strings.Join(project.EnvironmentConfigs, "|"); got != "shared-dev" {
		t.Fatalf("expected project environment configs to be assigned, got %q", got)
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "root-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate(root-node) error = %v", err)
	}

	bootstrap, err := service.store.LoadBootstrapState(node.ID)
	if err != nil {
		t.Fatalf("LoadBootstrapState(root-node) error = %v", err)
	}
	if got := strings.Join(bootstrap.BootstrapCommands, "|"); got != "./script/setup|direnv allow|make init" {
		t.Fatalf("expected resolved bootstrap commands, got %q", got)
	}

	if _, err := service.EnvironmentConfigDelete("shared-dev"); err == nil {
		t.Fatalf("expected delete to reject referenced environment config")
	}

	config, err = service.EnvironmentConfigUpdate("shared-dev", EnvironmentConfigUpdateInput{
		BootstrapCommands: []string{"mise install"},
	})
	if err != nil {
		t.Fatalf("EnvironmentConfigUpdate() error = %v", err)
	}
	if got := strings.Join(config.BootstrapCommands, "|"); got != "mise install" {
		t.Fatalf("expected updated commands, got %q", got)
	}

	node, err = service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "root-node-2",
	})
	if err != nil {
		t.Fatalf("NodeCreate(root-node-2) error = %v", err)
	}

	bootstrap, err = service.store.LoadBootstrapState(node.ID)
	if err != nil {
		t.Fatalf("LoadBootstrapState(root-node-2) error = %v", err)
	}
	if got := strings.Join(bootstrap.BootstrapCommands, "|"); got != "mise install|make init" {
		t.Fatalf("expected updated config commands to apply to future nodes, got %q", got)
	}

	project, err = service.ProjectUpdate(project.ID, ProjectUpdateInput{ClearEnvironmentConfigs: true})
	if err != nil {
		t.Fatalf("ProjectUpdate(clear env configs) error = %v", err)
	}
	if len(project.EnvironmentConfigs) != 0 {
		t.Fatalf("expected project environment configs to be cleared, got %v", project.EnvironmentConfigs)
	}

	deleted, err := service.EnvironmentConfigDelete("shared-dev")
	if err != nil {
		t.Fatalf("EnvironmentConfigDelete() error = %v", err)
	}
	if deleted.DeletedAt == nil {
		t.Fatalf("expected deleted environment config to be tombstoned")
	}

	configs, err = service.EnvironmentConfigList(false)
	if err != nil {
		t.Fatalf("EnvironmentConfigList(after delete) error = %v", err)
	}
	if containsEnvironmentConfigSlug(configs, "shared-dev") {
		t.Fatalf("expected deleted environment config to be filtered from list, got %#v", configs)
	}
}

func TestBuiltInEnvironmentConfigsSeedOnReadyWithoutOverwritingEdits(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)

	// Since work item 0.3, reads no longer seed: a mutating readiness check
	// (what any mutating command runs) performs the built-in seeding.
	if err := service.EnsureReady(true); err != nil {
		t.Fatalf("EnsureReady(true) error = %v", err)
	}

	configs, err := service.EnvironmentConfigList(false)
	if err != nil {
		t.Fatalf("EnvironmentConfigList() error = %v", err)
	}

	assertEnvironmentConfigCommands(t, configs, "codex",
		"apt-get update && apt-get install -y ca-certificates curl git",
		`curl -fsSL https://chatgpt.com/codex/install.sh | CODEX_INSTALL_DIR=/usr/local/bin CODEX_NON_INTERACTIVE=1 sh`,
		`command -v codex >/dev/null 2>&1`,
	)
	assertEnvironmentConfigCommands(t, configs, "claude-code",
		"curl -fsSL https://claude.ai/install.sh | bash",
	)

	config, err := service.EnvironmentConfigShow("codex")
	if err != nil {
		t.Fatalf("EnvironmentConfigShow(codex) error = %v", err)
	}
	if containsSubstring(config.BootstrapCommands, "npm") {
		t.Fatalf("expected codex bootstrap to use the official standalone installer instead of npm, got %q", strings.Join(config.BootstrapCommands, "|"))
	}

	if _, err := service.EnvironmentConfigUpdate("codex", EnvironmentConfigUpdateInput{
		BootstrapCommands: []string{"echo customized"},
	}); err != nil {
		t.Fatalf("EnvironmentConfigUpdate(codex) error = %v", err)
	}

	if err := service.EnsureReady(false); err != nil {
		t.Fatalf("EnsureReady(false) error = %v", err)
	}

	config, err = service.EnvironmentConfigShow("codex")
	if err != nil {
		t.Fatalf("EnvironmentConfigShow(codex) error = %v", err)
	}
	if got := strings.Join(config.BootstrapCommands, "|"); got != "echo customized" {
		t.Fatalf("expected customized codex commands to persist, got %q", got)
	}
}

func TestCodexEnvironmentBootstrapCannotCompleteWithoutCodexExecutable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")
	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:               "codex-project",
		WorkspacePath:      workspace,
		EnvironmentConfigs: []string{"codex"},
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}
	node, err := service.NodeCreate(ctx, NodeCreateInput{Project: project.ID, Slug: "codex-node"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	service.sandbox.(*fakeSandbox).failCommand = "command -v codex"
	if _, err := service.NodeStart(ctx, node.ID); err == nil {
		t.Fatal("NodeStart() succeeded without the selected codex executable")
	}

	bootstrap, err := service.store.LoadBootstrapState(node.ID)
	if err != nil {
		t.Fatalf("LoadBootstrapState() error = %v", err)
	}
	if bootstrap.Completed {
		t.Fatal("codex bootstrap was marked complete after executable validation failed")
	}
	stored, err := service.store.NodeByID(node.ID)
	if err != nil {
		t.Fatalf("NodeByID() error = %v", err)
	}
	if stored.Status != NodeStatusFailed || stored.BootstrapCompleted {
		t.Fatalf("failed codex bootstrap node = %#v", stored)
	}
}

func TestDeletedBuiltInEnvironmentConfigIsNotRecreated(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	if err := service.EnsureReady(true); err != nil {
		t.Fatalf("EnsureReady(true) error = %v", err)
	}
	defaultConfiguration, err := service.ConfigurationShow(DefaultConfigurationSlug)
	if err != nil {
		t.Fatalf("ConfigurationShow(default) error = %v", err)
	}
	if _, err := service.ConfigurationUpdate(defaultConfiguration.ID, ConfigurationUpdateInput{Environments: []string{}}); err != nil {
		t.Fatalf("ConfigurationUpdate(clear default environments) error = %v", err)
	}

	if _, err := service.EnvironmentConfigDelete("codex"); err != nil {
		t.Fatalf("EnvironmentConfigDelete(codex) error = %v", err)
	}

	if err := service.EnsureReady(false); err != nil {
		t.Fatalf("EnsureReady(false) error = %v", err)
	}

	configs, err := service.EnvironmentConfigList(false)
	if err != nil {
		t.Fatalf("EnvironmentConfigList(after delete) error = %v", err)
	}
	if containsEnvironmentConfigSlug(configs, "codex") {
		t.Fatalf("expected deleted built-in environment config to stay deleted, got %#v", configs)
	}
}

func TestShellUsesGuestWorkspacePathForInteractiveEntry(t *testing.T) {
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

	shellCalls := service.sandbox.(*fakeSandbox).shellCalls
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

	if got, want := strings.Join(lastCall.command, " "), strings.Join(interactiveShellLaunchCommand(), " "); got != want {
		t.Fatalf("expected interactive shell bootstrap command %q, got %q", want, got)
	}
}

func TestInteractiveShellLaunchCommandRepairsGNUSttyBeforeExec(t *testing.T) {
	t.Parallel()

	got := strings.Join(interactiveShellLaunchCommand(), " ")
	if !strings.Contains(got, "/usr/bin/gnustty") {
		t.Fatalf("expected interactive shell command to reference gnustty, got %q", got)
	}
	if !strings.Contains(got, "uutils coreutils") {
		t.Fatalf("expected interactive shell command to detect uutils stty, got %q", got)
	}
	if !strings.Contains(got, `"${SHELL:-/bin/bash}" -l`) {
		t.Fatalf("expected interactive shell command to run the user's login shell, got %q", got)
	}
	if !strings.Contains(got, `mktemp "${shell_inputrc_dir}/.codelima-inputrc.XXXXXX" 2>/dev/null`) {
		t.Fatalf("expected interactive shell command to create a temporary inputrc in a writable dir, got %q", got)
	}
	if !strings.Contains(got, `for shell_inputrc_dir in "${HOME:-}" "${PWD:-}/tmp" "${TMPDIR:-/tmp}"`) {
		t.Fatalf("expected interactive shell command to probe writable inputrc dirs before HOME fails (TODO #18), got %q", got)
	}
	if !strings.Contains(got, `"\e[27;2;13~": "\C-v\C-j"`) {
		t.Fatalf("expected interactive shell command to bind modifyOtherKeys shift-enter, got %q", got)
	}
	if !strings.Contains(got, `"\e[13;2u": "\C-v\C-j"`) {
		t.Fatalf("expected interactive shell command to bind CSI-u shift-enter, got %q", got)
	}
}

func TestProjectUpdateWorkspacePath(t *testing.T) {
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

	newWorkspace := filepath.Join(t.TempDir(), "moved-workspace")
	if err := os.MkdirAll(newWorkspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	updated, err := service.ProjectUpdate(project.ID, ProjectUpdateInput{
		WorkspacePath: &newWorkspace,
	})
	if err != nil {
		t.Fatalf("ProjectUpdate() error = %v", err)
	}

	if updated.WorkspacePath != newWorkspace {
		t.Fatalf("expected workspace path %q, got %q", newWorkspace, updated.WorkspacePath)
	}
}

func TestProjectUpdateWorkspacePathRejectsLiveNodes(t *testing.T) {
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

	if _, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "root-node",
	}); err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	newWorkspace := filepath.Join(t.TempDir(), "moved-workspace")
	if err := os.MkdirAll(newWorkspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	if _, err := service.ProjectUpdate(project.ID, ProjectUpdateInput{
		WorkspacePath: &newWorkspace,
	}); err == nil {
		t.Fatalf("expected ProjectUpdate() to reject workspace rebind while nodes are live")
	}
}

func TestNodeStartFailsWhenProjectWorkspacePathIsMissingBeforeSeed(t *testing.T) {
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

	if err := os.RemoveAll(workspace); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}

	if _, err := service.NodeStart(ctx, node.ID); err == nil {
		t.Fatalf("expected NodeStart() to fail when the registered workspace path is missing before the guest copy is seeded")
	}

	if len(service.sandbox.(*fakeSandbox).shellCalls) != 0 {
		t.Fatalf("expected guest workspace preparation to be skipped when workspace is missing")
	}
}

func TestShellAllowsSeededNodeWhenProjectWorkspacePathIsMissing(t *testing.T) {
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

	node, err = service.NodeStart(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	if !node.WorkspaceSeeded {
		t.Fatalf("expected guest workspace to be seeded")
	}

	if err := os.RemoveAll(workspace); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}

	if err := service.Shell(ctx, node.ID, []string{"pwd"}); err != nil {
		t.Fatalf("Shell() error = %v", err)
	}

	lastCall := service.sandbox.(*fakeSandbox).shellCalls[len(service.sandbox.(*fakeSandbox).shellCalls)-1]
	if lastCall.workdir != workspace {
		t.Fatalf("expected shell workdir %q, got %q", workspace, lastCall.workdir)
	}
}

func TestNodeCloneCyclesRunningSourceNodeAndPreservesGuestState(t *testing.T) {
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

	parentNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "root-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	parentNode, err = service.NodeStart(ctx, parentNode.ID)
	if err != nil {
		t.Fatalf("NodeStart(parent) error = %v", err)
	}

	childNode, err := service.NodeClone(ctx, NodeCloneInput{
		SourceNode: parentNode.ID,
		NodeSlug:   "child-node",
	})
	if err != nil {
		t.Fatalf("NodeClone() error = %v", err)
	}

	if childNode.GuestWorkspacePath != workspace {
		t.Fatalf("expected cloned node guest workspace path %q, got %q", workspace, childNode.GuestWorkspacePath)
	}

	if !childNode.WorkspaceSeeded {
		t.Fatalf("expected cloned node guest workspace to remain seeded")
	}

	if !childNode.BootstrapCompleted {
		t.Fatalf("expected cloned node bootstrap to remain completed")
	}

	childBootstrap, err := service.store.LoadBootstrapState(childNode.ID)
	if err != nil {
		t.Fatalf("LoadBootstrapState(child) error = %v", err)
	}

	if !childBootstrap.Completed {
		t.Fatalf("expected cloned bootstrap state to remain completed")
	}

	if !containsCall(service.sandbox.(*fakeSandbox).calls, "stop:"+parentNode.SandboxName) {
		t.Fatalf("expected running source node to be stopped before clone, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}

	if !containsCall(service.sandbox.(*fakeSandbox).calls, "clone:"+parentNode.SandboxName+"->"+childNode.SandboxName) {
		t.Fatalf("expected msb clone delegation, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}

	if !containsCall(service.sandbox.(*fakeSandbox).calls, "start:"+parentNode.SandboxName) {
		t.Fatalf("expected running source node to be restarted after clone, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}

	callsBeforeChildStart := len(service.sandbox.(*fakeSandbox).calls)
	childNode, err = service.NodeStart(ctx, childNode.ID)
	if err != nil {
		t.Fatalf("NodeStart(child) error = %v", err)
	}

	newCalls := append([]string(nil), service.sandbox.(*fakeSandbox).calls[callsBeforeChildStart:]...)
	if containsCall(newCalls, "copy:"+childNode.SandboxName+":"+workspace+"->"+workspace) {
		t.Fatalf("expected cloned node start to avoid reseeding the guest workspace, calls = %v", newCalls)
	}

	if containsCall(newCalls, "shell:"+childNode.SandboxName+":sh -lc cd "+quoted(workspace)+" && ./script/setup") {
		t.Fatalf("expected cloned node start to avoid rerunning setup, calls = %v", newCalls)
	}
}

func TestNodeCloneStopsCloneWhenProviderLeavesItRunning(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	service.sandbox.(*fakeSandbox).cloneStatus = "running"
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	parentNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "root-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	parentNode, err = service.NodeStart(ctx, parentNode.ID)
	if err != nil {
		t.Fatalf("NodeStart(parent) error = %v", err)
	}

	childNode, err := service.NodeClone(ctx, NodeCloneInput{
		SourceNode: parentNode.ID,
		NodeSlug:   "child-node",
	})
	if err != nil {
		t.Fatalf("NodeClone() error = %v", err)
	}

	if childNode.Status != NodeStatusStopped {
		t.Fatalf("expected cloned node to be normalized to stopped, got %q", childNode.Status)
	}

	if !containsCall(service.sandbox.(*fakeSandbox).calls, "stop:"+childNode.SandboxName) {
		t.Fatalf("expected running clone instance to be stopped, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}
}

func TestNodeDeleteBySlugTargetsActiveNodeWhenDeletedNodeSharesSlug(t *testing.T) {
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

	oldNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "design",
	})
	if err != nil {
		t.Fatalf("NodeCreate(old) error = %v", err)
	}

	oldNode, err = service.NodeDelete(ctx, oldNode.ID)
	if err != nil {
		t.Fatalf("NodeDelete(old) error = %v", err)
	}

	if oldNode.DeletedAt == nil {
		t.Fatalf("expected old node to be tombstoned")
	}

	newNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "design",
	})
	if err != nil {
		t.Fatalf("NodeCreate(new) error = %v", err)
	}

	deletedNode, err := service.NodeDelete(ctx, "design")
	if err != nil {
		t.Fatalf("NodeDelete(by slug) error = %v", err)
	}

	if deletedNode.ID != newNode.ID {
		t.Fatalf("expected delete by slug to target active node %q, got %q", newNode.ID, deletedNode.ID)
	}
}

func TestProjectCreateAllowsSlugReuseAfterProjectDeleted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate(initial) error = %v", err)
	}

	project, err = service.ProjectDelete(project.ID)
	if err != nil {
		t.Fatalf("ProjectDelete() error = %v", err)
	}

	if project.DeletedAt == nil {
		t.Fatalf("expected deleted project to be tombstoned")
	}

	recreatedWorkspace := filepath.Join(t.TempDir(), "workspace-recreated")
	if err := os.MkdirAll(recreatedWorkspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	recreated, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: recreatedWorkspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate(recreated) error = %v", err)
	}

	if recreated.ID == project.ID {
		t.Fatalf("expected recreated project to get a new id")
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
	service := NewService(cfg, newFakeSandbox(), strings.NewReader(""), ioDiscard{}, ioDiscard{})
	service.localTerminals = true
	if err := service.ensureDirectories(); err != nil {
		t.Fatalf("ensureDirectories() error = %v", err)
	}
	return service, workspace
}

func TestReclaimMountedNodeFilesystemCachesTargetsOnlyRunningMountedNodes(t *testing.T) {
	t.Parallel()
	service, workspace := newTestService(t)
	fake := service.sandbox.(*fakeSandbox)
	now := time.Now().UTC()
	nodes := []Node{
		{ID: newID(), Slug: "mounted-running", SandboxName: "mounted-running", DirectoryPath: workspace, WorkspaceMode: WorkspaceModeMounted, CreatedAt: now, UpdatedAt: now},
		{ID: newID(), Slug: "copy-running", SandboxName: "copy-running", DirectoryPath: workspace, WorkspaceMode: WorkspaceModeCopy, CreatedAt: now, UpdatedAt: now.Add(time.Second)},
		{ID: newID(), Slug: "mounted-stopped", SandboxName: "mounted-stopped", DirectoryPath: workspace, WorkspaceMode: WorkspaceModeMounted, CreatedAt: now, UpdatedAt: now.Add(2 * time.Second)},
	}
	for _, node := range nodes {
		if err := service.store.SaveNode(node, BootstrapState{}); err != nil {
			t.Fatalf("SaveNode(%s) error = %v", node.Slug, err)
		}
	}
	fake.observations[nodes[0].SandboxName] = RuntimeObservation{Name: nodes[0].SandboxName, Exists: true, Status: "running"}
	fake.observations[nodes[1].SandboxName] = RuntimeObservation{Name: nodes[1].SandboxName, Exists: true, Status: "running"}
	fake.observations[nodes[2].SandboxName] = RuntimeObservation{Name: nodes[2].SandboxName, Exists: true, Status: "stopped"}

	reclaimed, err := service.reclaimMountedNodeFilesystemCaches(context.Background())
	if err != nil {
		t.Fatalf("reclaimMountedNodeFilesystemCaches() error = %v", err)
	}
	if reclaimed != 1 {
		t.Fatalf("reclaimed nodes = %d, want 1", reclaimed)
	}
	fake.mu.Lock()
	calls := append([]fakeShellCall(nil), fake.shellCalls...)
	fake.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("shell calls = %#v, want one", calls)
	}
	if calls[0].instanceName != "mounted-running" || strings.Join(calls[0].command, " ") != "sh -c echo 2 > /proc/sys/vm/drop_caches" {
		t.Fatalf("reclaim shell call = %#v", calls[0])
	}
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

func containsEnvironmentConfigSlug(configs []EnvironmentConfig, slug string) bool {
	for _, config := range configs {
		if config.Slug == slug {
			return true
		}
	}
	return false
}

func assertEnvironmentConfigCommands(t *testing.T, configs []EnvironmentConfig, slug string, commands ...string) {
	t.Helper()

	for _, config := range configs {
		if config.Slug != slug {
			continue
		}

		if got := strings.Join(config.BootstrapCommands, "|"); got != strings.Join(commands, "|") {
			t.Fatalf("expected environment config %s commands %q, got %q", slug, strings.Join(commands, "|"), got)
		}
		return
	}

	t.Fatalf("expected environment config %s to exist, got %#v", slug, configs)
}

func quoted(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func countEnvironmentConfigSlug(configs []EnvironmentConfig, slug string) int {
	count := 0
	for _, config := range configs {
		if config.Slug == slug {
			count++
		}
	}
	return count
}

// snapshotHomeFiles records every file under root as path -> size/mtime/content
// hash so tests can assert that an operation left the metadata home untouched.
func snapshotHomeFiles(t *testing.T, root string) map[string]string {
	t.Helper()

	snapshot := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		snapshot[rel] = fmt.Sprintf("size=%d mtime=%s sha256=%x", info.Size(), info.ModTime().UTC().Format(time.RFC3339Nano), sum)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshotHomeFiles(%s) error = %v", root, err)
	}
	return snapshot
}

func diffFileSnapshots(before, after map[string]string) string {
	lines := []string{}
	for path, signature := range before {
		afterSignature, ok := after[path]
		if !ok {
			lines = append(lines, "removed: "+path)
			continue
		}
		if afterSignature != signature {
			lines = append(lines, fmt.Sprintf("changed: %s (%s -> %s)", path, signature, afterSignature))
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			lines = append(lines, "added: "+path)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestFreshHomeSeedsSingleBuiltInEnvironmentConfigs(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)

	if err := service.EnsureReady(true); err != nil {
		t.Fatalf("EnsureReady(true) error = %v", err)
	}

	// Repeated mutating readiness checks must stay idempotent.
	if err := service.EnsureReady(true); err != nil {
		t.Fatalf("EnsureReady(true, second) error = %v", err)
	}

	configs, err := service.EnvironmentConfigList(false)
	if err != nil {
		t.Fatalf("EnvironmentConfigList() error = %v", err)
	}

	if got := countEnvironmentConfigSlug(configs, "codex"); got != 1 {
		t.Fatalf("expected exactly one codex environment config, got %d (%#v)", got, configs)
	}
	if got := countEnvironmentConfigSlug(configs, "claude-code"); got != 1 {
		t.Fatalf("expected exactly one claude-code environment config, got %d (%#v)", got, configs)
	}
}

func TestConcurrentSeedingDoesNotDuplicate(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)

	const seeders = 12
	start := make(chan struct{})
	results := make(chan error, seeders)
	var wg sync.WaitGroup
	for i := 0; i < seeders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- service.EnsureReady(true)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("EnsureReady(true) error = %v", err)
		}
	}

	configs, err := service.EnvironmentConfigList(false)
	if err != nil {
		t.Fatalf("EnvironmentConfigList() error = %v", err)
	}

	if got := countEnvironmentConfigSlug(configs, "codex"); got != 1 {
		t.Fatalf("expected exactly one codex environment config after concurrent seeding, got %d (%#v)", got, configs)
	}
	if got := countEnvironmentConfigSlug(configs, "claude-code"); got != 1 {
		t.Fatalf("expected exactly one claude-code environment config after concurrent seeding, got %d (%#v)", got, configs)
	}
}

func TestTUIStartupSeedsBuiltInEnvironmentConfigs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	runner := &fakeTUIRunner{}
	service.tui = runner

	if err := service.TUI(ctx, ""); err != nil {
		t.Fatalf("TUI() error = %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("expected TUI runner to run once, got %d", runner.calls)
	}

	configs, err := service.EnvironmentConfigList(false)
	if err != nil {
		t.Fatalf("EnvironmentConfigList() error = %v", err)
	}
	if got := countEnvironmentConfigSlug(configs, "codex"); got != 1 {
		t.Fatalf("expected exactly one codex environment config after TUI startup, got %d (%#v)", got, configs)
	}
	if got := countEnvironmentConfigSlug(configs, "claude-code"); got != 1 {
		t.Fatalf("expected exactly one claude-code environment config after TUI startup, got %d (%#v)", got, configs)
	}
}

func TestReadSurfacesDoNotWrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{Slug: "root", WorkspacePath: workspace})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{Project: "root", Slug: "worker"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	if _, err := service.NodeStart(ctx, node.ID); err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	before := snapshotHomeFiles(t, service.cfg.MetadataRoot)

	// Simulate an external runtime transition: reads must report it live
	// (ADR 37 in-memory merge) without persisting anything.
	fake := service.sandbox.(*fakeSandbox)
	fake.observations[node.SandboxName] = RuntimeObservation{Name: node.SandboxName, Exists: true, Status: "stopped", Dir: "/fake/" + node.SandboxName}

	if _, err := service.ProjectTree(ctx, "", false); err != nil {
		t.Fatalf("ProjectTree() error = %v", err)
	}
	if _, err := service.ProjectTreeByWorkspaceRoot(ctx, workspace, false); err != nil {
		t.Fatalf("ProjectTreeByWorkspaceRoot() error = %v", err)
	}

	nodes, err := service.NodeList(ctx, false)
	if err != nil {
		t.Fatalf("NodeList() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].Status != NodeStatusStopped {
		t.Fatalf("expected NodeList to merge the live stopped observation in memory, got %#v", nodes)
	}

	shown, err := service.NodeShow(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeShow() error = %v", err)
	}
	if shown.Status != NodeStatusStopped {
		t.Fatalf("expected NodeShow to merge the live stopped observation in memory, got %q", shown.Status)
	}

	if _, err := service.ProjectList(false); err != nil {
		t.Fatalf("ProjectList() error = %v", err)
	}
	if _, err := service.ProjectShow(project.ID); err != nil {
		t.Fatalf("ProjectShow() error = %v", err)
	}
	if _, err := service.EnvironmentConfigList(false); err != nil {
		t.Fatalf("EnvironmentConfigList() error = %v", err)
	}
	if _, err := service.NodeLogs(node.ID); err != nil {
		t.Fatalf("NodeLogs() error = %v", err)
	}
	if _, err := service.Doctor(ctx, false); err != nil {
		t.Fatalf("Doctor() error = %v", err)
	}

	// The TUI auto-refresh tick reloads the tree through this path.
	if _, err := loadTUIProjectTree(ctx, service, ""); err != nil {
		t.Fatalf("loadTUIProjectTree() error = %v", err)
	}

	after := snapshotHomeFiles(t, service.cfg.MetadataRoot)
	if diff := diffFileSnapshots(before, after); diff != "" {
		t.Fatalf("expected read surfaces to leave the metadata home untouched, but files changed:\n%s", diff)
	}
}

func TestRefreshDoesNotRaceMutation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	if _, err := service.ProjectCreate(ctx, ProjectCreateInput{Slug: "root", WorkspacePath: workspace}); err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{Project: "root", Slug: "worker"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	if _, err := service.NodeStart(ctx, node.ID); err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	fake := service.sandbox.(*fakeSandbox)
	stopGate := newFakeSandboxGate()
	startGate := newFakeSandboxGate()
	fake.stopGate = stopGate
	fake.startGate = startGate

	refreshStop := make(chan struct{})
	refreshDone := make(chan struct{})
	go func() {
		defer close(refreshDone)
		for {
			select {
			case <-refreshStop:
				return
			default:
			}
			if _, err := service.NodeList(ctx, false); err != nil {
				t.Errorf("NodeList(refresh loop) error = %v", err)
				return
			}
		}
	}()

	stopResult := make(chan error, 1)
	go func() {
		_, err := service.NodeStop(ctx, node.ID)
		stopResult <- err
	}()
	awaitFakeSandboxGate(t, stopGate, node.SandboxName)
	for i := 0; i < 5; i++ {
		if _, err := service.NodeList(ctx, false); err != nil {
			t.Fatalf("NodeList(during stop) error = %v", err)
		}
	}
	close(stopGate.release)
	if err := <-stopResult; err != nil {
		t.Fatalf("NodeStop() error = %v", err)
	}

	startResult := make(chan error, 1)
	go func() {
		_, err := service.NodeStart(ctx, node.ID)
		startResult <- err
	}()
	awaitFakeSandboxGate(t, startGate, node.SandboxName)
	for i := 0; i < 5; i++ {
		if _, err := service.NodeList(ctx, false); err != nil {
			t.Fatalf("NodeList(during start) error = %v", err)
		}
	}
	close(startGate.release)
	if err := <-startResult; err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	close(refreshStop)
	<-refreshDone

	shown, err := service.NodeShow(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeShow() error = %v", err)
	}
	if shown.Status != NodeStatusRunning {
		t.Fatalf("expected node to be running after the stop/start cycle, got %q", shown.Status)
	}
}

// deleteFailingSandbox wraps *fakeSandbox and forces Delete to fail for the named
// instances so tests can exercise teardown-failure paths. Every other call is
// delegated to the embedded fake, so its observations and call bookkeeping stay
// authoritative.
type deleteFailingSandbox struct {
	*fakeSandbox
	failInstances map[string]bool
}

func (f *deleteFailingSandbox) Delete(ctx context.Context, project Project, node Node) error {
	if f.failInstances[node.SandboxName] {
		return externalCommandFailed(
			"forced delete failure",
			errors.New("teardown boom"),
			map[string]any{"sandbox_name": node.SandboxName},
		)
	}
	return f.fakeSandbox.Delete(ctx, project, node)
}

func seedObservation(fl *fakeSandbox, observation RuntimeObservation) {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	fl.observations[observation.Name] = observation
}

func TestNodeCleanupIncompleteTearsDownLiveInstanceBeforeRemovingMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	fl := service.sandbox.(*fakeSandbox)

	const instanceName = "orphan-project-orphan-node-abcd1234"
	partialDir := filepath.Join(service.cfg.MetadataRoot, "nodes", "orphan-node")
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(partial node) error = %v", err)
	}
	writeFile(t, filepath.Join(partialDir, "instance.sandbox.yaml"), "arch: aarch64\n")
	writeFile(t, filepath.Join(partialDir, "sandbox.ref"), instanceName+"\n")
	writeFile(t, service.store.nodeInstanceIndexPath(instanceName), "orphan-node\n")

	// The runtime instance named by the incomplete dir is still live.
	seedObservation(fl, RuntimeObservation{Name: instanceName, Exists: true, Status: "running", Dir: "/fake/" + instanceName})

	result, err := service.NodeCleanupIncomplete(true)
	if err != nil {
		t.Fatalf("NodeCleanupIncomplete(true) error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].NodeID != "orphan-node" {
		t.Fatalf("expected one incomplete item, got %#v", result.Items)
	}

	observations, err := fl.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if _, live := findObservation(observations, instanceName); live {
		t.Fatalf("orphan bug: cleanup removed metadata but left runtime instance %q running", instanceName)
	}
	if !containsCall(fl.calls, "delete:"+instanceName) {
		t.Fatalf("expected cleanup to tear down the live instance via sandbox.Delete, calls = %v", fl.calls)
	}

	if exists(partialDir) {
		t.Fatalf("expected cleanup to remove the incomplete node directory once the instance was torn down")
	}
	if exists(service.store.nodeInstanceIndexPath(instanceName)) {
		t.Fatalf("expected cleanup to remove the orphaned instance index")
	}
}

func TestNodeCleanupIncompleteKeepsDirectoryWhenTeardownFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	fl := service.sandbox.(*fakeSandbox)
	const instanceName = "stuck-project-stuck-node-99887766"
	service.sandbox = &deleteFailingSandbox{fakeSandbox: fl, failInstances: map[string]bool{instanceName: true}}

	partialDir := filepath.Join(service.cfg.MetadataRoot, "nodes", "stuck-node")
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(partial node) error = %v", err)
	}
	writeFile(t, filepath.Join(partialDir, "sandbox.ref"), instanceName+"\n")
	writeFile(t, service.store.nodeInstanceIndexPath(instanceName), "stuck-node\n")
	seedObservation(fl, RuntimeObservation{Name: instanceName, Exists: true, Status: "running"})

	_, err := service.NodeCleanupIncomplete(true)
	if err == nil {
		t.Fatalf("expected NodeCleanupIncomplete to fail when teardown fails")
	}
	if !strings.Contains(err.Error(), instanceName) {
		t.Fatalf("expected teardown-failure error to name instance %q, got %v", instanceName, err)
	}
	if !exists(partialDir) {
		t.Fatalf("expected cleanup to keep the incomplete dir when teardown fails")
	}
	if !exists(service.store.nodeInstanceIndexPath(instanceName)) {
		t.Fatalf("expected cleanup to keep the instance index when teardown fails")
	}
	observations, listErr := service.sandbox.List(ctx)
	if listErr != nil {
		t.Fatalf("List() error = %v", listErr)
	}
	if _, live := findObservation(observations, instanceName); !live {
		t.Fatalf("expected the live instance to survive a failed teardown")
	}
}

func TestNodeCleanupIncompleteDryRunDoesNotTearDownLiveInstance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	fl := service.sandbox.(*fakeSandbox)
	const instanceName = "dry-project-dry-node-1a2b3c4d"

	partialDir := filepath.Join(service.cfg.MetadataRoot, "nodes", "dry-node")
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(partial node) error = %v", err)
	}
	writeFile(t, filepath.Join(partialDir, "sandbox.ref"), instanceName+"\n")
	seedObservation(fl, RuntimeObservation{Name: instanceName, Exists: true, Status: "running"})

	result, err := service.NodeCleanupIncomplete(false)
	if err != nil {
		t.Fatalf("NodeCleanupIncomplete(false) error = %v", err)
	}
	if !result.DryRun {
		t.Fatalf("expected dry-run result")
	}
	if len(result.Items) != 1 || result.Items[0].NodeID != "dry-node" {
		t.Fatalf("expected one incomplete item in dry-run, got %#v", result.Items)
	}
	if containsCall(fl.calls, "delete:"+instanceName) {
		t.Fatalf("dry-run must not tear down the live instance, calls = %v", fl.calls)
	}
	if !exists(partialDir) {
		t.Fatalf("dry-run must leave the incomplete dir in place")
	}
	observations, err := fl.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if _, live := findObservation(observations, instanceName); !live {
		t.Fatalf("dry-run must leave the live instance running")
	}
}

func TestNodeDeleteTearsDownRunningInstance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	fl := service.sandbox.(*fakeSandbox)

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{Slug: "root", WorkspacePath: workspace})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}
	node, err := service.NodeCreate(ctx, NodeCreateInput{Project: project.ID, Slug: "runner"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	node, err = service.NodeStart(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}
	if node.Status != NodeStatusRunning {
		t.Fatalf("expected running node before delete, got %q", node.Status)
	}
	instanceName := node.SandboxName

	deleted, err := service.NodeDelete(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeDelete() error = %v", err)
	}
	if deleted.Status != NodeStatusTerminated {
		t.Fatalf("expected terminated status, got %q", deleted.Status)
	}
	if !containsCall(fl.calls, "delete:"+instanceName) {
		t.Fatalf("expected NodeDelete to delegate teardown to sandbox.Delete, calls = %v", fl.calls)
	}
	observations, err := fl.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if _, live := findObservation(observations, instanceName); live {
		t.Fatalf("expected NodeDelete to tear down the running instance %q", instanceName)
	}
	if exists(service.store.nodeInstanceIndexPath(instanceName)) {
		t.Fatalf("expected NodeDelete to remove the instance index for a terminated node")
	}
}

func TestNodeDeleteLeavesNodeListableWhenTeardownFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	fl := service.sandbox.(*fakeSandbox)

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{Slug: "root", WorkspacePath: workspace})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}
	node, err := service.NodeCreate(ctx, NodeCreateInput{Project: project.ID, Slug: "runner"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	node, err = service.NodeStart(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}
	instanceName := node.SandboxName

	failing := &deleteFailingSandbox{fakeSandbox: fl, failInstances: map[string]bool{instanceName: true}}
	service.sandbox = failing

	if _, err := service.NodeDelete(ctx, node.ID); err == nil {
		t.Fatalf("expected NodeDelete to fail when teardown fails")
	}

	nodes, err := service.NodeList(ctx, false)
	if err != nil {
		t.Fatalf("NodeList() error = %v", err)
	}
	found := false
	for _, listed := range nodes {
		if listed.ID == node.ID {
			found = true
			if listed.Status != NodeStatusTerminating {
				t.Fatalf("expected node to remain listable as terminating, got %q", listed.Status)
			}
		}
	}
	if !found {
		t.Fatalf("expected the node to remain listable after a failed teardown")
	}
	if !exists(service.store.nodeInstanceIndexPath(instanceName)) {
		t.Fatalf("expected the instance index to survive a failed teardown")
	}
	observations, err := failing.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if _, live := findObservation(observations, instanceName); !live {
		t.Fatalf("expected the live instance to survive a failed teardown")
	}

	// Recover the runtime and confirm a retry completes the deletion.
	failing.failInstances[instanceName] = false
	deleted, err := service.NodeDelete(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeDelete() retry error = %v", err)
	}
	if deleted.Status != NodeStatusTerminated {
		t.Fatalf("expected terminated status after retry, got %q", deleted.Status)
	}
	observations, err = failing.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if _, live := findObservation(observations, instanceName); live {
		t.Fatalf("expected the retry to tear down instance %q", instanceName)
	}
}
