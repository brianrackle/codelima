package codelima

import (
	"fmt"
	"os"
	"path/filepath"
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

func (s *Store) ensureDefaultConfiguration(now time.Time) error {
	if _, err := s.ConfigurationByIDOrSlug(DefaultConfigurationSlug); err == nil {
		return nil
	} else {
		var appErr *AppError
		if !As(err, &appErr) || appErr.Category != "NotFound" {
			return err
		}
	}

	return s.SaveConfiguration(Configuration{
		ID:                newID(),
		Slug:              DefaultConfigurationSlug,
		Image:             s.cfg.DefaultImage,
		AgentProfileName:  s.cfg.DefaultAgentProfile,
		Environments:      defaultConfigurationEnvironmentSlugs(),
		BootstrapCommands: []string{},
		VCPUs:             DefaultVCPUs,
		MemoryMiB:         DefaultMemoryMiB,
		DiskMiB:           DefaultDiskMiB,
		CreatedAt:         now,
		UpdatedAt:         now,
	})
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
		if configurations[i].Slug == DefaultConfigurationSlug {
			return configurations[j].Slug != DefaultConfigurationSlug
		}
		if configurations[j].Slug == DefaultConfigurationSlug {
			return false
		}
		return configurations[i].Slug < configurations[j].Slug
	})
	return configurations, nil
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
