package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

// MigrateScopeIdentity assigns stable internal identities to legacy rows,
// then replaces the old business-key uniqueness with generation-aware
// constraints. It is idempotent and runs before HTTP starts.
//
// The supported production path is SQLite; MySQL/PostgreSQL use the same
// logical steps and refuse on ambiguity instead of guessing.
func MigrateScopeIdentity(ctx context.Context, db *gorm.DB) error {
	if err := backfillEnvironments(ctx, db); err != nil {
		return err
	}
	if err := backfillPipelines(ctx, db); err != nil {
		return err
	}
	if err := backfillScopeChildren(ctx, db); err != nil {
		return err
	}
	if err := dropLegacyKeyIndexes(ctx, db); err != nil {
		return err
	}
	if err := createScopeIdentityIndexes(ctx, db); err != nil {
		return err
	}
	return nil
}

type legacyEnvironmentRow struct {
	ID             uint
	EnvironmentKey string
	EnvironmentID  string
	ActiveSlot     *string
	DeletedAt      *time.Time
}

type legacyPipelineRow struct {
	ID             uint
	EnvironmentKey string
	PipelineKey    string
	ScopeID        string
	EnvironmentID  string
	ActiveSlot     *string
	DeletedAt      *time.Time
}

func backfillEnvironments(ctx context.Context, db *gorm.DB) error {
	var environments []legacyEnvironmentRow
	if err := db.WithContext(ctx).Raw(
		`SELECT id, environment_key, COALESCE(environment_id,'') AS environment_id,
		        active_slot, deleted_at FROM environment ORDER BY id ASC`).Scan(&environments).Error; err != nil {
		return err
	}
	for i := range environments {
		env := &environments[i]
		updates := map[string]any{}
		if strings.TrimSpace(env.EnvironmentID) == "" {
			updates["environment_id"] = uuid.NewString()
		}
		if env.DeletedAt != nil {
			updates["active_slot"] = nil
		} else {
			updates["active_slot"] = model.ActiveSlotActive
		}
		if len(updates) == 0 {
			continue
		}
		if err := db.WithContext(ctx).Unscoped().Model(&model.Environment{}).
			Where("id = ?", env.ID).Updates(updates).Error; err != nil {
			return err
		}
	}
	return ensureNoDuplicateActive(ctx, db, "environment",
		"environment_key", "", "environment")
}

func backfillPipelines(ctx context.Context, db *gorm.DB) error {
	environmentsByKey, err := loadLegacyEnvironmentMap(ctx, db)
	if err != nil {
		return err
	}
	var pipelines []legacyPipelineRow
	if err := db.WithContext(ctx).Raw(
		`SELECT id, environment_key, pipeline_key, COALESCE(scope_id,'') AS scope_id,
		        COALESCE(environment_id,'') AS environment_id, active_slot, deleted_at
		 FROM pipeline ORDER BY id ASC`).Scan(&pipelines).Error; err != nil {
		return err
	}
	for i := range pipelines {
		pipe := &pipelines[i]
		env, ok := environmentsByKey[pipe.EnvironmentKey]
		if !ok {
			return fmt.Errorf("scope migration: pipeline %d references unknown environment_key %q", pipe.ID, pipe.EnvironmentKey)
		}
		updates := map[string]any{}
		if strings.TrimSpace(pipe.ScopeID) == "" {
			updates["scope_id"] = uuid.NewString()
		}
		if strings.TrimSpace(pipe.EnvironmentID) == "" {
			updates["environment_id"] = env.EnvironmentID
		}
		if pipe.DeletedAt != nil {
			updates["active_slot"] = nil
		} else {
			updates["active_slot"] = model.ActiveSlotActive
		}
		if len(updates) == 0 {
			continue
		}
		if err := db.WithContext(ctx).Unscoped().Model(&model.Pipeline{}).
			Where("id = ?", pipe.ID).Updates(updates).Error; err != nil {
			return err
		}
	}
	return ensureNoDuplicateActive(ctx, db, "pipeline",
		"environment_id", "pipeline_key", "pipeline")
}

// loadLegacyEnvironmentMap chooses the oldest row for each key. Before this
// migration the database enforced one row per key, so there is no ambiguity;
// the choice matters only for partially migrated diagnostic states.
func loadLegacyEnvironmentMap(ctx context.Context, db *gorm.DB) (map[string]legacyEnvironmentRow, error) {
	var environments []legacyEnvironmentRow
	if err := db.WithContext(ctx).Raw(
		`SELECT id, environment_key, COALESCE(environment_id,'') AS environment_id,
		        active_slot, deleted_at FROM environment ORDER BY id ASC`).Scan(&environments).Error; err != nil {
		return nil, err
	}
	result := make(map[string]legacyEnvironmentRow, len(environments))
	for _, env := range environments {
		if _, exists := result[env.EnvironmentKey]; !exists {
			result[env.EnvironmentKey] = env
		}
	}
	return result, nil
}

type scopeBusinessKey struct{ EnvironmentKey, PipelineKey string }

func backfillScopeChildren(ctx context.Context, db *gorm.DB) error {
	var pipelines []model.Pipeline
	if err := db.WithContext(ctx).Unscoped().Order("id ASC").Find(&pipelines).Error; err != nil {
		return err
	}
	scopeByKeys := make(map[scopeBusinessKey]string, len(pipelines))
	for _, pipe := range pipelines {
		k := scopeBusinessKey{EnvironmentKey: pipe.EnvironmentKey, PipelineKey: pipe.PipelineKey}
		if _, exists := scopeByKeys[k]; !exists {
			scopeByKeys[k] = pipe.ScopeID
		}
	}
	for _, table := range []string{
		"resource_config", "asset",
		"config_freeze_state", "config_freeze_event",
		"config_release_version", "config_release_head", "config_release_event",
	} {
		if err := backfillTableScope(ctx, db, table, scopeByKeys); err != nil {
			return err
		}
	}
	return nil
}

type childScopeRow struct {
	ID             uint
	EnvironmentKey string
	PipelineKey    string
}

func backfillTableScope(ctx context.Context, db *gorm.DB, table string,
	scopeByKeys map[scopeBusinessKey]string) error {
	var rows []childScopeRow
	if err := db.WithContext(ctx).Unscoped().Table(table).
		Select("id, environment_key, pipeline_key").
		Where("scope_id IS NULL OR scope_id = ''").
		Scan(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		k := scopeBusinessKey{row.EnvironmentKey, row.PipelineKey}
		scopeID, ok := scopeByKeys[k]
		if !ok || scopeID == "" {
			return fmt.Errorf("scope migration: %s row %d has no matching pipeline for %q/%q",
				table, row.ID, row.EnvironmentKey, row.PipelineKey)
		}
		if err := db.WithContext(ctx).Unscoped().Table(table).
			Where("id = ?", row.ID).Update("scope_id", scopeID).Error; err != nil {
			return err
		}
	}
	return nil
}

func ensureNoDuplicateActive(ctx context.Context, db *gorm.DB, table, firstColumn, secondColumn, label string) error {
	columns := "COALESCE(" + firstColumn + ", '')"
	group := columns
	if secondColumn != "" {
		group += ", COALESCE(" + secondColumn + ", '')"
	}
	var rows []struct {
		ScopeKey string
		Count    int64
	}
	keyExpr := fmt.Sprintf("%s || '|' || COALESCE(%s,'')", columns, secondColumn)
	if db.Dialector.Name() == "mysql" {
		keyExpr = fmt.Sprintf("CONCAT(%s,'|',COALESCE(%s,''))", columns, secondColumn)
	}
	query := fmt.Sprintf(
		"SELECT %s AS scope_key, COUNT(*) AS count FROM %s WHERE active_slot = ? GROUP BY %s HAVING COUNT(*) > 1",
		keyExpr, table, group)
	if secondColumn == "" {
		query = fmt.Sprintf(
			"SELECT %s AS scope_key, COUNT(*) AS count FROM %s WHERE active_slot = ? GROUP BY %s HAVING COUNT(*) > 1",
			firstColumn, table, firstColumn)
	}
	if err := db.WithContext(ctx).Raw(query, model.ActiveSlotActive).Scan(&rows).Error; err != nil {
		return err
	}
	if len(rows) > 0 {
		return fmt.Errorf("scope migration: multiple active %s rows share key %q", label, rows[0].ScopeKey)
	}
	return nil
}

func dropLegacyKeyIndexes(ctx context.Context, db *gorm.DB) error {
	indexes := []struct{ Name, Table string }{
		{"uk_environment_key", "environment"},
		{"uk_env_pipeline", "pipeline"},
		{"uk_config_resource", "resource_config"},
		{"uk_freeze_scope", "config_freeze_state"},
		{"uk_release_scope_version", "config_release_version"},
		{"uk_release_head_scope", "config_release_head"},
		{"uk_config_active_alias", "resource_config"},
	}
	for _, index := range indexes {
		if err := dropIndexIfExists(ctx, db, index.Table, index.Name); err != nil {
			return err
		}
	}
	return nil
}

func createScopeIdentityIndexes(ctx context.Context, db *gorm.DB) error {
	indexes := []struct {
		Name, Table string
		Columns     string
	}{
		{"uk_environment_id", "environment", "environment_id"},
		{"uk_environment_slot", "environment", "environment_key, active_slot"},
		{"uk_pipeline_scope_id", "pipeline", "scope_id"},
		{"uk_pipeline_slot", "pipeline", "environment_id, pipeline_key, active_slot"},
		{"uk_config_scope_resource", "resource_config", "scope_id, resource_key"},
		{"uk_freeze_scope_id", "config_freeze_state", "scope_id"},
		{"uk_release_version_scope", "config_release_version", "scope_id, version"},
		{"uk_release_head_scope_id", "config_release_head", "scope_id"},
	}
	for _, index := range indexes {
		if err := createUniqueIndexIfMissing(ctx, db, index.Table, index.Name, index.Columns); err != nil {
			return fmt.Errorf("scope migration: create %s: %w", index.Name, err)
		}
	}
	return nil
}

func dropIndexIfExists(ctx context.Context, db *gorm.DB, table, name string) error {
	switch db.Dialector.Name() {
	case "sqlite", "postgres":
		return db.WithContext(ctx).Exec("DROP INDEX IF EXISTS " + name).Error
	case "mysql":
		if !mysqlIndexExists(ctx, db, table, name) {
			return nil
		}
		return db.WithContext(ctx).Exec(fmt.Sprintf("DROP INDEX `%s` ON `%s`", name, table)).Error
	default:
		return fmt.Errorf("scope migration unsupported driver %s", db.Dialector.Name())
	}
}

func createUniqueIndexIfMissing(ctx context.Context, db *gorm.DB, table, name, columns string) error {
	switch db.Dialector.Name() {
	case "sqlite", "postgres":
		return db.WithContext(ctx).Exec(fmt.Sprintf(
			"CREATE UNIQUE INDEX IF NOT EXISTS %s ON %s(%s)", name, table, columns)).Error
	case "mysql":
		if mysqlIndexExists(ctx, db, table, name) {
			return nil
		}
		return db.WithContext(ctx).Exec(fmt.Sprintf(
			"CREATE UNIQUE INDEX `%s` ON `%s`(%s)", name, table, columns)).Error
	default:
		return fmt.Errorf("scope migration unsupported driver %s", db.Dialector.Name())
	}
}

func mysqlIndexExists(ctx context.Context, db *gorm.DB, table, name string) bool {
	var count int64
	if err := db.WithContext(ctx).Raw(
		"SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?",
		table, name).Scan(&count).Error; err != nil {
		return false
	}
	return count > 0
}
