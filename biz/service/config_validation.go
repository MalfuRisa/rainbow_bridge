package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/pkg/util"
)

var (
	colorHexRegexp       = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
	numberContentRegexp  = regexp.MustCompile(`^-?[0-9]+$`)
	decimalContentRegexp = regexp.MustCompile(`^-?[0-9]+([.][0-9]+)?$`)
)

// --------------------- Config Validation & Normalization ---------------------

func normalizeConfigPayload(cfg *model.Config) {
	if cfg == nil {
		return
	}
	cfg.EnvironmentKey = strings.TrimSpace(cfg.EnvironmentKey)
	cfg.PipelineKey = strings.TrimSpace(cfg.PipelineKey)
	cfg.Alias = strings.TrimSpace(cfg.Alias)
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.Type = normalizeConfigType(cfg.Type)
	cfg.Content = strings.TrimSpace(cfg.Content)
	cfg.Remark = strings.TrimSpace(cfg.Remark)
	cfg.Description = strings.TrimSpace(cfg.Description)
}

func normalizeConfigType(t string) string {
	return util.NormalizeConfigType(t)
}

func (l *Logic) validateConfigContent(ctx context.Context, cfg *model.Config) error {
	if cfg == nil {
		return errors.New("config payload required")
	}

	// 前端传什么类型就存什么类型，不做自动类型转换

	switch cfg.Type {
	case "number":
		if !numberContentRegexp.MatchString(cfg.Content) {
			return errors.New("数字内容必须为整数")
		}
		return nil
	case "decimal":
		if !decimalContentRegexp.MatchString(cfg.Content) {
			return errors.New("小数内容必须为有效十进制数字")
		}
		return nil
	case "boolean":
		if cfg.Content != "true" && cfg.Content != "false" {
			return errors.New("布尔内容必须为 true 或 false")
		}
		return nil
	case "image":
		if cfg.Content == "" {
			return errors.New("图片类型内容不能为空")
		}
		// 支持多种路径格式
		contentLower := strings.ToLower(cfg.Content)
		if strings.HasPrefix(cfg.Content, "asset://") ||
			strings.HasPrefix(cfg.Content, "/api/v1/asset/file/") ||
			strings.Contains(cfg.Content, "/api/v1/asset/file/") || // 支持带路由前缀的路径
			strings.HasPrefix(contentLower, "http://") ||
			strings.HasPrefix(contentLower, "https://") {
			cfg.Content = l.resolveAssetReference(ctx, cfg.Content)
			return nil
		}
		return errors.New("图片类型内容必须为有效的引用地址")
	case "file":
		if cfg.Content == "" {
			return errors.New("文件类型内容不能为空")
		}
		// 支持多种路径格式（与 image 类型相同的处理逻辑）
		contentLower := strings.ToLower(cfg.Content)
		if strings.HasPrefix(cfg.Content, "asset://") ||
			strings.HasPrefix(cfg.Content, "/api/v1/asset/file/") ||
			strings.Contains(cfg.Content, "/api/v1/asset/file/") || // 支持带路由前缀的路径
			strings.HasPrefix(contentLower, "http://") ||
			strings.HasPrefix(contentLower, "https://") {
			cfg.Content = l.resolveAssetReference(ctx, cfg.Content)
			return nil
		}
		return errors.New("文件类型内容必须为有效的引用地址")
	case "text":
		if cfg.Content == "" {
			return errors.New("文案内容不能为空")
		}
		return nil
	case "textarea":
		if cfg.Content == "" {
			return errors.New("文本内容不能为空")
		}
		return nil
	case "richtext":
		if cfg.Content == "" {
			return errors.New("富文本内容不能为空")
		}
		return nil
	case "color":
		colorValue, err := normalizeColorContent(cfg.Content)
		if err != nil {
			return err
		}
		cfg.Content = colorValue
		return nil
	default:
		if cfg.Content == "" {
			return errors.New("配置内容不能为空")
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(cfg.Content), &payload); err != nil {
			return fmt.Errorf("配置内容需为 JSON 对象: %w", err)
		}
		if payload == nil {
			return errors.New("配置内容不能为空对象")
		}
		// 结构化内容（object/keyvalue）中嵌套的资源引用与 image/file 类型一样，
		// 在写入时解析为可访问路径；读取时再统一补上部署前缀。
		canonical, err := json.Marshal(l.resolveNestedAssetRefs(ctx, payload))
		if err != nil {
			return err
		}
		cfg.Content = string(canonical)
		return nil
	}
}

// resolveAssetReference 把资源引用简写 asset://{file_id} 解析为可访问的资源路径
// /api/v1/asset/file/{file_id}/{file_name}（不含部署前缀，前缀在读取时补齐）。
// 非资源引用（普通文本、已解析路径、绝对 URL）原样返回；
// 资源不存在时退化为不带文件名的路径，由服务端按 file_id 查找。
func (l *Logic) resolveAssetReference(ctx context.Context, value string) string {
	fileID, ok := strings.CutPrefix(value, assetScheme)
	if !ok {
		return value
	}
	if asset, err := l.assetDAO.GetByFileID(ctx, l.db, fileID); err == nil && asset != nil {
		return fmt.Sprintf("%s%s/%s", fileURLPrefix, asset.FileID, asset.FileName)
	}
	return fileURLPrefix + fileID
}

// resolveNestedAssetRefs 递归解析 JSON 结构中的资源引用，非字符串值原样保留。
func (l *Logic) resolveNestedAssetRefs(ctx context.Context, value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			v[key] = l.resolveNestedAssetRefs(ctx, item)
		}
		return v
	case []any:
		for i, item := range v {
			v[i] = l.resolveNestedAssetRefs(ctx, item)
		}
		return v
	case string:
		return l.resolveAssetReference(ctx, v)
	default:
		return value
	}
}

func normalizeColorContent(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("色彩标签内容不能为空")
	}
	matches := colorHexRegexp.FindStringSubmatch(trimmed)
	if matches == nil {
		return "", errors.New("色彩标签内容需为 #RGB 或 #RRGGBB 格式")
	}
	hex := matches[1]
	if len(hex) == 3 {
		hex = fmt.Sprintf("%c%c%c%c%c%c", hex[0], hex[0], hex[1], hex[1], hex[2], hex[2])
	}
	return "#" + strings.ToUpper(hex), nil
}
