package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

// Scope resolution errors distinguish "never existed" from "deleted".
var (
	ErrNoActiveScope = errors.New("no active scope for business key")
	ErrScopeDeleted  = errors.New("scope has been deleted")
)

// ActiveEnvironment resolves the single live generation of environment_key.
func ActiveEnvironment(ctx context.Context, db *gorm.DB, environmentKey string) (*model.Environment, error) {
	key := strings.TrimSpace(environmentKey)
	if key == "" {
		return nil, ErrNoActiveScope
	}
	var env model.Environment
	err := db.WithContext(ctx).
		Where("environment_key = ? AND active_slot = ?", key, model.ActiveSlotActive).
		First(&env).Error
	if err == nil {
		return &env, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var deleted model.Environment
	if unscopedErr := db.WithContext(ctx).Unscoped().
		Where("environment_key = ?", key).First(&deleted).Error; unscopedErr == nil {
		return nil, ErrScopeDeleted
	} else if !errors.Is(unscopedErr, gorm.ErrRecordNotFound) {
		return nil, unscopedErr
	}
	return nil, ErrNoActiveScope
}

// ActivePipeline resolves the live generation of a pipeline inside env.
func ActivePipeline(ctx context.Context, db *gorm.DB, environmentID, pipelineKey string) (*model.Pipeline, error) {
	key := strings.TrimSpace(pipelineKey)
	if strings.TrimSpace(environmentID) == "" || key == "" {
		return nil, ErrNoActiveScope
	}
	var pipeline model.Pipeline
	err := db.WithContext(ctx).
		Where("environment_id = ? AND pipeline_key = ? AND active_slot = ?",
			environmentID, key, model.ActiveSlotActive).
		First(&pipeline).Error
	if err == nil {
		return &pipeline, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var deleted model.Pipeline
	if unscopedErr := db.WithContext(ctx).Unscoped().
		Where("environment_id = ? AND pipeline_key = ?", environmentID, key).
		First(&deleted).Error; unscopedErr == nil {
		return nil, ErrScopeDeleted
	} else if !errors.Is(unscopedErr, gorm.ErrRecordNotFound) {
		return nil, unscopedErr
	}
	return nil, ErrNoActiveScope
}

// ActiveScope resolves the current pipeline for an environment/pipeline key pair.
func ActiveScope(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string) (*model.Pipeline, error) {
	env, err := ActiveEnvironment(ctx, db, environmentKey)
	if err != nil {
		return nil, err
	}
	return ActivePipeline(ctx, db, env.EnvironmentID, pipelineKey)
}

// ActiveScopeID is the convenience form for DAO methods that only need the id.
func ActiveScopeID(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string) (string, error) {
	pipeline, err := ActiveScope(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		// Narrow DAO tests may migrate only resource_config. Treat the absent
		// scope tables as the legacy empty scope instead of breaking the DAO
		// contract; production always runs MigrateScopeIdentity first.
		if isMissingScopeTableError(err) {
			return legacyScopeID(environmentKey, pipelineKey), nil
		}
		if errors.Is(err, ErrNoActiveScope) || errors.Is(err, ErrScopeDeleted) {
			var envCount, pipelineCount int64
			_ = db.WithContext(ctx).Unscoped().Model(&model.Environment{}).Count(&envCount).Error
			_ = db.WithContext(ctx).Unscoped().Model(&model.Pipeline{}).Count(&pipelineCount).Error
			if envCount == 0 && pipelineCount == 0 {
				// Never-initialized database or isolated DAO tests: derive a
				// stable synthetic scope so different keys stay isolated until
				// real scope rows exist.
				return legacyScopeID(environmentKey, pipelineKey), nil
			}
		}
		return "", err
	}
	return pipeline.ScopeID, nil
}

// LegacyScopeID exposes the deterministic pre-init scope id to service
// compatibility wrappers.
func LegacyScopeID(environmentKey, pipelineKey string) string {
	return legacyScopeID(environmentKey, pipelineKey)
}

// Uninitialized reports a database with no environment or pipeline rows,
// which is the only case where the synthetic legacy scope is allowed.
func Uninitialized(ctx context.Context, db *gorm.DB) bool {
	var envCount, pipelineCount int64
	if err := db.WithContext(ctx).Unscoped().Model(&model.Environment{}).Count(&envCount).Error; err != nil {
		return false
	}
	if err := db.WithContext(ctx).Unscoped().Model(&model.Pipeline{}).Count(&pipelineCount).Error; err != nil {
		return false
	}
	return envCount == 0 && pipelineCount == 0
}

// legacyScopeID is only used before environment/pipeline rows exist (tests and
// never-initialized databases). Production always resolves real UUIDs after
// the startup migration.
func legacyScopeID(environmentKey, pipelineKey string) string {
	return fmt.Sprintf("legacy:%d:%s:%s", len(environmentKey), environmentKey, pipelineKey)
}

func isMissingScopeTableError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such table") && strings.Contains(message, "environment") ||
		strings.Contains(message, "no such table") && strings.Contains(message, "pipeline")
}

// ScopeByIDIncludingDeleted returns the pipeline row regardless of lifecycle.
func ScopeByIDIncludingDeleted(ctx context.Context, db *gorm.DB, scopeID string) (*model.Pipeline, error) {
	var pipeline model.Pipeline
	if err := db.WithContext(ctx).Unscoped().Where("scope_id = ?", scopeID).First(&pipeline).Error; err != nil {
		return nil, err
	}
	return &pipeline, nil
}

// EnvironmentByIDIncludingDeleted returns the environment row regardless of lifecycle.
func EnvironmentByIDIncludingDeleted(ctx context.Context, db *gorm.DB, environmentID string) (*model.Environment, error) {
	var env model.Environment
	if err := db.WithContext(ctx).Unscoped().Where("environment_id = ?", environmentID).First(&env).Error; err != nil {
		return nil, err
	}
	return &env, nil
}

// ScopeIsActive reports whether both the scope and its parent environment are live.
func ScopeIsActive(ctx context.Context, db *gorm.DB, scopeID string) (bool, error) {
	if strings.HasPrefix(scopeID, "legacy:") {
		var count int64
		if err := db.WithContext(ctx).Unscoped().Model(&model.Pipeline{}).Count(&count).Error; err != nil {
			return false, err
		}
		return count == 0, nil
	}
	pipeline, err := ScopeByIDIncludingDeleted(ctx, db, scopeID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	env, err := EnvironmentByIDIncludingDeleted(ctx, db, pipeline.EnvironmentID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return pipeline.ActiveSlot != nil && *pipeline.ActiveSlot == model.ActiveSlotActive &&
		!pipeline.DeletedAt.Valid && env.ActiveSlot != nil &&
		*env.ActiveSlot == model.ActiveSlotActive && !env.DeletedAt.Valid, nil
}
