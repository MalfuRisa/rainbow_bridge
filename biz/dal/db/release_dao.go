package db

import (
	"context"
	"errors"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReleaseDAO 读写发布版本、资源清单、生效指针与审计事件。
// 所有方法只操作数据库,不接触磁盘;写入必须由调用方在写协调事务内传入 tx。
type ReleaseDAO struct{}

func NewReleaseDAO() *ReleaseDAO { return &ReleaseDAO{} }

// --------------------- 版本 ---------------------

func (dao *ReleaseDAO) CreateVersion(ctx context.Context, db *gorm.DB, version *model.ConfigReleaseVersion) error {
	return db.WithContext(ctx).Create(version).Error
}

func (dao *ReleaseDAO) GetVersionBySnapshotID(ctx context.Context, db *gorm.DB, snapshotID string) (*model.ConfigReleaseVersion, error) {
	var version model.ConfigReleaseVersion
	if err := db.WithContext(ctx).Where("snapshot_id = ?", snapshotID).First(&version).Error; err != nil {
		return nil, err
	}
	return &version, nil
}

func (dao *ReleaseDAO) GetVersionByScopeAndName(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey, version string) (*model.ConfigReleaseVersion, error) {
	var row model.ConfigReleaseVersion
	err := db.WithContext(ctx).
		Where("environment_key = ? AND pipeline_key = ? AND version = ?", environmentKey, pipelineKey, version).
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

// ListVersions 按创建 ID 倒序分页;多取一条用于 has_more。
func (dao *ReleaseDAO) ListVersions(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string, limit int, beforeID uint) ([]model.ConfigReleaseVersion, bool, error) {
	query := db.WithContext(ctx).
		Where("environment_key = ? AND pipeline_key = ?", environmentKey, pipelineKey)
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

// CountVersionsByScope 统计范围内已提交版本(父级删除保护,须在协调事务内调用)。
func (dao *ReleaseDAO) CountVersionsByScope(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string) (int64, error) {
	var count int64
	err := db.WithContext(ctx).Model(&model.ConfigReleaseVersion{}).
		Where("environment_key = ? AND pipeline_key = ?", environmentKey, pipelineKey).
		Count(&count).Error
	return count, err
}

// CountVersionsByEnvironment 统计环境内任一渠道是否存在已提交版本。
func (dao *ReleaseDAO) CountVersionsByEnvironment(ctx context.Context, db *gorm.DB, environmentKey string) (int64, error) {
	var count int64
	err := db.WithContext(ctx).Model(&model.ConfigReleaseVersion{}).
		Where("environment_key = ?", environmentKey).
		Count(&count).Error
	return count, err
}

// ListVersionsByEnvironment 列出环境内全部版本(用于恢复/诊断与删除保护明细)。
func (dao *ReleaseDAO) ListVersionsByEnvironment(ctx context.Context, db *gorm.DB, environmentKey string) ([]model.ConfigReleaseVersion, error) {
	var rows []model.ConfigReleaseVersion
	if err := db.WithContext(ctx).Where("environment_key = ?", environmentKey).Order("id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// --------------------- 资源清单 ---------------------

func (dao *ReleaseDAO) CreateAssets(ctx context.Context, db *gorm.DB, assets []model.ConfigReleaseAsset) error {
	if len(assets) == 0 {
		return nil
	}
	return db.WithContext(ctx).CreateInBatches(assets, 100).Error
}

// ListAssets 按 asset_id 升序返回清单(摘要与读取的稳定顺序)。
func (dao *ReleaseDAO) ListAssets(ctx context.Context, db *gorm.DB, snapshotID string) ([]model.ConfigReleaseAsset, error) {
	var rows []model.ConfigReleaseAsset
	err := db.WithContext(ctx).
		Where("snapshot_id = ?", snapshotID).
		Order("asset_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
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

// GetHead 读取生效指针;无行时返回 (nil, nil)(语义为未设置,GET 不建行)。
func (dao *ReleaseDAO) GetHead(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string) (*model.ConfigReleaseHead, error) {
	var row model.ConfigReleaseHead
	err := db.WithContext(ctx).
		Where("environment_key = ? AND pipeline_key = ?", environmentKey, pipelineKey).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// EnsureHead 幂等补建指针行(revision=0、未设置生效版本)。
func (dao *ReleaseDAO) EnsureHead(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string) error {
	row := model.ConfigReleaseHead{
		EnvironmentKey: environmentKey,
		PipelineKey:    pipelineKey,
		Revision:       0,
	}
	return db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&row).Error
}

// ApplyHeadCAS 以 revision 为条件更新生效指针;返回 false 表示冲突。
func (dao *ReleaseDAO) ApplyHeadCAS(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string, expectedRevision int64, activeSnapshotID, actor, reason string, changedAt time.Time) (bool, error) {
	result := db.WithContext(ctx).
		Model(&model.ConfigReleaseHead{}).
		Where("environment_key = ? AND pipeline_key = ? AND revision = ?", environmentKey, pipelineKey, expectedRevision).
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

// --------------------- 审计事件 ---------------------

func (dao *ReleaseDAO) AppendEvent(ctx context.Context, db *gorm.DB, event *model.ConfigReleaseEvent) error {
	return db.WithContext(ctx).Create(event).Error
}

// GetEvent 按 (event_type, request_id) 读取事件,用于幂等确认。
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

// ListEvents 按事件 ID 倒序分页。
func (dao *ReleaseDAO) ListEvents(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string, limit int, beforeEventID uint) ([]model.ConfigReleaseEvent, bool, error) {
	query := db.WithContext(ctx).
		Where("environment_key = ? AND pipeline_key = ?", environmentKey, pipelineKey)
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

// IsReleaseNotFound 判定错误是否为记录不存在。
func IsReleaseNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
