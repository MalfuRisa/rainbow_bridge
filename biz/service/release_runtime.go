package service

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

// releaseReadPauseHookForTest 仅测试使用:读取生效版本时,在解析到版本行之后、
// 读取快照内容之前调用,用于构造"读取途中发生切换"的确定性时序。默认 nil,生产不启用。
var releaseReadPauseHookForTest func(snapshotID string)

// releaseRuntimeScopeHookForTest 仅测试使用:范围身份捕获之后、head/版本读取之前调用。
// 用于证明身份捕获后同 key 删除重建不会让旧 pin 改读新代。
var releaseRuntimeScopeHookForTest func(scopeID string)

// RuntimeRelease 读取版本快照:未指定 version 时跟随生效指针,指定时固定读该版本。
// 不回退到编辑区、最新创建版本或其他版本;所有完整性校验失败返回 503。
// 每个响应内部版本一致:active 只解析一次,后续校验与渲染都基于这一行快照。
func (s *Service) RuntimeRelease(ctx context.Context, environmentKey, pipelineKey string, versionParam *string) (*ReleaseRuntimeConfig, error) {
	return s.RuntimeReleaseWithScope(ctx, environmentKey, pipelineKey, versionParam, "")
}

// RuntimeReleaseLegacy preserves the old config endpoint: an empty or
// whitespace-only scope_id means no pin, but a non-empty value must be a UUID
// before the shared read-scope validation runs.
func (s *Service) RuntimeReleaseLegacy(ctx context.Context, environmentKey, pipelineKey, requestedScopeID string) (*ReleaseRuntimeConfig, error) {
	if strings.TrimSpace(environmentKey) == "" || strings.TrimSpace(pipelineKey) == "" {
		return nil, releaseInvalidRequestError("x-environment and x-pipeline headers are required")
	}
	scopeID, err := normalizeLegacyScopeID(requestedScopeID)
	if err != nil {
		return nil, err
	}
	return s.RuntimeReleaseWithScope(ctx, environmentKey, pipelineKey, nil, scopeID)
}

func normalizeLegacyScopeID(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if _, err := uuid.Parse(value); err != nil {
		return "", scopeInvalidRequest("scope_id must be a UUID")
	}
	return value, nil
}

// RuntimeReleaseWithScope optionally pins the caller to the scope_id it saw.
// The captured pipeline identity is used for every following head/version read.
func (s *Service) RuntimeReleaseWithScope(ctx context.Context, environmentKey, pipelineKey string,
	versionParam *string, requestedScopeID string) (*ReleaseRuntimeConfig, error) {
	ctx, cancel := context.WithTimeout(ctx, writeGateTotalTimeout)
	defer cancel()
	environmentKey = strings.TrimSpace(environmentKey)
	pipelineKey = strings.TrimSpace(pipelineKey)
	if environmentKey == "" || pipelineKey == "" {
		return nil, releaseInvalidRequestError("x-environment and x-pipeline headers are required")
	}
	scope, err := resolveReadScope(ctx, s.logic, environmentKey, pipelineKey, requestedScopeID)
	if err != nil {
		return nil, err
	}
	if hook := releaseRuntimeScopeHookForTest; hook != nil {
		hook(scope.ScopeID)
	}
	selection := model.ReleaseSelectionActive
	var activeRevision *int64
	var activatedAt string
	var versionRow *model.ConfigReleaseVersion
	if versionParam == nil {
		// 一次请求只解析一次 active_snapshot_id
		head, err := s.logic.releaseDAO.GetHeadByScopeID(ctx, s.logic.db, scope.ScopeID)
		if err != nil {
			return nil, releaseInternalError(err)
		}
		if head == nil || head.ActiveSnapshotID == "" {
			return nil, releaseNotActiveError(environmentKey, pipelineKey)
		}
		versionRow, err = s.logic.releaseDAO.GetVersionBySnapshotID(ctx, s.logic.db, head.ActiveSnapshotID)
		if err != nil {
			if db.IsReleaseNotFound(err) {
				return nil, releaseIncompleteError("active snapshot row is missing")
			}
			return nil, releaseInternalError(err)
		}
		revision := head.Revision
		activeRevision = &revision
		if head.ChangedAt != nil {
			activatedAt = head.ChangedAt.UTC().Format(time.RFC3339)
		}
	} else {
		versionName := strings.TrimSpace(*versionParam)
		if versionName == "" {
			return nil, releaseInvalidRequestError("version must not be empty when provided")
		}
		selection = model.ReleaseSelectionFixed
		versionRow, err = s.logic.releaseDAO.GetVersionByScopeIDAndName(ctx, s.logic.db, scope.ScopeID, versionName)
		if err != nil {
			if db.IsReleaseNotFound(err) {
				return nil, releaseVersionNotFoundError(environmentKey, pipelineKey, versionName)
			}
			return nil, releaseInternalError(err)
		}
	}
	if versionRow.ScopeID != scope.ScopeID {
		return nil, releaseIncompleteError("release version belongs to another scope generation")
	}

	if versionRow.DeletedAt != nil {
		if versionParam == nil {
			return nil, releaseIncompleteError("active snapshot has been deleted")
		}
		return nil, releaseVersionDeletedError()
	}
	storage, err := s.snapshotStore(ctx, s.logic, versionRow)
	if err != nil {
		return nil, err
	}
	if hook := releaseReadPauseHookForTest; hook != nil {
		hook(versionRow.SnapshotID)
	}
	if err := verifyReleaseReadIntegrity(ctx, s.logic, storage, versionRow); err != nil {
		return nil, err
	}
	payload, err := decodeReleasePayload(versionRow.Payload)
	if err != nil {
		return nil, releaseIncompleteError("release payload is not decodable")
	}
	configs := make([]releaseRuntimeConfig, 0, len(payload.Configs))
	for _, cfg := range payload.Configs {
		content, err := s.renderReleaseContent(versionRow.SnapshotID, cfg.Content, cfg.Type)
		if err != nil {
			return nil, err
		}
		configs = append(configs, releaseRuntimeConfig{
			ResourceKey: cfg.ResourceKey, Alias: cfg.Alias, Name: cfg.Name, Type: cfg.Type,
			Content: content, Remark: cfg.Remark, IsPerm: cfg.IsPerm, Description: cfg.Description,
		})
	}
	return &ReleaseRuntimeConfig{
		ScopeID:              scope.ScopeID,
		ActivatedAt:          activatedAt,
		SnapshotID:           versionRow.SnapshotID,
		Version:              versionRow.Version,
		SourceFreezeRevision: versionRow.SourceFreezeRevision,
		PayloadSha256:        versionRow.PayloadSha256,
		SelectionMode:        selection,
		EnvironmentKey:       payload.Scope.EnvironmentKey,
		EnvironmentName:      payload.Scope.EnvironmentName,
		PipelineKey:          payload.Scope.PipelineKey,
		PipelineName:         payload.Scope.PipelineName,
		Configs:              configs,
		ActiveRevision:       activeRevision,
	}, nil
}

// verifyReleaseReadIntegrity checks payload/manifest and referenced resource hashes.
// Both local and MinIO readers reject same-length corruption before rendering runtime.
func verifyReleaseReadIntegrity(ctx context.Context, l *Logic, storage releaseAssetStore, row *model.ConfigReleaseVersion) error {
	if payloadDigest(row.Payload) != row.PayloadSha256 {
		return releaseIncompleteError("release payload digest mismatch")
	}
	assets, err := l.releaseDAO.ListAssets(ctx, l.db, row.SnapshotID)
	if err != nil {
		return releaseInternalError(err)
	}
	if buildReleaseManifest(assets) != row.ManifestSha256 {
		return releaseIncompleteError("release manifest digest mismatch")
	}
	payload, err := decodeReleasePayload(row.Payload)
	if err != nil {
		return releaseIncompleteError("release payload is not decodable")
	}
	byID := make(map[string]model.ConfigReleaseAsset, len(assets))
	for _, asset := range assets {
		byID[asset.AssetID] = asset
	}
	for assetID := range referencedReleaseAssets(payload) {
		asset, ok := byID[assetID]
		if !ok {
			return releaseIncompleteError("release asset manifest row is missing")
		}
		if err := storage.verifyAssetFile(row.SnapshotID, asset.AssetID, asset.Size, asset.Sha256); err != nil {
			return err
		}
	}
	return nil
}

// ReleaseAssetDownload 仅从已提交清单定位本版本文件;先校验完整 SHA-256 再返回字节。
func (s *Service) ReleaseAssetDownload(ctx context.Context, snapshotID, assetID string) (string, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, writeGateTotalTimeout)
	defer cancel()
	snapshotID = strings.TrimSpace(snapshotID)
	assetID = strings.TrimSpace(assetID)
	if snapshotID == "" || assetID == "" {
		return "", nil, releaseInvalidRequestError("snapshot_id and asset_id are required")
	}
	version, err := s.logic.releaseDAO.GetVersionBySnapshotID(ctx, s.logic.db, snapshotID)
	if err != nil {
		if db.IsReleaseNotFound(err) {
			return "", nil, releaseAssetNotFoundError(snapshotID, assetID)
		}
		return "", nil, releaseInternalError(err)
	}
	if version.DeletedAt != nil {
		return "", nil, releaseVersionDeletedError()
	}
	active, err := db.ScopeIsActive(ctx, s.logic.db, version.ScopeID)
	if err != nil {
		return "", nil, releaseInternalError(err)
	}
	if !active {
		return "", nil, scopeDeletedError("release asset belongs to a deleted scope")
	}
	asset, err := s.logic.releaseDAO.GetAsset(ctx, s.logic.db, snapshotID, assetID)
	if err != nil {
		if db.IsReleaseNotFound(err) {
			return "", nil, releaseAssetNotFoundError(snapshotID, assetID)
		}
		return "", nil, releaseInternalError(err)
	}
	storage, err := s.snapshotStore(ctx, s.logic, version)
	if err != nil {
		return "", nil, err
	}
	payload, err := storage.readAssetBytes(snapshotID, assetID, asset.Size, asset.Sha256)
	if err != nil {
		return "", nil, err
	}
	contentType := strings.TrimSpace(asset.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return contentType, payload, nil
}
