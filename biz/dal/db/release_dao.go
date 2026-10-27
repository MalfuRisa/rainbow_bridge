package db

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrReleaseNumberExhausted means the scope has consumed the full int64
	// sequence and must not wrap around.
	ErrReleaseNumberExhausted = errors.New("release version number sequence exhausted")
	// ErrReleaseHeadMissing means allocation ran before the per-scope head row
	// was created.
	ErrReleaseHeadMissing = errors.New("release head row is missing")
	// ErrReleaseNumberConflict means a concurrent writer changed the counter
	// despite the write gate.
	ErrReleaseNumberConflict = errors.New("release version counter changed concurrently")
)

// ReleaseDAO reads and writes release rows. All scope-addressed methods resolve
// the current generation unless a scope_id is supplied explicitly.
type ReleaseDAO struct{}

func NewReleaseDAO() *ReleaseDAO { return &ReleaseDAO{} }

// --------------------- 版本 ---------------------

func (dao *ReleaseDAO) CreateVersion(ctx context.Context, db *gorm.DB, version *model.ConfigReleaseVersion) error {
	if version.ScopeID == "" {
		scopeID, err := ActiveScopeID(ctx, db, version.EnvironmentKey, version.PipelineKey)
		if err != nil {
			return err
		}
		version.ScopeID = scopeID
	}
	return db.WithContext(ctx).Create(version).Error
}

func (dao *ReleaseDAO) GetVersionBySnapshotID(ctx context.Context, db *gorm.DB, snapshotID string) (*model.ConfigReleaseVersion, error) {
	var version model.ConfigReleaseVersion
	if err := db.WithContext(ctx).Where("snapshot_id = ?", snapshotID).First(&version).Error; err != nil {
		return nil, err
	}
	return &version, nil
}

func (dao *ReleaseDAO) GetVersionByScopeAndName(ctx context.Context, db *gorm.DB,
	environmentKey, pipelineKey, version string) (*model.ConfigReleaseVersion, error) {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	return dao.GetVersionByScopeIDAndName(ctx, db, scopeID, version)
}

func (dao *ReleaseDAO) GetVersionByScopeIDAndName(ctx context.Context, db *gorm.DB,
	scopeID, version string) (*model.ConfigReleaseVersion, error) {
	var row model.ConfigReleaseVersion
	err := db.WithContext(ctx).
		Where("scope_id = ? AND version = ?", scopeID, version).
		First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (dao *ReleaseDAO) GetVersionByCreationRequestID(ctx context.Context, db *gorm.DB, requestID string) (*model.ConfigReleaseVersion, error) {
	var row model.ConfigReleaseVersion
	err := db.WithContext(ctx).Where("creation_request_id = ?", requestID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (dao *ReleaseDAO) ListVersions(ctx context.Context, db *gorm.DB,
	environmentKey, pipelineKey string, limit int, beforeID uint) ([]model.ConfigReleaseVersion, bool, error) {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return nil, false, err
	}
	return dao.ListVersionsByScopeID(ctx, db, scopeID, limit, beforeID)
}

func (dao *ReleaseDAO) ListVersionsByScopeID(ctx context.Context, db *gorm.DB,
	scopeID string, limit int, beforeID uint) ([]model.ConfigReleaseVersion, bool, error) {
	query := db.WithContext(ctx).
		Where("scope_id = ? AND deleted_at IS NULL", scopeID)
	if beforeID > 0 {
		query = query.Where("id < ?", beforeID)
	}
	var rows []model.ConfigReleaseVersion
	if err := query.Order("id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return rows, hasMore, nil
}

// CountVersionsByScope counts all versions (including deleted) in the current
// scope. Exact version-name reuse within one scope remains forbidden.
func (dao *ReleaseDAO) CountVersionsByScope(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string) (int64, error) {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return 0, err
	}
	return dao.CountVersionsByScopeID(ctx, db, scopeID)
}

func (dao *ReleaseDAO) CountVersionsByScopeID(ctx context.Context, db *gorm.DB, scopeID string) (int64, error) {
	var count int64
	err := db.WithContext(ctx).Model(&model.ConfigReleaseVersion{}).
		Where("scope_id = ?", scopeID).Count(&count).Error
	return count, err
}

func (dao *ReleaseDAO) CountVersionsByEnvironment(ctx context.Context, db *gorm.DB, environmentKey string) (int64, error) {
	env, err := ActiveEnvironment(ctx, db, environmentKey)
	if err != nil {
		return 0, err
	}
	var count int64
	err = db.WithContext(ctx).Model(&model.ConfigReleaseVersion{}).
		Where("scope_id IN (?)", db.Model(&model.Pipeline{}).
			Select("scope_id").Where("environment_id = ?", env.EnvironmentID)).
		Count(&count).Error
	return count, err
}

func (dao *ReleaseDAO) ListVersionsByEnvironment(ctx context.Context, db *gorm.DB, environmentKey string) ([]model.ConfigReleaseVersion, error) {
	env, err := ActiveEnvironment(ctx, db, environmentKey)
	if err != nil {
		return nil, err
	}
	var rows []model.ConfigReleaseVersion
	if err := db.WithContext(ctx).
		Where("scope_id IN (?)", db.Model(&model.Pipeline{}).
			Select("scope_id").Where("environment_id = ?", env.EnvironmentID)).
		Order("id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// SoftDeleteVersionsByScope marks all undeleted versions of a deleted scope
// while preserving the original deleted_at/actor/reason of earlier deletions.
func (dao *ReleaseDAO) SoftDeleteVersionsByScope(ctx context.Context, db *gorm.DB,
	scopeID, actor, reason string, deletedAt time.Time) (int64, error) {
	result := db.WithContext(ctx).Model(&model.ConfigReleaseVersion{}).
		Where("scope_id = ? AND deleted_at IS NULL", scopeID).
		Updates(map[string]any{
			"deleted_at":    deletedAt,
			"deleted_by":    actor,
			"delete_reason": reason,
		})
	return result.RowsAffected, result.Error
}

// --------------------- 资源清单 ---------------------

func (dao *ReleaseDAO) CreateAssets(ctx context.Context, db *gorm.DB, assets []model.ConfigReleaseAsset) error {
	if len(assets) == 0 {
		return nil
	}
	return db.WithContext(ctx).CreateInBatches(assets, 100).Error
}

func (dao *ReleaseDAO) ListAssets(ctx context.Context, db *gorm.DB, snapshotID string) ([]model.ConfigReleaseAsset, error) {
	var rows []model.ConfigReleaseAsset
	err := db.WithContext(ctx).
		Where("snapshot_id = ?", snapshotID).
		Order("asset_id ASC").
		Find(&rows).Error
	return rows, err
}

func (dao *ReleaseDAO) GetAsset(ctx context.Context, db *gorm.DB, snapshotID, assetID string) (*model.ConfigReleaseAsset, error) {
	var row model.ConfigReleaseAsset
	err := db.WithContext(ctx).
		Where("snapshot_id = ? AND asset_id = ?", snapshotID, assetID).
		First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// --------------------- 生效指针 ---------------------

func (dao *ReleaseDAO) GetHead(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string) (*model.ConfigReleaseHead, error) {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	return dao.GetHeadByScopeID(ctx, db, scopeID)
}

func (dao *ReleaseDAO) GetHeadByScopeID(ctx context.Context, db *gorm.DB, scopeID string) (*model.ConfigReleaseHead, error) {
	var row model.ConfigReleaseHead
	err := db.WithContext(ctx).Where("scope_id = ?", scopeID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (dao *ReleaseDAO) EnsureHead(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string) error {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return err
	}
	return dao.EnsureHeadByScope(ctx, db, scopeID, environmentKey, pipelineKey)
}

// EnsureHeadByScope creates the per-scope head without touching active state.
func (dao *ReleaseDAO) EnsureHeadByScope(ctx context.Context, db *gorm.DB,
	scopeID, environmentKey, pipelineKey string) error {
	if existing, err := dao.GetHeadByScopeID(ctx, db, scopeID); err != nil {
		return err
	} else if existing != nil {
		return nil
	}
	row := model.ConfigReleaseHead{
		ScopeID:        scopeID,
		EnvironmentKey: environmentKey,
		PipelineKey:    pipelineKey,
		Revision:       0,
	}
	return db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&row).Error
}

// AllocateVersionNumber atomically claims the next positive int64 for a scope.
// It only updates the counter, never the active pointer or revision.
func (dao *ReleaseDAO) AllocateVersionNumber(ctx context.Context, db *gorm.DB, scopeID string) (int64, error) {
	head, err := dao.GetHeadByScopeID(ctx, db, scopeID)
	if err != nil {
		return 0, err
	}
	if head == nil {
		return 0, ErrReleaseHeadMissing
	}
	if head.LastVersionNumber >= math.MaxInt64 {
		return 0, ErrReleaseNumberExhausted
	}
	next := head.LastVersionNumber + 1
	result := db.WithContext(ctx).
		Model(&model.ConfigReleaseHead{}).
		Where("scope_id = ? AND last_version_number = ?", scopeID, head.LastVersionNumber).
		Update("last_version_number", next)
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected != 1 {
		return 0, ErrReleaseNumberConflict
	}
	return next, nil
}

func (dao *ReleaseDAO) ApplyHeadCAS(ctx context.Context, db *gorm.DB,
	environmentKey, pipelineKey string, expectedRevision int64, activeSnapshotID, actor, reason string,
	changedAt time.Time) (bool, error) {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return false, err
	}
	result := db.WithContext(ctx).
		Model(&model.ConfigReleaseHead{}).
		Where("scope_id = ? AND revision = ?", scopeID, expectedRevision).
		Updates(map[string]any{
			"active_snapshot_id": activeSnapshotID,
			"revision":           gorm.Expr("revision + 1"),
			"changed_at":         changedAt,
			"changed_by":         actor,
			"changed_reason":     reason,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// ClearHeadByScope clears the active pointer of a deleted scope without
// touching version history.
func (dao *ReleaseDAO) ClearHeadByScope(ctx context.Context, db *gorm.DB, scopeID, actor, reason string, changedAt time.Time) error {
	return db.WithContext(ctx).Model(&model.ConfigReleaseHead{}).
		Where("scope_id = ? AND active_snapshot_id <> ''", scopeID).
		Updates(map[string]any{
			"active_snapshot_id": "",
			"revision":           gorm.Expr("revision + 1"),
			"changed_at":         changedAt,
			"changed_by":         actor,
			"changed_reason":     reason,
		}).Error
}

// --------------------- 审计事件 ---------------------

func (dao *ReleaseDAO) AppendEvent(ctx context.Context, db *gorm.DB, event *model.ConfigReleaseEvent) error {
	if event.ScopeID == "" {
		scopeID, err := ActiveScopeID(ctx, db, event.EnvironmentKey, event.PipelineKey)
		if err != nil {
			return err
		}
		event.ScopeID = scopeID
	}
	return db.WithContext(ctx).Create(event).Error
}

func (dao *ReleaseDAO) GetEvent(ctx context.Context, db *gorm.DB, eventType, requestID string) (*model.ConfigReleaseEvent, error) {
	var row model.ConfigReleaseEvent
	err := db.WithContext(ctx).
		Where("event_type = ? AND request_id = ?", eventType, requestID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (dao *ReleaseDAO) ListEvents(ctx context.Context, db *gorm.DB,
	environmentKey, pipelineKey string, limit int, beforeEventID uint) ([]model.ConfigReleaseEvent, bool, error) {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return nil, false, err
	}
	query := db.WithContext(ctx).Where("scope_id = ?", scopeID)
	if beforeEventID > 0 {
		query = query.Where("id < ?", beforeEventID)
	}
	var rows []model.ConfigReleaseEvent
	if err := query.Order("id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return rows, hasMore, nil
}

func IsReleaseNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
