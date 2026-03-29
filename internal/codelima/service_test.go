package codelima

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeLima struct {
	baseTemplate []byte
	observations map[string]RuntimeObservation
	calls        []string
	invocations  []string
	shellCalls   []fakeShellCall
	copyCalls    []fakeCopyCall
	pullCalls    []fakeCopyCall
	guestRoots   map[string]string
	tempRoot     string
	createErr    error
	failCommand  string
	cloneStatus  string
	listCalls    int
	listErr      error
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

func newFakeLima() *fakeLima {
	tempRoot, _ := os.MkdirTemp("", "codelima-fake-lima-*")
	return &fakeLima{
		baseTemplate: []byte("arch: aarch64\nimages: []\ncpus: 1\nmemory: 1GiB\ndisk: 10GiB\nmounts: []\n"),
		observations: map[string]RuntimeObservation{},
		calls:        []string{},
		invocations:  []string{},
		shellCalls:   []fakeShellCall{},
		copyCalls:    []fakeCopyCall{},
		pullCalls:    []fakeCopyCall{},
		guestRoots:   map[string]string{},
		tempRoot:     tempRoot,
		cloneStatus:  "stopped",
	}
}

func (f *fakeLima) BaseTemplate(_ context.Context, project Project, nodeCommands LimaCommandTemplates, locator string) ([]byte, error) {
	f.calls = append(f.calls, "template")
	commands, err := resolveConfiguredLimaCommands("limactl", defaultLimaCommandTemplates(), project, nodeCommands, limaCommandTemplateCopy, map[string]string{
		"locator": shellQuote(locator),
	})
	if err != nil {
		return nil, err
	}
	for _, command := range commands {
		f.invocations = append(f.invocations, "template:"+command)
	}
	return append([]byte(nil), f.baseTemplate...), nil
}

func (f *fakeLima) List(_ context.Context) ([]RuntimeObservation, error) {
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

func (f *fakeLima) Create(_ context.Context, project Project, node Node, templatePath string) error {
	f.calls = append(f.calls, "create:"+node.LimaInstanceName)
	commands, err := resolveConfiguredLimaCommands("limactl", defaultLimaCommandTemplates(), project, node.LimaCommands, limaCommandCreate, map[string]string{
		"instance_name": shellQuote(node.LimaInstanceName),
		"template_path": shellQuote(templatePath),
	})
	if err != nil {
		return err
	}
	for _, command := range commands {
		f.invocations = append(f.invocations, "create:"+command)
	}
	if f.createErr != nil {
		return f.createErr
	}
	f.observations[node.LimaInstanceName] = RuntimeObservation{Name: node.LimaInstanceName, Exists: true, Status: "stopped", Dir: "/fake/" + node.LimaInstanceName}
	return nil
}

func (f *fakeLima) Start(_ context.Context, project Project, node Node) error {
	f.calls = append(f.calls, "start:"+node.LimaInstanceName)
	commands, err := resolveConfiguredLimaCommands("limactl", defaultLimaCommandTemplates(), project, node.LimaCommands, limaCommandStart, map[string]string{
		"instance_name": shellQuote(node.LimaInstanceName),
	})
	if err != nil {
		return err
	}
	for _, command := range commands {
		f.invocations = append(f.invocations, "start:"+command)
	}
	observation := f.observations[node.LimaInstanceName]
	observation.Status = "running"
	observation.Exists = true
	observation.Name = node.LimaInstanceName
	f.observations[node.LimaInstanceName] = observation
	return nil
}

func (f *fakeLima) Stop(_ context.Context, project Project, node Node) error {
	f.calls = append(f.calls, "stop:"+node.LimaInstanceName)
	commands, err := resolveConfiguredLimaCommands("limactl", defaultLimaCommandTemplates(), project, node.LimaCommands, limaCommandStop, map[string]string{
		"instance_name": shellQuote(node.LimaInstanceName),
	})
	if err != nil {
		return err
	}
	for _, command := range commands {
		f.invocations = append(f.invocations, "stop:"+command)
	}
	observation := f.observations[node.LimaInstanceName]
	observation.Status = "stopped"
	observation.Exists = true
	observation.Name = node.LimaInstanceName
	f.observations[node.LimaInstanceName] = observation
	return nil
}

func (f *fakeLima) Delete(_ context.Context, project Project, node Node) error {
	f.calls = append(f.calls, "delete:"+node.LimaInstanceName)
	commands, err := resolveConfiguredLimaCommands("limactl", defaultLimaCommandTemplates(), project, node.LimaCommands, limaCommandDelete, map[string]string{
		"instance_name": shellQuote(node.LimaInstanceName),
	})
	if err != nil {
		return err
	}
	for _, command := range commands {
		f.invocations = append(f.invocations, "delete:"+command)
	}
	delete(f.observations, node.LimaInstanceName)
	return nil
}

func (f *fakeLima) Clone(_ context.Context, project Project, sourceNode, targetNode Node) error {
	f.calls = append(f.calls, "clone:"+sourceNode.LimaInstanceName+"->"+targetNode.LimaInstanceName)
	commands, err := resolveConfiguredLimaCommands("limactl", defaultLimaCommandTemplates(), project, targetNode.LimaCommands, limaCommandClone, map[string]string{
		"source_instance": shellQuote(sourceNode.LimaInstanceName),
		"target_instance": shellQuote(targetNode.LimaInstanceName),
	})
	if err != nil {
		return err
	}
	for _, command := range commands {
		f.invocations = append(f.invocations, "clone:"+command)
	}
	status := f.cloneStatus
	if strings.TrimSpace(status) == "" {
		status = "stopped"
	}
	f.observations[targetNode.LimaInstanceName] = RuntimeObservation{Name: targetNode.LimaInstanceName, Exists: true, Status: status, Dir: "/fake/" + targetNode.LimaInstanceName}
	return nil
}

func (f *fakeLima) CopyToGuest(_ context.Context, project Project, node Node, sourcePath, targetPath string, recursive bool) error {
	f.calls = append(f.calls, "copy:"+node.LimaInstanceName+":"+sourcePath+"->"+targetPath)
	commands, err := resolveConfiguredLimaCommands("limactl", defaultLimaCommandTemplates(), project, node.LimaCommands, limaCommandCopy, map[string]string{
		"source_path":    shellQuote(sourcePath),
		"target_path":    shellQuote(targetPath),
		"instance_name":  shellQuote(node.LimaInstanceName),
		"copy_target":    shellQuote(node.LimaInstanceName + ":" + targetPath),
		"recursive_flag": shellFlagFragment("-r", recursive),
	})
	if err != nil {
		return err
	}
	for _, command := range commands {
		f.invocations = append(f.invocations, "copy:"+command)
	}
	f.copyCalls = append(f.copyCalls, fakeCopyCall{
		instanceName: node.LimaInstanceName,
		sourcePath:   sourcePath,
		targetPath:   targetPath,
		recursive:    recursive,
	})
	guestRoot := filepath.Join(f.tempRoot, node.LimaInstanceName)
	_ = os.RemoveAll(guestRoot)
	if err := copyTree(sourcePath, guestRoot); err != nil {
		return err
	}
	f.guestRoots[node.LimaInstanceName] = guestRoot
	return nil
}

func (f *fakeLima) CopyFromGuest(_ context.Context, project Project, node Node, sourcePath, targetPath string, recursive bool) error {
	f.calls = append(f.calls, "pull:"+node.LimaInstanceName+":"+sourcePath+"->"+targetPath)
	commands, err := resolveConfiguredLimaCommands("limactl", defaultLimaCommandTemplates(), project, node.LimaCommands, limaCommandCopyFromGuest, map[string]string{
		"source_path":    shellQuote(sourcePath),
		"target_path":    shellQuote(targetPath),
		"instance_name":  shellQuote(node.LimaInstanceName),
		"copy_source":    shellQuote(fmt.Sprintf("%s:%s", node.LimaInstanceName, guestCopySourcePath(sourcePath, recursive))),
		"recursive_flag": shellFlagFragment("-r", recursive),
	})
	if err != nil {
		return err
	}
	for _, command := range commands {
		f.invocations = append(f.invocations, "pull:"+command)
	}
	f.pullCalls = append(f.pullCalls, fakeCopyCall{
		instanceName: node.LimaInstanceName,
		sourcePath:   sourcePath,
		targetPath:   targetPath,
		recursive:    recursive,
	})
	guestRoot := f.guestRoots[node.LimaInstanceName]
	if guestRoot == "" {
		return errors.New("fake guest workspace missing")
	}
	return copyTree(guestRoot, targetPath)
}

func (f *fakeLima) Shell(_ context.Context, project Project, node Node, command []string, workdir string, interactive bool, _ ShellStreams) error {
	f.calls = append(f.calls, "shell:"+node.LimaInstanceName+":"+strings.Join(command, " "))
	workdirFlag := ""
	if workdir != "" {
		workdirFlag = prefixedShellFragment("--workdir", shellQuote(workdir))
	}
	resolved, err := resolveConfiguredLimaCommands("limactl", defaultLimaCommandTemplates(), project, node.LimaCommands, limaCommandShell, map[string]string{
		"instance_name": shellQuote(node.LimaInstanceName),
		"workdir":       shellQuote(workdir),
		"workdir_flag":  workdirFlag,
		"command_args":  shellCommandArgsFragment(command),
	})
	if err != nil {
		return err
	}
	for _, resolvedCommand := range resolved {
		f.invocations = append(f.invocations, "shell:"+resolvedCommand)
	}
	f.shellCalls = append(f.shellCalls, fakeShellCall{
		instanceName: node.LimaInstanceName,
		command:      append([]string(nil), command...),
		workdir:      workdir,
		interactive:  interactive,
	})
	if f.failCommand != "" && strings.Contains(strings.Join(command, " "), f.failCommand) {
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

func TestProjectAndEnvironmentConfigMetadataMutationsDoNotRequireLima(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	fake := service.lima.(*fakeLima)
	fake.listErr = errors.New("lima should not be queried for metadata-only mutations")

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
		t.Fatalf("expected metadata-only mutations to avoid lima.List, got %d calls", fake.listCalls)
	}
}

func TestNodeLifecycleDelegatesToLima(t *testing.T) {
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

	if !containsPrefix(service.lima.(*fakeLima).calls, "create:"+node.LimaInstanceName) {
		t.Fatalf("expected limactl create delegation")
	}
	if !containsSubstring(service.lima.(*fakeLima).invocations, "create:'limactl' create -y --name '"+node.LimaInstanceName+"' --cpus=2 --memory=4 --disk=20 ") {
		t.Fatalf("expected create invocation to include resource flags, invocations = %v", service.lima.(*fakeLima).invocations)
	}

	templateBytes, err := os.ReadFile(node.GeneratedTemplatePath)
	if err != nil {
		t.Fatalf("ReadFile(template) error = %v", err)
	}

	if strings.Contains(string(templateBytes), "location: "+workspace) {
		t.Fatalf("expected generated template to avoid mounting host workspace, got %s", string(templateBytes))
	}
	for _, unexpected := range []string{"\ncpus:", "\nmemory:", "\ndisk:"} {
		if strings.Contains(string(templateBytes), unexpected) {
			t.Fatalf("expected generated template to omit VM resource keys, got %s", string(templateBytes))
		}
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

	if !containsCall(service.lima.(*fakeLima).calls, "copy:"+node.LimaInstanceName+":"+workspace+"->"+workspace) {
		t.Fatalf("expected workspace copy delegation, calls = %v", service.lima.(*fakeLima).calls)
	}
	if node.WorkspaceSeedSnapshotID == "" {
		t.Fatalf("expected copy-mode node start to record a workspace seed snapshot")
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

func TestNodeLifecycleMountedWorkspaceSkipsCopyAndAddsWritableMount(t *testing.T) {
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
		Slug:          "mounted-node",
		WorkspaceMode: WorkspaceModeMounted,
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
	if node.GuestWorkspacePath != "" {
		t.Fatalf("expected mounted node guest workspace path to be empty, got %q", node.GuestWorkspacePath)
	}

	templateBytes, err := os.ReadFile(node.GeneratedTemplatePath)
	if err != nil {
		t.Fatalf("ReadFile(template) error = %v", err)
	}

	templateText := string(templateBytes)
	if !strings.Contains(templateText, "location: "+workspace) {
		t.Fatalf("expected generated template to mount host workspace, got %s", templateText)
	}
	if !strings.Contains(templateText, "mountPoint: "+workspace) {
		t.Fatalf("expected generated template to mount at the host path, got %s", templateText)
	}
	if !strings.Contains(templateText, "writable: true") {
		t.Fatalf("expected generated template to use a writable mount, got %s", templateText)
	}

	node, err = service.NodeStart(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	if !node.WorkspaceSeeded {
		t.Fatalf("expected mounted node start to mark workspace prepared")
	}
	if containsSubstring(service.lima.(*fakeLima).calls, "copy:"+node.LimaInstanceName+":") {
		t.Fatalf("expected mounted node to skip workspace copy, calls = %v", service.lima.(*fakeLima).calls)
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

	project.LimaCommands.WorkspaceSeedPrepare = []string{"echo preparing {{instance_name}} {{target_path}} {{target_parent}}"}
	if err := service.store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject(custom workspace seed prepare command) error = %v", err)
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "root-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	if _, err := service.NodeStart(ctx, node.ID); err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	if len(service.lima.(*fakeLima).shellCalls) == 0 {
		t.Fatalf("expected workspace seed prepare command to run")
	}

	firstCall := service.lima.(*fakeLima).shellCalls[0]
	if firstCall.instanceName != node.LimaInstanceName {
		t.Fatalf("expected workspace seed prepare to target %q, got %q", node.LimaInstanceName, firstCall.instanceName)
	}

	expected := "echo preparing " + shellQuote(node.LimaInstanceName) + " " + shellQuote(workspace) + " " + shellQuote(filepath.Dir(workspace))
	if got := strings.Join(firstCall.command, " "); got != "sh -lc "+expected {
		t.Fatalf("expected workspace seed prepare command %q, got %q", "sh -lc "+expected, got)
	}
}

func TestNodeCreateCleansUpPartialMetadataWhenLimaCreateFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	service.lima.(*fakeLima).createErr = errors.New("forced create failure")
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
		t.Fatalf("expected NodeCreate() to fail when Lima create fails")
	}

	entries, err := os.ReadDir(filepath.Join(service.cfg.MetadataRoot, "nodes"))
	if err != nil {
		t.Fatalf("ReadDir(nodes) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected failed node create to remove partial metadata, found %d entries", len(entries))
	}
}

func TestProjectScopedLimaCommandsApplyToNodeLifecycle(t *testing.T) {
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

	project.LimaCommands = LimaCommandTemplates{
		Create:       []string{"{{binary}} create --name {{instance_name}} --cpus=8 --memory=16 --disk=100 {{template_path}} --vm-type=vz"},
		Start:        []string{"{{binary}} start {{instance_name}} --set '.nestedVirtualization=true'"},
		Stop:         []string{"{{binary}} stop {{instance_name}} --preserve-state"},
		Delete:       []string{"{{binary}} delete {{instance_name}} --archive"},
		Clone:        []string{"{{binary}} clone {{source_instance}} {{target_instance}} --vm-type=vz"},
		Copy:         []string{"{{binary}} copy{{recursive_flag}} {{source_path}} {{copy_target}} --checksum"},
		Shell:        []string{"{{binary}} shell{{workdir_flag}} {{instance_name}}{{command_args}} --debug"},
		TemplateCopy: []string{"{{binary}} template copy --fill {{locator}} -"},
	}
	if err := service.store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject(custom commands) error = %v", err)
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: project.ID,
		Slug:    "root-node",
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

	invocations := service.lima.(*fakeLima).invocations
	for _, expected := range []string{
		"template:'limactl' template copy --fill 'template:default' -",
		"create:'limactl' create --name ",
		"--cpus=8 --memory=16 --disk=100",
		"--vm-type=vz",
		"start:'limactl' start '" + node.LimaInstanceName + "' --set '.nestedVirtualization=true'",
		"stop:'limactl' stop '" + node.LimaInstanceName + "' --preserve-state",
		"clone:'limactl' clone '" + node.LimaInstanceName + "' '" + childNode.LimaInstanceName + "'",
		"copy:'limactl' copy -r ",
		"--checksum",
		"shell:'limactl' shell --workdir ",
		"--debug",
		"delete:'limactl' delete '" + childNode.LimaInstanceName + "' --archive",
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
	if err := os.WriteFile(filepath.Join(service.cfg.MetadataRoot, "nodes", "partial-node", "instance.lima.yaml"), []byte("arch: aarch64\n"), 0o644); err != nil {
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

	nodes, err := service.NodeList(false)
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

	tree, err := service.ProjectTree("", false)
	if err != nil {
		t.Fatalf("ProjectTree() error = %v", err)
	}
	if len(tree) != 1 || len(tree[0].Nodes) != 1 || tree[0].Nodes[0].ID != node.ID {
		t.Fatalf("expected project tree to include only the healthy node, got %#v", tree)
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
	if err := os.WriteFile(filepath.Join(partialDir, "instance.lima.yaml"), []byte("arch: aarch64\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(template) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(partialDir, "lima-instance.ref"), []byte("project-node-12345678\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(instance ref) error = %v", err)
	}

	report, err := service.Doctor(ctx)
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
	if err := os.WriteFile(filepath.Join(partialDir, "instance.lima.yaml"), []byte("arch: aarch64\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(template) error = %v", err)
	}
	instanceName := "partial-project-partial-node-12345678"
	if err := os.WriteFile(filepath.Join(partialDir, "lima-instance.ref"), []byte(instanceName+"\n"), 0o644); err != nil {
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

	nodes, err := service.NodeList(false)
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

	if childNode.GuestWorkspacePath != workspace {
		t.Fatalf("expected child node guest workspace path %q, got %q", workspace, childNode.GuestWorkspacePath)
	}

	if childNode.WorkspaceSeeded {
		t.Fatalf("expected unstarted source clone to remain unseeded")
	}

	if !containsPrefix(service.lima.(*fakeLima).calls, "clone:"+node.LimaInstanceName+"->"+childNode.LimaInstanceName) {
		t.Fatalf("expected limactl clone delegation, calls = %v", service.lima.(*fakeLima).calls)
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
	if childNode.GuestWorkspacePath != "" {
		t.Fatalf("expected cloned mounted node to keep empty guest workspace path, got %q", childNode.GuestWorkspacePath)
	}
}

func TestNodeClonePreservesWorkspaceSeedSnapshotID(t *testing.T) {
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

	childNode, err := service.NodeClone(ctx, NodeCloneInput{
		SourceNode: node.ID,
		NodeSlug:   "child-node",
	})
	if err != nil {
		t.Fatalf("NodeClone() error = %v", err)
	}

	if childNode.WorkspaceSeedSnapshotID != node.WorkspaceSeedSnapshotID {
		t.Fatalf("expected cloned node to preserve workspace seed snapshot id %q, got %q", node.WorkspaceSeedSnapshotID, childNode.WorkspaceSeedSnapshotID)
	}
}

func TestNodeSyncPreviewSummarizesGuestChangesWithoutMutatingHost(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")
	writeFile(t, filepath.Join(workspace, "keep.txt"), "keep\n")

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

	fake := service.lima.(*fakeLima)
	guestRoot := fake.guestRoots[node.LimaInstanceName]
	writeFile(t, filepath.Join(guestRoot, "README.md"), "hello\nfrom guest\n")
	writeFile(t, filepath.Join(guestRoot, "notes.txt"), "guest only\n")
	if err := os.Remove(filepath.Join(guestRoot, "keep.txt")); err != nil {
		t.Fatalf("Remove(guest keep.txt) error = %v", err)
	}

	result, err := service.NodeSync(ctx, node.ID, false)
	if err != nil {
		t.Fatalf("NodeSync(preview) error = %v", err)
	}

	if !result.DryRun || result.Applied {
		t.Fatalf("expected preview sync result, got %#v", result)
	}
	if !result.Changed {
		t.Fatalf("expected preview sync to report changes")
	}
	if result.DiffSummary.FilesChanged != 3 || result.DiffSummary.AddedFiles != 1 || result.DiffSummary.ModifiedFiles != 1 || result.DiffSummary.DeletedFiles != 1 {
		t.Fatalf("unexpected diff summary: %#v", result.DiffSummary)
	}
	if !containsSubstring(fake.calls, "pull:"+node.LimaInstanceName+":"+workspace+"->") {
		t.Fatalf("expected guest pull delegation, calls = %v", fake.calls)
	}

	content, err := os.ReadFile(filepath.Join(workspace, "README.md"))
	if err != nil {
		t.Fatalf("ReadFile(host README) error = %v", err)
	}
	if string(content) != "hello\n" {
		t.Fatalf("expected preview to leave host workspace unchanged, got %q", string(content))
	}
	if exists(filepath.Join(workspace, "notes.txt")) {
		t.Fatalf("expected preview to avoid creating host-only files")
	}
}

func TestNodeSyncApplyPromotesGuestChangesAndRefreshesBaseline(t *testing.T) {
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

	fake := service.lima.(*fakeLima)
	guestRoot := fake.guestRoots[node.LimaInstanceName]
	writeFile(t, filepath.Join(guestRoot, "README.md"), "hello\nsynced\n")
	writeFile(t, filepath.Join(guestRoot, "new.txt"), "new\n")
	originalSnapshotID := node.WorkspaceSeedSnapshotID

	result, err := service.NodeSync(ctx, node.ID, true)
	if err != nil {
		t.Fatalf("NodeSync(apply) error = %v", err)
	}

	if result.DryRun || !result.Applied || !result.Changed {
		t.Fatalf("expected applied sync result, got %#v", result)
	}
	if result.DiffSummary.FilesChanged != 2 {
		t.Fatalf("expected two changed files, got %#v", result.DiffSummary)
	}

	content, err := os.ReadFile(filepath.Join(workspace, "README.md"))
	if err != nil {
		t.Fatalf("ReadFile(host README) error = %v", err)
	}
	if string(content) != "hello\nsynced\n" {
		t.Fatalf("expected host workspace to receive guest changes, got %q", string(content))
	}
	if _, err := os.Stat(filepath.Join(workspace, "new.txt")); err != nil {
		t.Fatalf("expected host workspace to receive new file: %v", err)
	}

	updatedNode, err := service.NodeShow(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeShow(updated node) error = %v", err)
	}
	if updatedNode.WorkspaceSeedSnapshotID == "" || updatedNode.WorkspaceSeedSnapshotID == originalSnapshotID {
		t.Fatalf("expected sync apply to refresh workspace seed snapshot id, got %q", updatedNode.WorkspaceSeedSnapshotID)
	}
}

func TestNodeSyncApplyDetectsHostConflictsFromSeedBaseline(t *testing.T) {
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

	fake := service.lima.(*fakeLima)
	guestRoot := fake.guestRoots[node.LimaInstanceName]
	writeFile(t, filepath.Join(guestRoot, "README.md"), "guest change\n")
	writeFile(t, filepath.Join(workspace, "README.md"), "host change\n")

	if _, err := service.NodeSync(ctx, node.ID, true); err == nil {
		t.Fatalf("expected sync apply to detect host conflict")
	}

	content, err := os.ReadFile(filepath.Join(workspace, "README.md"))
	if err != nil {
		t.Fatalf("ReadFile(host README) error = %v", err)
	}
	if string(content) != "host change\n" {
		t.Fatalf("expected host workspace to remain unchanged after conflict, got %q", string(content))
	}
}

func TestNodeSyncRejectsMountedNodes(t *testing.T) {
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
		Slug:          "mounted-node",
		WorkspaceMode: WorkspaceModeMounted,
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	if _, err := service.NodeSync(ctx, node.ID, false); err == nil {
		t.Fatalf("expected mounted node sync to be rejected")
	}
}

func TestNodeSyncRequiresRunningSeededCopyModeNode(t *testing.T) {
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

	if _, err := service.NodeSync(ctx, node.ID, false); err == nil {
		t.Fatalf("expected unstarted node sync to be rejected")
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

func TestDispatchProjectCreateAndUpdateEnvironmentCommandFlags(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	if _, err := dispatch(ctx, service, []string{
		"project", "create",
		"--slug", "root",
		"--workspace", workspace,
		"--env-command", "./script/setup",
		"--env-command", "direnv allow",
	}); err != nil {
		t.Fatalf("dispatch(project create --env-command) error = %v", err)
	}

	project, err := service.ProjectShow("root")
	if err != nil {
		t.Fatalf("ProjectShow(root) error = %v", err)
	}
	if got := strings.Join(project.LimaCommands.Bootstrap, "|"); got != "./script/setup|direnv allow" {
		t.Fatalf("expected bootstrap commands from create, got %v", project.LimaCommands.Bootstrap)
	}

	if _, err := dispatch(ctx, service, []string{
		"project", "update", "root",
		"--env-command", "mise install",
		"--env-command", "make init",
	}); err != nil {
		t.Fatalf("dispatch(project update --env-command) error = %v", err)
	}

	project, err = service.ProjectShow("root")
	if err != nil {
		t.Fatalf("ProjectShow(updated root) error = %v", err)
	}
	if got := strings.Join(project.LimaCommands.Bootstrap, "|"); got != "mise install|make init" {
		t.Fatalf("expected bootstrap commands from update, got %v", project.LimaCommands.Bootstrap)
	}

	if _, err := dispatch(ctx, service, []string{
		"project", "update", "root",
		"--clear-env-commands",
	}); err != nil {
		t.Fatalf("dispatch(project update --clear-env-commands) error = %v", err)
	}

	project, err = service.ProjectShow("root")
	if err != nil {
		t.Fatalf("ProjectShow(cleared root) error = %v", err)
	}
	if len(project.LimaCommands.Bootstrap) != 0 {
		t.Fatalf("expected cleared bootstrap commands, got %v", project.LimaCommands.Bootstrap)
	}
}

func TestNodeCloneInheritsSourceNodeLimaCommandsByDefault(t *testing.T) {
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
		LimaCommands: LimaCommandTemplates{
			Clone: []string{"{{binary}} clone {{source_instance}} {{target_instance}} --set '.nestedVirtualization=true'"},
			Start: []string{"{{binary}} start {{instance_name}} --vm-type=vz"},
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

	if strings.Join(childNode.LimaCommands.Clone, "|") != strings.Join(node.LimaCommands.Clone, "|") {
		t.Fatalf("expected cloned node to inherit clone command override %q, got %q", node.LimaCommands.Clone, childNode.LimaCommands.Clone)
	}
	if strings.Join(childNode.LimaCommands.Start, "|") != strings.Join(node.LimaCommands.Start, "|") {
		t.Fatalf("expected cloned node to inherit start command override %q, got %q", node.LimaCommands.Start, childNode.LimaCommands.Start)
	}
	if !containsSubstring(service.lima.(*fakeLima).invocations, "--set '.nestedVirtualization=true'") {
		t.Fatalf("expected clone invocation to use inherited node-specific clone command override, invocations = %v", service.lima.(*fakeLima).invocations)
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

	configs, err := service.EnvironmentConfigList(false)
	if err != nil {
		t.Fatalf("EnvironmentConfigList() error = %v", err)
	}

	assertEnvironmentConfigCommands(t, configs, "codex",
		"sudo snap install node --classic",
		"sudo npm install -g @openai/codex",
	)
	assertEnvironmentConfigCommands(t, configs, "claude-code",
		"curl -fsSL https://claude.ai/install.sh | bash",
	)

	if _, err := service.EnvironmentConfigUpdate("codex", EnvironmentConfigUpdateInput{
		BootstrapCommands: []string{"echo customized"},
	}); err != nil {
		t.Fatalf("EnvironmentConfigUpdate(codex) error = %v", err)
	}

	if err := service.EnsureReady(false); err != nil {
		t.Fatalf("EnsureReady(false) error = %v", err)
	}

	config, err := service.EnvironmentConfigShow("codex")
	if err != nil {
		t.Fatalf("EnvironmentConfigShow(codex) error = %v", err)
	}
	if got := strings.Join(config.BootstrapCommands, "|"); got != "echo customized" {
		t.Fatalf("expected customized codex commands to persist, got %q", got)
	}
}

func TestDeletedBuiltInEnvironmentConfigIsNotRecreated(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)

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

func TestDispatchEnvironmentConfigCommandsAndProjectEnvConfigFlags(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	if _, err := dispatch(ctx, service, []string{
		"environment", "create",
		"--slug", "shared-dev",
		"--env-command", "./script/setup",
		"--env-command", "direnv allow",
	}); err != nil {
		t.Fatalf("dispatch(environment create) error = %v", err)
	}

	config, err := service.EnvironmentConfigShow("shared-dev")
	if err != nil {
		t.Fatalf("EnvironmentConfigShow(shared-dev) error = %v", err)
	}
	if got := strings.Join(config.BootstrapCommands, "|"); got != "./script/setup|direnv allow" {
		t.Fatalf("expected created environment config commands, got %q", got)
	}

	if _, err := dispatch(ctx, service, []string{
		"project", "create",
		"--slug", "root",
		"--workspace", workspace,
		"--env-config", "shared-dev",
	}); err != nil {
		t.Fatalf("dispatch(project create --env-config) error = %v", err)
	}

	project, err := service.ProjectShow("root")
	if err != nil {
		t.Fatalf("ProjectShow(root) error = %v", err)
	}
	if got := strings.Join(project.EnvironmentConfigs, "|"); got != "shared-dev" {
		t.Fatalf("expected assigned environment config refs, got %q", got)
	}

	if _, err := dispatch(ctx, service, []string{
		"environment", "update", "shared-dev",
		"--env-command", "mise install",
	}); err != nil {
		t.Fatalf("dispatch(environment update) error = %v", err)
	}

	config, err = service.EnvironmentConfigShow("shared-dev")
	if err != nil {
		t.Fatalf("EnvironmentConfigShow(updated shared-dev) error = %v", err)
	}
	if got := strings.Join(config.BootstrapCommands, "|"); got != "mise install" {
		t.Fatalf("expected updated environment config commands, got %q", got)
	}

	if _, err := dispatch(ctx, service, []string{
		"project", "update", "root",
		"--clear-env-configs",
	}); err != nil {
		t.Fatalf("dispatch(project update --clear-env-configs) error = %v", err)
	}

	project, err = service.ProjectShow("root")
	if err != nil {
		t.Fatalf("ProjectShow(cleared root) error = %v", err)
	}
	if len(project.EnvironmentConfigs) != 0 {
		t.Fatalf("expected cleared environment config refs, got %v", project.EnvironmentConfigs)
	}

	if _, err := dispatch(ctx, service, []string{
		"environment", "delete", "shared-dev",
	}); err != nil {
		t.Fatalf("dispatch(environment delete) error = %v", err)
	}

	configs, err := service.EnvironmentConfigList(false)
	if err != nil {
		t.Fatalf("EnvironmentConfigList(after delete) error = %v", err)
	}
	if containsEnvironmentConfigSlug(configs, "shared-dev") {
		t.Fatalf("expected deleted environment config to disappear from list, got %#v", configs)
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
	if !strings.Contains(got, `exec "${SHELL:-/bin/bash}" -l`) {
		t.Fatalf("expected interactive shell command to exec the user's login shell, got %q", got)
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

	if len(service.lima.(*fakeLima).shellCalls) != 0 {
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

	lastCall := service.lima.(*fakeLima).shellCalls[len(service.lima.(*fakeLima).shellCalls)-1]
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

	if !containsCall(service.lima.(*fakeLima).calls, "stop:"+parentNode.LimaInstanceName) {
		t.Fatalf("expected running source node to be stopped before clone, calls = %v", service.lima.(*fakeLima).calls)
	}

	if !containsCall(service.lima.(*fakeLima).calls, "clone:"+parentNode.LimaInstanceName+"->"+childNode.LimaInstanceName) {
		t.Fatalf("expected limactl clone delegation, calls = %v", service.lima.(*fakeLima).calls)
	}

	if !containsCall(service.lima.(*fakeLima).calls, "start:"+parentNode.LimaInstanceName) {
		t.Fatalf("expected running source node to be restarted after clone, calls = %v", service.lima.(*fakeLima).calls)
	}

	callsBeforeChildStart := len(service.lima.(*fakeLima).calls)
	childNode, err = service.NodeStart(ctx, childNode.ID)
	if err != nil {
		t.Fatalf("NodeStart(child) error = %v", err)
	}

	newCalls := append([]string(nil), service.lima.(*fakeLima).calls[callsBeforeChildStart:]...)
	if containsCall(newCalls, "copy:"+childNode.LimaInstanceName+":"+workspace+"->"+workspace) {
		t.Fatalf("expected cloned node start to avoid reseeding the guest workspace, calls = %v", newCalls)
	}

	if containsCall(newCalls, "shell:"+childNode.LimaInstanceName+":sh -lc cd "+quoted(workspace)+" && ./script/setup") {
		t.Fatalf("expected cloned node start to avoid rerunning setup, calls = %v", newCalls)
	}
}

func TestNodeCloneStopsCloneWhenProviderLeavesItRunning(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	service.lima.(*fakeLima).cloneStatus = "running"
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

	if !containsCall(service.lima.(*fakeLima).calls, "stop:"+childNode.LimaInstanceName) {
		t.Fatalf("expected running clone instance to be stopped, calls = %v", service.lima.(*fakeLima).calls)
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
