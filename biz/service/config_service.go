package service

import (
	"context"
	"errors"

	configdb "github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	"gorm.io/gorm"
)

// --------------------- Config operations ---------------------
// 所有写操作在写协调事务内执行:先取得协调行,再检查冻结,再写入;
// 缓存失效只在事务成功提交后进行。

func (s *Service) AddConfig(ctx context.Context, req *common.ResourceConfig) (*common.ResourceConfig, error) {
	if req == nil {
		return nil, errors.New("config payload required")
	}
	entity := pbConfigToModel(req)
	s.normalizeStoredConfig(entity)
	scope := FreezeScopeRef{EnvironmentKey: entity.EnvironmentKey, PipelineKey: entity.PipelineKey}

	err := s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		if err := txLogic.ensurePipelineUnlocked(gateCtx, scope.EnvironmentKey, scope.PipelineKey); err != nil {
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

func (s *Service) UpdateConfig(ctx context.Context, req *common.ResourceConfig, expected string) (*common.ResourceConfig, error) {
	if !configdb.ValidEditRevision(expected) {
		return nil, configdb.ErrEditRevisionInvalid
	}
	if req == nil {
		return nil, errors.New("config payload required")
	}
	entity := pbConfigToModel(req)

	scope := FreezeScopeRef{EnvironmentKey: entity.EnvironmentKey, PipelineKey: entity.PipelineKey}

	var updated *model.Config
	err := s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		if err := txLogic.ensurePipelineUnlocked(gateCtx, scope.EnvironmentKey, scope.PipelineKey); err != nil {
			return err
		}
		if err := txLogic.checkEditBaseline(gateCtx, entity, expected); err != nil {
			return err
		}
		s.normalizeStoredConfig(entity)
		if err := txLogic.UpdateConfig(gateCtx, entity, expected); err != nil {
			return err
		}
		var err error
		updated, err = txLogic.configDAO.GetByResourceKey(gateCtx, txLogic.db, entity.EnvironmentKey, entity.PipelineKey, entity.ResourceKey)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.logic.invalidateConfigCaches(context.WithoutCancel(ctx), []model.Config{*entity})
	return s.decorateConfig(modelConfigToPB(updated)), nil
}

func (s *Service) DeleteConfig(ctx context.Context, environmentKey, pipelineKey, resourceKey string) error {
	err := s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		if err := txLogic.ensurePipelineUnlocked(gateCtx, environmentKey, pipelineKey); err != nil {
			return err
		}
		return txLogic.DeleteConfig(gateCtx, environmentKey, pipelineKey, resourceKey)
	})
	if err != nil {
		return err
	}
	s.logic.invalidateConfigCaches(context.WithoutCancel(ctx), []model.Config{{
		EnvironmentKey: environmentKey,
		PipelineKey:    pipelineKey,
		ResourceKey:    resourceKey,
	}})
	return nil
}

func (s *Service) ListConfigs(ctx context.Context, environmentKey, pipelineKey, typ, minVer, maxVer string, isLatest bool) ([]*common.ResourceConfig, error) {
	configs, err := s.logic.ListConfigs(ctx, environmentKey, pipelineKey, minVer, maxVer, typ, isLatest)
	if err != nil {
		return nil, err
	}
	return s.decorateConfigList(configSliceToPB(configs)), nil
}

func (s *Service) GetConfigDetail(ctx context.Context, environmentKey, pipelineKey, resourceKey string) (*common.ResourceConfig, error) {
	cfg, err := s.logic.configDAO.GetByResourceKey(ctx, s.logic.db, environmentKey, pipelineKey, resourceKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrResourceNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.decorateConfig(modelConfigToPB(cfg)), nil
}
