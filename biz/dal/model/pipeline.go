package model

import (
	"time"

	"gorm.io/gorm"
)

// Pipeline represents a pipeline entity for configuration isolation.
type Pipeline struct {
	ID             uint           `gorm:"primaryKey" json:"id,omitempty"`
	CreatedAt      time.Time      `json:"created_at,omitempty"`
	UpdatedAt      time.Time      `json:"updated_at,omitempty"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
	ScopeID        string         `gorm:"column:scope_id;size:36;not null;default:'';index:idx_pipeline_identity" json:"scope_id,omitempty"`
	EnvironmentID  string         `gorm:"column:environment_id;size:36;not null;default:'';index:idx_pipeline_environment" json:"environment_id,omitempty"`
	ActiveSlot     *string        `gorm:"column:active_slot;size:16;index:idx_pipeline_active_slot" json:"-"`
	EnvironmentKey string         `gorm:"column:environment_key;index:idx_pipeline_env_key" json:"environment_key,omitempty"`
	PipelineKey    string         `gorm:"column:pipeline_key;index:idx_pipeline_key" json:"pipeline_key,omitempty"`
	PipelineName   string         `gorm:"column:pipeline_name" json:"pipeline_name,omitempty"`
	Description    string         `gorm:"column:description;type:varchar(512)" json:"description,omitempty"`
	SortOrder      int            `gorm:"column:sort_order;default:0;index:idx_pipeline_active_sort,priority:2" json:"sort_order,omitempty"`
	IsActive       bool           `gorm:"column:is_active;default:true;index:idx_pipeline_active_sort,priority:1" json:"is_active,omitempty"`
}

// TableName overrides gorm to use pipeline table.
func (Pipeline) TableName() string {
	return "pipeline"
}
