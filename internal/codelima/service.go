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

	"github.com/brianrackle/test_lima/internal/codelima/terminal"
	"gopkg.in/yaml.v3"
)

type Service struct {
	cfg      Config
	store    *Store
	lima     LimaClient
	tui      TUIRunner
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
	now      func() time.Time
	ready    *serviceReadiness
	logger   *slog.Logger
	logLevel slog.Level
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
	Template           string
}

type ProjectUpdateInput struct {
	Slug                    *string
	WorkspacePath           *string
	AgentProfile            *string
	EnvironmentConfigs      []string
	ClearEnvironmentConfigs bool
	BootstrapCommands       []string
	ClearBootstrap          bool
	Template                *string
}

type ProjectForkInput struct {
	SourceProject string
	Slug          string
	WorkspacePath string
}

type NodeCreateInput struct {
	Project       string
	Slug          string
	Runtime       string
	Provider      string
	AgentProfile  string
	WorkspaceMode string
	LimaCommands  LimaCommandTemplates
}

type NodeCloneInput struct {
	SourceNode   string
	NodeSlug     string
	AgentProfile string
	LimaCommands LimaCommandTemplates
}

func NewService(cfg Config, lima LimaClient, stdin io.Reader, stdout, stderr io.Writer) *Service {
	if lima == nil {
		lima = NewExecLimaClient()
	}
	if execLima, ok := lima.(*ExecLimaClient); ok {
		execLima.LimaCommands = execLima.LimaCommands.ApplyDefaults(cfg.LimaCommands.ApplyDefaults(defaultLimaCommandTemplates()))
		execLima.Stdout = stdout
		execLima.Stderr = stderr
	}

	return &Service{
		cfg:    cfg,
		store:  NewStore(cfg),
		lima:   lima,
		tui:    newTUIRunner(),
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
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

// logLima records a runtime (lima) invocation at a service call site: the verb
// only, never the full argv (argv can carry host paths). Debug level keeps it out
// of default CLI output.
func (s *Service) logLima(verb, nodeID string) {
	s.log().Debug("lima invocation", "verb", verb, "node", nodeID)
}

// recordNodeStartRollback persists the failed-start rollback state and logs any
// error from those writes at error level instead of discarding it (absorbed work
// item 0.7.2). Behaviour is otherwise unchanged: the caller still returns the
// original start failure.
func (s *Service) recordNodeStartRollback(node Node, bootstrap BootstrapState, event Event) {
	if saveErr := s.store.SaveNode(node, bootstrap, nil); saveErr != nil {
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

	if execLima, ok := s.lima.(*ExecLimaClient); ok {
		limaClone := *execLima
		limaClone.Stdout = stdout
		limaClone.Stderr = stderr
		cloned.lima = &limaClone
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
	// dependencies are validated: launching the TUI must work without limactl.
	if err := s.ensureReadyForWrite(); err != nil {
		return err
	}

	if s.tui == nil {
		s.tui = newTUIRunner()
	}

	return s.tui.Run(ctx, s, workspaceRoot)
}

// EnsureReady prepares CODELIMA_HOME for an operation. Read surfaces call it
// with mutating=false: that only creates missing directories (once per Service
// instance) and never writes, seeds, or rewrites files — reads must not write.
// mutating=true additionally seeds and repairs metadata under the
// environment-configs/projects/nodes locks and validates runtime dependencies.
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
// (project and environment-config writes) call it directly so they keep
// working without limactl present.
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
// the environment-configs, projects, and nodes flocks (acquireLocks sorts its
// keys, so the lock order stays deadlock-free). Holding the locks is what
// keeps concurrent seeding from duplicating built-in environment configs
// (TODO #20).
func (s *Service) seedAndRepair() error {
	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "environment-configs", "projects", "nodes")
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

	if _, err := exec.LookPath("limactl"); err != nil {
		if _, ok := s.lima.(*ExecLimaClient); ok {
			return dependencyUnavailable("limactl is required", err, nil)
		}
	}

	if _, err := s.lima.List(ctx); err != nil {
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

	if _, err := exec.LookPath("limactl"); err != nil {
		report.Checks = append(report.Checks, DoctorCheck{Name: "limactl", Status: "fail", Message: err.Error()})
	} else {
		report.Checks = append(report.Checks, DoctorCheck{Name: "limactl", Status: "ok", Message: "limactl is available"})
	}

	observations, err := s.lima.List(ctx)
	if err != nil {
		report.Checks = append(report.Checks, DoctorCheck{Name: "limactl_list", Status: "fail", Message: err.Error()})
	} else {
		report.Checks = append(report.Checks, DoctorCheck{Name: "limactl_list", Status: "ok", Message: "limactl list --json succeeded"})
		orphanWarnings, orphanErr := s.detectOrphans(observations)
		if orphanErr != nil {
			return DoctorReport{}, orphanErr
		}

		report.Warnings = append(report.Warnings, orphanWarnings...)
	}

	if missing, err := s.store.MissingProjectIndexes(); err != nil {
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
		report.Warnings = append(report.Warnings, "CODELIMA_HOME path is long and may hit Lima path length limits")
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
		nodeByInstance[node.LimaInstanceName] = node
	}

	for _, observation := range observations {
		if _, ok := nodeByInstance[observation.Name]; !ok {
			warnings = append(warnings, "lima instance without metadata: "+observation.Name)
		}
	}

	for _, node := range nodes {
		if node.Status == NodeStatusTerminated {
			continue
		}

		if _, ok := findObservation(observations, node.LimaInstanceName); !ok {
			warnings = append(warnings, "metadata exists but lima instance is missing: "+node.LimaInstanceName)
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
		ID:                  newID(),
		Slug:                slug,
		WorkspacePath:       workspacePath,
		AgentProfileName:    coalesce(input.AgentProfile, s.cfg.DefaultAgentProfile),
		EnvironmentConfigs:  environmentConfigs,
		LimaCommands:        LimaCommandTemplates{Bootstrap: append([]string(nil), input.BootstrapCommands...)},
		DefaultRuntime:      RuntimeVM,
		DefaultProvider:     ProviderLima,
		DefaultLimaTemplate: coalesce(input.Template, s.cfg.DefaultTemplate),
		CreatedAt:           now,
		UpdatedAt:           now,
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
		project.LimaCommands.Bootstrap = []string{}
	} else if input.BootstrapCommands != nil {
		project.LimaCommands.Bootstrap = append([]string(nil), input.BootstrapCommands...)
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

	if input.Template != nil {
		project.DefaultLimaTemplate = *input.Template
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
		ID:                  newID(),
		Slug:                slug,
		WorkspacePath:       destinationPath,
		ParentProjectID:     source.ID,
		ForkBaseSnapshotID:  baseSnapshot.ID,
		AgentProfileName:    source.AgentProfileName,
		EnvironmentConfigs:  append([]string(nil), source.EnvironmentConfigs...),
		DefaultRuntime:      source.DefaultRuntime,
		DefaultProvider:     source.DefaultProvider,
		DefaultLimaTemplate: source.DefaultLimaTemplate,
		LimaCommands:        source.LimaCommands,
		CreatedAt:           now,
		UpdatedAt:           now,
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
	s.log().Debug("operation started", "op", "node.create", "project", input.Project)
	defer s.logOperation("node.create", s.now(), &err)

	if err := s.ensureReady(ctx, true); err != nil {
		return Node{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "projects", "nodes")
	if err != nil {
		return Node{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	project, err := s.store.ProjectByIDOrSlug(input.Project)
	if err != nil {
		return Node{}, err
	}

	if err := s.ensureProjectWorkspaceAvailable(project); err != nil {
		return Node{}, err
	}

	runtime := coalesce(input.Runtime, project.DefaultRuntime)
	provider := coalesce(input.Provider, project.DefaultProvider)
	if runtime != RuntimeVM {
		return Node{}, unsupportedFeature("runtime is reserved but not implemented in Milestone 1", map[string]any{"runtime": runtime})
	}

	if provider != ProviderLima {
		return Node{}, unsupportedFeature("provider is reserved but not implemented in Milestone 1", map[string]any{"provider": provider})
	}

	profileName := coalesce(input.AgentProfile, project.AgentProfileName, s.cfg.DefaultAgentProfile)
	profile, err := s.store.LoadAgentProfile(profileName)
	if err != nil {
		return Node{}, err
	}

	workspaceMode := normalizeWorkspaceMode(input.WorkspaceMode)
	if workspaceMode == "" {
		return Node{}, invalidArgument("workspace mode must be copy or mounted", map[string]any{"workspace_mode": input.WorkspaceMode})
	}

	projectCommands, err := s.resolveProjectBootstrapCommands(project, input.LimaCommands)
	if err != nil {
		return Node{}, err
	}

	nodeID := newID()
	nodeSlug := coalesce(input.Slug, slugify(project.Slug+"-node"))
	if err := s.ensureUniqueNodeSlug(nodeSlug); err != nil {
		return Node{}, err
	}

	instanceName, err := s.generateInstanceName(nodeSlug)
	if err != nil {
		return Node{}, err
	}

	bootstrap := BootstrapState{
		AgentProfileName:  profile.Name,
		InstallCommands:   append([]string(nil), profile.InstallCommands...),
		BootstrapCommands: projectCommands,
		ValidationCommand: profile.ValidationCommand,
		LaunchCommand:     profile.LaunchCommand,
		Environment:       cloneMap(profile.Environment),
		Completed:         false,
	}

	guestWorkspacePath := project.WorkspacePath
	workspaceMountPath := ""
	workspaceSeeded := false
	if workspaceMode == WorkspaceModeMounted {
		guestWorkspacePath = ""
		workspaceMountPath = project.WorkspacePath
	}

	node := Node{
		ID:                    nodeID,
		Slug:                  nodeSlug,
		ProjectID:             project.ID,
		Runtime:               runtime,
		Provider:              provider,
		LimaInstanceName:      instanceName,
		Status:                NodeStatusCreated,
		AgentProfileName:      profileName,
		LimaCommands:          input.LimaCommands,
		BootstrapCommands:     bootstrap.CombinedCommands(),
		GeneratedTemplatePath: s.store.nodeTemplatePath(nodeID),
		WorkspaceMode:         workspaceMode,
		GuestWorkspacePath:    guestWorkspacePath,
		WorkspaceMountPath:    workspaceMountPath,
		WorkspaceSeeded:       workspaceSeeded,
		BootstrapCompleted:    false,
		CreatedAt:             s.now(),
		UpdatedAt:             s.now(),
	}

	template, err := s.renderTemplate(ctx, project, node, bootstrap, workspaceMode)
	if err != nil {
		return Node{}, err
	}

	cleanupNodeDir := true
	cleanupInstance := false
	defer func() {
		if err == nil {
			return
		}
		if cleanupInstance {
			_ = s.lima.Delete(ctx, project, node)
		}
		if cleanupNodeDir {
			_ = os.RemoveAll(s.store.nodeDir(nodeID))
		}
	}()

	if err := atomicWriteFile(s.store.nodeTemplatePath(nodeID), template, 0o644); err != nil {
		return Node{}, err
	}

	s.logLima("create", node.ID)
	if err := s.lima.Create(ctx, project, node, s.store.nodeTemplatePath(nodeID)); err != nil {
		return Node{}, err
	}
	cleanupInstance = true

	if err := s.store.SaveNode(node, bootstrap, template); err != nil {
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

	return s.reconcileNodes(ctx, nodes, false)
}

func (s *Service) NodeCleanupIncomplete(apply bool) (IncompleteNodeCleanupResult, error) {
	// A dry run only inspects the home, so it stays on the read tier and never
	// requires a runtime backend. Applying can tear down live runtime instances,
	// so it takes the full write-readiness path (seed/repair under locks plus
	// runtime-dependency validation, which requires limactl).
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
	// still carries its lima-instance.ref, so os.RemoveAll'ing it while the
	// instance is live orphans a running VM and loses the only pointer back to
	// it (TODO #10). Tear the instance down first; only then is removing the
	// metadata that references it safe. Dirs with no matching live instance keep
	// the historical behavior of a straight metadata removal.
	observations, err := s.lima.List(context.Background())
	if err != nil {
		return IncompleteNodeCleanupResult{}, err
	}

	var teardownFailures []string
	for _, item := range items {
		if instanceName := strings.TrimSpace(item.InstanceName); instanceName != "" {
			if _, live := findObservation(observations, instanceName); live {
				if delErr := s.lima.Delete(context.Background(), Project{}, Node{LimaInstanceName: instanceName}); delErr != nil {
					// Leave the dir (and its ref) in place so a retry or a
					// manual limactl delete can still find the instance.
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
	return s.reconcileNode(ctx, node, false)
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

	project, err := s.store.ProjectByID(node.ProjectID)
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
		s.logLima("start", node.ID)
		if err := s.lima.Start(ctx, project, node); err != nil {
			return Node{}, err
		}
	}

	now := s.now()
	node.Status = NodeStatusProvisioning
	node.UpdatedAt = now
	if err := s.store.SaveNode(node, bootstrap, nil); err != nil {
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
				s.recordNodeStartRollback(node, bootstrap, Event{Timestamp: s.now(), Type: "node.start.failed", Fields: map[string]any{"workspace_path": project.WorkspacePath, "error": err.Error()}})
				return Node{}, err
			}

			node.WorkspaceSeeded = true
			node.UpdatedAt = s.now()
			if err := s.store.SaveNode(node, bootstrap, nil); err != nil {
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
	if err := s.store.SaveNode(node, bootstrap, nil); err != nil {
		return Node{}, err
	}

	if err := s.store.AppendNodeEvent(node.ID, Event{Timestamp: node.UpdatedAt, Type: "node.started"}); err != nil {
		return Node{}, err
	}

	return s.reconcileNode(ctx, node, true)
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

	project, err := s.store.ProjectByID(node.ProjectID)
	if err != nil {
		return Node{}, err
	}

	if node.LastRuntimeObservation != nil && node.LastRuntimeObservation.Status != "running" {
		node.Status = NodeStatusStopped
		node.UpdatedAt = s.now()
		if err := s.store.SaveNode(node, bootstrap, nil); err != nil {
			return Node{}, err
		}
		return node, nil
	}

	s.logLima("stop", node.ID)
	if err := s.lima.Stop(ctx, project, node); err != nil {
		return Node{}, err
	}

	node.Status = NodeStatusStopped
	node.UpdatedAt = s.now()
	if err := s.store.SaveNode(node, bootstrap, nil); err != nil {
		return Node{}, err
	}

	if err := s.store.AppendNodeEvent(node.ID, Event{Timestamp: node.UpdatedAt, Type: "node.stopped"}); err != nil {
		return Node{}, err
	}

	return s.reconcileNode(ctx, node, true)
}

func (s *Service) NodeClone(ctx context.Context, input NodeCloneInput) (childNode Node, err error) {
	s.log().Debug("operation started", "op", "node.clone", "source", input.SourceNode)
	defer s.logOperation("node.clone", s.now(), &err)

	if err := s.ensureReady(ctx, true); err != nil {
		return Node{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "projects", "nodes")
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

	sourceNode, err = s.reconcileNode(ctx, sourceNode, false)
	if err != nil {
		return Node{}, err
	}

	if input.AgentProfile != "" && input.AgentProfile != sourceNode.AgentProfileName {
		return Node{}, preconditionFailed("node clone copies the source VM and does not support agent profile overrides", map[string]any{"source_node_id": sourceNode.ID, "agent_profile_name": input.AgentProfile})
	}

	sourceProject, err := s.store.ProjectByID(sourceNode.ProjectID)
	if err != nil {
		return Node{}, err
	}

	sourceBootstrap, err := s.store.LoadBootstrapState(sourceNode.ID)
	if err != nil {
		return Node{}, err
	}

	sourceWasRunning := sourceNode.LastRuntimeObservation != nil && sourceNode.LastRuntimeObservation.Status == "running"
	if sourceWasRunning {
		if err := s.lima.Stop(ctx, sourceProject, sourceNode); err != nil {
			return Node{}, err
		}
	}
	defer func() {
		if !sourceWasRunning {
			return
		}

		if restartErr := s.lima.Start(ctx, sourceProject, sourceNode); restartErr != nil {
			err = errors.Join(err, restartErr)
			return
		}

		if _, reconcileErr := s.reconcileNode(ctx, sourceNode, true); reconcileErr != nil {
			err = errors.Join(err, reconcileErr)
		}
	}()

	childNodeSlug := coalesce(input.NodeSlug, slugify(sourceNode.Slug+"-clone"))
	if err := s.ensureUniqueNodeSlug(childNodeSlug); err != nil {
		return Node{}, err
	}

	nodeID := newID()
	instanceName, err := s.generateInstanceName(childNodeSlug)
	if err != nil {
		return Node{}, err
	}

	bootstrap := sourceBootstrap
	bootstrap.InstallCommands = append([]string(nil), sourceBootstrap.InstallCommands...)
	bootstrap.BootstrapCommands = append([]string(nil), sourceBootstrap.BootstrapCommands...)
	bootstrap.Environment = cloneMap(sourceBootstrap.Environment)

	childNode = Node{
		ID:                    nodeID,
		Slug:                  childNodeSlug,
		ProjectID:             sourceProject.ID,
		ParentNodeID:          sourceNode.ID,
		Runtime:               RuntimeVM,
		Provider:              ProviderLima,
		LimaInstanceName:      instanceName,
		Status:                NodeStatusCreated,
		AgentProfileName:      sourceNode.AgentProfileName,
		LimaCommands:          input.LimaCommands.ApplyDefaults(sourceNode.LimaCommands),
		BootstrapCommands:     append([]string(nil), sourceNode.BootstrapCommands...),
		GeneratedTemplatePath: s.store.nodeTemplatePath(nodeID),
		WorkspaceMode:         nodeWorkspaceMode(sourceNode),
		GuestWorkspacePath:    sourceNode.GuestWorkspacePath,
		WorkspaceMountPath:    sourceNode.WorkspaceMountPath,
		WorkspaceSeeded:       sourceNode.WorkspaceSeeded,
		BootstrapCompleted:    bootstrap.Completed,
		BootstrapCompletedAt:  bootstrap.CompletedAt,
		CreatedAt:             s.now(),
		UpdatedAt:             s.now(),
	}

	s.logLima("clone", childNode.ID)
	if err := s.lima.Clone(ctx, sourceProject, sourceNode, childNode); err != nil {
		return Node{}, err
	}

	template, err := s.renderTemplate(ctx, sourceProject, childNode, bootstrap, nodeWorkspaceMode(sourceNode))
	if err != nil {
		return Node{}, err
	}

	reconciledChildNode, err := s.reconcileNode(ctx, childNode, false)
	if err != nil {
		return Node{}, err
	}
	if reconciledChildNode.LastRuntimeObservation != nil && reconciledChildNode.LastRuntimeObservation.Status == "running" {
		if err := s.lima.Stop(ctx, sourceProject, childNode); err != nil {
			return Node{}, err
		}
		reconciledChildNode, err = s.reconcileNode(ctx, childNode, false)
		if err != nil {
			return Node{}, err
		}
	}
	childNode = reconciledChildNode

	if err := s.store.SaveNode(childNode, bootstrap, template); err != nil {
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

	bootstrap, err := s.store.LoadBootstrapState(node.ID)
	if err != nil {
		return Node{}, err
	}

	now := s.now()
	node.Status = NodeStatusTerminating
	node.UpdatedAt = now
	if err := s.store.SaveNode(node, bootstrap, nil); err != nil {
		return Node{}, err
	}

	project, err := s.store.ProjectByID(node.ProjectID)
	if err != nil {
		return Node{}, err
	}

	if err := s.lima.Delete(ctx, project, node); err != nil {
		return Node{}, err
	}

	deletedAt := s.now()
	node.Status = NodeStatusTerminated
	node.UpdatedAt = deletedAt
	node.DeletedAt = &deletedAt
	if err := s.store.SaveNode(node, bootstrap, nil); err != nil {
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

	project, err := s.store.ProjectByID(node.ProjectID)
	if err != nil {
		return err
	}

	command = normalizeShellCommand(command)
	workdir := s.nodeGuestWorkspacePath(node)
	interactive := len(command) == 0
	if interactive {
		command = interactiveShellLaunchCommand()
	}
	return s.lima.Shell(ctx, project, node, command, workdir, interactive, ShellStreams{
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
//   - ProjectHostShell is an interactive login shell rooted at the project's
//     host workspace. The workspace path is validated here (non-empty, exists,
//     is a directory); a failure is returned as a typed InvalidArgument error
//     that the caller records against the target.
//
// The caller supplies the already-resolved project workspace path (node shells
// pass ""); resolution of the project/node from the store stays with the caller
// — the TUI store today, the daemon with its own lock discipline tomorrow —
// which keeps this a pure, store-free spec builder.
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
	case terminal.ProjectHostShell:
		if strings.TrimSpace(workspacePath) == "" {
			return LaunchSpec{}, invalidArgument("project workspace path is not configured", map[string]any{"target": target.String()})
		}
		info, err := os.Stat(workspacePath)
		if err != nil {
			return LaunchSpec{}, invalidArgument("project workspace path is unavailable", map[string]any{"target": target.String(), "workspace_path": workspacePath, "error": err.Error()})
		}
		if !info.IsDir() {
			return LaunchSpec{}, invalidArgument("project workspace path is not a directory", map[string]any{"target": target.String(), "workspace_path": workspacePath})
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

func (s *Service) resolveProjectBootstrapCommands(project Project, nodeCommands LimaCommandTemplates) ([]string, error) {
	commands := []string{}
	for _, slug := range project.EnvironmentConfigs {
		config, err := s.store.EnvironmentConfigByIDOrSlug(slug)
		if err != nil {
			return nil, err
		}
		if config.DeletedAt != nil {
			return nil, notFound("environment config not found", map[string]any{"query": slug})
		}
		commands = append(commands, config.BootstrapCommands...)
	}

	resolved, err := resolveConfiguredLimaCommands("limactl", s.cfg.LimaCommands, project, nodeCommands, limaCommandBootstrap, nil)
	if err != nil {
		return nil, err
	}

	commands = append(commands, resolved...)
	return commands, nil
}

func (s *Service) generateInstanceName(nodeSlug string) (string, error) {
	instanceName := slugify(nodeSlug)
	if len(instanceName) > 63 {
		instanceName = instanceName[:63]
		instanceName = strings.Trim(instanceName, "-")
	}
	if instanceName == "" {
		instanceName = "node"
	}

	nodes, err := s.store.ListNodes(false)
	if err != nil {
		return "", err
	}

	for _, node := range nodes {
		if node.LimaInstanceName == instanceName && node.Status != NodeStatusTerminated {
			return "", preconditionFailed("lima instance name already exists", map[string]any{"instance_name": instanceName})
		}
	}

	return instanceName, nil
}

func (s *Service) renderTemplate(ctx context.Context, project Project, node Node, bootstrap BootstrapState, workspaceMode string) ([]byte, error) {
	rawTemplate, err := s.lima.BaseTemplate(ctx, project, node.LimaCommands, project.DefaultLimaTemplate)
	if err != nil {
		return nil, err
	}

	document := map[string]any{}
	if err := yaml.Unmarshal(rawTemplate, &document); err != nil {
		return nil, metadataCorruption("failed to parse base lima template", err, nil)
	}

	delete(document, "cpus")
	delete(document, "memory")
	delete(document, "disk")
	document["provision"] = appendLimaProvision(document["provision"], map[string]any{
		"mode":   "system",
		"script": nodeHostnameProvisionScript(node.LimaInstanceName),
	})
	document["mounts"] = renderWorkspaceMounts(project.WorkspacePath, workspaceMode)

	templateBytes, err := yaml.Marshal(document)
	if err != nil {
		return nil, err
	}

	return append(templateBytes, []byte(bootstrapComment(bootstrap))...), nil
}

func appendLimaProvision(existing any, provision map[string]any) []any {
	provisions := []any{}
	if values, ok := existing.([]any); ok {
		provisions = append(provisions, values...)
	}
	return append(provisions, provision)
}

func nodeHostnameProvisionScript(hostname string) string {
	quotedHostname := shellQuote(hostname)
	return fmt.Sprintf(`#!/bin/sh
set -eu
if command -v hostnamectl >/dev/null 2>&1; then
	hostnamectl set-hostname %[1]s
else
	hostname %[1]s
	printf '%%s\n' %[1]s > /etc/hostname
fi
`, quotedHostname)
}

func (s *Service) runGuestCommand(ctx context.Context, node Node, command string) error {
	if strings.TrimSpace(command) == "" {
		return nil
	}

	project, err := s.store.ProjectByID(node.ProjectID)
	if err != nil {
		return err
	}

	workdir := s.nodeGuestWorkspacePath(node)
	script := command
	if workdir != "" {
		script = fmt.Sprintf("cd %q && %s", workdir, command)
	}
	return s.lima.Shell(ctx, project, node, []string{"sh", "-lc", script}, workdir, false, ShellStreams{})
}

func (s *Service) prepareGuestWorkspace(ctx context.Context, project Project, node Node) error {
	if err := s.ensureProjectWorkspaceAvailable(project); err != nil {
		return err
	}

	if nodeWorkspaceMode(node) == WorkspaceModeMounted {
		return nil
	}

	return s.seedGuestWorkspace(ctx, project, node)
}

func (s *Service) seedGuestWorkspace(ctx context.Context, project Project, node Node) error {
	targetPath := s.nodeGuestWorkspacePath(node)
	prepareScript, err := s.resolveWorkspaceSeedPrepareCommand(project, node, project.WorkspacePath, targetPath)
	if err != nil {
		return err
	}

	if err := s.lima.Shell(ctx, project, node, []string{"sh", "-lc", prepareScript}, "", false, ShellStreams{}); err != nil {
		return err
	}

	return s.lima.CopyToGuest(ctx, project, node, project.WorkspacePath, targetPath, true)
}

func (s *Service) resolveWorkspaceSeedPrepareCommand(project Project, node Node, sourcePath, targetPath string) (string, error) {
	commands, err := resolveConfiguredLimaCommands("limactl", s.cfg.LimaCommands, project, node.LimaCommands, limaCommandWorkspaceSeedPrepare, map[string]string{
		"instance_name": shellQuote(node.LimaInstanceName),
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

	project, err := s.store.ProjectByID(node.ProjectID)
	if err != nil {
		return ""
	}

	return project.WorkspacePath
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

func renderWorkspaceMounts(workspacePath, workspaceMode string) []map[string]any {
	if normalizeWorkspaceMode(workspaceMode) != WorkspaceModeMounted || strings.TrimSpace(workspacePath) == "" {
		return []map[string]any{}
	}

	return []map[string]any{
		{
			"location":   workspacePath,
			"mountPoint": workspacePath,
			"writable":   true,
		},
	}
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
	observations, err := s.lima.List(ctx)
	if err != nil {
		return Node{}, err
	}

	return s.reconcileNodeWithObservations(node, observations, persist, s.now())
}

func (s *Service) reconcileNodes(ctx context.Context, nodes []Node, persist bool) ([]Node, error) {
	observations, err := s.lima.List(ctx)
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
	observation, ok := findObservation(observations, node.LimaInstanceName)
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
		node.LastRuntimeObservation = &RuntimeObservation{Name: node.LimaInstanceName, Exists: false}
	}

	if persist {
		bootstrap, bootstrapErr := s.store.LoadBootstrapState(node.ID)
		if bootstrapErr != nil {
			return Node{}, bootstrapErr
		}

		node.UpdatedAt = now
		if saveErr := s.store.SaveNode(node, bootstrap, nil); saveErr != nil {
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
