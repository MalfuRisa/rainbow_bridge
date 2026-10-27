package service

import (
	"context"
	"log"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

const (
	DefaultEnvironmentKey  = "default"
	DefaultEnvironmentName = "默认环境"
	DefaultPipelineKey     = "default"
	DefaultPipelineName    = "默认渠道"
)

// InitDefaultEnvironment creates the default environment only when the
// database has never contained any environment row.
func (s *Service) InitDefaultEnvironment(ctx context.Context) error {
	count, err := s.logic.environmentDAO.CountAllIncludingDeleted(ctx, s.logic.db)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	env := &model.Environment{
		EnvironmentID:   uuid.NewString(),
		EnvironmentKey:  DefaultEnvironmentKey,
		EnvironmentName: DefaultEnvironmentName,
		Description:     "系统默认环境",
		SortOrder:       0,
		IsActive:        true,
	}
	active := model.ActiveSlotActive
	env.ActiveSlot = &active
	if err := s.logic.db.WithContext(ctx).Create(env).Error; err != nil {
		return err
	}
	log.Printf("[Init] Created default environment: %s", DefaultEnvironmentKey)
	return nil
}

// InitDefaultPipeline creates the default pipeline only when the database has
// never contained any pipeline row.
func (s *Service) InitDefaultPipeline(ctx context.Context) error {
	count, err := s.logic.pipelineDAO.CountAllIncludingDeleted(ctx, s.logic.db)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	env, err := s.logic.environmentDAO.GetByKey(ctx, s.logic.db, DefaultEnvironmentKey)
	if err != nil {
		// A user who deleted the default environment must not get it back.
		return nil
	}
	pl := &model.Pipeline{
		ScopeID:        uuid.NewString(),
		EnvironmentID:  env.EnvironmentID,
		EnvironmentKey: DefaultEnvironmentKey,
		PipelineKey:    DefaultPipelineKey,
		PipelineName:   DefaultPipelineName,
		Description:    "系统默认渠道",
		SortOrder:      0,
		IsActive:       true,
	}
	active := model.ActiveSlotActive
	pl.ActiveSlot = &active
	if err := s.logic.db.WithContext(ctx).Create(pl).Error; err != nil {
		return err
	}
	log.Printf("[Init] Created default pipeline: %s", DefaultPipelineKey)
	return nil
}

func (s *Service) InitDefaults(ctx context.Context) error {
	if err := s.InitDefaultEnvironment(ctx); err != nil {
		return err
	}
	return s.InitDefaultPipeline(ctx)
}

// MigrateConfigDefaults sets default environment_key and pipeline_key for
// existing configs before scope identity backfill.
func MigrateConfigDefaults(db *gorm.DB) error {
	var rows []model.Config
	if err := db.Where("environment_key = '' OR environment_key IS NULL OR pipeline_key = '' OR pipeline_key IS NULL").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		fields := map[string]any{"edit_revision": uuid.NewString()}
		if row.EnvironmentKey == "" {
			fields["environment_key"] = DefaultEnvironmentKey
		}
		if row.PipelineKey == "" {
			fields["pipeline_key"] = DefaultPipelineKey
		}
		if err := db.Model(&model.Config{}).Where("id = ? AND (environment_key = '' OR environment_key IS NULL OR pipeline_key = '' OR pipeline_key IS NULL)", row.ID).Updates(fields).Error; err != nil {
			return err
		}
	}
	return nil
}

// EnsureSystemDefaults seeds only a never-initialized database. Rows are
// counted including soft-deleted generations, so a user who deleted the
// default environment/pipeline does not have them recreated on every restart.
func EnsureSystemDefaults(ctx context.Context, dbConn *gorm.DB) error {
	envDAO := db.NewEnvironmentDAO()
	plDAO := db.NewPipelineDAO()

	envCount, err := envDAO.CountAllIncludingDeleted(ctx, dbConn)
	if err != nil {
		return err
	}
	pipelineCount, err := plDAO.CountAllIncludingDeleted(ctx, dbConn)
	if err != nil {
		return err
	}
	if envCount > 0 || pipelineCount > 0 {
		return nil
	}

	active := model.ActiveSlotActive
	env := &model.Environment{
		EnvironmentID:   uuid.NewString(),
		EnvironmentKey:  DefaultEnvironmentKey,
		EnvironmentName: DefaultEnvironmentName,
		Description:     "系统默认环境",
		SortOrder:       0,
		IsActive:        true,
		ActiveSlot:      &active,
	}
	if err := dbConn.WithContext(ctx).Create(env).Error; err != nil {
		return err
	}
	pl := &model.Pipeline{
		ScopeID:        uuid.NewString(),
		EnvironmentID:  env.EnvironmentID,
		EnvironmentKey: DefaultEnvironmentKey,
		PipelineKey:    DefaultPipelineKey,
		PipelineName:   DefaultPipelineName,
		Description:    "系统默认渠道",
		SortOrder:      0,
		IsActive:       true,
		ActiveSlot:     &active,
	}
	if err := dbConn.WithContext(ctx).Create(pl).Error; err != nil {
		return err
	}
	log.Printf("[Init] Created default environment and pipeline")
	return nil
}