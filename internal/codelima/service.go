package codelima

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

type Service struct {
	cfg            Config
	store          *Store
	sandbox        SandboxClient
	tui            TUIRunner
	stdin          io.Reader
	stdout         io.Writer
	stderr         io.Writer
	now            func() time.Time
	ready          *serviceReadiness
	logger         *slog.Logger
	logLevel       slog.Level
	daemonClient   *daemonclient.Client
	localTerminals bool
}

// serviceReadiness caches the once-per-instance directory bootstrap for read
// paths. It sits behind a pointer so withIO's shallow clone shares it and so
// Service stays copyable without embedding a sync.Once by value.
type serviceReadiness struct {
	directoriesOnce sync.Once
	directoriesErr  error
}

type NodeCreateInput struct {
	Configuration string
	Directory     string
	Slug          string
	Runtime       string
	Provider      string
	WorkspaceMode string
	Ports         []string
	NetPolicy     *NetPolicy
}

type NodeCloneInput struct {
	SourceNode string
	NodeSlug   string
}

func NewService(cfg Config, sandbox SandboxClient, stdin io.Reader, stdout, stderr io.Writer) *Service {
	if sandbox == nil {
		sandbox = NewSDKSandboxClient()
	}
	if sdk, ok := sandbox.(*SDKSandboxClient); ok {
		sdk.RuntimeCommands = sdk.RuntimeCommands.ApplyDefaults(cfg.RuntimeCommands.ApplyDefaults(defaultRuntimeCommandTemplates()))
		sdk.Stdout = stdout
		sdk.Stderr = stderr
	}

	return &Service{
		cfg:     cfg,
		store:   NewStore(cfg),
		sandbox: sandbox,
		tui:     newTUIRunner(),
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		now: func() time.Time {
			return time.Now().UTC()
		},
		ready:    &serviceReadiness{},
		logger:   discardLogger(),
		logLevel: slog.LevelInfo,
	}
}

// SetLogger installs the process logger and records its level. Run wires the
// CLI-mode text sink here; enableFileLogging later swaps in the TUI file sink at
// the same level. The field is a plain pointer, so withIO's shallow clone shares
// it (mirroring the ready pointer-field pattern from work item 0.3).
func (s *Service) SetLogger(logger *slog.Logger, level slog.Level) {
	if s == nil {
		return
	}
	if logger == nil {
		logger = discardLogger()
	}
	s.logger = logger
	s.logLevel = level
}

// log returns the Service logger, falling back to a discard logger so seam log
// calls are always safe even on a Service built without SetLogger (tests).
func (s *Service) log() *slog.Logger {
	if s == nil || s.logger == nil {
		return discardLogger()
	}
	return s.logger
}

// enableFileLogging switches the Service logger (and the process-global
// libghostty capture logger) to the TUI file sink at the configured level. The
// TUI runner calls it once at startup and defers the returned closer.
func (s *Service) enableFileLogging() (func() error, error) {
	logger, closeLog, err := newTUIFileLogger(s.cfg.MetadataRoot, s.logLevel)
	if err != nil {
		return nil, err
	}
	s.logger = logger
	setPackageLogger(logger)
	return closeLog, nil
}

// logOperation is the deferred half of service-operation seam logging: it emits
// a start record at call time (via logOperationStart) and a finish record here,
// carrying the operation name, duration, and terminal error. Errors log at error
// level; clean finishes at debug so default (info) CLI runs stay quiet.
func (s *Service) logOperation(op string, start time.Time, errp *error) {
	logger := s.log()
	duration := s.now().Sub(start)
	var err error
	if errp != nil {
		err = *errp
	}
	if err != nil {
		logger.Error("operation failed", "op", op, "duration", duration.String(), "error", err.Error())
		return
	}
	logger.Debug("operation finished", "op", op, "duration", duration.String())
}

// logRuntime records a runtime (sandbox) invocation at a service call site: the verb
// only, never the full argv (argv can carry host paths). Debug level keeps it out
// of default CLI output.
func (s *Service) logRuntime(verb, nodeID string) {
	s.log().Debug("sandbox invocation", "verb", verb, "node", nodeID)
}

// recordNodeStartRollback persists the failed-start rollback state and logs any
// error from those writes at error level instead of discarding it (absorbed work
// item 0.7.2). Behaviour is otherwise unchanged: the caller still returns the
// original start failure.
func (s *Service) recordNodeStartRollback(node Node, bootstrap BootstrapState, event Event) {
	if saveErr := s.store.SaveNode(node, bootstrap); saveErr != nil {
		s.log().Error("node start rollback save failed", "node", node.ID, "error", saveErr.Error())
	}
	if eventErr := s.store.AppendNodeEvent(node.ID, event); eventErr != nil {
		s.log().Error("node start rollback event append failed", "node", node.ID, "error", eventErr.Error())
	}
}

func (s *Service) withIO(stdout, stderr io.Writer) *Service {
	if s == nil {
		return nil
	}

	cloned := *s
	cloned.stdout = stdout
	cloned.stderr = stderr

	if sdk, ok := s.sandbox.(*SDKSandboxClient); ok {
		cloned.sandbox = sdk.withIO(stdout, stderr)
	}

	return &cloned
}

func (s *Service) TUI(ctx context.Context, workspaceRoot string) error {
	// A user-initiated app launch is a session start, not a background read:
	// run the one-time locked seed/repair pass here so a fresh home shows the
	// built-in agent profiles and environment configs in the TUI's pickers.
	// This is idempotent, flock-guarded, and once per process — categorically
	// different from the per-tick unlocked writes work item 0.3 removed (ADR
	// 57). The 2s auto-refresh path stays a pure read, and no runtime
	// dependencies are validated: launching the TUI must work without msb.
	if err := s.ensureReadyForWrite(ctx); err != nil {
		return err
	}
	if !s.localTerminals {
		client, err := s.connectTUIDaemon(ctx)
		if err != nil {
			return err
		}
		s.daemonClient = client
		defer func() {
			_ = client.Close()
			s.daemonClient = nil
		}()
	}

	if s.tui == nil {
		s.tui = newTUIRunner()
	}

	return s.tui.Run(ctx, s, workspaceRoot)
}

func (s *Service) connectTUIDaemon(ctx context.Context) (*daemonclient.Client, error) {
	client, err := daemonclient.Dial(ctx, daemonclient.Options{Home: s.cfg.MetadataRoot, Version: Version, WantInput: true})
	if err == nil {
		return claimTUIDaemonInput(ctx, client)
	}
	if !s.cfg.Daemon.Autostart {
		return nil, dependencyUnavailable("daemon not running (codelima daemon start)", err, nil)
	}
	if _, startErr := startDaemon(ctx, s); startErr != nil {
		return nil, startErr
	}
	client, err = daemonclient.Dial(ctx, daemonclient.Options{Home: s.cfg.MetadataRoot, Version: Version, WantInput: true})
	if err != nil {
		return nil, dependencyUnavailable("daemon did not accept the TUI connection", err, nil)
	}
	return claimTUIDaemonInput(ctx, client)
}

func claimTUIDaemonInput(ctx context.Context, client *daemonclient.Client) (*daemonclient.Client, error) {
	if client.Hello.InputOwner {
		return client, nil
	}
	if err := takeTUIDaemonInput(ctx, client); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

// takeTUIDaemonInput makes an already-connected interactive TUI authoritative.
// Unlike claimTUIDaemonInput, it always sends the idempotent takeover request:
// Hello describes connection-time ownership and becomes stale when another TUI
// takes over later.
func takeTUIDaemonInput(ctx context.Context, client *daemonclient.Client) error {
	var result map[string]bool
	if err := client.Call(ctx, "input.takeover", nil, &result); err != nil {
		return fromDaemonError(err)
	}
	if !result["input_owner"] {
		return preconditionFailed("daemon did not grant TUI input ownership", map[string]any{"client_id": client.Hello.ClientID})
	}
	client.Hello.InputOwner = true
	return nil
}

// EnsureReady prepares CODELIMA_HOME for an operation. Read surfaces call it
// with mutating=false: that only creates missing directories (once per Service
// instance) and never writes, seeds, or rewrites files — reads must not write.
// mutating=true additionally seeds and repairs metadata under the
// environments/configurations/nodes locks and validates runtime dependencies.
// Stale metadata (for example an old config.yaml) is therefore upgraded only
// by mutating commands or `codelima doctor --repair`.
func (s *Service) EnsureReady(ctx context.Context, mutating bool) error {
	return s.ensureReady(ctx, mutating)
}

func (s *Service) ensureReady(ctx context.Context, mutating bool) error {
	if !mutating {
		return s.ensureDirectories()
	}

	if err := s.ensureReadyForWrite(ctx); err != nil {
		return err
	}

	return s.validateDependencies(ctx)
}

// ensureReadyForWrite prepares the home for a metadata mutation: directory
// skeleton plus seed/repair under locks, without the runtime-dependency
// validation that EnsureReady(mutating=true) performs. Metadata-only mutations
// (configuration and environment writes) call it directly so they keep
// working without a usable Microsandbox runtime.
func (s *Service) ensureReadyForWrite(ctx context.Context) error {
	if err := s.ensureDirectories(); err != nil {
		return err
	}

	return s.seedAndRepair(ctx, false)
}

func (s *Service) ensureDirectories() error {
	s.ready.directoriesOnce.Do(func() {
		s.ready.directoriesErr = s.store.ensureDirectories()
	})

	return s.ready.directoriesErr
}

// seedAndRepair seeds built-in metadata and repairs stale files while holding
// the environments, configurations, and nodes flocks (acquireLocks sorts its
// keys, so the lock order stays deadlock-free). Holding the locks is what
// keeps concurrent seeding from duplicating built-in environment configs
// (TODO #20).
func (s *Service) seedAndRepair(ctx context.Context, force bool) error {
	lockSet, err := acquireLocks(ctx, s.cfg.MetadataRoot, lockEnvironments, lockConfigurations, lockNodes)
	if err != nil {
		return err
	}
	defer lockSet.release()

	return s.store.seedAndRepair(s.now(), force)
}

func (s *Service) validateDependencies(ctx context.Context) error {
	if _, err := exec.LookPath("git"); err != nil {
		return dependencyUnavailable("git is required", err, nil)
	}

	if version, err := s.sandbox.Version(ctx); err != nil {
		return err
	} else if version != requiredMicrosandboxVersion {
		return dependencyUnavailable(fmt.Sprintf("microsandbox SDK runtime %s found; codelima %s requires exactly %s (see docs: pinning microsandbox)", version, Version, requiredMicrosandboxVersion), nil, nil)
	}

	if _, err := s.sandbox.List(ctx); err != nil {
		return err
	}

	return nil
}

// Doctor inspects the home without modifying it. With repair=true it first
// runs the locked seed-and-repair pass (the same one mutating commands run),
// which is the supported way to upgrade stale metadata from a read-only
// workflow.
func (s *Service) Doctor(ctx context.Context, repair bool) (DoctorReport, error) {
	if err := s.ensureDirectories(); err != nil {
		return DoctorReport{}, err
	}

	report := DoctorReport{
		Checks: []DoctorCheck{},
	}

	if repair {
		if err := s.seedAndRepair(ctx, true); err != nil {
			return DoctorReport{}, err
		}
		report.Checks = append(report.Checks, DoctorCheck{Name: "repair", Status: "ok", Message: "seeded built-in metadata and repaired stale files"})
	}

	if err := validateConfig(s.cfg); err != nil {
		report.Checks = append(report.Checks, DoctorCheck{Name: "config", Status: "fail", Message: err.Error()})
	} else {
		report.Checks = append(report.Checks, DoctorCheck{Name: "config", Status: "ok", Message: "config is valid"})
	}

	if _, err := exec.LookPath("git"); err != nil {
		report.Checks = append(report.Checks, DoctorCheck{Name: "git", Status: "fail", Message: err.Error()})
	} else {
		report.Checks = append(report.Checks, DoctorCheck{Name: "git", Status: "ok", Message: "git is available"})
	}

	if version, versionErr := s.sandbox.Version(ctx); versionErr != nil {
		report.Checks = append(report.Checks, DoctorCheck{Name: "microsandbox_sdk", Status: "fail", Message: versionErr.Error()})
	} else if version != requiredMicrosandboxVersion {
		report.Checks = append(report.Checks, DoctorCheck{Name: "microsandbox_sdk", Status: "fail", Message: fmt.Sprintf("found %s, required exactly %s", version, requiredMicrosandboxVersion)})
	} else {
		report.Checks = append(report.Checks, DoctorCheck{Name: "microsandbox_sdk", Status: "ok", Message: fmt.Sprintf("SDK and runtime match required version %s", version)})
	}

	observations, err := s.sandbox.List(ctx)
	if err != nil {
		report.Checks = append(report.Checks, DoctorCheck{Name: "microsandbox_list", Status: "fail", Message: err.Error()})
	} else {
		report.Checks = append(report.Checks, DoctorCheck{Name: "microsandbox_list", Status: "ok", Message: "SDK sandbox listing succeeded"})
		orphanWarnings, orphanErr := s.detectOrphans(observations)
		if orphanErr != nil {
			return DoctorReport{}, orphanErr
		}

		report.Warnings = append(report.Warnings, orphanWarnings...)
	}

	if missing, err := s.store.MissingConfigurationIndexes(); err != nil {
		return DoctorReport{}, err
	} else {
		report.Warnings = append(report.Warnings, missing...)
	}

	if missing, err := s.store.MissingEnvironmentConfigIndexes(); err != nil {
		return DoctorReport{}, err
	} else {
		report.Warnings = append(report.Warnings, missing...)
	}

	if missing, err := s.store.MissingNodeIndexes(); err != nil {
		return DoctorReport{}, err
	} else {
		report.Warnings = append(report.Warnings, missing...)
	}

	if incomplete, err := s.store.IncompleteNodeWarnings(); err != nil {
		return DoctorReport{}, err
	} else {
		report.Warnings = append(report.Warnings, incomplete...)
	}

	if info, err := os.Stat(s.cfg.MetadataRoot); err == nil {
		if info.Mode().Perm()&0o077 != 0 {
			report.Warnings = append(report.Warnings, "CODELIMA_HOME permissions are broader than user-private")
		}
	}

	if len(s.cfg.MetadataRoot) > 120 {
		report.Warnings = append(report.Warnings, "CODELIMA_HOME path is long; keep MSB_HOME short enough for Unix sockets")
	}

	return report, nil
}

func (s *Service) detectOrphans(observations []RuntimeObservation) ([]string, error) {
	warnings := []string{}
	nodes, err := s.store.ListNodes(true)
	if err != nil {
		return nil, err
	}

	nodeByInstance := map[string]Node{}
	for _, node := range nodes {
		nodeByInstance[node.SandboxName] = node
	}

	for _, observation := range observations {
		if _, ok := nodeByInstance[observation.Name]; !ok {
			warnings = append(warnings, "microsandbox without metadata: "+observation.Name)
		}
	}

	for _, node := range nodes {
		if node.Status == NodeStatusTerminated {
			continue
		}

		if _, ok := findObservation(observations, node.SandboxName); !ok {
			warnings = append(warnings, "metadata exists but microsandbox is missing: "+node.SandboxName)
		}
	}

	return warnings, nil
}

func (s *Service) ConfigSummary() map[string]any {
	return s.cfg.Summary()
}

func pathWithinRoot(rootPath, targetPath string) bool {
	rel, err := filepath.Rel(rootPath, filepath.Clean(targetPath))
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func (s *Service) NodeCreate(ctx context.Context, input NodeCreateInput) (_ Node, err error) {
	s.log().Debug("operation started", "op", "node.create", "configuration", input.Configuration, "directory", input.Directory)
	defer s.logOperation("node.create", s.now(), &err)

	if err := s.ensureReady(ctx, true); err != nil {
		return Node{}, err
	}

	lockSet, err := acquireLocks(ctx, s.cfg.MetadataRoot, lockConfigurations, lockNodes)
	if err != nil {
		return Node{}, err
	}
	defer lockSet.release()

	if strings.TrimSpace(input.Slug) == "" {
		return Node{}, invalidArgument("node slug is required", nil)
	}
	if slugify(input.Slug) != input.Slug {
		return Node{}, invalidArgument("node slug must be a lowercase slug", map[string]any{"slug": input.Slug})
	}

	configurationRef := coalesce(input.Configuration, DefaultConfigurationSlug)
	configuration, err := s.store.ConfigurationByIDOrSlug(configurationRef)
	if err != nil {
		return Node{}, err
	}
	directoryPath, err := s.resolveNodeDirectoryPath(input.Directory)
	if err != nil {
		return Node{}, err
	}

	runtime := coalesce(input.Runtime, RuntimeVM)
	provider := coalesce(input.Provider, ProviderMicrosandbox)
	if runtime != RuntimeVM {
		return Node{}, unsupportedFeature("runtime is reserved but not implemented in Milestone 1", map[string]any{"runtime": runtime})
	}

	if provider != ProviderMicrosandbox {
		return Node{}, unsupportedFeature("provider is reserved but not implemented in Milestone 1", map[string]any{"provider": provider})
	}

	profileName := configuration.AgentProfileName
	profile, err := s.store.LoadAgentProfile(profileName)
	if err != nil {
		return Node{}, err
	}

	workspaceMode := normalizeWorkspaceMode(coalesce(input.WorkspaceMode, DefaultWorkspaceMode))
	if workspaceMode == "" {
		return Node{}, invalidArgument("workspace mode must be copy or mounted", map[string]any{"workspace_mode": input.WorkspaceMode})
	}

	configurationCommands, err := s.resolveConfigurationBootstrapCommands(configuration)
	if err != nil {
		return Node{}, err
	}

	nodeID := newID()
	nodeSlug := input.Slug
	if err := s.ensureUniqueNodeSlug(nodeSlug); err != nil {
		return Node{}, err
	}

	sandboxName, err := s.generateSandboxName(nodeSlug)
	if err != nil {
		return Node{}, err
	}

	bootstrap := BootstrapState{
		AgentProfileName:  profile.Name,
		InstallCommands:   append([]string(nil), profile.InstallCommands...),
		BootstrapCommands: configurationCommands,
		ValidationCommand: profile.ValidationCommand,
		LaunchCommand:     profile.LaunchCommand,
		Environment:       cloneMap(profile.Environment),
		Completed:         false,
	}

	guestWorkspacePath := directoryPath
	workspaceMountPath := ""
	workspaceSeeded := false
	if workspaceMode == WorkspaceModeMounted {
		workspaceMountPath = directoryPath
	}
	image := configuration.Image
	ports := resolvePorts(input.Ports, []string{})
	if _, err := validatePorts(ports); err != nil {
		return Node{}, err
	}

	node := Node{
		ID:                 nodeID,
		Slug:               nodeSlug,
		ConfigurationID:    configuration.ID,
		ConfigurationSlug:  configuration.Slug,
		DirectoryPath:      directoryPath,
		Runtime:            runtime,
		Provider:           provider,
		SandboxName:        sandboxName,
		Image:              image,
		VCPUs:              configuration.VCPUs,
		MemoryMiB:          configuration.MemoryMiB,
		DiskMiB:            configuration.DiskMiB,
		Environments:       append([]string(nil), configuration.Environments...),
		Ports:              ports,
		NetPolicy:          cloneNetPolicy(input.NetPolicy),
		Status:             NodeStatusCreated,
		AgentProfileName:   profileName,
		BootstrapCommands:  bootstrap.CombinedCommands(),
		WorkspaceMode:      workspaceMode,
		GuestWorkspacePath: guestWorkspacePath,
		WorkspaceMountPath: workspaceMountPath,
		WorkspaceSeeded:    workspaceSeeded,
		BootstrapCompleted: false,
		CreatedAt:          s.now(),
		UpdatedAt:          s.now(),
	}

	cleanupNodeDir := true
	cleanupInstance := false
	defer func() {
		if err == nil {
			return
		}
		if cleanupInstance {
			_ = s.sandbox.Delete(ctx, node)
		}
		if cleanupNodeDir {
			_ = os.RemoveAll(s.store.nodeDir(nodeID))
		}
	}()

	s.logRuntime("create", node.ID)
	if err := s.sandbox.Create(ctx, node); err != nil {
		return Node{}, err
	}
	cleanupInstance = true

	if err := s.store.SaveNode(node, bootstrap); err != nil {
		return Node{}, err
	}
	cleanupNodeDir = false
	cleanupInstance = false

	if err := s.store.AppendNodeEvent(node.ID, Event{Timestamp: s.now(), Type: "node.created"}); err != nil {
		return Node{}, err
	}

	return node, nil
}

func (s *Service) NodeList(ctx context.Context, includeDeleted bool) ([]Node, error) {
	if err := s.EnsureReady(ctx, false); err != nil {
		return nil, err
	}

	nodes, err := s.store.ListNodes(includeDeleted)
	if err != nil {
		return nil, err
	}

	nodes, err = s.reconcileNodes(ctx, nodes, false)
	if err != nil {
		return nil, err
	}
	if err := s.hydrateConfigurationSlugs(nodes); err != nil {
		return nil, err
	}
	sortNodesByDirectory(nodes)
	return nodes, nil
}

func (s *Service) NodeListByDirectoryRoot(ctx context.Context, directoryRoot string, includeDeleted bool) ([]Node, error) {
	nodes, err := s.NodeList(ctx, includeDeleted)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(directoryRoot) == "" {
		return nodes, nil
	}
	root, err := canonicalPath(directoryRoot)
	if err != nil {
		return nil, invalidArgument("directory scope must be resolvable", map[string]any{"path": directoryRoot})
	}
	filtered := make([]Node, 0)
	for _, node := range nodes {
		if pathWithinRoot(root, node.DirectoryPath) {
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

func sortNodesByDirectory(nodes []Node) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].DirectoryPath == nodes[j].DirectoryPath {
			return nodes[i].Slug < nodes[j].Slug
		}
		return nodes[i].DirectoryPath < nodes[j].DirectoryPath
	})
}

func (s *Service) hydrateConfigurationSlugs(nodes []Node) error {
	cache := make(map[string]string)
	for index := range nodes {
		if nodes[index].ConfigurationID == "" {
			continue
		}
		slug, ok := cache[nodes[index].ConfigurationID]
		if !ok {
			configuration, err := s.store.ConfigurationByID(nodes[index].ConfigurationID)
			if err != nil {
				return err
			}
			slug = configuration.Slug
			cache[configuration.ID] = slug
		}
		nodes[index].ConfigurationSlug = slug
	}
	return nil
}

func (s *Service) hydrateConfigurationSlug(node *Node) error {
	if node == nil || node.ConfigurationID == "" {
		return nil
	}
	configuration, err := s.store.ConfigurationByID(node.ConfigurationID)
	if err != nil {
		return err
	}
	node.ConfigurationSlug = configuration.Slug
	return nil
}

func (s *Service) NodeCleanupIncomplete(ctx context.Context, apply bool) (IncompleteNodeCleanupResult, error) {
	// A dry run only inspects the home, so it stays on the read tier and never
	// requires a runtime backend. Applying can tear down live runtime instances,
	// so it takes the full write-readiness path (seed/repair under locks plus
	// runtime-dependency validation, which requires msb).
	if apply {
		if err := s.EnsureReady(ctx, true); err != nil {
			return IncompleteNodeCleanupResult{}, err
		}
	} else if err := s.EnsureReady(ctx, false); err != nil {
		return IncompleteNodeCleanupResult{}, err
	}

	lockSet, err := acquireLocks(ctx, s.cfg.MetadataRoot, lockNodes)
	if err != nil {
		return IncompleteNodeCleanupResult{}, err
	}
	defer lockSet.release()

	items, err := s.store.IncompleteNodeMetadata()
	if err != nil {
		return IncompleteNodeCleanupResult{}, err
	}

	result := IncompleteNodeCleanupResult{DryRun: !apply, Items: items}
	if !apply || len(items) == 0 {
		return result, nil
	}

	// Consult the runtime before deleting any metadata. An incomplete node dir
	// still carries its sandbox-instance.ref, so os.RemoveAll'ing it while the
	// instance is live orphans a running VM and loses the only pointer back to
	// it (TODO #10). Tear the instance down first; only then is removing the
	// metadata that references it safe. Dirs with no matching live instance keep
	// the historical behavior of a straight metadata removal.
	observations, err := s.sandbox.List(ctx)
	if err != nil {
		return IncompleteNodeCleanupResult{}, err
	}

	var teardownFailures []string
	for _, item := range items {
		if instanceName := strings.TrimSpace(item.SandboxName); instanceName != "" {
			if _, live := findObservation(observations, instanceName); live {
				if delErr := s.sandbox.Delete(ctx, Node{SandboxName: instanceName}); delErr != nil {
					// Leave the dir (and its ref) in place so a retry or a
					// manual msb removal can still find the sandbox.
					teardownFailures = append(teardownFailures, instanceName)
					continue
				}
			}
		}

		if err := s.store.RemoveIncompleteNodeMetadata([]IncompleteNodeMetadata{item}); err != nil {
			return IncompleteNodeCleanupResult{}, err
		}
	}

	if len(teardownFailures) > 0 {
		return result, externalCommandFailed(
			"failed to tear down runtime instances for incomplete nodes; their metadata was kept",
			fmt.Errorf("instances still present: %s", strings.Join(teardownFailures, ", ")),
			map[string]any{"instances": teardownFailures},
		)
	}

	return result, nil
}

func (s *Service) NodeShow(ctx context.Context, value string) (Node, error) {
	if err := s.EnsureReady(ctx, false); err != nil {
		return Node{}, err
	}

	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return Node{}, err
	}

	// Showing a node is a read: merge the live observation in memory only.
	node, err = s.reconcileNode(ctx, node, false)
	if err != nil {
		return Node{}, err
	}
	if err := s.hydrateConfigurationSlug(&node); err != nil {
		return Node{}, err
	}
	return node, nil
}

// nodeTerminalMetadata resolves the durable node fields needed to launch a
// terminal without reconciling live VM status. A host terminal must remain
// available when Microsandbox is stopped or temporarily unavailable, and a
// guest terminal's child command performs its own runtime checks.
func (s *Service) nodeTerminalMetadata(ctx context.Context, value string) (Node, error) {
	if err := s.EnsureReady(ctx, false); err != nil {
		return Node{}, err
	}
	return s.store.NodeByIDOrSlug(value)
}

func (s *Service) NodeStart(ctx context.Context, value string) (_ Node, err error) {
	s.log().Debug("operation started", "op", "node.start", "node", value)
	defer s.logOperation("node.start", s.now(), &err)

	if err := s.ensureReady(ctx, true); err != nil {
		return Node{}, err
	}

	lockSet, err := acquireLocks(ctx, s.cfg.MetadataRoot, lockNodes)
	if err != nil {
		return Node{}, err
	}
	defer lockSet.release()

	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return Node{}, err
	}

	bootstrap, err := s.store.LoadBootstrapState(node.ID)
	if err != nil {
		return Node{}, err
	}

	node, err = s.reconcileNode(ctx, node, false)
	if err != nil {
		return Node{}, err
	}

	if node.LastRuntimeObservation == nil || node.LastRuntimeObservation.Status != ObservationRunning {
		s.logRuntime("start", node.ID)
		if err := s.sandbox.Start(ctx, node); err != nil {
			return Node{}, err
		}
	}

	now := s.now()
	node.Status = NodeStatusProvisioning
	node.UpdatedAt = now
	if err := s.store.SaveNode(node, bootstrap); err != nil {
		return Node{}, err
	}

	if err := s.store.AppendNodeEvent(node.ID, Event{Timestamp: now, Type: "node.start.started"}); err != nil {
		return Node{}, err
	}

	if !bootstrap.Completed {
		if !node.WorkspaceSeeded {
			if err := s.prepareGuestWorkspace(ctx, node); err != nil {
				node.Status = NodeStatusFailed
				node.UpdatedAt = s.now()
				s.recordNodeStartRollback(node, bootstrap, Event{Timestamp: s.now(), Type: "node.start.failed", Fields: map[string]any{"directory_path": node.DirectoryPath, "error": err.Error()}})
				return Node{}, err
			}

			node.WorkspaceSeeded = true
			node.UpdatedAt = s.now()
			if err := s.store.SaveNode(node, bootstrap); err != nil {
				return Node{}, err
			}
		}

		for _, command := range bootstrap.CombinedCommands() {
			if err := s.runGuestCommand(ctx, node, command); err != nil {
				node.Status = NodeStatusFailed
				node.UpdatedAt = s.now()
				s.recordNodeStartRollback(node, bootstrap, Event{Timestamp: s.now(), Type: "node.start.failed", Fields: map[string]any{"command": command, "error": err.Error()}})
				return Node{}, err
			}
		}

		completedAt := s.now()
		bootstrap.Completed = true
		bootstrap.CompletedAt = &completedAt
		node.BootstrapCompleted = true
		node.BootstrapCompletedAt = &completedAt
	}

	if err := s.runGuestCommand(ctx, node, bootstrap.ValidationCommand); err != nil {
		node.Status = NodeStatusFailed
		node.UpdatedAt = s.now()
		s.recordNodeStartRollback(node, bootstrap, Event{Timestamp: s.now(), Type: "node.start.failed", Fields: map[string]any{"validation_command": bootstrap.ValidationCommand, "error": err.Error()}})
		return Node{}, err
	}

	node.Status = NodeStatusRunning
	node.UpdatedAt = s.now()
	if err := s.store.SaveNode(node, bootstrap); err != nil {
		return Node{}, err
	}

	if err := s.store.AppendNodeEvent(node.ID, Event{Timestamp: node.UpdatedAt, Type: "node.started"}); err != nil {
		return Node{}, err
	}

	result, err := s.reconcileNode(ctx, node, true)
	if err != nil {
		return Node{}, err
	}
	if err := s.hydrateConfigurationSlug(&result); err != nil {
		return Node{}, err
	}
	return result, nil
}

func (s *Service) NodeStop(ctx context.Context, value string) (_ Node, err error) {
	s.log().Debug("operation started", "op", "node.stop", "node", value)
	defer s.logOperation("node.stop", s.now(), &err)

	if err := s.ensureReady(ctx, true); err != nil {
		return Node{}, err
	}

	lockSet, err := acquireLocks(ctx, s.cfg.MetadataRoot, lockNodes)
	if err != nil {
		return Node{}, err
	}
	defer lockSet.release()

	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return Node{}, err
	}

	bootstrap, err := s.store.LoadBootstrapState(node.ID)
	if err != nil {
		return Node{}, err
	}

	node, err = s.reconcileNode(ctx, node, false)
	if err != nil {
		return Node{}, err
	}

	if node.LastRuntimeObservation != nil && node.LastRuntimeObservation.Status != ObservationRunning {
		node.Status = NodeStatusStopped
		node.UpdatedAt = s.now()
		if err := s.store.SaveNode(node, bootstrap); err != nil {
			return Node{}, err
		}
		if err := s.hydrateConfigurationSlug(&node); err != nil {
			return Node{}, err
		}
		return node, nil
	}

	s.logRuntime("stop", node.ID)
	if err := s.sandbox.Stop(ctx, node); err != nil {
		return Node{}, err
	}

	node.Status = NodeStatusStopped
	node.UpdatedAt = s.now()
	if err := s.store.SaveNode(node, bootstrap); err != nil {
		return Node{}, err
	}

	if err := s.store.AppendNodeEvent(node.ID, Event{Timestamp: node.UpdatedAt, Type: "node.stopped"}); err != nil {
		return Node{}, err
	}

	result, err := s.reconcileNode(ctx, node, true)
	if err != nil {
		return Node{}, err
	}
	if err := s.hydrateConfigurationSlug(&result); err != nil {
		return Node{}, err
	}
	return result, nil
}

func (s *Service) NodeClone(ctx context.Context, input NodeCloneInput) (childNode Node, err error) {
	s.log().Debug("operation started", "op", "node.clone", "source", input.SourceNode)
	defer s.logOperation("node.clone", s.now(), &err)

	if err := s.ensureReady(ctx, true); err != nil {
		return Node{}, err
	}

	lockSet, err := acquireLocks(ctx, s.cfg.MetadataRoot, lockNodes)
	if err != nil {
		return Node{}, err
	}
	defer lockSet.release()

	sourceNode, err := s.store.NodeByIDOrSlug(input.SourceNode)
	if err != nil {
		return Node{}, err
	}
	if err := s.hydrateConfigurationSlug(&sourceNode); err != nil {
		return Node{}, err
	}

	sourceNode, err = s.reconcileNode(ctx, sourceNode, false)
	if err != nil {
		return Node{}, err
	}

	sourceBootstrap, err := s.store.LoadBootstrapState(sourceNode.ID)
	if err != nil {
		return Node{}, err
	}

	sourceWasRunning := sourceNode.LastRuntimeObservation != nil && sourceNode.LastRuntimeObservation.Status == ObservationRunning
	if sourceWasRunning {
		if err := s.sandbox.Stop(ctx, sourceNode); err != nil {
			return Node{}, err
		}
	}
	defer func() {
		if !sourceWasRunning {
			return
		}

		if restartErr := s.sandbox.Start(ctx, sourceNode); restartErr != nil {
			err = errors.Join(err, restartErr)
			return
		}

		if _, reconcileErr := s.reconcileNode(ctx, sourceNode, true); reconcileErr != nil {
			err = errors.Join(err, reconcileErr)
		}
	}()

	childNodeSlug := strings.TrimSpace(input.NodeSlug)
	if childNodeSlug == "" {
		return Node{}, invalidArgument("node clone requires --slug", nil)
	}
	if slugify(childNodeSlug) != childNodeSlug {
		return Node{}, invalidArgument("node slug must be a lowercase slug", map[string]any{"slug": childNodeSlug})
	}
	if err := s.ensureUniqueNodeSlug(childNodeSlug); err != nil {
		return Node{}, err
	}

	nodeID := newID()
	sandboxName, err := s.generateSandboxName(childNodeSlug)
	if err != nil {
		return Node{}, err
	}

	bootstrap := sourceBootstrap
	bootstrap.InstallCommands = append([]string(nil), sourceBootstrap.InstallCommands...)
	bootstrap.BootstrapCommands = append([]string(nil), sourceBootstrap.BootstrapCommands...)
	bootstrap.Environment = cloneMap(sourceBootstrap.Environment)

	childNode = Node{
		ID:                   nodeID,
		Slug:                 childNodeSlug,
		ConfigurationID:      sourceNode.ConfigurationID,
		ConfigurationSlug:    sourceNode.ConfigurationSlug,
		DirectoryPath:        sourceNode.DirectoryPath,
		ParentNodeID:         sourceNode.ID,
		Runtime:              RuntimeVM,
		Provider:             ProviderMicrosandbox,
		SandboxName:          sandboxName,
		Image:                sourceNode.Image,
		VCPUs:                sourceNode.VCPUs,
		MemoryMiB:            sourceNode.MemoryMiB,
		DiskMiB:              sourceNode.DiskMiB,
		Environments:         append([]string(nil), sourceNode.Environments...),
		Ports:                append([]string(nil), sourceNode.Ports...),
		NetPolicy:            cloneNetPolicy(sourceNode.NetPolicy),
		Status:               NodeStatusCreated,
		AgentProfileName:     sourceNode.AgentProfileName,
		RuntimeCommands:      sourceNode.RuntimeCommands,
		BootstrapCommands:    append([]string(nil), sourceNode.BootstrapCommands...),
		WorkspaceMode:        nodeWorkspaceMode(sourceNode),
		GuestWorkspacePath:   sourceNode.GuestWorkspacePath,
		WorkspaceMountPath:   sourceNode.WorkspaceMountPath,
		WorkspaceSeeded:      sourceNode.WorkspaceSeeded,
		BootstrapCompleted:   bootstrap.Completed,
		BootstrapCompletedAt: bootstrap.CompletedAt,
		CreatedAt:            s.now(),
		UpdatedAt:            s.now(),
	}

	s.logRuntime("clone", childNode.ID)
	if err := s.sandbox.Clone(ctx, sourceNode, childNode); err != nil {
		return Node{}, err
	}

	reconciledChildNode, err := s.reconcileNode(ctx, childNode, false)
	if err != nil {
		return Node{}, err
	}
	if reconciledChildNode.LastRuntimeObservation != nil && reconciledChildNode.LastRuntimeObservation.Status == ObservationRunning {
		if err := s.sandbox.Stop(ctx, childNode); err != nil {
			return Node{}, err
		}
		reconciledChildNode, err = s.reconcileNode(ctx, childNode, false)
		if err != nil {
			return Node{}, err
		}
	}
	childNode = reconciledChildNode

	if err := s.store.SaveNode(childNode, bootstrap); err != nil {
		return Node{}, err
	}

	if err := s.store.AppendNodeEvent(childNode.ID, Event{Timestamp: s.now(), Type: "node.cloned", Fields: map[string]any{"source_node_id": sourceNode.ID}}); err != nil {
		return Node{}, err
	}

	return childNode, nil
}

func (s *Service) NodeDelete(ctx context.Context, value string) (Node, error) {
	if err := s.ensureReady(ctx, true); err != nil {
		return Node{}, err
	}

	lockSet, err := acquireLocks(ctx, s.cfg.MetadataRoot, lockNodes)
	if err != nil {
		return Node{}, err
	}
	defer lockSet.release()

	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return Node{}, err
	}
	if err := s.hydrateConfigurationSlug(&node); err != nil {
		return Node{}, err
	}

	bootstrap, err := s.store.LoadBootstrapState(node.ID)
	if err != nil {
		return Node{}, err
	}

	now := s.now()
	node.Status = NodeStatusTerminating
	node.UpdatedAt = now
	if err := s.store.SaveNode(node, bootstrap); err != nil {
		return Node{}, err
	}

	if err := s.sandbox.Delete(ctx, node); err != nil {
		return Node{}, err
	}

	deletedAt := s.now()
	node.Status = NodeStatusTerminated
	node.UpdatedAt = deletedAt
	node.DeletedAt = &deletedAt
	if err := s.store.SaveNode(node, bootstrap); err != nil {
		return Node{}, err
	}

	if err := s.store.AppendNodeEvent(node.ID, Event{Timestamp: deletedAt, Type: "node.deleted"}); err != nil {
		return Node{}, err
	}

	return node, nil
}

func (s *Service) NodeStatus(ctx context.Context, value string) (Node, error) {
	return s.NodeShow(ctx, value)
}

func (s *Service) NodeLogs(ctx context.Context, value string) ([]Event, error) {
	if err := s.EnsureReady(ctx, false); err != nil {
		return nil, err
	}

	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return nil, err
	}

	return s.store.NodeEvents(node.ID)
}

func (s *Service) Shell(ctx context.Context, value string, command []string) error {
	if err := s.EnsureReady(ctx, false); err != nil {
		return err
	}

	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return err
	}

	command = normalizeShellCommand(command)
	workdir := s.nodeGuestWorkspacePath(node)
	interactive := len(command) == 0
	if interactive {
		command = interactiveShellLaunchCommand()
	}
	return s.sandbox.Shell(ctx, node, command, workdir, interactive, ShellStreams{
		Stdin:  s.stdin,
		Stdout: s.stdout,
		Stderr: s.stderr,
	})
}

// LaunchSpec is the single description of how to spawn a managed terminal: the
// argv to exec, the working directory to root it in (empty means inherit), and
// the environment to run it with. It is the one shell-launch contract every
// front end (the TUI today, the daemon and any tmux sidebar tomorrow) asks the
// Service for, then hands to the runtime registry to spawn — no caller builds a
// terminal command itself (IMPROVEMENT_PLAN Part F §2.2).
type LaunchSpec struct {
	Argv []string
	Dir  string
	Env  []string
}

// TerminalLaunchSpec builds the LaunchSpec for a target terminal. It is the
// sole place a managed-terminal command is assembled; front ends spawn only
// what it returns (IMPROVEMENT_PLAN Part F §2.2).
//
//   - NodeShell re-enters the codelima binary as `codelima shell <nodeID>`,
//     where nodeID is target.ID, so the child re-enters the VM through CodeLima
//     rather than a raw runtime shell. The codelima executable is resolved here
//     in the Service (os.Executable + resolveCodelimaExecutablePath), not taken
//     from any caller-cached copy.
//   - NodeHostShell is an interactive login shell rooted at the node's host
//     directory. The path is validated here (non-empty, exists,
//     is a directory); a failure is returned as a typed InvalidArgument error
//     that the caller records against the target.
//
// The caller supplies the already-resolved node directory path (node shells
// pass ""); resolution of the node from the store stays with the caller, which
// keeps this a pure, store-free spec builder.
func (s *Service) TerminalLaunchSpec(target terminal.TargetKey, kind terminal.TerminalKind, workspacePath string) (LaunchSpec, error) {
	switch kind {
	case terminal.NodeShell:
		nodeID := strings.TrimSpace(target.ID)
		if nodeID == "" {
			return LaunchSpec{}, invalidArgument("node id is required to launch a node shell", nil)
		}
		executable, err := resolveCodelimaSelfExecutable()
		if err != nil {
			return LaunchSpec{}, dependencyUnavailable("could not resolve the codelima executable to launch a node shell", err, nil)
		}
		return LaunchSpec{
			Argv: []string{executable, "--home", s.cfg.MetadataRoot, "shell", nodeID},
			Env:  os.Environ(),
		}, nil
	case terminal.NodeHostShell:
		if strings.TrimSpace(workspacePath) == "" {
			return LaunchSpec{}, invalidArgument("node directory path is not configured", map[string]any{"target": target.String()})
		}
		info, err := os.Stat(workspacePath)
		if err != nil {
			return LaunchSpec{}, invalidArgument("node directory path is unavailable", map[string]any{"target": target.String(), "directory_path": workspacePath, "error": err.Error()})
		}
		if !info.IsDir() {
			return LaunchSpec{}, invalidArgument("node directory path is not a directory", map[string]any{"target": target.String(), "directory_path": workspacePath})
		}
		return LaunchSpec{
			Argv: interactiveShellLaunchCommand(),
			Dir:  workspacePath,
			Env:  os.Environ(),
		}, nil
	default:
		return LaunchSpec{}, invalidArgument("unsupported terminal kind", map[string]any{"kind": kind.String()})
	}
}

// resolveCodelimaSelfExecutable resolves the path to the running codelima
// binary (following the platform-dir compatibility symlink). It is a package
// var so the Service resolves the executable itself rather than depending on any
// caller's cached copy.
var resolveCodelimaSelfExecutable = func() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return resolveCodelimaExecutablePath(executable), nil
}

func (s *Service) ensureUniqueNodeSlug(slug string) error {
	nodes, err := s.store.ListNodes(false)
	if err != nil {
		return err
	}

	for _, node := range nodes {
		if node.Slug == slug {
			return preconditionFailed("node slug already exists", map[string]any{"slug": slug})
		}
	}

	return nil
}

func (s *Service) ensureUniqueEnvironmentConfigSlug(slug, currentConfigID string) error {
	configs, err := s.store.ListEnvironmentConfigs(false)
	if err != nil {
		return err
	}

	for _, config := range configs {
		if config.Slug == slug && config.ID != currentConfigID {
			return preconditionFailed("environment config slug already exists", map[string]any{"slug": slug})
		}
	}

	return nil
}

func (s *Service) resolveEnvironmentConfigRefs(refs []string) ([]string, error) {
	if refs == nil {
		return nil, nil
	}

	resolved := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return nil, invalidArgument("environment config slug is required", nil)
		}

		config, err := s.store.EnvironmentConfigByIDOrSlug(ref)
		if err != nil {
			return nil, err
		}
		if config.DeletedAt != nil {
			return nil, notFound("environment config not found", map[string]any{"query": ref})
		}
		if seen[config.Slug] {
			continue
		}

		resolved = append(resolved, config.Slug)
		seen[config.Slug] = true
	}

	return resolved, nil
}

func (s *Service) resolveConfigurationBootstrapCommands(configuration Configuration) ([]string, error) {
	commands := make([]string, 0)
	for _, slug := range configuration.Environments {
		environment, err := s.store.EnvironmentConfigByIDOrSlug(slug)
		if err != nil {
			return nil, err
		}
		if environment.DeletedAt != nil {
			return nil, notFound("environment not found", map[string]any{"query": slug})
		}
		commands = append(commands, environment.BootstrapCommands...)
	}
	commands = append(commands, configuration.BootstrapCommands...)
	return commands, nil
}

func (s *Service) resolveNodeDirectoryPath(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", invalidArgument("current directory is unavailable", nil)
		}
		input = cwd
	}
	directoryPath, err := canonicalPath(input)
	if err != nil {
		return "", invalidArgument("node directory must be resolvable", map[string]any{"path": input})
	}
	info, err := os.Stat(directoryPath)
	if err != nil || !info.IsDir() {
		return "", invalidArgument("node directory must be an existing directory", map[string]any{"path": directoryPath})
	}
	if pathWithinRoot(s.cfg.MetadataRoot, directoryPath) {
		return "", invalidArgument("node directory must not be inside CODELIMA_HOME", map[string]any{"path": directoryPath})
	}
	return directoryPath, nil
}

func ensureNodeDirectoryAvailable(node Node) error {
	info, err := os.Stat(node.DirectoryPath)
	if err != nil {
		return preconditionFailed("node directory no longer exists on the host", map[string]any{"node_id": node.ID, "directory_path": node.DirectoryPath})
	}
	if !info.IsDir() {
		return preconditionFailed("node directory is no longer a directory on the host", map[string]any{"node_id": node.ID, "directory_path": node.DirectoryPath})
	}
	return nil
}

func (s *Service) generateSandboxName(nodeSlug string) (string, error) {
	sandboxName := slugify(nodeSlug)
	if len(sandboxName) > 128 {
		sandboxName = sandboxName[:128]
		sandboxName = strings.Trim(sandboxName, "-")
	}
	if sandboxName == "" {
		sandboxName = "node"
	}
	if err := validateSandboxName(sandboxName); err != nil {
		return "", err
	}

	nodes, err := s.store.ListNodes(false)
	if err != nil {
		return "", err
	}

	for _, node := range nodes {
		if node.SandboxName == sandboxName && node.Status != NodeStatusTerminated {
			return "", preconditionFailed("sandbox name already exists", map[string]any{"sandbox_name": sandboxName})
		}
	}

	return sandboxName, nil
}

func (s *Service) runGuestCommand(ctx context.Context, node Node, command string) error {
	if strings.TrimSpace(command) == "" {
		return nil
	}

	workdir := s.nodeGuestWorkspacePath(node)
	script := command
	if workdir != "" {
		script = fmt.Sprintf("cd %q && %s", workdir, command)
	}
	return s.sandbox.Shell(ctx, node, []string{"sh", "-lc", script}, workdir, false, ShellStreams{})
}

func (s *Service) reclaimMountedNodeFilesystemCaches(ctx context.Context) (int, error) {
	nodes, err := s.NodeList(ctx, false)
	if err != nil {
		return 0, err
	}

	reclaimed := 0
	var reclaimErr error
	for _, node := range nodes {
		if nodeWorkspaceMode(node) != WorkspaceModeMounted || node.LastRuntimeObservation == nil || node.LastRuntimeObservation.Status != ObservationRunning {
			continue
		}
		reclaimCtx, cancel := context.WithTimeout(ctx, guestFilesystemCacheReclaimTimeout)
		shellErr := s.sandbox.Shell(
			reclaimCtx,
			node,
			[]string{"sh", "-c", "echo 2 > /proc/sys/vm/drop_caches"},
			"",
			false,
			ShellStreams{},
		)
		cancel()
		if shellErr != nil {
			reclaimErr = errors.Join(reclaimErr, fmt.Errorf("reclaim filesystem metadata cache for mounted node %s: %w", node.Slug, shellErr))
			continue
		}
		reclaimed++
	}

	return reclaimed, reclaimErr
}

func (s *Service) prepareGuestWorkspace(ctx context.Context, node Node) error {
	if err := ensureNodeDirectoryAvailable(node); err != nil {
		return err
	}

	if nodeWorkspaceMode(node) == WorkspaceModeMounted {
		return nil
	}

	return s.seedGuestWorkspace(ctx, node)
}

func (s *Service) seedGuestWorkspace(ctx context.Context, node Node) error {
	targetPath := s.nodeGuestWorkspacePath(node)
	sourcePath := node.DirectoryPath
	prepareScript, err := s.resolveWorkspaceSeedPrepareCommand(node, sourcePath, targetPath)
	if err != nil {
		return err
	}

	if err := s.sandbox.Shell(ctx, node, []string{"sh", "-lc", prepareScript}, "", false, ShellStreams{}); err != nil {
		return err
	}

	return s.sandbox.CopyToGuest(ctx, node, sourcePath, targetPath, true)
}

func (s *Service) resolveWorkspaceSeedPrepareCommand(node Node, sourcePath, targetPath string) (string, error) {
	commands, err := s.sandbox.ResolveCommands(node, runtimeCommandWorkspaceSeedPrepare, map[string]string{
		"sandbox_name":  shellQuote(node.SandboxName),
		"source_path":   shellQuote(sourcePath),
		"target_path":   shellQuote(targetPath),
		"target_parent": shellQuote(filepath.Dir(targetPath)),
	})
	if err != nil {
		return "", err
	}

	return strings.Join(commands, " && "), nil
}

func normalizeShellCommand(command []string) []string {
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}

	return append([]string(nil), command...)
}

// interactiveShellScript is the login-shell bootstrap for managed terminals.
// It lives in a real .sh file so it can be read and linted as shell; the
// @INPUTRC_LINES@ token is replaced with quoted readline bindings at launch.
//
//go:embed embedded/interactive_shell.sh
var interactiveShellScript string

func interactiveShellLaunchCommand() []string {
	shellInputRCLines := []string{
		`"\e[27;2;13~": "\C-v\C-j"`,
		`"\e[13;2u": "\C-v\C-j"`,
	}
	shellInputRCArgs := make([]string, 0, len(shellInputRCLines))
	for _, line := range shellInputRCLines {
		shellInputRCArgs = append(shellInputRCArgs, shellQuote(line))
	}

	script := strings.ReplaceAll(interactiveShellScript, "@INPUTRC_LINES@", strings.Join(shellInputRCArgs, " "))
	return []string{"sh", "-lc", script}
}

func (s *Service) nodeGuestWorkspacePath(node Node) string {
	if node.GuestWorkspacePath != "" {
		return node.GuestWorkspacePath
	}

	if node.WorkspaceMountPath != "" {
		return node.WorkspaceMountPath
	}

	return node.DirectoryPath
}

func normalizeWorkspaceMode(mode string) string {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "", WorkspaceModeCopy:
		return WorkspaceModeCopy
	case WorkspaceModeMounted:
		return WorkspaceModeMounted
	default:
		return ""
	}
}

func nodeWorkspaceMode(node Node) string {
	if mode := normalizeWorkspaceMode(node.WorkspaceMode); mode != "" {
		return mode
	}
	if node.WorkspaceMountPath != "" {
		return WorkspaceModeMounted
	}
	return WorkspaceModeCopy
}

func (s *Service) reconcileNode(ctx context.Context, node Node, persist bool) (Node, error) {
	observations, err := s.sandbox.List(ctx)
	if err != nil {
		return Node{}, err
	}

	return s.reconcileNodeWithObservations(node, observations, persist, s.now())
}

func (s *Service) reconcileNodes(ctx context.Context, nodes []Node, persist bool) ([]Node, error) {
	observations, err := s.sandbox.List(ctx)
	if err != nil {
		return nil, err
	}

	now := s.now()
	reconciled := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		refreshed, err := s.reconcileNodeWithObservations(node, observations, persist, now)
		if err != nil {
			return nil, err
		}
		reconciled = append(reconciled, refreshed)
	}

	return reconciled, nil
}

func (s *Service) reconcileNodeWithObservations(node Node, observations []RuntimeObservation, persist bool, now time.Time) (Node, error) {
	observation, ok := findObservation(observations, node.SandboxName)
	node.LastReconciledAt = &now
	if ok {
		node.LastRuntimeObservation = &observation
		switch observation.Status {
		case ObservationRunning:
			if node.Status != NodeStatusFailed && node.Status != NodeStatusTerminating && node.Status != NodeStatusTerminated {
				node.Status = NodeStatusRunning
			}
		case ObservationStopped:
			if node.Status != NodeStatusFailed && node.Status != NodeStatusTerminating && node.Status != NodeStatusTerminated {
				node.Status = NodeStatusStopped
			}
		}
	} else {
		node.LastRuntimeObservation = &RuntimeObservation{Name: node.SandboxName, Exists: false}
	}

	if persist {
		bootstrap, bootstrapErr := s.store.LoadBootstrapState(node.ID)
		if bootstrapErr != nil {
			return Node{}, bootstrapErr
		}

		node.UpdatedAt = now
		if saveErr := s.store.SaveNode(node, bootstrap); saveErr != nil {
			return Node{}, saveErr
		}
	}

	return node, nil
}

func findObservation(observations []RuntimeObservation, instanceName string) (RuntimeObservation, bool) {
	for _, observation := range observations {
		if observation.Name == instanceName {
			return observation, true
		}
	}

	return RuntimeObservation{}, false
}

func cloneMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return map[string]string{}
	}

	target := make(map[string]string, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

func coalesce(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}
