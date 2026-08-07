package codelima

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	shellCalls   []fakeShellCall
	copyCalls    []fakeCopyCall
	createErr    error
	cloneErr     error
	failCommand  string
	cloneStatus  ObservationStatus
	listCalls    int
	listErr      error
	// staleObservations stands in for a runtime whose List answers from a cache
	// that has drifted. When it is set, List serves it and ListUncached still
	// serves the real state, which is exactly the split Service relies on for
	// decisions that gate a runtime mutation.
	staleObservations map[string]RuntimeObservation
	uncachedListCalls int
	// startContexts records the state of the context each Start was handed, so
	// tests can assert that a mutation which must survive caller cancellation
	// was not handed the caller's context.
	startContexts []fakeContextState
	createGate    *fakeSandboxGate
	startGate     *fakeSandboxGate
	stopGate      *fakeSandboxGate
	deleteGate    *fakeSandboxGate
	cloneGate     *fakeSandboxGate
}

type fakeSandboxGate struct {
	entered chan string
	release chan struct{}
}

// fakeContextState captures what a sandbox call could observe about the context
// it was handed: whether it was already dead, and whether it was bounded at all.
type fakeContextState struct {
	err     error
	bounded bool
}

func fakeContextBounded(ctx context.Context) bool {
	_, ok := ctx.Deadline()
	return ok
}

type fakeShellCall struct {
	instanceName string
	command      []string
	workdir      string
	interactive  bool
	// budget is the deadline the caller threaded through the context, or zero
	// when it supplied none. It is how tests observe guest-command timeout
	// classes without waiting one out.
	budget time.Duration
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

// recordCall appends a bookkeeping record under the fake's mutex so tests may
// exercise Service reads and mutations concurrently under -race.
func (f *fakeSandbox) recordCall(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeSandbox) Version(context.Context) (string, error) {
	return requiredLimaVersion, nil
}

func (f *fakeSandbox) ResolveCommands(node Node, kind runtimeCommandKind, values map[string]string) ([]string, error) {
	return resolveConfiguredRuntimeCommands("", RuntimeCommandTemplates{}, node.RuntimeCommands, kind, values)
}

func (f *fakeSandbox) List(_ context.Context) ([]RuntimeObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	source := f.observations
	if f.staleObservations != nil {
		source = f.staleObservations
	}
	observations := make([]RuntimeObservation, 0, len(source))
	for _, observation := range source {
		observations = append(observations, observation)
	}
	return observations, nil
}

func (f *fakeSandbox) ListUncached(_ context.Context) ([]RuntimeObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	f.uncachedListCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	observations := make([]RuntimeObservation, 0, len(f.observations))
	for _, observation := range f.observations {
		observations = append(observations, observation)
	}
	return observations, nil
}

func (f *fakeSandbox) Create(_ context.Context, node Node) error {
	f.recordCall("create " + node.SandboxName)
	if f.createErr != nil {
		return f.createErr
	}
	f.createGate.block(node.SandboxName)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observations[node.SandboxName] = RuntimeObservation{Name: node.SandboxName, Exists: true, Status: "stopped", Dir: "/fake/" + node.SandboxName}
	return nil
}

func (f *fakeSandbox) Start(ctx context.Context, node Node) error {
	f.recordCall("start " + node.SandboxName)
	f.startGate.block(node.SandboxName)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startContexts = append(f.startContexts, fakeContextState{err: ctx.Err(), bounded: fakeContextBounded(ctx)})
	observation := f.observations[node.SandboxName]
	observation.Status = "running"
	observation.Exists = true
	observation.Name = node.SandboxName
	f.observations[node.SandboxName] = observation
	return nil
}

func (f *fakeSandbox) Stop(_ context.Context, node Node) error {
	f.recordCall("stop " + node.SandboxName)
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

func (f *fakeSandbox) Delete(_ context.Context, node Node) error {
	f.recordCall("delete " + node.SandboxName)
	f.deleteGate.block(node.SandboxName)
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.observations, node.SandboxName)
	return nil
}

func (f *fakeSandbox) Clone(_ context.Context, sourceNode, targetNode Node) error {
	f.recordCall("clone " + sourceNode.SandboxName + " " + targetNode.SandboxName)
	if f.cloneErr != nil {
		return f.cloneErr
	}
	f.cloneGate.block(targetNode.SandboxName)
	f.mu.Lock()
	defer f.mu.Unlock()
	status := f.cloneStatus
	if strings.TrimSpace(string(status)) == "" {
		status = ObservationStopped
	}
	f.observations[targetNode.SandboxName] = RuntimeObservation{Name: targetNode.SandboxName, Exists: true, Status: status, Dir: "/fake/" + targetNode.SandboxName}
	return nil
}

func TestNodeCloneFailureCleansRuntimeReferenceAndMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, workspace := newTestService(t)
	source, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "clone-source"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	fake := service.sandbox.(*fakeSandbox)
	fake.cloneErr = &runtimeMutationError{error: errors.New("forced clone failure")}
	if _, err := service.NodeClone(ctx, NodeCloneInput{SourceNode: source.ID, NodeSlug: "clone-target"}); err == nil {
		t.Fatal("NodeClone() unexpectedly succeeded")
	}
	entries, err := os.ReadDir(filepath.Join(service.cfg.MetadataRoot, "nodes"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != source.ID {
		t.Fatalf("node metadata after clone rollback = %#v", entries)
	}
	if exists(service.store.nodeInstanceIndexPath("clone-target")) {
		t.Fatal("clone rollback left the target instance index")
	}
	if !containsCall(fake.calls, "delete clone-target") {
		t.Fatalf("clone rollback did not attempt runtime deletion: %v", fake.calls)
	}
}

func (f *fakeSandbox) CopyToGuest(_ context.Context, node Node, sourcePath, targetPath string, recursive bool) error {
	f.recordCall("copy " + node.SandboxName + " " + sourcePath + " " + targetPath)
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

func (f *fakeSandbox) Shell(ctx context.Context, node Node, command []string, workdir string, interactive bool, _ ShellStreams) error {
	f.recordCall("shell " + node.SandboxName + " " + strings.Join(command, " "))
	var budget time.Duration
	if deadline, ok := ctx.Deadline(); ok {
		budget = time.Until(deadline)
	}
	f.mu.Lock()
	f.shellCalls = append(f.shellCalls, fakeShellCall{
		instanceName: node.SandboxName,
		command:      append([]string(nil), command...),
		workdir:      workdir,
		interactive:  interactive,
		budget:       budget,
	})
	failCommand := f.failCommand
	f.mu.Unlock()
	if failCommand != "" && strings.Contains(strings.Join(command, " "), failCommand) {
		return errors.New("forced shell failure")
	}
	return nil
}

func TestEnvironmentConfigMetadataMutationsDoNotRequireLima(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)

	fake := service.sandbox.(*fakeSandbox)
	fake.listErr = errors.New("sandbox should not be queried for metadata-only mutations")

	config, err := service.EnvironmentConfigCreate(context.Background(), EnvironmentConfigCreateInput{
		Slug:              "shared-dev",
		BootstrapCommands: []string{"./script/setup"},
	})
	if err != nil {
		t.Fatalf("EnvironmentConfigCreate() error = %v", err)
	}

	if _, err := service.EnvironmentConfigUpdate(context.Background(), config.ID, EnvironmentConfigUpdateInput{
		BootstrapCommands: []string{"./script/setup", "direnv allow"},
	}); err != nil {
		t.Fatalf("EnvironmentConfigUpdate() error = %v", err)
	}

	if _, err := service.EnvironmentConfigDelete(context.Background(), config.ID); err != nil {
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

	if _, err := service.ConfigurationCreate(context.Background(), ConfigurationCreateInput{
		Slug:              "setup",
		Environments:      []string{},
		BootstrapCommands: []string{"./script/setup"},
	}); err != nil {
		t.Fatalf("ConfigurationCreate() error = %v", err)
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Configuration: "setup",
		Directory:     workspace,
		Slug:          "root-node",
		WorkspaceMode: WorkspaceModeCopy,
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	if node.SandboxName != "root-node" {
		t.Fatalf("expected sandbox instance name to match node slug, got %q", node.SandboxName)
	}

	if !containsCall(service.sandbox.(*fakeSandbox).calls, "create "+node.SandboxName) {
		t.Fatalf("expected sandbox create delegation")
	}

	node, err = service.NodeStart(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	if node.Status != NodeStatusRunning {
		t.Fatalf("expected running status, got %q", node.Status)
	}

	// The workdir is shell-quoted, not Go-quoted: %q leaves $ and backticks live.
	if !containsCall(service.sandbox.(*fakeSandbox).calls, "shell "+node.SandboxName+" sh -lc cd "+shellQuote(workspace)+" && ./script/setup") {
		t.Fatalf("expected setup command delegation, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}

	if !containsCall(service.sandbox.(*fakeSandbox).calls, "copy "+node.SandboxName+" "+workspace+" "+workspace) {
		t.Fatalf("expected workspace copy delegation, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}

	if !containsSubstring(service.sandbox.(*fakeSandbox).calls, "codex --version") {
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

func TestNodeStartRejectsHostPortOwnedByRunningNode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, workspace := newTestService(t)
	first, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "first-port-node", Ports: []string{"18080:8080"}})
	if err != nil {
		t.Fatalf("NodeCreate(first) error = %v", err)
	}
	second, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "second-port-node", Ports: []string{"18080:3000"}})
	if err != nil {
		t.Fatalf("NodeCreate(second) error = %v", err)
	}
	if _, err := service.NodeStart(ctx, first.ID); err != nil {
		t.Fatalf("NodeStart(first) error = %v", err)
	}
	_, err = service.NodeStart(ctx, second.ID)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Category != CategoryPreconditionFailed {
		t.Fatalf("NodeStart(second) error = %#v", err)
	}
	if appErr.Fields["host_port"] != "18080" {
		t.Fatalf("port conflict fields = %#v", appErr.Fields)
	}
	if containsCall(service.sandbox.(*fakeSandbox).calls, "start "+second.SandboxName) {
		t.Fatalf("conflicting node reached runtime start: %v", service.sandbox.(*fakeSandbox).calls)
	}
}

func TestNodeLifecycleDefaultsToMountedWorkspaceAndSkipsCopy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory: workspace,
		Slug:      "mounted-node",
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

	node, err = service.NodeStart(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	if !node.WorkspaceSeeded {
		t.Fatalf("expected mounted node start to mark workspace prepared")
	}
	if containsPrefix(service.sandbox.(*fakeSandbox).calls, "copy "+node.SandboxName+" ") {
		t.Fatalf("expected mounted node to skip workspace copy, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}
}

func TestNodeStartClearsCreatedLifecycleStateFromPersistedNodeMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory: workspace,
		Slug:      "root-node",
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

// TestNodeStartGivesBootstrapCommandsTheLongBudget pins the guest-command
// timeout classes: bootstrap and agent-install commands run under the long
// budget, and the quick probes around them keep the short one. The blanket
// ten-minute cap this replaces killed slow installs mid-flight and recorded the
// node Failed.
func TestNodeStartGivesBootstrapCommandsTheLongBudget(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "budget-node"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	bootstrap, err := service.store.LoadBootstrapState(node.ID)
	if err != nil {
		t.Fatalf("LoadBootstrapState() error = %v", err)
	}
	bootstrap.InstallCommands = []string{"install-the-agent"}
	bootstrap.BootstrapCommands = nil
	bootstrap.ValidationCommand = "probe-the-agent"
	if err := service.store.SaveNode(node, bootstrap); err != nil {
		t.Fatalf("SaveNode() error = %v", err)
	}

	if _, err := service.NodeStart(ctx, node.ID); err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	fake := service.sandbox.(*fakeSandbox)
	fake.mu.Lock()
	calls := append([]fakeShellCall(nil), fake.shellCalls...)
	fake.mu.Unlock()

	var install, probe *fakeShellCall
	for index := range calls {
		joined := strings.Join(calls[index].command, " ")
		switch {
		case strings.Contains(joined, "install-the-agent"):
			install = &calls[index]
		case strings.Contains(joined, "probe-the-agent"):
			probe = &calls[index]
		}
	}
	if install == nil || probe == nil {
		t.Fatalf("expected both a bootstrap and a validation shell call, got %#v", calls)
	}
	// Bounded, and bounded by the long class rather than the short one.
	if install.budget <= defaultGuestCommandTimeout || install.budget > bootstrapGuestCommandTimeout {
		t.Fatalf("bootstrap command budget = %s, want (%s, %s]", install.budget, defaultGuestCommandTimeout, bootstrapGuestCommandTimeout)
	}
	// The validation probe stays on the runtime's own cap: no deadline is
	// threaded, so the caller has not widened it.
	if probe.budget != 0 {
		t.Fatalf("validation probe budget = %s, want the runtime default", probe.budget)
	}
}

// TestGuestCommandTimeoutDefersToCallerDeadline is the runtime half of the same
// contract: LimaClient.Shell must not re-cap work the caller already bounded.
func TestGuestCommandTimeoutDefersToCallerDeadline(t *testing.T) {
	t.Parallel()

	if got := guestCommandTimeout(context.Background()); got != defaultGuestCommandTimeout {
		t.Fatalf("guestCommandTimeout(unbounded) = %s, want %s", got, defaultGuestCommandTimeout)
	}
	bounded, cancel := context.WithTimeout(context.Background(), bootstrapGuestCommandTimeout)
	defer cancel()
	if got := guestCommandTimeout(bounded); got != 0 {
		t.Fatalf("guestCommandTimeout(bounded) = %s, want the caller's own deadline to stand", got)
	}
}

// TestNodeStartIgnoresStaleRuntimeCacheWhenDecidingToBoot pins the boundary
// ADR 126 draws: read surfaces may serve the observation cache, but the
// decision to boot a VM may not. A cache entry that still says "running" for an
// instance that is stopped makes NodeStart skip the boot and then run every
// bootstrap command against a machine that is not there.
func TestNodeStartIgnoresStaleRuntimeCacheWhenDecidingToBoot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "stale-cache"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	fake := service.sandbox.(*fakeSandbox)
	fake.mu.Lock()
	// The runtime is genuinely stopped; only the cache claims otherwise.
	fake.staleObservations = map[string]RuntimeObservation{
		node.SandboxName: {Name: node.SandboxName, Exists: true, Status: ObservationRunning, Dir: "/fake/" + node.SandboxName},
	}
	fake.mu.Unlock()

	if _, err := service.NodeStart(ctx, node.ID); err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	fake.mu.Lock()
	calls := append([]string(nil), fake.calls...)
	uncached := fake.uncachedListCalls
	fake.mu.Unlock()
	if !containsCall(calls, "start "+node.SandboxName) {
		t.Fatalf("NodeStart() trusted a stale cache entry and never booted the VM: %v", calls)
	}
	if uncached == 0 {
		t.Fatal("NodeStart() never asked the runtime for an uncached listing")
	}
}

func TestNodeStartUsesConfiguredWorkspaceSeedPrepareCommand(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory:     workspace,
		Slug:          "root-node",
		WorkspaceMode: WorkspaceModeCopy,
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	bootstrap, err := service.store.LoadBootstrapState(node.ID)
	if err != nil {
		t.Fatalf("LoadBootstrapState() error = %v", err)
	}
	node.RuntimeCommands.WorkspaceSeedPrepare = []string{"echo preparing {{sandbox_name}} {{target_path}} {{target_parent}}"}
	if err := service.store.SaveNode(node, bootstrap); err != nil {
		t.Fatalf("SaveNode(custom workspace seed prepare command) error = %v", err)
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

	if _, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory: workspace,
		Slug:      "broken-node",
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
	if containsCall(service.sandbox.(*fakeSandbox).calls, "delete broken-node") {
		t.Fatal("a create failure without a mutation marker deleted a potentially pre-existing runtime instance")
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

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory: workspace,
		Slug:      "healthy-node",
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
}

func TestNodeListReconcilesRuntimeStatusInBatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	firstNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory: workspace,
		Slug:      "first-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate(first) error = %v", err)
	}
	secondNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory: workspace,
		Slug:      "second-node",
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
	if err := os.WriteFile(filepath.Join(partialDir, "sandbox.ref"), []byte("partial-node-12345678\n"), 0o644); err != nil {
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
	if !strings.Contains(warningText, "partial-node-12345678") {
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

	healthyNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory: workspace,
		Slug:      "healthy-node",
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
	instanceName := "partial-node-12345678"
	if err := os.WriteFile(filepath.Join(partialDir, "sandbox.ref"), []byte(instanceName+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(instance ref) error = %v", err)
	}
	if err := os.WriteFile(service.store.nodeInstanceIndexPath(instanceName), []byte("partial-node\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(instance index) error = %v", err)
	}

	dryRun, err := service.NodeCleanupIncomplete(context.Background(), false)
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

	applied, err := service.NodeCleanupIncomplete(context.Background(), true)
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

func TestNodeCloneCreatesSiblingNode(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "root-node"})
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

	if childNode.ParentNodeID != node.ID {
		t.Fatalf("expected child node parent id %q, got %q", node.ID, childNode.ParentNodeID)
	}

	if childNode.ConfigurationID != node.ConfigurationID {
		t.Fatalf("expected child node configuration id %q, got %q", node.ConfigurationID, childNode.ConfigurationID)
	}

	if childNode.SandboxName != "child-node" {
		t.Fatalf("expected cloned node sandbox instance name to match child node slug, got %q", childNode.SandboxName)
	}

	if childNode.GuestWorkspacePath != workspace {
		t.Fatalf("expected child node guest workspace path %q, got %q", workspace, childNode.GuestWorkspacePath)
	}

	if !containsCall(service.sandbox.(*fakeSandbox).calls, "clone "+node.SandboxName+" "+childNode.SandboxName) {
		t.Fatalf("expected sandbox clone delegation, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}
}

func TestNodeClonePreservesMountedWorkspaceMode(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory:     workspace,
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

func TestHomeWithLegacyPatchDirsStillLoads(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)

	// Simulate a home written by an older binary that still had the patch
	// subsystem: a patch metadata directory and a by-status index ref.
	home := service.cfg.MetadataRoot
	writeFile(t, filepath.Join(home, "patches", "legacy-patch-id", "proposal.yaml"), "id: legacy-patch-id\nstatus: draft\n")
	writeFile(t, filepath.Join(home, "_index", "patches", "by-status", "draft", "legacy-patch-id.ref"), "legacy-patch-id\n")

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

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory: workspace,
		Slug:      "root-node",
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	if _, err := dispatch(ctx, service, []string{"shell", node.ID, "--", "uname", "-a"}); err != nil {
		t.Fatalf("dispatch(shell) error = %v", err)
	}

	if !containsCall(service.sandbox.(*fakeSandbox).calls, "shell "+node.SandboxName+" uname -a") {
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

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory:     workspace,
		Slug:          "root-node",
		WorkspaceMode: WorkspaceModeCopy,
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	bootstrap, err := service.store.LoadBootstrapState(node.ID)
	if err != nil {
		t.Fatalf("LoadBootstrapState() error = %v", err)
	}
	node.RuntimeCommands = RuntimeCommandTemplates{
		WorkspaceSeedPrepare: []string{"echo inherited-prepare {{target_path}}"},
	}
	if err := service.store.SaveNode(node, bootstrap); err != nil {
		t.Fatalf("SaveNode(custom guest commands) error = %v", err)
	}

	childNode, err := service.NodeClone(ctx, NodeCloneInput{
		SourceNode: node.ID,
		NodeSlug:   "root-node-clone",
	})
	if err != nil {
		t.Fatalf("NodeClone() error = %v", err)
	}

	if strings.Join(childNode.RuntimeCommands.WorkspaceSeedPrepare, "|") != strings.Join(node.RuntimeCommands.WorkspaceSeedPrepare, "|") {
		t.Fatalf("expected cloned node to inherit workspace seed prepare override %q, got %q", node.RuntimeCommands.WorkspaceSeedPrepare, childNode.RuntimeCommands.WorkspaceSeedPrepare)
	}

	stored, err := service.store.NodeByID(childNode.ID)
	if err != nil {
		t.Fatalf("NodeByID(child) error = %v", err)
	}
	if strings.Join(stored.RuntimeCommands.WorkspaceSeedPrepare, "|") != strings.Join(node.RuntimeCommands.WorkspaceSeedPrepare, "|") {
		t.Fatalf("expected persisted clone to keep inherited override, got %q", stored.RuntimeCommands.WorkspaceSeedPrepare)
	}

	if _, err := service.NodeStart(ctx, childNode.ID); err != nil {
		t.Fatalf("NodeStart(child) error = %v", err)
	}
	if !containsSubstring(service.sandbox.(*fakeSandbox).calls, "echo inherited-prepare") {
		t.Fatalf("expected cloned node start to use the inherited workspace seed prepare override, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}
}

func TestEnvironmentConfigLifecycleAndConfigurationResolution(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	config, err := service.EnvironmentConfigCreate(context.Background(), EnvironmentConfigCreateInput{
		Slug:              "shared-dev",
		BootstrapCommands: []string{"./script/setup", "direnv allow"},
	})
	if err != nil {
		t.Fatalf("EnvironmentConfigCreate() error = %v", err)
	}

	if got := strings.Join(config.BootstrapCommands, "|"); got != "./script/setup|direnv allow" {
		t.Fatalf("expected created commands, got %q", got)
	}

	configs, err := service.EnvironmentConfigList(context.Background(), false)
	if err != nil {
		t.Fatalf("EnvironmentConfigList() error = %v", err)
	}
	if !containsEnvironmentConfigSlug(configs, "shared-dev") {
		t.Fatalf("expected shared-dev to be listed, got %#v", configs)
	}

	configuration, err := service.ConfigurationCreate(context.Background(), ConfigurationCreateInput{
		Slug:              "root",
		Environments:      []string{"shared-dev"},
		BootstrapCommands: []string{"make init"},
	})
	if err != nil {
		t.Fatalf("ConfigurationCreate() error = %v", err)
	}

	if got := strings.Join(configuration.Environments, "|"); got != "shared-dev" {
		t.Fatalf("expected configuration environments to be assigned, got %q", got)
	}

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Configuration: configuration.ID,
		Directory:     workspace,
		Slug:          "root-node",
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

	if _, err := service.EnvironmentConfigDelete(context.Background(), "shared-dev"); err == nil {
		t.Fatalf("expected delete to reject referenced environment config")
	}

	config, err = service.EnvironmentConfigUpdate(context.Background(), "shared-dev", EnvironmentConfigUpdateInput{
		BootstrapCommands: []string{"mise install"},
	})
	if err != nil {
		t.Fatalf("EnvironmentConfigUpdate() error = %v", err)
	}
	if got := strings.Join(config.BootstrapCommands, "|"); got != "mise install" {
		t.Fatalf("expected updated commands, got %q", got)
	}

	node, err = service.NodeCreate(ctx, NodeCreateInput{
		Configuration: configuration.ID,
		Directory:     workspace,
		Slug:          "root-node-2",
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

	configuration, err = service.ConfigurationUpdate(context.Background(), configuration.ID, ConfigurationUpdateInput{Environments: []string{}})
	if err != nil {
		t.Fatalf("ConfigurationUpdate(clear environments) error = %v", err)
	}
	if len(configuration.Environments) != 0 {
		t.Fatalf("expected configuration environments to be cleared, got %v", configuration.Environments)
	}

	deleted, err := service.EnvironmentConfigDelete(context.Background(), "shared-dev")
	if err != nil {
		t.Fatalf("EnvironmentConfigDelete() error = %v", err)
	}
	if deleted.DeletedAt == nil {
		t.Fatalf("expected deleted environment config to be tombstoned")
	}

	configs, err = service.EnvironmentConfigList(context.Background(), false)
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
	if err := service.EnsureReady(context.Background(), true); err != nil {
		t.Fatalf("EnsureReady(true) error = %v", err)
	}

	configs, err := service.EnvironmentConfigList(context.Background(), false)
	if err != nil {
		t.Fatalf("EnvironmentConfigList() error = %v", err)
	}

	assertEnvironmentConfigCommands(t, configs, "codex",
		`apt-get update && apt-get install -y ca-certificates curl git && node_major="$(node -p 'process.versions.node.split(".")[0]' 2>/dev/null || printf '0')" && if [ "$node_major" -lt 22 ] || ! command -v npm >/dev/null 2>&1; then nodesource_script="$(mktemp)" && trap 'rm -f "$nodesource_script"' 0 && curl -fsSL https://deb.nodesource.com/setup_22.x -o "$nodesource_script" && bash "$nodesource_script" && apt-get install -y nodejs; fi`,
		`guest_user="${SUDO_USER:-$(id -un)}"; guest_home="$(getent passwd "$guest_user" | cut -d: -f6)"; test -n "$guest_home" && sudo -u "$guest_user" -H env HOME="$guest_home" sh -c 'mkdir -p "$HOME/.local/bin" && npm config set prefix "$HOME/.local"'`,
		`guest_user="${SUDO_USER:-$(id -un)}"; guest_home="$(getent passwd "$guest_user" | cut -d: -f6)"; test -n "$guest_home" && sudo -u "$guest_user" -H env HOME="$guest_home" PATH="$guest_home/.local/bin:$PATH" npm install -g @openai/codex && ln -sfn "$guest_home/.local/bin/codex" /usr/local/bin/codex`,
		`guest_user="${SUDO_USER:-$(id -un)}"; guest_home="$(getent passwd "$guest_user" | cut -d: -f6)"; test -n "$guest_home" && sudo -u "$guest_user" -H env HOME="$guest_home" PATH="$guest_home/.local/bin:$PATH" codex --version >/dev/null`,
	)
	assertEnvironmentConfigCommands(t, configs, "claude-code",
		`apt-get update && apt-get install -y ca-certificates curl git && node_major="$(node -p 'process.versions.node.split(".")[0]' 2>/dev/null || printf '0')" && if [ "$node_major" -lt 22 ] || ! command -v npm >/dev/null 2>&1; then nodesource_script="$(mktemp)" && trap 'rm -f "$nodesource_script"' 0 && curl -fsSL https://deb.nodesource.com/setup_22.x -o "$nodesource_script" && bash "$nodesource_script" && apt-get install -y nodejs; fi`,
		`guest_user="${SUDO_USER:-$(id -un)}"; guest_home="$(getent passwd "$guest_user" | cut -d: -f6)"; test -n "$guest_home" && sudo -u "$guest_user" -H env HOME="$guest_home" sh -c 'mkdir -p "$HOME/.local/bin" && npm config set prefix "$HOME/.local"'`,
		`guest_user="${SUDO_USER:-$(id -un)}"; guest_home="$(getent passwd "$guest_user" | cut -d: -f6)"; test -n "$guest_home" && sudo -u "$guest_user" -H env HOME="$guest_home" PATH="$guest_home/.local/bin:$PATH" npm install -g @anthropic-ai/claude-code && ln -sfn "$guest_home/.local/bin/claude" /usr/local/bin/claude`,
		`guest_user="${SUDO_USER:-$(id -un)}"; guest_home="$(getent passwd "$guest_user" | cut -d: -f6)"; test -n "$guest_home" && sudo -u "$guest_user" -H env HOME="$guest_home" PATH="$guest_home/.local/bin:$PATH" claude --version >/dev/null`,
	)
	for profileName, executable := range map[string]string{"codex-cli": "codex", "claude-code": "claude"} {
		profile, err := service.store.LoadAgentProfile(profileName)
		if err != nil {
			t.Fatalf("LoadAgentProfile(%s) error = %v", profileName, err)
		}
		if !strings.Contains(profile.ValidationCommand, executable+" --version") ||
			!strings.Contains(profile.ValidationCommand, `sudo -u "$guest_user"`) {
			t.Fatalf("expected %s profile to validate as the Lima login user, got %q", profileName, profile.ValidationCommand)
		}
	}

	config, err := service.EnvironmentConfigShow(context.Background(), "codex")
	if err != nil {
		t.Fatalf("EnvironmentConfigShow(codex) error = %v", err)
	}
	if !containsSubstring(config.BootstrapCommands, "npm install -g @openai/codex") ||
		containsSubstring(config.BootstrapCommands, "chatgpt.com/codex/install.sh") ||
		containsSubstring(config.BootstrapCommands, "sudo npm") {
		t.Fatalf("expected codex bootstrap to use a user-owned npm installation, got %q", strings.Join(config.BootstrapCommands, "|"))
	}

	config, err = service.EnvironmentConfigShow(context.Background(), "claude-code")
	if err != nil {
		t.Fatalf("EnvironmentConfigShow(claude-code) error = %v", err)
	}
	if !containsSubstring(config.BootstrapCommands, "npm install -g @anthropic-ai/claude-code") ||
		containsSubstring(config.BootstrapCommands, "claude.ai/install.sh") ||
		containsSubstring(config.BootstrapCommands, "sudo npm") {
		t.Fatalf("expected claude-code bootstrap to use a user-owned npm installation, got %q", strings.Join(config.BootstrapCommands, "|"))
	}

	if _, err := service.EnvironmentConfigUpdate(context.Background(), "codex", EnvironmentConfigUpdateInput{
		BootstrapCommands: []string{"echo customized"},
	}); err != nil {
		t.Fatalf("EnvironmentConfigUpdate(codex) error = %v", err)
	}

	if err := service.EnsureReady(context.Background(), false); err != nil {
		t.Fatalf("EnsureReady(false) error = %v", err)
	}

	config, err = service.EnvironmentConfigShow(context.Background(), "codex")
	if err != nil {
		t.Fatalf("EnvironmentConfigShow(codex) error = %v", err)
	}
	if got := strings.Join(config.BootstrapCommands, "|"); got != "echo customized" {
		t.Fatalf("expected customized codex commands to persist, got %q", got)
	}
}

func TestAgentEnvironmentBootstrapCannotCompleteWithoutExecutable(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		executable string
	}{
		{name: "codex", executable: "codex"},
		{name: "claude-code", executable: "claude"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			service, workspace := newTestService(t)
			writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

			node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: test.name + "-node"})
			if err != nil {
				t.Fatalf("NodeCreate() error = %v", err)
			}

			service.sandbox.(*fakeSandbox).failCommand = test.executable + " --version"
			if _, err := service.NodeStart(ctx, node.ID); err == nil {
				t.Fatalf("NodeStart() succeeded without the selected %s executable", test.executable)
			}

			bootstrap, err := service.store.LoadBootstrapState(node.ID)
			if err != nil {
				t.Fatalf("LoadBootstrapState() error = %v", err)
			}
			if bootstrap.Completed {
				t.Fatalf("%s bootstrap was marked complete after executable validation failed", test.name)
			}
			stored, err := service.store.NodeByID(node.ID)
			if err != nil {
				t.Fatalf("NodeByID() error = %v", err)
			}
			if stored.Status != NodeStatusFailed || stored.BootstrapCompleted {
				t.Fatalf("failed %s bootstrap node = %#v", test.name, stored)
			}
		})
	}
}

func TestUntouchedNativeAgentInstallersMigrateToUserOwnedNPM(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	if err := service.EnsureReady(ctx, true); err != nil {
		t.Fatalf("EnsureReady(true) error = %v", err)
	}

	legacyCommands := map[string][]string{
		"codex": {
			"apt-get update && apt-get install -y ca-certificates curl git",
			`curl -fsSL https://chatgpt.com/codex/install.sh | CODEX_INSTALL_DIR=/usr/local/bin CODEX_NON_INTERACTIVE=1 sh`,
			`command -v codex >/dev/null 2>&1`,
		},
		"claude-code": {
			"curl -fsSL https://claude.ai/install.sh | bash",
		},
	}
	for slug, commands := range legacyCommands {
		if _, err := service.EnvironmentConfigUpdate(ctx, slug, EnvironmentConfigUpdateInput{BootstrapCommands: commands}); err != nil {
			t.Fatalf("EnvironmentConfigUpdate(%s) error = %v", slug, err)
		}
	}
	writeFile(t, service.store.seedVersionPath(), "4\n")

	if err := service.EnsureReady(ctx, true); err != nil {
		t.Fatalf("EnsureReady(true, migrate) error = %v", err)
	}

	for slug, packageName := range map[string]string{
		"codex":       "@openai/codex",
		"claude-code": "@anthropic-ai/claude-code",
	} {
		config, err := service.EnvironmentConfigShow(ctx, slug)
		if err != nil {
			t.Fatalf("EnvironmentConfigShow(%s) error = %v", slug, err)
		}
		if !containsSubstring(config.BootstrapCommands, "npm install -g "+packageName) {
			t.Fatalf("expected %s native installer to migrate to npm, got %q", slug, strings.Join(config.BootstrapCommands, "|"))
		}
	}
}

func TestUntouchedVersion5AgentDefinitionsMigrateToLoginUserValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	if err := service.EnsureReady(ctx, true); err != nil {
		t.Fatalf("EnsureReady(true) error = %v", err)
	}

	for slug, specs := range legacyBuiltInEnvironmentConfigs() {
		if _, err := service.EnvironmentConfigUpdate(ctx, slug, EnvironmentConfigUpdateInput{
			BootstrapCommands: specs[0].BootstrapCommands,
		}); err != nil {
			t.Fatalf("EnvironmentConfigUpdate(%s) error = %v", slug, err)
		}
	}
	for name, profiles := range legacyBuiltInProfiles() {
		if err := writeYAMLFile(service.store.agentProfilePath(name), profiles[0]); err != nil {
			t.Fatalf("write legacy profile %s: %v", name, err)
		}
	}
	writeFile(t, service.store.seedVersionPath(), "5\n")

	if err := service.EnsureReady(ctx, true); err != nil {
		t.Fatalf("EnsureReady(true, migrate) error = %v", err)
	}

	for slug, executable := range map[string]string{"codex": "codex", "claude-code": "claude"} {
		config, err := service.EnvironmentConfigShow(ctx, slug)
		if err != nil {
			t.Fatalf("EnvironmentConfigShow(%s) error = %v", slug, err)
		}
		if !containsSubstring(config.BootstrapCommands, executable+" --version") ||
			containsSubstring(config.BootstrapCommands, "command -v "+executable) {
			t.Fatalf("expected %s environment to validate executable as the login user, got %q", slug, strings.Join(config.BootstrapCommands, "|"))
		}
	}
	for profileName, executable := range map[string]string{"codex-cli": "codex", "claude-code": "claude"} {
		profile, err := service.store.LoadAgentProfile(profileName)
		if err != nil {
			t.Fatalf("LoadAgentProfile(%s) error = %v", profileName, err)
		}
		if !strings.Contains(profile.ValidationCommand, executable+" --version") {
			t.Fatalf("expected %s profile migration, got %q", profileName, profile.ValidationCommand)
		}
	}
}

func TestCustomizedBuiltInAgentProfileIsNotMigrated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	if err := service.EnsureReady(ctx, true); err != nil {
		t.Fatalf("EnsureReady(true) error = %v", err)
	}
	custom := AgentProfile{
		Name:              "codex-cli",
		InstallCommands:   []string{},
		ValidationCommand: "test -x /opt/custom/bin/codex",
		LaunchCommand:     "/opt/custom/bin/codex",
		Environment:       map[string]string{"CODEX_HOME": "/opt/custom/state"},
	}
	if err := writeYAMLFile(service.store.agentProfilePath(custom.Name), custom); err != nil {
		t.Fatalf("write custom profile: %v", err)
	}
	writeFile(t, service.store.seedVersionPath(), "5\n")

	if err := service.EnsureReady(ctx, true); err != nil {
		t.Fatalf("EnsureReady(true, migrate) error = %v", err)
	}
	got, err := service.store.LoadAgentProfile(custom.Name)
	if err != nil {
		t.Fatalf("LoadAgentProfile(%s) error = %v", custom.Name, err)
	}
	if !reflect.DeepEqual(got, custom) {
		t.Fatalf("custom profile was overwritten: got %#v, want %#v", got, custom)
	}
}

func TestNodeStartRepairsCompletedLegacyBuiltInAgentBootstrap(t *testing.T) {
	t.Parallel()

	legacyConfigs := legacyBuiltInEnvironmentConfigs()
	version5Commands := append([]string{}, legacyConfigs["codex"][0].BootstrapCommands...)
	version5Commands = append(version5Commands, legacyConfigs["claude-code"][0].BootstrapCommands...)
	tests := []struct {
		name     string
		commands []string
	}{
		{name: "version-5-npm", commands: version5Commands},
		{name: "native-installers", commands: []string{
			legacyCodexPrerequisitesCommand,
			legacyCodexStandaloneInstallCommand,
			legacyCodexLookupValidationCommand,
			legacyClaudeCodeNativeInstallCommand,
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			service, workspace := newTestService(t)
			writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

			node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "legacy-agent-node"})
			if err != nil {
				t.Fatalf("NodeCreate() error = %v", err)
			}
			bootstrap, err := service.store.LoadBootstrapState(node.ID)
			if err != nil {
				t.Fatalf("LoadBootstrapState() error = %v", err)
			}
			bootstrap.BootstrapCommands = test.commands
			completedAt := time.Now().UTC()
			bootstrap.Completed = true
			bootstrap.CompletedAt = &completedAt
			bootstrap.ValidationCommand = defaultAgentValidationCommand
			node.BootstrapCommands = bootstrap.CombinedCommands()
			node.BootstrapCompleted = true
			node.BootstrapCompletedAt = &completedAt
			if err := service.store.SaveNode(node, bootstrap); err != nil {
				t.Fatalf("SaveNode(legacy bootstrap) error = %v", err)
			}

			fake := service.sandbox.(*fakeSandbox)
			callStart := len(fake.calls)
			started, err := service.NodeStart(ctx, node.ID)
			if err != nil {
				t.Fatalf("NodeStart() error = %v", err)
			}
			calls := append([]string(nil), fake.calls[callStart:]...)
			for _, packageName := range []string{"@openai/codex", "@anthropic-ai/claude-code"} {
				if !containsSubstring(calls, "npm install -g "+packageName) {
					t.Fatalf("legacy bootstrap did not install %s as the login user, calls = %v", packageName, calls)
				}
			}
			for _, executable := range []string{"codex", "claude"} {
				if !containsSubstring(calls, executable+" --version") {
					t.Fatalf("legacy bootstrap did not execute %s as validation, calls = %v", executable, calls)
				}
			}
			if containsSubstring(calls, "command -v codex") || containsSubstring(calls, "command -v claude") ||
				containsSubstring(calls, "chatgpt.com/codex/install.sh") || containsSubstring(calls, "claude.ai/install.sh") {
				t.Fatalf("defective legacy commands were rerun, calls = %v", calls)
			}
			if !started.BootstrapCompleted {
				t.Fatalf("repaired node bootstrap is incomplete: %#v", started)
			}
			repaired, err := service.store.LoadBootstrapState(node.ID)
			if err != nil {
				t.Fatalf("LoadBootstrapState(repaired) error = %v", err)
			}
			if !repaired.Completed || !containsSubstring(repaired.BootstrapCommands, "npm install -g @openai/codex") ||
				!containsSubstring(repaired.BootstrapCommands, "npm install -g @anthropic-ai/claude-code") {
				t.Fatalf("repaired bootstrap state = %#v", repaired)
			}
		})
	}
}

func TestDeletedBuiltInEnvironmentConfigIsNotRecreated(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	if err := service.EnsureReady(context.Background(), true); err != nil {
		t.Fatalf("EnsureReady(true) error = %v", err)
	}
	configurations, err := service.ConfigurationList(context.Background(), false)
	if err != nil {
		t.Fatalf("ConfigurationList() error = %v", err)
	}
	for _, configuration := range configurations {
		if _, err := service.ConfigurationUpdate(context.Background(), configuration.ID, ConfigurationUpdateInput{Environments: []string{}}); err != nil {
			t.Fatalf("ConfigurationUpdate(clear %s environments) error = %v", configuration.Slug, err)
		}
	}

	if _, err := service.EnvironmentConfigDelete(context.Background(), "codex"); err != nil {
		t.Fatalf("EnvironmentConfigDelete(codex) error = %v", err)
	}

	if err := service.EnsureReady(context.Background(), false); err != nil {
		t.Fatalf("EnsureReady(false) error = %v", err)
	}

	configs, err := service.EnvironmentConfigList(context.Background(), false)
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

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory: workspace,
		Slug:      "root-node",
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

func TestNodeStartFailsWhenNodeDirectoryIsMissingBeforeSeed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory:     workspace,
		Slug:          "root-node",
		WorkspaceMode: WorkspaceModeCopy,
	})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}

	if err := os.RemoveAll(workspace); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}

	if _, err := service.NodeStart(ctx, node.ID); err == nil {
		t.Fatalf("expected NodeStart() to fail when the node directory is missing before the guest copy is seeded")
	}

	if len(service.sandbox.(*fakeSandbox).shellCalls) != 0 {
		t.Fatalf("expected guest workspace preparation to be skipped when the node directory is missing")
	}
}

func TestShellAllowsSeededNodeWhenNodeDirectoryIsMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	node, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory:     workspace,
		Slug:          "root-node",
		WorkspaceMode: WorkspaceModeCopy,
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

	parentNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory:     workspace,
		Slug:          "root-node",
		WorkspaceMode: WorkspaceModeCopy,
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

	if !containsCall(service.sandbox.(*fakeSandbox).calls, "stop "+parentNode.SandboxName) {
		t.Fatalf("expected running source node to be stopped before clone, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}

	if !containsCall(service.sandbox.(*fakeSandbox).calls, "clone "+parentNode.SandboxName+" "+childNode.SandboxName) {
		t.Fatalf("expected sandbox clone delegation, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}

	if !containsCall(service.sandbox.(*fakeSandbox).calls, "start "+parentNode.SandboxName) {
		t.Fatalf("expected running source node to be restarted after clone, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}

	callsBeforeChildStart := len(service.sandbox.(*fakeSandbox).calls)
	childNode, err = service.NodeStart(ctx, childNode.ID)
	if err != nil {
		t.Fatalf("NodeStart(child) error = %v", err)
	}

	newCalls := append([]string(nil), service.sandbox.(*fakeSandbox).calls[callsBeforeChildStart:]...)
	if containsCall(newCalls, "copy "+childNode.SandboxName+" "+workspace+" "+workspace) {
		t.Fatalf("expected cloned node start to avoid reseeding the guest workspace, calls = %v", newCalls)
	}

	if containsSubstring(newCalls, "curl -fsSL") {
		t.Fatalf("expected cloned node start to avoid rerunning bootstrap commands, calls = %v", newCalls)
	}
}

func TestNodeCloneStopsCloneWhenProviderLeavesItRunning(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	service.sandbox.(*fakeSandbox).cloneStatus = "running"
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	parentNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory: workspace,
		Slug:      "root-node",
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

	if !containsCall(service.sandbox.(*fakeSandbox).calls, "stop "+childNode.SandboxName) {
		t.Fatalf("expected running clone instance to be stopped, calls = %v", service.sandbox.(*fakeSandbox).calls)
	}
}

func TestNodeDeleteBySlugTargetsActiveNodeWhenDeletedNodeSharesSlug(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	oldNode, err := service.NodeCreate(ctx, NodeCreateInput{
		Directory: workspace,
		Slug:      "design",
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
		Directory: workspace,
		Slug:      "design",
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

	if err := service.EnsureReady(context.Background(), true); err != nil {
		t.Fatalf("EnsureReady(true) error = %v", err)
	}

	// Repeated mutating readiness checks must stay idempotent.
	if err := service.EnsureReady(context.Background(), true); err != nil {
		t.Fatalf("EnsureReady(true, second) error = %v", err)
	}

	configs, err := service.EnvironmentConfigList(context.Background(), false)
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
			results <- service.EnsureReady(context.Background(), true)
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

	configs, err := service.EnvironmentConfigList(context.Background(), false)
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

	configs, err := service.EnvironmentConfigList(context.Background(), false)
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

	node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "worker"})
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

	if _, err := service.ConfigurationList(context.Background(), false); err != nil {
		t.Fatalf("ConfigurationList() error = %v", err)
	}
	if _, err := service.EnvironmentConfigList(context.Background(), false); err != nil {
		t.Fatalf("EnvironmentConfigList() error = %v", err)
	}
	if _, err := service.NodeLogs(context.Background(), node.ID); err != nil {
		t.Fatalf("NodeLogs() error = %v", err)
	}
	if _, err := service.Doctor(ctx, false); err != nil {
		t.Fatalf("Doctor() error = %v", err)
	}

	// The TUI auto-refresh tick reloads the node list through this path.
	if _, err := loadTUINodes(ctx, service, ""); err != nil {
		t.Fatalf("loadTUINodes() error = %v", err)
	}

	after := snapshotHomeFiles(t, service.cfg.MetadataRoot)
	if diff := diffFileSnapshots(before, after); diff != "" {
		t.Fatalf("expected read surfaces to leave the metadata home untouched, but files changed:\n%s", diff)
	}
}

// TestNodeCloneRestartsSourceAfterCallerCancellation pins the rule that a
// mutation owed to the user must not inherit the caller's context. NodeClone
// stops a running source VM and is obliged to put it back; hanging that restart
// off ctx meant a Ctrl+C mid-clone cancelled the restart the moment it began
// and left the user's VM stopped.
func TestNodeCloneRestartsSourceAfterCallerCancellation(t *testing.T) {
	t.Parallel()

	service, workspace := newTestService(t)
	source, err := service.NodeCreate(context.Background(), NodeCreateInput{Directory: workspace, Slug: "clone-src"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	if _, err := service.NodeStart(context.Background(), source.ID); err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	fake := service.sandbox.(*fakeSandbox)
	cloneGate := newFakeSandboxGate()
	fake.cloneGate = cloneGate
	fake.mu.Lock()
	fake.startContexts = nil
	fake.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cloneDone := make(chan struct{})
	go func() {
		defer close(cloneDone)
		// The clone itself is expected to fail once the caller goes away; the
		// source VM's restart is what must still happen.
		_, _ = service.NodeClone(ctx, NodeCloneInput{SourceNode: source.ID, NodeSlug: "clone-dst"})
	}()

	// The clone target's instance name is generated, so wait on the gate itself
	// rather than a name the test can predict.
	select {
	case <-cloneGate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the clone to start")
	}
	cancel()
	close(cloneGate.release)
	select {
	case <-cloneDone:
	case <-time.After(10 * time.Second):
		t.Fatal("NodeClone() did not return after caller cancellation")
	}

	fake.mu.Lock()
	starts := append([]fakeContextState(nil), fake.startContexts...)
	calls := append([]string(nil), fake.calls...)
	fake.mu.Unlock()
	if !containsCall(calls, "start "+source.SandboxName) {
		t.Fatalf("clone never restarted the source VM it stopped: %v", calls)
	}
	if len(starts) == 0 {
		t.Fatal("no Start context was recorded")
	}
	restart := starts[len(starts)-1]
	if restart.err != nil {
		t.Fatalf("source restart ran on the cancelled caller context: %v", restart.err)
	}
	if !restart.bounded {
		t.Fatal("source restart ran on an unbounded context; it must carry a VM-start budget")
	}
}

func TestRefreshDoesNotRaceMutation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "worker"})
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

func (f *deleteFailingSandbox) Delete(ctx context.Context, node Node) error {
	if f.failInstances[node.SandboxName] {
		return externalCommandFailed(
			"forced delete failure",
			errors.New("teardown boom"),
			map[string]any{"sandbox_name": node.SandboxName},
		)
	}
	return f.fakeSandbox.Delete(ctx, node)
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

	const instanceName = "orphan-node-abcd1234"
	partialDir := filepath.Join(service.cfg.MetadataRoot, "nodes", "orphan-node")
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(partial node) error = %v", err)
	}
	writeFile(t, filepath.Join(partialDir, "instance.sandbox.yaml"), "arch: aarch64\n")
	writeFile(t, filepath.Join(partialDir, "sandbox.ref"), instanceName+"\n")
	writeFile(t, service.store.nodeInstanceIndexPath(instanceName), "orphan-node\n")

	// The runtime instance named by the incomplete dir is still live.
	seedObservation(fl, RuntimeObservation{Name: instanceName, Exists: true, Status: "running", Dir: "/fake/" + instanceName})

	result, err := service.NodeCleanupIncomplete(context.Background(), true)
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
	if !containsCall(fl.calls, "delete "+instanceName) {
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
	const instanceName = "stuck-node-99887766"
	service.sandbox = &deleteFailingSandbox{fakeSandbox: fl, failInstances: map[string]bool{instanceName: true}}

	partialDir := filepath.Join(service.cfg.MetadataRoot, "nodes", "stuck-node")
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(partial node) error = %v", err)
	}
	writeFile(t, filepath.Join(partialDir, "sandbox.ref"), instanceName+"\n")
	writeFile(t, service.store.nodeInstanceIndexPath(instanceName), "stuck-node\n")
	seedObservation(fl, RuntimeObservation{Name: instanceName, Exists: true, Status: "running"})

	_, err := service.NodeCleanupIncomplete(context.Background(), true)
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
	const instanceName = "dry-node-1a2b3c4d"

	partialDir := filepath.Join(service.cfg.MetadataRoot, "nodes", "dry-node")
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(partial node) error = %v", err)
	}
	writeFile(t, filepath.Join(partialDir, "sandbox.ref"), instanceName+"\n")
	seedObservation(fl, RuntimeObservation{Name: instanceName, Exists: true, Status: "running"})

	result, err := service.NodeCleanupIncomplete(context.Background(), false)
	if err != nil {
		t.Fatalf("NodeCleanupIncomplete(false) error = %v", err)
	}
	if !result.DryRun {
		t.Fatalf("expected dry-run result")
	}
	if len(result.Items) != 1 || result.Items[0].NodeID != "dry-node" {
		t.Fatalf("expected one incomplete item in dry-run, got %#v", result.Items)
	}
	if containsCall(fl.calls, "delete "+instanceName) {
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

	node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "runner"})
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
	if !containsCall(fl.calls, "delete "+instanceName) {
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

	node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "runner"})
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

// nodeStartGateSandbox blocks the runtime start of one named instance so tests
// can inspect the whole system while a single node is mid-boot. Every other
// call is delegated to the embedded fake, so its observations and call
// bookkeeping stay authoritative.
type nodeStartGateSandbox struct {
	*fakeSandbox
	instance string
	entered  chan struct{}
	release  chan struct{}
}

func (f *nodeStartGateSandbox) Start(ctx context.Context, node Node) error {
	if node.SandboxName == f.instance {
		select {
		case f.entered <- struct{}{}:
		default:
		}
		<-f.release
	}
	return f.fakeSandbox.Start(ctx, node)
}

// awaitLifecycleResult fails the test if a lifecycle call has not returned
// within budget, which is what "blocked behind a booting node" looks like.
func awaitLifecycleResult(t *testing.T, label string, results <-chan error, budget time.Duration) error {
	t.Helper()

	select {
	case err := <-results:
		return err
	case <-time.After(budget):
		t.Fatalf("%s did not complete within %s", label, budget)
		return nil
	}
}

func TestNodeStartHoldsNoLocksAcrossRuntimeWork(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	nodeA, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "node-a"})
	if err != nil {
		t.Fatalf("NodeCreate(node-a) error = %v", err)
	}
	nodeB, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "node-b"})
	if err != nil {
		t.Fatalf("NodeCreate(node-b) error = %v", err)
	}

	gate := &nodeStartGateSandbox{
		fakeSandbox: service.sandbox.(*fakeSandbox),
		instance:    nodeA.SandboxName,
		entered:     make(chan struct{}, 1),
		release:     make(chan struct{}),
	}
	service.sandbox = gate

	startA := make(chan error, 1)
	go func() {
		_, startErr := service.NodeStart(ctx, nodeA.ID)
		startA <- startErr
	}()
	select {
	case <-gate.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("node A never reached the runtime start")
	}

	// The durable guard is in place while nothing holds a flock.
	provisioning, err := service.store.NodeByID(nodeA.ID)
	if err != nil {
		t.Fatalf("NodeByID(node-a) error = %v", err)
	}
	if provisioning.Status != NodeStatusProvisioning {
		t.Fatalf("expected node A to be persisted as provisioning while booting, got %q", provisioning.Status)
	}

	// (a) read paths do not block.
	listResult := make(chan error, 1)
	go func() {
		_, listErr := service.NodeList(ctx, false)
		listResult <- listErr
	}()
	if err := awaitLifecycleResult(t, "NodeList", listResult, 5*time.Second); err != nil {
		t.Fatalf("NodeList() error = %v", err)
	}

	seedResult := make(chan error, 1)
	go func() {
		seedResult <- service.ensureReadyForWrite(ctx)
	}()
	if err := awaitLifecycleResult(t, "seedAndRepair", seedResult, 5*time.Second); err != nil {
		t.Fatalf("ensureReadyForWrite() error = %v", err)
	}

	// No global lock domain is held across the VM boot.
	lockCtx, cancelLocks := context.WithTimeout(ctx, 3*time.Second)
	globalLocks, err := acquireLockSet(lockCtx, service.cfg.MetadataRoot, nil, []lockKey{lockNodes, lockConfigurations, lockEnvironments}, nil)
	cancelLocks()
	if err != nil {
		t.Fatalf("a global lock domain was held across the VM boot: %v", err)
	}
	globalLocks.release()

	// (c) a second operation on the same node fails fast with a typed error.
	for _, second := range []struct {
		label string
		run   func() error
	}{
		{label: "NodeStart", run: func() error { _, err := service.NodeStart(ctx, nodeA.ID); return err }},
		{label: "NodeStop", run: func() error { _, err := service.NodeStop(ctx, nodeA.ID); return err }},
	} {
		results := make(chan error, 1)
		go func() { results <- second.run() }()
		err := awaitLifecycleResult(t, "second "+second.label+" on the booting node", results, 5*time.Second)
		var appErr *AppError
		if !errors.As(err, &appErr) || appErr.Category != CategoryPreconditionFailed {
			t.Fatalf("second %s on the booting node error = %#v, want PreconditionFailed", second.label, err)
		}
		if appErr.Fields["node_id"] != nodeA.ID {
			t.Fatalf("second %s error fields = %#v", second.label, appErr.Fields)
		}
	}

	// (b) an independent node runs its own lifecycle operation in parallel.
	startB := make(chan error, 1)
	go func() {
		_, startErr := service.NodeStart(ctx, nodeB.ID)
		startB <- startErr
	}()
	if err := awaitLifecycleResult(t, "NodeStart(node-b)", startB, 15*time.Second); err != nil {
		t.Fatalf("NodeStart(node-b) error = %v", err)
	}

	close(gate.release)
	if err := <-startA; err != nil {
		t.Fatalf("NodeStart(node-a) error = %v", err)
	}

	persisted, err := service.store.NodeByID(nodeA.ID)
	if err != nil {
		t.Fatalf("NodeByID(node-a, after start) error = %v", err)
	}
	if nodeStatusInFlight(persisted.Status) {
		t.Fatalf("expected node A to leave its in-flight status, got %q", persisted.Status)
	}
	shown, err := service.NodeShow(ctx, nodeA.ID)
	if err != nil {
		t.Fatalf("NodeShow(node-a) error = %v", err)
	}
	if shown.Status != NodeStatusRunning {
		t.Fatalf("expected node A to finish running, got %q", shown.Status)
	}
}

func TestSeedAndRepairSkipsLocksWhenTheHomeIsCurrent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	if err := service.EnsureReady(ctx, true); err != nil {
		t.Fatalf("EnsureReady(true) error = %v", err)
	}

	// Another process owns every domain lock for the rest of this test.
	held, err := acquireLockSet(ctx, service.cfg.MetadataRoot, nil, []lockKey{lockEnvironments, lockConfigurations, lockNodes}, nil)
	if err != nil {
		t.Fatalf("acquireLockSet() error = %v", err)
	}
	defer held.release()

	// The seed stamp is checked before any lock, so an already-seeded home is
	// unaffected even by an already-cancelled context.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := service.seedAndRepair(cancelled, false); err != nil {
		t.Fatalf("seedAndRepair on a current home took a lock: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- service.ensureReadyForWrite(ctx)
	}()
	if err := awaitLifecycleResult(t, "ensureReadyForWrite", done, 2*time.Second); err != nil {
		t.Fatalf("ensureReadyForWrite() error = %v", err)
	}

	// A pass that actually has work to do does take the locks, and therefore
	// does observe contention.
	forcedCtx, cancelForced := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancelForced()
	if err := service.seedAndRepair(forcedCtx, true); err == nil {
		t.Fatal("expected a forced seed pass to wait on the held domain locks")
	}
}

func TestStrandedInFlightNodeStatusIsRecoveredByTheNextOperation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "crashed-node"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	if _, err := service.NodeStart(ctx, node.ID); err != nil {
		t.Fatalf("NodeStart() error = %v", err)
	}

	// Simulate a process killed mid-start: the durable in-flight status is left
	// behind, but no live process holds the node's lifecycle token.
	bootstrap, err := service.store.LoadBootstrapState(node.ID)
	if err != nil {
		t.Fatalf("LoadBootstrapState() error = %v", err)
	}
	stranded, err := service.store.NodeByID(node.ID)
	if err != nil {
		t.Fatalf("NodeByID() error = %v", err)
	}
	stranded.Status = NodeStatusProvisioning
	if err := service.store.SaveNode(stranded, bootstrap); err != nil {
		t.Fatalf("SaveNode(stranded) error = %v", err)
	}

	claimed, err := nodeOperationClaimed(service.cfg.MetadataRoot, node.ID)
	if err != nil {
		t.Fatalf("nodeOperationClaimed() error = %v", err)
	}
	if claimed {
		t.Fatal("expected the stranded node to have no live lifecycle claim")
	}

	report, err := service.Doctor(ctx, false)
	if err != nil {
		t.Fatalf("Doctor(false) error = %v", err)
	}
	if !containsSubstring(report.Warnings, "stuck in status") {
		t.Fatalf("expected doctor to report the stranded node, warnings = %#v", report.Warnings)
	}

	started, err := service.NodeStart(ctx, node.ID)
	if err != nil {
		t.Fatalf("NodeStart(after crash) error = %v", err)
	}
	if started.Status != NodeStatusRunning {
		t.Fatalf("expected the recovered node to start, got %q", started.Status)
	}

	events, err := service.store.NodeEvents(node.ID)
	if err != nil {
		t.Fatalf("NodeEvents() error = %v", err)
	}
	recovered := false
	for _, event := range events {
		if event.Type == "node.lifecycle.recovered" {
			recovered = true
			if event.Fields["previous_status"] != string(NodeStatusProvisioning) {
				t.Fatalf("recovery event fields = %#v", event.Fields)
			}
		}
	}
	if !recovered {
		t.Fatalf("expected a node.lifecycle.recovered event, got %#v", events)
	}
}

func TestDoctorRepairRecoversStrandedNodeClaims(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "crashed-node"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	bootstrap, err := service.store.LoadBootstrapState(node.ID)
	if err != nil {
		t.Fatalf("LoadBootstrapState() error = %v", err)
	}
	node.Status = NodeStatusTerminating
	if err := service.store.SaveNode(node, bootstrap); err != nil {
		t.Fatalf("SaveNode(stranded) error = %v", err)
	}

	report, err := service.Doctor(ctx, true)
	if err != nil {
		t.Fatalf("Doctor(true) error = %v", err)
	}
	repaired := false
	for _, check := range report.Checks {
		if check.Name == "node_claims" {
			repaired = true
			if !strings.Contains(check.Message, "recovered 1") {
				t.Fatalf("node_claims check = %#v", check)
			}
		}
	}
	if !repaired {
		t.Fatalf("expected doctor --repair to report a node_claims check, got %#v", report.Checks)
	}

	persisted, err := service.store.NodeByID(node.ID)
	if err != nil {
		t.Fatalf("NodeByID() error = %v", err)
	}
	if persisted.Status != NodeStatusFailed {
		t.Fatalf("expected the stranded node to be repaired to failed, got %q", persisted.Status)
	}
}

func TestDoctorRepairLeavesLiveLifecycleClaimsAlone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)

	node, err := service.NodeCreate(ctx, NodeCreateInput{Directory: workspace, Slug: "busy-node"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	bootstrap, err := service.store.LoadBootstrapState(node.ID)
	if err != nil {
		t.Fatalf("LoadBootstrapState() error = %v", err)
	}
	node.Status = NodeStatusProvisioning
	if err := service.store.SaveNode(node, bootstrap); err != nil {
		t.Fatalf("SaveNode(provisioning) error = %v", err)
	}

	token, ok, err := tryAcquireNodeOperation(service.cfg.MetadataRoot, node.ID)
	if err != nil || !ok {
		t.Fatalf("tryAcquireNodeOperation() = %v, %v", ok, err)
	}
	defer token.release()

	report, err := service.Doctor(ctx, true)
	if err != nil {
		t.Fatalf("Doctor(true) error = %v", err)
	}
	if containsSubstring(report.Warnings, "stuck in status") {
		t.Fatalf("doctor reported a live operation as stranded: %#v", report.Warnings)
	}

	persisted, err := service.store.NodeByID(node.ID)
	if err != nil {
		t.Fatalf("NodeByID() error = %v", err)
	}
	if persisted.Status != NodeStatusProvisioning {
		t.Fatalf("doctor --repair clobbered a live operation's status: %q", persisted.Status)
	}
}

func TestRunGuestCommandQuotesWorkdirAgainstCommandSubstitution(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	fake := service.sandbox.(*fakeSandbox)

	base := t.TempDir()
	marker := filepath.Join(base, "pwned")
	workdir := filepath.Join(base, "node $(touch "+marker+") `touch "+marker+"` dir")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("MkdirAll(hostile workdir) error = %v", err)
	}

	node := Node{ID: newID(), Slug: "hostile", SandboxName: "hostile-node", GuestWorkspacePath: workdir}
	if err := service.runGuestCommand(context.Background(), node, "echo ok"); err != nil {
		t.Fatalf("runGuestCommand() error = %v", err)
	}
	if len(fake.shellCalls) != 1 {
		t.Fatalf("shell calls = %#v, want one", fake.shellCalls)
	}
	script := fake.shellCalls[0].command[2]
	if want := "cd " + shellQuote(workdir) + " && echo ok"; script != want {
		t.Fatalf("guest script = %q, want %q", script, want)
	}

	// The real proof: running the generated script must not evaluate the
	// directory name. Go's %q would leave $(...) and backticks live.
	output, err := exec.Command("sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("generated script %q failed: %v (%s)", script, err, output)
	}
	if exists(marker) {
		t.Fatalf("workdir command substitution executed as part of the guest script: %q", script)
	}
}
