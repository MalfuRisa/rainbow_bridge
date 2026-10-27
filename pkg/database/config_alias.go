package database

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm"
)

const ConfigAliasIndex = "uk_config_active_alias"

type AliasConflict struct {
	ScopeID        string   `json:"scope_id"`
	EnvironmentKey string   `json:"environment_key,omitempty"`
	PipelineKey    string   `json:"pipeline_key,omitempty"`
	Alias          string   `json:"alias"`
	ResourceKeys   []string `json:"resource_keys"`
}

// CheckConfigAliases is read-only; it never chooses or deletes a winner.
func CheckConfigAliases(ctx context.Context, db *gorm.DB) ([]AliasConflict, error) {
	var groups []struct {
		ScopeID string
		Alias   string
	}
	err := db.WithContext(ctx).Table("resource_config").Select("scope_id, alias").Where("deleted_at IS NULL AND alias IS NOT NULL AND alias <> ''").Group("scope_id, alias").Having("COUNT(*) > 1").Scan(&groups).Error
	if err != nil {
		return nil, err
	}
	conflicts := make([]AliasConflict, 0, len(groups))
	for _, group := range groups {
		conflict := AliasConflict{ScopeID: group.ScopeID, Alias: group.Alias}
		if err := db.WithContext(ctx).Table("resource_config").Where("deleted_at IS NULL AND scope_id = ? AND alias = ?", conflict.ScopeID, conflict.Alias).Order("resource_key").Pluck("resource_key", &conflict.ResourceKeys).Error; err != nil {
			return nil, err
		}
		conflicts = append(conflicts, conflict)
	}
	return conflicts, nil
}

func PrepareConfigAliasConstraint(ctx context.Context, db *gorm.DB) error {
	conflicts, err := CheckConfigAliases(ctx, db)
	if err != nil {
		return err
	}
	if len(conflicts) > 0 {
		data, _ := json.Marshal(conflicts)
		return fmt.Errorf("active config alias conflicts; no rows changed: %s", data)
	}
	switch db.Dialector.Name() {
	case "sqlite":
		const ddl = "CREATE UNIQUE INDEX uk_config_active_alias ON resource_config(scope_id, alias) WHERE deleted_at IS NULL AND alias IS NOT NULL AND alias <> ''"
		var existing string
		if err := db.Raw("SELECT sql FROM sqlite_master WHERE type='index' AND name=?", ConfigAliasIndex).Scan(&existing).Error; err != nil {
			return err
		}
		if existing != "" {
			normalize := func(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }
			if normalize(existing) != normalize(ddl) {
				return fmt.Errorf("unexpected definition for %s: %s", ConfigAliasIndex, existing)
			}
			return nil
		}
		return db.WithContext(ctx).Exec(ddl).Error
	case "postgres":
		const ddl = "CREATE UNIQUE INDEX uk_config_active_alias ON resource_config(scope_id, alias) WHERE deleted_at IS NULL AND alias IS NOT NULL AND alias <> ''"
		var definition string
		if err := db.Raw("SELECT indexdef FROM pg_indexes WHERE schemaname=current_schema() AND tablename='resource_config' AND indexname=?", ConfigAliasIndex).Scan(&definition).Error; err != nil {
			return err
		}
		if definition != "" {
			// PostgreSQL renders casts/parentheses; verify via its catalog rather than a textual DDL comparison.
			var catalog struct {
				Valid     bool
				Predicate string
			}
			err := db.Raw(`SELECT i.indisunique AND i.indisvalid AND i.indnatts=2
    AND pg_get_indexdef(i.indexrelid,1,true)='scope_id'
    AND pg_get_indexdef(i.indexrelid,2,true)='alias' AS valid,
    pg_get_expr(i.indpred,i.indrelid) AS predicate
    FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid
    JOIN pg_namespace n ON n.oid=c.relnamespace
    WHERE c.relname=? AND n.nspname=current_schema() AND i.indrelid='resource_config'::regclass`, ConfigAliasIndex).Scan(&catalog).Error
			if err != nil {
				return err
			}
			predicate := strings.ReplaceAll(strings.ToLower(catalog.Predicate), "::text", "")
			predicate = regexp.MustCompile(`[()\s"]`).ReplaceAllString(predicate, "")
			if !catalog.Valid || predicate != "deleted_atisnullandaliasisnotnullandalias<>''" {
				return fmt.Errorf("unexpected definition for %s: %s", ConfigAliasIndex, definition)
			}
			return nil
		}
		return db.WithContext(ctx).Exec(ddl).Error
	case "mysql":
		return prepareMySQLConfigAlias(ctx, db)
	default:
		return fmt.Errorf("alias constraint unsupported driver %s", db.Dialector.Name())
	}
}

func prepareMySQLConfigAlias(ctx context.Context, db *gorm.DB) error {
	// Match the alias column exactly; never truncate or reduce the indexed identity.
	var columns []struct {
		ColumnName, ColumnType, CharacterSetName, CollationName string
		CharacterMaximumLength                                  *int64
	}
	if err := db.Raw("SELECT column_name,column_type,character_set_name,collation_name,character_maximum_length FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='resource_config' AND column_name IN ('scope_id','alias')").Scan(&columns).Error; err != nil {
		return err
	}
	var aliasType, charset, collation string
	var bytes int64
	for _, c := range columns {
		if c.CharacterMaximumLength == nil {
			return fmt.Errorf("MySQL alias constraint requires bounded string columns; column=%s type=%s; no DDL applied", c.ColumnName, c.ColumnType)
		}
		bytes += *c.CharacterMaximumLength * 4
		if c.ColumnName == "alias" {
			aliasType = c.ColumnType
			charset = c.CharacterSetName
			collation = c.CollationName
		}
	}
	if len(columns) != 2 || bytes > 3072 {
		return fmt.Errorf("MySQL full alias identity exceeds supported 3072-byte index budget (%d); no fields truncated; review bounded schema migration", bytes)
	}
	var expr string
	if err := db.Raw("SELECT generation_expression FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='resource_config' AND column_name='active_config_alias'").Scan(&expr).Error; err != nil {
		return err
	}
	if expr == "" {
		ddl := fmt.Sprintf("ALTER TABLE resource_config ADD COLUMN active_config_alias %s CHARACTER SET %s COLLATE %s GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL AND alias IS NOT NULL AND alias <> '' THEN alias ELSE NULL END) STORED", aliasType, charset, collation)
		if err := db.WithContext(ctx).Exec(ddl).Error; err != nil {
			return fmt.Errorf("MySQL generated alias column requires supported generated columns/index length; DDL=%s: %w", ddl, err)
		}
	} else {
		normalized := strings.ToLower(strings.Join(strings.Fields(expr), ""))
		normalized = strings.ReplaceAll(normalized, "`", "")
		normalized = strings.NewReplacer("(", "", ")", "", "_utf8mb4", "", "_utf8", "").Replace(normalized)
		if normalized != "casewhendeleted_atisnullandaliasisnotnullandalias<>''thenaliaselsenullend" {
			return fmt.Errorf("unexpected generated column: %s", expr)
		}
	}
	var indexes []struct {
		NonUnique  int
		ColumnName string
		SubPart    *int
	}
	if err := db.Raw("SELECT non_unique,column_name,sub_part FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='resource_config' AND index_name=? ORDER BY seq_in_index", ConfigAliasIndex).Scan(&indexes).Error; err != nil {
		return err
	}
	if len(indexes) > 0 {
		expected := []string{"scope_id", "active_config_alias"}
		if len(indexes) != 2 {
			return fmt.Errorf("unexpected MySQL index %s", ConfigAliasIndex)
		}
		for i, c := range indexes {
			if c.NonUnique != 0 || c.SubPart != nil || c.ColumnName != expected[i] {
				return fmt.Errorf("unexpected MySQL index %s", ConfigAliasIndex)
			}
		}
		return nil
	}
	return db.WithContext(ctx).Exec("CREATE UNIQUE INDEX uk_config_active_alias ON resource_config(scope_id,active_config_alias)").Error
}
