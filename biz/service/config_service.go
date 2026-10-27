package service

import (
	"context"
	"errors"

	configdb "github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	"gorm.io/gorm"
)

// AddConfig is the internal/test wrapper; handlers must use
// AddConfigWithIdentity so a stale page cannot write into a recreated scope.
func (s *Service) AddConfig(ctx context.Context, req *common.ResourceConfig) (*common.ResourceConfig, error) {
	if maintenanceActive(s) {
		return nil, maintenanceError()
	}
	return s.AddConfigWithIdentity(ctx, req, "")
}

func (s *Service) AddConfigWithIdentity(ctx context.Context, req *common.ResourceConfig,
	expectedScopeID string) (*common.ResourceConfig, error) {
	if req == nil {
		return nil, errors.New("config payload required")
	}
	entity := pbConfigToModel(req)
	s.normalizeStoredConfig(entity)
	var err error
	err = s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		pipeline, err := resolveScopeForWrite(gateCtx, txLogic, entity.EnvironmentKey, entity.PipelineKey, expectedScopeID)
		if err != nil {
			return err
		}
		entity.ScopeID = pipeline.ScopeID
		if err := txLogic.ensurePipelineUnlocked(gateCtx, entity.EnvironmentKey, entity.PipelineKey); err != nil {
			return err
		}
		return txLogic.AddConfig(gateCtx, entity)
	})
	if err != nil {
		return nil, err
	}
	s.logic.invalidateConfigCaches(context.WithoutCancel(ctx), []model.Config{*entity})
	return s.decorateConfig(modelConfigToPB(entity)), nil
}

// UpdateConfig is the internal/test wrapper.
func (s *Service) UpdateConfig(ctx context.Context, req *common.ResourceConfig, expected string) (*common.ResourceConfig, error) {
	if maintenanceActive(s) {
		return nil, maintenanceError()
	}
	return s.UpdateConfigWithIdentity(ctx, req, expected, "")
}

func (s *Service) UpdateConfigWithIdentity(ctx context.Context, req *common.ResourceConfig, expected string,
	expectedScopeID string) (*common.ResourceConfig, error) {
	if !configdb.ValidEditRevision(expected) {
		return nil, configdb.ErrEditRevisionInvalid
	}
	if req == nil {
		return nil, errors.New("config payload required")
	}
	entity := pbConfigToModel(req)
	var updated *model.Config
	err := s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		pipeline, err := resolveScopeForWrite(gateCtx, txLogic, entity.EnvironmentKey, entity.PipelineKey, expectedScopeID)
		if err != nil {
			return err
		}
		entity.ScopeID = pipeline.ScopeID
		if err := txLogic.ensurePipelineUnlocked(gateCtx, entity.EnvironmentKey, entity.PipelineKey); err != nil {
			return err
		}
		if err := txLogic.checkEditBaseline(gateCtx, entity, expected); err != nil {
			return err
		}
		s.normalizeStoredConfig(entity)
		if err := txLogic.UpdateConfig(gateCtx, entity, expected); err != nil {
			return err
		}
		updated, err = txLogic.configDAO.GetByScopeAndResourceKey(gateCtx, txLogic.db, entity.ScopeID, entity.ResourceKey)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.logic.invalidateConfigCaches(context.WithoutCancel(ctx), []model.Config{*entity})
	return s.decorateConfig(modelConfigToPB(updated)), nil
}

// DeleteConfig is the internal/test wrapper.
func (s *Service) DeleteConfig(ctx context.Context, environmentKey, pipelineKey, resourceKey string) error {
	if maintenanceActive(s) {
		return maintenanceError()
	}
	return s.DeleteConfigWithIdentity(ctx, environmentKey, pipelineKey, resourceKey, "")
}

func (s *Service) DeleteConfigWithIdentity(ctx context.Context, environmentKey, pipelineKey, resourceKey,
	expectedScopeID string) error {
	var deleted model.Config
	err := s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		pipeline, err := resolveScopeForWrite(gateCtx, txLogic, environmentKey, pipelineKey, expectedScopeID)
		if err != nil {
			return err
		}
		if err := txLogic.ensurePipelineUnlocked(gateCtx, environmentKey, pipelineKey); err != nil {
			return err
		}
		if _, err := txLogic.configDAO.GetByScopeAndResourceKey(gateCtx, txLogic.db, pipeline.ScopeID, resourceKey); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrResourceNotFound
			}
			return err
		}
		if err := txLogic.configDAO.DeleteByScopeAndResourceKey(gateCtx, txLogic.db, pipeline.ScopeID, resourceKey); err != nil {
			return err
		}
		deleted = model.Config{
			ScopeID:        pipeline.ScopeID,
			EnvironmentKey: environmentKey,
			PipelineKey:    pipelineKey,
			ResourceKey:    resourceKey,
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.logic.invalidateConfigCaches(context.WithoutCancel(ctx), []model.Config{deleted})
	return nil
}

// ValidateReadScope checks an optional scope_id on read-only management APIs.
// Empty keeps the legacy key-based behavior; a supplied value must match the
// live generation.
func (s *Service) ValidateReadScope(ctx context.Context, environmentKey, pipelineKey, requestedScopeID string) error {
	_, err := resolveReadScope(ctx, s.logic, environmentKey, pipelineKey, requestedScopeID)
	return err
}

func (s *Service) ListConfigs(ctx context.Context, environmentKey, pipelineKey, typ, minVer, maxVer string, isLatest bool) ([]*common.ResourceConfig, error) {
	configs, err := s.logic.ListConfigs(ctx, environmentKey, pipelineKey, minVer, maxVer, typ, isLatest)
	if err != nil {
		return nil, mapScopeReadError(err, environmentKey, pipelineKey)
	}
	return s.decorateConfigList(configSliceToPB(configs)), nil
}

func (s *Service) GetConfigDetail(ctx context.Context, environmentKey, pipelineKey, resourceKey string) (*common.ResourceConfig, error) {
	pipeline, err := resolveScopeByKeys(ctx, s.logic, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	cfg, err := s.logic.configDAO.GetByScopeAndResourceKey(ctx, s.logic.db, pipeline.ScopeID, resourceKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrResourceNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.decorateConfig(modelConfigToPB(cfg)), nil
}
