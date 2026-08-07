package codelima

import "context"

type EnvironmentConfigCreateInput struct {
	Slug              string
	BootstrapCommands []string
}

type EnvironmentConfigUpdateInput struct {
	BootstrapCommands      []string
	ClearBootstrapCommands bool
}

func (s *Service) EnvironmentConfigCreate(ctx context.Context, input EnvironmentConfigCreateInput) (EnvironmentConfig, error) {
	if err := s.ensureReadyForWrite(ctx); err != nil {
		return EnvironmentConfig{}, err
	}

	var config EnvironmentConfig
	if err := s.withLocks(ctx, []lockKey{lockEnvironments}, nil, func() error {
		if input.Slug == "" {
			return invalidArgument("environment slug is required", nil)
		}

		if err := s.ensureUniqueEnvironmentConfigSlug(input.Slug, ""); err != nil {
			return err
		}

		now := s.now()
		config = EnvironmentConfig{
			ID:                newID(),
			Slug:              input.Slug,
			BootstrapCommands: append([]string(nil), input.BootstrapCommands...),
			CreatedAt:         now,
			UpdatedAt:         now,
		}

		return s.store.SaveEnvironmentConfig(config)
	}); err != nil {
		return EnvironmentConfig{}, err
	}

	return config, nil
}

func (s *Service) EnvironmentConfigList(ctx context.Context, includeDeleted bool) ([]EnvironmentConfig, error) {
	if err := s.EnsureReady(ctx, false); err != nil {
		return nil, err
	}

	return s.store.ListEnvironmentConfigs(includeDeleted)
}

func (s *Service) EnvironmentConfigShow(ctx context.Context, value string) (EnvironmentConfig, error) {
	if err := s.EnsureReady(ctx, false); err != nil {
		return EnvironmentConfig{}, err
	}

	return s.store.EnvironmentConfigByIDOrSlug(value)
}

func (s *Service) EnvironmentConfigUpdate(ctx context.Context, value string, input EnvironmentConfigUpdateInput) (EnvironmentConfig, error) {
	if err := s.ensureReadyForWrite(ctx); err != nil {
		return EnvironmentConfig{}, err
	}

	var config EnvironmentConfig
	if err := s.withLocks(ctx, []lockKey{lockEnvironments}, nil, func() error {
		var err error
		config, err = s.store.EnvironmentConfigByIDOrSlug(value)
		if err != nil {
			return err
		}

		if input.ClearBootstrapCommands {
			config.BootstrapCommands = []string{}
		} else if input.BootstrapCommands != nil {
			config.BootstrapCommands = append([]string(nil), input.BootstrapCommands...)
		}

		config.UpdatedAt = s.now()
		return s.store.SaveEnvironmentConfig(config)
	}); err != nil {
		return EnvironmentConfig{}, err
	}

	return config, nil
}

func (s *Service) EnvironmentConfigDelete(ctx context.Context, value string) (EnvironmentConfig, error) {
	if err := s.ensureReadyForWrite(ctx); err != nil {
		return EnvironmentConfig{}, err
	}

	var config EnvironmentConfig
	if err := s.withLocks(ctx, []lockKey{lockEnvironments, lockConfigurations}, nil, func() error {
		var err error
		config, err = s.store.EnvironmentConfigByIDOrSlug(value)
		if err != nil {
			return err
		}

		configurations, err := s.store.ListConfigurations(false)
		if err != nil {
			return err
		}

		for _, configuration := range configurations {
			for _, slug := range configuration.Environments {
				if slug == config.Slug {
					return preconditionFailed("environment is assigned to a configuration", map[string]any{"environment": config.Slug, "configuration_id": configuration.ID, "configuration_slug": configuration.Slug})
				}
			}
		}
		now := s.now()
		config.DeletedAt = &now
		config.UpdatedAt = now
		return s.store.SaveEnvironmentConfig(config)
	}); err != nil {
		return EnvironmentConfig{}, err
	}

	return config, nil
}
