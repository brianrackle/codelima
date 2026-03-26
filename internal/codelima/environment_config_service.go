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
	if err := s.EnsureReady(false); err != nil {
		return EnvironmentConfig{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "environment-configs")
	if err != nil {
		return EnvironmentConfig{}, err
	}
	defer func() {
		_ = lockSet.Close()
	}()

	if input.Slug == "" {
		return EnvironmentConfig{}, invalidArgument("environment config slug is required", nil)
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
	if err := s.EnsureReady(false); err != nil {
		return EnvironmentConfig{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "environment-configs")
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
	if err := s.EnsureReady(false); err != nil {
		return EnvironmentConfig{}, err
	}

	lockSet, err := acquireLocks(s.cfg.MetadataRoot, "environment-configs", "projects")
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

	projects, err := s.store.ListProjects(false)
	if err != nil {
		return EnvironmentConfig{}, err
	}

	for _, project := range projects {
		for _, slug := range project.EnvironmentConfigs {
			if slug == config.Slug {
				return EnvironmentConfig{}, preconditionFailed("environment config is assigned to a project", map[string]any{"environment_config": config.Slug, "project_id": project.ID, "project_slug": project.Slug})
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
