package service

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

// 版本静态包:配置与资源的数据包(不作为业务网页、旧导入格式或备份恢复包)。
const (
	releaseStaticFormatName    = "rainbow-release-static-v1"
	releaseStaticConfigName    = "config.json"
	releaseStaticManifestName  = "manifest.json"
	releaseStaticAssetsDirName = "assets"
	releaseStaticSelectionMode = "fixed"
	releaseExportTempPrefix    = "rainbow-release-static-"
)

var (
	// 未压缩输出总量上限(两个 JSON 文件 + 全部资源)。超出即 413,不影响该版本在线读取。
	releaseExportMaxOutputByte int64 = 192 << 20
	// 整次构包期限;var 便于测试收紧,生产不可由外部配置。
	releaseExportTotalTimeout = 60 * time.Second
	// releaseExportPauseHookForTest 仅测试使用:元数据已固定、写包之前调用,用于构造构包途中切换生效版本的时序。默认 nil。
	releaseExportPauseHookForTest func(snapshotID string)
)

// ReleaseStaticExportInput 固定导出页面已展示的版本快照;不重新读取 active。
type ReleaseStaticExportInput struct {
	ExpectedScopeID    string
	EnvironmentKey     string
	PipelineKey        string
	Version            string
	ExpectedSnapshotID string
}

// ReleaseStaticExportResult 是已生成的临时 ZIP;调用方负责删除 TempPath。
type ReleaseStaticExportResult struct {
	SnapshotID string
	Version    string
	FileName   string
	TempPath   string
	Size       int64
}

// releaseStaticExportPlan 是一次导出已固定的元数据与资源清单。
type releaseStaticExportPlan struct {
	versionRow *model.ConfigReleaseVersion
	assets     []model.ConfigReleaseAsset
	configJSON []byte
	manifest   []byte
}

// ReleaseStaticExport 生成版本静态包:先固定范围与版本行并校验摘要,再逐份读取资源、
// 边校验边写入临时 ZIP;任一步失败都删除临时文件,不产生半成品。
func (s *Service) ReleaseStaticExport(ctx context.Context, input ReleaseStaticExportInput) (*ReleaseStaticExportResult, error) {
	environmentKey := strings.TrimSpace(input.EnvironmentKey)
	pipelineKey := strings.TrimSpace(input.PipelineKey)
	versionName := strings.TrimSpace(input.Version)
	expectedSnapshot := strings.TrimSpace(input.ExpectedSnapshotID)
	if environmentKey == "" || pipelineKey == "" {
		return nil, releaseInvalidRequestError("x-environment and x-pipeline headers are required")
	}
	if versionName == "" {
		return nil, releaseInvalidRequestError("version is required")
	}
	if _, err := uuid.Parse(expectedSnapshot); err != nil {
		return nil, releaseInvalidRequestError("expected_snapshot_id must be a snapshot UUID")
	}
	scope, err := resolveReadScope(ctx, s.logic, environmentKey, pipelineKey, input.ExpectedScopeID)
	if err != nil {
		return nil, err
	}
	// 服从更短的调用方期限
	ctx, cancel := context.WithTimeout(ctx, releaseExportTotalTimeout)
	defer cancel()

	plan, err := s.buildReleaseStaticPlan(ctx, scope, versionName, expectedSnapshot)
	if err != nil {
		// 期限内失败才是内部/业务错误;已超时或已取消统一按导出超时表达
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, releaseExportContextError(ctxErr)
		}
		return nil, err
	}
	storage, err := s.snapshotStore(ctx, s.logic, plan.versionRow)
	if err != nil {
		return nil, err
	}
	if hook := releaseExportPauseHookForTest; hook != nil {
		hook(plan.versionRow.SnapshotID)
	}
	tempPath, size, err := writeReleaseStaticArchive(ctx, storage, plan)
	if err != nil {
		return nil, err
	}
	return &ReleaseStaticExportResult{
		SnapshotID: plan.versionRow.SnapshotID,
		Version:    plan.versionRow.Version,
		FileName:   releaseStaticFileName(plan.versionRow.SnapshotID, plan.versionRow.Version),
		TempPath:   tempPath,
		Size:       size,
	}, nil
}

// buildReleaseStaticPlan 固定捕获到的范围与版本行,核对页面展示的 snapshot 标识与两类摘要。
func (s *Service) buildReleaseStaticPlan(ctx context.Context, scope *model.Pipeline,
	versionName, expectedSnapshot string) (*releaseStaticExportPlan, error) {
	row, err := s.logic.releaseDAO.GetVersionByScopeIDAndName(ctx, s.logic.db, scope.ScopeID, versionName)
	if err != nil {
		if db.IsReleaseNotFound(err) {
			return nil, releaseVersionNotFoundError(scope.EnvironmentKey, scope.PipelineKey, versionName)
		}
		return nil, releaseInternalError(err)
	}
	if row.ScopeID != scope.ScopeID {
		return nil, releaseIncompleteError("release version belongs to another scope generation")
	}
	if row.SnapshotID != expectedSnapshot {
		return nil, releaseSnapshotMismatchError(scope.EnvironmentKey, scope.PipelineKey, versionName, expectedSnapshot, row.SnapshotID)
	}
	if row.DeletedAt != nil {
		return nil, releaseVersionDeletedError()
	}
	return s.buildReleaseStaticPlanFromVersion(ctx, row)
}

// buildReleaseStaticPlanFromVersion works from an already selected immutable version.
// It never reselects active or reloads lifecycle state during an in-flight export.
func (s *Service) buildReleaseStaticPlanFromVersion(ctx context.Context, row *model.ConfigReleaseVersion) (*releaseStaticExportPlan, error) {
	if payloadDigest(row.Payload) != row.PayloadSha256 {
		return nil, releaseIncompleteError("release payload digest mismatch")
	}
	assets, err := s.logic.releaseDAO.ListAssets(ctx, s.logic.db, row.SnapshotID)
	if err != nil {
		return nil, releaseInternalError(err)
	}
	if buildReleaseManifest(assets) != row.ManifestSha256 {
		return nil, releaseIncompleteError("release manifest digest mismatch")
	}
	payload, err := decodeReleasePayload(row.Payload)
	if err != nil {
		return nil, releaseIncompleteError("release payload is not decodable")
	}
	listed := make(map[string]model.ConfigReleaseAsset, len(assets))
	for _, asset := range assets {
		listed[asset.AssetID] = asset
	}
	for assetID := range referencedReleaseAssets(payload) {
		if _, ok := listed[assetID]; !ok {
			return nil, releaseIncompleteError("release asset manifest row is missing")
		}
	}
	configJSON, err := buildReleaseStaticConfig(row, payload)
	if err != nil {
		return nil, err
	}
	manifest, err := buildReleaseStaticManifest(row, payload, assets)
	if err != nil {
		return nil, err
	}
	return &releaseStaticExportPlan{versionRow: row, assets: assets, configJSON: configJSON, manifest: manifest}, nil
}

// releaseStaticEnvironment 与在线读取保持一致的范围字段。
type releaseStaticEnvironment struct {
	EnvironmentKey  string `json:"environment_key"`
	EnvironmentName string `json:"environment_name"`
	PipelineKey     string `json:"pipeline_key"`
	PipelineName    string `json:"pipeline_name"`
}

// releaseStaticConfig 是包内 config.json 的一项:结构化内容按原生 JSON 输出。
type releaseStaticConfig struct {
	ResourceKey string `json:"resource_key"`
	Alias       string `json:"alias"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Content     any    `json:"content"`
	Remark      string `json:"remark"`
	IsPerm      bool   `json:"is_perm"`
	Description string `json:"description"`
}

type releaseStaticConfigData struct {
	SnapshotID           string                   `json:"snapshot_id"`
	Version              string                   `json:"version"`
	SourceFreezeRevision string                   `json:"source_freeze_revision"`
	PayloadSha256        string                   `json:"payload_sha256"`
	SelectionMode        string                   `json:"selection_mode"`
	Environment          releaseStaticEnvironment `json:"environment"`
	Configs              []releaseStaticConfig    `json:"configs"`
}

type releaseStaticConfigEnvelope struct {
	Code int32                   `json:"code"`
	Msg  string                  `json:"msg"`
	Data releaseStaticConfigData `json:"data"`
}

// buildReleaseStaticConfig 生成包内 config.json:受管资源引用改为包内相对路径。
func buildReleaseStaticConfig(row *model.ConfigReleaseVersion, payload *releasePayload) ([]byte, error) {
	configs := make([]releaseStaticConfig, 0, len(payload.Configs))
	for i := range payload.Configs {
		cfg := payload.Configs[i]
		rendered, err := renderReleaseContentWith(cfg.Content, cfg.Type, releaseStaticArchiveRenderer)
		if err != nil {
			return nil, err
		}
		content, err := releaseStaticConfigContent(rendered, cfg.Type)
		if err != nil {
			return nil, err
		}
		configs = append(configs, releaseStaticConfig{
			ResourceKey: cfg.ResourceKey, Alias: cfg.Alias, Name: cfg.Name, Type: cfg.Type,
			Content: content, Remark: cfg.Remark, IsPerm: cfg.IsPerm, Description: cfg.Description,
		})
	}
	envelope := releaseStaticConfigEnvelope{
		Code: 200,
		Msg:  "OK",
		Data: releaseStaticConfigData{
			SnapshotID:           row.SnapshotID,
			Version:              row.Version,
			SourceFreezeRevision: fmt.Sprintf("%d", row.SourceFreezeRevision),
			PayloadSha256:        row.PayloadSha256,
			SelectionMode:        releaseStaticSelectionMode,
			Environment: releaseStaticEnvironment{
				EnvironmentKey:  payload.Scope.EnvironmentKey,
				EnvironmentName: payload.Scope.EnvironmentName,
				PipelineKey:     payload.Scope.PipelineKey,
				PipelineName:    payload.Scope.PipelineName,
			},
			Configs: configs,
		},
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, releaseInternalError(err)
	}
	return encoded, nil
}

// releaseStaticConfigContent 与在线 json 格式一致:object/keyvalue 为原生 JSON,其余为字符串。
func releaseStaticConfigContent(rendered, typ string) (any, error) {
	switch normalizeConfigTypeString(typ) {
	case "object", "keyvalue":
		var value any
		if err := DecodeConfigJSON([]byte(rendered), &value); err != nil {
			return nil, releaseIncompleteError("release config content is not decodable")
		}
		return value, nil
	default:
		return rendered, nil
	}
}

// releaseStaticArchiveRenderer 把标记解析为包内相对路径(以解压包根目录为基准,保留原后缀)。
func releaseStaticArchiveRenderer(assetID, suffix string) (string, bool) {
	return "./" + releaseStaticAssetsDirName + "/" + assetID + releaseAssetBinSuffix + suffix, true
}

// releaseStaticManifestAsset 是清单中的一份资源。
type releaseStaticManifestAsset struct {
	AssetID     string `json:"asset_id"`
	Path        string `json:"path"`
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Sha256      string `json:"sha256"`
}

type releaseStaticManifestFile struct {
	Format               string                       `json:"format"`
	SnapshotID           string                       `json:"snapshot_id"`
	Version              string                       `json:"version"`
	EnvironmentKey       string                       `json:"environment_key"`
	PipelineKey          string                       `json:"pipeline_key"`
	SourceFreezeRevision string                       `json:"source_freeze_revision"`
	ConfigCount          int                          `json:"config_count"`
	Assets               []releaseStaticManifestAsset `json:"assets"`
}

// buildReleaseStaticManifest 生成包内 manifest.json,清单按 asset_id 升序(与快照清单摘要同一顺序)。
func buildReleaseStaticManifest(row *model.ConfigReleaseVersion, payload *releasePayload, assets []model.ConfigReleaseAsset) ([]byte, error) {
	ordered := make([]model.ConfigReleaseAsset, len(assets))
	copy(ordered, assets)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].AssetID < ordered[j].AssetID })
	entries := make([]releaseStaticManifestAsset, 0, len(ordered))
	for _, asset := range ordered {
		entries = append(entries, releaseStaticManifestAsset{
			AssetID:     asset.AssetID,
			Path:        path.Join(releaseStaticAssetsDirName, asset.AssetID+releaseAssetBinSuffix),
			FileName:    asset.FileName,
			ContentType: asset.ContentType,
			Size:        asset.Size,
			Sha256:      asset.Sha256,
		})
	}
	encoded, err := json.Marshal(releaseStaticManifestFile{
		Format:               releaseStaticFormatName,
		SnapshotID:           row.SnapshotID,
		Version:              row.Version,
		EnvironmentKey:       payload.Scope.EnvironmentKey,
		PipelineKey:          payload.Scope.PipelineKey,
		SourceFreezeRevision: fmt.Sprintf("%d", row.SourceFreezeRevision),
		ConfigCount:          len(payload.Configs),
		Assets:               entries,
	})
	if err != nil {
		return nil, releaseInternalError(err)
	}
	return encoded, nil
}

// writeReleaseStaticArchive 写临时 ZIP:JSON 用 Deflate,资源原样写入并在写入过程中核对字节数与摘要。
func writeReleaseStaticArchive(ctx context.Context, storage releaseAssetStore, plan *releaseStaticExportPlan) (string, int64, error) {
	temp, err := os.CreateTemp("", releaseExportTempPrefix+"*.zip")
	if err != nil {
		return "", 0, releaseStorageError(err)
	}
	tempPath := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	writer := zip.NewWriter(temp)
	total := int64(len(plan.configJSON) + len(plan.manifest))
	if total > releaseExportMaxOutputByte {
		cleanup()
		return "", 0, releaseExportLimitError(total)
	}
	if err := writeZipEntry(writer, releaseStaticConfigName, plan.configJSON, zip.Deflate); err != nil {
		cleanup()
		return "", 0, err
	}
	if err := writeZipEntry(writer, releaseStaticManifestName, plan.manifest, zip.Deflate); err != nil {
		cleanup()
		return "", 0, err
	}
	for _, asset := range plan.assets {
		if err := ctx.Err(); err != nil {
			cleanup()
			return "", 0, releaseExportContextError(err)
		}
		total += asset.Size
		if total > releaseExportMaxOutputByte {
			cleanup()
			return "", 0, releaseExportLimitError(total)
		}
		if err := writeReleaseStaticAsset(ctx, writer, storage, plan.versionRow.SnapshotID, asset); err != nil {
			cleanup()
			return "", 0, err
		}
	}
	if err := writer.Close(); err != nil {
		cleanup()
		return "", 0, releaseStorageError(err)
	}
	info, err := temp.Stat()
	if err != nil {
		cleanup()
		return "", 0, releaseStorageError(err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", 0, releaseStorageError(err)
	}
	return tempPath, info.Size(), nil
}

// writeZipEntry 写入一份内存中的条目。
func writeZipEntry(writer *zip.Writer, name string, content []byte, method uint16) error {
	header := &zip.FileHeader{Name: name, Method: method}
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return releaseStorageError(err)
	}
	if _, err := entry.Write(content); err != nil {
		return releaseStorageError(err)
	}
	return nil
}

// writeReleaseStaticAsset 只打开一次文件:写入包的同时计算摘要,写完立即核对字节数与 SHA-256。
func writeReleaseStaticAsset(ctx context.Context, writer *zip.Writer, storage releaseAssetStore, snapshotID string, asset model.ConfigReleaseAsset) error {
	source, err := storage.openAsset(snapshotID, asset.AssetID)
	if err != nil {
		return err
	}
	defer source.Close()

	entry, err := writer.CreateHeader(&zip.FileHeader{
		Name:   path.Join(releaseStaticAssetsDirName, asset.AssetID+releaseAssetBinSuffix),
		Method: zip.Store,
	})
	if err != nil {
		return releaseStorageError(err)
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(entry, hasher), io.LimitReader(releaseContextReader{ctx: ctx, source: source}, asset.Size+1))
	if err != nil {
		return releaseStorageError(err)
	}
	if written != asset.Size {
		return releaseIncompleteError(fmt.Sprintf("release asset %s size mismatch", asset.AssetID))
	}
	if hex.EncodeToString(hasher.Sum(nil)) != asset.Sha256 {
		return releaseIncompleteError(fmt.Sprintf("release asset %s digest mismatch", asset.AssetID))
	}
	return nil
}

// releaseExportLimitError 未压缩输出总量超限(不影响该版本在线读取)。
func releaseExportLimitError(actual int64) *ReleaseError {
	return releaseLimitExceededError(ReleaseLimitInfo{Item: "export_uncompressed_bytes", Limit: releaseExportMaxOutputByte, Actual: actual})
}

// releaseExportContextError 区分构包超时与主动取消:超时在响应开始前返回 503。
func releaseExportContextError(err error) *ReleaseError {
	if errors.Is(err, context.DeadlineExceeded) {
		return releaseExportTimeoutError()
	}
	return releaseInternalError(err)
}

// releaseStaticFileName 只使用已校验的 snapshot UUID 与受限字符的版本号。
func releaseStaticFileName(snapshotID, version string) string {
	var builder strings.Builder
	for _, r := range version {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		}
	}
	name := builder.String()
	if name == "" {
		name = "version"
	}
	return fmt.Sprintf("release-%s-%s.zip", snapshotID, name)
}

// ExportActiveReleasePackage 供旧 runtime 静态导出使用;空 pin 保持原调用兼容。
func (s *Service) ExportActiveReleasePackage(ctx context.Context, environmentKey, pipelineKey string) (result *ReleaseStaticExportResult, err error) {
	return s.ExportActiveReleasePackageWithScope(ctx, environmentKey, pipelineKey, "")
}

// ExportActiveReleasePackageWithScope 在本次请求内先捕获范围代身份,再按该 scope 的
// head 固定一次生效版本并复用发布构包器;不按 env/pipeline 重新解析另一代。
func (s *Service) ExportActiveReleasePackageWithScope(ctx context.Context,
	environmentKey, pipelineKey, requestedScopeID string) (result *ReleaseStaticExportResult, err error) {
	environmentKey = strings.TrimSpace(environmentKey)
	pipelineKey = strings.TrimSpace(pipelineKey)
	if environmentKey == "" || pipelineKey == "" {
		return nil, releaseInvalidRequestError("environment_key and pipeline_key are required")
	}
	ctx, cancel := context.WithTimeout(ctx, releaseExportTotalTimeout)
	defer cancel()
	defer func() {
		if err != nil && ctx.Err() != nil {
			err = releaseExportContextError(ctx.Err())
		}
	}()
	scopeID, err := normalizeLegacyScopeID(requestedScopeID)
	if err != nil {
		return nil, err
	}
	scope, err := resolveReadScope(ctx, s.logic, environmentKey, pipelineKey, scopeID)
	if err != nil {
		return nil, err
	}
	if hook := releaseRuntimeScopeHookForTest; hook != nil {
		hook(scope.ScopeID)
	}
	head, err := s.logic.releaseDAO.GetHeadByScopeID(ctx, s.logic.db, scope.ScopeID)
	if err != nil {
		return nil, releaseInternalError(err)
	}
	if head == nil || head.ActiveSnapshotID == "" {
		return nil, releaseNotActiveError(environmentKey, pipelineKey)
	}
	row, err := s.logic.releaseDAO.GetVersionBySnapshotID(ctx, s.logic.db, head.ActiveSnapshotID)
	if err != nil {
		if db.IsReleaseNotFound(err) {
			return nil, releaseIncompleteError("active snapshot row is missing")
		}
		return nil, releaseInternalError(err)
	}
	if row.DeletedAt != nil {
		return nil, releaseIncompleteError("active snapshot has been deleted")
	}
	if row.ScopeID != scope.ScopeID {
		return nil, releaseIncompleteError("active snapshot belongs to another scope generation")
	}
	plan, err := s.buildReleaseStaticPlanFromVersion(ctx, row)
	if err != nil {
		if ctx.Err() != nil {
			return nil, releaseExportContextError(ctx.Err())
		}
		return nil, err
	}
	storage, err := s.snapshotStore(ctx, s.logic, row)
	if err != nil {
		return nil, err
	}
	if hook := releaseExportPauseHookForTest; hook != nil {
		hook(row.SnapshotID)
	}
	tempPath, size, err := writeReleaseStaticArchive(ctx, storage, plan)
	if err != nil {
		return nil, err
	}
	return &ReleaseStaticExportResult{
		SnapshotID: row.SnapshotID,
		Version:    row.Version,
		FileName:   releaseStaticFileName(row.SnapshotID, row.Version),
		TempPath:   tempPath,
		Size:       size,
	}, nil
}
