package db

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

// PipelineDAO wraps CRUD operations for one live scope generation.
type PipelineDAO struct{}

func NewPipelineDAO() *PipelineDAO { return &PipelineDAO{} }

// Create persists a new pipeline generation.
func (dao *PipelineDAO) Create(ctx context.Context, db *gorm.DB, entity *model.Pipeline) error {
	if entity == nil {
		return errors.New("pipeline must not be nil")
	}
	if entity.EnvironmentKey == "" {
		return errors.New("environment_key is required")
	}
	if entity.PipelineKey == "" {
		return errors.New("pipeline_key is required")
	}
	if entity.EnvironmentID == "" {
		env, err := ActiveEnvironment(ctx, db, entity.EnvironmentKey)
		if err != nil {
			return err
		}
		entity.EnvironmentID = env.EnvironmentID
	}
	if entity.ScopeID == "" {
		entity.ScopeID = uuid.NewString()
	}
	active := model.ActiveSlotActive
	entity.ActiveSlot = &active
	var count int64
	if err := db.WithContext(ctx).Model(&model.Pipeline{}).
		Where("environment_id = ? AND pipeline_key = ? AND active_slot = ?",
			entity.EnvironmentID, entity.PipelineKey, active).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("pipeline_key already exists")
	}
	return db.WithContext(ctx).Create(entity).Error
}

// Update updates the live pipeline addressed by scope_id.
func (dao *PipelineDAO) Update(ctx context.Context, db *gorm.DB, entity *model.Pipeline) error {
	if entity == nil {
		return errors.New("pipeline must not be nil")
	}
	return db.WithContext(ctx).
		Model(&model.Pipeline{}).
		Where("scope_id = ?", entity.ScopeID).
		Updates(entity).
		Error
}

// SoftDeleteScope marks the live scope deleted and releases its key slot.
func (dao *PipelineDAO) SoftDeleteScope(ctx context.Context, db *gorm.DB, scopeID string, deletedAt time.Time) error {
	result := db.WithContext(ctx).
		Model(&model.Pipeline{}).
		Where("scope_id = ? AND active_slot = ?", scopeID, model.ActiveSlotActive).
		Updates(map[string]any{"deleted_at": deletedAt, "active_slot": nil})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// Delete is a compatibility wrapper that soft-deletes the live scope.
func (dao *PipelineDAO) Delete(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string) error {
	pipeline, err := dao.GetByKey(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	return dao.SoftDeleteScope(ctx, db, pipeline.ScopeID, time.Now().UTC())
}

// GetByKey resolves the live environment then the live pipeline.
func (dao *PipelineDAO) GetByKey(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string) (*model.Pipeline, error) {
	return ActiveScope(ctx, db, environmentKey, pipelineKey)
}

// GetByScopeID fetches a live scope by identity.
func (dao *PipelineDAO) GetByScopeID(ctx context.Context, db *gorm.DB, scopeID string) (*model.Pipeline, error) {
	var entity model.Pipeline
	if err := db.WithContext(ctx).
		Where("scope_id = ? AND active_slot = ?", scopeID, model.ActiveSlotActive).
		First(&entity).Error; err != nil {
		return nil, err
	}
	return &entity, nil
}

// List returns live pipelines for the live environment generation.
func (dao *PipelineDAO) List(ctx context.Context, db *gorm.DB, environmentKey string, isActive *bool, page, pageSize int) ([]model.Pipeline, error) {
	env, err := ActiveEnvironment(ctx, db, environmentKey)
	if err != nil {
		return nil, err
	}
	return dao.ListByEnvironmentID(ctx, db, env.EnvironmentID, isActive, page, pageSize)
}

func (dao *PipelineDAO) ListByEnvironmentID(ctx context.Context, db *gorm.DB, environmentID string,
	isActive *bool, page, pageSize int) ([]model.Pipeline, error) {
	tx := db.WithContext(ctx).
		Where("environment_id = ? AND active_slot = ?", environmentID, model.ActiveSlotActive)
	if isActive != nil {
		tx = tx.Where("is_active = ?", *isActive)
	}
	if page > 0 && pageSize > 0 {
		offset := (page - 1) * pageSize
		tx = tx.Limit(pageSize).Offset(offset)
	}
	var entities []model.Pipeline
	if err := tx.Order("sort_order ASC, created_at ASC").Find(&entities).Error; err != nil {
		return nil, err
	}
	return entities, nil
}

// ExistsByKey reports whether a live pipeline exists in the live environment.
func (dao *PipelineDAO) ExistsByKey(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string) (bool, error) {
	env, err := ActiveEnvironment(ctx, db, environmentKey)
	if err != nil {
		if errors.Is(err, ErrNoActiveScope) || errors.Is(err, ErrScopeDeleted) {
			return false, nil
		}
		return false, err
	}
	var count int64
	if err := db.WithContext(ctx).
		Model(&model.Pipeline{}).
		Where("environment_id = ? AND pipeline_key = ? AND active_slot = ?",
			env.EnvironmentID, pipelineKey, model.ActiveSlotActive).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// CountAllIncludingDeleted distinguishes an empty database from a fully
// deleted set of user scopes.
func (dao *PipelineDAO) CountAllIncludingDeleted(ctx context.Context, db *gorm.DB) (int64, error) {
	var count int64
	err := db.WithContext(ctx).Unscoped().Model(&model.Pipeline{}).Count(&count).Error
	return count, err
}
