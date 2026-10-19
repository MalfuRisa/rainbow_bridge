package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/pkg/common"
	"github.com/yi-nology/rainbow_bridge/pkg/redis"

	"gorm.io/gorm"
)

// --------------------- Config Operations ---------------------
// 注意:本层只管数据库写入;缓存失效由调用方在写协调事务成功提交后进行。

func (l *Logic) AddConfig(ctx context.Context, cfg *model.Config) error {
	if cfg == nil {
		return nil
	}
	normalizeConfigPayload(cfg)
	if err := l.validateConfigContent(ctx, cfg); err != nil {
		return err
	}

	// Check if alias already exists in the same environment and pipeline
	if cfg.Alias != "" {
		existing, err := l.configDAO.GetByAlias(ctx, l.db, cfg.EnvironmentKey, cfg.PipelineKey, cfg.Alias)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if existing != nil {
			return ErrConfigAliasExists
		}
	}

	return l.configDAO.Create(ctx, l.db, cfg)
}

func (l *Logic) UpdateConfig(ctx context.Context, cfg *model.Config) error {
	if cfg == nil {
		return nil
	}
	normalizeConfigPayload(cfg)
	if err := l.validateConfigContent(ctx, cfg); err != nil {
		return err
	}
	if _, err := l.configDAO.GetByResourceKey(ctx, l.db, cfg.EnvironmentKey, cfg.PipelineKey, cfg.ResourceKey); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrResourceNotFound
		}
		return err
	}

	return l.configDAO.UpdateByEnvironmentAndPipeline(ctx, l.db, cfg.EnvironmentKey, cfg.PipelineKey, cfg)
}

func (l *Logic) DeleteConfig(ctx context.Context, environmentKey, pipelineKey, resourceKey string) error {
	_, err := l.configDAO.GetByResourceKey(ctx, l.db, environmentKey, pipelineKey, resourceKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrResourceNotFound
		}
		return err
	}

	return l.configDAO.DeleteByEnvironmentPipelineAndResourceKey(ctx, l.db, environmentKey, pipelineKey, resourceKey)
}

func (l *Logic) GetConfig(ctx context.Context, environmentKey, pipelineKey, resourceKey string) (*model.Config, error) {
	// 生成缓存键
	cacheKey := redis.GenerateConfigKey(environmentKey, pipelineKey, resourceKey)

	// 尝试从缓存中获取
	var cachedConfig model.Config
	found, err := redis.Get(ctx, l.redisClient, cacheKey, &cachedConfig)
	if err == nil && found {
		return &cachedConfig, nil
	}

	// 缓存未命中，从数据库获取
	cfg, err := l.configDAO.GetByResourceKey(ctx, l.db, environmentKey, pipelineKey, resourceKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrResourceNotFound
	}
	if err != nil {
		return nil, err
	}

	// 存入缓存，设置过期时间为1小时
	if err := redis.Set(ctx, l.redisClient, cacheKey, cfg, time.Hour); err != nil {
		// 缓存错误不影响主流程，只记录错误
		fmt.Printf("Failed to cache config: %v\n", err)
	}

	return cfg, nil
}

func (l *Logic) GetConfigByKey(ctx context.Context, resourceKey string) (*model.Config, error) {
	// 生成缓存键
	cacheKey := fmt.Sprintf("rainbow_bridge:config:key:%s", resourceKey)

	// 尝试从缓存中获取
	var cachedConfig model.Config
	found, err := redis.Get(ctx, l.redisClient, cacheKey, &cachedConfig)
	if err == nil && found {
		return &cachedConfig, nil
	}

	// 缓存未命中，从数据库获取
	cfg, err := l.configDAO.GetByResourceKeyOnly(ctx, l.db, resourceKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrResourceNotFound
	}
	if err != nil {
		return nil, err
	}

	// 存入缓存，设置过期时间为1小时
	if err := redis.Set(ctx, l.redisClient, cacheKey, cfg, time.Hour); err != nil {
		// 缓存错误不影响主流程，只记录错误
		fmt.Printf("Failed to cache config by key: %v\n", err)
	}

	return cfg, nil
}

func (l *Logic) ListConfigs(ctx context.Context, environmentKey, pipelineKey, minVersion, maxVersion, resourceType string, latestOnly bool) ([]model.Config, error) {
	// 生成缓存键
	cacheKey := fmt.Sprintf("rainbow_bridge:config:list:%s:%s:%s:%s:%s:%t", environmentKey, pipelineKey, minVersion, maxVersion, resourceType, latestOnly)

	// 尝试从缓存中获取
	var cachedConfigs []model.Config
	found, err := redis.Get(ctx, l.redisClient, cacheKey, &cachedConfigs)
	if err == nil && found {
		return cachedConfigs, nil
	}

	// 缓存未命中，从数据库获取
	var configs []model.Config
	if latestOnly {
		version := common.GetClientVersion(ctx)
		configs, err = l.configDAO.ListByEnvironmentAndPipeline(ctx, l.db, environmentKey, pipelineKey, version, 0, 0)
	} else {
		configs, err = l.configDAO.ListByEnvironmentAndPipelineWithFilter(ctx, l.db, environmentKey, pipelineKey, minVersion, maxVersion, resourceType, 0, 0)
	}
	if err != nil {
		return nil, err
	}

	// 存入缓存，设置过期时间为30分钟
	if err := redis.Set(ctx, l.redisClient, cacheKey, configs, 30*time.Minute); err != nil {
		// 缓存错误不影响主流程，只记录错误
		fmt.Printf("Failed to cache config list: %v\n", err)
	}

	return configs, nil
}

func (l *Logic) ListConfigsAsMap(ctx context.Context, environmentKey, pipelineKey string) (map[string]any, error) {
	// 生成缓存键
	cacheKey := redis.GenerateConfigMapKey(environmentKey, pipelineKey)

	// 尝试从缓存中获取
	var cachedMap map[string]any
	found, err := redis.Get(ctx, l.redisClient, cacheKey, &cachedMap)
	if err == nil && found {
		return cachedMap, nil
	}

	// 缓存未命中，从数据库获取
	data, err := l.configDAO.ListByEnvironmentAndPipelineWithFilter(ctx, l.db, environmentKey, pipelineKey, "", "", "", 0, 0)
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

	// 存入缓存，设置过期时间为1小时
	if err := redis.Set(ctx, l.redisClient, cacheKey, result, time.Hour); err != nil {
		// 缓存错误不影响主流程，只记录错误
		fmt.Printf("Failed to cache config map: %v\n", err)
	}

	return result, nil
}

func (l *Logic) ExportConfigs(ctx context.Context, environmentKey, pipelineKey string) ([]model.Config, error) {
	data, err := l.configDAO.ListByEnvironmentAndPipeline(ctx, l.db, environmentKey, pipelineKey, common.GetClientVersion(ctx), 0, 0)
	if err != nil {
		return nil, err
	}
	for i := range data {
		data[i].ResourceKey = ""
	}
	return data, nil
}

func (l *Logic) ListBusinessKeys(ctx context.Context) ([]string, error) {
	// Deprecated: business_key is replaced by environment_key + pipeline_key
	return []string{}, errors.New("business_key is deprecated")
}
