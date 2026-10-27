package db

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"

	"gorm.io/gorm"
)

// AssetDAO handles CRUD operations for file assets.
type AssetDAO struct{}

func NewAssetDAO() *AssetDAO { return &AssetDAO{} }

func (dao *AssetDAO) Create(ctx context.Context, db *gorm.DB, asset *model.Asset) error {
	if asset == nil {
		return nil
	}
	if asset.FileID == "" {
		asset.FileID = uuid.NewString()
	}
	if asset.ScopeID == "" {
		scopeID, err := ActiveScopeID(ctx, db, asset.EnvironmentKey, asset.PipelineKey)
		if err != nil {
			return err
		}
		asset.ScopeID = scopeID
	}
	return db.WithContext(ctx).Create(asset).Error
}

func (dao *AssetDAO) Update(ctx context.Context, db *gorm.DB, asset *model.Asset) error {
	if asset == nil {
		return nil
	}
	result := db.WithContext(ctx).
		Model(&model.Asset{}).
		Where("file_id = ?", asset.FileID).
		Updates(asset)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (dao *AssetDAO) DeleteByFileID(ctx context.Context, db *gorm.DB, fileID string) error {
	return db.WithContext(ctx).Unscoped().Where("file_id = ?", fileID).Delete(&model.Asset{}).Error
}

func (dao *AssetDAO) GetByFileID(ctx context.Context, db *gorm.DB, fileID string) (*model.Asset, error) {
	var asset model.Asset
	if err := db.WithContext(ctx).Where("file_id = ?", fileID).First(&asset).Error; err != nil {
		return nil, err
	}
	return &asset, nil
}

// GetByFileIDIncludingDeleted is used to distinguish a deleted-scope asset
// (410) from a never-existing file id (404).
func (dao *AssetDAO) GetByFileIDIncludingDeleted(ctx context.Context, db *gorm.DB, fileID string) (*model.Asset, error) {
	var asset model.Asset
	if err := db.WithContext(ctx).Unscoped().Where("file_id = ?", fileID).First(&asset).Error; err != nil {
		return nil, err
	}
	return &asset, nil
}

func (dao *AssetDAO) ListByEnvironmentAndPipeline(ctx context.Context, db *gorm.DB,
	environmentKey, pipelineKey string, page, pageSize int) ([]model.Asset, error) {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	return dao.ListByScope(ctx, db, scopeID, page, pageSize)
}

func (dao *AssetDAO) ListByScope(ctx context.Context, db *gorm.DB, scopeID string, page, pageSize int) ([]model.Asset, error) {
	tx := db.WithContext(ctx).
		Where("scope_id = ?", scopeID).
		Order("created_at DESC")
	if page > 0 && pageSize > 0 {
		offset := (page - 1) * pageSize
		tx = tx.Limit(pageSize).Offset(offset)
	}
	var assets []model.Asset
	if err := tx.Find(&assets).Error; err != nil {
		return nil, err
	}
	return assets, nil
}

// SoftDeleteByScope marks every live asset of a deleted scope.
func (dao *AssetDAO) SoftDeleteByScope(ctx context.Context, db *gorm.DB, scopeID string, deletedAt time.Time) (int64, error) {
	result := db.WithContext(ctx).Unscoped().Model(&model.Asset{}).
		Where("scope_id = ? AND deleted_at IS NULL", scopeID).
		Updates(map[string]any{"deleted_at": deletedAt})
	return result.RowsAffected, result.Error
}

func (dao *AssetDAO) CountByScope(ctx context.Context, db *gorm.DB, scopeID string) (int64, error) {
	var count int64
	err := db.WithContext(ctx).Unscoped().Model(&model.Asset{}).
		Where("scope_id = ? AND deleted_at IS NULL", scopeID).Count(&count).Error
	return count, err
}