package codelima

import (
	"context"
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

	"github.com/brianrackle/test_lima/internal/codelima/daemonclient"
	"github.com/brianrackle/test_lima/internal/codelima/terminal"
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

type ProjectCreateInput struct {
	Slug               string
	WorkspacePath      string
	AgentProfile       string
	EnvironmentConfigs []string
	BootstrapCommands  []string
	Image              string
	DefaultPorts       []string
}

type ProjectUpdateInput struct {
	Slug                    *string
	WorkspacePath           *string
	AgentProfile            *string
	EnvironmentConfigs      []string
	ClearEnvironmentConfigs bool
	BootstrapCommands       []string
	ClearBootstrap          bool
	Image                   *string
	DefaultPorts            []string
	ClearDefaultPorts       bool
}

type ProjectForkInput struct {
	SourceProject string
	Slug          string
	WorkspacePath string
}

type NodeCreateInput struct {
	Configuration   string
	Directory       string
	Project         string
	Slug            string
	Runtime         string
	Provider        string
	AgentProfile    string
	WorkspaceMode   string
	Image           string
	Ports           []string
	NetPolicy       *NetPolicy
	RuntimeCommands RuntimeCommandTemplates
}

type NodeCloneInput struct {
	SourceNode      string
	NodeSlug        string
	AgentProfile    string
	RuntimeCommands RuntimeCommandTemplates
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
	if err := s.ensureReadyForWrite(); err != nil {
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
	var result map[string]bool
	if err := client.Call(ctx, "input.takeover", nil, &result); err != nil {
		_ = client.Close()
		return nil, fromDaemonError(err)
	}
	if !result["input_owner"] {
		_ = client.Close()
		return nil, preconditionFailed("daemon did not grant TUI input ownership", map[string]any{"client_id": client.Hello.ClientID})
	}
	client.Hello.InputOwner = true
	return client, nil
}

// EnsureReady prepares CODELIMA_HOME for an operation. Read surfaces call it
// with mutating=false: that only creates missing directories (once per Service
// instance) and never writes, seeds, or rewrites files — reads must not write.
// mutating=true additionally seeds and repairs metadata under the
// environments/configurations/nodes locks and validates runtime dependencies.
// Stale metadata (for example an old config.yaml) is therefore upgraded only
// by mutating commands or `codelima doctor --repair`.
func (s *Service) EnsureReady(mutating bool) error {
	return s.ensureReady(context.Background(), mutating)
}

func (s *Service) ensureReady(ctx context.Context, mutating bool) error {
	if !mutating {
		return s.ensureDirectories()
	}

	if err := s.ensureReadyForWrite(); err != nil {
		return err
	}

	return s.validateDependencies(ctx)
}

// ensureReadyForWrite prepares the home for a metadata mutation: directory
// skeleton plus seed/repair under locks, without the runtime-dependency
// validation that EnsureReady(mutating=true) performs. Metadata-only mutations
// (configuration and environment writes) call it directly so they keep
// working without a usable Microsandbox runtime.
func (s *Service) ensureReadyForWrite() error {
	if err := s.ensureDirectories(); err != nil {
		return err
	}

	return s.seedAndRepair()
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
func (s *Service) seedAndRepair() error {
	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "environments", "configurations", "nodes")
	if err != nil {
		return err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	return s.store.seedAndRepair(s.now())
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
		if err := s.seedAndRepair(); err != nil {
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

func (s *Service) ProjectCreate(ctx context.Context, input ProjectCreateInput) (Project, error) {
	_ = ctx
	if err := s.ensureReadyForWrite(); err != nil {
		return Project{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "projects")
	if err != nil {
		return Project{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	workspacePath, err := s.resolveProjectWorkspacePath(input.WorkspacePath, "")
	if err != nil {
		return Project{}, err
	}

	slug := input.Slug
	if slug == "" {
		slug = slugify(filepath.Base(workspacePath))
	}

	if err := s.ensureUniqueProjectSlug(slug, ""); err != nil {
		return Project{}, err
	}

	environmentConfigs, err := s.resolveEnvironmentConfigRefs(input.EnvironmentConfigs)
	if err != nil {
		return Project{}, err
	}

	now := s.now()
	project := Project{
		ID:                 newID(),
		Slug:               slug,
		WorkspacePath:      workspacePath,
		AgentProfileName:   coalesce(input.AgentProfile, s.cfg.DefaultAgentProfile),
		EnvironmentConfigs: environmentConfigs,
		RuntimeCommands:    RuntimeCommandTemplates{Bootstrap: append([]string(nil), input.BootstrapCommands...)},
		DefaultRuntime:     RuntimeVM,
		DefaultProvider:    ProviderMicrosandbox,
		DefaultImage:       coalesce(input.Image, s.cfg.DefaultImage),
		DefaultPorts:       resolvePorts(input.DefaultPorts, nil, s.cfg.DefaultPorts),
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	if err := s.store.SaveProject(project); err != nil {
		return Project{}, err
	}

	if err := s.store.AppendProjectEvent(project.ID, Event{Timestamp: now, Type: "project.created"}); err != nil {
		return Project{}, err
	}

	return project, nil
}

func (s *Service) ProjectList(includeDeleted bool) ([]Project, error) {
	if err := s.EnsureReady(false); err != nil {
		return nil, err
	}

	return s.store.ListProjects(includeDeleted)
}

func (s *Service) ProjectShow(value string) (Project, error) {
	if err := s.EnsureReady(false); err != nil {
		return Project{}, err
	}

	return s.store.ProjectByIDOrSlug(value)
}

func (s *Service) ProjectUpdate(value string, input ProjectUpdateInput) (Project, error) {
	if err := s.ensureReadyForWrite(); err != nil {
		return Project{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "configurations", "nodes")
	if err != nil {
		return Project{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	project, err := s.store.ProjectByIDOrSlug(value)
	if err != nil {
		return Project{}, err
	}

	if input.Slug != nil && *input.Slug != "" && *input.Slug != project.Slug {
		if err := s.ensureUniqueProjectSlug(*input.Slug, project.ID); err != nil {
			return Project{}, err
		}

		project.Slug = *input.Slug
	}

	if input.AgentProfile != nil {
		project.AgentProfileName = *input.AgentProfile
	}

	if input.WorkspacePath != nil {
		workspacePath, err := s.resolveProjectWorkspacePath(*input.WorkspacePath, project.ID)
		if err != nil {
			return Project{}, err
		}

		if filepath.Clean(workspacePath) != filepath.Clean(project.WorkspacePath) {
			nodes, err := s.store.ProjectNodes(project.ID, false)
			if err != nil {
				return Project{}, err
			}

			for _, node := range nodes {
				if node.Status != NodeStatusTerminated {
					return Project{}, preconditionFailed("project workspace cannot be changed while nodes are live", map[string]any{"project_id": project.ID, "node_id": node.ID, "node_slug": node.Slug})
				}
			}

			project.WorkspacePath = workspacePath
		}
	}

	if input.ClearBootstrap {
		project.RuntimeCommands.Bootstrap = []string{}
	} else if input.BootstrapCommands != nil {
		project.RuntimeCommands.Bootstrap = append([]string(nil), input.BootstrapCommands...)
	}

	if input.ClearEnvironmentConfigs {
		project.EnvironmentConfigs = []string{}
	} else if input.EnvironmentConfigs != nil {
		environmentConfigs, err := s.resolveEnvironmentConfigRefs(input.EnvironmentConfigs)
		if err != nil {
			return Project{}, err
		}
		project.EnvironmentConfigs = environmentConfigs
	}

	if input.Image != nil {
		project.DefaultImage = *input.Image
	}
	if input.ClearDefaultPorts {
		project.DefaultPorts = []string{}
	} else if input.DefaultPorts != nil {
		if _, err := validatePorts(input.DefaultPorts); err != nil {
			return Project{}, err
		}
		project.DefaultPorts = append([]string(nil), input.DefaultPorts...)
	}

	project.UpdatedAt = s.now()
	if err := s.store.SaveProject(project); err != nil {
		return Project{}, err
	}

	if err := s.store.AppendProjectEvent(project.ID, Event{Timestamp: project.UpdatedAt, Type: "project.updated"}); err != nil {
		return Project{}, err
	}

	return project, nil
}

func (s *Service) ProjectDelete(value string) (Project, error) {
	if err := s.EnsureReady(true); err != nil {
		return Project{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "projects", "nodes")
	if err != nil {
		return Project{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	project, err := s.store.ProjectByIDOrSlug(value)
	if err != nil {
		return Project{}, err
	}

	nodes, err := s.store.ProjectNodes(project.ID, false)
	if err != nil {
		return Project{}, err
	}

	for _, node := range nodes {
		if node.Status != NodeStatusTerminated {
			return Project{}, preconditionFailed("project has live nodes", map[string]any{"node_id": node.ID})
		}
	}

	children, err := s.store.ProjectChildren(project.ID, false)
	if err != nil {
		return Project{}, err
	}

	if len(children) > 0 {
		return Project{}, preconditionFailed("project has live child projects", map[string]any{"child_count": len(children)})
	}

	now := s.now()
	project.DeletedAt = &now
	project.UpdatedAt = now
	if err := s.store.SaveProject(project); err != nil {
		return Project{}, err
	}

	if err := s.store.AppendProjectEvent(project.ID, Event{Timestamp: now, Type: "project.deleted"}); err != nil {
		return Project{}, err
	}

	return project, nil
}

func (s *Service) ProjectTree(ctx context.Context, rootQuery string, includeDeleted bool) ([]ProjectTreeNode, error) {
	projects, nodes, err := s.projectTreeData(ctx, includeDeleted)
	if err != nil {
		return nil, err
	}

	var roots []Project
	if rootQuery != "" {
		project, err := s.store.ProjectByIDOrSlug(rootQuery)
		if err != nil {
			return nil, err
		}
		roots = []Project{project}
	} else {
		roots = projectTreeTopLevelRoots(projects)
	}

	return buildProjectTree(projects, nodes, roots), nil
}

func (s *Service) ProjectTreeByWorkspaceRoot(ctx context.Context, workspaceRoot string, includeDeleted bool) ([]ProjectTreeNode, error) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return s.ProjectTree(ctx, "", includeDeleted)
	}

	rootPath, err := canonicalPath(workspaceRoot)
	if err != nil {
		return nil, invalidArgument("workspace root must be resolvable", map[string]any{"path": workspaceRoot})
	}

	projects, nodes, err := s.projectTreeData(ctx, includeDeleted)
	if err != nil {
		return nil, err
	}

	filteredProjects := make([]Project, 0, len(projects))
	includedProjects := map[string]bool{}
	for _, project := range projects {
		if !pathWithinRoot(rootPath, project.WorkspacePath) {
			continue
		}
		filteredProjects = append(filteredProjects, project)
		includedProjects[project.ID] = true
	}

	filteredNodes := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		if includedProjects[node.ProjectID] {
			filteredNodes = append(filteredNodes, node)
		}
	}

	return buildProjectTree(filteredProjects, filteredNodes, projectTreeVisibleRoots(filteredProjects)), nil
}

func (s *Service) projectTreeData(ctx context.Context, includeDeleted bool) ([]Project, []Node, error) {
	if err := s.EnsureReady(false); err != nil {
		return nil, nil, err
	}

	projects, err := s.store.ListProjects(includeDeleted)
	if err != nil {
		return nil, nil, err
	}

	nodes, err := s.store.ListNodes(includeDeleted)
	if err != nil {
		return nil, nil, err
	}
	nodes, err = s.reconcileNodes(ctx, nodes, false)
	if err != nil {
		return nil, nil, err
	}

	return projects, nodes, nil
}

func projectTreeTopLevelRoots(projects []Project) []Project {
	roots := make([]Project, 0)
	for _, project := range projects {
		if project.ParentProjectID == "" {
			roots = append(roots, project)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].Slug < roots[j].Slug
	})

	return roots
}

func projectTreeVisibleRoots(projects []Project) []Project {
	projectMap := map[string]Project{}
	for _, project := range projects {
		projectMap[project.ID] = project
	}

	roots := make([]Project, 0)
	for _, project := range projects {
		if _, ok := projectMap[project.ParentProjectID]; !ok {
			roots = append(roots, project)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].Slug < roots[j].Slug
	})

	return roots
}

func buildProjectTree(projects []Project, nodes []Node, roots []Project) []ProjectTreeNode {
	childrenByParent := map[string][]Project{}
	for _, project := range projects {
		childrenByParent[project.ParentProjectID] = append(childrenByParent[project.ParentProjectID], project)
	}

	nodesByProject := map[string][]Node{}
	for _, node := range nodes {
		nodesByProject[node.ProjectID] = append(nodesByProject[node.ProjectID], node)
	}

	var build func(Project) ProjectTreeNode
	build = func(project Project) ProjectTreeNode {
		children := childrenByParent[project.ID]
		sort.Slice(children, func(i, j int) bool {
			return children[i].Slug < children[j].Slug
		})

		projectNodes := append([]Node(nil), nodesByProject[project.ID]...)
		sort.Slice(projectNodes, func(i, j int) bool {
			return projectNodes[i].Slug < projectNodes[j].Slug
		})

		node := ProjectTreeNode{Project: project, Nodes: projectNodes}
		for _, child := range children {
			node.Children = append(node.Children, build(child))
		}
		return node
	}

	result := make([]ProjectTreeNode, 0, len(roots))
	for _, root := range roots {
		result = append(result, build(root))
	}

	return result
}

func pathWithinRoot(rootPath, targetPath string) bool {
	rel, err := filepath.Rel(rootPath, filepath.Clean(targetPath))
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func (s *Service) ProjectFork(ctx context.Context, input ProjectForkInput) (Project, error) {
	if err := s.ensureReady(ctx, true); err != nil {
		return Project{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "projects")
	if err != nil {
		return Project{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	return s.projectForkUnlocked(ctx, input)
}

func (s *Service) projectForkUnlocked(ctx context.Context, input ProjectForkInput) (Project, error) {
	source, err := s.store.ProjectByIDOrSlug(input.SourceProject)
	if err != nil {
		return Project{}, err
	}

	destinationPath, err := canonicalPath(input.WorkspacePath)
	if err != nil {
		return Project{}, invalidArgument("destination path must be resolvable", map[string]any{"path": input.WorkspacePath})
	}

	if exists(destinationPath) {
		empty, err := directoryEmpty(destinationPath)
		if err != nil {
			return Project{}, err
		}

		if !empty {
			return Project{}, preconditionFailed("destination workspace must be empty", map[string]any{"path": destinationPath})
		}
	}

	if existing, found, err := s.store.ProjectByWorkspacePath(destinationPath); err != nil {
		return Project{}, err
	} else if found {
		return Project{}, preconditionFailed("destination workspace is already registered", map[string]any{"project_id": existing.ID})
	}

	now := s.now()
	baseSnapshotID := newID()
	baseSnapshot, err := captureSnapshot(source, baseSnapshotID, "fork_base", s.store.snapshotTreePath(source.ID, baseSnapshotID), s.cfg.Snapshot.Excludes, now)
	if err != nil {
		return Project{}, err
	}

	if err := s.store.SaveSnapshot(source.ID, baseSnapshot); err != nil {
		return Project{}, err
	}

	if err := materializeSnapshot(baseSnapshot, destinationPath); err != nil {
		return Project{}, err
	}

	slug := input.Slug
	if slug == "" {
		slug = slugify(filepath.Base(destinationPath))
	}

	if err := s.ensureUniqueProjectSlug(slug, ""); err != nil {
		return Project{}, err
	}

	child := Project{
		ID:                 newID(),
		Slug:               slug,
		WorkspacePath:      destinationPath,
		ParentProjectID:    source.ID,
		ForkBaseSnapshotID: baseSnapshot.ID,
		AgentProfileName:   source.AgentProfileName,
		EnvironmentConfigs: append([]string(nil), source.EnvironmentConfigs...),
		DefaultRuntime:     source.DefaultRuntime,
		DefaultProvider:    source.DefaultProvider,
		DefaultImage:       source.DefaultImage,
		DefaultPorts:       append([]string(nil), source.DefaultPorts...),
		RuntimeCommands:    source.RuntimeCommands,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	if err := s.store.SaveProject(child); err != nil {
		return Project{}, err
	}

	// The forked workspace begins from the recorded immutable base snapshot.
	forkManifest := baseSnapshot
	forkManifest.ID = child.ForkBaseSnapshotID
	forkManifest.ProjectID = source.ID

	if err := s.store.AppendProjectEvent(source.ID, Event{Timestamp: now, Type: "project.forked", Fields: map[string]any{"child_project_id": child.ID}}); err != nil {
		return Project{}, err
	}

	if err := s.store.AppendProjectEvent(child.ID, Event{Timestamp: now, Type: "project.created", Fields: map[string]any{"fork_base_snapshot_id": forkManifest.ID}}); err != nil {
		return Project{}, err
	}

	return child, nil
}

func (s *Service) NodeCreate(ctx context.Context, input NodeCreateInput) (_ Node, err error) {
	s.log().Debug("operation started", "op", "node.create", "configuration", input.Configuration, "directory", input.Directory)
	defer s.logOperation("node.create", s.now(), &err)

	if err := s.ensureReady(ctx, true); err != nil {
		return Node{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "configurations", "nodes")
	if err != nil {
		return Node{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	if strings.TrimSpace(input.Slug) == "" {
		return Node{}, invalidArgument("node slug is required", nil)
	}
	if slugify(input.Slug) != input.Slug {
		return Node{}, invalidArgument("node slug must be a lowercase slug", map[string]any{"slug": input.Slug})
	}

	var configuration Configuration
	var project Project
	var directoryPath string
	if input.Project != "" { // Pre-v3 internal compatibility; the public CLI has no project surface.
		project, err = s.store.ProjectByIDOrSlug(input.Project)
		if err != nil {
			return Node{}, err
		}
		directoryPath = project.WorkspacePath
		configuration = Configuration{
			ID: project.ID, Slug: project.Slug, Image: project.DefaultImage,
			AgentProfileName: project.AgentProfileName, Environments: project.EnvironmentConfigs,
			BootstrapCommands: append([]string(nil), project.RuntimeCommands.Bootstrap...),
			VCPUs:             DefaultVCPUs, MemoryMiB: DefaultMemoryMiB, DiskMiB: DefaultDiskMiB,
		}
	} else {
		configurationRef := coalesce(input.Configuration, DefaultConfigurationSlug)
		configuration, err = s.store.ConfigurationByIDOrSlug(configurationRef)
		if err != nil {
			return Node{}, err
		}
		directoryPath, err = s.resolveNodeDirectoryPath(input.Directory)
		if err != nil {
			return Node{}, err
		}
		project = runtimeProjectForNode(Node{DirectoryPath: directoryPath, Image: configuration.Image})
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
	if input.Project != "" {
		profileName = coalesce(input.AgentProfile, profileName, s.cfg.DefaultAgentProfile)
	}
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
	if input.Project != "" {
		image = coalesce(input.Image, configuration.Image, s.cfg.DefaultImage)
		ports = resolvePorts(input.Ports, project.DefaultPorts, s.cfg.DefaultPorts)
	}
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
		RuntimeCommands:    input.RuntimeCommands,
		BootstrapCommands:  bootstrap.CombinedCommands(),
		WorkspaceMode:      workspaceMode,
		GuestWorkspacePath: guestWorkspacePath,
		WorkspaceMountPath: workspaceMountPath,
		WorkspaceSeeded:    workspaceSeeded,
		BootstrapCompleted: false,
		CreatedAt:          s.now(),
		UpdatedAt:          s.now(),
	}
	if input.Project != "" {
		node.ProjectID = project.ID
		node.ConfigurationID = ""
	}

	cleanupNodeDir := true
	cleanupInstance := false
	defer func() {
		if err == nil {
			return
		}
		if cleanupInstance {
			_ = s.sandbox.Delete(ctx, project, node)
		}
		if cleanupNodeDir {
			_ = os.RemoveAll(s.store.nodeDir(nodeID))
		}
	}()

	s.logRuntime("create", node.ID)
	if err := s.sandbox.Create(ctx, project, node); err != nil {
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
	if err := s.EnsureReady(false); err != nil {
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

func (s *Service) NodeCleanupIncomplete(apply bool) (IncompleteNodeCleanupResult, error) {
	// A dry run only inspects the home, so it stays on the read tier and never
	// requires a runtime backend. Applying can tear down live runtime instances,
	// so it takes the full write-readiness path (seed/repair under locks plus
	// runtime-dependency validation, which requires msb).
	if apply {
		if err := s.EnsureReady(true); err != nil {
			return IncompleteNodeCleanupResult{}, err
		}
	} else if err := s.EnsureReady(false); err != nil {
		return IncompleteNodeCleanupResult{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "nodes")
	if err != nil {
		return IncompleteNodeCleanupResult{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

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
	observations, err := s.sandbox.List(context.Background())
	if err != nil {
		return IncompleteNodeCleanupResult{}, err
	}

	var teardownFailures []string
	for _, item := range items {
		if instanceName := strings.TrimSpace(item.SandboxName); instanceName != "" {
			if _, live := findObservation(observations, instanceName); live {
				if delErr := s.sandbox.Delete(context.Background(), Project{}, Node{SandboxName: instanceName}); delErr != nil {
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
	if err := s.EnsureReady(false); err != nil {
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
func (s *Service) nodeTerminalMetadata(value string) (Node, error) {
	if err := s.EnsureReady(false); err != nil {
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

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "nodes")
	if err != nil {
		return Node{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return Node{}, err
	}

	project, err := s.runtimeProject(node)
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

	if node.LastRuntimeObservation == nil || node.LastRuntimeObservation.Status != "running" {
		s.logRuntime("start", node.ID)
		if err := s.sandbox.Start(ctx, project, node); err != nil {
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
			if err := s.prepareGuestWorkspace(ctx, project, node); err != nil {
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

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "nodes")
	if err != nil {
		return Node{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

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

	project, err := s.runtimeProject(node)
	if err != nil {
		return Node{}, err
	}

	if node.LastRuntimeObservation != nil && node.LastRuntimeObservation.Status != "running" {
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
	if err := s.sandbox.Stop(ctx, project, node); err != nil {
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

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "nodes")
	if err != nil {
		return Node{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

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

	if input.AgentProfile != "" && input.AgentProfile != sourceNode.AgentProfileName {
		return Node{}, preconditionFailed("node clone copies the source VM and does not support agent profile overrides", map[string]any{"source_node_id": sourceNode.ID, "agent_profile_name": input.AgentProfile})
	}

	sourceProject, err := s.runtimeProject(sourceNode)
	if err != nil {
		return Node{}, err
	}

	sourceBootstrap, err := s.store.LoadBootstrapState(sourceNode.ID)
	if err != nil {
		return Node{}, err
	}

	sourceWasRunning := sourceNode.LastRuntimeObservation != nil && sourceNode.LastRuntimeObservation.Status == "running"
	if sourceWasRunning {
		if err := s.sandbox.Stop(ctx, sourceProject, sourceNode); err != nil {
			return Node{}, err
		}
	}
	defer func() {
		if !sourceWasRunning {
			return
		}

		if restartErr := s.sandbox.Start(ctx, sourceProject, sourceNode); restartErr != nil {
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
		ProjectID:            sourceNode.ProjectID,
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
	if sourceNode.ProjectID != "" {
		childNode.RuntimeCommands = input.RuntimeCommands.ApplyDefaults(sourceNode.RuntimeCommands)
	}

	s.logRuntime("clone", childNode.ID)
	if err := s.sandbox.Clone(ctx, sourceProject, sourceNode, childNode); err != nil {
		return Node{}, err
	}

	reconciledChildNode, err := s.reconcileNode(ctx, childNode, false)
	if err != nil {
		return Node{}, err
	}
	if reconciledChildNode.LastRuntimeObservation != nil && reconciledChildNode.LastRuntimeObservation.Status == "running" {
		if err := s.sandbox.Stop(ctx, sourceProject, childNode); err != nil {
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

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "nodes")
	if err != nil {
		return Node{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

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

	project, err := s.runtimeProject(node)
	if err != nil {
		return Node{}, err
	}

	if err := s.sandbox.Delete(ctx, project, node); err != nil {
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

func (s *Service) NodeLogs(value string) ([]Event, error) {
	if err := s.EnsureReady(false); err != nil {
		return nil, err
	}

	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return nil, err
	}

	return s.store.NodeEvents(node.ID)
}

func (s *Service) Shell(ctx context.Context, value string, command []string) error {
	if err := s.EnsureReady(false); err != nil {
		return err
	}

	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return err
	}

	project, err := s.runtimeProject(node)
	if err != nil {
		return err
	}

	command = normalizeShellCommand(command)
	workdir := s.nodeGuestWorkspacePath(node)
	interactive := len(command) == 0
	if interactive {
		command = interactiveShellLaunchCommand()
	}
	return s.sandbox.Shell(ctx, project, node, command, workdir, interactive, ShellStreams{
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

func (s *Service) ensureUniqueProjectSlug(slug, currentProjectID string) error {
	projects, err := s.store.ListProjects(false)
	if err != nil {
		return err
	}

	for _, project := range projects {
		if project.Slug == slug && project.ID != currentProjectID {
			return preconditionFailed("project slug already exists", map[string]any{"slug": slug})
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

func runtimeProjectForNode(node Node) Project {
	return Project{
		ID:              node.ConfigurationID,
		Slug:            "configuration",
		WorkspacePath:   node.DirectoryPath,
		DefaultRuntime:  RuntimeVM,
		DefaultProvider: ProviderMicrosandbox,
		DefaultImage:    node.Image,
	}
}

func (s *Service) runtimeProject(node Node) (Project, error) {
	if node.ProjectID != "" {
		return s.store.ProjectByID(node.ProjectID)
	}
	return runtimeProjectForNode(node), nil
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

	project, err := s.runtimeProject(node)
	if err != nil {
		return err
	}

	workdir := s.nodeGuestWorkspacePath(node)
	script := command
	if workdir != "" {
		script = fmt.Sprintf("cd %q && %s", workdir, command)
	}
	return s.sandbox.Shell(ctx, project, node, []string{"sh", "-lc", script}, workdir, false, ShellStreams{})
}

func (s *Service) reclaimMountedNodeFilesystemCaches(ctx context.Context) (int, error) {
	nodes, err := s.NodeList(ctx, false)
	if err != nil {
		return 0, err
	}

	reclaimed := 0
	var reclaimErr error
	for _, node := range nodes {
		if nodeWorkspaceMode(node) != WorkspaceModeMounted || node.LastRuntimeObservation == nil || node.LastRuntimeObservation.Status != NodeStatusRunning {
			continue
		}
		project, projectErr := s.runtimeProject(node)
		if projectErr != nil {
			reclaimErr = errors.Join(reclaimErr, fmt.Errorf("resolve runtime project for mounted node %s: %w", node.Slug, projectErr))
			continue
		}
		reclaimCtx, cancel := context.WithTimeout(ctx, guestFilesystemCacheReclaimTimeout)
		shellErr := s.sandbox.Shell(
			reclaimCtx,
			project,
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

func (s *Service) prepareGuestWorkspace(ctx context.Context, project Project, node Node) error {
	if node.ProjectID != "" {
		if err := s.ensureProjectWorkspaceAvailable(project); err != nil {
			return err
		}
	} else if err := ensureNodeDirectoryAvailable(node); err != nil {
		return err
	}

	if nodeWorkspaceMode(node) == WorkspaceModeMounted {
		return nil
	}

	return s.seedGuestWorkspace(ctx, project, node)
}

func (s *Service) seedGuestWorkspace(ctx context.Context, project Project, node Node) error {
	targetPath := s.nodeGuestWorkspacePath(node)
	sourcePath := node.DirectoryPath
	if sourcePath == "" {
		sourcePath = project.WorkspacePath
	}
	prepareScript, err := s.resolveWorkspaceSeedPrepareCommand(project, node, sourcePath, targetPath)
	if err != nil {
		return err
	}

	if err := s.sandbox.Shell(ctx, project, node, []string{"sh", "-lc", prepareScript}, "", false, ShellStreams{}); err != nil {
		return err
	}

	return s.sandbox.CopyToGuest(ctx, project, node, sourcePath, targetPath, true)
}

func (s *Service) resolveWorkspaceSeedPrepareCommand(project Project, node Node, sourcePath, targetPath string) (string, error) {
	commands, err := s.sandbox.ResolveCommands(project, node, runtimeCommandWorkspaceSeedPrepare, map[string]string{
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

func interactiveShellLaunchCommand() []string {
	shellInputRCLines := []string{
		`"\e[27;2;13~": "\C-v\C-j"`,
		`"\e[13;2u": "\C-v\C-j"`,
	}
	shellInputRCArgs := make([]string, 0, len(shellInputRCLines))
	for _, line := range shellInputRCLines {
		shellInputRCArgs = append(shellInputRCArgs, shellQuote(line))
	}

	// The INPUTRC customization needs a writable directory for its temp file.
	// A read-only $HOME (mktemp: Read-only file system) must not fail the shell
	// (TODO #18): probe $HOME first, fall back to the working-directory-rooted
	// ./tmp and then $TMPDIR (defaulting to /tmp), and if nothing is writable,
	// skip the customization entirely rather than aborting. All probes suppress
	// their own stderr so a non-writable candidate never leaks an error.
	script := strings.Join([]string{
		`if [ -x /usr/bin/gnustty ] && /bin/stty --version 2>/dev/null | grep -qi 'uutils coreutils'; then`,
		`  sudo -n ln -sf /usr/bin/gnustty /bin/stty >/dev/null 2>&1 || true`,
		`  sudo -n ln -sf /usr/bin/gnustty /usr/bin/stty >/dev/null 2>&1 || true`,
		`fi`,
		`shell_inputrc=""`,
		`if command -v mktemp >/dev/null 2>&1; then`,
		`  for shell_inputrc_dir in "${HOME:-}" "${PWD:-}/tmp" "${TMPDIR:-/tmp}"; do`,
		`    [ -n "${shell_inputrc_dir}" ] || continue`,
		`    [ -d "${shell_inputrc_dir}" ] || continue`,
		`    shell_inputrc="$(mktemp "${shell_inputrc_dir}/.codelima-inputrc.XXXXXX" 2>/dev/null)" || shell_inputrc=""`,
		`    [ -n "${shell_inputrc}" ] && break`,
		`  done`,
		`fi`,
		`if [ -n "${shell_inputrc}" ]; then`,
		`  if [ -n "${HOME:-}" ] && [ -f "${HOME}/.inputrc" ]; then`,
		`    cat "${HOME}/.inputrc" > "${shell_inputrc}"`,
		`    printf '\n' >> "${shell_inputrc}"`,
		`  fi`,
		fmt.Sprintf(`  printf '%%s\n' %s >> "${shell_inputrc}"`, strings.Join(shellInputRCArgs, " ")),
		`  export INPUTRC="${shell_inputrc}"`,
		`fi`,
		`"${SHELL:-/bin/bash}" -l`,
		`status=$?`,
		`if [ -n "${shell_inputrc}" ]; then`,
		`  rm -f "${shell_inputrc}"`,
		`fi`,
		`exit "${status}"`,
	}, "\n")

	return []string{"sh", "-lc", script}
}

func (s *Service) nodeGuestWorkspacePath(node Node) string {
	if node.GuestWorkspacePath != "" {
		return node.GuestWorkspacePath
	}

	if node.WorkspaceMountPath != "" {
		return node.WorkspaceMountPath
	}

	if node.DirectoryPath != "" {
		return node.DirectoryPath
	}
	if node.ProjectID != "" {
		project, err := s.store.ProjectByID(node.ProjectID)
		if err == nil {
			return project.WorkspacePath
		}
	}
	return ""
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

func (s *Service) resolveProjectWorkspacePath(input string, currentProjectID string) (string, error) {
	workspacePath, err := canonicalPath(input)
	if err != nil {
		return "", invalidArgument("workspace path must be resolvable", map[string]any{"path": input})
	}

	info, err := os.Stat(workspacePath)
	if err != nil {
		return "", invalidArgument("workspace path must exist", map[string]any{"path": workspacePath})
	}

	if !info.IsDir() {
		return "", invalidArgument("workspace path must be a directory", map[string]any{"path": workspacePath})
	}

	if strings.HasPrefix(workspacePath, s.cfg.MetadataRoot+string(os.PathSeparator)) {
		return "", invalidArgument("workspace path must not be inside CODELIMA_HOME", map[string]any{"path": workspacePath})
	}

	if existing, found, err := s.store.ProjectByWorkspacePath(workspacePath); err != nil {
		return "", err
	} else if found && existing.ID != currentProjectID {
		return "", preconditionFailed("workspace is already registered", map[string]any{"project_id": existing.ID, "workspace_path": workspacePath})
	}

	return workspacePath, nil
}

func (s *Service) ensureProjectWorkspaceAvailable(project Project) error {
	info, err := os.Stat(project.WorkspacePath)
	if err != nil {
		return preconditionFailed("registered workspace path no longer exists on the host; update the project workspace before creating, starting, or shelling into nodes", map[string]any{"project_id": project.ID, "workspace_path": project.WorkspacePath})
	}

	if !info.IsDir() {
		return preconditionFailed("registered workspace path is no longer a directory on the host; update the project workspace before creating, starting, or shelling into nodes", map[string]any{"project_id": project.ID, "workspace_path": project.WorkspacePath})
	}

	return nil
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
		case "running":
			if node.Status != NodeStatusFailed && node.Status != NodeStatusTerminating && node.Status != NodeStatusTerminated {
				node.Status = NodeStatusRunning
			}
		case "stopped":
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
