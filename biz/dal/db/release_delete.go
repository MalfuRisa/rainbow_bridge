package db

import (
	"context"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

// MarkVersionDeleted retains the version identity, payload, manifests and unique keys.
// The caller holds the database write gate and appends the event in this transaction.
func (dao *ReleaseDAO) MarkVersionDeleted(ctx context.Context, tx *gorm.DB, snapshotID, actor, reason string, at time.Time) (bool, error) {
	result := tx.WithContext(ctx).Model(&model.ConfigReleaseVersion{}).
		Where("snapshot_id = ? AND deleted_at IS NULL", snapshotID).
		Updates(map[string]interface{}{"deleted_at": at, "deleted_by": actor, "delete_reason": reason})
	return result.RowsAffected == 1, result.Error
}
