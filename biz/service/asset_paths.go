package service

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"net/url"
	"strings"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

// Only remove complete current deployment prefixes immediately before the
// managed route. External URLs and unrelated text remain untouched.
func storageAssetPath(value, basePath string) string {
	candidate := value
	if basePath != "" {
		for strings.HasPrefix(candidate, basePath+"/") {
			candidate = strings.TrimPrefix(candidate, basePath)
		}
	}
	if strings.HasPrefix(candidate, fileURLPrefix) {
		return candidate
	}
	return value
}

func (s *Service) normalizeStoredConfig(cfg *model.Config) {
	typ := normalizeConfigTypeString(cfg.Type)
	switch typ {
	case "image", "file":
		cfg.Content = storageAssetPath(strings.TrimSpace(cfg.Content), s.basePath)
	case "object", "keyvalue":
		var value any
		if DecodeConfigJSON([]byte(cfg.Content), &value) == nil {
			value = mapAssetStrings(value, func(v string) string { return storageAssetPath(v, s.basePath) })
			if encoded, err := jsonContent(value); err == nil {
				cfg.Content = encoded
			}
		}
	}
}

func mapAssetStrings(value any, convert func(string) string) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			v[key] = mapAssetStrings(item, convert)
		}
	case []any:
		for i, item := range v {
			v[i] = mapAssetStrings(item, convert)
		}
	case string:
		return convert(v)
	}
	return value
}

func (s *Service) MigrateToFullAssetPaths(ctx context.Context) error {
	var configs []model.Config
	if err := s.logic.db.WithContext(ctx).Where("type = ?", "image").Find(&configs).Error; err != nil {
		return err
	}
	// 启动内容迁移必须尊重持久冻结状态:locked 范围的配置跳过并记录,不能借初始化改写内容。
	lockedScopes, err := s.lockedScopeSet(ctx)
	if err != nil {
		return err
	}
	skippedLocked := 0
	for _, cfg := range configs {
		if lockedScopes[cfg.ScopeID] {
			skippedLocked++
			fmt.Printf("asset migration: scope %s/%s locked; skipped resource_key=%s\n", cfg.EnvironmentKey, cfg.PipelineKey, cfg.ResourceKey)
			continue
		}
		normalized := storageAssetPath(cfg.Content, s.basePath)
		if !strings.HasPrefix(normalized, fileURLPrefix) {
			if strings.Contains(normalized, fileURLPrefix) && !strings.HasPrefix(normalized, "http://") && !strings.HasPrefix(normalized, "https://") {
				fmt.Printf("asset migration: resource_key=%s unrecognized prefix; preserved\n", cfg.ResourceKey)
			}
			continue
		}
		parsed, err := url.Parse(normalized)
		if err != nil {
			continue
		}
		id := strings.Split(strings.TrimPrefix(parsed.Path, fileURLPrefix), "/")[0]
		asset, err := s.logic.GetAsset(ctx, id)
		if err != nil {
			fmt.Printf("asset migration: resource_key=%s file_id=%s unresolved: %v\n", cfg.ResourceKey, id, err)
		} else {
			parsed.Path = fileURLPrefix + asset.FileID
			if asset.FileName != "" {
				parsed.Path += "/" + asset.FileName
			}
			parsed.RawPath = ""
			normalized = parsed.String()
		}
		if normalized == cfg.Content {
			continue
		}
		if err := s.logic.db.WithContext(ctx).Model(&cfg).Updates(map[string]any{"content": normalized, "edit_revision": uuid.NewString()}).Error; err != nil {
			return err
		}
		s.logic.invalidateConfigCaches(ctx, []model.Config{cfg})
	}
	if skippedLocked > 0 {
		fmt.Printf("asset migration: %d locked configs skipped\n", skippedLocked)
	}
	return nil
}
