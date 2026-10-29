package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/pkg/redis"
)

var configCacheGlobEscaper = strings.NewReplacer("\\", "\\\\", "*", "\\*", "?", "\\?", "[", "\\[", "]", "\\]")

// Shared by ordinary config writes and committed imports. Cache errors cannot
// undo a successful database write.
func (l *Logic) invalidateConfigCaches(ctx context.Context, configs []model.Config) {
	if l.redisClient == nil || len(configs) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	scopes := make(map[[2]string]bool)
	identities := make(map[string]bool)
	for _, cfg := range configs {
		// Writers pass the identity captured inside their transaction. Never
		// resolve a missing ID by a business key after commit: it may be a new generation.
		if cfg.ScopeID == "" {
			fmt.Printf("Config write committed; cache invalidation missing scope identity resource_key=%s\n", cfg.ResourceKey)
		} else {
			identities[cfg.ScopeID] = true
			key := redis.GenerateScopedConfigKey(cfg.ScopeID, cfg.ResourceKey)
			if err := redis.Delete(ctx, l.redisClient, key); err != nil {
				fmt.Printf("Config write committed; cache invalidation failed key=%s: %v\n", key, err)
			}
		}
		keys := []string{redis.GenerateConfigKey(cfg.EnvironmentKey, cfg.PipelineKey, cfg.ResourceKey), fmt.Sprintf("rainbow_bridge:config:key:%s", cfg.ResourceKey)}
		for _, key := range keys {
			if err := redis.Delete(ctx, l.redisClient, key); err != nil {
				fmt.Printf("Config write committed; cache invalidation failed key=%s: %v\n", key, err)
			}
		}
		scopes[[2]string{cfg.EnvironmentKey, cfg.PipelineKey}] = true
	}
	for scopeID := range identities {
		pattern := configCacheGlobEscaper.Replace(redis.GenerateScopedConfigListPrefix(scopeID)) + ":*"
		if err := redis.DeleteByPatternScan(ctx, l.redisClient, pattern); err != nil {
			fmt.Printf("Config write committed; cache invalidation failed pattern=%s: %v\n", pattern, err)
		}
		key := redis.GenerateScopedConfigMapKey(scopeID)
		if err := redis.Delete(ctx, l.redisClient, key); err != nil {
			fmt.Printf("Config write committed; cache invalidation failed key=%s: %v\n", key, err)
		}
	}
	for scope := range scopes {
		pattern := fmt.Sprintf("rainbow_bridge:config:list:%s:%s:*", configCacheGlobEscaper.Replace(scope[0]), configCacheGlobEscaper.Replace(scope[1]))
		if err := redis.DeleteByPatternScan(ctx, l.redisClient, pattern); err != nil {
			fmt.Printf("Config write committed; cache invalidation failed pattern=%s: %v\n", pattern, err)
		}
		key := redis.GenerateConfigMapKey(scope[0], scope[1])
		if err := redis.Delete(ctx, l.redisClient, key); err != nil {
			fmt.Printf("Config write committed; cache invalidation failed key=%s: %v\n", key, err)
		}
	}
}

// invalidateScopeCaches drops every cached view of a deleted generation.
// Old key-based keys are removed too so a recreated same-key scope cannot be
// served a cached predecessor response.
func (l *Logic) invalidateScopeCaches(ctx context.Context, scopeID, environmentKey, pipelineKey string) {
	if l.redisClient == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	patterns := []string{
		redis.GenerateScopedConfigListPrefix(scopeID) + ":*",
		redis.GenerateScopedConfigKey(scopeID, "*"),
		configCacheGlobEscaper.Replace(redis.GenerateConfigListKey(environmentKey, pipelineKey)) + ":*",
		fmt.Sprintf("rainbow_bridge:config:%s:%s:*",
			configCacheGlobEscaper.Replace(environmentKey), configCacheGlobEscaper.Replace(pipelineKey)),
	}
	for _, pattern := range patterns {
		_ = redis.DeleteByPattern(ctx, l.redisClient, pattern)
	}
	_ = redis.Delete(ctx, l.redisClient, redis.GenerateScopedConfigMapKey(scopeID))
	_ = redis.Delete(ctx, l.redisClient, redis.GenerateConfigMapKey(environmentKey, pipelineKey))
}
