package db

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	appdatabase "github.com/yi-nology/rainbow_bridge/pkg/database"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMigrateScopeIdentityBackfillsAndAllowsSameKeyRebuild(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	ctx := context.Background()

	legacyDDL := []string{
		`CREATE TABLE environment (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME, environment_key TEXT)`,
		`CREATE UNIQUE INDEX uk_environment_key ON environment(environment_key)`,
		`CREATE TABLE pipeline (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME, environment_key TEXT, pipeline_key TEXT)`,
		`CREATE UNIQUE INDEX uk_env_pipeline ON pipeline(environment_key, pipeline_key)`,
		`CREATE TABLE resource_config (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME, environment_key TEXT, pipeline_key TEXT, resource_key TEXT, alias TEXT, edit_revision TEXT)`,
		`CREATE UNIQUE INDEX uk_config_resource ON resource_config(environment_key, pipeline_key, resource_key)`,
		`CREATE TABLE asset (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME, file_id TEXT, environment_key TEXT, pipeline_key TEXT, path TEXT)`,
		`CREATE TABLE config_freeze_state (id INTEGER PRIMARY KEY AUTOINCREMENT, environment_key TEXT, pipeline_key TEXT, state TEXT, revision INTEGER)`,
		`CREATE UNIQUE INDEX uk_freeze_scope ON config_freeze_state(environment_key, pipeline_key)`,
		`CREATE TABLE config_freeze_event (id INTEGER PRIMARY KEY AUTOINCREMENT, environment_key TEXT, pipeline_key TEXT)`,
		`CREATE TABLE config_release_version (id INTEGER PRIMARY KEY AUTOINCREMENT, environment_key TEXT, pipeline_key TEXT, snapshot_id TEXT, version TEXT, deleted_at DATETIME)`,
		`CREATE UNIQUE INDEX uk_release_scope_version ON config_release_version(environment_key, pipeline_key, version)`,
		`CREATE TABLE config_release_asset (id INTEGER PRIMARY KEY AUTOINCREMENT, snapshot_id TEXT, asset_id TEXT)`,
		`CREATE TABLE config_release_head (id INTEGER PRIMARY KEY AUTOINCREMENT, environment_key TEXT, pipeline_key TEXT)`,
		`CREATE UNIQUE INDEX uk_release_head_scope ON config_release_head(environment_key, pipeline_key)`,
		`CREATE TABLE config_release_event (id INTEGER PRIMARY KEY AUTOINCREMENT, environment_key TEXT, pipeline_key TEXT)`,
		`CREATE INDEX uk_config_active_alias ON resource_config(environment_key, pipeline_key, alias)`,
	}
	for _, ddl := range legacyDDL {
		if err := conn.Exec(ddl).Error; err != nil {
			t.Fatalf("legacy ddl: %v", err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO environment (environment_key) VALUES ('prod')`,
		`INSERT INTO pipeline (environment_key, pipeline_key) VALUES ('prod', 'main')`,
		`INSERT INTO resource_config (environment_key, pipeline_key, resource_key, alias) VALUES ('prod', 'main', 'rk', 'alias')`,
		`INSERT INTO asset (file_id, environment_key, pipeline_key, path) VALUES ('file-1', 'prod', 'main', 'uploads/file-1/a.txt')`,
		`INSERT INTO config_freeze_state (environment_key, pipeline_key, state) VALUES ('prod', 'main', 'unlocked')`,
		`INSERT INTO config_freeze_event (environment_key, pipeline_key) VALUES ('prod', 'main')`,
		`INSERT INTO config_release_version (environment_key, pipeline_key, snapshot_id, version) VALUES ('prod', 'main', 'snap-1', 'v1.0')`,
		`INSERT INTO config_release_asset (snapshot_id, asset_id) VALUES ('snap-1', 'ra-1')`,
		`INSERT INTO config_release_head (environment_key, pipeline_key) VALUES ('prod', 'main')`,
		`INSERT INTO config_release_event (environment_key, pipeline_key) VALUES ('prod', 'main')`,
	} {
		if err := conn.Exec(statement).Error; err != nil {
			t.Fatalf("legacy row: %v", err)
		}
	}

	for _, column := range []string{
		`ALTER TABLE environment ADD COLUMN environment_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE environment ADD COLUMN active_slot TEXT`,
		`ALTER TABLE pipeline ADD COLUMN scope_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE pipeline ADD COLUMN environment_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE pipeline ADD COLUMN active_slot TEXT`,
		`ALTER TABLE resource_config ADD COLUMN scope_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE asset ADD COLUMN scope_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE config_freeze_state ADD COLUMN scope_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE config_freeze_event ADD COLUMN scope_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE config_release_version ADD COLUMN scope_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE config_release_head ADD COLUMN scope_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE config_release_event ADD COLUMN scope_id TEXT NOT NULL DEFAULT ''`,
	} {
		if err := conn.Exec(column).Error; err != nil {
			t.Fatalf("add identity column: %v", err)
		}
	}

	if err := MigrateScopeIdentity(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var env model.Environment
	if err := conn.Unscoped().Where("environment_key = 'prod'").First(&env).Error; err != nil {
		t.Fatal(err)
	}
	if env.EnvironmentID == "" || env.ActiveSlot == nil || *env.ActiveSlot != model.ActiveSlotActive {
		t.Fatalf("environment identity not backfilled: %+v", env)
	}
	var pipeline model.Pipeline
	if err := conn.Unscoped().Where("pipeline_key = 'main'").First(&pipeline).Error; err != nil {
		t.Fatal(err)
	}
	if pipeline.ScopeID == "" || pipeline.EnvironmentID != env.EnvironmentID {
		t.Fatalf("pipeline identity not backfilled: %+v", pipeline)
	}
	for _, table := range []string{"resource_config", "asset", "config_freeze_state",
		"config_freeze_event", "config_release_version", "config_release_head", "config_release_event"} {
		var count int64
		if err := conn.Table(table).Where("scope_id = ?", pipeline.ScopeID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s scope_id backfill count=%d", table, count)
		}
	}

	for _, oldIndex := range []string{"uk_environment_key", "uk_env_pipeline", "uk_config_resource",
		"uk_freeze_scope", "uk_release_scope_version", "uk_release_head_scope"} {
		var count int64
		if err := conn.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", oldIndex).
			Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("legacy index %s still exists", oldIndex)
		}
	}
	for _, newIndex := range []string{"uk_environment_id", "uk_environment_slot", "uk_pipeline_scope_id",
		"uk_pipeline_slot", "uk_config_scope_resource", "uk_freeze_scope_id",
		"uk_release_version_scope", "uk_release_head_scope_id"} {
		var count int64
		if err := conn.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", newIndex).
			Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("new index %s missing", newIndex)
		}
	}

	// Restarting the migration is idempotent.
	if err := MigrateScopeIdentity(ctx, conn); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	var after model.Pipeline
	if err := conn.Unscoped().Where("scope_id = ?", pipeline.ScopeID).First(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.ScopeID != pipeline.ScopeID {
		t.Fatal("idempotent migration changed an existing identity")
	}
	if err := appdatabase.PrepareConfigAliasConstraint(ctx, conn); err != nil {
		t.Fatalf("scope alias constraint: %v", err)
	}
}
