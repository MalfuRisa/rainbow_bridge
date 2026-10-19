package service

import (
	"context"
	"errors"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	envpb "github.com/yi-nology/rainbow_bridge/biz/model/environment"
	"gorm.io/gorm"
)

var (
	ErrEnvironmentNotFound    = errors.New("environment not found")
	ErrEnvironmentKeyRequired = errors.New("environment_key is required")
	ErrEnvironmentKeyExists   = errors.New("environment_key already exists")
)

// AddEnvironment creates a new environment with default pipeline.
func (s *Service) AddEnvironment(ctx context.Context, env *envpb.Environment) error {
	if env == nil || env.GetEnvironmentKey() == "" {
		return ErrEnvironmentKeyRequired
	}

	entity := &model.Environment{
		EnvironmentKey:  env.GetEnvironmentKey(),
		EnvironmentName: env.GetEnvironmentName(),
		Description:     env.GetDescription(),
		SortOrder:       int(env.GetSortOrder()),
		IsActive:        env.GetIsActive(),
	}

	// 在写协调事务内完成:存在性检查 → 创建环境 → 创建默认渠道 → 补建渠道冻结状态行
	return s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		exists, err := txLogic.environmentDAO.ExistsByKey(gateCtx, txLogic.db, entity.EnvironmentKey)
		if err != nil {
			return err
		}
		if exists {
			return ErrEnvironmentKeyExists
		}
		if err := txLogic.environmentDAO.Create(gateCtx, txLogic.db, entity); err != nil {
			return err
		}
		defaultPipeline := &model.Pipeline{
			EnvironmentKey: entity.EnvironmentKey,
			PipelineKey:    "default",
			PipelineName:   "默认渠道",
			Description:    "系统默认渠道",
			SortOrder:      0,
			IsActive:       true,
		}
		if err := txLogic.pipelineDAO.Create(gateCtx, txLogic.db, defaultPipeline); err != nil {
			return err
		}
		return txLogic.ensurePipelineStateExists(gateCtx, entity.EnvironmentKey, defaultPipeline.PipelineKey)
	})
}

// UpdateEnvironment updates an existing environment.
func (s *Service) UpdateEnvironment(ctx context.Context, env *envpb.Environment) error {
	if env == nil || env.GetEnvironmentKey() == "" {
		return ErrEnvironmentKeyRequired
	}

	entity := &model.Environment{
		EnvironmentKey:  env.GetEnvironmentKey(),
		EnvironmentName: env.GetEnvironmentName(),
		Description:     env.GetDescription(),
		SortOrder:       int(env.GetSortOrder()),
		IsActive:        env.GetIsActive(),
	}
	// 环境内存在 locked 渠道时整体拒绝(名称/激活状态变化不作用于冻结范围)
	return s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		if _, err := txLogic.environmentDAO.GetByKey(gateCtx, txLogic.db, entity.EnvironmentKey); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnvironmentNotFound
			}
			return err
		}
		if err := txLogic.ensureEnvironmentMetadataUnlocked(gateCtx, entity.EnvironmentKey); err != nil {
			return err
		}
		return txLogic.environmentDAO.Update(gateCtx, txLogic.db, entity)
	})
}

// DeleteEnvironment deletes an environment by key.
func (s *Service) DeleteEnvironment(ctx context.Context, environmentKey string) error {
	if environmentKey == "" {
		return ErrEnvironmentKeyRequired
	}

	// 冻结状态与审计记录在父级删除后保留(不随删除清除)。
	return s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		if _, err := txLogic.environmentDAO.GetByKey(gateCtx, txLogic.db, environmentKey); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnvironmentNotFound
			}
			return err
		}
		if err := txLogic.ensureEnvironmentMetadataUnlocked(gateCtx, environmentKey); err != nil {
			return err
		}
		return txLogic.environmentDAO.Delete(gateCtx, txLogic.db, environmentKey)
	})
}

// GetEnvironment returns an environment by key.
func (s *Service) GetEnvironment(ctx context.Context, environmentKey string) (*envpb.Environment, error) {
	if environmentKey == "" {
		return nil, ErrEnvironmentKeyRequired
	}

	entity, err := s.logic.environmentDAO.GetByKey(ctx, s.logic.db, environmentKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEnvironmentNotFound
		}
		return nil, err
	}

	return modelEnvironmentToPB(entity), nil
}

// ListEnvironments returns all environments with optional active filter.
func (s *Service) ListEnvironments(ctx context.Context, isActive *bool) ([]*envpb.Environment, error) {
	entities, err := s.logic.environmentDAO.List(ctx, s.logic.db, isActive, 0, 0)
	if err != nil {
		return nil, err
	}

	return environmentSliceToPB(entities), nil
}

func modelEnvironmentToPB(env *model.Environment) *envpb.Environment {
	if env == nil {
		return nil
	}
	return &envpb.Environment{
		EnvironmentKey:  env.EnvironmentKey,
		EnvironmentName: env.EnvironmentName,
		Description:     env.Description,
		SortOrder:       int32(env.SortOrder),
		IsActive:        env.IsActive,
	}
}

func environmentSliceToPB(envs []model.Environment) []*envpb.Environment {
	list := make([]*envpb.Environment, 0, len(envs))
	for i := range envs {
		list = append(list, modelEnvironmentToPB(&envs[i]))
	}
	return list
}
