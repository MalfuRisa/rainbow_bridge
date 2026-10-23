package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	"github.com/yi-nology/rainbow_bridge/pkg/config"
	"google.golang.org/protobuf/proto"

	"gorm.io/gorm"
)

// closeQuietly 安静地关闭资源,忽略错误
func closeQuietly(closer io.Closer) {
	if closer != nil {
		_ = closer.Close()
	}
}

const (
	dataDirectory   = "data"
	uploadDirectory = "uploads"
	assetScheme     = "asset://"
	fileURLPrefix   = "/api/v1/asset/file/"
)

var (
	assetRefRegexp   = regexp.MustCompile(`asset://([a-zA-Z0-9\-]+)`)
	fileURLRefRegexp = regexp.MustCompile(`/api/v1/asset/file/([a-zA-Z0-9\-]+)`)
)

// FileUploadInput captures metadata and payload for asset uploads.
type FileUploadInput struct {
	EnvironmentKey string
	PipelineKey    string
	Remark         string
	FileName       string
	ContentType    string
	Data           []byte
}

// Service orchestrates config and asset operations using Logic.
type Service struct {
	logic       *Logic
	basePath    string
	config      *config.Config
	redisClient *redis.Client
	auth        *AuthSettings
	clock       func() time.Time
	loginRate   *authRateLimiter
	passwordOps *authComputeLimiter
}

func NewService(db *gorm.DB, redisClient *redis.Client, basePath string, cfg *config.Config) *Service {
	return &Service{
		logic:       NewLogic(db, redisClient),
		basePath:    sanitizeServiceBasePath(basePath),
		config:      cfg,
		redisClient: redisClient,
		loginRate:   newAuthRateLimiter(nil),
		passwordOps: newAuthComputeLimiter(authPasswordConcurrent),
	}
}

// --------------------- Model conversion helpers ---------------------

func pbConfigToModel(cfg *common.ResourceConfig) *model.Config {
	if cfg == nil {
		return &model.Config{}
	}
	return &model.Config{
		ResourceKey:    cfg.GetResourceKey(),
		Alias:          cfg.GetAlias(),
		Name:           cfg.GetName(),
		EnvironmentKey: cfg.GetEnvironmentKey(),
		PipelineKey:    cfg.GetPipelineKey(),
		Content:        cfg.GetContent(),
		Type:           cfg.GetType(),
		Remark:         cfg.GetRemark(),
		IsPerm:         cfg.GetIsPerm(),
		Description:    cfg.GetDescription(),
	}
}

func modelConfigToPB(cfg *model.Config) *common.ResourceConfig {
	if cfg == nil {
		return nil
	}
	return &common.ResourceConfig{
		EditRevision:   cfg.EditRevision,
		ResourceKey:    cfg.ResourceKey,
		Alias:          cfg.Alias,
		Name:           cfg.Name,
		EnvironmentKey: cfg.EnvironmentKey,
		PipelineKey:    cfg.PipelineKey,
		Content:        cfg.Content,
		Type:           cfg.Type,
		Remark:         cfg.Remark,
		IsPerm:         cfg.IsPerm,
		Description:    cfg.Description,
	}
}

func configSliceToPB(configs []model.Config) []*common.ResourceConfig {
	list := make([]*common.ResourceConfig, 0, len(configs))
	for i := range configs {
		list = append(list, modelConfigToPB(&configs[i]))
	}
	return list
}

func assetModelToPB(asset *model.Asset) *common.FileAsset {
	if asset == nil {
		return nil
	}
	url := "/api/v1/asset/file/" + asset.FileID
	if asset.FileName != "" {
		url = "/api/v1/asset/file/" + asset.FileID + "/" + asset.FileName
	}
	return &common.FileAsset{
		FileId:         asset.FileID,
		EnvironmentKey: asset.EnvironmentKey,
		PipelineKey:    asset.PipelineKey,
		FileName:       asset.FileName,
		ContentType:    asset.ContentType,
		FileSize:       asset.FileSize,
		Url:            url,
		Remark:         asset.Remark,
	}
}

func assetSliceToPB(assets []model.Asset) []*common.FileAsset {
	list := make([]*common.FileAsset, 0, len(assets))
	for i := range assets {
		list = append(list, assetModelToPB(&assets[i]))
	}
	return list
}

// --------------------- Service helpers ---------------------

// MigrateToFullAssetPaths ensures all asset URLs and config contents use the full path with filename.
func sanitizeServiceBasePath(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || trimmed == "/" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	return strings.TrimSuffix(trimmed, "/")
}

func (s *Service) applyFileURLPrefix(value string) string {
	if s == nil || s.basePath == "" {
		return value
	}
	if strings.HasPrefix(value, fileURLPrefix) {
		return s.basePath + value
	}
	return value
}

func (s *Service) expandConfigContent(content string, typ string) string {
	if s == nil || s.basePath == "" || content == "" {
		return content
	}
	normalizedType := normalizeConfigTypeString(typ)
	// 只有 object 和 keyvalue 类型需要展开嵌套的文件URL
	if normalizedType != "object" && normalizedType != "keyvalue" {
		return s.applyFileURLPrefix(content)
	}
	var payload any
	if err := DecodeConfigJSON([]byte(content), &payload); err != nil {
		return s.applyFileURLPrefix(content)
	}
	expanded := s.expandNestedValue(payload)
	data, err := json.Marshal(expanded)
	if err != nil {
		return s.applyFileURLPrefix(content)
	}
	return string(data)
}

func (s *Service) expandNestedValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, val := range v {
			v[key] = s.expandNestedValue(val)
		}
		return v
	case []any:
		for i, val := range v {
			v[i] = s.expandNestedValue(val)
		}
		return v
	case string:
		return s.applyFileURLPrefix(v)
	default:
		return value
	}
}

func (s *Service) decorateConfig(cfg *common.ResourceConfig) *common.ResourceConfig {
	if cfg == nil || s == nil || s.basePath == "" {
		return cfg
	}
	cfg = proto.Clone(cfg).(*common.ResourceConfig)
	cfg.Type = normalizeConfigTypeString(cfg.GetType())
	cfg.Content = s.expandConfigContent(cfg.GetContent(), cfg.Type)
	return cfg
}

func (s *Service) decorateConfigList(list []*common.ResourceConfig) []*common.ResourceConfig {
	if s == nil || s.basePath == "" {
		return list
	}
	for i, cfg := range list {
		list[i] = s.decorateConfig(cfg)
	}
	return list
}

func (s *Service) decorateAsset(asset *common.FileAsset) *common.FileAsset {
	if asset == nil || s == nil || s.basePath == "" {
		return asset
	}
	asset.Url = s.applyFileURLPrefix(asset.GetUrl())
	return asset
}

func (s *Service) decorateAssetList(list []*common.FileAsset) []*common.FileAsset {
	if s == nil || s.basePath == "" {
		return list
	}
	for _, asset := range list {
		s.decorateAsset(asset)
	}
	return list
}

func ensureUploadDir(fileID string) error {
	relative := filepath.Join(uploadDirectory, fileID)
	return os.MkdirAll(filepath.Join(dataDirectory, relative), 0o755)
}

func detectContentType(provided string, data []byte) string {
	if provided != "" {
		return provided
	}
	return http.DetectContentType(data)
}

func (s *Service) generateFileURL(asset *model.Asset) string {
	if asset == nil {
		return ""
	}
	if asset.FileName != "" {
		return fmt.Sprintf("%s/api/v1/asset/file/%s/%s", s.basePath, asset.FileID, asset.FileName)
	}
	return fmt.Sprintf("%s/api/v1/asset/file/%s", s.basePath, asset.FileID)
}

func normalizeConfigTypeString(t string) string {
	normalized := strings.ToLower(strings.TrimSpace(t))
	switch normalized {
	// 直接支持的类型
	case "text", "textarea", "richtext":
		return normalized
	case "number", "decimal":
		return normalized
	case "boolean":
		return normalized
	case "keyvalue":
		return normalized
	case "object":
		return normalized
	case "color":
		return normalized
	case "file":
		return normalized
	case "image":
		return normalized

	// 兼容旧的中文别名
	case "string", "copy", "文案":
		return "text"
	case "colour", "color_tag", "color-tag", "色彩", "色彩标签":
		return "color"
	case "kv", "key-value", "键值对":
		return "keyvalue"
	case "json", "config":
		return "object"

	// 默认返回原值(直接透传)
	default:
		return t
	}
}
