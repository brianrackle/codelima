package codelima

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

type Store struct {
	cfg Config
}

const metadataSchemaVersion = "4"

func ensureSchemaVersion(home string) error {
	marker := filepath.Join(home, "_config", "schema.version")
	if data, err := os.ReadFile(marker); err == nil {
		found := strings.TrimSpace(string(data))
		if found == "3" {
			return preconditionFailed("this CODELIMA_HOME uses schema v3. Stop and delete its Microsandbox nodes with the previous CodeLima release, or point --home/CODELIMA_HOME at a new directory; no automatic runtime migration exists", map[string]any{"found": found, "required": metadataSchemaVersion})
		}
		if found == "2" {
			return preconditionFailed(fmt.Sprintf("this CODELIMA_HOME uses schema v%s. Use its matching previous CodeLima release to remove runtime nodes, or point --home/CODELIMA_HOME at a new directory; no automatic runtime migration exists", found), map[string]any{"found": found, "required": metadataSchemaVersion})
		}
		if found != metadataSchemaVersion {
			return preconditionFailed("unsupported CODELIMA_HOME schema version", map[string]any{"found": strings.TrimSpace(string(data)), "required": metadataSchemaVersion})
		}
		return nil
	} else if !os.IsNotExist(err) {
		return metadataCorruption("failed to read schema version", err, map[string]any{"path": marker})
	}

	legacy, err := homeContainsLimaArtifacts(home)
	if err != nil {
		return err
	}
	if legacy {
		return preconditionFailed("this CODELIMA_HOME contains Lima-backed nodes from codelima <v1>. Delete them with the previous release (codelima node delete ...) or point --home/CODELIMA_HOME at a new directory. No automatic migration exists.", nil)
	}
	fresh, err := homeIsFresh(home)
	if err != nil {
		return err
	}
	if !fresh {
		return preconditionFailed("this CODELIMA_HOME has an unrecognized home layout; point --home/CODELIMA_HOME at a new directory", nil)
	}
	if err := ensureDir(filepath.Dir(marker)); err != nil {
		return err
	}
	return atomicWriteFile(marker, []byte(metadataSchemaVersion+"\n"), 0o644)
}

func homeContainsLimaArtifacts(home string) (bool, error) {
	paths := []string{filepath.Join(home, "_config", "config.yaml")}
	for _, root := range []string{"nodes", "projects"} {
		entries, err := os.ReadDir(filepath.Join(home, root))
		if err != nil && !os.IsNotExist(err) {
			return false, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if root == "nodes" && exists(filepath.Join(home, root, entry.Name(), "lima-instance.ref")) {
				return true, nil
			}
			paths = append(paths, filepath.Join(home, root, entry.Name(), map[string]string{"nodes": "node.yaml", "projects": "project.yaml"}[root]))
		}
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		current := string(data)
		for _, marker := range []string{"lima_home:", "lima_commands:", "lima_instance_name:", "default_lima_template:"} {
			if strings.Contains(current, marker) {
				return true, nil
			}
		}
	}
	return false, nil
}

func homeIsFresh(home string) (bool, error) {
	entries, err := os.ReadDir(home)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return false, nil
		}
		children, err := os.ReadDir(filepath.Join(home, entry.Name()))
		if err != nil {
			return false, err
		}
		if len(children) != 0 {
			return false, nil
		}
	}
	return true, nil
}

func NewStore(cfg Config) *Store {
	return &Store{cfg: cfg}
}

// EnsureLayout fully initializes the metadata home: directory layout plus
// seeded/repaired metadata. Read paths must not use it — they call
// ensureDirectories only, while mutating paths run seedAndRepair under the
// environments/configurations/nodes locks (see Service.EnsureReady).
func (s *Store) EnsureLayout() error {
	if err := s.ensureDirectories(); err != nil {
		return err
	}

	return s.seedAndRepair(time.Now().UTC(), true)
}

// ensureDirectories creates the directory skeleton only. It is idempotent,
// cheap, and safe for read paths: it never writes or rewrites files.
func (s *Store) ensureDirectories() error {
	if err := validateConfig(s.cfg); err != nil {
		return err
	}
	if err := ensureSchemaVersion(s.cfg.MetadataRoot); err != nil {
		return err
	}

	directories := []string{
		filepath.Join(s.cfg.MetadataRoot, "_config"),
		s.cfg.AgentProfilesDir,
		filepath.Join(s.cfg.MetadataRoot, "_locks"),
		filepath.Join(s.cfg.MetadataRoot, "_daemon"),
		filepath.Join(s.cfg.MetadataRoot, "_index", "environments", "by-slug"),
		filepath.Join(s.cfg.MetadataRoot, "_index", "configurations", "by-slug"),
		filepath.Join(s.cfg.MetadataRoot, "_index", "nodes", "by-instance"),
		filepath.Join(s.cfg.MetadataRoot, "_index", "nodes", "by-slug"),
		filepath.Join(s.cfg.MetadataRoot, "environments"),
		filepath.Join(s.cfg.MetadataRoot, "configurations"),
		filepath.Join(s.cfg.MetadataRoot, "nodes"),
	}

	for _, directory := range directories {
		if err := ensureDir(directory); err != nil {
			return err
		}
	}

	return nil
}

// seedRevision versions the seed-and-repair pass. Bump it whenever the
// built-in profiles/environments/configurations, the config refresh
// rules, or the node-file format change so existing homes re-run the full
// pass exactly once instead of on every mutating command.
const seedRevision = "3"

func (s *Store) seedVersionPath() string {
	return filepath.Join(s.cfg.MetadataRoot, "_config", "seed.version")
}

func (s *Store) seedVersionCurrent() bool {
	data, err := os.ReadFile(s.seedVersionPath())
	return err == nil && strings.TrimSpace(string(data)) == seedRevision
}

// seedAndRepair writes the global config when missing or stale, seeds built-in
// agent profiles, environments, and configurations, and refreshes
// node metadata files. The pass is versioned: once a home is stamped with the
// current seedRevision it is skipped, so ordinary mutating commands stop
// re-reading every node file. force (doctor --repair) always runs it.
// Callers must hold the environments, configurations, and nodes locks (Store
// keeps no locking of its own — lock discipline lives at the Service
// operation level).
func (s *Store) seedAndRepair(now time.Time, force bool) error {
	if !force && s.seedVersionCurrent() {
		return nil
	}
	if err := s.ensureConfigFile(); err != nil {
		return err
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

	if err := s.ensureBuiltInEnvironmentConfigs(now); err != nil {
		return err
	}
	if err := s.ensureBuiltInConfigurations(now); err != nil {
		return err
	}
	if err := s.retireLegacyDefaultConfiguration(now); err != nil {
		return err
	}

	if err := s.ensureNodeMetadataFiles(); err != nil {
		return err
	}

	return atomicWriteFile(s.seedVersionPath(), []byte(seedRevision+"\n"), 0o644)
}

func (s *Store) ensureConfigFile() error {
	path := s.configPath()
	if !exists(path) {
		return writeConfigFile(path, s.cfg)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return metadataCorruption("failed to read config.yaml", err, map[string]any{"path": path})
	}

	if !configFileNeedsRefresh(data) {
		return nil
	}

	return writeConfigFile(path, s.cfg)
}

func (s *Store) ensureNodeMetadataFiles() error {
	nodeRoot := filepath.Join(s.cfg.MetadataRoot, "nodes")
	entries, err := os.ReadDir(nodeRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		path := filepath.Join(nodeRoot, entry.Name(), "node.yaml")
		if !exists(path) {
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return metadataCorruption("failed to read node metadata", err, map[string]any{"path": path})
		}

		var wire nodeFileWire
		if err := readYAMLFile(path, &wire); err != nil {
			return metadataCorruption("failed to load node", err, map[string]any{"path": path})
		}
		node := wire.node()

		// The full pass already has the node in hand: backfill the by-slug
		// index for homes created before it existed.
		if node.DeletedAt == nil && node.Status != NodeStatusTerminated && !exists(s.nodeSlugIndexPath(node.Slug)) {
			if err := atomicWriteFile(s.nodeSlugIndexPath(node.Slug), []byte(node.ID+"\n"), 0o644); err != nil {
				return err
			}
		}

		defaults := s.cfg.RuntimeCommands.ApplyDefaults(defaultRuntimeCommandTemplates())
		if !nodeFileNeedsRefresh(data, node, defaults) {
			continue
		}

		if err := writeNodeFile(path, node, defaults); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) ensureBuiltInEnvironmentConfigs(createdAt time.Time) error {
	legacyConfigs := legacyBuiltInEnvironmentConfigs()
	for _, spec := range builtInEnvironmentConfigs() {
		config, err := s.EnvironmentConfigByIDOrSlug(spec.Slug)
		if err == nil {
			if config.DeletedAt != nil {
				continue
			}

			if bootstrapCommandsEqual(config.BootstrapCommands, spec.BootstrapCommands) {
				continue
			}

			legacySpecs, ok := legacyConfigs[spec.Slug]
			if !ok {
				continue
			}
			matchesLegacy := false
			for _, legacy := range legacySpecs {
				if bootstrapCommandsEqual(config.BootstrapCommands, legacy.BootstrapCommands) {
					matchesLegacy = true
					break
				}
			}
			if !matchesLegacy {
				continue
			}

			config.BootstrapCommands = append([]string(nil), spec.BootstrapCommands...)
			config.UpdatedAt = createdAt
			if err := s.SaveEnvironmentConfig(config); err != nil {
				return err
			}
			continue
		}

		if !IsNotFound(err) {
			return err
		}

		if err := s.SaveEnvironmentConfig(EnvironmentConfig{
			ID:                newID(),
			Slug:              spec.Slug,
			BootstrapCommands: append([]string(nil), spec.BootstrapCommands...),
			CreatedAt:         createdAt,
			UpdatedAt:         createdAt,
		}); err != nil {
			return err
		}
	}

	return nil
}

func bootstrapCommandsEqual(a, b []string) bool {
	if a == nil {
		a = []string{}
	}
	if b == nil {
		b = []string{}
	}
	return reflect.DeepEqual(a, b)
}

func (s *Store) configPath() string {
	return filepath.Join(s.cfg.MetadataRoot, "_config", "settings.yaml")
}

func (s *Store) agentProfilePath(name string) string {
	return filepath.Join(s.cfg.AgentProfilesDir, name+".yaml")
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

func (s *Store) nodeBootstrapPath(nodeID string) string {
	return filepath.Join(s.nodeDir(nodeID), "bootstrap.json")
}

func (s *Store) nodeInstanceRefPath(nodeID string) string {
	return filepath.Join(s.nodeDir(nodeID), "sandbox.ref")
}

func (s *Store) incompleteNodeMetadata(nodeID string) (IncompleteNodeMetadata, error) {
	item := IncompleteNodeMetadata{
		NodeID:        nodeID,
		DirectoryPath: s.nodeDir(nodeID),
	}

	instanceRefPath := s.nodeInstanceRefPath(nodeID)
	if exists(instanceRefPath) {
		item.InstanceRefPath = instanceRefPath
		data, err := os.ReadFile(instanceRefPath)
		if err != nil {
			return IncompleteNodeMetadata{}, metadataCorruption("failed to read node instance ref", err, map[string]any{"path": instanceRefPath})
		}
		item.SandboxName = strings.TrimSpace(string(data))
	}

	return item, nil
}

func (s *Store) nodeInstanceIndexPath(instanceName string) string {
	return filepath.Join(s.cfg.MetadataRoot, "_index", "nodes", "by-instance", instanceName)
}

func (s *Store) SaveIncompleteNodeReference(nodeID, sandboxName string) error {
	if strings.TrimSpace(nodeID) == "" {
		return invalidArgument("node id is required for incomplete runtime registration", nil)
	}
	if err := validateSandboxName(sandboxName); err != nil {
		return err
	}
	if err := ensureDir(s.nodeDir(nodeID)); err != nil {
		return err
	}
	if err := atomicWriteFile(s.nodeInstanceRefPath(nodeID), []byte(sandboxName+"\n"), 0o644); err != nil {
		_ = os.RemoveAll(s.nodeDir(nodeID))
		return err
	}
	if err := atomicWriteFile(s.nodeInstanceIndexPath(sandboxName), []byte(nodeID+"\n"), 0o644); err != nil {
		_ = os.RemoveAll(s.nodeDir(nodeID))
		return err
	}
	return nil
}

func (s *Store) nodeSlugIndexPath(slug string) string {
	return filepath.Join(s.cfg.MetadataRoot, "_index", "nodes", "by-slug", slug)
}

// maintainNodeSlugIndex keeps _index/nodes/by-slug consistent with a saved
// node: live nodes claim their slug entry, renames drop the previous entry,
// and terminated/deleted nodes release theirs so the slug can be reused.
func (s *Store) maintainNodeSlugIndex(node Node, previous *Node) error {
	if previous != nil && previous.Slug != node.Slug {
		_ = os.Remove(s.nodeSlugIndexPath(previous.Slug))
	}
	if node.DeletedAt != nil || node.Status == NodeStatusTerminated {
		_ = os.Remove(s.nodeSlugIndexPath(node.Slug))
		return nil
	}
	return atomicWriteFile(s.nodeSlugIndexPath(node.Slug), []byte(node.ID+"\n"), 0o644)
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

func (s *Store) SaveNode(node Node, bootstrap BootstrapState) error {
	if err := ensureDir(s.nodeDir(node.ID)); err != nil {
		return err
	}

	var previous *Node
	if loaded, err := s.NodeByID(node.ID); err == nil {
		previous = &loaded
	}

	defaults := s.cfg.RuntimeCommands.ApplyDefaults(defaultRuntimeCommandTemplates())
	if err := writeNodeFile(s.nodePath(node.ID), node, defaults); err != nil {
		return err
	}

	if err := writeJSONFile(s.nodeBootstrapPath(node.ID), bootstrap); err != nil {
		return err
	}

	if err := atomicWriteFile(s.nodeInstanceRefPath(node.ID), []byte(node.SandboxName+"\n"), 0o644); err != nil {
		return err
	}

	if err := atomicWriteFile(s.nodeInstanceIndexPath(node.SandboxName), []byte(node.ID+"\n"), 0o644); err != nil {
		return err
	}

	if !exists(s.nodeContextPath(node.ID)) {
		if err := atomicWriteFile(s.nodeContextPath(node.ID), []byte{}, 0o644); err != nil {
			return err
		}
	}

	if previous != nil && previous.SandboxName != node.SandboxName {
		_ = os.Remove(s.nodeInstanceIndexPath(previous.SandboxName))
	}

	if node.DeletedAt != nil || node.Status == NodeStatusTerminated {
		_ = os.Remove(s.nodeInstanceIndexPath(node.SandboxName))
	}

	return s.maintainNodeSlugIndex(node, previous)
}

func (s *Store) NodeByID(nodeID string) (Node, error) {
	var wire nodeFileWire
	path := s.nodePath(nodeID)
	if err := readYAMLFile(path, &wire); err != nil {
		if os.IsNotExist(err) {
			return Node{}, notFound("node not found", map[string]any{"id": nodeID})
		}

		return Node{}, metadataCorruption("failed to load node", err, map[string]any{"path": path})
	}

	return wire.node(), nil
}

func (s *Store) NodeByIDOrSlug(value string) (Node, error) {
	if exists(s.nodePath(value)) {
		return s.NodeByID(value)
	}

	// The by-slug index resolves live slugs without scanning every node file;
	// the full scans below remain as the fallback for homes whose index has
	// not been rebuilt yet (doctor reports those as warnings).
	if indexPath := s.nodeSlugIndexPath(value); exists(indexPath) {
		data, err := os.ReadFile(indexPath)
		if err != nil {
			return Node{}, metadataCorruption("failed to read node slug index", err, map[string]any{"path": indexPath})
		}
		if node, err := s.NodeByID(strings.TrimSpace(string(data))); err == nil {
			return node, nil
		}
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

func (s *Store) NodeBySandboxName(instanceName string) (Node, error) {
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
		if node.SandboxName == instanceName {
			return node, nil
		}
	}

	return Node{}, notFound("node not found", map[string]any{"sandbox_name": instanceName})
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

func (s *Store) IncompleteNodeMetadata() ([]IncompleteNodeMetadata, error) {
	root := filepath.Join(s.cfg.MetadataRoot, "nodes")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	items := []IncompleteNodeMetadata{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if s.nodeMetadataExists(entry.Name()) {
			continue
		}

		item, err := s.incompleteNodeMetadata(entry.Name())
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].NodeID < items[j].NodeID
	})

	return items, nil
}

func (s *Store) RemoveIncompleteNodeMetadata(items []IncompleteNodeMetadata) error {
	for _, item := range items {
		if strings.TrimSpace(item.SandboxName) != "" {
			_ = os.Remove(s.nodeInstanceIndexPath(item.SandboxName))
		}
		if err := os.RemoveAll(item.DirectoryPath); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) AppendNodeEvent(nodeID string, event Event) error {
	return appendEvent(s.nodeEventsPath(nodeID), event)
}

func (s *Store) NodeEvents(nodeID string) ([]Event, error) {
	return readEvents(s.nodeEventsPath(nodeID))
}

func (s *Store) MissingNodeIndexes() ([]string, error) {
	nodes, err := s.ListNodes(false)
	if err != nil {
		return nil, err
	}

	missing := []string{}
	for _, node := range nodes {
		if !exists(s.nodeInstanceIndexPath(node.SandboxName)) && node.Status != NodeStatusTerminated {
			missing = append(missing, fmt.Sprintf("node instance index missing for %s", node.SandboxName))
		}
	}

	return missing, nil
}

func (s *Store) IncompleteNodeWarnings() ([]string, error) {
	items, err := s.IncompleteNodeMetadata()
	if err != nil {
		return nil, err
	}

	warnings := make([]string, 0, len(items))
	for _, item := range items {
		message := fmt.Sprintf("incomplete node metadata directory: %s", item.DirectoryPath)
		if item.SandboxName != "" {
			message += fmt.Sprintf(" (instance %s)", item.SandboxName)
		}
		message += "; remove it with `codelima node cleanup-incomplete --apply`"
		warnings = append(warnings, message)
	}

	return warnings, nil
}
