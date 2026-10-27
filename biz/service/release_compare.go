package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

const (
	releaseCompareDefaultTimeout = 60 * time.Second
	releaseCompareDefaultLimit   = 20
	releaseCompareMaxLimit       = 100

	releaseCompareModeActive = "active"
	releaseCompareModeFixed  = "fixed"

	releaseCompareSideBaseline = "baseline"
	releaseCompareSideTarget   = "target"
)

type releaseCompareSnapshot struct {
	version    string
	snapshotID string
	scopeID    string
	configs    map[string]releasePayloadConfig
	assets     map[string]model.ConfigReleaseAsset
}

type releaseCompareSelection struct {
	mode                     string
	environmentKey           string
	pipelineKey              string
	scopeID                  string
	versionName              string
	snapshotID               string
	baselineVersion          string
	baselineSnapshotID       string
	expectedRevision         int64
	expectedActiveSnapshotID string
	offset                   int
	limit                    int
}

// CompareRelease compares immutable snapshots. Active mode keeps the original
// "target vs current active" contract; fixed mode chooses both sides by version
// without consulting the head row.
func (s *Service) CompareRelease(ctx context.Context, input ReleaseCompareInput) (*ReleaseCompareResult, error) {
	selection, err := validateCompareInput(input)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, releaseCompareDefaultTimeout)
	defer cancel()

	tx, err := s.beginReadTransaction(ctx)
	if err != nil {
		return nil, releaseInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	l := s.logic.withTx(tx)

	pipeline, err := requireScopeIdentity(ctx, l, selection.environmentKey, selection.pipelineKey, selection.scopeID)
	if err != nil {
		return nil, err
	}
	if selection.mode == releaseCompareModeFixed {
		return s.compareFixedRelease(ctx, l, tx, pipeline, selection)
	}
	return s.compareActiveRelease(ctx, l, tx, pipeline, selection)
}

func (s *Service) compareActiveRelease(ctx context.Context, l *Logic, tx *gorm.DB,
	pipeline *model.Pipeline, selection releaseCompareSelection) (*ReleaseCompareResult, error) {
	head, err := l.releaseDAO.GetHeadByScopeID(ctx, l.db, pipeline.ScopeID)
	if err != nil {
		return nil, releaseInternalError(err)
	}
	headRevision := int64(0)
	activeSnapshotID := ""
	if head != nil {
		headRevision = head.Revision
		activeSnapshotID = head.ActiveSnapshotID
	}

	var baselineRow *model.ConfigReleaseVersion
	var latestActive *ReleaseActiveInfo
	if activeSnapshotID != "" {
		baselineRow, err = l.releaseDAO.GetVersionBySnapshotID(ctx, l.db, activeSnapshotID)
		if err != nil {
			if db.IsReleaseNotFound(err) {
				return nil, releaseIncompleteError("active snapshot row is missing")
			}
			return nil, releaseInternalError(err)
		}
		if baselineRow.DeletedAt != nil {
			return nil, releaseVersionDeletedError()
		}
		if baselineRow.ScopeID != pipeline.ScopeID {
			return nil, releaseIncompleteError("active snapshot belongs to another scope generation")
		}
		latestActive = compareActiveFromHead(head, baselineRow)
	}

	if headRevision != selection.expectedRevision {
		return nil, releaseActiveRevisionError(selection.expectedRevision, latestActive)
	}
	if activeSnapshotID != selection.expectedActiveSnapshotID {
		return nil, releaseCompareBaselineMismatchError(latestActive)
	}

	targetRow, err := l.releaseDAO.GetVersionByScopeIDAndName(ctx, l.db, pipeline.ScopeID, selection.versionName)
	if err != nil {
		if db.IsReleaseNotFound(err) {
			return nil, releaseVersionNotFoundError(selection.environmentKey, selection.pipelineKey, selection.versionName)
		}
		return nil, releaseInternalError(err)
	}
	if targetRow.DeletedAt != nil {
		return nil, releaseVersionDeletedError()
	}
	if targetRow.SnapshotID != selection.snapshotID {
		return nil, releaseSnapshotMismatchError(selection.environmentKey, selection.pipelineKey,
			selection.versionName, selection.snapshotID, targetRow.SnapshotID)
	}
	if targetRow.ScopeID != pipeline.ScopeID {
		return nil, releaseIncompleteError("target version belongs to another scope generation")
	}

	targetSnapshot, err := s.loadCompareSnapshot(ctx, l, targetRow)
	if err != nil {
		return nil, err
	}
	var baselineSnapshot *releaseCompareSnapshot
	if baselineRow != nil {
		if baselineRow.SnapshotID == targetRow.SnapshotID {
			baselineSnapshot = targetSnapshot
		} else {
			baselineSnapshot, err = s.loadCompareSnapshot(ctx, l, baselineRow)
			if err != nil {
				return nil, err
			}
		}
	}
	result, err := finishCompare(ctx, tx, targetSnapshot, baselineSnapshot, selection)
	if err != nil {
		return nil, err
	}
	result.ActiveRevision = headRevision
	result.TargetIsActive = activeSnapshotID != "" && activeSnapshotID == targetRow.SnapshotID
	return result, nil
}

func (s *Service) compareFixedRelease(ctx context.Context, l *Logic, tx *gorm.DB,
	pipeline *model.Pipeline, selection releaseCompareSelection) (*ReleaseCompareResult, error) {
	baselineRow, err := resolveFixedCompareVersion(ctx, l, pipeline.ScopeID, selection,
		releaseCompareSideBaseline, selection.baselineVersion, selection.baselineSnapshotID)
	if err != nil {
		return nil, err
	}
	targetRow, err := resolveFixedCompareVersion(ctx, l, pipeline.ScopeID, selection,
		releaseCompareSideTarget, selection.versionName, selection.snapshotID)
	if err != nil {
		return nil, err
	}
	baselineSnapshot, err := s.loadCompareSnapshot(ctx, l, baselineRow)
	if err != nil {
		return nil, withCompareSide(err, releaseCompareSideBaseline)
	}
	targetSnapshot, err := s.loadCompareSnapshot(ctx, l, targetRow)
	if err != nil {
		return nil, withCompareSide(err, releaseCompareSideTarget)
	}
	if baselineRow.SnapshotID == targetRow.SnapshotID {
		targetSnapshot = baselineSnapshot
	}
	return finishCompare(ctx, tx, targetSnapshot, baselineSnapshot, selection)
}

func resolveFixedCompareVersion(ctx context.Context, l *Logic, scopeID string,
	selection releaseCompareSelection, side, versionName, snapshotID string) (*model.ConfigReleaseVersion, error) {
	row, err := l.releaseDAO.GetVersionByScopeIDAndName(ctx, l.db, scopeID, versionName)
	if err != nil {
		if db.IsReleaseNotFound(err) {
			return nil, withCompareSide(releaseVersionNotFoundError(
				selection.environmentKey, selection.pipelineKey, versionName), side)
		}
		return nil, releaseInternalError(err)
	}
	if row.DeletedAt != nil {
		return nil, withCompareSide(releaseVersionDeletedError(), side)
	}
	if row.ScopeID != scopeID {
		return nil, withCompareSide(releaseIncompleteError("version belongs to another scope generation"), side)
	}
	if row.SnapshotID != snapshotID {
		return nil, withCompareSide(releaseSnapshotMismatchError(selection.environmentKey, selection.pipelineKey,
			versionName, snapshotID, row.SnapshotID), side)
	}
	return row, nil
}

func finishCompare(ctx context.Context, tx *gorm.DB, target, baseline *releaseCompareSnapshot,
	selection releaseCompareSelection) (*ReleaseCompareResult, error) {
	if rollback := tx.Rollback(); rollback.Error != nil && !errors.Is(rollback.Error, sql.ErrTxDone) {
		return nil, releaseInternalError(rollback.Error)
	}
	nonce, err := newCompareNonce()
	if err != nil {
		return nil, releaseCompareTimeoutError()
	}
	result, err := compareSnapshots(ctx, target, baseline, nonce)
	if err != nil {
		return nil, err
	}
	result.ScopeID = selection.scopeID
	result.ComparisonMode = selection.mode
	return paginateCompareResult(result, selection.offset, selection.limit), nil
}

func withCompareSide(err error, side string) error {
	if releaseErr, ok := AsReleaseError(err); ok {
		releaseErr.Side = side
	}
	return err
}

func (s *Service) beginReadTransaction(ctx context.Context) (*gorm.DB, error) {
	tx := s.logic.db.WithContext(ctx).Begin(&sql.TxOptions{ReadOnly: true})
	if tx.Error == nil {
		return tx, nil
	}
	// Some SQLite drivers ignore/decline read-only flags; a normal transaction
	// still provides the required consistent snapshot.
	tx = s.logic.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	return tx, nil
}

func validateCompareInput(input ReleaseCompareInput) (releaseCompareSelection, error) {
	selection := releaseCompareSelection{
		environmentKey: strings.TrimSpace(input.EnvironmentKey),
		pipelineKey:    strings.TrimSpace(input.PipelineKey),
		mode:           strings.ToLower(strings.TrimSpace(input.ComparisonMode)),
		offset:         input.Offset,
		limit:          input.Limit,
	}
	if selection.environmentKey == "" || selection.pipelineKey == "" {
		return selection, releaseInvalidRequestError("environment_key and pipeline_key are required")
	}
	if selection.mode == "" {
		selection.mode = releaseCompareModeActive
	}
	if selection.mode != releaseCompareModeActive && selection.mode != releaseCompareModeFixed {
		return selection, releaseInvalidRequestError("comparison_mode must be active or fixed")
	}
	selection.scopeID = strings.TrimSpace(input.ExpectedScopeID)
	if _, err := uuid.Parse(selection.scopeID); err != nil {
		return selection, releaseInvalidRequestError("expected_scope_id must be a UUID")
	}
	versionName, err := validateCompareVersionName(input.Version)
	if err != nil {
		return selection, err
	}
	selection.versionName = versionName
	selection.snapshotID = strings.TrimSpace(input.ExpectedSnapshotID)
	if _, err := uuid.Parse(selection.snapshotID); err != nil {
		return selection, releaseInvalidRequestError("expected_snapshot_id must be a UUID")
	}
	if selection.mode == releaseCompareModeFixed {
		if strings.TrimSpace(input.ExpectedActiveRevision) != "" ||
			strings.TrimSpace(input.ExpectedActiveSnapshotID) != "" {
			return selection, releaseInvalidRequestError("expected_active_revision and expected_active_snapshot_id are only valid in active mode")
		}
		if strings.TrimSpace(input.BaselineVersion) == "" {
			return selection, releaseInvalidRequestError("baseline_version is required in fixed mode")
		}
		baselineVersion, err := validateCompareVersionName(input.BaselineVersion)
		if err != nil {
			return selection, err
		}
		selection.baselineVersion = baselineVersion
		selection.baselineSnapshotID = strings.TrimSpace(input.ExpectedBaselineSnapshotID)
		if _, err := uuid.Parse(selection.baselineSnapshotID); err != nil {
			return selection, releaseInvalidRequestError("expected_baseline_snapshot_id must be a UUID")
		}
	} else {
		if strings.TrimSpace(input.BaselineVersion) != "" ||
			strings.TrimSpace(input.ExpectedBaselineSnapshotID) != "" {
			return selection, releaseInvalidRequestError("baseline_version and expected_baseline_snapshot_id are only valid in fixed mode")
		}
		revisionRaw := strings.TrimSpace(input.ExpectedActiveRevision)
		if revisionRaw == "" {
			return selection, releaseInvalidRequestError("expected_active_revision is required")
		}
		expectedRevision, err := strconv.ParseInt(revisionRaw, 10, 64)
		if err != nil || expectedRevision < 0 {
			return selection, releaseInvalidRequestError("expected_active_revision must be a non-negative decimal string")
		}
		selection.expectedRevision = expectedRevision
		selection.expectedActiveSnapshotID = strings.TrimSpace(input.ExpectedActiveSnapshotID)
		if selection.expectedActiveSnapshotID != "" {
			if _, err := uuid.Parse(selection.expectedActiveSnapshotID); err != nil {
				return selection, releaseInvalidRequestError("expected_active_snapshot_id must be a UUID or empty")
			}
		}
	}
	if selection.offset < 0 {
		return selection, releaseInvalidRequestError("offset must be non-negative")
	}
	if selection.limit < 0 || selection.limit > releaseCompareMaxLimit {
		return selection, releaseInvalidRequestError("limit must be between 0 and 100")
	}
	if selection.limit == 0 {
		selection.limit = releaseCompareDefaultLimit
	}
	return selection, nil
}

func validateCompareVersionName(raw string) (string, error) {
	versionName := strings.TrimSpace(raw)
	if !releaseVersionRegexp.MatchString(versionName) {
		return "", releaseInvalidRequestError("version must match ^[a-z0-9][a-z0-9._-]{0,63}$")
	}
	switch versionName {
	case releaseVersionReservedActive, releaseVersionReservedCurrent, releaseVersionReservedLatest:
		return "", releaseInvalidRequestError(fmt.Sprintf("version %q is reserved", versionName))
	}
	return versionName, nil
}

func compareActiveFromHead(head *model.ConfigReleaseHead, row *model.ConfigReleaseVersion) *ReleaseActiveInfo {
	if head == nil || row == nil {
		return nil
	}
	changedAt := ""
	if head.ChangedAt != nil {
		changedAt = head.ChangedAt.UTC().Format(time.RFC3339)
	}
	return &ReleaseActiveInfo{
		ScopeID: row.ScopeID, SnapshotID: row.SnapshotID, Version: row.Version,
		Revision: head.Revision, ChangedAt: changedAt,
		ChangedBy: head.ChangedBy, ChangedReason: head.ChangedReason,
	}
}

func (s *Service) loadCompareSnapshot(ctx context.Context, l *Logic,
	row *model.ConfigReleaseVersion) (*releaseCompareSnapshot, error) {
	if payloadDigest(row.Payload) != row.PayloadSha256 {
		return nil, releaseIncompleteError("release payload digest mismatch")
	}
	assets, err := l.releaseDAO.ListAssets(ctx, l.db, row.SnapshotID)
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
	configs := make(map[string]releasePayloadConfig, len(payload.Configs))
	for _, cfg := range payload.Configs {
		if cfg.ResourceKey == "" {
			return nil, releaseIncompleteError("release config is missing resource_key")
		}
		if _, exists := configs[cfg.ResourceKey]; exists {
			return nil, releaseIncompleteError("release payload has duplicate resource_key")
		}
		configs[cfg.ResourceKey] = cfg
	}
	assetMap := make(map[string]model.ConfigReleaseAsset, len(assets))
	for _, asset := range assets {
		if _, exists := assetMap[asset.AssetID]; exists {
			return nil, releaseIncompleteError("release manifest has duplicate asset id")
		}
		assetMap[asset.AssetID] = asset
	}
	for assetID := range referencedReleaseAssets(payload) {
		if _, ok := assetMap[assetID]; !ok {
			return nil, releaseIncompleteError("release asset manifest row is missing")
		}
	}
	return &releaseCompareSnapshot{
		version: row.Version, snapshotID: row.SnapshotID, scopeID: row.ScopeID,
		configs: configs, assets: assetMap,
	}, nil
}

func compareSnapshots(ctx context.Context, target, baseline *releaseCompareSnapshot,
	nonce string) (*ReleaseCompareResult, error) {
	result := &ReleaseCompareResult{
		Target: releaseCompareVersion{Version: target.version, SnapshotID: target.snapshotID},
		Items:  make([]ReleaseCompareItem, 0),
	}
	if baseline != nil {
		result.Baseline = &releaseCompareVersion{
			Version: baseline.version, SnapshotID: baseline.snapshotID,
		}
	}
	changes := make([]ReleaseCompareItem, 0)
	for key, after := range target.configs {
		if err := ctx.Err(); err != nil {
			return nil, releaseCompareTimeoutError()
		}
		before, exists := releasePayloadConfig{}, false
		if baseline != nil {
			before, exists = baseline.configs[key]
		}
		if !exists {
			result.Summary.Added++
			changes = append(changes, ReleaseCompareItem{
				ResourceKey: key, ChangeType: "added", ChangedFields: []string{},
				After: compareSideFromConfig(after),
			})
			continue
		}
		fields, err := changedConfigFields(before, after, baseline.assets, target.assets, nonce)
		if err != nil {
			return nil, err
		}
		if len(fields) == 0 {
			result.Summary.Unchanged++
			continue
		}
		result.Summary.Modified++
		changes = append(changes, ReleaseCompareItem{
			ResourceKey: key, ChangeType: "modified", ChangedFields: fields,
			Before: compareSideFromConfig(before), After: compareSideFromConfig(after),
		})
	}
	if baseline != nil {
		for key, before := range baseline.configs {
			if _, exists := target.configs[key]; exists {
				continue
			}
			result.Summary.Removed++
			changes = append(changes, ReleaseCompareItem{
				ResourceKey: key, ChangeType: "removed", ChangedFields: []string{},
				Before: compareSideFromConfig(before),
			})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].ResourceKey < changes[j].ResourceKey })
	result.Items = changes
	result.TotalChanges = len(changes)
	return result, nil
}

func compareSideFromConfig(cfg releasePayloadConfig) *ReleaseCompareSide {
	return &ReleaseCompareSide{Alias: cfg.Alias, Name: cfg.Name, Type: cfg.Type}
}

func changedConfigFields(before, after releasePayloadConfig,
	beforeAssets, afterAssets map[string]model.ConfigReleaseAsset,
	nonce string) ([]string, error) {
	fields := make([]string, 0, 4)
	if before.Alias != after.Alias {
		fields = append(fields, "alias")
	}
	if before.Name != after.Name {
		fields = append(fields, "name")
	}
	if before.Type != after.Type {
		fields = append(fields, "type")
	}
	beforeContent, err := canonicalReleaseContent(before.Content, before.Type, beforeAssets, nonce)
	if err != nil {
		return nil, err
	}
	afterContent, err := canonicalReleaseContent(after.Content, after.Type, afterAssets, nonce)
	if err != nil {
		return nil, err
	}
	if beforeContent != afterContent {
		fields = append(fields, "content")
	}
	if before.Remark != after.Remark {
		fields = append(fields, "remark")
	}
	if before.IsPerm != after.IsPerm {
		fields = append(fields, "is_perm")
	}
	if before.Description != after.Description {
		fields = append(fields, "description")
	}
	return fields, nil
}

func paginateCompareResult(result *ReleaseCompareResult, offset, limit int) *ReleaseCompareResult {
	result.Offset = offset
	result.Limit = limit
	result.Items = append([]ReleaseCompareItem{}, result.Items...)
	if offset >= len(result.Items) {
		result.Items = []ReleaseCompareItem{}
		result.HasMore = false
		result.NextOffset = nil
		return result
	}
	end := offset + limit
	if end > len(result.Items) {
		end = len(result.Items)
	}
	result.Items = result.Items[offset:end]
	result.HasMore = end < result.TotalChanges
	if result.HasMore {
		next := end
		result.NextOffset = &next
	}
	return result
}
