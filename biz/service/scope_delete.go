package service

import (
	"context"
	"errors"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

// DeleteEnvironment removes the whole live environment generation after
// verifying every live pipeline is unlocked. History and files are retained.
func (s *Service) DeleteEnvironmentWithIdentity(ctx context.Context, environmentKey, expectedEnvironmentID string) (*model.ScopeDeleteResult, error) {
	if environmentKey == "" {
		return nil, ErrEnvironmentKeyRequired
	}
	identity, err := requireAdminIdentity(ctx)
	if err != nil {
		return nil, err
	}
	var (
		result *model.ScopeDeleteResult
		cached []deletedScopeCache
	)
	err = s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		env, err := resolveEnvironmentForWrite(gateCtx, txLogic, environmentKey, expectedEnvironmentID)
		if err != nil {
			return err
		}
		pipelines, err := txLogic.pipelineDAO.ListByEnvironmentID(gateCtx, txLogic.db,
			env.EnvironmentID, nil, 0, 0)
		if err != nil {
			return err
		}
		blocked := make([]FreezeBlockedScope, 0)
		for _, pipeline := range pipelines {
			state, err := txLogic.freezeDAO.GetStateByScopeID(gateCtx, txLogic.db, pipeline.ScopeID)
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return err
			}
			if state.State == model.FreezeStateLocked {
				blocked = append(blocked, FreezeBlockedScope{
					ScopeID:        pipeline.ScopeID,
					EnvironmentKey: pipeline.EnvironmentKey,
					PipelineKey:    pipeline.PipelineKey,
					State:          state.State,
					Revision:       state.Revision,
				})
			}
		}
		if len(blocked) > 0 {
			return scopeFrozenError(blocked)
		}

		now := time.Now().UTC()
		total := model.ScopeDeleteResult{EnvironmentID: env.EnvironmentID, DeletedAt: now}
		for _, pipeline := range pipelines {
			cached = append(cached, deletedScopeCache{
				scopeID: pipeline.ScopeID, environmentKey: pipeline.EnvironmentKey,
				pipelineKey: pipeline.PipelineKey,
			})
			counts, err := purgeScopeRows(gateCtx, txLogic, pipeline, identity.Username, now)
			if err != nil {
				return err
			}
			total.Pipelines++
			total.Configs += counts.Configs
			total.Assets += counts.Assets
			total.Versions += counts.Versions
		}
		if err := txLogic.environmentDAO.SoftDeleteActive(gateCtx, txLogic.db, env.EnvironmentID, now); err != nil {
			return err
		}
		event := &model.ScopeLifecycleEvent{
			EventType:         model.ScopeEventEnvironmentDelete,
			EnvironmentID:     env.EnvironmentID,
			EnvironmentKey:    env.EnvironmentKey,
			Actor:             identity.Username,
			Reason:            "environment deleted",
			AffectedPipelines: total.Pipelines,
			AffectedConfigs:   total.Configs,
			AffectedAssets:    total.Assets,
			AffectedVersions:  total.Versions,
			CreatedAt:         now,
		}
		if err := txLogic.db.WithContext(gateCtx).Create(event).Error; err != nil {
			return err
		}
		result = &total
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, scope := range cached {
		s.logic.invalidateScopeCaches(context.WithoutCancel(ctx), scope.scopeID,
			scope.environmentKey, scope.pipelineKey)
	}
	return result, nil
}

type deletedScopeCache struct {
	scopeID, environmentKey, pipelineKey string
}

// DeletePipeline removes one live scope after checking only its own freeze state.
func (s *Service) DeletePipelineWithIdentity(ctx context.Context, environmentKey, pipelineKey, expectedScopeID string) (*model.ScopeDeleteResult, error) {
	if pipelineKey == "" {
		return nil, ErrPipelineKeyRequired
	}
	if environmentKey == "" {
		return nil, errors.New("environment_key is required")
	}
	identity, err := requireAdminIdentity(ctx)
	if err != nil {
		return nil, err
	}
	var result *model.ScopeDeleteResult
	err = s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		pipeline, err := resolveScopeForWrite(gateCtx, txLogic, environmentKey, pipelineKey, expectedScopeID)
		if err != nil {
			return err
		}
		state, err := txLogic.freezeDAO.GetStateByScopeID(gateCtx, txLogic.db, pipeline.ScopeID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && state.State == model.FreezeStateLocked {
			return scopeFrozenError([]FreezeBlockedScope{{
				ScopeID:        pipeline.ScopeID,
				EnvironmentKey: pipeline.EnvironmentKey,
				PipelineKey:    pipeline.PipelineKey,
				State:          state.State,
				Revision:       state.Revision,
			}})
		}
		now := time.Now().UTC()
		counts, err := purgeScopeRows(gateCtx, txLogic, *pipeline, identity.Username, now)
		if err != nil {
			return err
		}
		event := &model.ScopeLifecycleEvent{
			EventType:        model.ScopeEventPipelineDelete,
			EnvironmentID:    pipeline.EnvironmentID,
			ScopeID:          pipeline.ScopeID,
			EnvironmentKey:   pipeline.EnvironmentKey,
			PipelineKey:      pipeline.PipelineKey,
			Actor:            identity.Username,
			Reason:           "pipeline deleted",
			AffectedConfigs:  counts.Configs,
			AffectedAssets:   counts.Assets,
			AffectedVersions: counts.Versions,
			CreatedAt:        now,
		}
		if err := txLogic.db.WithContext(gateCtx).Create(event).Error; err != nil {
			return err
		}
		result = &model.ScopeDeleteResult{
			EnvironmentID: pipeline.EnvironmentID,
			ScopeID:       pipeline.ScopeID,
			DeletedAt:     now,
			Pipelines:     1,
			Configs:       counts.Configs,
			Assets:        counts.Assets,
			Versions:      counts.Versions,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result != nil {
		s.logic.invalidateScopeCaches(context.WithoutCancel(ctx), result.ScopeID,
			environmentKey, pipelineKey)
	}
	return result, nil
}

type scopeDeleteCounts struct {
	Configs  int
	Assets   int
	Versions int
}

func purgeScopeRows(ctx context.Context, l *Logic, pipeline model.Pipeline, actor string, now time.Time) (scopeDeleteCounts, error) {
	configs, err := l.configDAO.CountByScope(ctx, l.db, pipeline.ScopeID)
	if err != nil {
		return scopeDeleteCounts{}, err
	}
	assets, err := l.assetDAO.CountByScope(ctx, l.db, pipeline.ScopeID)
	if err != nil {
		return scopeDeleteCounts{}, err
	}
	versions, err := l.releaseDAO.CountVersionsByScopeID(ctx, l.db, pipeline.ScopeID)
	if err != nil {
		return scopeDeleteCounts{}, err
	}
	if _, err := l.configDAO.SoftDeleteByScope(ctx, l.db, pipeline.ScopeID, now); err != nil {
		return scopeDeleteCounts{}, err
	}
	if _, err := l.assetDAO.SoftDeleteByScope(ctx, l.db, pipeline.ScopeID, now); err != nil {
		return scopeDeleteCounts{}, err
	}
	if _, err := l.releaseDAO.SoftDeleteVersionsByScope(ctx, l.db, pipeline.ScopeID,
		actor, "scope_deleted", now); err != nil {
		return scopeDeleteCounts{}, err
	}
	if err := l.releaseDAO.ClearHeadByScope(ctx, l.db, pipeline.ScopeID, actor, "scope_deleted", now); err != nil {
		return scopeDeleteCounts{}, err
	}
	if err := l.pipelineDAO.SoftDeleteScope(ctx, l.db, pipeline.ScopeID, now); err != nil {
		return scopeDeleteCounts{}, err
	}
	return scopeDeleteCounts{Configs: int(configs), Assets: int(assets), Versions: int(versions)}, nil
}
