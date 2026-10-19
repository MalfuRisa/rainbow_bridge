package db

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"

	"gorm.io/gorm"
)

// ConfigDAO wraps basic CRUD operations for configuration entities.
type ConfigDAO struct{}

func NewConfigDAO() *ConfigDAO { return &ConfigDAO{} }

// Create persists a new configuration entry. If no resource key is provided a UUID is assigned.
func (dao *ConfigDAO) Create(ctx context.Context, db *gorm.DB, entity *model.Config) error {
	if entity == nil {
		return errors.New("config must not be nil")
	}
	if entity.ResourceKey == "" {
		entity.ResourceKey = uuid.NewString()
	}
	return db.WithContext(ctx).Create(entity).Error
}

// UpdateByEnvironmentAndPipeline updates an existing configuration identified by environment_key + pipeline_key + resource_key.
// Note: alias field is immutable and will not be updated.
func (dao *ConfigDAO) UpdateByEnvironmentAndPipeline(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string, entity *model.Config) error {
	if entity == nil {
		return errors.New("config must not be nil")
	}
	// Preserve existing non-zero update semantics except description, which is
	// always the submitted value (including an omitted/empty string).
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
	return db.WithContext(ctx).
		Model(&model.Config{}).
		Where("environment_key = ? AND pipeline_key = ? AND resource_key = ?", environmentKey, pipelineKey, entity.ResourceKey).
		Updates(fields).
		Error
}

// ClearAll removes all configuration entries.
func (dao *ConfigDAO) ClearAll(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Where("1 = 1").Unscoped().Delete(&model.Config{}).Error
}

// DeleteByEnvironmentPipelineAndResourceKey performs a hard delete by composite key.
func (dao *ConfigDAO) DeleteByEnvironmentPipelineAndResourceKey(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey, resourceKey string) error {
	return db.WithContext(ctx).
		Unscoped().
		Where("environment_key = ? AND pipeline_key = ? AND resource_key = ?", environmentKey, pipelineKey, resourceKey).
		Delete(&model.Config{}).Error
}

// GetByResourceKey fetches a single config by environment + pipeline + resource key.
func (dao *ConfigDAO) GetByResourceKey(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey, resourceKey string) (*model.Config, error) {
	var entity model.Config
	if err := db.WithContext(ctx).
		Where("environment_key = ? AND pipeline_key = ? AND resource_key = ?", environmentKey, pipelineKey, resourceKey).
		First(&entity).Error; err != nil {
		return nil, err
	}
	return &entity, nil
}

// GetByResourceKeyOnly fetches a config by resource key ignoring business key.
func (dao *ConfigDAO) GetByResourceKeyOnly(ctx context.Context, db *gorm.DB, resourceKey string) (*model.Config, error) {
	var entity model.Config
	if err := db.WithContext(ctx).
		Where("resource_key = ?", resourceKey).
		First(&entity).Error; err != nil {
		return nil, err
	}
	return &entity, nil
}

// GetByAlias fetches the latest config by environment + pipeline + alias.
func (dao *ConfigDAO) GetByAlias(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey, alias string) (*model.Config, error) {
	var entity model.Config
	if err := db.WithContext(ctx).
		Where("environment_key = ? AND pipeline_key = ? AND alias = ?", environmentKey, pipelineKey, alias).
		Order("updated_at DESC").
		First(&entity).Error; err != nil {
		return nil, err
	}
	return &entity, nil
}

// ListByEnvironmentAndPipeline returns the most recently updated configs per alias.
func (dao *ConfigDAO) ListByEnvironmentAndPipeline(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string, _ string, page, pageSize int) ([]model.Config, error) {
	var entities []model.Config
	// 使用子查询获取每个alias的最新记录
	subQuery := db.Model(&model.Config{}).
		Select("MAX(id) as id").
		Where("environment_key = ? AND pipeline_key = ?", environmentKey, pipelineKey).
		Group("alias")

	tx := db.WithContext(ctx).
		Where("id IN (?) AND environment_key = ? AND pipeline_key = ?", subQuery, environmentKey, pipelineKey).
		Order("updated_at DESC")

	// 添加分页支持
	if page > 0 && pageSize > 0 {
		offset := (page - 1) * pageSize
		tx = tx.Limit(pageSize).Offset(offset)
	}

	if err := tx.Find(&entities).Error; err != nil {
		return nil, err
	}
	return entities, nil
}

// ListByEnvironmentAndPipelineWithFilter retrieves configs optionally filtering by type.
func (dao *ConfigDAO) ListByEnvironmentAndPipelineWithFilter(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string, _ string, _ string, resourceType string, page, pageSize int) ([]model.Config, error) {
	tx := db.WithContext(ctx).Where("environment_key = ? AND pipeline_key = ?", environmentKey, pipelineKey)

	if resourceType != "" {
		tx = tx.Where("type = ?", resourceType)
	}

	// 添加分页支持
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

// ListAllBusinessKeys is deprecated, use environment/pipeline queries instead.
// Kept for backward compatibility during migration.
func (dao *ConfigDAO) ListAllBusinessKeys(ctx context.Context, db *gorm.DB) ([]string, error) {
	return []string{}, errors.New("business_key is deprecated, use environment_key and pipeline_key instead")
}
