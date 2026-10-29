package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

var releaseVersionRegexp = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// --------------------- 请求输入 ---------------------

// ReleaseCreateInput 是创建版本的请求。
type ReleaseCreateInput struct {
	ExpectedScopeID           string
	EnvironmentKey            string
	PipelineKey               string
	Version                   string
	AutoOnly                  bool
	ExpectedFreezeRevisionRaw string
	RequestID                 string
	Reason                    string
}

// ReleaseActivateInput 是切换生效版本的请求。
type ReleaseActivateInput struct {
	ExpectedScopeID           string
	EnvironmentKey            string
	PipelineKey               string
	Version                   string
	ExpectedActiveRevisionRaw string
	RequestID                 string
	Reason                    string
}

// --------------------- 创建版本 ---------------------

// CreateRelease 在已冻结范围上创建完整独立的版本快照。
// 管理凭据验证 → 写协调 → 幂等/冲突(先于源存储与冻结状态,已提交结果必须可确认) →
// 版本号冲突与范围校验 → 源存储能力与快照目录 → 冻结与 revision 校验 → 捕获与限额 →
// 复制到准备目录(流式+SHA) → 移动最终目录 → 同一事务插入版本/清单/事件 → 提交。
func (s *Service) CreateRelease(ctx context.Context, input ReleaseCreateInput) (*ReleaseCreateResult, error) {
	environmentKey, pipelineKey, version, reason, requestID, expectedRevision, err := validateReleaseCreateInput(
		input.EnvironmentKey, input.PipelineKey, input.Version, input.ExpectedFreezeRevisionRaw, input.RequestID, input.Reason)
	if err != nil {
		return nil, err
	}
	identity, err := requireAdminIdentity(ctx)
	if err != nil {
		return nil, err
	}
	expectedScopeID := strings.TrimSpace(input.ExpectedScopeID)
	autoVersion := version == ""
	// 新入口主动设置不长于 60 秒的期限(不延长调用方更短的期限)
	ctx, cancel := context.WithTimeout(ctx, writeGateTotalTimeout)
	defer cancel()

	fingerprintOperation := "create"
	fingerprintParts := []string{environmentKey, pipelineKey, expectedScopeID, version,
		strconv.FormatInt(expectedRevision, 10), reason}
	if autoVersion {
		// Auto requests keep the client payload replayable: the server-assigned
		// version is deliberately absent from the fingerprint.
		fingerprintOperation = "create-auto-v1"
		fingerprintParts = []string{environmentKey, pipelineKey, expectedScopeID,
			strconv.FormatInt(expectedRevision, 10), reason}
	}
	fingerprint := releaseFingerprint(fingerprintOperation, fingerprintParts...)
	// Register ownership independently before the business transaction. A
	// committed replay must not depend on today's storage configuration.
	var attempt *model.ReleaseStorageAttempt
	existing, lookupErr := s.logic.releaseDAO.GetVersionByCreationRequestID(ctx, s.logic.db, requestID)
	if lookupErr != nil {
		return nil, releaseInternalError(lookupErr)
	}
	if existing == nil {
		backend, err := s.newSnapshotBackend()
		if err != nil {
			return nil, err
		}
		if backend == "minio" {
			pipeline, err := s.logic.pipelineDAO.GetByKey(ctx, s.logic.db, environmentKey, pipelineKey)
			if err != nil {
				return nil, releaseScopeNotFoundError(environmentKey, pipelineKey)
			}
			attempt, err = s.registerSnapshotAttempt(ctx, uuid.NewString(), requestID, fingerprint, pipeline.ScopeID, "create")
			if err != nil {
				return nil, err
			}
		}
	}
	defer func() { s.finishSnapshotAttempt(attempt) }()

	var (
		result      *ReleaseCreateResult
		storage     *releaseStorage
		stagingUsed bool
		finalMoved  bool
		snapshotID  string
	)
	var removeTemporary func()
	defer func() {
		if removeTemporary != nil {
			removeTemporary()
		}
	}()
	err = s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		// 1) 幂等重放与冲突:先于源存储能力、快照目录与冻结状态,
		//    已提交结果在任何后续配置变化下都必须能被确认
		if existing, err := txLogic.releaseDAO.GetVersionByCreationRequestID(gateCtx, txLogic.db, requestID); err != nil {
			return releaseInternalError(err)
		} else if existing != nil {
			if existing.RequestFingerprint != fingerprint {
				return releaseRequestConflictError("request_id was already used with different fields")
			}
			if existing.DeletedAt != nil {
				return releaseVersionDeletedError()
			}
			active, activeErr := db.ScopeIsActive(gateCtx, txLogic.db, existing.ScopeID)
			if activeErr != nil {
				return releaseInternalError(activeErr)
			}
			if !active {
				return scopeDeletedError("release request belongs to a deleted scope")
			}
			result = &ReleaseCreateResult{Meta: releaseMetaFromVersion(existing), Replayed: true}
			return nil
		}
		if !autoVersion {
			// HTTP clients may no longer submit a version. Idempotency was
			// checked first so an old committed request can still be replayed.
			if input.AutoOnly {
				return releaseVersionAutoAssignedError()
			}
			if existing, err := txLogic.releaseDAO.GetVersionByScopeAndName(gateCtx, txLogic.db, environmentKey, pipelineKey, version); err != nil {
				if !db.IsReleaseNotFound(err) {
					return releaseInternalError(err)
				}
			} else if existing != nil {
				return releaseVersionExistsError(environmentKey, pipelineKey, version)
			}
		}
		// 2) Identity + live scope validation
		pipelineRow, err := resolveScopeForWrite(gateCtx, txLogic, environmentKey, pipelineKey, expectedScopeID)
		if err != nil {
			return err
		}
		scopeRow, err := txLogic.environmentDAO.GetByID(gateCtx, txLogic.db, pipelineRow.EnvironmentID)
		if err != nil {
			return releaseScopeNotFoundError(environmentKey, pipelineKey)
		}
		// 3) Both original backends use bounded local capture; MinIO snapshot
		// mode uses disposable staging independent of the permanent local root.
		var resolvedStorage *releaseStorage
		if attempt != nil {
			if attempt.ScopeID != pipelineRow.ScopeID {
				return scopeDeletedError("snapshot attempt belongs to a previous scope")
			}
			if err := checkSnapshotAttempt(gateCtx, txLogic, attempt); err != nil {
				return err
			}
			resolvedStorage, removeTemporary, err = temporarySnapshotStorage()
		} else {
			resolvedStorage, err = s.releaseStorage()
		}
		if err != nil {
			return err
		}
		storage = resolvedStorage
		// 4) 冻结状态与 revision 校验(tx 直读,不用缓存)
		freezeState, err := txLogic.freezeStateOrUnlocked(gateCtx, environmentKey, pipelineKey)
		if err != nil {
			return releaseInternalError(err)
		}
		if freezeState.State != model.FreezeStateLocked {
			return releaseSourceNotFrozenError(environmentKey, pipelineKey)
		}
		if freezeState.Revision != expectedRevision {
			return releaseSourceRevisionError(expectedRevision, freezeState.Revision)
		}
		// 5) Allocate the version number only after the request is known to be
		// a real new operation, inside the same write transaction.
		if autoVersion {
			allocated, allocErr := allocateReleaseVersion(gateCtx, txLogic,
				environmentKey, pipelineKey, pipelineRow.ScopeID)
			if allocErr != nil {
				return allocErr
			}
			version = db.ReleaseVersionFromNumber(allocated)
		}
		// 6) 捕获计划(payload 已定型,资源待复制)
		plan, err := s.captureReleasePlan(gateCtx, txLogic, environmentKey, pipelineKey, scopeRow, pipelineRow)
		if err != nil {
			return err
		}
		snapshotID = uuid.NewString()
		if attempt != nil {
			snapshotID = attempt.SnapshotID
		}
		if err := storage.removeStaging(snapshotID); err != nil && !errors.Is(err, os.ErrNotExist) {
			return releaseStorageError(err)
		}
		stagingUsed = true
		return s.materializeRelease(gateCtx, txLogic, storage, snapshotID, plan, scopeRow, pipelineRow,
			version, requestID, fingerprint, expectedRevision, reason, identity.Username, &finalMoved, &result, attempt)
	})
	if err != nil {
		if storage != nil {
			s.cleanupReleaseArtifacts(ctx, storage, snapshotID, stagingUsed, finalMoved)
		}
		if attempt != nil {
			if business, ok := AsReleaseError(err); ok {
				annotated := *business
				annotated.Message += "; storage attempt " + attempt.ID
				err = &annotated
			}
		}
		return nil, err
	}
	maybeReleaseCrash("after_commit")
	return result, nil
}

// releaseCopyPauseHookForTest 仅测试使用:在复制资源期间调用,用于构造"复制中并发写入"的时序。默认 nil。
var releaseCopyPauseHookForTest func(snapshotID string)

// materializeRelease 复制资源、移动目录并在同一事务插入版本/清单/创建事件。
func (s *Service) materializeRelease(
	ctx context.Context,
	txLogic *Logic,
	storage *releaseStorage,
	snapshotID string,
	plan *releaseCapturePlan,
	scopeRow *model.Environment,
	pipelineRow *model.Pipeline,
	version, requestID, fingerprint string,
	expectedRevision int64,
	reason, actor string,
	finalMoved *bool,
	result **ReleaseCreateResult,
	attempt *model.ReleaseStorageAttempt,
) error {
	// 无资源快照也必须存在准备目录(空范围/无资源版本允许创建)
	if err := os.MkdirAll(filepath.Join(storage.stagingDir(snapshotID), releaseAssetsDirName), 0o755); err != nil {
		return releaseStorageError(err)
	}
	copied := make([]releaseCopiedAsset, 0, len(plan.Assets))
	var realTotal int64
	for _, item := range plan.Assets {
		record, err := s.copyReleaseSource(ctx, storage, snapshotID, item)
		if err != nil {
			return err
		}
		record.SourceFileID = item.SourceFileID
		realTotal += record.Size
		maybeReleaseCrash("during_copy")
		if hook := releaseCopyPauseHookForTest; hook != nil {
			hook(snapshotID)
		}
		if realTotal > model.ReleaseMaxTotalByte {
			return releaseLimitExceededError(ReleaseLimitInfo{Item: "total_asset_bytes", Limit: model.ReleaseMaxTotalByte, Actual: realTotal})
		}
		copied = append(copied, record)
	}
	if err := storage.moveStagingToFinal(snapshotID); err != nil {
		return err
	}
	*finalMoved = true
	maybeReleaseCrash("before_commit")

	assetRows := make([]model.ConfigReleaseAsset, 0, len(copied))
	for _, record := range copied {
		assetRows = append(assetRows, model.ConfigReleaseAsset{
			SnapshotID:   snapshotID,
			AssetID:      record.AssetID,
			SourceFileID: record.SourceFileID,
			FileName:     record.FileName,
			ContentType:  record.ContentType,
			Size:         record.Size,
			Sha256:       record.Sha256,
		})
	}
	manifest := buildReleaseManifest(assetRows)
	externalJSON, err := marshalExternalReferences(plan.ExternalReferences)
	if err != nil {
		return releaseInternalError(err)
	}
	now := time.Now().UTC()
	versionRow := &model.ConfigReleaseVersion{
		StorageBackend:       "local",
		SnapshotID:           snapshotID,
		ScopeID:              pipelineRow.ScopeID,
		EnvironmentKey:       scopeRow.EnvironmentKey,
		PipelineKey:          pipelineRow.PipelineKey,
		Version:              version,
		CreationRequestID:    requestID,
		RequestFingerprint:   fingerprint,
		SourceFreezeRevision: expectedRevision,
		EnvironmentName:      scopeRow.EnvironmentName,
		PipelineName:         pipelineRow.PipelineName,
		Payload:              plan.Payload,
		PayloadSha256:        plan.PayloadSha256,
		ManifestSha256:       manifest,
		ConfigCount:          plan.ConfigCount,
		AssetCount:           len(assetRows),
		AssetBytes:           realTotal,
		ExternalReferences:   externalJSON,
		Reason:               reason,
		Actor:                actor,
		CreatedAt:            now,
	}
	if attempt != nil {
		if err := s.publishSnapshotObjects(ctx, storage, versionRow, assetRows, attempt); err != nil {
			return err
		}
		if err := commitSnapshotAttempt(ctx, txLogic, attempt); err != nil {
			return err
		}
	}
	if err := txLogic.releaseDAO.CreateVersion(ctx, txLogic.db, versionRow); err != nil {
		return mapReleaseUniqueViolation(err)
	}
	if err := txLogic.releaseDAO.CreateAssets(ctx, txLogic.db, assetRows); err != nil {
		return releaseInternalError(err)
	}
	event := &model.ConfigReleaseEvent{
		ScopeID:            pipelineRow.ScopeID,
		EventType:          model.ReleaseEventCreate,
		RequestID:          requestID,
		RequestFingerprint: fingerprint,
		EnvironmentKey:     scopeRow.EnvironmentKey,
		PipelineKey:        pipelineRow.PipelineKey,
		SnapshotID:         snapshotID,
		Version:            version,
		Reason:             reason,
		Actor:              actor,
		CreatedAt:          now,
	}
	if err := txLogic.releaseDAO.AppendEvent(ctx, txLogic.db, event); err != nil {
		return releaseInternalError(err)
	}
	*result = &ReleaseCreateResult{Meta: releaseMetaFromVersion(versionRow), Replayed: false}
	return nil
}

// releaseCrashEnv 是测试专用的确定性进程中断点开关(生产不设置即完全无副作用)。
// 取值:during_copy / before_commit / after_commit。
const releaseCrashEnv = "RAINBOW_RELEASE_CRASH_POINT"

func maybeReleaseCrash(point string) {
	if strings.TrimSpace(os.Getenv(releaseCrashEnv)) != point {
		return
	}
	fmt.Printf("release crash point hit: %s; exiting for deterministic test\n", point)
	os.Exit(97)
}

// cleanupReleaseArtifacts 处理回滚后的目录清理:
// 已移动最终目录时先用新连接确认未被提交;确认不了则保留并记录(不误删成功版本)。
func (s *Service) cleanupReleaseArtifacts(ctx context.Context, storage *releaseStorage, snapshotID string, stagingUsed, finalMoved bool) {
	if storage == nil || snapshotID == "" {
		return
	}
	if finalMoved {
		confirmCtx, cancelConfirm := context.WithTimeout(ctx, 10*time.Second)
		defer cancelConfirm()
		var count int64
		err := s.logic.db.WithContext(confirmCtx).Model(&model.ConfigReleaseVersion{}).
			Where("snapshot_id = ?", snapshotID).Count(&count).Error
		if err != nil {
			fmt.Printf("release cleanup: snapshot %s commit state unknown; final directory kept for manual inspection\n", snapshotID)
			return
		}
		if count > 0 {
			// 已提交(响应丢失等):保留文件
			return
		}
		if err := storage.removeFinal(snapshotID); err != nil {
			fmt.Printf("release cleanup: snapshot %s final directory removal failed: %v\n", snapshotID, err)
		}
		return
	}
	if stagingUsed {
		if err := storage.removeStaging(snapshotID); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Printf("release cleanup: snapshot %s staging removal failed: %v\n", snapshotID, err)
		}
	}
}

// --------------------- 切换生效版本 ---------------------

// ActivateRelease 切换范围的生效版本;切回旧版与切换新版本使用同一规则。
func (s *Service) ActivateRelease(ctx context.Context, input ReleaseActivateInput) (*ReleaseActivateResult, error) {
	environmentKey, pipelineKey, version, reason, requestID, expectedRevision, err := validateReleaseTransitionInput(
		input.EnvironmentKey, input.PipelineKey, input.Version, input.ExpectedActiveRevisionRaw, input.RequestID, input.Reason)
	if err != nil {
		return nil, err
	}
	identity, err := requireAdminIdentity(ctx)
	if err != nil {
		return nil, err
	}
	expectedScopeID := strings.TrimSpace(input.ExpectedScopeID)
	ctx, cancel := context.WithTimeout(ctx, writeGateTotalTimeout)
	defer cancel()

	fingerprint := releaseFingerprint("activate", environmentKey, pipelineKey, expectedScopeID, version,
		strconv.FormatInt(expectedRevision, 10), reason)

	var (
		result  *ReleaseActivateResult
		storage releaseAssetStore
	)
	err = s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		// 幂等重放:返回原 applied_revision 与此刻最新的 active 状态;
		// 先于快照目录解析与完整性校验,已提交结果不依赖当前环境配置
		if event, err := txLogic.releaseDAO.GetEvent(gateCtx, txLogic.db, model.ReleaseEventActivate, requestID); err != nil {
			return releaseInternalError(err)
		} else if event != nil {
			if event.RequestFingerprint != fingerprint {
				return releaseRequestConflictError("request_id was already used with different fields")
			}
			activeScope, scopeErr := db.ScopeIsActive(gateCtx, txLogic.db, event.ScopeID)
			if scopeErr != nil {
				return releaseInternalError(scopeErr)
			}
			if !activeScope {
				return scopeDeletedError("release request belongs to a deleted scope")
			}
			active, err := s.loadActive(gateCtx, txLogic, environmentKey, pipelineKey)
			if err != nil {
				return err
			}
			result = &ReleaseActivateResult{
				RequestID:       requestID,
				Replayed:        true,
				AppliedRevision: event.ToHeadRevision,
				Active:          active,
				Version:         version,
			}
			return nil
		}
		pipelineRow, err := resolveScopeForWrite(gateCtx, txLogic, environmentKey, pipelineKey, expectedScopeID)
		if err != nil {
			return err
		}
		target, err := txLogic.releaseDAO.GetVersionByScopeAndName(gateCtx, txLogic.db, environmentKey, pipelineKey, version)
		if err != nil {
			if db.IsReleaseNotFound(err) {
				return releaseVersionNotFoundError(environmentKey, pipelineKey, version)
			}
			return releaseInternalError(err)
		}
		if target.DeletedAt != nil {
			return releaseVersionDeletedError()
		}
		if err := txLogic.releaseDAO.EnsureHead(gateCtx, txLogic.db, environmentKey, pipelineKey); err != nil {
			return releaseInternalError(err)
		}
		head, err := txLogic.releaseDAO.GetHead(gateCtx, txLogic.db, environmentKey, pipelineKey)
		if err != nil {
			return releaseInternalError(err)
		}
		if head == nil {
			return releaseInternalError(errors.New("release head row missing after ensure"))
		}
		if head.Revision != expectedRevision {
			latest, err := s.activeInfoFromHead(gateCtx, txLogic, head)
			if err != nil {
				return err
			}
			return releaseActiveRevisionError(expectedRevision, latest)
		}
		if head.ActiveSnapshotID == target.SnapshotID {
			latest, err := s.activeInfoFromHead(gateCtx, txLogic, head)
			if err != nil {
				return err
			}
			return releaseActiveUnchangedError(latest)
		}
		// 目标版本完整性与资源可读校验
		resolvedStorage, err := s.snapshotStore(gateCtx, txLogic, target)
		if err != nil {
			return err
		}
		storage = resolvedStorage
		if err := s.verifyReleaseComplete(gateCtx, txLogic, storage, target); err != nil {
			return err
		}
		changedAt := time.Now().UTC()
		applied, err := txLogic.releaseDAO.ApplyHeadCAS(gateCtx, txLogic.db, environmentKey, pipelineKey, expectedRevision, target.SnapshotID, identity.Username, reason, changedAt)
		if err != nil {
			return releaseInternalError(err)
		}
		if !applied {
			latestHead, err := txLogic.releaseDAO.GetHead(gateCtx, txLogic.db, environmentKey, pipelineKey)
			if err != nil {
				return releaseInternalError(err)
			}
			latest, err := s.activeInfoFromHead(gateCtx, txLogic, latestHead)
			if err != nil {
				return err
			}
			return releaseActiveRevisionError(expectedRevision, latest)
		}
		event := &model.ConfigReleaseEvent{
			ScopeID:            pipelineRow.ScopeID,
			EventType:          model.ReleaseEventActivate,
			RequestID:          requestID,
			RequestFingerprint: fingerprint,
			EnvironmentKey:     environmentKey,
			PipelineKey:        pipelineKey,
			SnapshotID:         target.SnapshotID,
			Version:            target.Version,
			FromActiveSnapshot: head.ActiveSnapshotID,
			ToActiveSnapshot:   target.SnapshotID,
			FromHeadRevision:   head.Revision,
			ToHeadRevision:     expectedRevision + 1,
			Reason:             reason,
			Actor:              identity.Username,
			CreatedAt:          changedAt,
		}
		if err := txLogic.releaseDAO.AppendEvent(gateCtx, txLogic.db, event); err != nil {
			return releaseInternalError(err)
		}
		active := &ReleaseActiveInfo{
			ScopeID:       pipelineRow.ScopeID,
			SnapshotID:    target.SnapshotID,
			Version:       target.Version,
			Revision:      expectedRevision + 1,
			ChangedAt:     changedAt.Format(time.RFC3339),
			ChangedBy:     identity.Username,
			ChangedReason: reason,
		}
		result = &ReleaseActivateResult{
			RequestID:       requestID,
			Replayed:        false,
			AppliedRevision: expectedRevision + 1,
			Active:          active,
			Version:         version,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// --------------------- 只读查询 ---------------------

// ReleaseVersions 返回版本列表页(创建 ID 倒序)与独立的 active 状态。
func (s *Service) ReleaseVersions(ctx context.Context, environmentKey, pipelineKey string, limit int, beforeIDRaw string) (*ReleasePage, error) {
	if err := s.assertReleaseScopeExists(ctx, s.logic, environmentKey, pipelineKey); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = releaseListDefaultLimit
	}
	if limit > releaseListMaxLimit {
		limit = releaseListMaxLimit
	}
	beforeID, err := parseReleaseCursor(beforeIDRaw, "before_id")
	if err != nil {
		return nil, err
	}
	rows, hasMore, err := s.logic.releaseDAO.ListVersions(ctx, s.logic.db, environmentKey, pipelineKey, limit, beforeID)
	if err != nil {
		return nil, releaseInternalError(err)
	}
	active, err := s.loadActive(ctx, s.logic, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	page := &ReleasePage{Items: make([]ReleaseVersionListItem, 0, len(rows)), HasMore: hasMore, Active: active}
	for i := range rows {
		page.Items = append(page.Items, ReleaseVersionListItem{
			Meta:     releaseMetaFromVersion(&rows[i]),
			IsActive: active != nil && active.SnapshotID == rows[i].SnapshotID,
		})
	}
	if hasMore && len(rows) > 0 {
		page.NextBeforeID = rows[len(rows)-1].ID
	}
	return page, nil
}

// ReleaseDetail 返回版本元数据、快照配置与资源清单;受管引用渲染为本版本 URL。
func (s *Service) ReleaseDetail(ctx context.Context, environmentKey, pipelineKey, versionName string) (*releaseDetail, error) {
	versionName = strings.TrimSpace(versionName)
	if versionName == "" {
		return nil, releaseInvalidRequestError("version is required")
	}
	row, err := s.logic.releaseDAO.GetVersionByScopeAndName(ctx, s.logic.db, environmentKey, pipelineKey, versionName)
	if err != nil {
		if db.IsReleaseNotFound(err) {
			return nil, releaseVersionNotFoundError(environmentKey, pipelineKey, versionName)
		}
		return nil, releaseInternalError(err)
	}
	if row.DeletedAt != nil {
		return nil, releaseVersionDeletedError()
	}
	payload, err := decodeReleasePayload(row.Payload)
	if err != nil {
		return nil, releaseIncompleteError("release payload is not decodable")
	}
	configs := make([]releaseRuntimeConfig, 0, len(payload.Configs))
	for _, cfg := range payload.Configs {
		content, err := s.renderReleaseContent(row.SnapshotID, cfg.Content, cfg.Type)
		if err != nil {
			return nil, err
		}
		configs = append(configs, releaseRuntimeConfig{
			ResourceKey: cfg.ResourceKey, Alias: cfg.Alias, Name: cfg.Name, Type: cfg.Type,
			Content: content, Remark: cfg.Remark, IsPerm: cfg.IsPerm, Description: cfg.Description,
		})
	}
	assets, err := s.logic.releaseDAO.ListAssets(ctx, s.logic.db, row.SnapshotID)
	if err != nil {
		return nil, releaseInternalError(err)
	}
	active, err := s.loadActive(ctx, s.logic, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	detail := &releaseDetail{
		Meta:    releaseMetaFromVersion(row),
		Configs: configs,
		Assets:  make([]releaseAssetInfo, 0, len(assets)),
		Active:  active,
	}
	for _, asset := range assets {
		detail.Assets = append(detail.Assets, releaseAssetInfo{
			AssetID:     asset.AssetID,
			FileName:    asset.FileName,
			ContentType: asset.ContentType,
			Size:        asset.Size,
			Sha256:      asset.Sha256,
			URL:         fmt.Sprintf("%s/api/v1/release/asset/%s/%s", s.basePath, row.SnapshotID, asset.AssetID),
		})
	}
	return detail, nil
}

// releaseDetail 是版本详情。
type releaseDetail struct {
	Meta    ReleaseVersionMeta
	Configs []releaseRuntimeConfig
	Assets  []releaseAssetInfo
	Active  *ReleaseActiveInfo
}

// releaseAssetInfo 是详情内的资源清单项(不输出服务器路径)。
type releaseAssetInfo struct {
	AssetID     string
	FileName    string
	ContentType string
	Size        int64
	Sha256      string
	URL         string
}

// ReleaseActive 读取范围的生效指针;无行时返回 nil(未设置,revision 0)。
func (s *Service) ReleaseActive(ctx context.Context, environmentKey, pipelineKey string) (*ReleaseActiveInfo, error) {
	if err := s.assertReleaseScopeExists(ctx, s.logic, environmentKey, pipelineKey); err != nil {
		return nil, err
	}
	return s.loadActive(ctx, s.logic, environmentKey, pipelineKey)
}

// ReleaseHistoryPage 返回创建与切换审计页(事件 ID 倒序)。
func (s *Service) ReleaseHistoryPage(ctx context.Context, environmentKey, pipelineKey string, limit int, beforeEventIDRaw string) (*ReleaseHistoryPage, error) {
	if err := s.assertReleaseScopeExists(ctx, s.logic, environmentKey, pipelineKey); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = releaseListDefaultLimit
	}
	if limit > releaseListMaxLimit {
		limit = releaseListMaxLimit
	}
	beforeEventID, err := parseReleaseCursor(beforeEventIDRaw, "before_event_id")
	if err != nil {
		return nil, err
	}
	rows, hasMore, err := s.logic.releaseDAO.ListEvents(ctx, s.logic.db, environmentKey, pipelineKey, limit, beforeEventID)
	if err != nil {
		return nil, releaseInternalError(err)
	}
	page := &ReleaseHistoryPage{Items: make([]ReleaseEventItem, 0, len(rows)), HasMore: hasMore}
	for i := range rows {
		row := rows[i]
		page.Items = append(page.Items, ReleaseEventItem{
			EventID:      row.ID,
			EventType:    row.EventType,
			RequestID:    row.RequestID,
			SnapshotID:   row.SnapshotID,
			Version:      row.Version,
			FromActiveID: row.FromActiveSnapshot,
			ToActiveID:   row.ToActiveSnapshot,
			FromRevision: row.FromHeadRevision,
			ToRevision:   row.ToHeadRevision,
			Reason:       row.Reason,
			Actor:        row.Actor,
			CreatedAt:    row.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	if hasMore && len(rows) > 0 {
		page.NextBeforeEventID = rows[len(rows)-1].ID
	}
	return page, nil
}

// --------------------- 内部辅助 ---------------------

// loadActive 读取范围生效状态;未设置返回 nil;指针指向缺失版本 → 503。
func (s *Service) loadActive(ctx context.Context, l *Logic, environmentKey, pipelineKey string) (*ReleaseActiveInfo, error) {
	head, err := l.releaseDAO.GetHead(ctx, l.db, environmentKey, pipelineKey)
	if err != nil {
		return nil, releaseInternalError(err)
	}
	if head == nil || head.ActiveSnapshotID == "" {
		return nil, nil
	}
	info, err := s.activeInfoFromHead(ctx, l, head)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (s *Service) activeInfoFromHead(ctx context.Context, l *Logic, head *model.ConfigReleaseHead) (*ReleaseActiveInfo, error) {
	if head == nil || head.ActiveSnapshotID == "" {
		return nil, nil
	}
	versionRow, err := l.releaseDAO.GetVersionBySnapshotID(ctx, l.db, head.ActiveSnapshotID)
	if err != nil {
		if db.IsReleaseNotFound(err) {
			return nil, releaseIncompleteError("active snapshot row is missing")
		}
		return nil, releaseInternalError(err)
	}
	if versionRow.DeletedAt != nil {
		return nil, releaseIncompleteError("active snapshot has been deleted")
	}
	changedAt := ""
	if head.ChangedAt != nil {
		changedAt = head.ChangedAt.UTC().Format(time.RFC3339)
	}
	return &ReleaseActiveInfo{
		ScopeID:       head.ScopeID,
		SnapshotID:    versionRow.SnapshotID,
		Version:       versionRow.Version,
		Revision:      head.Revision,
		ChangedAt:     changedAt,
		ChangedBy:     head.ChangedBy,
		ChangedReason: head.ChangedReason,
	}, nil
}

func (s *Service) assertReleaseScopeExists(ctx context.Context, l *Logic, environmentKey, pipelineKey string) error {
	_, err := resolveScopeByKeys(ctx, l, environmentKey, pipelineKey)
	return err
}

// verifyReleaseComplete 校验版本 payload 摘要与全部资源清单行可读且 SHA 一致。
func (s *Service) verifyReleaseComplete(ctx context.Context, l *Logic, storage releaseAssetStore, row *model.ConfigReleaseVersion) error {
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
	var total int64
	for _, a := range assets {
		total += a.Size
	}
	if len(assets) != row.AssetCount || total != row.AssetBytes {
		return releaseIncompleteError("release asset totals mismatch")
	}
	payload, err := decodeReleasePayload(row.Payload)
	if err != nil || len(payload.Configs) != row.ConfigCount {
		return releaseIncompleteError("release payload count mismatch")
	}
	listed := make(map[string]bool, len(assets))
	for _, a := range assets {
		listed[a.AssetID] = true
	}
	for id := range referencedReleaseAssets(payload) {
		if !listed[id] {
			return releaseIncompleteError("release asset manifest row missing")
		}
	}
	for _, asset := range assets {
		if err := storage.verifyAssetFile(row.SnapshotID, asset.AssetID, asset.Size, asset.Sha256); err != nil {
			return err
		}
	}
	return nil
}

// decodeReleasePayload 无损解析快照 payload。
func decodeReleasePayload(payload string) (*releasePayload, error) {
	var parsed releasePayload
	if err := DecodeConfigJSON([]byte(payload), &parsed); err != nil {
		return nil, err
	}
	if parsed.Configs == nil {
		parsed.Configs = []releasePayloadConfig{}
	}
	return &parsed, nil
}

func releaseMetaFromVersion(row *model.ConfigReleaseVersion) ReleaseVersionMeta {
	external := []string{}
	if row.ExternalReferences != "" {
		_ = DecodeConfigJSON([]byte(row.ExternalReferences), &external)
	}
	sort.Strings(external)
	return ReleaseVersionMeta{
		ScopeID:              row.ScopeID,
		SnapshotID:           row.SnapshotID,
		EnvironmentKey:       row.EnvironmentKey,
		PipelineKey:          row.PipelineKey,
		Version:              row.Version,
		EnvironmentName:      row.EnvironmentName,
		PipelineName:         row.PipelineName,
		SourceFreezeRevision: row.SourceFreezeRevision,
		CreationRequestID:    row.CreationRequestID,
		ConfigCount:          row.ConfigCount,
		AssetCount:           row.AssetCount,
		AssetBytes:           row.AssetBytes,
		PayloadSha256:        row.PayloadSha256,
		ManifestSha256:       row.ManifestSha256,
		ExternalReferences:   external,
		Reason:               row.Reason,
		Actor:                row.Actor,
		CreatedAt:            row.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// validateReleaseTransitionInput 校验创建／切换请求的公共输入。
// allocateReleaseVersion ensures the per-scope head exists and claims N+1 in
// the caller's transaction. A rollback also rolls the counter back.
func allocateReleaseVersion(ctx context.Context, txLogic *Logic,
	environmentKey, pipelineKey, scopeID string) (int64, error) {
	if err := txLogic.releaseDAO.EnsureHeadByScope(ctx, txLogic.db, scopeID,
		environmentKey, pipelineKey); err != nil {
		return 0, releaseInternalError(err)
	}
	number, err := txLogic.releaseDAO.AllocateVersionNumber(ctx, txLogic.db, scopeID)
	switch {
	case err == nil:
		return number, nil
	case errors.Is(err, db.ErrReleaseNumberExhausted):
		return 0, releaseVersionExhaustedError()
	case errors.Is(err, db.ErrReleaseNumberConflict):
		return 0, releaseInternalError(err)
	case errors.Is(err, db.ErrReleaseHeadMissing):
		return 0, releaseInternalError(err)
	default:
		return 0, releaseInternalError(err)
	}
}

// validateReleaseCreateInput mirrors activation validation but permits an
// empty version, which means the server allocates one.
func validateReleaseCreateInput(environmentKey, pipelineKey, versionName, expectedRevisionRaw, requestID, reason string) (string, string, string, string, string, int64, error) {
	environmentKey = strings.TrimSpace(environmentKey)
	pipelineKey = strings.TrimSpace(pipelineKey)
	if environmentKey == "" || pipelineKey == "" {
		return "", "", "", "", "", 0, releaseInvalidRequestError("environment_key and pipeline_key are required")
	}
	if len(environmentKey) > 191 || len(pipelineKey) > 191 {
		return "", "", "", "", "", 0, releaseInvalidRequestError("scope keys must not exceed 191 characters")
	}
	versionName = strings.TrimSpace(versionName)
	if versionName != "" {
		if !releaseVersionRegexp.MatchString(versionName) {
			return "", "", "", "", "", 0, releaseInvalidRequestError("version must match ^[a-z0-9][a-z0-9._-]{0,63}$")
		}
		switch versionName {
		case releaseVersionReservedActive, releaseVersionReservedCurrent, releaseVersionReservedLatest:
			return "", "", "", "", "", 0, releaseInvalidRequestError(fmt.Sprintf("version %q is reserved", versionName))
		}
	}
	requestID = strings.TrimSpace(requestID)
	if _, err := uuid.Parse(requestID); err != nil {
		return "", "", "", "", "", 0, releaseInvalidRequestError("request_id must be a UUID")
	}
	reason, validReason := normalizeOptionalReason(reason)
	if !validReason {
		return "", "", "", "", "", 0, releaseInvalidRequestError("reason must not exceed 500 characters after trimming")
	}
	expectedRevision, err := parseReleaseRevision(expectedRevisionRaw)
	if err != nil {
		return "", "", "", "", "", 0, err
	}
	return environmentKey, pipelineKey, versionName, reason, requestID, expectedRevision, nil
}

func validateReleaseTransitionInput(environmentKey, pipelineKey, versionName, expectedRevisionRaw, requestID, reason string) (string, string, string, string, string, int64, error) {
	environmentKey = strings.TrimSpace(environmentKey)
	pipelineKey = strings.TrimSpace(pipelineKey)
	if environmentKey == "" || pipelineKey == "" {
		return "", "", "", "", "", 0, releaseInvalidRequestError("environment_key and pipeline_key are required")
	}
	if len(environmentKey) > 191 || len(pipelineKey) > 191 {
		return "", "", "", "", "", 0, releaseInvalidRequestError("scope keys must not exceed 191 characters")
	}
	versionName = strings.TrimSpace(versionName)
	if !releaseVersionRegexp.MatchString(versionName) {
		return "", "", "", "", "", 0, releaseInvalidRequestError("version must match ^[a-z0-9][a-z0-9._-]{0,63}$")
	}
	switch versionName {
	case releaseVersionReservedActive, releaseVersionReservedCurrent, releaseVersionReservedLatest:
		return "", "", "", "", "", 0, releaseInvalidRequestError(fmt.Sprintf("version %q is reserved", versionName))
	}
	requestID = strings.TrimSpace(requestID)
	if _, err := uuid.Parse(requestID); err != nil {
		return "", "", "", "", "", 0, releaseInvalidRequestError("request_id must be a UUID")
	}
	reason, validReason := normalizeOptionalReason(reason)
	if !validReason {
		return "", "", "", "", "", 0, releaseInvalidRequestError("reason must not exceed 500 characters after trimming")
	}
	expectedRevision, err := parseReleaseRevision(expectedRevisionRaw)
	if err != nil {
		return "", "", "", "", "", 0, err
	}
	return environmentKey, pipelineKey, versionName, reason, requestID, expectedRevision, nil
}

// parseReleaseRevision 解析非负 int64 十进制字符串。
func parseReleaseRevision(raw string) (int64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, releaseInvalidRequestError(fmt.Sprintf("revision must be a non-negative decimal string, got %q", raw))
	}
	return parsed, nil
}

// parseReleaseCursor 解析分页游标;缺省为空(首屏),显式非法值拒绝。
func parseReleaseCursor(raw, field string) (uint, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		return 0, releaseInvalidRequestError(fmt.Sprintf("%s must be a positive decimal string, got %q", field, raw))
	}
	return uint(parsed), nil
}

// releaseFingerprint 计算标准化请求指纹(不含令牌与字段排列顺序)。
// 每个字段以"长度:内容"编码,字段边界明确,避免 a|b + c 与 a + b|c 之类的跨字段碰撞。
func releaseFingerprint(operation string, parts ...string) string {
	hasher := sha256.New()
	fmt.Fprintf(hasher, "rainbow-release-v2|%d:%s", len(operation), operation)
	for _, part := range parts {
		fmt.Fprintf(hasher, "|%d:%s", len(part), part)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// marshalExternalReferences 以确定性方式序列化外部引用列表。
func marshalExternalReferences(references []string) (string, error) {
	if references == nil {
		references = []string{}
	}
	sorted := make([]string, len(references))
	copy(sorted, references)
	sort.Strings(sorted)
	encoded, err := jsonMarshal(sorted)
	return encoded, err
}

func jsonMarshal(value any) (string, error) {
	data, err := jsonContent(value)
	return data, err
}

// mapReleaseUniqueViolation 将唯一约束冲突映射为稳定的 409 语义。
func mapReleaseUniqueViolation(err error) error {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "creation_request_id"):
		return releaseRequestConflictError("request_id was already used")
	case strings.Contains(message, "uk_release_scope_version"):
		return newReleaseError(409, ErrTypeReleaseVersionExists, "version already exists in this scope")
	default:
		return releaseInternalError(err)
	}
}
