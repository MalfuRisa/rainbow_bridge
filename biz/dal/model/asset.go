package model

import (
	"time"

	"gorm.io/gorm"
)

// Asset stores metadata for uploaded files.
type Asset struct {
	ID             uint           `gorm:"primaryKey" json:"id,omitempty"`
	CreatedAt      time.Time      `json:"created_at,omitempty"`
	UpdatedAt      time.Time      `json:"updated_at,omitempty"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
	FileID         string         `gorm:"column:file_id;uniqueIndex:idx_file" json:"file_id,omitempty"`
	ScopeID        string         `gorm:"column:scope_id;size:36;not null;default:'';index:idx_asset_scope" json:"scope_id,omitempty"`
	EnvironmentKey string         `gorm:"column:environment_key;index:idx_asset_env;index:idx_asset_env_pipeline,priority:1" json:"environment_key,omitempty"`
	PipelineKey    string         `gorm:"column:pipeline_key;index:idx_asset_pipeline;index:idx_asset_env_pipeline,priority:2" json:"pipeline_key,omitempty"`
	FileName       string         `gorm:"column:file_name" json:"file_name,omitempty"`
	ContentType    string         `gorm:"column:content_type" json:"content_type,omitempty"`
	FileSize       int64          `gorm:"column:file_size" json:"file_size,omitempty"`
	Path           string         `gorm:"column:path;type:text" json:"path,omitempty"`
	Remark         string         `gorm:"column:remark;type:varchar(512)" json:"remark,omitempty"`
}

// TableName overrides gorm to use asset table.
func (Asset) TableName() string {
	return "asset"
}
