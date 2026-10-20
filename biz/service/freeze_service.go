package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

var externalURLRegexp = regexp.MustCompile(`https?://[^\s"',\\]+`)

// --------------------- 状态查询 ---------------------

// FreezeStatus 返回范围的当前冻结状态与管理功能可用性。
func (s *Service) FreezeStatus(ctx context.Context, environmentKey, pipelineKey string) (*FreezeStatusData, error) {
	environmentKey = strings.TrimSpace(environmentKey)
	pipelineKey = strings.TrimSpace(pipelineKey)
	if environmentKey == "" || pipelineKey == "" {
		return nil, freezeInvalidRequestError("environment_key and pipeline_key are required")
	}
	if err := s.assertFreezeScopeExists(ctx, s.logic, environmentKey, pipelineKey); err != nil {
		return nil, err
	}
	state, err := s.logic.freezeStateOrUnlocked(ctx, environmentKey, pipelineKey)
	if err != nil {
		return nil, freezeInternalError(err)
	}
	return freezeStatusFromState(state, s.adminAccountAvailable(ctx)), nil
}

// FreezeHistory 返回按事件 ID 倒序的审计历史(不返回 token、配置内容或服务器路径)。
func (s *Service) FreezeHistory(ctx context.Context, environmentKey, pipelineKey string, limit int, beforeEventIDRaw string) (*FreezeHistoryData, error) {
	environmentKey = strings.TrimSpace(environmentKey)
	pipelineKey = strings.TrimSpace(pipelineKey)
	if environmentKey == "" || pipelineKey == "" {
		return nil, freezeInvalidRequestError("environment_key and pipeline_key are required")
	}
	if err := s.assertFreezeScopeExists(ctx, s.logic, environmentKey, pipelineKey); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = freezeHistoryDefaultLimit
	}
	if limit > freezeHistoryMaxLimit {
		limit = freezeHistoryMaxLimit
	}
	beforeEventID, err := parseBeforeEventID(beforeEventIDRaw)
	if err != nil {
		return nil, err
	}
	events, hasMore, err := s.logic.freezeDAO.ListEvents(ctx, s.logic.db, environmentKey, pipelineKey, limit, beforeEventID)
	if err != nil {
		return nil, freezeInternalError(err)
	}
	items := make([]FreezeEventData, 0, len(events))
	for _, event := range events {
		items = append(items, FreezeEventData{
			EventID:   event.ID,
			EventType: event.EventType,
			FromState: event.FromState,
			ToState:   event.ToState,
			Revision:  event.Revision,
			Reason:    event.Reason,
			Actor:     event.Actor,
			CreatedAt: event.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	history := &FreezeHistoryData{Items: items, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		history.NextBeforeEventID = items[len(items)-1].EventID
	}
	return history, nil
}

// --------------------- 锁定 / 解锁 ---------------------

// LockFreeze 在管理凭据验证通过后锁定范围;锁定前执行受管资源检查。
func (s *Service) LockFreeze(ctx context.Context, environmentKey, pipelineKey, reason, expectedRevisionRaw string) (*FreezeStatusData, error) {
	return s.applyFreezeTransition(ctx, freezeTransitionInput{
		environmentKey:      environmentKey,
		pipelineKey:         pipelineKey,
		reason:              reason,
		expectedRevisionRaw: expectedRevisionRaw,
		desiredState:        model.FreezeStateLocked,
	})
}

// UnlockFreeze 在可信管理员身份验证通过后解除冻结。
func (s *Service) UnlockFreeze(ctx context.Context, environmentKey, pipelineKey, reason, expectedRevisionRaw string) (*FreezeStatusData, error) {
	return s.applyFreezeTransition(ctx, freezeTransitionInput{
		environmentKey:      environmentKey,
		pipelineKey:         pipelineKey,
		reason:              reason,
		expectedRevisionRaw: expectedRevisionRaw,
		desiredState:        model.FreezeStateUnlocked,
	})
}

type freezeTransitionInput struct {
	environmentKey      string
	pipelineKey         string
	reason              string
	expectedRevisionRaw string
	desiredState        string
}

// applyFreezeTransition 是锁定与解锁共用的状态机:
// 输入校验 → 凭据校验 → 取得写协调 → 范围存在性 → revision 条件更新 → 追加事件,同一事务提交。
func (s *Service) applyFreezeTransition(ctx context.Context, input freezeTransitionInput) (*FreezeStatusData, error) {
	environmentKey := strings.TrimSpace(input.environmentKey)
	pipelineKey := strings.TrimSpace(input.pipelineKey)
	if environmentKey == "" || pipelineKey == "" {
		return nil, freezeInvalidRequestError("environment_key and pipeline_key are required")
	}
	reason := strings.TrimSpace(input.reason)
	if len(reason) == 0 || len(reason) > freezeReasonMax {
		return nil, freezeInvalidReasonError()
	}
	expectedRevision, err := parseExpectedRevision(input.expectedRevisionRaw)
	if err != nil {
		return nil, err
	}
	// 凭据校验先于写协调,避免未授权请求占用协调
	identity, err := requireAdminIdentity(ctx)
	if err != nil {
		return nil, err
	}

	var result *FreezeStatusData
	err = s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		if err := s.assertFreezeScopeExists(gateCtx, txLogic, environmentKey, pipelineKey); err != nil {
			return err
		}
		if err := txLogic.freezeDAO.EnsureState(gateCtx, txLogic.db, environmentKey, pipelineKey); err != nil {
			return freezeInternalError(err)
		}
		current, err := txLogic.freezeDAO.GetState(gateCtx, txLogic.db, environmentKey, pipelineKey)
		if err != nil {
			return freezeInternalError(err)
		}
		if current.State == input.desiredState {
			return freezeConflictError(ErrTypeFreezeStateUnchanged,
				fmt.Sprintf("scope %s/%s is already %s", environmentKey, pipelineKey, input.desiredState),
				current.State, current.Revision)
		}
		if current.Revision != expectedRevision {
			return freezeConflictError(ErrTypeFreezeRevisionConflict,
				fmt.Sprintf("expected_revision %d does not match current revision %d", expectedRevision, current.Revision),
				current.State, current.Revision)
		}
		if input.desiredState == model.FreezeStateLocked {
			precheck, err := s.precheckLockResources(gateCtx, txLogic, environmentKey, pipelineKey)
			if err != nil {
				if _, ok := AsFreezeError(err); ok {
					return err
				}
				return freezeInternalError(err)
			}
			if len(precheck.Issues) > 0 {
				return freezePrecheckError(precheck)
			}
		}
		changedAt := time.Now().UTC()
		applied, err := txLogic.freezeDAO.ApplyTransition(gateCtx, txLogic.db, environmentKey, pipelineKey, expectedRevision, input.desiredState, identity.Username, reason, changedAt)
		if err != nil {
			return freezeInternalError(err)
		}
		if !applied {
			latest, readErr := txLogic.freezeDAO.GetState(gateCtx, txLogic.db, environmentKey, pipelineKey)
			if readErr != nil {
				return freezeInternalError(readErr)
			}
			if latest.State == input.desiredState {
				return freezeConflictError(ErrTypeFreezeStateUnchanged,
					fmt.Sprintf("scope %s/%s is already %s", environmentKey, pipelineKey, input.desiredState),
					latest.State, latest.Revision)
			}
			return freezeConflictError(ErrTypeFreezeRevisionConflict,
				fmt.Sprintf("expected_revision %d does not match current revision %d", expectedRevision, latest.Revision),
				latest.State, latest.Revision)
		}
		event := &model.ConfigFreezeEvent{
			EnvironmentKey: environmentKey,
			PipelineKey:    pipelineKey,
			EventType:      freezeEventType(input.desiredState),
			FromState:      current.State,
			ToState:        input.desiredState,
			Revision:       expectedRevision + 1,
			Reason:         reason,
			Actor:          identity.Username,
			CreatedAt:      changedAt,
		}
		if err := txLogic.freezeDAO.AppendEvent(gateCtx, txLogic.db, event); err != nil {
			return freezeInternalError(err)
		}
		// 本次迁移已通过可信身份,管理账号必然存在
		result = freezeStatusFromState(model.ConfigFreezeState{
			EnvironmentKey: environmentKey,
			PipelineKey:    pipelineKey,
			State:          input.desiredState,
			Revision:       expectedRevision + 1,
			LastChangedAt:  &changedAt,
			LastChangedBy:  identity.Username,
			LastReason:     reason,
		}, true)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// --------------------- 锁定前资源检查 ---------------------

// precheckLockResources 列出本范围受管理资源引用并逐项检查:
// 元数据存在、归属范围一致、文件可读取。使用 content 原文与无损 JSON 方法,不改写任何内容。
// 外部 URL 不下载,仅登记提示。
func (s *Service) precheckLockResources(ctx context.Context, l *Logic, environmentKey, pipelineKey string) (FreezeLockPrecheck, error) {
	precheck := FreezeLockPrecheck{Issues: make([]FreezeResourceIssue, 0), ExternalReferences: make([]string, 0)}
	configs, err := l.configDAO.ListByEnvironmentAndPipelineWithFilter(ctx, l.db, environmentKey, pipelineKey, "", "", "", 0, 0)
	if err != nil {
		return precheck, err
	}
	externalSeen := map[string]bool{}
	for i := range configs {
		cfg := configs[i]
		issue := func(fileID, reason string) {
			precheck.Issues = append(precheck.Issues, FreezeResourceIssue{
				ResourceKey: cfg.ResourceKey,
				Alias:       cfg.Alias,
				FileID:      fileID,
				Reason:      reason,
			})
		}
		refs := extractAssetIDsFromContent(cfg.Content)
		if len(refs) == 0 && strings.Contains(cfg.Content, assetScheme) {
			// 出现 asset:// 但无法无损解析出 file_id:无法定位的资源引用
			issue("", "unresolvable_reference")
		}
		for _, fileID := range refs {
			asset, err := l.GetAsset(ctx, fileID)
			if err != nil {
				if errors.Is(err, ErrAssetNotFound) {
					issue(fileID, "missing_metadata")
					continue
				}
				return precheck, err
			}
			if asset.EnvironmentKey != environmentKey || asset.PipelineKey != pipelineKey {
				issue(fileID, "cross_scope_reference")
				continue
			}
			reason, err := s.managedResourceReadability(ctx, asset)
			if err != nil {
				return precheck, err
			}
			if reason != "" {
				issue(fileID, reason)
			}
		}
		for _, match := range externalURLRegexp.FindAllString(cfg.Content, -1) {
			value := strings.TrimRight(match, `\/`)
			if !externalSeen[value] {
				externalSeen[value] = true
				precheck.ExternalReferences = append(precheck.ExternalReferences, value)
			}
		}
	}
	return precheck, nil
}

// managedResourceReadability 检查受管资源的文件可读性;返回空串表示可读。
func (s *Service) managedResourceReadability(ctx context.Context, asset *model.Asset) (string, error) {
	if strings.TrimSpace(asset.Path) == "" {
		return "unresolvable_reference", nil
	}
	if s.config != nil && s.config.Storage.Type == "minio" {
		exists, err := s.minioObjectExists(ctx, asset.Path)
		if err != nil {
			return "", err
		}
		if !exists {
			return "file_missing", nil
		}
		return "", nil
	}
	fullPath := filepath.Join(dataDirectory, asset.Path)
	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			return "file_missing", nil
		}
		return "", err
	}
	return "", nil
}

// --------------------- 启动初始化 ---------------------

// EnsureFreezeBootstrap 在 HTTP 开放前完成:创建固定协调行,并为全部存量渠道补建初始
// unlocked 状态行。幂等:重复初始化不重置既有状态、revision 或审计记录。
func (s *Service) EnsureFreezeBootstrap(ctx context.Context) error {
	if err := s.logic.writeGateDAO.EnsureGate(ctx, s.logic.db); err != nil {
		return err
	}
	var pipelines []model.Pipeline
	if err := s.logic.db.WithContext(ctx).Find(&pipelines).Error; err != nil {
		return err
	}
	for _, pipeline := range pipelines {
		if err := s.logic.freezeDAO.EnsureState(ctx, s.logic.db, pipeline.EnvironmentKey, pipeline.PipelineKey); err != nil {
			return err
		}
	}
	return nil
}

// lockedScopeSet 返回当前全部 locked 范围集合(用于启动迁移等非事务场景的跳过判断)。
func (s *Service) lockedScopeSet(ctx context.Context) (map[FreezeScopeRef]bool, error) {
	var states []model.ConfigFreezeState
	if err := s.logic.db.WithContext(ctx).Where("state = ?", model.FreezeStateLocked).Find(&states).Error; err != nil {
		return nil, err
	}
	set := make(map[FreezeScopeRef]bool, len(states))
	for _, state := range states {
		set[FreezeScopeRef{EnvironmentKey: state.EnvironmentKey, PipelineKey: state.PipelineKey}] = true
	}
	return set, nil
}

// --------------------- 内部辅助 ---------------------

func (s *Service) assertFreezeScopeExists(ctx context.Context, l *Logic, environmentKey, pipelineKey string) error {
	if _, err := l.environmentDAO.GetByKey(ctx, l.db, environmentKey); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return freezeScopeNotFoundError(environmentKey, pipelineKey)
		}
		return freezeInternalError(err)
	}
	if _, err := l.pipelineDAO.GetByKey(ctx, l.db, environmentKey, pipelineKey); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return freezeScopeNotFoundError(environmentKey, pipelineKey)
		}
		return freezeInternalError(err)
	}
	return nil
}

func freezeStatusFromState(state model.ConfigFreezeState, adminAvailable bool) *FreezeStatusData {
	lastChangedAt := ""
	if state.LastChangedAt != nil {
		lastChangedAt = state.LastChangedAt.UTC().Format(time.RFC3339)
	}
	return &FreezeStatusData{
		EnvironmentKey:  state.EnvironmentKey,
		PipelineKey:     state.PipelineKey,
		State:           state.State,
		Revision:        state.Revision,
		LastChangedAt:   lastChangedAt,
		LastChangedBy:   state.LastChangedBy,
		LastReason:      state.LastReason,
		AdminConfigured: adminAvailable,
	}
}

func freezeEventType(desiredState string) string {
	if desiredState == model.FreezeStateLocked {
		return FreezeEventLock
	}
	return FreezeEventUnlock
}

func parseExpectedRevision(raw string) (int64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, nil
	}
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision < 0 {
		return 0, freezeInvalidRevisionError(raw)
	}
	return revision, nil
}

func parseBeforeEventID(raw string) (uint, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, freezeInvalidRequestError(fmt.Sprintf("before_event_id must be a non-negative decimal string, got %q", raw))
	}
	return uint(parsed), nil
}
