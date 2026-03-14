package codelima

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (s *Store) environmentConfigDir(configID string) string {
	return filepath.Join(s.cfg.MetadataRoot, "environment-configs", configID)
}

func (s *Store) environmentConfigPath(configID string) string {
	return filepath.Join(s.environmentConfigDir(configID), "environment-config.yaml")
}

func (s *Store) environmentConfigSlugIndexPath(slug string) string {
	return filepath.Join(s.cfg.MetadataRoot, "_index", "environment-configs", "by-slug", slug)
}

func (s *Store) SaveEnvironmentConfig(config EnvironmentConfig) error {
	if err := ensureDir(s.environmentConfigDir(config.ID)); err != nil {
		return err
	}

	var previous *EnvironmentConfig
	if loaded, err := s.EnvironmentConfigByID(config.ID); err == nil {
		previous = &loaded
	}

	if err := writeYAMLFile(s.environmentConfigPath(config.ID), config); err != nil {
		return err
	}

	if previous != nil && previous.Slug != config.Slug {
		_ = os.Remove(s.environmentConfigSlugIndexPath(previous.Slug))
	}

	if config.DeletedAt == nil {
		if err := atomicWriteFile(s.environmentConfigSlugIndexPath(config.Slug), []byte(config.ID+"\n"), 0o644); err != nil {
			return err
		}
	} else {
		_ = os.Remove(s.environmentConfigSlugIndexPath(config.Slug))
	}

	return nil
}

func (s *Store) EnvironmentConfigByID(configID string) (EnvironmentConfig, error) {
	var config EnvironmentConfig
	path := s.environmentConfigPath(configID)
	if err := readYAMLFile(path, &config); err != nil {
		if os.IsNotExist(err) {
			return EnvironmentConfig{}, notFound("environment config not found", map[string]any{"id": configID})
		}

		return EnvironmentConfig{}, metadataCorruption("failed to load environment config", err, map[string]any{"path": path})
	}

	return config, nil
}

func (s *Store) EnvironmentConfigByIDOrSlug(value string) (EnvironmentConfig, error) {
	if exists(s.environmentConfigPath(value)) {
		return s.EnvironmentConfigByID(value)
	}

	indexPath := s.environmentConfigSlugIndexPath(value)
	if exists(indexPath) {
		data, err := os.ReadFile(indexPath)
		if err != nil {
			return EnvironmentConfig{}, metadataCorruption("failed to read environment config slug index", err, map[string]any{"path": indexPath})
		}

		return s.EnvironmentConfigByID(strings.TrimSpace(string(data)))
	}

	configs, err := s.ListEnvironmentConfigs(false)
	if err != nil {
		return EnvironmentConfig{}, err
	}

	for _, config := range configs {
		if config.Slug == value {
			return config, nil
		}
	}

	configs, err = s.ListEnvironmentConfigs(true)
	if err != nil {
		return EnvironmentConfig{}, err
	}

	var deletedMatch *EnvironmentConfig
	for _, config := range configs {
		if config.Slug == value {
			configCopy := config
			deletedMatch = &configCopy
		}
	}

	if deletedMatch != nil {
		return *deletedMatch, nil
	}

	return EnvironmentConfig{}, notFound("environment config not found", map[string]any{"query": value})
}

func (s *Store) ListEnvironmentConfigs(includeDeleted bool) ([]EnvironmentConfig, error) {
	root := filepath.Join(s.cfg.MetadataRoot, "environment-configs")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	configs := []EnvironmentConfig{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		config, err := s.EnvironmentConfigByID(entry.Name())
		if err != nil {
			return nil, err
		}

		if config.DeletedAt != nil && !includeDeleted {
			continue
		}

		configs = append(configs, config)
	}

	sort.Slice(configs, func(i, j int) bool {
		return configs[i].CreatedAt.Before(configs[j].CreatedAt)
	})

	return configs, nil
}

func (s *Store) MissingEnvironmentConfigIndexes() ([]string, error) {
	configs, err := s.ListEnvironmentConfigs(false)
	if err != nil {
		return nil, err
	}

	missing := []string{}
	for _, config := range configs {
		if !exists(s.environmentConfigSlugIndexPath(config.Slug)) {
			missing = append(missing, "environment config slug index missing for "+config.Slug)
		}
	}

	return missing, nil
}
