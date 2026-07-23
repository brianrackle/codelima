package codelima

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

func (s *Store) configurationDir(id string) string {
	return filepath.Join(s.cfg.MetadataRoot, "configurations", id)
}

func (s *Store) configurationPath(id string) string {
	return filepath.Join(s.configurationDir(id), "configuration.yaml")
}

func (s *Store) configurationSlugIndexPath(slug string) string {
	return filepath.Join(s.cfg.MetadataRoot, "_index", "configurations", "by-slug", slug)
}

type builtInConfigurationSpec struct {
	Slug      string
	VCPUs     uint8
	MemoryMiB uint32
	DiskMiB   uint32
}

const (
	xSmallConfigurationSlug = "xsmall"
	legacySmallVCPUs        = uint8(1)
	legacySmallMemoryMiB    = uint32(1 * 1024)
	legacySmallDiskMiB      = uint32(10 * 1024)
)

func builtInConfigurationSpecs() []builtInConfigurationSpec {
	return []builtInConfigurationSpec{
		{Slug: xSmallConfigurationSlug, VCPUs: legacySmallVCPUs, MemoryMiB: legacySmallMemoryMiB, DiskMiB: legacySmallDiskMiB},
		{Slug: DefaultConfigurationSlug, VCPUs: DefaultVCPUs, MemoryMiB: DefaultMemoryMiB, DiskMiB: DefaultDiskMiB},
		{Slug: "medium", VCPUs: 4, MemoryMiB: 8 * 1024, DiskMiB: 50 * 1024},
		{Slug: "large", VCPUs: 6, MemoryMiB: 16 * 1024, DiskMiB: 75 * 1024},
		{Slug: "xlarge", VCPUs: 8, MemoryMiB: 32 * 1024, DiskMiB: 100 * 1024},
	}
}

func (s *Store) ensureBuiltInConfigurations(now time.Time, migrateLegacySmall bool) error {
	for _, spec := range builtInConfigurationSpecs() {
		if configuration, err := s.ConfigurationByIDOrSlug(spec.Slug); err == nil {
			changed := false
			if migrateLegacySmall && spec.Slug == DefaultConfigurationSlug && s.isUntouchedLegacySmall(configuration) {
				configuration.VCPUs = spec.VCPUs
				configuration.MemoryMiB = spec.MemoryMiB
				configuration.DiskMiB = spec.DiskMiB
				changed = true
			}
			// Small was deletable before it became the protected implicit
			// default. Restore a tombstoned record without replacing any values
			// the user customized while it was live.
			if spec.Slug == DefaultConfigurationSlug && configuration.DeletedAt != nil {
				configuration.DeletedAt = nil
				changed = true
			}
			if changed {
				configuration.UpdatedAt = now
				if err := s.SaveConfiguration(configuration); err != nil {
					return err
				}
			}
			continue
		} else if !IsNotFound(err) {
			return err
		}

		if err := s.SaveConfiguration(Configuration{
			ID:                newID(),
			Slug:              spec.Slug,
			Image:             s.cfg.DefaultImage,
			AgentProfileName:  s.cfg.DefaultAgentProfile,
			Environments:      defaultConfigurationEnvironmentSlugs(),
			BootstrapCommands: []string{},
			VCPUs:             spec.VCPUs,
			MemoryMiB:         spec.MemoryMiB,
			DiskMiB:           spec.DiskMiB,
			CreatedAt:         now,
			UpdatedAt:         now,
		}); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) isUntouchedLegacySmall(configuration Configuration) bool {
	return configuration.VCPUs == legacySmallVCPUs &&
		configuration.MemoryMiB == legacySmallMemoryMiB &&
		configuration.DiskMiB == legacySmallDiskMiB &&
		configuration.Image == s.cfg.DefaultImage &&
		configuration.AgentProfileName == s.cfg.DefaultAgentProfile &&
		slices.Equal(configuration.Environments, defaultConfigurationEnvironmentSlugs()) &&
		len(configuration.BootstrapCommands) == 0
}

const legacyDefaultConfigurationSlug = "default"

// retireLegacyDefaultConfiguration hides the former default from live
// configuration surfaces without removing the record. Existing nodes keep the
// configuration ID they were created from and can therefore continue to
// hydrate their historical "default" label and frozen values.
func (s *Store) retireLegacyDefaultConfiguration(now time.Time) error {
	configuration, err := s.ConfigurationByIDOrSlug(legacyDefaultConfigurationSlug)
	if IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if configuration.DeletedAt != nil {
		return nil
	}

	configuration.DeletedAt = &now
	configuration.UpdatedAt = now
	return s.SaveConfiguration(configuration)
}

func defaultConfigurationEnvironmentSlugs() []string {
	return []string{"codex", "claude-code"}
}

func (s *Store) SaveConfiguration(configuration Configuration) error {
	if err := validateConfiguration(configuration); err != nil {
		return err
	}
	if err := ensureDir(s.configurationDir(configuration.ID)); err != nil {
		return err
	}
	if err := ensureDir(filepath.Dir(s.configurationSlugIndexPath(configuration.Slug))); err != nil {
		return err
	}

	var previous *Configuration
	if loaded, err := s.ConfigurationByID(configuration.ID); err == nil {
		previous = &loaded
	}
	if err := writeYAMLFile(s.configurationPath(configuration.ID), configuration); err != nil {
		return err
	}
	if previous != nil && previous.Slug != configuration.Slug {
		_ = os.Remove(s.configurationSlugIndexPath(previous.Slug))
	}
	if configuration.DeletedAt == nil {
		return atomicWriteFile(s.configurationSlugIndexPath(configuration.Slug), []byte(configuration.ID+"\n"), 0o644)
	}
	_ = os.Remove(s.configurationSlugIndexPath(configuration.Slug))
	return nil
}

func (s *Store) ConfigurationByID(id string) (Configuration, error) {
	var configuration Configuration
	path := s.configurationPath(id)
	if err := readYAMLFile(path, &configuration); err != nil {
		if os.IsNotExist(err) {
			return Configuration{}, notFound("configuration not found", map[string]any{"id": id})
		}
		return Configuration{}, metadataCorruption("failed to load configuration", err, map[string]any{"path": path})
	}
	if err := validateConfiguration(configuration); err != nil {
		return Configuration{}, metadataCorruption("invalid configuration metadata", err, map[string]any{"path": path})
	}
	return configuration, nil
}

func (s *Store) ConfigurationByIDOrSlug(value string) (Configuration, error) {
	if exists(s.configurationPath(value)) {
		return s.ConfigurationByID(value)
	}
	indexPath := s.configurationSlugIndexPath(value)
	if data, err := os.ReadFile(indexPath); err == nil {
		return s.ConfigurationByID(strings.TrimSpace(string(data)))
	} else if !os.IsNotExist(err) {
		return Configuration{}, metadataCorruption("failed to read configuration slug index", err, map[string]any{"path": indexPath})
	}
	configurations, err := s.ListConfigurations(true)
	if err != nil {
		return Configuration{}, err
	}
	for _, configuration := range configurations {
		if configuration.Slug == value {
			return configuration, nil
		}
	}
	return Configuration{}, notFound("configuration not found", map[string]any{"query": value})
}

func (s *Store) ListConfigurations(includeDeleted bool) ([]Configuration, error) {
	root := filepath.Join(s.cfg.MetadataRoot, "configurations")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	configurations := make([]Configuration, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		configuration, err := s.ConfigurationByID(entry.Name())
		if err != nil {
			return nil, err
		}
		if configuration.DeletedAt != nil && !includeDeleted {
			continue
		}
		configurations = append(configurations, configuration)
	}
	sort.Slice(configurations, func(i, j int) bool {
		leftRank := builtInConfigurationSortRank(configurations[i].Slug)
		rightRank := builtInConfigurationSortRank(configurations[j].Slug)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return configurations[i].Slug < configurations[j].Slug
	})
	return configurations, nil
}

func builtInConfigurationSortRank(slug string) int {
	switch slug {
	case xSmallConfigurationSlug:
		return 0
	case DefaultConfigurationSlug:
		return 1
	case "medium":
		return 2
	case "large":
		return 3
	case "xlarge":
		return 4
	default:
		return 5
	}
}

func (s *Store) ConfigurationNodes(configurationID string, includeDeleted bool) ([]Node, error) {
	nodes, err := s.ListNodes(includeDeleted)
	if err != nil {
		return nil, err
	}
	result := make([]Node, 0)
	for _, node := range nodes {
		if node.ConfigurationID == configurationID {
			result = append(result, node)
		}
	}
	return result, nil
}

func (s *Store) MissingConfigurationIndexes() ([]string, error) {
	configurations, err := s.ListConfigurations(false)
	if err != nil {
		return nil, err
	}
	missing := make([]string, 0)
	for _, configuration := range configurations {
		if !exists(s.configurationSlugIndexPath(configuration.Slug)) {
			missing = append(missing, fmt.Sprintf("configuration slug index missing for %s", configuration.Slug))
		}
	}
	return missing, nil
}

func validateConfiguration(configuration Configuration) error {
	if strings.TrimSpace(configuration.ID) == "" {
		return invalidArgument("configuration id is required", nil)
	}
	if strings.TrimSpace(configuration.Slug) == "" || slugify(configuration.Slug) != configuration.Slug {
		return invalidArgument("configuration slug must be a lowercase slug", map[string]any{"slug": configuration.Slug})
	}
	if strings.TrimSpace(configuration.Image) == "" {
		return invalidArgument("configuration image is required", nil)
	}
	if strings.TrimSpace(configuration.AgentProfileName) == "" {
		return invalidArgument("configuration agent profile is required", nil)
	}
	if configuration.VCPUs == 0 || configuration.MemoryMiB == 0 || configuration.DiskMiB == 0 {
		return invalidArgument("configuration vcpus, memory, and disk must be positive", nil)
	}
	return nil
}
