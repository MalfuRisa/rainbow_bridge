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
	for _, cfg := range configs {
		keys := []string{redis.GenerateConfigKey(cfg.EnvironmentKey, cfg.PipelineKey, cfg.ResourceKey), fmt.Sprintf("rainbow_bridge:config:key:%s", cfg.ResourceKey)}
		for _, key := range keys {
			if err := redis.Delete(ctx, l.redisClient, key); err != nil {
				fmt.Printf("Config write committed; cache invalidation failed key=%s: %v\n", key, err)
			}
		}
		scopes[[2]string{cfg.EnvironmentKey, cfg.PipelineKey}] = true
	}
	for scope := range scopes {
		pattern := fmt.Sprintf("rainbow_bridge:config:list:%s:%s:*", configCacheGlobEscaper.Replace(scope[0]), configCacheGlobEscaper.Replace(scope[1]))
		if err := redis.DeleteByPattern(ctx, l.redisClient, pattern); err != nil {
			fmt.Printf("Config write committed; cache invalidation failed pattern=%s: %v\n", pattern, err)
		}
		key := redis.GenerateConfigMapKey(scope[0], scope[1])
		if err := redis.Delete(ctx, l.redisClient, key); err != nil {
			fmt.Printf("Config write committed; cache invalidation failed key=%s: %v\n", key, err)
		}
	}
}
