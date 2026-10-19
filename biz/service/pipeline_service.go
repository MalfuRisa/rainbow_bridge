package service

import (
	"context"
	"errors"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	plpb "github.com/yi-nology/rainbow_bridge/biz/model/pipeline"
	"gorm.io/gorm"
)

var (
	ErrPipelineNotFound    = errors.New("pipeline not found")
	ErrPipelineKeyRequired = errors.New("pipeline_key is required")
	ErrPipelineKeyExists   = errors.New("pipeline_key already exists")
)

// AddPipeline creates a new pipeline.
func (s *Service) AddPipeline(ctx context.Context, environmentKey string, pl *plpb.Pipeline) error {
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
	// 在同环境新建其他渠道允许(不修改已冻结渠道);新渠道补建 unlocked 状态行。
	return s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		exists, err := txLogic.pipelineDAO.ExistsByKey(gateCtx, txLogic.db, environmentKey, entity.PipelineKey)
		if err != nil {
			return err
		}
		if exists {
			return ErrPipelineKeyExists
		}
		if err := txLogic.pipelineDAO.Create(gateCtx, txLogic.db, entity); err != nil {
			return err
		}
		return txLogic.ensurePipelineStateExists(gateCtx, environmentKey, entity.PipelineKey)
	})
}

// UpdatePipeline updates an existing pipeline.
func (s *Service) UpdatePipeline(ctx context.Context, environmentKey string, pl *plpb.Pipeline) error {
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
	// 冻结范围的渠道属性不允许被修改。
	return s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		if _, err := txLogic.pipelineDAO.GetByKey(gateCtx, txLogic.db, environmentKey, entity.PipelineKey); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPipelineNotFound
			}
			return err
		}
		if err := txLogic.ensurePipelineUnlocked(gateCtx, environmentKey, entity.PipelineKey); err != nil {
			return err
		}
		return txLogic.pipelineDAO.Update(gateCtx, txLogic.db, entity)
	})
}

// DeletePipeline deletes a pipeline by key.
// 冻结状态与审计记录保留(删除重建时不复用已发生过的 revision)。
func (s *Service) DeletePipeline(ctx context.Context, environmentKey, pipelineKey string) error {
	if pipelineKey == "" {
		return ErrPipelineKeyRequired
	}
	if environmentKey == "" {
		return errors.New("environment_key is required")
	}

	return s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		if _, err := txLogic.pipelineDAO.GetByKey(gateCtx, txLogic.db, environmentKey, pipelineKey); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPipelineNotFound
			}
			return err
		}
		if err := txLogic.ensurePipelineUnlocked(gateCtx, environmentKey, pipelineKey); err != nil {
			return err
		}
		return txLogic.pipelineDAO.Delete(gateCtx, txLogic.db, environmentKey, pipelineKey)
	})
}

// GetPipeline returns a pipeline by key.
func (s *Service) GetPipeline(ctx context.Context, environmentKey, pipelineKey string) (*plpb.Pipeline, error) {
	if pipelineKey == "" {
		return nil, ErrPipelineKeyRequired
	}
	if environmentKey == "" {
		return nil, errors.New("environment_key is required")
	}

	entity, err := s.logic.pipelineDAO.GetByKey(ctx, s.logic.db, environmentKey, pipelineKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPipelineNotFound
		}
		return nil, err
	}

	return modelPipelineToPB(entity), nil
}

// ListPipelines returns all pipelines with optional active filter.
func (s *Service) ListPipelines(ctx context.Context, environmentKey string, isActive *bool) ([]*plpb.Pipeline, error) {
	if environmentKey == "" {
		return nil, errors.New("environment_key is required")
	}

	entities, err := s.logic.pipelineDAO.List(ctx, s.logic.db, environmentKey, isActive, 0, 0)
	if err != nil {
		return nil, err
	}

	return pipelineSliceToPB(entities), nil
}

func modelPipelineToPB(pl *model.Pipeline) *plpb.Pipeline {
	if pl == nil {
		return nil
	}
	return &plpb.Pipeline{
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
