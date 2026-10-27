package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	envpb "github.com/yi-nology/rainbow_bridge/biz/model/environment"
	"gorm.io/gorm"
)

var (
	ErrEnvironmentNotFound    = errors.New("environment not found")
	ErrEnvironmentKeyRequired = errors.New("environment_key is required")
	ErrEnvironmentKeyExists   = errors.New("environment_key already exists")
)

// AddEnvironment creates a new environment generation and its default pipeline.
// A key whose old generation was deleted can be created again with fresh
// internal identities.
func (s *Service) AddEnvironment(ctx context.Context, env *envpb.Environment) error {
	if env == nil || env.GetEnvironmentKey() == "" {
		return ErrEnvironmentKeyRequired
	}
	entity := &model.Environment{
		EnvironmentID:   uuid.NewString(),
		EnvironmentKey:  env.GetEnvironmentKey(),
		EnvironmentName: env.GetEnvironmentName(),
		Description:     env.GetDescription(),
		SortOrder:       int(env.GetSortOrder()),
		IsActive:        env.GetIsActive(),
	}
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
			ScopeID:        uuid.NewString(),
			EnvironmentID:  entity.EnvironmentID,
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

// UpdateEnvironment updates the live generation selected by expected id.
func (s *Service) UpdateEnvironmentWithIdentity(ctx context.Context, env *envpb.Environment, expectedEnvironmentID string) error {
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
	return s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		current, err := resolveEnvironmentForWrite(gateCtx, txLogic, entity.EnvironmentKey, expectedEnvironmentID)
		if err != nil {
			return err
		}
		if err := txLogic.ensureEnvironmentMetadataUnlocked(gateCtx, entity.EnvironmentKey); err != nil {
			return err
		}
		entity.EnvironmentID = current.EnvironmentID
		return txLogic.environmentDAO.Update(gateCtx, txLogic.db, entity)
	})
}

// UpdateEnvironment is the internal/test convenience wrapper; management
// handlers must call UpdateEnvironmentWithIdentity so a stale page cannot
// modify a recreated generation.
func (s *Service) UpdateEnvironment(ctx context.Context, env *envpb.Environment) error {
	if env == nil {
		return ErrEnvironmentKeyRequired
	}
	return s.UpdateEnvironmentWithIdentity(ctx, env, "")
}

// DeleteEnvironment is the internal/test convenience wrapper.
func (s *Service) DeleteEnvironment(ctx context.Context, environmentKey string) error {
	_, err := s.DeleteEnvironmentWithIdentity(ctx, environmentKey, "")
	return err
}

// GetEnvironment returns the live generation by key.
func (s *Service) GetEnvironment(ctx context.Context, environmentKey string) (*envpb.Environment, error) {
	if environmentKey == "" {
		return nil, ErrEnvironmentKeyRequired
	}
	env, err := resolveEnvironmentByKey(ctx, s.logic, environmentKey)
	if err != nil {
		return nil, err
	}
	return modelEnvironmentToPB(env), nil
}

// ListEnvironments returns live environments.
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
		EnvironmentId:   env.EnvironmentID,
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
