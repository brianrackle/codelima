package codelima

import (
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Store struct {
	cfg    Config
	logger *slog.Logger
	// nodeCache and configurationCache memoize parsed metadata records so a
	// repeated list stats each file instead of re-reading and re-unmarshaling
	// it. See metadataCache for the validity rule and why it is safe.
	nodeCache          metadataCache[Node]
	configurationCache metadataCache[Configuration]
	// parsedRecords counts the metadata files this Store has opened and
	// unmarshaled. Nothing in production reads it; the cache tests assert
	// against it that listing an unchanged home costs zero parses.
	parsedRecords atomic.Uint64
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

// isIgnorableHomeEntry reports whether a directory entry is macOS Finder junk
// rather than state. Finder writes .DS_Store into any directory a user browses
// and .localized into ones it localizes, so on the tool's primary platform a
// single Finder visit to an otherwise empty ~/.codelima used to make first run
// fail with "unrecognized home layout". Only these exact names are exempt:
// refusing to adopt a home that holds anything else is a safety property, not a
// strictness knob.
func isIgnorableHomeEntry(name string) bool {
	switch name {
	case ".DS_Store", ".localized":
		return true
	default:
		return false
	}
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
		if isIgnorableHomeEntry(entry.Name()) {
			continue
		}
		if !entry.IsDir() {
			return false, nil
		}
		children, err := os.ReadDir(filepath.Join(home, entry.Name()))
		if err != nil {
			return false, err
		}
		for _, child := range children {
			if isIgnorableHomeEntry(child.Name()) {
				continue
			}
			return false, nil
		}
	}
	return true, nil
}

func NewStore(cfg Config) *Store {
	return &Store{cfg: cfg}
}

// metadataCache memoizes parsed metadata records keyed by their file path. The
// daemon forwarder lists nodes once a second and every TUI window polls on top
// of that, so without it the whole metadata home is re-read and re-YAML-parsed
// at >=1Hz for records that change a few times an hour.
//
// A cached record is served only when a fresh stat reports the same file
// (os.SameFile — device plus inode), the same size, and the same modification
// time. The inode leg is what makes this safe when a filesystem's timestamps
// are coarse: every metadata write goes through internal/atomicfile, which
// writes a temp file and renames it over the destination, so a rewritten record
// always lands on a different inode even when its size and its
// one-second-resolution mtime match the record it replaced. Store's own writes
// additionally drop the entry (forget), so a write-then-list inside one process
// cannot serve the previous content even if some future write path stops going
// through rename.
//
// Failures are never cached. A record that cannot be parsed is re-read on every
// pass, which keeps listNodes' skip-and-warn semantics intact (one warning per
// list, quarantine still offered) and guarantees that repairing the file is
// picked up on the next call rather than after some expiry.
//
// Entries are never evicted. Node and configuration records are tombstoned
// rather than deleted, so the cache is bounded by what the home actually holds.
type metadataCache[T any] struct {
	mu      sync.Mutex
	entries map[string]metadataCacheEntry[T]
}

// metadataCacheEntry is one parsed record together with the identity of the
// file it was parsed from.
type metadataCacheEntry[T any] struct {
	info  os.FileInfo
	value T
}

// lookup returns the cached record for path when info describes the same file
// contents the entry was parsed from. A nil info (an unstattable file) never
// hits, so the caller falls through to the uncached read.
func (c *metadataCache[T]) lookup(path string, info os.FileInfo) (T, bool) {
	var zero T
	if info == nil {
		return zero, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[path]
	if !ok || !sameMetadataFile(entry.info, info) {
		return zero, false
	}

	return entry.value, true
}

// store records a parsed value under the file identity it was read from. info
// must have been sampled *before* the read: stamping a record with an identity
// taken beforehand can only cost one extra parse on the next pass, while
// stamping it afterwards could bind newer content to an older identity and then
// serve it until the file changes again.
func (c *metadataCache[T]) store(path string, info os.FileInfo, value T) {
	if info == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]metadataCacheEntry[T]{}
	}
	c.entries[path] = metadataCacheEntry[T]{info: info, value: value}
}

func (c *metadataCache[T]) forget(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, path)
}

func sameMetadataFile(cached, current os.FileInfo) bool {
	return cached != nil && current != nil &&
		cached.Size() == current.Size() &&
		cached.ModTime().Equal(current.ModTime()) &&
		os.SameFile(cached, current)
}

// statMetadataFile samples the identity of a metadata file. Every cached read
// stats: the win is skipping the open, the read, and the YAML unmarshal, not
// skipping the stat, and a stale answer here would be a correctness bug rather
// than a slow one. A stat failure (and anything that is not a regular file)
// yields nil so the caller performs the uncached read and the error surfaces
// from exactly the code path that produced it before this cache existed.
func statMetadataFile(path string) os.FileInfo {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}

	return info
}

// cloneStringSlice copies a metadata string list, preserving the nil/empty
// distinction: an explicitly empty runtime-command list means "no commands",
// which is not the same as "unset".
func cloneStringSlice(values []string) []string {
	if values == nil {
		return nil
	}

	return append(make([]string, 0, len(values)), values...)
}

// cloneNode deep-copies the parts of a Node that a caller could mutate in
// place. Cached records are shared by every list pass, so handing out the
// stored value directly would let one caller's edit rewrite what the next
// caller reads. NodeList's own hydrateConfigurationSlugs already writes into
// the records it is given.
func cloneNode(source Node) Node {
	cloned := source
	cloned.Environments = cloneStringSlice(source.Environments)
	cloned.Ports = cloneStringSlice(source.Ports)
	cloned.BootstrapCommands = cloneStringSlice(source.BootstrapCommands)
	cloned.RuntimeCommands = cloneRuntimeCommandTemplates(source.RuntimeCommands)
	if source.BootstrapCompletedAt != nil {
		completedAt := *source.BootstrapCompletedAt
		cloned.BootstrapCompletedAt = &completedAt
	}
	if source.DeletedAt != nil {
		deletedAt := *source.DeletedAt
		cloned.DeletedAt = &deletedAt
	}
	return cloned
}

func cloneRuntimeCommandTemplates(source RuntimeCommandTemplates) RuntimeCommandTemplates {
	cloned := source
	for _, field := range cloned.orderedFields() {
		*field.values = cloneStringSlice(*field.values)
	}
	return cloned
}

// SetLogger installs the sink for Store-level warnings — the skipped and
// quarantined records below. Store keeps its own pointer instead of reaching
// for a Service so read paths stay usable standalone; when it is unset the
// process-wide package sink is used, which is the TUI/daemon log file.
func (s *Store) SetLogger(logger *slog.Logger) {
	if s == nil {
		return
	}
	s.logger = logger
}

func (s *Store) log() *slog.Logger {
	if s == nil || s.logger == nil {
		return packageLog()
	}
	return s.logger
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
// Revision 7 retires virtiofs_reclaim_threshold_percent: the refresh rule that
// strips it only runs on homes whose stamp is behind, so the key would survive
// forever in every home already stamped 6.
const seedRevision = "7"

func (s *Store) seedVersionPath() string {
	return filepath.Join(s.cfg.MetadataRoot, "_config", "seed.version")
}

func (s *Store) seedVersion() string {
	data, err := os.ReadFile(s.seedVersionPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
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
	previousSeedRevision := s.seedVersion()
	if !force && previousSeedRevision == seedRevision {
		return nil
	}
	if err := s.ensureConfigFile(); err != nil {
		return err
	}

	if err := s.ensureBuiltInAgentProfiles(); err != nil {
		return err
	}

	if err := s.ensureBuiltInEnvironmentConfigs(now); err != nil {
		return err
	}
	if err := s.ensureBuiltInConfigurations(now, previousSeedRevision != seedRevision); err != nil {
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

func (s *Store) ensureBuiltInAgentProfiles() error {
	legacyProfiles := legacyBuiltInProfiles()
	for name, profile := range builtInProfiles() {
		profilePath := s.agentProfilePath(name)
		if !exists(profilePath) {
			if err := writeYAMLFile(profilePath, profile); err != nil {
				return err
			}
			continue
		}

		existing, err := s.LoadAgentProfile(name)
		if err != nil {
			return err
		}
		if agentProfilesEqual(existing, profile) {
			continue
		}
		matchesLegacy := false
		for _, legacy := range legacyProfiles[name] {
			if agentProfilesEqual(existing, legacy) {
				matchesLegacy = true
				break
			}
		}
		if !matchesLegacy {
			continue
		}
		if err := writeYAMLFile(profilePath, profile); err != nil {
			return err
		}
	}
	return nil
}

func agentProfilesEqual(a, b AgentProfile) bool {
	return a.Name == b.Name &&
		bootstrapCommandsEqual(a.InstallCommands, b.InstallCommands) &&
		a.ValidationCommand == b.ValidationCommand &&
		a.LaunchCommand == b.LaunchCommand &&
		maps.Equal(a.Environment, b.Environment)
}

func (s *Store) ensureConfigFile() error {
	path := s.configPath()
	if !exists(path) {
		return writeConfigFile(path, s.cfg)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return metadataCorruption("failed to read settings.yaml", err, map[string]any{"path": path})
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

		// A record nobody can parse must not abort the pass: seed-and-repair
		// runs on TUI/daemon startup and on every mutating command, so failing
		// here is exactly how one bad file used to brick the whole tool.
		// doctor --repair quarantines what is skipped.
		node, err := s.NodeByID(entry.Name())
		if err != nil {
			if IsNotFound(err) {
				continue
			}
			s.logSkippedNodeRecord("repair", entry.Name(), path, err)
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			s.logSkippedNodeRecord("repair", entry.Name(), path, err)
			continue
		}

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

		if err := s.writeNodeMetadata(entry.Name(), node, defaults); err != nil {
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

// writeNodeMetadata writes node.yaml and drops the parse-cache entry for it.
// The write already changes the file's identity (atomicfile renames a fresh
// inode over the destination), so the drop is belt and braces: it keeps a
// write-then-list inside one process correct no matter how coarse the
// filesystem's timestamps are or how a future write path is implemented.
func (s *Store) writeNodeMetadata(nodeID string, node Node, defaults RuntimeCommandTemplates) error {
	path := s.nodePath(nodeID)
	err := writeNodeFile(path, node, defaults)
	s.nodeCache.forget(path)
	return err
}

// SaveNode persists one node's record set. Each individual write is atomic
// (internal/atomicfile), so the only failure mode left is a torn *sequence*: a
// crash between two of these writes. The order below is chosen so that every
// state a crash can leave behind is either correct or self-healing, never
// destructive. Reading it as a matrix, crashing after step N leaves:
//
//  1. bootstrap.json — guest work already finished (bootstrap.Completed is only
//     set after every command and the validation probe succeeded) is durable.
//     node.yaml is still the previous record, so the node reads as not-yet-
//     finished and the next start re-runs the validation probe and rewrites it.
//     Nothing destructive. This write comes first precisely because it is the
//     record that *prevents* an action: NodeStart re-runs every bootstrap and
//     agent-install command when bootstrap.json says Completed=false. Writing
//     node.yaml first left a window where node.yaml claimed the bootstrap was
//     complete while bootstrap.json still asked for the whole install to be run
//     again on a node the user had already provisioned.
//  2. node.yaml — the record every reader treats as the node. Written after
//     everything it implies (the bootstrap state above) and before everything
//     that names it (the ref and indexes below), so an index never points at a
//     record that does not exist yet.
//  3. lima-instance.ref — a directory holding this file but no node.yaml is
//     what `node cleanup-incomplete` treats as an abandoned creation, so it
//     must never precede node.yaml or a real node becomes eligible for removal.
//  4. by-instance index, then 5. the context file — derived caches. Both
//     NodeBySandboxName and NodeByIDOrSlug fall back to a full scan when their
//     index entry is missing, so a crash here degrades performance, not
//     correctness, and the next SaveNode restores it.
//  6. stale and terminated by-instance removal — leaves at worst an extra entry
//     resolving to this same node.
//  7. by-slug index — released before it is reclaimed (see maintainNodeSlugIndex)
//     so a crash mid-rename leaves no entry rather than a wrong one; slug
//     uniqueness is enforced against node.yaml records, never against the index.
func (s *Store) SaveNode(node Node, bootstrap BootstrapState) error {
	if err := ensureDir(s.nodeDir(node.ID)); err != nil {
		return err
	}

	var previous *Node
	if loaded, err := s.NodeByID(node.ID); err == nil {
		previous = &loaded
	}

	if err := writeJSONFile(s.nodeBootstrapPath(node.ID), bootstrap); err != nil {
		return err
	}

	defaults := s.cfg.RuntimeCommands.ApplyDefaults(defaultRuntimeCommandTemplates())
	if err := s.writeNodeMetadata(node.ID, node, defaults); err != nil {
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
	path := s.nodePath(nodeID)
	// Sample the file identity before reading, never after: see
	// metadataCache.store.
	info := statMetadataFile(path)
	if node, ok := s.nodeCache.lookup(path, info); ok {
		return cloneNode(node), nil
	}

	node, err := s.readNodeRecord(path, nodeID)
	if err != nil {
		// Corruption is not cached, and a previously good entry for this path
		// is dropped: the file on disk no longer parses, so nothing about it
		// should still be servable.
		s.nodeCache.forget(path)
		return Node{}, err
	}

	s.nodeCache.store(path, info, node)
	return cloneNode(node), nil
}

// readNodeRecord is the uncached parse behind NodeByID and the only place node
// metadata is unmarshaled.
func (s *Store) readNodeRecord(path, nodeID string) (Node, error) {
	var wire nodeFileWire
	s.parsedRecords.Add(1)
	if err := readYAMLFile(path, &wire); err != nil {
		if os.IsNotExist(err) {
			return Node{}, notFound("node not found", map[string]any{"id": nodeID})
		}

		return Node{}, metadataCorruption("failed to load node", err, map[string]any{"path": path})
	}

	// An empty or truncated node.yaml is valid YAML that unmarshals to the
	// zero value. Returning that as a node would hand every caller a record
	// with no identity, so it is corruption, not a node.
	if strings.TrimSpace(wire.ID) == "" {
		return Node{}, metadataCorruption("node metadata has no id", nil, map[string]any{"path": path})
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
		node, err := s.NodeByID(strings.TrimSpace(string(data)))
		if err == nil {
			return node, nil
		}
		if !IsNotFound(err) {
			// The index names a record that exists but cannot be parsed. The
			// scans below skip corrupt records, so falling through would
			// answer "no such node" for a node that is demonstrably there;
			// report the corruption instead.
			return Node{}, err
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

// CorruptNodeRecord is a node metadata directory that exists but cannot be
// loaded. List paths skip these and keep going — one unreadable record must
// never make every other node invisible — while point lookups by id still fail
// with a MetadataCorruption error so nothing silently pretends the node is
// absent. doctor reports them; doctor --repair quarantines them.
type CorruptNodeRecord struct {
	NodeID string `json:"node_id" yaml:"node_id"`
	Path   string `json:"path" yaml:"path"`
	Reason string `json:"reason" yaml:"reason"`
}

func (s *Store) logSkippedNodeRecord(phase, nodeID, path string, err error) {
	s.log().Warn("skipping unreadable node metadata",
		"phase", phase,
		"node_id", nodeID,
		"path", path,
		"error", err,
		"remedy", "codelima doctor --repair")
}

func (s *Store) ListNodes(includeDeleted bool) ([]Node, error) {
	nodes, _, err := s.listNodes(includeDeleted)
	return nodes, err
}

// listNodes enumerates node metadata, skipping every record that cannot be read
// or parsed and returning those separately so doctor can report and quarantine
// them. Only a failure to enumerate the nodes directory itself is fatal: the
// TUI, the daemon forwarder, and doctor all route through this path, so
// aborting on the first bad file makes the whole tool unusable.
func (s *Store) listNodes(includeDeleted bool) ([]Node, []CorruptNodeRecord, error) {
	root := filepath.Join(s.cfg.MetadataRoot, "nodes")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, err
	}

	nodes := []Node{}
	corrupt := []CorruptNodeRecord{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !s.nodeMetadataExists(entry.Name()) {
			continue
		}

		node, err := s.NodeByID(entry.Name())
		if err != nil {
			if IsNotFound(err) {
				// The directory lost its node.yaml between the scan above and
				// the load: incomplete metadata, which cleanup-incomplete owns.
				continue
			}

			path := s.nodePath(entry.Name())
			corrupt = append(corrupt, CorruptNodeRecord{NodeID: entry.Name(), Path: path, Reason: err.Error()})
			s.logSkippedNodeRecord("list", entry.Name(), path, err)
			continue
		}

		if node.DeletedAt != nil && !includeDeleted {
			continue
		}

		nodes = append(nodes, node)
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].CreatedAt.Before(nodes[j].CreatedAt)
	})
	sort.Slice(corrupt, func(i, j int) bool {
		return corrupt[i].NodeID < corrupt[j].NodeID
	})

	return nodes, corrupt, nil
}

// CorruptNodeRecords returns every node record that cannot be loaded. It always
// scans deleted records too, so a corrupt tombstone is reported rather than
// hidden behind the live-only filter.
func (s *Store) CorruptNodeRecords() ([]CorruptNodeRecord, error) {
	_, corrupt, err := s.listNodes(true)
	if err != nil {
		return nil, err
	}

	return corrupt, nil
}

func (s *Store) CorruptNodeWarnings() ([]string, error) {
	corrupt, err := s.CorruptNodeRecords()
	if err != nil {
		return nil, err
	}

	warnings := make([]string, 0, len(corrupt))
	for _, record := range corrupt {
		warnings = append(warnings, fmt.Sprintf(
			"unreadable node metadata %s (%s); it is skipped by every listing — quarantine it with `codelima doctor --repair`",
			record.Path, record.Reason))
	}

	return warnings, nil
}

// quarantineDirName is the metadata-root subdirectory holding records doctor
// --repair has taken out of service. Nothing is ever deleted: the node's whole
// directory is moved here under a timestamped name so a human can inspect it,
// salvage its events log, or move it back.
const quarantineDirName = "_quarantine"

// quarantineTimestampLayout keeps quarantine directory names sortable and free
// of characters that need quoting in a shell.
const quarantineTimestampLayout = "20060102T150405Z"

// QuarantinedNodeRecord records one quarantine move. A copy is written into the
// quarantined directory as quarantine.yaml so the move is self-describing even
// after the reporting process is gone.
type QuarantinedNodeRecord struct {
	NodeID         string    `json:"node_id" yaml:"node_id"`
	SourcePath     string    `json:"source_path" yaml:"source_path"`
	QuarantinePath string    `json:"quarantine_path" yaml:"quarantine_path"`
	Reason         string    `json:"reason" yaml:"reason"`
	RemovedIndexes []string  `json:"removed_indexes,omitempty" yaml:"removed_indexes,omitempty"`
	QuarantinedAt  time.Time `json:"quarantined_at" yaml:"quarantined_at"`
}

func (s *Store) quarantineRoot() string {
	return filepath.Join(s.cfg.MetadataRoot, quarantineDirName)
}

// quarantineDirPath picks an unused _quarantine/<timestamp>-<id> path. The
// suffix loop matters because a repeated repair inside the same second must not
// rename one quarantined directory on top of another.
func (s *Store) quarantineDirPath(now time.Time, nodeID string) (string, error) {
	base := filepath.Join(s.quarantineRoot(), now.UTC().Format(quarantineTimestampLayout)+"-"+nodeID)
	candidate := base
	for attempt := 1; attempt <= 1000; attempt++ {
		if !exists(candidate) {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, attempt)
	}

	return "", metadataCorruption("failed to reserve a quarantine directory", nil, map[string]any{"base": base})
}

// removeNodeIndexEntries drops every by-slug and by-instance entry that resolves
// to nodeID. A corrupt record's slug and instance name cannot be read out of its
// own file, so the indexes are scanned by content instead.
func (s *Store) removeNodeIndexEntries(nodeID string) ([]string, error) {
	removed := []string{}
	for _, dir := range []string{
		filepath.Join(s.cfg.MetadataRoot, "_index", "nodes", "by-slug"),
		filepath.Join(s.cfg.MetadataRoot, "_index", "nodes", "by-instance"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return removed, err
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			path := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return removed, metadataCorruption("failed to read node index entry", err, map[string]any{"path": path})
			}
			if strings.TrimSpace(string(data)) != nodeID {
				continue
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return removed, err
			}
			removed = append(removed, path)
		}
	}

	sort.Strings(removed)
	return removed, nil
}

// QuarantineCorruptNodeRecords moves every unloadable node directory out of
// nodes/ into _quarantine/<timestamp>-<id>/ and drops the index entries that
// pointed at it. Callers must hold the global nodes lock and the per-node lock
// of every corrupt record (see Service.quarantineCorruptNodeRecords).
//
// Index entries go first: they are the only pointers that can resolve a slug or
// instance name to the directory about to move, so removing them before the
// rename means a crash in the middle leaves a skipped-but-present record — the
// state the tool already tolerates — rather than an index aimed at nothing.
func (s *Store) QuarantineCorruptNodeRecords(now time.Time) ([]QuarantinedNodeRecord, error) {
	corrupt, err := s.CorruptNodeRecords()
	if err != nil {
		return nil, err
	}

	quarantined := make([]QuarantinedNodeRecord, 0, len(corrupt))
	if len(corrupt) == 0 {
		return quarantined, nil
	}
	if err := ensureDir(s.quarantineRoot()); err != nil {
		return quarantined, err
	}

	for _, item := range corrupt {
		removed, err := s.removeNodeIndexEntries(item.NodeID)
		if err != nil {
			return quarantined, err
		}

		destination, err := s.quarantineDirPath(now, item.NodeID)
		if err != nil {
			return quarantined, err
		}

		source := s.nodeDir(item.NodeID)
		if err := os.Rename(source, destination); err != nil {
			return quarantined, metadataCorruption("failed to quarantine node metadata", err, map[string]any{
				"source":      source,
				"destination": destination,
			})
		}
		// The record is gone from nodes/; nothing may still resolve its path.
		// (A corrupt record is never cached, so this only matters for one that
		// became corrupt after a good read.)
		s.nodeCache.forget(s.nodePath(item.NodeID))

		record := QuarantinedNodeRecord{
			NodeID:         item.NodeID,
			SourcePath:     source,
			QuarantinePath: destination,
			Reason:         item.Reason,
			RemovedIndexes: removed,
			QuarantinedAt:  now.UTC(),
		}
		if err := writeYAMLFile(filepath.Join(destination, "quarantine.yaml"), record); err != nil {
			return quarantined, err
		}

		s.log().Warn("quarantined corrupt node metadata",
			"node_id", record.NodeID,
			"source", record.SourcePath,
			"quarantine", record.QuarantinePath,
			"reason", record.Reason)
		quarantined = append(quarantined, record)
	}

	return quarantined, nil
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
	return readEvents(s.nodeEventsPath(nodeID), s.log())
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
