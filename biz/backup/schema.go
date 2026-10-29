package backup

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	appconfig "github.com/yi-nology/rainbow_bridge/pkg/config"
	"github.com/yi-nology/rainbow_bridge/pkg/database"
	"gorm.io/gorm"
)

// tableSpec is the explicit business allowlist. Column lists mirror the
// current GORM models and are never derived from uploaded DDL.
type tableSpec struct {
	Name    string
	Columns []string
}

// schemaV2Tables is the frozen schema-v2 allowlist. Every scope generation,
// including deleted ones, is preserved. It intentionally lacks the release
// number counter so existing schema-2 packages keep restoring.
var schemaV2Tables = []tableSpec{
	{Name: "environment", Columns: []string{
		"id", "created_at", "updated_at", "deleted_at", "environment_id", "active_slot",
		"environment_key", "environment_name", "description", "sort_order", "is_active",
	}},
	{Name: "pipeline", Columns: []string{
		"id", "created_at", "updated_at", "deleted_at", "scope_id", "environment_id", "active_slot",
		"environment_key", "pipeline_key", "pipeline_name", "description", "sort_order", "is_active",
	}},
	{Name: "resource_config", Columns: []string{
		"edit_revision", "id", "created_at", "updated_at", "deleted_at", "scope_id",
		"environment_key", "pipeline_key", "resource_key", "alias", "name",
		"content", "type", "remark", "is_perm", "description",
	}},
	{Name: "asset", Columns: []string{
		"id", "created_at", "updated_at", "deleted_at", "file_id", "scope_id",
		"environment_key", "pipeline_key", "file_name", "content_type",
		"file_size", "path", "remark",
	}},
	{Name: "config_freeze_state", Columns: []string{
		"id", "scope_id", "environment_key", "pipeline_key", "state", "revision",
		"last_changed_at", "last_changed_by", "last_reason", "created_at", "updated_at",
	}},
	{Name: "config_freeze_event", Columns: []string{
		"id", "scope_id", "environment_key", "pipeline_key", "event_type", "from_state",
		"to_state", "revision", "reason", "actor", "created_at",
	}},
	{Name: "config_release_version", Columns: []string{
		"id", "snapshot_id", "scope_id", "environment_key", "pipeline_key", "version",
		"creation_request_id", "request_fingerprint", "source_freeze_revision",
		"environment_name", "pipeline_name", "payload", "payload_sha256",
		"manifest_sha256", "config_count", "asset_count", "asset_bytes",
		"external_references", "reason", "actor", "created_at", "deleted_at",
		"deleted_by", "delete_reason",
	}},
	{Name: "config_release_asset", Columns: []string{
		"id", "snapshot_id", "asset_id", "source_file_id", "file_name",
		"content_type", "size", "sha256", "created_at",
	}},
	{Name: "config_release_head", Columns: []string{
		"id", "scope_id", "environment_key", "pipeline_key", "active_snapshot_id", "revision",
		"changed_at", "changed_by", "changed_reason", "created_at", "updated_at",
	}},
	{Name: "config_release_event", Columns: []string{
		"id", "scope_id", "event_type", "request_id", "request_fingerprint", "environment_key",
		"pipeline_key", "snapshot_id", "version", "from_active_snapshot",
		"to_active_snapshot", "from_head_revision", "to_head_revision",
		"reason", "actor", "created_at",
	}},
	{Name: "scope_lifecycle_event", Columns: []string{
		"id", "event_type", "environment_id", "scope_id", "environment_key",
		"pipeline_key", "actor", "reason", "affected_pipelines", "affected_configs",
		"affected_assets", "affected_versions", "created_at",
	}},
}

// businessTables is the current schema-v3 allowlist: schema 2 plus the
// per-scope last_version_number counter.
var schemaV3Tables = withReleaseNumberColumn(schemaV2Tables)
var businessTables = withReleaseStorageColumns(schemaV3Tables)

func withReleaseStorageColumns(base []tableSpec) []tableSpec {
	result := make([]tableSpec, len(base))
	copy(result, base)
	for i := range result {
		if result[i].Name == "config_release_version" {
			result[i].Columns = append(append([]string{}, result[i].Columns...), "storage_backend", "storage_bucket", "storage_prefix", "storage_attempt", "legacy_local")
		}
	}
	return result
}

func withReleaseNumberColumn(base []tableSpec) []tableSpec {
	result := make([]tableSpec, len(base))
	copy(result, base)
	for index := range result {
		if result[index].Name != "config_release_head" {
			continue
		}
		columns := make([]string, len(result[index].Columns), len(result[index].Columns)+1)
		copy(columns, result[index].Columns)
		columns = append(columns, "last_version_number")
		result[index].Columns = columns
	}
	return result
}

// schemaV1Tables is the pre-identity allowlist used only to validate and
// upgrade a schema-1 package inside the isolated import workspace.
var schemaV1Tables = []tableSpec{
	{Name: "environment", Columns: []string{
		"id", "created_at", "updated_at", "deleted_at", "environment_key",
		"environment_name", "description", "sort_order", "is_active",
	}},
	{Name: "pipeline", Columns: []string{
		"id", "created_at", "updated_at", "deleted_at", "environment_key",
		"pipeline_key", "pipeline_name", "description", "sort_order", "is_active",
	}},
	{Name: "resource_config", Columns: []string{
		"edit_revision", "id", "created_at", "updated_at", "deleted_at",
		"environment_key", "pipeline_key", "resource_key", "alias", "name",
		"content", "type", "remark", "is_perm", "description",
	}},
	{Name: "asset", Columns: []string{
		"id", "created_at", "updated_at", "deleted_at", "file_id",
		"environment_key", "pipeline_key", "file_name", "content_type",
		"file_size", "path", "remark",
	}},
	{Name: "config_freeze_state", Columns: []string{
		"id", "environment_key", "pipeline_key", "state", "revision",
		"last_changed_at", "last_changed_by", "last_reason", "created_at", "updated_at",
	}},
	{Name: "config_freeze_event", Columns: []string{
		"id", "environment_key", "pipeline_key", "event_type", "from_state",
		"to_state", "revision", "reason", "actor", "created_at",
	}},
	{Name: "config_release_version", Columns: []string{
		"id", "snapshot_id", "environment_key", "pipeline_key", "version",
		"creation_request_id", "request_fingerprint", "source_freeze_revision",
		"environment_name", "pipeline_name", "payload", "payload_sha256",
		"manifest_sha256", "config_count", "asset_count", "asset_bytes",
		"external_references", "reason", "actor", "created_at", "deleted_at",
		"deleted_by", "delete_reason",
	}},
	{Name: "config_release_asset", Columns: []string{
		"id", "snapshot_id", "asset_id", "source_file_id", "file_name",
		"content_type", "size", "sha256", "created_at",
	}},
	{Name: "config_release_head", Columns: []string{
		"id", "environment_key", "pipeline_key", "active_snapshot_id", "revision",
		"changed_at", "changed_by", "changed_reason", "created_at", "updated_at",
	}},
	{Name: "config_release_event", Columns: []string{
		"id", "event_type", "request_id", "request_fingerprint", "environment_key",
		"pipeline_key", "snapshot_id", "version", "from_active_snapshot",
		"to_active_snapshot", "from_head_revision", "to_head_revision",
		"reason", "actor", "created_at",
	}},
}

func businessModels() []any {
	return []any{
		&model.Config{}, &model.Asset{}, &model.Environment{}, &model.Pipeline{},
		&model.ConfigFreezeState{}, &model.ConfigFreezeEvent{},
		&model.ConfigReleaseVersion{}, &model.ConfigReleaseAsset{},
		&model.ConfigReleaseHead{}, &model.ConfigReleaseEvent{},
		&model.ScopeLifecycleEvent{},
	}
}

func tableSpecByName(name string) (tableSpec, bool) {
	for _, spec := range businessTables {
		if spec.Name == name {
			return spec, true
		}
	}
	return tableSpec{}, false
}

func allowedTableSetFor(specs []tableSpec) map[string]tableSpec {
	result := make(map[string]tableSpec, len(specs))
	for _, spec := range specs {
		result[spec.Name] = spec
	}
	return result
}

func allowedTableSet() map[string]tableSpec {
	return allowedTableSetFor(businessTables)
}

// openBusinessDB creates (or opens) a server-model SQLite database. Uploaded
// DDL is never executed; only these known models define the schema.
func openBusinessDB(path string) (*gorm.DB, error) {
	db, err := database.Open(appconfig.DatabaseConfig{
		Driver: "sqlite",
		SQLite: appconfig.SQLiteConfig{Path: path},
	})
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(businessModels()...); err != nil {
		_ = closeGorm(db)
		return nil, err
	}
	return db, nil
}

func closeGorm(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// copyBusinessTables copies every allowlisted table from src to dst. dst is a
// freshly migrated database, so no uploaded schema or trigger is involved.
func copyBusinessTables(ctx context.Context, src, dst *gorm.DB) error {
	return dst.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, spec := range businessTables {
			if err := copyTable(ctx, src, tx, spec); err != nil {
				return err
			}
			if err := verifyTableCount(ctx, src, tx, spec.Name); err != nil {
				return err
			}
		}
		return nil
	})
}

func copyTable(ctx context.Context, src, dst *gorm.DB, spec tableSpec) error {
	query := fmt.Sprintf("SELECT %s FROM %s", strings.Join(spec.Columns, ", "), spec.Name)
	rows, err := src.WithContext(ctx).Raw(query).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()

	placeholders := make([]string, len(spec.Columns))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		spec.Name, strings.Join(spec.Columns, ", "), strings.Join(placeholders, ", "))

	values := make([]any, len(spec.Columns))
	pointers := make([]any, len(spec.Columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return err
		}
		// Keep the statement inside the GORM transaction connection.
		if err := dst.WithContext(ctx).Exec(insertSQL, values...).Error; err != nil {
			return err
		}
	}
	return rows.Err()
}

func verifyTableCount(ctx context.Context, src, dst *gorm.DB, table string) error {
	countSource, err := tableRowCount(ctx, src, table)
	if err != nil {
		return err
	}
	countDestination, err := tableRowCount(ctx, dst, table)
	if err != nil {
		return err
	}
	if countSource != countDestination {
		return fmt.Errorf("row count mismatch for %s", table)
	}
	return nil
}

func tableRowCount(ctx context.Context, db *gorm.DB, table string) (int64, error) {
	var count int64
	if err := db.WithContext(ctx).Raw("SELECT COUNT(*) FROM " + table).Row().Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// Counts is the manifest summary of business rows.
type Counts struct {
	Environments    int `json:"environments"`
	Pipelines       int `json:"pipelines"`
	Configs         int `json:"configs"`
	Assets          int `json:"assets"`
	Versions        int `json:"versions"`
	DeletedVersions int `json:"deleted_versions"`
	FreezeEvents    int `json:"freeze_events"`
	ReleaseEvents   int `json:"release_events"`
	ConfigsDeleted  int `json:"configs_deleted"`
	AssetsDeleted   int `json:"assets_deleted"`
}

func collectCounts(ctx context.Context, db *gorm.DB) (Counts, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return Counts{}, err
	}
	return collectCountsSQL(ctx, sqlDB)
}

func collectCountsSQL(ctx context.Context, db *sql.DB) (Counts, error) {
	counts := Counts{}
	queries := []struct {
		target *int
		query  string
	}{
		{&counts.Environments, "SELECT COUNT(*) FROM environment"},
		{&counts.Pipelines, "SELECT COUNT(*) FROM pipeline"},
		{&counts.Configs, "SELECT COUNT(*) FROM resource_config"},
		{&counts.Assets, "SELECT COUNT(*) FROM asset"},
		{&counts.Versions, "SELECT COUNT(*) FROM config_release_version"},
		{&counts.DeletedVersions, "SELECT COUNT(*) FROM config_release_version WHERE deleted_at IS NOT NULL"},
		{&counts.FreezeEvents, "SELECT COUNT(*) FROM config_freeze_event"},
		{&counts.ReleaseEvents, "SELECT COUNT(*) FROM config_release_event"},
		{&counts.ConfigsDeleted, "SELECT COUNT(*) FROM resource_config WHERE deleted_at IS NOT NULL"},
		{&counts.AssetsDeleted, "SELECT COUNT(*) FROM asset WHERE deleted_at IS NOT NULL"},
	}
	for _, item := range queries {
		if err := db.QueryRowContext(ctx, item.query).Scan(item.target); err != nil {
			return Counts{}, err
		}
	}
	return counts, nil
}

// checkIntegrity runs SQLite's own consistency checks.
func checkIntegrity(ctx context.Context, db *gorm.DB) error {
	var result string
	row := db.WithContext(ctx).Raw("PRAGMA integrity_check").Row()
	if err := row.Scan(&result); err != nil {
		return err
	}
	if !strings.EqualFold(result, "ok") {
		return fmt.Errorf("sqlite integrity check failed: %s", result)
	}
	return nil
}
