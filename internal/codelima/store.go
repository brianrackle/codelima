package codelima

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Store struct {
	cfg Config
}

func NewStore(cfg Config) *Store {
	return &Store{cfg: cfg}
}

func (s *Store) EnsureLayout() error {
	if err := validateConfig(s.cfg); err != nil {
		return err
	}

	directories := []string{
		filepath.Join(s.cfg.MetadataRoot, "_config"),
		s.cfg.AgentProfilesDir,
		filepath.Join(s.cfg.MetadataRoot, "_locks"),
		filepath.Join(s.cfg.MetadataRoot, "_index", "environment-configs", "by-slug"),
		filepath.Join(s.cfg.MetadataRoot, "_index", "projects", "by-slug"),
		filepath.Join(s.cfg.MetadataRoot, "_index", "nodes", "by-instance"),
		filepath.Join(s.cfg.MetadataRoot, "_index", "patches", "by-status"),
		filepath.Join(s.cfg.MetadataRoot, "environment-configs"),
		filepath.Join(s.cfg.MetadataRoot, "projects"),
		filepath.Join(s.cfg.MetadataRoot, "nodes"),
		filepath.Join(s.cfg.MetadataRoot, "patches"),
	}

	for _, directory := range directories {
		if err := ensureDir(directory); err != nil {
			return err
		}
	}

	if !exists(s.configPath()) {
		data, err := defaultConfigYAML(s.cfg)
		if err != nil {
			return err
		}

		if err := atomicWriteFile(s.configPath(), data, 0o644); err != nil {
			return err
		}
	}

	for name, profile := range builtInProfiles() {
		profilePath := s.agentProfilePath(name)
		if exists(profilePath) {
			continue
		}

		if err := writeYAMLFile(profilePath, profile); err != nil {
			return err
		}
	}

	if err := s.ensureBuiltInEnvironmentConfigs(time.Now().UTC()); err != nil {
		return err
	}

	return nil
}

func (s *Store) ensureBuiltInEnvironmentConfigs(createdAt time.Time) error {
	for _, spec := range builtInEnvironmentConfigs() {
		config, err := s.EnvironmentConfigByIDOrSlug(spec.Slug)
		if err == nil {
			if config.DeletedAt != nil {
				continue
			}
			continue
		}

		var appErr *AppError
		if !As(err, &appErr) || appErr.Category != "NotFound" {
			return err
		}

		if err := s.SaveEnvironmentConfig(EnvironmentConfig{
			ID:        newID(),
			Slug:      spec.Slug,
			Commands:  append([]string(nil), spec.Commands...),
			CreatedAt: createdAt,
			UpdatedAt: createdAt,
		}); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) configPath() string {
	return filepath.Join(s.cfg.MetadataRoot, "_config", "config.yaml")
}

func (s *Store) agentProfilePath(name string) string {
	return filepath.Join(s.cfg.AgentProfilesDir, name+".yaml")
}

func (s *Store) projectDir(projectID string) string {
	return filepath.Join(s.cfg.MetadataRoot, "projects", projectID)
}

func (s *Store) projectPath(projectID string) string {
	return filepath.Join(s.projectDir(projectID), "project.yaml")
}

func (s *Store) projectEventsPath(projectID string) string {
	return filepath.Join(s.projectDir(projectID), "events.jsonl")
}

func (s *Store) projectSnapshotsDir(projectID string) string {
	return filepath.Join(s.projectDir(projectID), "snapshots")
}

func (s *Store) snapshotDir(projectID, snapshotID string) string {
	return filepath.Join(s.projectSnapshotsDir(projectID), snapshotID)
}

func (s *Store) snapshotManifestPath(projectID, snapshotID string) string {
	return filepath.Join(s.snapshotDir(projectID, snapshotID), "manifest.json")
}

func (s *Store) snapshotTreePath(projectID, snapshotID string) string {
	return filepath.Join(s.snapshotDir(projectID, snapshotID), "tree")
}

func (s *Store) nodeDir(nodeID string) string {
	return filepath.Join(s.cfg.MetadataRoot, "nodes", nodeID)
}

func (s *Store) nodePath(nodeID string) string {
	return filepath.Join(s.nodeDir(nodeID), "node.yaml")
}

func (s *Store) nodeMetadataExists(nodeID string) bool {
	return exists(s.nodePath(nodeID))
}

func (s *Store) nodeEventsPath(nodeID string) string {
	return filepath.Join(s.nodeDir(nodeID), "events.jsonl")
}

func (s *Store) nodeContextPath(nodeID string) string {
	return filepath.Join(s.nodeDir(nodeID), "context.jsonl")
}

func (s *Store) nodeTemplatePath(nodeID string) string {
	return filepath.Join(s.nodeDir(nodeID), "instance.lima.yaml")
}

func (s *Store) nodeBootstrapPath(nodeID string) string {
	return filepath.Join(s.nodeDir(nodeID), "bootstrap.json")
}

func (s *Store) nodeInstanceRefPath(nodeID string) string {
	return filepath.Join(s.nodeDir(nodeID), "lima-instance.ref")
}

func (s *Store) patchDir(patchID string) string {
	return filepath.Join(s.cfg.MetadataRoot, "patches", patchID)
}

func (s *Store) patchProposalPath(patchID string) string {
	return filepath.Join(s.patchDir(patchID), "proposal.yaml")
}

func (s *Store) patchEventsPath(patchID string) string {
	return filepath.Join(s.patchDir(patchID), "events.jsonl")
}

func (s *Store) patchDiffPath(patchID string) string {
	return filepath.Join(s.patchDir(patchID), "patch.diff")
}

func (s *Store) patchSummaryPath(patchID string) string {
	return filepath.Join(s.patchDir(patchID), "summary.json")
}

func (s *Store) patchConflictsPath(patchID string) string {
	return filepath.Join(s.patchDir(patchID), "conflicts.json")
}

func (s *Store) patchApplyResultPath(patchID string) string {
	return filepath.Join(s.patchDir(patchID), "apply-result.json")
}

func (s *Store) projectSlugIndexPath(slug string) string {
	return filepath.Join(s.cfg.MetadataRoot, "_index", "projects", "by-slug", slug)
}

func (s *Store) nodeInstanceIndexPath(instanceName string) string {
	return filepath.Join(s.cfg.MetadataRoot, "_index", "nodes", "by-instance", instanceName)
}

func (s *Store) patchStatusIndexPath(status, patchID string) string {
	return filepath.Join(s.cfg.MetadataRoot, "_index", "patches", "by-status", status, patchID+".ref")
}

func (s *Store) LoadAgentProfile(name string) (AgentProfile, error) {
	var profile AgentProfile
	path := s.agentProfilePath(name)
	if err := readYAMLFile(path, &profile); err != nil {
		if os.IsNotExist(err) {
			return AgentProfile{}, notFound("agent profile not found", map[string]any{"name": name})
		}

		return AgentProfile{}, metadataCorruption("failed to load agent profile", err, map[string]any{"path": path})
	}

	return profile, nil
}

func (s *Store) SaveProject(project Project) error {
	if err := ensureDir(s.projectDir(project.ID)); err != nil {
		return err
	}

	var previous *Project
	if loaded, err := s.ProjectByID(project.ID); err == nil {
		previous = &loaded
	}

	if err := writeYAMLFile(s.projectPath(project.ID), project); err != nil {
		return err
	}

	if err := ensureDir(s.projectSnapshotsDir(project.ID)); err != nil {
		return err
	}

	if previous != nil && previous.Slug != project.Slug {
		_ = os.Remove(s.projectSlugIndexPath(previous.Slug))
	}

	if project.DeletedAt == nil {
		if err := atomicWriteFile(s.projectSlugIndexPath(project.Slug), []byte(project.ID+"\n"), 0o644); err != nil {
			return err
		}
	} else {
		_ = os.Remove(s.projectSlugIndexPath(project.Slug))
	}

	return nil
}

func (s *Store) SaveSnapshot(projectID string, manifest SnapshotManifest) error {
	snapshotDir := s.snapshotDir(projectID, manifest.ID)
	if err := ensureDir(snapshotDir); err != nil {
		return err
	}

	return writeJSONFile(s.snapshotManifestPath(projectID, manifest.ID), manifest)
}

func (s *Store) ProjectByID(projectID string) (Project, error) {
	var project Project
	path := s.projectPath(projectID)
	if err := readYAMLFile(path, &project); err != nil {
		if os.IsNotExist(err) {
			return Project{}, notFound("project not found", map[string]any{"id": projectID})
		}

		return Project{}, metadataCorruption("failed to load project", err, map[string]any{"path": path})
	}

	return project, nil
}

func (s *Store) ProjectByIDOrSlug(value string) (Project, error) {
	if exists(s.projectPath(value)) {
		return s.ProjectByID(value)
	}

	indexPath := s.projectSlugIndexPath(value)
	if exists(indexPath) {
		data, err := os.ReadFile(indexPath)
		if err != nil {
			return Project{}, metadataCorruption("failed to read project slug index", err, map[string]any{"path": indexPath})
		}

		return s.ProjectByID(strings.TrimSpace(string(data)))
	}

	projects, err := s.ListProjects(false)
	if err != nil {
		return Project{}, err
	}

	for _, project := range projects {
		if project.Slug == value {
			return project, nil
		}
	}

	projects, err = s.ListProjects(true)
	if err != nil {
		return Project{}, err
	}

	var deletedMatch *Project
	for _, project := range projects {
		if project.Slug == value {
			projectCopy := project
			deletedMatch = &projectCopy
		}
	}

	if deletedMatch != nil {
		return *deletedMatch, nil
	}

	return Project{}, notFound("project not found", map[string]any{"query": value})
}

func (s *Store) ProjectByWorkspacePath(workspacePath string) (Project, bool, error) {
	projects, err := s.ListProjects(true)
	if err != nil {
		return Project{}, false, err
	}

	for _, project := range projects {
		if filepath.Clean(project.WorkspacePath) == filepath.Clean(workspacePath) && project.DeletedAt == nil {
			return project, true, nil
		}
	}

	return Project{}, false, nil
}

func (s *Store) ListProjects(includeDeleted bool) ([]Project, error) {
	root := filepath.Join(s.cfg.MetadataRoot, "projects")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	projects := []Project{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		project, err := s.ProjectByID(entry.Name())
		if err != nil {
			return nil, err
		}

		if project.DeletedAt != nil && !includeDeleted {
			continue
		}

		projects = append(projects, project)
	}

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].CreatedAt.Before(projects[j].CreatedAt)
	})

	return projects, nil
}

func (s *Store) ProjectChildren(projectID string, includeDeleted bool) ([]Project, error) {
	projects, err := s.ListProjects(includeDeleted)
	if err != nil {
		return nil, err
	}

	children := []Project{}
	for _, project := range projects {
		if project.ParentProjectID == projectID {
			children = append(children, project)
		}
	}

	return children, nil
}

func (s *Store) LoadSnapshot(projectID, snapshotID string) (SnapshotManifest, error) {
	var manifest SnapshotManifest
	path := s.snapshotManifestPath(projectID, snapshotID)
	if err := readJSONFile(path, &manifest); err != nil {
		if os.IsNotExist(err) {
			return SnapshotManifest{}, notFound("snapshot not found", map[string]any{"project_id": projectID, "snapshot_id": snapshotID})
		}

		return SnapshotManifest{}, metadataCorruption("failed to load snapshot manifest", err, map[string]any{"path": path})
	}

	return manifest, nil
}

func (s *Store) FindSnapshot(snapshotID string) (SnapshotManifest, error) {
	projects, err := s.ListProjects(true)
	if err != nil {
		return SnapshotManifest{}, err
	}

	for _, project := range projects {
		manifest, err := s.LoadSnapshot(project.ID, snapshotID)
		if err == nil {
			return manifest, nil
		}

		var appErr *AppError
		if !As(err, &appErr) || appErr.Category != "NotFound" {
			return SnapshotManifest{}, err
		}
	}

	return SnapshotManifest{}, notFound("snapshot not found", map[string]any{"snapshot_id": snapshotID})
}

func (s *Store) LatestSnapshot(projectID string) (SnapshotManifest, error) {
	snapshotsDir := s.projectSnapshotsDir(projectID)
	entries, err := os.ReadDir(snapshotsDir)
	if err != nil {
		return SnapshotManifest{}, err
	}

	var latest SnapshotManifest
	found := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		manifest, err := s.LoadSnapshot(projectID, entry.Name())
		if err != nil {
			return SnapshotManifest{}, err
		}

		if !found || manifest.CreatedAt.After(latest.CreatedAt) {
			latest = manifest
			found = true
		}
	}

	if !found {
		return SnapshotManifest{}, notFound("project has no snapshots", map[string]any{"project_id": projectID})
	}

	return latest, nil
}

func (s *Store) SaveNode(node Node, bootstrap BootstrapState, template []byte) error {
	if err := ensureDir(s.nodeDir(node.ID)); err != nil {
		return err
	}

	var previous *Node
	if loaded, err := s.NodeByID(node.ID); err == nil {
		previous = &loaded
	}

	if err := writeYAMLFile(s.nodePath(node.ID), node); err != nil {
		return err
	}

	if err := writeJSONFile(s.nodeBootstrapPath(node.ID), bootstrap); err != nil {
		return err
	}

	if len(template) > 0 {
		if err := atomicWriteFile(s.nodeTemplatePath(node.ID), template, 0o644); err != nil {
			return err
		}
	}

	if err := atomicWriteFile(s.nodeInstanceRefPath(node.ID), []byte(node.LimaInstanceName+"\n"), 0o644); err != nil {
		return err
	}

	if err := atomicWriteFile(s.nodeInstanceIndexPath(node.LimaInstanceName), []byte(node.ID+"\n"), 0o644); err != nil {
		return err
	}

	if !exists(s.nodeContextPath(node.ID)) {
		if err := atomicWriteFile(s.nodeContextPath(node.ID), []byte{}, 0o644); err != nil {
			return err
		}
	}

	if previous != nil && previous.LimaInstanceName != node.LimaInstanceName {
		_ = os.Remove(s.nodeInstanceIndexPath(previous.LimaInstanceName))
	}

	if node.DeletedAt != nil || node.Status == NodeStatusTerminated {
		_ = os.Remove(s.nodeInstanceIndexPath(node.LimaInstanceName))
	}

	return nil
}

func (s *Store) NodeByID(nodeID string) (Node, error) {
	var node Node
	path := s.nodePath(nodeID)
	if err := readYAMLFile(path, &node); err != nil {
		if os.IsNotExist(err) {
			return Node{}, notFound("node not found", map[string]any{"id": nodeID})
		}

		return Node{}, metadataCorruption("failed to load node", err, map[string]any{"path": path})
	}

	return node, nil
}

func (s *Store) NodeByIDOrSlug(value string) (Node, error) {
	if exists(s.nodePath(value)) {
		return s.NodeByID(value)
	}

	nodes, err := s.ListNodes(false)
	if err != nil {
		return Node{}, err
	}

	for _, node := range nodes {
		if node.Slug == value {
			return node, nil
		}
	}

	nodes, err = s.ListNodes(true)
	if err != nil {
		return Node{}, err
	}

	var deletedMatch *Node
	for _, node := range nodes {
		if node.Slug == value {
			nodeCopy := node
			deletedMatch = &nodeCopy
		}
	}

	if deletedMatch != nil {
		return *deletedMatch, nil
	}

	return Node{}, notFound("node not found", map[string]any{"query": value})
}

func (s *Store) NodeByInstanceName(instanceName string) (Node, error) {
	indexPath := s.nodeInstanceIndexPath(instanceName)
	if exists(indexPath) {
		data, err := os.ReadFile(indexPath)
		if err != nil {
			return Node{}, metadataCorruption("failed to read node instance index", err, map[string]any{"path": indexPath})
		}

		return s.NodeByID(strings.TrimSpace(string(data)))
	}

	nodes, err := s.ListNodes(true)
	if err != nil {
		return Node{}, err
	}

	for _, node := range nodes {
		if node.LimaInstanceName == instanceName {
			return node, nil
		}
	}

	return Node{}, notFound("node not found", map[string]any{"instance_name": instanceName})
}

func (s *Store) LoadBootstrapState(nodeID string) (BootstrapState, error) {
	var bootstrap BootstrapState
	path := s.nodeBootstrapPath(nodeID)
	if err := readJSONFile(path, &bootstrap); err != nil {
		if os.IsNotExist(err) {
			return BootstrapState{}, notFound("bootstrap state not found", map[string]any{"node_id": nodeID})
		}

		return BootstrapState{}, metadataCorruption("failed to load bootstrap state", err, map[string]any{"path": path})
	}

	return bootstrap, nil
}

func (s *Store) ListNodes(includeDeleted bool) ([]Node, error) {
	root := filepath.Join(s.cfg.MetadataRoot, "nodes")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	nodes := []Node{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !s.nodeMetadataExists(entry.Name()) {
			continue
		}

		node, err := s.NodeByID(entry.Name())
		if err != nil {
			return nil, err
		}

		if node.DeletedAt != nil && !includeDeleted {
			continue
		}

		nodes = append(nodes, node)
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].CreatedAt.Before(nodes[j].CreatedAt)
	})

	return nodes, nil
}

func (s *Store) ProjectNodes(projectID string, includeDeleted bool) ([]Node, error) {
	nodes, err := s.ListNodes(includeDeleted)
	if err != nil {
		return nil, err
	}

	projectNodes := []Node{}
	for _, node := range nodes {
		if node.ProjectID == projectID {
			projectNodes = append(projectNodes, node)
		}
	}

	return projectNodes, nil
}

func (s *Store) SavePatch(proposal PatchProposal, patchText []byte) error {
	if err := ensureDir(s.patchDir(proposal.ID)); err != nil {
		return err
	}

	var previous *PatchProposal
	if loaded, err := s.PatchByID(proposal.ID); err == nil {
		previous = &loaded
	}

	if err := writeYAMLFile(s.patchProposalPath(proposal.ID), proposal); err != nil {
		return err
	}

	if len(patchText) > 0 {
		if err := atomicWriteFile(s.patchDiffPath(proposal.ID), patchText, 0o644); err != nil {
			return err
		}
	}

	if err := writeJSONFile(s.patchSummaryPath(proposal.ID), proposal.DiffSummary); err != nil {
		return err
	}

	if proposal.ConflictSummary != nil {
		if err := writeJSONFile(s.patchConflictsPath(proposal.ID), proposal.ConflictSummary); err != nil {
			return err
		}
	}

	if proposal.ApplyResult != nil {
		if err := writeJSONFile(s.patchApplyResultPath(proposal.ID), proposal.ApplyResult); err != nil {
			return err
		}
	}

	if previous != nil && previous.Status != proposal.Status {
		_ = os.Remove(s.patchStatusIndexPath(previous.Status, proposal.ID))
	}

	statusDir := filepath.Dir(s.patchStatusIndexPath(proposal.Status, proposal.ID))
	if err := ensureDir(statusDir); err != nil {
		return err
	}

	if err := atomicWriteFile(s.patchStatusIndexPath(proposal.Status, proposal.ID), []byte(proposal.ID+"\n"), 0o644); err != nil {
		return err
	}

	return nil
}

func (s *Store) PatchByID(patchID string) (PatchProposal, error) {
	var proposal PatchProposal
	path := s.patchProposalPath(patchID)
	if err := readYAMLFile(path, &proposal); err != nil {
		if os.IsNotExist(err) {
			return PatchProposal{}, notFound("patch not found", map[string]any{"id": patchID})
		}

		return PatchProposal{}, metadataCorruption("failed to load patch proposal", err, map[string]any{"path": path})
	}

	return proposal, nil
}

func (s *Store) LoadPatchDiff(patchID string) ([]byte, error) {
	path := s.patchDiffPath(patchID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, notFound("patch bundle not found", map[string]any{"id": patchID})
		}

		return nil, metadataCorruption("failed to load patch bundle", err, map[string]any{"path": path})
	}

	return data, nil
}

func (s *Store) ListPatches(status string) ([]PatchProposal, error) {
	root := filepath.Join(s.cfg.MetadataRoot, "patches")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	proposals := []PatchProposal{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		proposal, err := s.PatchByID(entry.Name())
		if err != nil {
			return nil, err
		}

		if status != "" && proposal.Status != status {
			continue
		}

		proposals = append(proposals, proposal)
	}

	sort.Slice(proposals, func(i, j int) bool {
		return proposals[i].CreatedAt.Before(proposals[j].CreatedAt)
	})

	return proposals, nil
}

func (s *Store) AppendProjectEvent(projectID string, event Event) error {
	return appendEvent(s.projectEventsPath(projectID), event)
}

func (s *Store) AppendNodeEvent(nodeID string, event Event) error {
	return appendEvent(s.nodeEventsPath(nodeID), event)
}

func (s *Store) AppendPatchEvent(patchID string, event Event) error {
	return appendEvent(s.patchEventsPath(patchID), event)
}

func (s *Store) NodeEvents(nodeID string) ([]Event, error) {
	return readEvents(s.nodeEventsPath(nodeID))
}

func (s *Store) PatchEvents(patchID string) ([]Event, error) {
	return readEvents(s.patchEventsPath(patchID))
}

func (s *Store) ProjectEvents(projectID string) ([]Event, error) {
	return readEvents(s.projectEventsPath(projectID))
}

func (s *Store) MissingProjectIndexes() ([]string, error) {
	projects, err := s.ListProjects(false)
	if err != nil {
		return nil, err
	}

	missing := []string{}
	for _, project := range projects {
		if !exists(s.projectSlugIndexPath(project.Slug)) {
			missing = append(missing, fmt.Sprintf("project slug index missing for %s", project.Slug))
		}
	}

	return missing, nil
}

func (s *Store) MissingNodeIndexes() ([]string, error) {
	nodes, err := s.ListNodes(false)
	if err != nil {
		return nil, err
	}

	missing := []string{}
	for _, node := range nodes {
		if !exists(s.nodeInstanceIndexPath(node.LimaInstanceName)) && node.Status != NodeStatusTerminated {
			missing = append(missing, fmt.Sprintf("node instance index missing for %s", node.LimaInstanceName))
		}
	}

	return missing, nil
}

func (s *Store) OrphanedPatchStatusIndexes() ([]string, error) {
	root := filepath.Join(s.cfg.MetadataRoot, "_index", "patches", "by-status")
	statusDirectories, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	missing := []string{}
	for _, statusDirectory := range statusDirectories {
		if !statusDirectory.IsDir() {
			continue
		}

		files, err := os.ReadDir(filepath.Join(root, statusDirectory.Name()))
		if err != nil {
			return nil, err
		}

		for _, file := range files {
			patchID := strings.TrimSuffix(file.Name(), ".ref")
			if !exists(s.patchProposalPath(patchID)) {
				missing = append(missing, filepath.Join(root, statusDirectory.Name(), file.Name()))
			}
		}
	}

	return missing, nil
}
