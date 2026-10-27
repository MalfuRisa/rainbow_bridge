package db

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

var (
	ErrEditRevisionInvalid = errors.New("expected_edit_revision must be a canonical lowercase UUID v4")
	ErrEditConflict        = errors.New("配置已被其他操作修改，请重新加载后核对再保存")
	ErrEditTargetNotFound  = errors.New("配置已不存在，请刷新列表确认")
)

func ValidEditRevision(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.Version() == 4 && id.Variant() == uuid.RFC4122 && id.String() == value
}

// BackfillConfigRevisions preserves timestamps and another writer's token.
func BackfillConfigRevisions(ctx context.Context, conn *gorm.DB) error {
	var rows []model.Config
	if err := conn.WithContext(ctx).Unscoped().Select("id").Where("edit_revision IS NULL OR edit_revision = ''").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := conn.WithContext(ctx).Unscoped().Model(&model.Config{}).Where("id = ? AND (edit_revision IS NULL OR edit_revision = '')", row.ID).UpdateColumn("edit_revision", uuid.NewString()).Error; err != nil {
			return err
		}
	}
	return nil
}
