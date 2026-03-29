package codelima

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Service struct {
	cfg    Config
	store  *Store
	lima   LimaClient
	tui    TUIRunner
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	now    func() time.Time
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

type PatchProposeInput struct {
	SourceProject string
	SourceNode    string
	TargetProject string
	TargetNode    string
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
	}
}

func (s *Service) TUI(ctx context.Context) error {
	if s.tui == nil {
		s.tui = newTUIRunner()
	}

	return s.tui.Run(ctx, s)
}

func (s *Service) EnsureReady(mutating bool) error {
	if err := s.store.EnsureLayout(); err != nil {
		return err
	}

	if mutating {
		if err := s.validateDependencies(); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) validateDependencies() error {
	if _, err := exec.LookPath("git"); err != nil {
		return dependencyUnavailable("git is required", err, nil)
	}

	if _, err := exec.LookPath("limactl"); err != nil {
		if _, ok := s.lima.(*ExecLimaClient); ok {
			return dependencyUnavailable("limactl is required", err, nil)
		}
	}

	if _, err := s.lima.List(context.Background()); err != nil {
		return err
	}

	return nil
}

func (s *Service) Doctor(ctx context.Context) (DoctorReport, error) {
	if err := s.store.EnsureLayout(); err != nil {
		return DoctorReport{}, err
	}

	report := DoctorReport{
		Checks: []DoctorCheck{},
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

	if orphans, err := s.store.OrphanedPatchStatusIndexes(); err != nil {
		return DoctorReport{}, err
	} else {
		for _, orphan := range orphans {
			report.Warnings = append(report.Warnings, "orphaned patch status index: "+orphan)
		}
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
	if err := s.EnsureReady(false); err != nil {
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
	if err := s.EnsureReady(false); err != nil {
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

func (s *Service) ProjectTree(rootQuery string, includeDeleted bool) ([]ProjectTreeNode, error) {
	if err := s.EnsureReady(false); err != nil {
		return nil, err
	}

	projects, err := s.store.ListProjects(includeDeleted)
	if err != nil {
		return nil, err
	}

	nodes, err := s.store.ListNodes(includeDeleted)
	if err != nil {
		return nil, err
	}

	projectMap := map[string]Project{}
	childrenByParent := map[string][]Project{}
	for _, project := range projects {
		projectMap[project.ID] = project
		childrenByParent[project.ParentProjectID] = append(childrenByParent[project.ParentProjectID], project)
	}

	nodesByProject := map[string][]Node{}
	for _, node := range nodes {
		nodesByProject[node.ProjectID] = append(nodesByProject[node.ProjectID], node)
	}

	var roots []Project
	if rootQuery != "" {
		project, err := s.store.ProjectByIDOrSlug(rootQuery)
		if err != nil {
			return nil, err
		}
		roots = []Project{project}
	} else {
		roots = childrenByParent[""]
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

	return result, nil
}

func (s *Service) ProjectFork(ctx context.Context, input ProjectForkInput) (Project, error) {
	if err := s.EnsureReady(true); err != nil {
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
	if err := s.EnsureReady(true); err != nil {
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

	instanceName, err := s.generateInstanceName(project.Slug, nodeSlug, nodeID)
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

func (s *Service) NodeList(includeDeleted bool) ([]Node, error) {
	if err := s.EnsureReady(false); err != nil {
		return nil, err
	}

	return s.store.ListNodes(includeDeleted)
}

func (s *Service) NodeCleanupIncomplete(apply bool) (IncompleteNodeCleanupResult, error) {
	if err := s.EnsureReady(false); err != nil {
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

	if apply && len(items) > 0 {
		if err := s.store.RemoveIncompleteNodeMetadata(items); err != nil {
			return IncompleteNodeCleanupResult{}, err
		}
	}

	return IncompleteNodeCleanupResult{
		DryRun: !apply,
		Items:  items,
	}, nil
}

func (s *Service) NodeShow(ctx context.Context, value string) (Node, error) {
	if err := s.EnsureReady(false); err != nil {
		return Node{}, err
	}

	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return Node{}, err
	}

	return s.reconcileNode(ctx, node, true)
}

func (s *Service) NodeStart(ctx context.Context, value string) (Node, error) {
	if err := s.EnsureReady(true); err != nil {
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
			seedSnapshotID := ""
			if nodeWorkspaceMode(node) == WorkspaceModeCopy {
				seedSnapshot, err := s.captureStoredSnapshot(project, project.WorkspacePath, newID(), "workspace_seed")
				if err != nil {
					node.Status = NodeStatusFailed
					node.UpdatedAt = s.now()
					_ = s.store.SaveNode(node, bootstrap, nil)
					return Node{}, err
				}
				seedSnapshotID = seedSnapshot.ID
			}

			if err := s.prepareGuestWorkspace(ctx, project, node); err != nil {
				node.Status = NodeStatusFailed
				node.UpdatedAt = s.now()
				_ = s.store.SaveNode(node, bootstrap, nil)
				_ = s.store.AppendNodeEvent(node.ID, Event{Timestamp: s.now(), Type: "node.start.failed", Fields: map[string]any{"workspace_path": project.WorkspacePath, "error": err.Error()}})
				return Node{}, err
			}

			node.WorkspaceSeeded = true
			node.WorkspaceSeedSnapshotID = seedSnapshotID
			node.UpdatedAt = s.now()
			if err := s.store.SaveNode(node, bootstrap, nil); err != nil {
				return Node{}, err
			}
		}

		for _, command := range bootstrap.CombinedCommands() {
			if err := s.runGuestCommand(ctx, node, command); err != nil {
				node.Status = NodeStatusFailed
				node.UpdatedAt = s.now()
				_ = s.store.SaveNode(node, bootstrap, nil)
				_ = s.store.AppendNodeEvent(node.ID, Event{Timestamp: s.now(), Type: "node.start.failed", Fields: map[string]any{"command": command, "error": err.Error()}})
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
		_ = s.store.SaveNode(node, bootstrap, nil)
		_ = s.store.AppendNodeEvent(node.ID, Event{Timestamp: s.now(), Type: "node.start.failed", Fields: map[string]any{"validation_command": bootstrap.ValidationCommand, "error": err.Error()}})
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

func (s *Service) NodeStop(ctx context.Context, value string) (Node, error) {
	if err := s.EnsureReady(true); err != nil {
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
	if err := s.EnsureReady(true); err != nil {
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
	instanceName, err := s.generateInstanceName(sourceProject.Slug, childNodeSlug, nodeID)
	if err != nil {
		return Node{}, err
	}

	bootstrap := sourceBootstrap
	bootstrap.InstallCommands = append([]string(nil), sourceBootstrap.InstallCommands...)
	bootstrap.BootstrapCommands = append([]string(nil), sourceBootstrap.BootstrapCommands...)
	bootstrap.Environment = cloneMap(sourceBootstrap.Environment)

	childNode = Node{
		ID:                      nodeID,
		Slug:                    childNodeSlug,
		ProjectID:               sourceProject.ID,
		ParentNodeID:            sourceNode.ID,
		Runtime:                 RuntimeVM,
		Provider:                ProviderLima,
		LimaInstanceName:        instanceName,
		Status:                  NodeStatusCreated,
		AgentProfileName:        sourceNode.AgentProfileName,
		LimaCommands:            input.LimaCommands.ApplyDefaults(sourceNode.LimaCommands),
		BootstrapCommands:       append([]string(nil), sourceNode.BootstrapCommands...),
		GeneratedTemplatePath:   s.store.nodeTemplatePath(nodeID),
		WorkspaceMode:           nodeWorkspaceMode(sourceNode),
		GuestWorkspacePath:      sourceNode.GuestWorkspacePath,
		WorkspaceMountPath:      sourceNode.WorkspaceMountPath,
		WorkspaceSeeded:         sourceNode.WorkspaceSeeded,
		WorkspaceSeedSnapshotID: sourceNode.WorkspaceSeedSnapshotID,
		BootstrapCompleted:      bootstrap.Completed,
		BootstrapCompletedAt:    bootstrap.CompletedAt,
		CreatedAt:               s.now(),
		UpdatedAt:               s.now(),
	}

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
	if err := s.EnsureReady(true); err != nil {
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

func (s *Service) NodeSync(ctx context.Context, value string, apply bool) (NodeSyncResult, error) {
	if err := s.EnsureReady(true); err != nil {
		return NodeSyncResult{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "projects", "nodes")
	if err != nil {
		return NodeSyncResult{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return NodeSyncResult{}, err
	}

	project, err := s.store.ProjectByID(node.ProjectID)
	if err != nil {
		return NodeSyncResult{}, err
	}

	result := NodeSyncResult{
		NodeID:             node.ID,
		NodeSlug:           node.Slug,
		ProjectID:          project.ID,
		ProjectSlug:        project.Slug,
		WorkspaceMode:      nodeWorkspaceMode(node),
		GuestWorkspacePath: s.nodeGuestWorkspacePath(node),
		HostWorkspacePath:  project.WorkspacePath,
		DryRun:             !apply,
		DiffSummary:        DiffSummary{Paths: []string{}},
	}

	if result.WorkspaceMode != WorkspaceModeCopy {
		return NodeSyncResult{}, preconditionFailed("node sync is only available for copy-mode nodes", map[string]any{"node_id": node.ID, "workspace_mode": result.WorkspaceMode})
	}
	if !node.WorkspaceSeeded {
		return NodeSyncResult{}, preconditionFailed("node sync requires a seeded copy-mode node; start the node first", map[string]any{"node_id": node.ID})
	}
	if node.WorkspaceSeedSnapshotID == "" {
		return NodeSyncResult{}, preconditionFailed("node sync requires a workspace seed snapshot; recreate or reseed the node", map[string]any{"node_id": node.ID})
	}
	if err := s.ensureProjectWorkspaceAvailable(project); err != nil {
		return NodeSyncResult{}, err
	}

	node, err = s.reconcileNode(ctx, node, false)
	if err != nil {
		return NodeSyncResult{}, err
	}
	if node.LastRuntimeObservation == nil || node.LastRuntimeObservation.Status != "running" {
		return NodeSyncResult{}, preconditionFailed("node sync requires a running copy-mode node", map[string]any{"node_id": node.ID, "status": node.Status})
	}

	baseSnapshot, err := s.store.LoadSnapshot(project.ID, node.WorkspaceSeedSnapshotID)
	if err != nil {
		return NodeSyncResult{}, err
	}
	result.BaseSnapshotID = baseSnapshot.ID

	scratchDir, err := createTempDir(s.store.scratchRoot(), "node-sync-*")
	if err != nil {
		return NodeSyncResult{}, err
	}
	defer func() {
		_ = os.RemoveAll(scratchDir)
	}()

	pulledWorkspacePath := filepath.Join(scratchDir, "guest-workspace")
	if err := ensureDir(pulledWorkspacePath); err != nil {
		return NodeSyncResult{}, err
	}
	if err := s.lima.CopyFromGuest(ctx, project, node, s.nodeGuestWorkspacePath(node), pulledWorkspacePath, true); err != nil {
		return NodeSyncResult{}, err
	}

	guestProject := project
	guestProject.WorkspacePath = pulledWorkspacePath
	guestSnapshot, err := captureSnapshot(guestProject, newID(), "node_sync_source", filepath.Join(scratchDir, "guest-snapshot"), s.cfg.Snapshot.Excludes, s.now())
	if err != nil {
		return NodeSyncResult{}, err
	}

	patchBytes, summary, changed, err := buildPatchAllowNoop(ctx, s.store.scratchRoot(), baseSnapshot.TreeRoot, guestSnapshot.TreeRoot)
	if err != nil {
		return NodeSyncResult{}, err
	}
	result.DiffSummary = summary
	result.Changed = changed
	if !changed || !apply {
		return result, nil
	}

	currentTarget, err := captureSnapshot(project, newID(), "node_sync_target", filepath.Join(scratchDir, "target-snapshot"), s.cfg.Snapshot.Excludes, s.now())
	if err != nil {
		return NodeSyncResult{}, err
	}

	nextBaseSnapshot, err := s.captureStoredSnapshot(project, pulledWorkspacePath, newID(), "workspace_seed")
	if err != nil {
		return NodeSyncResult{}, err
	}

	patchPath := filepath.Join(scratchDir, "workspace.diff")
	if err := atomicWriteFile(patchPath, patchBytes, 0o644); err != nil {
		return NodeSyncResult{}, err
	}

	stageDir, err := applyPatchChecked(ctx, s.store.scratchRoot(), patchPath, currentTarget)
	if err != nil {
		return NodeSyncResult{}, err
	}
	defer func() {
		_ = os.RemoveAll(stageDir)
	}()

	if err := syncWorkspaceFromTree(currentTarget, stageDir, project.WorkspacePath); err != nil {
		_ = restoreWorkspace(currentTarget, project.WorkspacePath)
		return NodeSyncResult{}, err
	}

	bootstrap, err := s.store.LoadBootstrapState(node.ID)
	if err != nil {
		return NodeSyncResult{}, err
	}

	node.WorkspaceSeedSnapshotID = nextBaseSnapshot.ID
	node.UpdatedAt = s.now()
	if err := s.store.SaveNode(node, bootstrap, nil); err != nil {
		return NodeSyncResult{}, err
	}

	if err := s.store.AppendNodeEvent(node.ID, Event{Timestamp: node.UpdatedAt, Type: "node.sync.applied", Fields: map[string]any{"files_changed": summary.FilesChanged, "base_snapshot_id": baseSnapshot.ID, "next_base_snapshot_id": nextBaseSnapshot.ID}}); err != nil {
		return NodeSyncResult{}, err
	}

	result.DryRun = false
	result.Applied = true
	return result, nil
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

func (s *Service) PatchPropose(ctx context.Context, input PatchProposeInput) (PatchProposal, error) {
	if err := s.EnsureReady(true); err != nil {
		return PatchProposal{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "projects", "patches")
	if err != nil {
		return PatchProposal{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	source, err := s.store.ProjectByIDOrSlug(input.SourceProject)
	if err != nil {
		return PatchProposal{}, err
	}

	target, err := s.store.ProjectByIDOrSlug(input.TargetProject)
	if err != nil {
		return PatchProposal{}, err
	}

	direction, baseProject, baseSnapshotID, err := resolveLineageEdge(source, target)
	if err != nil {
		return PatchProposal{}, err
	}

	baseSnapshot, err := s.store.LoadSnapshot(baseProject.ID, baseSnapshotID)
	if err != nil {
		return PatchProposal{}, err
	}

	now := s.now()
	sourceSnapshotID := newID()
	sourceSnapshot, err := captureSnapshot(source, sourceSnapshotID, "patch_source", s.store.snapshotTreePath(source.ID, sourceSnapshotID), s.cfg.Snapshot.Excludes, now)
	if err != nil {
		return PatchProposal{}, err
	}

	targetSnapshotID := newID()
	targetSnapshot, err := captureSnapshot(target, targetSnapshotID, "patch_target", s.store.snapshotTreePath(target.ID, targetSnapshotID), s.cfg.Snapshot.Excludes, now)
	if err != nil {
		return PatchProposal{}, err
	}

	if err := s.store.SaveSnapshot(source.ID, sourceSnapshot); err != nil {
		return PatchProposal{}, err
	}

	if err := s.store.SaveSnapshot(target.ID, targetSnapshot); err != nil {
		return PatchProposal{}, err
	}

	patchBytes, summary, err := buildPatch(ctx, s.store.scratchRoot(), baseSnapshot.TreeRoot, sourceSnapshot.TreeRoot)
	if err != nil {
		return PatchProposal{}, err
	}

	proposal := PatchProposal{
		ID:               newID(),
		Direction:        direction,
		SourceProjectID:  source.ID,
		TargetProjectID:  target.ID,
		BaseSnapshotID:   baseSnapshot.ID,
		SourceSnapshotID: sourceSnapshot.ID,
		TargetSnapshotID: targetSnapshot.ID,
		Status:           PatchStatusSubmitted,
		PatchPath:        s.store.patchDiffPath(newID()),
		DiffSummary:      summary,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if input.SourceNode != "" {
		sourceNode, err := s.store.NodeByIDOrSlug(input.SourceNode)
		if err != nil {
			return PatchProposal{}, err
		}
		proposal.SourceNodeID = sourceNode.ID
	}

	if input.TargetNode != "" {
		targetNode, err := s.store.NodeByIDOrSlug(input.TargetNode)
		if err != nil {
			return PatchProposal{}, err
		}
		proposal.TargetNodeID = targetNode.ID
	}

	proposal.PatchPath = s.store.patchDiffPath(proposal.ID)
	if err := s.store.SavePatch(proposal, patchBytes); err != nil {
		return PatchProposal{}, err
	}

	if err := s.store.AppendPatchEvent(proposal.ID, Event{Timestamp: now, Type: "patch.proposed"}); err != nil {
		return PatchProposal{}, err
	}

	return proposal, nil
}

func (s *Service) PatchList(status string) ([]PatchProposal, error) {
	if err := s.EnsureReady(false); err != nil {
		return nil, err
	}

	return s.store.ListPatches(status)
}

func (s *Service) PatchShow(value string) (PatchProposal, []Event, error) {
	if err := s.EnsureReady(false); err != nil {
		return PatchProposal{}, nil, err
	}

	proposal, err := s.store.PatchByID(value)
	if err != nil {
		return PatchProposal{}, nil, err
	}

	events, err := s.store.PatchEvents(proposal.ID)
	if err != nil {
		return PatchProposal{}, nil, err
	}

	return proposal, events, nil
}

func (s *Service) PatchApprove(value, actor, note string) (PatchProposal, error) {
	if err := s.EnsureReady(true); err != nil {
		return PatchProposal{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "patches")
	if err != nil {
		return PatchProposal{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	proposal, err := s.store.PatchByID(value)
	if err != nil {
		return PatchProposal{}, err
	}

	if proposal.Status != PatchStatusSubmitted {
		return PatchProposal{}, preconditionFailed("patch must be submitted before approval", map[string]any{"status": proposal.Status})
	}

	now := s.now()
	proposal.Status = PatchStatusApproved
	proposal.UpdatedAt = now
	proposal.Approval = &ApprovalMetadata{
		Actor:     actor,
		Timestamp: now,
		Note:      note,
	}

	if err := s.store.SavePatch(proposal, nil); err != nil {
		return PatchProposal{}, err
	}

	if err := s.store.AppendPatchEvent(proposal.ID, Event{Timestamp: now, Type: "patch.approved", Fields: map[string]any{"actor": actor}}); err != nil {
		return PatchProposal{}, err
	}

	return proposal, nil
}

func (s *Service) PatchReject(value, actor, note string) (PatchProposal, error) {
	if err := s.EnsureReady(true); err != nil {
		return PatchProposal{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "patches")
	if err != nil {
		return PatchProposal{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	proposal, err := s.store.PatchByID(value)
	if err != nil {
		return PatchProposal{}, err
	}

	if proposal.Status != PatchStatusSubmitted && proposal.Status != PatchStatusApproved {
		return PatchProposal{}, preconditionFailed("patch can only be rejected from submitted or approved", map[string]any{"status": proposal.Status})
	}

	proposal.Status = PatchStatusRejected
	proposal.UpdatedAt = s.now()
	if note != "" || actor != "" {
		proposal.Approval = &ApprovalMetadata{Actor: actor, Timestamp: s.now(), Note: note}
	}

	if err := s.store.SavePatch(proposal, nil); err != nil {
		return PatchProposal{}, err
	}

	if err := s.store.AppendPatchEvent(proposal.ID, Event{Timestamp: s.now(), Type: "patch.rejected", Fields: map[string]any{"actor": actor}}); err != nil {
		return PatchProposal{}, err
	}

	return proposal, nil
}

func (s *Service) PatchApply(ctx context.Context, value string) (PatchProposal, error) {
	if err := s.EnsureReady(true); err != nil {
		return PatchProposal{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "projects", "patches")
	if err != nil {
		return PatchProposal{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	proposal, err := s.store.PatchByID(value)
	if err != nil {
		return PatchProposal{}, err
	}

	if proposal.Status != PatchStatusApproved {
		return PatchProposal{}, preconditionFailed("patch must be approved before apply", map[string]any{"status": proposal.Status})
	}

	targetProject, err := s.store.ProjectByID(proposal.TargetProjectID)
	if err != nil {
		return PatchProposal{}, err
	}

	targetSnapshotID := newID()
	currentTarget, err := captureSnapshot(targetProject, targetSnapshotID, "patch_target", s.store.snapshotTreePath(targetProject.ID, targetSnapshotID), s.cfg.Snapshot.Excludes, s.now())
	if err != nil {
		return PatchProposal{}, err
	}

	if err := s.store.SaveSnapshot(targetProject.ID, currentTarget); err != nil {
		return PatchProposal{}, err
	}

	stageDir, err := applyPatchChecked(ctx, s.store.scratchRoot(), s.store.patchDiffPath(proposal.ID), currentTarget)
	if err != nil {
		proposal.Status = PatchStatusFailed
		proposal.UpdatedAt = s.now()
		proposal.ConflictSummary = &ConflictSummary{Message: "patch apply preflight failed", Details: err.Error()}
		if saveErr := s.store.SavePatch(proposal, nil); saveErr != nil {
			return PatchProposal{}, saveErr
		}
		_ = s.store.AppendPatchEvent(proposal.ID, Event{Timestamp: proposal.UpdatedAt, Type: "patch.apply.failed", Fields: map[string]any{"error": err.Error()}})
		return proposal, err
	}
	defer func() {
		_ = os.RemoveAll(stageDir)
	}()

	if err := syncWorkspaceFromTree(currentTarget, stageDir, targetProject.WorkspacePath); err != nil {
		_ = restoreWorkspace(currentTarget, targetProject.WorkspacePath)
		proposal.Status = PatchStatusFailed
		proposal.UpdatedAt = s.now()
		proposal.ApplyResult = &ApplyResult{
			AppliedAt:    proposal.UpdatedAt,
			RecoveryNote: "workspace was restored from pre-apply snapshot after promotion failure",
		}
		if saveErr := s.store.SavePatch(proposal, nil); saveErr != nil {
			return PatchProposal{}, saveErr
		}
		return PatchProposal{}, err
	}

	postSnapshotID := newID()
	postSnapshot, err := captureSnapshot(targetProject, postSnapshotID, "post_apply", s.store.snapshotTreePath(targetProject.ID, postSnapshotID), s.cfg.Snapshot.Excludes, s.now())
	if err != nil {
		return PatchProposal{}, err
	}

	if err := s.store.SaveSnapshot(targetProject.ID, postSnapshot); err != nil {
		return PatchProposal{}, err
	}

	proposal.Status = PatchStatusApplied
	proposal.UpdatedAt = s.now()
	proposal.ApplyResult = &ApplyResult{
		AppliedAt:      proposal.UpdatedAt,
		PostSnapshotID: postSnapshot.ID,
	}
	if err := s.store.SavePatch(proposal, nil); err != nil {
		return PatchProposal{}, err
	}

	if err := s.store.AppendPatchEvent(proposal.ID, Event{Timestamp: proposal.UpdatedAt, Type: "patch.applied", Fields: map[string]any{"post_snapshot_id": postSnapshot.ID}}); err != nil {
		return PatchProposal{}, err
	}

	return proposal, nil
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

func (s *Service) generateInstanceName(projectSlug, nodeSlug, nodeID string) (string, error) {
	prefix := fmt.Sprintf("%s-%s-%s", projectSlug, nodeSlug, shortID(nodeID))
	instanceName := slugify(prefix)
	if len(instanceName) > 63 {
		instanceName = instanceName[:63]
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
	document["mounts"] = renderWorkspaceMounts(project.WorkspacePath, workspaceMode)

	templateBytes, err := yaml.Marshal(document)
	if err != nil {
		return nil, err
	}

	return append(templateBytes, []byte(bootstrapComment(bootstrap))...), nil
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

func (s *Service) captureStoredSnapshot(project Project, workspacePath, snapshotID, kind string) (SnapshotManifest, error) {
	snapshotProject := project
	snapshotProject.WorkspacePath = workspacePath
	manifest, err := captureSnapshot(snapshotProject, snapshotID, kind, s.store.snapshotTreePath(project.ID, snapshotID), s.cfg.Snapshot.Excludes, s.now())
	if err != nil {
		return SnapshotManifest{}, err
	}

	if err := s.store.SaveSnapshot(project.ID, manifest); err != nil {
		return SnapshotManifest{}, err
	}

	return manifest, nil
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
	script := strings.Join([]string{
		`if [ -x /usr/bin/gnustty ] && /bin/stty --version 2>/dev/null | grep -qi 'uutils coreutils'; then`,
		`  sudo -n ln -sf /usr/bin/gnustty /bin/stty >/dev/null 2>&1 || true`,
		`  sudo -n ln -sf /usr/bin/gnustty /usr/bin/stty >/dev/null 2>&1 || true`,
		`fi`,
		`exec "${SHELL:-/bin/bash}" -l`,
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

	observation, ok := findObservation(observations, node.LimaInstanceName)
	now := s.now()
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

func resolveLineageEdge(source, target Project) (direction string, baseProject Project, baseSnapshotID string, err error) {
	switch {
	case source.ParentProjectID == target.ID:
		return PatchDirectionChildToParent, target, source.ForkBaseSnapshotID, nil
	case target.ParentProjectID == source.ID:
		return PatchDirectionParentToChild, source, target.ForkBaseSnapshotID, nil
	default:
		return "", Project{}, "", preconditionFailed("projects are not direct lineage neighbors", map[string]any{"source_project_id": source.ID, "target_project_id": target.ID})
	}
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
