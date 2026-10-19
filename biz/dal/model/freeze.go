package model

import "time"

// 冻结状态取值:只有 unlocked 与 locked 两种。
const (
	FreezeStateUnlocked = "unlocked"
	FreezeStateLocked   = "locked"
)

// ConfigFreezeState 按 environment_key + pipeline_key 记录当前冻结状态与 revision。
// 该表只保存业务状态;admin_configured 由运行配置计算,不落库。
type ConfigFreezeState struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	EnvironmentKey string     `gorm:"uniqueIndex:uk_freeze_scope,priority:1;size:191;not null" json:"environment_key"`
	PipelineKey    string     `gorm:"uniqueIndex:uk_freeze_scope,priority:2;size:191;not null" json:"pipeline_key"`
	State          string     `gorm:"size:16;not null;default:unlocked" json:"state"`
	Revision       int64      `gorm:"not null;default:0" json:"revision"`
	LastChangedAt  *time.Time `json:"last_changed_at"`
	LastChangedBy  string     `gorm:"size:64;not null;default:''" json:"last_changed_by"`
	LastReason     string     `gorm:"size:500;not null;default:''" json:"last_reason"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (ConfigFreezeState) TableName() string { return "config_freeze_state" }

// ConfigFreezeEvent 为追加式审计记录;普通导入与父级删除都不清除它。
type ConfigFreezeEvent struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	EnvironmentKey string    `gorm:"index:idx_freeze_event_scope,priority:1;size:191;not null" json:"environment_key"`
	PipelineKey    string    `gorm:"index:idx_freeze_event_scope,priority:2;size:191;not null" json:"pipeline_key"`
	EventType      string    `gorm:"size:16;not null" json:"event_type"` // lock | unlock
	FromState      string    `gorm:"size:16;not null" json:"from_state"`
	ToState        string    `gorm:"size:16;not null" json:"to_state"`
	Revision       int64     `gorm:"not null" json:"revision"`
	Reason         string    `gorm:"size:500;not null" json:"reason"`
	Actor          string    `gorm:"size:64;not null" json:"actor"`
	CreatedAt      time.Time `json:"created_at"`
}

func (ConfigFreezeEvent) TableName() string { return "config_freeze_event" }

// ConfigWriteGate 是固定单行的写协调载体:所有业务写入与冻结状态变更,
// 在事务的第一条语句 UPDATE 本行以取得数据库级写协调。
type ConfigWriteGate struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	CoordinationCount int64     `gorm:"not null;default:0" json:"coordination_count"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func (ConfigWriteGate) TableName() string { return "config_write_gate" }

// ConfigWriteGateRowID 是协调行的固定主键。
const ConfigWriteGateRowID = 1
