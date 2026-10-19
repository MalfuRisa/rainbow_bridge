package db

import (
	"context"
	"errors"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// FreezeDAO 读写冻结状态与追加式冻结事件。
type FreezeDAO struct{}

func NewFreezeDAO() *FreezeDAO { return &FreezeDAO{} }

// GetState 返回指定范围的状态行;不存在时返回 gorm.ErrRecordNotFound。
func (dao *FreezeDAO) GetState(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string) (*model.ConfigFreezeState, error) {
	var state model.ConfigFreezeState
	err := db.WithContext(ctx).
		Where("environment_key = ? AND pipeline_key = ?", environmentKey, pipelineKey).
		First(&state).Error
	if err != nil {
		return nil, err
	}
	return &state, nil
}

// ListStatesByEnvironment 返回环境下的全部状态行(用于父级操作检查与初始化)。
func (dao *FreezeDAO) ListStatesByEnvironment(ctx context.Context, db *gorm.DB, environmentKey string) ([]model.ConfigFreezeState, error) {
	var states []model.ConfigFreezeState
	err := db.WithContext(ctx).
		Where("environment_key = ?", environmentKey).
		Find(&states).Error
	if err != nil {
		return nil, err
	}
	return states, nil
}

// EnsureState 幂等补建范围状态行(unlocked, revision=0);已存在时不改动任何既有状态。
func (dao *FreezeDAO) EnsureState(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string) error {
	state := model.ConfigFreezeState{
		EnvironmentKey: environmentKey,
		PipelineKey:    pipelineKey,
		State:          model.FreezeStateUnlocked,
		Revision:       0,
	}
	return db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&state).Error
}

// ApplyTransition 以 revision 为条件执行状态迁移。
// 仅当 (environment_key, pipeline_key, revision) 命中且目标状态尚不等于 desiredState 时更新成功。
// 返回 false 表示并发冲突或状态未变化,由调用方读取最新状态后决定错误类型。
func (dao *FreezeDAO) ApplyTransition(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string, expectedRevision int64, desiredState, actor, reason string, changedAt any) (bool, error) {
	result := db.WithContext(ctx).
		Model(&model.ConfigFreezeState{}).
		Where("environment_key = ? AND pipeline_key = ? AND revision = ? AND state <> ?",
			environmentKey, pipelineKey, expectedRevision, desiredState).
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

// AppendEvent 追加一条冻结事件;事件为追加记录,不更新也不删除历史。
func (dao *FreezeDAO) AppendEvent(ctx context.Context, db *gorm.DB, event *model.ConfigFreezeEvent) error {
	return db.WithContext(ctx).Create(event).Error
}

// ListEvents 按事件 ID 倒序返回分页结果;多取一条用于计算 has_more。
func (dao *FreezeDAO) ListEvents(ctx context.Context, db *gorm.DB, environmentKey, pipelineKey string, limit int, beforeEventID uint) ([]model.ConfigFreezeEvent, bool, error) {
	query := db.WithContext(ctx).
		Where("environment_key = ? AND pipeline_key = ?", environmentKey, pipelineKey)
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

// IsStateMissing 判定错误是否代表状态行不存在。
func IsStateMissing(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
