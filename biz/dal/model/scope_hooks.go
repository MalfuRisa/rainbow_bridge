package model

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// These hooks keep direct model creates (tests, seeds and maintenance tools)
// coherent with the identity model. Production services set the same fields
// explicitly before entering the write transaction.

func (e *Environment) BeforeCreate(tx *gorm.DB) error {
	if e.EnvironmentID == "" {
		e.EnvironmentID = uuid.NewString()
	}
	if e.ActiveSlot == nil && !e.DeletedAt.Valid {
		active := ActiveSlotActive
		e.ActiveSlot = &active
	}
	return nil
}

func (p *Pipeline) BeforeCreate(tx *gorm.DB) error {
	if p.ScopeID == "" {
		p.ScopeID = uuid.NewString()
	}
	if p.EnvironmentID == "" && p.EnvironmentKey != "" {
		var env Environment
		if err := tx.Unscoped().Where("environment_key = ? AND deleted_at IS NULL", p.EnvironmentKey).
			Order("id ASC").First(&env).Error; err == nil {
			p.EnvironmentID = env.EnvironmentID
		}
	}
	if p.ActiveSlot == nil && !p.DeletedAt.Valid {
		active := ActiveSlotActive
		p.ActiveSlot = &active
	}
	return nil
}

func (c *Config) BeforeCreate(tx *gorm.DB) error {
	if c.ScopeID == "" {
		c.ScopeID = lookupScopeID(tx, c.EnvironmentKey, c.PipelineKey)
	}
	return nil
}

func (a *Asset) BeforeCreate(tx *gorm.DB) error {
	if a.ScopeID == "" {
		a.ScopeID = lookupScopeID(tx, a.EnvironmentKey, a.PipelineKey)
	}
	return nil
}

func (s *ConfigFreezeState) BeforeCreate(tx *gorm.DB) error {
	if s.ScopeID == "" {
		s.ScopeID = lookupScopeID(tx, s.EnvironmentKey, s.PipelineKey)
	}
	return nil
}

func (e *ConfigFreezeEvent) BeforeCreate(tx *gorm.DB) error {
	if e.ScopeID == "" {
		e.ScopeID = lookupScopeID(tx, e.EnvironmentKey, e.PipelineKey)
	}
	return nil
}

func (v *ConfigReleaseVersion) BeforeCreate(tx *gorm.DB) error {
	if v.ScopeID == "" {
		v.ScopeID = lookupScopeID(tx, v.EnvironmentKey, v.PipelineKey)
	}
	return nil
}

func (h *ConfigReleaseHead) BeforeCreate(tx *gorm.DB) error {
	if h.ScopeID == "" {
		h.ScopeID = lookupScopeID(tx, h.EnvironmentKey, h.PipelineKey)
	}
	return nil
}

func (e *ConfigReleaseEvent) BeforeCreate(tx *gorm.DB) error {
	if e.ScopeID == "" {
		e.ScopeID = lookupScopeID(tx, e.EnvironmentKey, e.PipelineKey)
	}
	return nil
}

func lookupScopeID(tx *gorm.DB, environmentKey, pipelineKey string) string {
	if environmentKey == "" || pipelineKey == "" {
		return ""
	}
	var scopeID string
	err := tx.Raw(`SELECT scope_id FROM pipeline
		WHERE environment_key = ? AND pipeline_key = ? AND active_slot = ? AND deleted_at IS NULL
		ORDER BY id ASC LIMIT 1`, environmentKey, pipelineKey, ActiveSlotActive).
		Row().Scan(&scopeID)
	if err == nil && scopeID != "" {
		return scopeID
	}
	err = tx.Raw(`SELECT scope_id FROM pipeline
		WHERE environment_key = ? AND pipeline_key = ? AND deleted_at IS NULL
		ORDER BY id ASC LIMIT 1`, environmentKey, pipelineKey).
		Row().Scan(&scopeID)
	if err == nil {
		return scopeID
	}
	return ""
}