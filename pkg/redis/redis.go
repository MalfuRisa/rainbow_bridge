package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/yi-nology/rainbow_bridge/pkg/config"
)

// NewClient creates a Redis client based on the provided configuration.
// Returns nil, nil if Redis is not enabled.
func NewClient(cfg config.RedisConfig) (*redis.Client, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	addr := cfg.Address
	if addr == "" {
		addr = "localhost:6379"
	}

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	return client, nil
}

// Set sets a key-value pair in Redis with expiration
func Set(ctx context.Context, client *redis.Client, key string, value interface{}, expiration time.Duration) error {
	if client == nil {
		return nil
	}

	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal value: %w", err)
	}

	return client.Set(ctx, key, data, expiration).Err()
}

// Get gets a value from Redis and unmarshals it into the provided pointer
func Get(ctx context.Context, client *redis.Client, key string, dest interface{}) (bool, error) {
	if client == nil {
		return false, nil
	}

	data, err := client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get key: %w", err)
	}

	if err := json.Unmarshal(data, dest); err != nil {
		return false, fmt.Errorf("unmarshal value: %w", err)
	}

	return true, nil
}

// Delete deletes a key from Redis
func Delete(ctx context.Context, client *redis.Client, key string) error {
	if client == nil {
		return nil
	}

	return client.Del(ctx, key).Err()
}

// DeleteByPattern deletes keys matching a pattern
func DeleteByPattern(ctx context.Context, client *redis.Client, pattern string) error {
	if client == nil {
		return nil
	}

	keys, err := client.Keys(ctx, pattern).Result()
	if err != nil {
		return fmt.Errorf("get keys: %w", err)
	}

	if len(keys) > 0 {
		return client.Del(ctx, keys...).Err()
	}

	return nil
}

// DeleteByPatternScan bounds each scan and delete batch. Callers supply a
// deadline and an escaped, scope-specific pattern; this never uses KEYS.
func DeleteByPatternScan(ctx context.Context, client *redis.Client, pattern string) error {
	if client == nil {
		return nil
	}
	var cursor uint64
	for {
		keys, next, err := client.Scan(ctx, cursor, pattern, 128).Result()
		if err != nil {
			return fmt.Errorf("scan cache keys: %w", err)
		}
		for start := 0; start < len(keys); start += 128 {
			end := start + 128
			if end > len(keys) {
				end = len(keys)
			}
			if err := client.Del(ctx, keys[start:end]...).Err(); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// Exists checks if a key exists in Redis
func Exists(ctx context.Context, client *redis.Client, key string) (bool, error) {
	if client == nil {
		return false, nil
	}

	result, err := client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("exists check: %w", err)
	}

	return result > 0, nil
}

// GenerateConfigKey generates a Redis key for config data
func GenerateConfigKey(environmentKey, pipelineKey, resourceKey string) string {
	return fmt.Sprintf("rainbow_bridge:config:%s:%s:%s", environmentKey, pipelineKey, resourceKey)
}

// GenerateScopedConfigKey isolates configurations by internal scope identity.
func GenerateScopedConfigKey(scopeID, resourceKey string) string {
	return fmt.Sprintf("rainbow_bridge:config:scope:%s:%s", scopeID, resourceKey)
}

// GenerateScopedConfigListPrefix is the list cache prefix for one scope.
func GenerateScopedConfigListPrefix(scopeID string) string {
	return fmt.Sprintf("rainbow_bridge:config:list:scope:%s", scopeID)
}

// GenerateScopedConfigMapKey isolates runtime config maps by scope.
func GenerateScopedConfigMapKey(scopeID string) string {
	return fmt.Sprintf("rainbow_bridge:config:map:scope:%s", scopeID)
}

// GenerateConfigListKey generates a Redis key for config list data
func GenerateConfigListKey(environmentKey, pipelineKey string) string {
	return fmt.Sprintf("rainbow_bridge:config:list:%s:%s", environmentKey, pipelineKey)
}

// GenerateConfigMapKey generates a Redis key for config map data
func GenerateConfigMapKey(environmentKey, pipelineKey string) string {
	return fmt.Sprintf("rainbow_bridge:config:map:%s:%s", environmentKey, pipelineKey)
}
