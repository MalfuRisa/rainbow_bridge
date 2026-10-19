package db

import (
	"context"
	"errors"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// WriteGateDAO 维护固定单行写协调载体。
// 所有业务写入与冻结状态变更都必须在其事务的第一条语句 UPDATE 该行,
// 以在具体数据库(PostgreSQL/MySQL 行锁,SQLite 事务写锁)上取得写协调。
type WriteGateDAO struct{}

func NewWriteGateDAO() *WriteGateDAO { return &WriteGateDAO{} }

// EnsureGate 幂等创建固定协调行(id=1);已存在时不重置计数。
func (dao *WriteGateDAO) EnsureGate(ctx context.Context, db *gorm.DB) error {
	gate := model.ConfigWriteGate{ID: model.ConfigWriteGateRowID}
	return db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&gate).Error
}

// TouchGate 作为协调事务的第一条语句执行;返回错误表示未取得协调。
func (dao *WriteGateDAO) TouchGate(ctx context.Context, db *gorm.DB) error {
	result := db.WithContext(ctx).
		Exec("UPDATE config_write_gate SET coordination_count = coordination_count + 1, updated_at = ? WHERE id = ?",
			time.Now().UTC(), model.ConfigWriteGateRowID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("write gate row missing")
	}
	return nil
}
