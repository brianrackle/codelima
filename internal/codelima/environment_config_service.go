package codelima

type EnvironmentConfigCreateInput struct {
	Slug              string
	BootstrapCommands []string
}

type EnvironmentConfigUpdateInput struct {
	BootstrapCommands      []string
	ClearBootstrapCommands bool
}

func (s *Service) EnvironmentConfigCreate(input EnvironmentConfigCreateInput) (EnvironmentConfig, error) {
	if err := s.ensureReadyForWrite(); err != nil {
		return EnvironmentConfig{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "environments")
	if err != nil {
		return EnvironmentConfig{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	if input.Slug == "" {
		return EnvironmentConfig{}, invalidArgument("environment slug is required", nil)
	}

	if err := s.ensureUniqueEnvironmentConfigSlug(input.Slug, ""); err != nil {
		return EnvironmentConfig{}, err
	}

	now := s.now()
	config := EnvironmentConfig{
		ID:                newID(),
		Slug:              input.Slug,
		BootstrapCommands: append([]string(nil), input.BootstrapCommands...),
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := s.store.SaveEnvironmentConfig(config); err != nil {
		return EnvironmentConfig{}, err
	}

	return config, nil
}

func (s *Service) EnvironmentConfigList(includeDeleted bool) ([]EnvironmentConfig, error) {
	if err := s.EnsureReady(false); err != nil {
		return nil, err
	}

	return s.store.ListEnvironmentConfigs(includeDeleted)
}

func (s *Service) EnvironmentConfigShow(value string) (EnvironmentConfig, error) {
	if err := s.EnsureReady(false); err != nil {
		return EnvironmentConfig{}, err
	}

	return s.store.EnvironmentConfigByIDOrSlug(value)
}

func (s *Service) EnvironmentConfigUpdate(value string, input EnvironmentConfigUpdateInput) (EnvironmentConfig, error) {
	if err := s.ensureReadyForWrite(); err != nil {
		return EnvironmentConfig{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "environments")
	if err != nil {
		return EnvironmentConfig{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	config, err := s.store.EnvironmentConfigByIDOrSlug(value)
	if err != nil {
		return EnvironmentConfig{}, err
	}

	if input.ClearBootstrapCommands {
		config.BootstrapCommands = []string{}
	} else if input.BootstrapCommands != nil {
		config.BootstrapCommands = append([]string(nil), input.BootstrapCommands...)
	}

	config.UpdatedAt = s.now()
	if err := s.store.SaveEnvironmentConfig(config); err != nil {
		return EnvironmentConfig{}, err
	}

	return config, nil
}

func (s *Service) EnvironmentConfigDelete(value string) (EnvironmentConfig, error) {
	if err := s.ensureReadyForWrite(); err != nil {
		return EnvironmentConfig{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "environments", "configurations")
	if err != nil {
		return EnvironmentConfig{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	config, err := s.store.EnvironmentConfigByIDOrSlug(value)
	if err != nil {
		return EnvironmentConfig{}, err
	}

	configurations, err := s.store.ListConfigurations(false)
	if err != nil {
		return EnvironmentConfig{}, err
	}

	for _, configuration := range configurations {
		for _, slug := range configuration.Environments {
			if slug == config.Slug {
				return EnvironmentConfig{}, preconditionFailed("environment is assigned to a configuration", map[string]any{"environment": config.Slug, "configuration_id": configuration.ID, "configuration_slug": configuration.Slug})
			}
		}
	}
	// Pre-v3 source compatibility for tests and in-process callers. Schema-v3
	// homes never create project metadata and the public project command is gone.
	projects, err := s.store.ListProjects(false)
	if err != nil {
		return EnvironmentConfig{}, err
	}
	for _, project := range projects {
		for _, slug := range project.EnvironmentConfigs {
			if slug == config.Slug {
				return EnvironmentConfig{}, preconditionFailed("environment is assigned to a legacy project", map[string]any{"environment": config.Slug, "project_id": project.ID})
			}
		}
	}

	now := s.now()
	config.DeletedAt = &now
	config.UpdatedAt = now
	if err := s.store.SaveEnvironmentConfig(config); err != nil {
		return EnvironmentConfig{}, err
	}

	return config, nil
}
