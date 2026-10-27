package db

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"

	"gorm.io/gorm"
)

// ConfigDAO wraps CRUD operations for configurations inside one scope.
type ConfigDAO struct{}

func NewConfigDAO() *ConfigDAO { return &ConfigDAO{} }

// Create persists a new configuration in the current scope generation.
func (dao *ConfigDAO) Create(ctx context.Context, db *gorm.DB, entity *model.Config) error {
	if entity == nil {
		return errors.New("config must not be nil")
	}
	if entity.ResourceKey == "" {
		entity.ResourceKey = uuid.NewString()
	}
	if entity.ScopeID == "" {
		scopeID, err := ActiveScopeID(ctx, db, entity.EnvironmentKey, entity.PipelineKey)
		if err != nil {
			return err
		}
		entity.ScopeID = scopeID
	}
	entity.EditRevision = uuid.NewString()
	return configWriteError(db.WithContext(ctx).Create(entity).Error)
}

// OverwriteByEnvironmentAndPipeline updates an existing configuration in the
// current scope. Alias remains immutable.
func (dao *ConfigDAO) OverwriteByEnvironmentAndPipeline(ctx context.Context, db *gorm.DB,
	environmentKey, pipelineKey string, entity *model.Config) error {
	if entity == nil {
		return errors.New("config must not be nil")
	}
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return err
	}
	entity.ScopeID = scopeID
	return dao.updateConfig(ctx, db, scopeID, entity, "")
}

func (dao *ConfigDAO) UpdateIfRevision(ctx context.Context, db *gorm.DB, entity *model.Config, expected string) error {
	if !ValidEditRevision(expected) {
		return ErrEditRevisionInvalid
	}
	if entity == nil {
		return errors.New("config must not be nil")
	}
	scopeID := entity.ScopeID
	if scopeID == "" {
		resolved, err := ActiveScopeID(ctx, db, entity.EnvironmentKey, entity.PipelineKey)
		if err != nil {
			return err
		}
		scopeID = resolved
		entity.ScopeID = scopeID
	}
	return dao.updateConfig(ctx, db, scopeID, entity, expected)
}

func (dao *ConfigDAO) updateConfig(ctx context.Context, db *gorm.DB, scopeID string,
	entity *model.Config, expected string) error {
	fields := map[string]any{"description": entity.Description}
	if entity.Name != "" {
		fields["name"] = entity.Name
	}
	if entity.Content != "" {
		fields["content"] = entity.Content
	}
	if entity.Type != "" {
		fields["type"] = entity.Type
	}
	if entity.Remark != "" {
		fields["remark"] = entity.Remark
	}
	if entity.IsPerm {
		fields["is_perm"] = true
	}
	revision := uuid.NewString()
	fields["edit_revision"] = revision
	query := db.WithContext(ctx).Model(&model.Config{}).
		Where("scope_id = ? AND resource_key = ?", scopeID, entity.ResourceKey)
	if expected != "" {
		query = query.Where("edit_revision = ?", expected)
	}
	result := query.Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		_, err := dao.GetByScopeAndResourceKey(ctx, db, scopeID, entity.ResourceKey)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrEditTargetNotFound
		}
		if err != nil {
			return err
		}
		return ErrEditConflict
	}
	if result.RowsAffected != 1 {
		return errors.New("config update affected multiple rows")
	}
	entity.EditRevision = revision
	return nil
}

// ClearAll removes all configuration entries.
func (dao *ConfigDAO) ClearAll(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Where("1 = 1").Unscoped().Delete(&model.Config{}).Error
}

// DeleteByEnvironmentPipelineAndResourceKey performs a hard delete in the
// current scope (normal user deletion semantics).
func (dao *ConfigDAO) DeleteByEnvironmentPipelineAndResourceKey(ctx context.Context, db *gorm.DB,
	environmentKey, pipelineKey, resourceKey string) error {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Unscoped().
		Where("scope_id = ? AND resource_key = ?", scopeID, resourceKey).
		Delete(&model.Config{}).Error
}

// SoftDeleteByScope marks every live config of a deleted scope.
func (dao *ConfigDAO) SoftDeleteByScope(ctx context.Context, db *gorm.DB, scopeID string, deletedAt time.Time) (int64, error) {
	result := db.WithContext(ctx).Unscoped().Model(&model.Config{}).
		Where("scope_id = ? AND deleted_at IS NULL", scopeID).
		Updates(map[string]any{"deleted_at": deletedAt})
	return result.RowsAffected, result.Error
}

// GetByResourceKey fetches a config in the current scope.
func (dao *ConfigDAO) GetByResourceKey(ctx context.Context, db *gorm.DB,
	environmentKey, pipelineKey, resourceKey string) (*model.Config, error) {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	return dao.GetByScopeAndResourceKey(ctx, db, scopeID, resourceKey)
}

func (dao *ConfigDAO) GetByScopeAndResourceKey(ctx context.Context, db *gorm.DB,
	scopeID, resourceKey string) (*model.Config, error) {
	var entity model.Config
	if err := db.WithContext(ctx).
		Where("scope_id = ? AND resource_key = ?", scopeID, resourceKey).
		First(&entity).Error; err != nil {
		return nil, err
	}
	return &entity, nil
}

// GetByResourceKeyOnly is retained for the legacy key-only helper; new code
// must resolve a scope before reading.
func (dao *ConfigDAO) GetByResourceKeyOnly(ctx context.Context, db *gorm.DB, resourceKey string) (*model.Config, error) {
	var entity model.Config
	if err := db.WithContext(ctx).
		Where("resource_key = ?", resourceKey).Order("id DESC").
		First(&entity).Error; err != nil {
		return nil, err
	}
	return &entity, nil
}

// GetByAlias fetches the current scope config by alias.
func (dao *ConfigDAO) GetByAlias(ctx context.Context, db *gorm.DB,
	environmentKey, pipelineKey, alias string) (*model.Config, error) {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	return dao.GetByAliasInScope(ctx, db, scopeID, alias)
}

func (dao *ConfigDAO) GetByAliasInScope(ctx context.Context, db *gorm.DB, scopeID, alias string) (*model.Config, error) {
	var entity model.Config
	if err := db.WithContext(ctx).
		Where("scope_id = ? AND alias = ?", scopeID, alias).
		Order("updated_at DESC").
		First(&entity).Error; err != nil {
		return nil, err
	}
	return &entity, nil
}

// ListLatestByScope returns the most recently updated config per alias.
func (dao *ConfigDAO) ListLatestByScope(ctx context.Context, db *gorm.DB, scopeID string) ([]model.Config, error) {
	subQuery := db.Model(&model.Config{}).
		Select("MAX(id) as id").
		Where("scope_id = ?", scopeID).
		Group("alias")
	var entities []model.Config
	if err := db.WithContext(ctx).
		Where("id IN (?) AND scope_id = ?", subQuery, scopeID).
		Order("updated_at DESC").Find(&entities).Error; err != nil {
		return nil, err
	}
	return entities, nil
}

// DeleteByScopeAndResourceKey hard-deletes one config inside a scope.
func (dao *ConfigDAO) DeleteByScopeAndResourceKey(ctx context.Context, db *gorm.DB, scopeID, resourceKey string) error {
	return db.WithContext(ctx).Unscoped().
		Where("scope_id = ? AND resource_key = ?", scopeID, resourceKey).
		Delete(&model.Config{}).Error
}

// ListByEnvironmentAndPipeline returns the most recently updated configs per alias.
func (dao *ConfigDAO) ListByEnvironmentAndPipeline(ctx context.Context, db *gorm.DB,
	environmentKey, pipelineKey string, _ string, page, pageSize int) ([]model.Config, error) {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	subQuery := db.Model(&model.Config{}).
		Select("MAX(id) as id").
		Where("scope_id = ?", scopeID).
		Group("alias")
	tx := db.WithContext(ctx).
		Where("id IN (?) AND scope_id = ?", subQuery, scopeID).
		Order("updated_at DESC")
	if page > 0 && pageSize > 0 {
		offset := (page - 1) * pageSize
		tx = tx.Limit(pageSize).Offset(offset)
	}
	var entities []model.Config
	if err := tx.Find(&entities).Error; err != nil {
		return nil, err
	}
	return entities, nil
}

// ListByEnvironmentAndPipelineWithFilter retrieves current-scope configs with
// optional type filtering.
func (dao *ConfigDAO) ListByEnvironmentAndPipelineWithFilter(ctx context.Context, db *gorm.DB,
	environmentKey, pipelineKey string, _ string, _ string, resourceType string, page, pageSize int) ([]model.Config, error) {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	return dao.ListByScopeWithFilter(ctx, db, scopeID, resourceType, page, pageSize)
}

func (dao *ConfigDAO) ListByScopeWithFilter(ctx context.Context, db *gorm.DB,
	scopeID, resourceType string, page, pageSize int) ([]model.Config, error) {
	tx := db.WithContext(ctx).Where("scope_id = ?", scopeID)
	if resourceType != "" {
		tx = tx.Where("type = ?", resourceType)
	}
	if page > 0 && pageSize > 0 {
		offset := (page - 1) * pageSize
		tx = tx.Limit(pageSize).Offset(offset)
	}
	var entities []model.Config
	if err := tx.Order("updated_at DESC").Find(&entities).Error; err != nil {
		return nil, err
	}
	return entities, nil
}

// ListByScopeAll returns all live configs of one scope.
func (dao *ConfigDAO) ListByScopeAll(ctx context.Context, db *gorm.DB, scopeID string) ([]model.Config, error) {
	var entities []model.Config
	if err := db.WithContext(ctx).Where("scope_id = ?", scopeID).
		Order("resource_key ASC").Find(&entities).Error; err != nil {
		return nil, err
	}
	return entities, nil
}

// CountByScope counts live configs for deletion reporting.
func (dao *ConfigDAO) CountByScope(ctx context.Context, db *gorm.DB, scopeID string) (int64, error) {
	var count int64
	err := db.WithContext(ctx).Unscoped().Model(&model.Config{}).
		Where("scope_id = ? AND deleted_at IS NULL", scopeID).Count(&count).Error
	return count, err
}

// ListAllBusinessKeys is deprecated, use environment/pipeline queries instead.
func (dao *ConfigDAO) ListAllBusinessKeys(ctx context.Context, db *gorm.DB) ([]string, error) {
	return []string{}, errors.New("business_key is deprecated, use environment_key and pipeline_key instead")
}