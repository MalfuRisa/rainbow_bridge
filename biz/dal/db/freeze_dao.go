package db

import (
	"context"
	"errors"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// FreezeDAO reads and writes freeze state/events for one scope generation.
type FreezeDAO struct{}

func NewFreezeDAO() *FreezeDAO { return &FreezeDAO{} }

func (dao *FreezeDAO) GetState(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string) (*model.ConfigFreezeState, error) {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	return dao.GetStateByScopeID(ctx, db, scopeID)
}

func (dao *FreezeDAO) GetStateByScopeID(ctx context.Context, db *gorm.DB, scopeID string) (*model.ConfigFreezeState, error) {
	var state model.ConfigFreezeState
	err := db.WithContext(ctx).Where("scope_id = ?", scopeID).First(&state).Error
	if err != nil {
		return nil, err
	}
	return &state, nil
}

// ListStatesByEnvironment returns state rows for the live environment generation.
func (dao *FreezeDAO) ListStatesByEnvironment(ctx context.Context, db *gorm.DB, environmentKey string) ([]model.ConfigFreezeState, error) {
	env, err := ActiveEnvironment(ctx, db, environmentKey)
	if err != nil {
		return nil, err
	}
	var states []model.ConfigFreezeState
	err = db.WithContext(ctx).
		Where("scope_id IN (?)", db.Model(&model.Pipeline{}).
			Select("scope_id").
			Where("environment_id = ? AND active_slot = ?", env.EnvironmentID, model.ActiveSlotActive)).
		Find(&states).Error
	return states, err
}

// EnsureState creates the unlocked row for the current scope; existing state is
// never reset.
func (dao *FreezeDAO) EnsureState(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string) error {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return err
	}
	if _, err := dao.GetStateByScopeID(ctx, db, scopeID); err == nil {
		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	state := model.ConfigFreezeState{
		ScopeID:        scopeID,
		EnvironmentKey: environmentKey,
		PipelineKey:    pipelineKey,
		State:          model.FreezeStateUnlocked,
		Revision:       0,
	}
	return db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&state).Error
}

func (dao *FreezeDAO) ApplyTransition(ctx context.Context, db *gorm.DB,
	environmentKey, pipelineKey string, expectedRevision int64, desiredState, actor, reason string,
	changedAt any) (bool, error) {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return false, err
	}
	result := db.WithContext(ctx).
		Model(&model.ConfigFreezeState{}).
		Where("scope_id = ? AND revision = ? AND state <> ?", scopeID, expectedRevision, desiredState).
		Updates(map[string]any{
			"state":           desiredState,
			"revision":        gorm.Expr("revision + 1"),
			"last_changed_at": changedAt,
			"last_changed_by": actor,
			"last_reason":     reason,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (dao *FreezeDAO) AppendEvent(ctx context.Context, db *gorm.DB, event *model.ConfigFreezeEvent) error {
	if event.ScopeID == "" {
		scopeID, err := ActiveScopeID(ctx, db, event.EnvironmentKey, event.PipelineKey)
		if err != nil {
			return err
		}
		event.ScopeID = scopeID
	}
	return db.WithContext(ctx).Create(event).Error
}

func (dao *FreezeDAO) ListEvents(ctx context.Context, db *gorm.DB,
	environmentKey, pipelineKey string, limit int, beforeEventID uint) ([]model.ConfigFreezeEvent, bool, error) {
	scopeID, err := ActiveScopeID(ctx, db, environmentKey, pipelineKey)
	if err != nil {
		return nil, false, err
	}
	query := db.WithContext(ctx).Where("scope_id = ?", scopeID)
	if beforeEventID > 0 {
		query = query.Where("id < ?", beforeEventID)
	}
	var events []model.ConfigFreezeEvent
	if err := query.Order("id DESC").Limit(limit + 1).Find(&events).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	return events, hasMore, nil
}

func (dao *FreezeDAO) CountLockedScopes(ctx context.Context, db *gorm.DB, scopeIDs []string) (int64, error) {
	if len(scopeIDs) == 0 {
		return 0, nil
	}
	var count int64
	err := db.WithContext(ctx).Model(&model.ConfigFreezeState{}).
		Where("scope_id IN ? AND state = ?", scopeIDs, model.FreezeStateLocked).Count(&count).Error
	return count, err
}

func IsStateMissing(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
