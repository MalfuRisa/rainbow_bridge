package db

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

// EnvironmentDAO wraps CRUD operations for one live environment generation.
type EnvironmentDAO struct{}

func NewEnvironmentDAO() *EnvironmentDAO { return &EnvironmentDAO{} }

// Create persists a new environment generation.
func (dao *EnvironmentDAO) Create(ctx context.Context, db *gorm.DB, entity *model.Environment) error {
	if entity == nil {
		return errors.New("environment must not be nil")
	}
	if entity.EnvironmentKey == "" {
		return errors.New("environment_key is required")
	}
	if entity.EnvironmentID == "" {
		entity.EnvironmentID = uuid.NewString()
	}
	active := model.ActiveSlotActive
	entity.ActiveSlot = &active
	var count int64
	if err := db.WithContext(ctx).Model(&model.Environment{}).
		Where("environment_key = ? AND active_slot = ?", entity.EnvironmentKey, active).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("environment_key already exists")
	}
	return db.WithContext(ctx).Create(entity).Error
}

// Update updates the live environment addressed by its internal identity.
func (dao *EnvironmentDAO) Update(ctx context.Context, db *gorm.DB, entity *model.Environment) error {
	if entity == nil {
		return errors.New("environment must not be nil")
	}
	return db.WithContext(ctx).
		Model(&model.Environment{}).
		Where("environment_id = ?", entity.EnvironmentID).
		Updates(entity).
		Error
}

// SoftDeleteActive marks the live generation deleted and releases the key slot.
func (dao *EnvironmentDAO) SoftDeleteActive(ctx context.Context, db *gorm.DB, environmentID string, deletedAt time.Time) error {
	result := db.WithContext(ctx).
		Model(&model.Environment{}).
		Where("environment_id = ? AND active_slot = ?", environmentID, model.ActiveSlotActive).
		Updates(map[string]any{"deleted_at": deletedAt, "active_slot": nil})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// Delete is a compatibility wrapper that soft-deletes the live generation.
func (dao *EnvironmentDAO) Delete(ctx context.Context, db *gorm.DB, environmentKey string) error {
	env, err := dao.GetByKey(ctx, db, environmentKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	return dao.SoftDeleteActive(ctx, db, env.EnvironmentID, time.Now().UTC())
}

// GetByKey fetches the live generation by business key.
func (dao *EnvironmentDAO) GetByKey(ctx context.Context, db *gorm.DB, environmentKey string) (*model.Environment, error) {
	var entity model.Environment
	if err := db.WithContext(ctx).
		Where("environment_key = ? AND active_slot = ?", environmentKey, model.ActiveSlotActive).
		First(&entity).Error; err != nil {
		return nil, err
	}
	return &entity, nil
}

// GetByID fetches the live generation by internal identity.
func (dao *EnvironmentDAO) GetByID(ctx context.Context, db *gorm.DB, environmentID string) (*model.Environment, error) {
	var entity model.Environment
	if err := db.WithContext(ctx).
		Where("environment_id = ? AND active_slot = ?", environmentID, model.ActiveSlotActive).
		First(&entity).Error; err != nil {
		return nil, err
	}
	return &entity, nil
}

// List returns live environments with optional active filter.
func (dao *EnvironmentDAO) List(ctx context.Context, db *gorm.DB, isActive *bool, page, pageSize int) ([]model.Environment, error) {
	tx := db.WithContext(ctx).Where("active_slot = ?", model.ActiveSlotActive)
	if isActive != nil {
		tx = tx.Where("is_active = ?", *isActive)
	}
	if page > 0 && pageSize > 0 {
		offset := (page - 1) * pageSize
		tx = tx.Limit(pageSize).Offset(offset)
	}
	var entities []model.Environment
	if err := tx.Order("sort_order ASC, created_at ASC").Find(&entities).Error; err != nil {
		return nil, err
	}
	return entities, nil
}

// ExistsByKey reports whether a live generation exists for the business key.
func (dao *EnvironmentDAO) ExistsByKey(ctx context.Context, db *gorm.DB, environmentKey string) (bool, error) {
	var count int64
	if err := db.WithContext(ctx).
		Model(&model.Environment{}).
		Where("environment_key = ? AND active_slot = ?", environmentKey, model.ActiveSlotActive).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// CountAllIncludingDeleted distinguishes a never-initialized database from
// one where the user deliberately removed every environment.
func (dao *EnvironmentDAO) CountAllIncludingDeleted(ctx context.Context, db *gorm.DB) (int64, error) {
	var count int64
	err := db.WithContext(ctx).Unscoped().Model(&model.Environment{}).Count(&count).Error
	return count, err
}
