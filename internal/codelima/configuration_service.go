package codelima

import (
	"strings"
)

type ConfigurationCreateInput struct {
	Slug              string
	Image             *string
	AgentProfile      *string
	Environments      []string
	BootstrapCommands []string
	VCPUs             *uint8
	MemoryMiB         *uint32
	DiskMiB           *uint32
}

type ConfigurationUpdateInput = ConfigurationCreateInput

type ConfigurationCloneInput struct {
	Source string
	Slug   string
}

func (s *Service) ConfigurationCreate(input ConfigurationCreateInput) (Configuration, error) {
	if err := s.ensureReadyForWrite(); err != nil {
		return Configuration{}, err
	}
	locks, err := acquireLocks(s.cfg.MetadataRoot, "configurations", "environments")
	if err != nil {
		return Configuration{}, err
	}
	defer func() { _ = locks.Close() }()

	if strings.TrimSpace(input.Slug) == "" {
		return Configuration{}, invalidArgument("configuration slug is required", nil)
	}
	if err := s.ensureUniqueConfigurationSlug(input.Slug, ""); err != nil {
		return Configuration{}, err
	}
	base, err := s.store.ConfigurationByIDOrSlug(DefaultConfigurationSlug)
	if err != nil {
		return Configuration{}, err
	}
	now := s.now()
	configuration := cloneConfiguration(base)
	configuration.ID = newID()
	configuration.Slug = input.Slug
	configuration.CreatedAt = now
	configuration.UpdatedAt = now
	configuration.DeletedAt = nil
	if err := s.applyConfigurationInput(&configuration, input, true); err != nil {
		return Configuration{}, err
	}
	if err := s.store.SaveConfiguration(configuration); err != nil {
		return Configuration{}, err
	}
	return configuration, nil
}

func (s *Service) ConfigurationClone(input ConfigurationCloneInput) (Configuration, error) {
	if err := s.ensureReadyForWrite(); err != nil {
		return Configuration{}, err
	}
	locks, err := acquireLocks(s.cfg.MetadataRoot, "configurations")
	if err != nil {
		return Configuration{}, err
	}
	defer func() { _ = locks.Close() }()
	if strings.TrimSpace(input.Slug) == "" {
		return Configuration{}, invalidArgument("configuration clone requires --slug", nil)
	}
	if err := s.ensureUniqueConfigurationSlug(input.Slug, ""); err != nil {
		return Configuration{}, err
	}
	source, err := s.store.ConfigurationByIDOrSlug(input.Source)
	if err != nil {
		return Configuration{}, err
	}
	now := s.now()
	cloned := cloneConfiguration(source)
	cloned.ID = newID()
	cloned.Slug = input.Slug
	cloned.CreatedAt = now
	cloned.UpdatedAt = now
	cloned.DeletedAt = nil
	if err := s.store.SaveConfiguration(cloned); err != nil {
		return Configuration{}, err
	}
	return cloned, nil
}

func (s *Service) ConfigurationList(includeDeleted bool) ([]Configuration, error) {
	if err := s.EnsureReady(false); err != nil {
		return nil, err
	}
	return s.store.ListConfigurations(includeDeleted)
}

func (s *Service) ConfigurationShow(value string) (Configuration, error) {
	if err := s.EnsureReady(false); err != nil {
		return Configuration{}, err
	}
	return s.store.ConfigurationByIDOrSlug(value)
}

func (s *Service) ConfigurationUpdate(value string, input ConfigurationUpdateInput) (Configuration, error) {
	if err := s.ensureReadyForWrite(); err != nil {
		return Configuration{}, err
	}
	locks, err := acquireLocks(s.cfg.MetadataRoot, "configurations", "environments")
	if err != nil {
		return Configuration{}, err
	}
	defer func() { _ = locks.Close() }()
	configuration, err := s.store.ConfigurationByIDOrSlug(value)
	if err != nil {
		return Configuration{}, err
	}
	if input.Slug != "" && input.Slug != configuration.Slug {
		if configuration.Slug == DefaultConfigurationSlug {
			return Configuration{}, preconditionFailed("the default configuration cannot be renamed", nil)
		}
		if err := s.ensureUniqueConfigurationSlug(input.Slug, configuration.ID); err != nil {
			return Configuration{}, err
		}
		configuration.Slug = input.Slug
	}
	if err := s.applyConfigurationInput(&configuration, input, false); err != nil {
		return Configuration{}, err
	}
	configuration.UpdatedAt = s.now()
	if err := s.store.SaveConfiguration(configuration); err != nil {
		return Configuration{}, err
	}
	return configuration, nil
}

func (s *Service) ConfigurationDelete(value string) (Configuration, error) {
	if err := s.ensureReadyForWrite(); err != nil {
		return Configuration{}, err
	}
	locks, err := acquireLocks(s.cfg.MetadataRoot, "configurations", "nodes")
	if err != nil {
		return Configuration{}, err
	}
	defer func() { _ = locks.Close() }()
	configuration, err := s.store.ConfigurationByIDOrSlug(value)
	if err != nil {
		return Configuration{}, err
	}
	if configuration.Slug == DefaultConfigurationSlug {
		return Configuration{}, preconditionFailed("the default configuration cannot be deleted", nil)
	}
	nodes, err := s.store.ConfigurationNodes(configuration.ID, false)
	if err != nil {
		return Configuration{}, err
	}
	if len(nodes) != 0 {
		return Configuration{}, preconditionFailed("configuration is referenced by nodes", map[string]any{"configuration": configuration.Slug, "node_count": len(nodes)})
	}
	now := s.now()
	configuration.DeletedAt = &now
	configuration.UpdatedAt = now
	if err := s.store.SaveConfiguration(configuration); err != nil {
		return Configuration{}, err
	}
	return configuration, nil
}

func (s *Service) applyConfigurationInput(configuration *Configuration, input ConfigurationCreateInput, creating bool) error {
	if input.Image != nil {
		configuration.Image = strings.TrimSpace(*input.Image)
	}
	if input.AgentProfile != nil {
		profile := strings.TrimSpace(*input.AgentProfile)
		if _, err := s.store.LoadAgentProfile(profile); err != nil {
			return err
		}
		configuration.AgentProfileName = profile
	}
	if input.Environments != nil {
		resolved, err := s.resolveEnvironmentConfigRefs(input.Environments)
		if err != nil {
			return err
		}
		configuration.Environments = resolved
	} else if creating {
		configuration.Environments = append([]string(nil), configuration.Environments...)
	}
	if input.BootstrapCommands != nil {
		configuration.BootstrapCommands = append([]string(nil), input.BootstrapCommands...)
	}
	if input.VCPUs != nil {
		configuration.VCPUs = *input.VCPUs
	}
	if input.MemoryMiB != nil {
		configuration.MemoryMiB = *input.MemoryMiB
	}
	if input.DiskMiB != nil {
		configuration.DiskMiB = *input.DiskMiB
	}
	return validateConfiguration(*configuration)
}

func (s *Service) ensureUniqueConfigurationSlug(slug, currentID string) error {
	if slugify(slug) != slug {
		return invalidArgument("configuration slug must be a lowercase slug", map[string]any{"slug": slug})
	}
	configurations, err := s.store.ListConfigurations(false)
	if err != nil {
		return err
	}
	for _, configuration := range configurations {
		if configuration.Slug == slug && configuration.ID != currentID {
			return preconditionFailed("configuration slug already exists", map[string]any{"slug": slug})
		}
	}
	return nil
}

func cloneConfiguration(source Configuration) Configuration {
	cloned := source
	cloned.Environments = append([]string(nil), source.Environments...)
	cloned.BootstrapCommands = append([]string(nil), source.BootstrapCommands...)
	return cloned
}
