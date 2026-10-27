package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	configdb "github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/pkg/common"
	"github.com/yi-nology/rainbow_bridge/pkg/redis"

	"gorm.io/gorm"
)

// --------------------- Config Operations ---------------------

func (l *Logic) AddConfig(ctx context.Context, cfg *model.Config) error {
	if cfg == nil {
		return nil
	}
	normalizeConfigPayload(cfg)
	if err := l.validateConfigContent(ctx, cfg); err != nil {
		return err
	}
	if cfg.ScopeID == "" {
		scopeID, err := configdb.ActiveScopeID(ctx, l.db, cfg.EnvironmentKey, cfg.PipelineKey)
		if err != nil {
			return err
		}
		cfg.ScopeID = scopeID
	}
	if cfg.Alias != "" {
		existing, err := l.configDAO.GetByAliasInScope(ctx, l.db, cfg.ScopeID, cfg.Alias)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if existing != nil {
			return ErrConfigAliasExists
		}
	}
	return l.configDAO.Create(ctx, l.db, cfg)
}

func (l *Logic) UpdateConfig(ctx context.Context, cfg *model.Config, expected string) error {
	if !configdb.ValidEditRevision(expected) {
		return configdb.ErrEditRevisionInvalid
	}
	if cfg == nil {
		return errors.New("config payload required")
	}
	if err := l.checkEditBaseline(ctx, cfg, expected); err != nil {
		return err
	}
	return l.writeConfig(ctx, cfg, expected)
}

func (l *Logic) checkEditBaseline(ctx context.Context, cfg *model.Config, expected string) error {
	scopeID := cfg.ScopeID
	if scopeID == "" {
		resolved, err := configdb.ActiveScopeID(ctx, l.db, cfg.EnvironmentKey, cfg.PipelineKey)
		if err != nil {
			return err
		}
		scopeID = resolved
		cfg.ScopeID = scopeID
	}
	current, err := l.configDAO.GetByScopeAndResourceKey(ctx, l.db, scopeID, cfg.ResourceKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return configdb.ErrEditTargetNotFound
	}
	if err != nil {
		return err
	}
	if current.EditRevision != expected {
		return configdb.ErrEditConflict
	}
	return nil
}

// overwriteConfig is reserved for explicit migration writes.
func (l *Logic) overwriteConfig(ctx context.Context, cfg *model.Config) error {
	return l.writeConfig(ctx, cfg, "")
}

func (l *Logic) writeConfig(ctx context.Context, cfg *model.Config, expected string) error {
	normalizeConfigPayload(cfg)
	if err := l.validateConfigContent(ctx, cfg); err != nil {
		return err
	}
	if expected != "" {
		return l.configDAO.UpdateIfRevision(ctx, l.db, cfg, expected)
	}
	return l.configDAO.OverwriteByEnvironmentAndPipeline(ctx, l.db, cfg.EnvironmentKey, cfg.PipelineKey, cfg)
}

func (l *Logic) DeleteConfig(ctx context.Context, environmentKey, pipelineKey, resourceKey string) error {
	scopeID, err := configdb.ActiveScopeID(ctx, l.db, environmentKey, pipelineKey)
	if err != nil {
		return err
	}
	if _, err := l.configDAO.GetByScopeAndResourceKey(ctx, l.db, scopeID, resourceKey); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrResourceNotFound
		}
		return err
	}
	return l.configDAO.DeleteByScopeAndResourceKey(ctx, l.db, scopeID, resourceKey)
}

func (l *Logic) GetConfig(ctx context.Context, environmentKey, pipelineKey, resourceKey string) (*model.Config, error) {
	scopeID, err := configdb.ActiveScopeID(ctx, l.db, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	cacheKey := redis.GenerateScopedConfigKey(scopeID, resourceKey)
	var cachedConfig model.Config
	found, err := redis.Get(ctx, l.redisClient, cacheKey, &cachedConfig)
	if err == nil && found && configdb.ValidEditRevision(cachedConfig.EditRevision) {
		return &cachedConfig, nil
	}
	cfg, err := l.configDAO.GetByScopeAndResourceKey(ctx, l.db, scopeID, resourceKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrResourceNotFound
	}
	if err != nil {
		return nil, err
	}
	cfg.ScopeID = scopeID
	if err := redis.Set(ctx, l.redisClient, cacheKey, cfg, time.Hour); err != nil {
		fmt.Printf("Failed to cache config: %v\n", err)
	}
	return cfg, nil
}

// GetConfigByKey is the legacy unscoped helper; new callers must resolve a scope.
func (l *Logic) GetConfigByKey(ctx context.Context, resourceKey string) (*model.Config, error) {
	cacheKey := fmt.Sprintf("rainbow_bridge:config:key:%s", resourceKey)
	var cachedConfig model.Config
	found, err := redis.Get(ctx, l.redisClient, cacheKey, &cachedConfig)
	if err == nil && found && configdb.ValidEditRevision(cachedConfig.EditRevision) {
		return &cachedConfig, nil
	}
	cfg, err := l.configDAO.GetByResourceKeyOnly(ctx, l.db, resourceKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrResourceNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := redis.Set(ctx, l.redisClient, cacheKey, cfg, time.Hour); err != nil {
		fmt.Printf("Failed to cache config by key: %v\n", err)
	}
	return cfg, nil
}

func (l *Logic) ListConfigs(ctx context.Context, environmentKey, pipelineKey, minVersion, maxVersion, resourceType string, latestOnly bool) ([]model.Config, error) {
	scopeID, err := configdb.ActiveScopeID(ctx, l.db, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	cacheKey := fmt.Sprintf("%s:%s:%s:%s:%t", redis.GenerateScopedConfigListPrefix(scopeID),
		minVersion, maxVersion, resourceType, latestOnly)
	var cachedConfigs []model.Config
	found, err := redis.Get(ctx, l.redisClient, cacheKey, &cachedConfigs)
	if err == nil && found && validConfigRevisions(cachedConfigs) {
		return cachedConfigs, nil
	}
	var configs []model.Config
	if latestOnly {
		configs, err = l.configDAO.ListLatestByScope(ctx, l.db, scopeID)
	} else {
		configs, err = l.configDAO.ListByScopeWithFilter(ctx, l.db, scopeID, resourceType, 0, 0)
	}
	if err != nil {
		return nil, err
	}
	for i := range configs {
		configs[i].ScopeID = scopeID
	}
	if err := redis.Set(ctx, l.redisClient, cacheKey, configs, 30*time.Minute); err != nil {
		fmt.Printf("Failed to cache config list: %v\n", err)
	}
	return configs, nil
}

func (l *Logic) ListConfigsAsMap(ctx context.Context, environmentKey, pipelineKey string) (map[string]any, error) {
	scopeID, err := configdb.ActiveScopeID(ctx, l.db, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	cacheKey := redis.GenerateScopedConfigMapKey(scopeID)
	var cachedMap map[string]any
	found, err := redis.Get(ctx, l.redisClient, cacheKey, &cachedMap)
	if err == nil && found {
		return cachedMap, nil
	}
	data, err := l.configDAO.ListByScopeWithFilter(ctx, l.db, scopeID, "", 0, 0)
	if err != nil {
		return nil, err
	}
	userID, ok := common.GetUserID(ctx)
	if !ok || userID == 0 {
		filtered := make([]model.Config, 0, len(data))
		for _, res := range data {
			if res.IsPerm {
				continue
			}
			filtered = append(filtered, res)
		}
		data = filtered
	}
	result := make(map[string]any, len(data))
	for _, res := range data {
		result[res.Alias] = res.Content
	}
	if err := redis.Set(ctx, l.redisClient, cacheKey, result, time.Hour); err != nil {
		fmt.Printf("Failed to cache config map: %v\n", err)
	}
	return result, nil
}

func (l *Logic) ExportConfigs(ctx context.Context, environmentKey, pipelineKey string) ([]model.Config, error) {
	scopeID, err := configdb.ActiveScopeID(ctx, l.db, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	data, err := l.configDAO.ListByScopeWithFilter(ctx, l.db, scopeID, "", 0, 0)
	if err != nil {
		return nil, err
	}
	for i := range data {
		data[i].ResourceKey = ""
		data[i].ScopeID = scopeID
	}
	return data, nil
}

func (l *Logic) ListBusinessKeys(ctx context.Context) ([]string, error) {
	return []string{}, errors.New("business_key is deprecated")
}

func validConfigRevisions(configs []model.Config) bool {
	for _, cfg := range configs {
		if !configdb.ValidEditRevision(cfg.EditRevision) {
			return false
		}
	}
	return true
}