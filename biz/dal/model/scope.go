package model

import "time"

// ActiveSlotActive marks the single live generation for a business key.
// Deleted generations keep a NULL active_slot, which allows the same key to be
// recreated without resurrecting the old row.
const ActiveSlotActive = "active"

// Scope lifecycle event types.
const (
	ScopeEventEnvironmentDelete = "environment_delete"
	ScopeEventPipelineDelete    = "pipeline_delete"
)

// ScopeLifecycleEvent is an append-only record of a whole-scope deletion.
// It deliberately stays separate from per-version delete events so a parent
// deletion is not disguised as many manual version deletions.
type ScopeLifecycleEvent struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	EventType          string    `gorm:"size:32;not null;index:idx_scope_event_type" json:"event_type"`
	EnvironmentID      string    `gorm:"size:36;not null;index:idx_scope_event_environment" json:"environment_id"`
	ScopeID            string    `gorm:"size:36;not null;index:idx_scope_event_scope" json:"scope_id"`
	EnvironmentKey     string    `gorm:"size:191;not null" json:"environment_key"`
	PipelineKey        string    `gorm:"size:191;not null;default:''" json:"pipeline_key"`
	Actor              string    `gorm:"size:64;not null" json:"actor"`
	Reason             string    `gorm:"size:500;not null;default:''" json:"reason"`
	AffectedPipelines  int       `gorm:"not null;default:0" json:"affected_pipelines"`
	AffectedConfigs    int       `gorm:"not null;default:0" json:"affected_configs"`
	AffectedAssets     int       `gorm:"not null;default:0" json:"affected_assets"`
	AffectedVersions   int       `gorm:"not null;default:0" json:"affected_versions"`
	CreatedAt          time.Time `json:"created_at"`
}

func (ScopeLifecycleEvent) TableName() string { return "scope_lifecycle_event" }

// ScopeDeleteResult is returned by environment/pipeline deletion.
type ScopeDeleteResult struct {
	EnvironmentID string
	ScopeID       string
	DeletedAt     time.Time
	Pipelines     int
	Configs       int
	Assets        int
	Versions      int
}