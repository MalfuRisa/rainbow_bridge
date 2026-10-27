package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	"gorm.io/gorm"
)

// ImportConfigs preserves the existing error-only entry point.
// 仅用于测试与兼容调用;生产写入口经 Service.ImportConfigsWithResult 的写协调与冻结检查。
func (l *Logic) ImportConfigs(ctx context.Context, configs []model.Config, overwrite bool) error {
	written, err := l.importConfigs(ctx, configs, overwrite)
	if err != nil {
		return err
	}
	l.invalidateConfigCaches(context.WithoutCancel(ctx), written)
	return nil
}

// ImportConfigsWithResult returns only committed writes, in input order.
// 整个导入在写协调事务内执行:先对全部目标范围做冻结检查,任一 locked 则整请求在写前拒绝
// (包括 overwrite=false),不把冻结项静默当冲突跳过。
func (s *Service) ImportConfigsWithResult(ctx context.Context, configs []*common.ResourceConfig, overwrite bool) ([]*common.ResourceConfig, error) {
	return s.ImportConfigsWithIdentity(ctx, configs, overwrite, "", "")
}

// ImportConfigsWithIdentity binds a JSON import to the selected generation.
func (s *Service) ImportConfigsWithIdentity(ctx context.Context, configs []*common.ResourceConfig,
	overwrite bool, expectedEnvironmentID, expectedScopeID string) ([]*common.ResourceConfig, error) {
	models := make([]model.Config, 0, len(configs))
	scopes := make([]FreezeScopeRef, 0, len(configs))
	for i, cfg := range configs {
		if cfg == nil {
			return nil, fmt.Errorf("config %d: payload required", i+1)
		}
		normalized := pbConfigToModel(cfg)
		s.normalizeStoredConfig(normalized)
		models = append(models, *normalized)
		scopes = append(scopes, FreezeScopeRef{
			EnvironmentKey: normalized.EnvironmentKey,
			PipelineKey:    normalized.PipelineKey,
		})
	}
	var written []model.Config
	err := s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		if expectedEnvironmentID != "" || expectedScopeID != "" {
			seen := map[string]bool{}
			for _, cfg := range models {
				key := cfg.EnvironmentKey + "|" + cfg.PipelineKey
				if seen[key] {
					continue
				}
				seen[key] = true
				if expectedEnvironmentID != "" {
					if _, err := requireEnvironmentIdentity(gateCtx, txLogic, cfg.EnvironmentKey, expectedEnvironmentID); err != nil {
						return err
					}
				}
				if expectedScopeID != "" {
					if _, err := requireScopeIdentity(gateCtx, txLogic, cfg.EnvironmentKey, cfg.PipelineKey, expectedScopeID); err != nil {
						return err
					}
				}
			}
		}
		if err := txLogic.ensureScopesUnlocked(gateCtx, scopes); err != nil {
			return err
		}
		result, err := txLogic.importConfigs(gateCtx, models, overwrite)
		if err != nil {
			return err
		}
		written = result
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logic.invalidateConfigCaches(context.WithoutCancel(ctx), written)
	return s.decorateConfigList(configSliceToPB(written)), nil
}

type configImportIdentity struct{ environment, pipeline, value string }

func (l *Logic) prepareConfigImport(ctx context.Context, configs []model.Config) ([]model.Config, error) {
	if len(configs) == 0 {
		return nil, errors.New("configs cannot be empty")
	}
	plan := make([]model.Config, 0, len(configs))
	aliases := make(map[configImportIdentity]bool)
	keys := make(map[configImportIdentity]bool)
	for i, cfg := range configs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		normalizeConfigPayload(&cfg)
		if cfg.EnvironmentKey == "" || cfg.PipelineKey == "" {
			return nil, fmt.Errorf("config %d: environment_key and pipeline_key required", i+1)
		}
		if cfg.ResourceKey == "" && cfg.Alias == "" {
			return nil, fmt.Errorf("config %d: resource_key or alias required", i+1)
		}
		if err := l.validateConfigContent(ctx, &cfg); err != nil {
			return nil, fmt.Errorf("config %d: %w", i+1, err)
		}
		alias := configImportIdentity{cfg.EnvironmentKey, cfg.PipelineKey, cfg.Alias}
		key := configImportIdentity{cfg.EnvironmentKey, cfg.PipelineKey, cfg.ResourceKey}
		if (cfg.Alias != "" && aliases[alias]) || (cfg.ResourceKey != "" && keys[key]) {
			continue
		}
		if cfg.Alias != "" {
			aliases[alias] = true
		}
		if cfg.ResourceKey != "" {
			keys[key] = true
		}
		// Incoming persistence metadata never identifies a target row.
		cfg.ID = 0
		cfg.ScopeID = ""
		cfg.CreatedAt = time.Time{}
		cfg.UpdatedAt = time.Time{}
		cfg.DeletedAt = gorm.DeletedAt{}
		plan = append(plan, cfg)
	}
	return plan, nil
}

func (l *Logic) importConfigs(ctx context.Context, configs []model.Config, overwrite bool) ([]model.Config, error) {
	plan, err := l.prepareConfigImport(ctx, configs)
	if err != nil {
		return nil, err
	}
	written := make([]model.Config, 0, len(plan))
	err = l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, cfg := range plan {
			if err := ctx.Err(); err != nil {
				return err
			}
			target, err := l.findImportTarget(ctx, tx, &cfg)
			if err != nil {
				return err
			}
			if target != nil {
				if !overwrite {
					continue
				}
				cfg.ResourceKey = target.ResourceKey
				cfg.Alias = target.Alias
				if err := l.configDAO.OverwriteByEnvironmentAndPipeline(ctx, tx, cfg.EnvironmentKey, cfg.PipelineKey, &cfg); err != nil {
					return err
				}
				// Return persisted values, including fields whose zero values are ignored.
				saved, err := l.configDAO.GetByResourceKey(ctx, tx, cfg.EnvironmentKey, cfg.PipelineKey, cfg.ResourceKey)
				if err != nil {
					return err
				}
				cfg = *saved
			} else if err := l.configDAO.Create(ctx, tx, &cfg); err != nil {
				return err
			}
			written = append(written, cfg)
		}
		return ctx.Err()
	})
	if err != nil {
		return nil, err
	}
	return written, nil
}

func (l *Logic) findImportTarget(ctx context.Context, tx *gorm.DB, cfg *model.Config) (*model.Config, error) {
	var byKey, byAlias *model.Config
	var err error
	if cfg.ResourceKey != "" {
		byKey, err = l.configDAO.GetByResourceKey(ctx, tx, cfg.EnvironmentKey, cfg.PipelineKey, cfg.ResourceKey)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	if cfg.Alias != "" {
		byAlias, err = l.configDAO.GetByAlias(ctx, tx, cfg.EnvironmentKey, cfg.PipelineKey, cfg.Alias)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	if byKey != nil {
		if (cfg.Alias != "" && byKey.Alias != cfg.Alias) || (byAlias != nil && byAlias.ID != byKey.ID) {
			return nil, fmt.Errorf("ambiguous config identity in %s/%s: resource_key=%q alias=%q", cfg.EnvironmentKey, cfg.PipelineKey, cfg.ResourceKey, cfg.Alias)
		}
		return byKey, nil
	}
	return byAlias, nil
}
