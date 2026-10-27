package backup

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	configdb "github.com/yi-nology/rainbow_bridge/biz/dal/db"
)

type releaseNumberBounds struct {
	byScope map[string]int64
	byKeys  map[string]int64
}

func newReleaseNumberBounds() releaseNumberBounds {
	return releaseNumberBounds{
		byScope: make(map[string]int64),
		byKeys:  make(map[string]int64),
	}
}

// mergeReleaseNumberBounds preserves the upper bound of the root being
// replaced when a package was created before later versions. The restored
// business data still comes from the package; only the counters of identical
// scope_id rows are raised. If the old root has no counter column (schema 1/2)
// the bound is derived from its version names.
func mergeReleaseNumberBounds(oldRoot, candidateRoot, databaseRel string) error {
	oldPath, ok := joinWithin(oldRoot, databaseRel)
	if !ok {
		return fmt.Errorf("old database path is invalid")
	}
	if _, err := os.Stat(oldPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect old database: %w", err)
	}
	oldBounds, err := readReleaseNumberBounds(oldPath)
	if err != nil {
		return fmt.Errorf("read old release numbering: %w", err)
	}
	candidatePath, ok := joinWithin(candidateRoot, databaseRel)
	if !ok {
		return fmt.Errorf("candidate database path is invalid")
	}
	if err := applyReleaseNumberBounds(candidatePath, oldBounds); err != nil {
		return fmt.Errorf("merge release numbering: %w", err)
	}
	return nil
}

func readReleaseNumberBounds(path string) (releaseNumberBounds, error) {
	bounds := newReleaseNumberBounds()
	db, err := sql.Open("sqlite", readOnlySQLiteDSN(path))
	if err != nil {
		return bounds, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return bounds, err
	}
	scopeColumn, counterColumn, err := releaseHeadColumns(db)
	if err != nil {
		return bounds, err
	}
	if counterColumn {
		query := "SELECT last_version_number"
		if scopeColumn {
			query = "SELECT scope_id, last_version_number FROM config_release_head"
			rows, err := db.Query(query)
			if err != nil {
				return bounds, err
			}
			for rows.Next() {
				var scopeID string
				var value int64
				if err := rows.Scan(&scopeID, &value); err != nil {
					_ = rows.Close()
					return bounds, err
				}
				if value > bounds.byScope[scopeID] {
					bounds.byScope[scopeID] = value
				}
			}
			if err := rows.Close(); err != nil {
				return bounds, err
			}
		} else {
			rows, err := db.Query("SELECT environment_key, pipeline_key, last_version_number FROM config_release_head")
			if err != nil {
				return bounds, err
			}
			for rows.Next() {
				var environmentKey, pipelineKey string
				var value int64
				if err := rows.Scan(&environmentKey, &pipelineKey, &value); err != nil {
					_ = rows.Close()
					return bounds, err
				}
				key := releaseScopeKey(environmentKey, pipelineKey)
				if value > bounds.byKeys[key] {
					bounds.byKeys[key] = value
				}
			}
			if err := rows.Close(); err != nil {
				return bounds, err
			}
		}
	}
	versionScope, versionEnvPipe, err := releaseVersionColumns(db)
	if err != nil {
		return bounds, err
	}
	if versionScope {
		rows, err := db.Query("SELECT scope_id, version FROM config_release_version")
		if err != nil {
			return bounds, err
		}
		for rows.Next() {
			var scopeID, version string
			if err := rows.Scan(&scopeID, &version); err != nil {
				_ = rows.Close()
				return bounds, err
			}
			value, canonical, err := configdb.ParseReleaseSequenceVersion(version)
			if err != nil {
				_ = rows.Close()
				return bounds, err
			}
			if canonical && value > bounds.byScope[scopeID] {
				bounds.byScope[scopeID] = value
			}
		}
		if err := rows.Close(); err != nil {
			return bounds, err
		}
	} else if versionEnvPipe {
		rows, err := db.Query("SELECT environment_key, pipeline_key, version FROM config_release_version")
		if err != nil {
			return bounds, err
		}
		if err := mergeKeyedVersionRows(rows, bounds); err != nil {
			return bounds, err
		}
	}
	return bounds, nil
}

func releaseHeadColumns(db *sql.DB) (scopeColumn, counterColumn bool, err error) {
	rows, err := db.Query("PRAGMA table_info(config_release_head)")
	if err != nil {
		return false, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, false, err
		}
		switch name {
		case "scope_id":
			scopeColumn = true
		case "last_version_number":
			counterColumn = true
		}
	}
	return scopeColumn, counterColumn, rows.Err()
}

func releaseVersionColumns(db *sql.DB) (scopeColumn, envPipeColumns bool, err error) {
	rows, err := db.Query("PRAGMA table_info(config_release_version)")
	if err != nil {
		return false, false, err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, false, err
		}
		columns[name] = true
	}
	return columns["scope_id"], columns["environment_key"] && columns["pipeline_key"], rows.Err()
}

func mergeKeyedVersionRows(rows *sql.Rows, bounds releaseNumberBounds) error {
	defer rows.Close()
	for rows.Next() {
		var environmentKey, pipelineKey, version string
		if err := rows.Scan(&environmentKey, &pipelineKey, &version); err != nil {
			return err
		}
		value, canonical, err := configdb.ParseReleaseSequenceVersion(version)
		if err != nil {
			return err
		}
		if !canonical {
			continue
		}
		key := releaseScopeKey(environmentKey, pipelineKey)
		if value > bounds.byKeys[key] {
			bounds.byKeys[key] = value
		}
	}
	return rows.Err()
}

func applyReleaseNumberBounds(candidatePath string, oldBounds releaseNumberBounds) error {
	db, err := sql.Open("sqlite", candidatePath)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return err
	}
	type headRow struct {
		scopeID        string
		environmentKey string
		pipelineKey    string
		current        int64
	}
	rows, err := db.Query("SELECT scope_id, environment_key, pipeline_key, last_version_number FROM config_release_head")
	if err != nil {
		return err
	}
	heads := make([]headRow, 0)
	for rows.Next() {
		var head headRow
		if err := rows.Scan(&head.scopeID, &head.environmentKey, &head.pipelineKey, &head.current); err != nil {
			_ = rows.Close()
			return err
		}
		heads = append(heads, head)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	history, err := candidateReleaseSequenceMax(db)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, head := range heads {
		maximum := head.current
		if value := oldBounds.byScope[head.scopeID]; value > maximum {
			maximum = value
		}
		if value := oldBounds.byKeys[releaseScopeKey(head.environmentKey, head.pipelineKey)]; value > maximum {
			maximum = value
		}
		if value := history[head.scopeID]; value > maximum {
			maximum = value
		}
		if maximum <= head.current {
			continue
		}
		if _, err := tx.Exec(
			"UPDATE config_release_head SET last_version_number = ? WHERE scope_id = ? AND last_version_number < ?",
			maximum, head.scopeID, maximum); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func candidateReleaseSequenceMax(db *sql.DB) (map[string]int64, error) {
	result := make(map[string]int64)
	rows, err := db.Query("SELECT scope_id, version FROM config_release_version")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var scopeID, version string
		if err := rows.Scan(&scopeID, &version); err != nil {
			return nil, err
		}
		value, canonical, err := configdb.ParseReleaseSequenceVersion(version)
		if err != nil {
			return nil, err
		}
		if canonical && value > result[scopeID] {
			result[scopeID] = value
		}
	}
	return result, rows.Err()
}

func releaseScopeKey(environmentKey, pipelineKey string) string {
	return strings.TrimSpace(environmentKey) + "\x00" + strings.TrimSpace(pipelineKey)
}
