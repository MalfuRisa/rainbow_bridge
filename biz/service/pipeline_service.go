package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	plpb "github.com/yi-nology/rainbow_bridge/biz/model/pipeline"
	"gorm.io/gorm"
)

var (
	ErrPipelineNotFound    = errors.New("pipeline not found")
	ErrPipelineKeyRequired = errors.New("pipeline_key is required")
	ErrPipelineKeyExists   = errors.New("pipeline_key already exists")
)

// AddPipeline creates a new scope generation inside the selected environment.
func (s *Service) AddPipelineWithIdentity(ctx context.Context, environmentKey string, pl *plpb.Pipeline,
	expectedEnvironmentID string) error {
	if pl == nil || pl.GetPipelineKey() == "" {
		return ErrPipelineKeyRequired
	}
	if environmentKey == "" {
		return errors.New("environment_key is required")
	}
	entity := &model.Pipeline{
		ScopeID:        uuid.NewString(),
		EnvironmentKey: environmentKey,
		PipelineKey:    pl.GetPipelineKey(),
		PipelineName:   pl.GetPipelineName(),
		Description:    pl.GetDescription(),
		SortOrder:      int(pl.GetSortOrder()),
		IsActive:       pl.GetIsActive(),
	}
	return s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		env, err := resolveEnvironmentForWrite(gateCtx, txLogic, environmentKey, expectedEnvironmentID)
		if err != nil {
			return err
		}
		exists, err := txLogic.pipelineDAO.ExistsByKey(gateCtx, txLogic.db, environmentKey, entity.PipelineKey)
		if err != nil {
			return err
		}
		if exists {
			return ErrPipelineKeyExists
		}
		entity.EnvironmentID = env.EnvironmentID
		if err := txLogic.pipelineDAO.Create(gateCtx, txLogic.db, entity); err != nil {
			return err
		}
		return txLogic.ensurePipelineStateExists(gateCtx, environmentKey, entity.PipelineKey)
	})
}

// UpdatePipeline updates the live scope selected by expected_scope_id.
func (s *Service) UpdatePipelineWithIdentity(ctx context.Context, environmentKey string, pl *plpb.Pipeline,
	expectedScopeID string) error {
	if pl == nil || pl.GetPipelineKey() == "" {
		return ErrPipelineKeyRequired
	}
	if environmentKey == "" {
		return errors.New("environment_key is required")
	}
	entity := &model.Pipeline{
		EnvironmentKey: environmentKey,
		PipelineKey:    pl.GetPipelineKey(),
		PipelineName:   pl.GetPipelineName(),
		Description:    pl.GetDescription(),
		SortOrder:      int(pl.GetSortOrder()),
		IsActive:       pl.GetIsActive(),
	}
	return s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		pipeline, err := resolveScopeForWrite(gateCtx, txLogic, environmentKey, entity.PipelineKey, expectedScopeID)
		if err != nil {
			return err
		}
		if err := txLogic.ensurePipelineUnlocked(gateCtx, environmentKey, entity.PipelineKey); err != nil {
			return err
		}
		entity.ScopeID = pipeline.ScopeID
		entity.EnvironmentID = pipeline.EnvironmentID
		return txLogic.pipelineDAO.Update(gateCtx, txLogic.db, entity)
	})
}

// AddPipeline is the internal/test convenience wrapper.
func (s *Service) AddPipeline(ctx context.Context, environmentKey string, pl *plpb.Pipeline) error {
	return s.AddPipelineWithIdentity(ctx, environmentKey, pl, "")
}

// UpdatePipeline is the internal/test convenience wrapper.
func (s *Service) UpdatePipeline(ctx context.Context, environmentKey string, pl *plpb.Pipeline) error {
	if pl == nil {
		return ErrPipelineKeyRequired
	}
	return s.UpdatePipelineWithIdentity(ctx, environmentKey, pl, "")
}

// DeletePipeline is the internal/test convenience wrapper.
func (s *Service) DeletePipeline(ctx context.Context, environmentKey, pipelineKey string) error {
	_, err := s.DeletePipelineWithIdentity(ctx, environmentKey, pipelineKey, "")
	return err
}

// GetPipeline returns the live generation by key.
func (s *Service) GetPipeline(ctx context.Context, environmentKey, pipelineKey string) (*plpb.Pipeline, error) {
	if pipelineKey == "" {
		return nil, ErrPipelineKeyRequired
	}
	if environmentKey == "" {
		return nil, errors.New("environment_key is required")
	}
	entity, err := resolveScopeByKeys(ctx, s.logic, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	return modelPipelineToPB(entity), nil
}

// ListPipelines returns live pipelines for the live environment.
func (s *Service) ListPipelines(ctx context.Context, environmentKey string, isActive *bool) ([]*plpb.Pipeline, error) {
	if environmentKey == "" {
		return nil, errors.New("environment_key is required")
	}
	entities, err := s.logic.pipelineDAO.List(ctx, s.logic.db, environmentKey, isActive, 0, 0)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, scopeNotFoundError("environment " + environmentKey + " not found")
		}
		if errors.Is(err, db.ErrScopeDeleted) {
			return nil, scopeDeletedError("environment " + environmentKey + " has been deleted")
		}
		if errors.Is(err, db.ErrNoActiveScope) {
			return nil, scopeNotFoundError("environment " + environmentKey + " not found")
		}
		return nil, err
	}
	return pipelineSliceToPB(entities), nil
}

func modelPipelineToPB(pl *model.Pipeline) *plpb.Pipeline {
	if pl == nil {
		return nil
	}
	return &plpb.Pipeline{
		ScopeId:        pl.ScopeID,
		EnvironmentId:  pl.EnvironmentID,
		EnvironmentKey: pl.EnvironmentKey,
		PipelineKey:    pl.PipelineKey,
		PipelineName:   pl.PipelineName,
		Description:    pl.Description,
		SortOrder:      int32(pl.SortOrder),
		IsActive:       pl.IsActive,
	}
}

func pipelineSliceToPB(pls []model.Pipeline) []*plpb.Pipeline {
	list := make([]*plpb.Pipeline, 0, len(pls))
	for i := range pls {
		list = append(list, modelPipelineToPB(&pls[i]))
	}
	return list
}
