package service

import (
	"context"
	"strings"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

// releaseReadPauseHookForTest 仅测试使用:读取生效版本时,在解析到版本行之后、
// 读取快照内容之前调用,用于构造"读取途中发生切换"的确定性时序。默认 nil,生产不启用。
var releaseReadPauseHookForTest func(snapshotID string)

// RuntimeRelease 读取版本快照:未指定 version 时跟随生效指针,指定时固定读该版本。
// 不回退到编辑区、最新创建版本或其他版本;所有完整性校验失败返回 503。
// 每个响应内部版本一致:active 只解析一次,后续校验与渲染都基于这一行快照。
func (s *Service) RuntimeRelease(ctx context.Context, environmentKey, pipelineKey string, versionParam *string) (*ReleaseRuntimeConfig, error) {
	environmentKey = strings.TrimSpace(environmentKey)
	pipelineKey = strings.TrimSpace(pipelineKey)
	if environmentKey == "" || pipelineKey == "" {
		return nil, releaseInvalidRequestError("x-environment and x-pipeline headers are required")
	}
	if err := s.assertReleaseScopeExists(ctx, s.logic, environmentKey, pipelineKey); err != nil {
		return nil, err
	}
	selection := model.ReleaseSelectionActive
	var activeRevision *int64
	var activatedAt string
	var versionRow *model.ConfigReleaseVersion
	var err error
	if versionParam == nil {
		// 一次请求只解析一次 active_snapshot_id
		head, err := s.logic.releaseDAO.GetHead(ctx, s.logic.db, environmentKey, pipelineKey)
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
		versionRow, err = s.logic.releaseDAO.GetVersionByScopeAndName(ctx, s.logic.db, environmentKey, pipelineKey, versionName)
		if err != nil {
			if db.IsReleaseNotFound(err) {
				return nil, releaseVersionNotFoundError(environmentKey, pipelineKey, versionName)
			}
			return nil, releaseInternalError(err)
		}
	}

	if versionRow.DeletedAt != nil {
		if versionParam == nil {
			return nil, releaseIncompleteError("active snapshot has been deleted")
		}
		return nil, releaseVersionDeletedError()
	}
	storage, err := s.releaseStorage()
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

// verifyReleaseReadIntegrity 校验 payload 摘要、清单摘要与引用资源的存在与长度。
// 完整 SHA-256 校验在资源下载时执行,这里保证"存在且长度一致"。
func verifyReleaseReadIntegrity(ctx context.Context, l *Logic, storage *releaseStorage, row *model.ConfigReleaseVersion) error {
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
		size, _, err := storage.verifyFile(storage.assetPath(row.SnapshotID, asset.AssetID))
		if err != nil {
			return err
		}
		if size != asset.Size {
			return releaseIncompleteError("release asset size mismatch")
		}
	}
	return nil
}

// ReleaseAssetDownload 仅从已提交清单定位本版本文件;先校验完整 SHA-256 再返回字节。
func (s *Service) ReleaseAssetDownload(ctx context.Context, snapshotID, assetID string) (string, []byte, error) {
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
	asset, err := s.logic.releaseDAO.GetAsset(ctx, s.logic.db, snapshotID, assetID)
	if err != nil {
		if db.IsReleaseNotFound(err) {
			return "", nil, releaseAssetNotFoundError(snapshotID, assetID)
		}
		return "", nil, releaseInternalError(err)
	}
	storage, err := s.releaseStorage()
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
