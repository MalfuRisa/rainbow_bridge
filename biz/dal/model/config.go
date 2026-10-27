package model

import (
	"time"

	"gorm.io/gorm"
)

// Config represents a configuration resource persisted in storage.
type Config struct {
	EditRevision   string         `gorm:"column:edit_revision;type:varchar(36)" json:"edit_revision,omitempty"`
	ID             uint           `gorm:"primaryKey" json:"id,omitempty"`
	CreatedAt      time.Time      `json:"created_at,omitempty"`
	UpdatedAt      time.Time      `json:"updated_at,omitempty"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
	ScopeID        string         `gorm:"column:scope_id;size:36;not null;default:'';index:idx_config_scope" json:"scope_id,omitempty"`
	EnvironmentKey string         `gorm:"column:environment_key;index:idx_config_env" json:"environment_key,omitempty"`
	PipelineKey    string         `gorm:"column:pipeline_key;index:idx_config_pipeline" json:"pipeline_key,omitempty"`
	ResourceKey    string         `gorm:"column:resource_key" json:"resource_key,omitempty"`
	Alias          string         `gorm:"column:alias;index:idx_config_alias" json:"alias,omitempty"`
	Name           string         `gorm:"column:name" json:"name,omitempty"`
	Content        string         `gorm:"column:content;type:text" json:"content,omitempty"`
	Type           string         `gorm:"column:type;index:idx_config_type" json:"type,omitempty"`
	Remark         string         `gorm:"column:remark;type:varchar(512)" json:"remark,omitempty"`
	IsPerm         bool           `gorm:"column:is_perm" json:"is_perm,omitempty"`
	Description    string         `gorm:"column:description;type:text" json:"description,omitempty"`
}

// TableName overrides gorm to use resource_config table.
func (Config) TableName() string {
	return "resource_config"
}
