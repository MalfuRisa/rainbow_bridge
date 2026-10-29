package backup

import (
	"context"
	"database/sql"
	"os"
	"time"
)

// Includes deleted history; a local global setting does not turn remote history
// into a complete file backup. Read-only, no migrations or attempt restoration.
func snapshotBackupInventory(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return ""
	}
	db, err := sql.Open("sqlite", readOnlySQLiteDSN(path))
	if err != nil {
		return "snapshot_inventory_unavailable"
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return "snapshot_inventory_unavailable"
	}
	if !sqliteColumnExists(db, "config_release_version", "storage_backend") {
		return ""
	}
	var count int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM config_release_version WHERE storage_backend NOT IN ('','local')").Scan(&count); err != nil {
		return "snapshot_inventory_unavailable"
	}
	if count != 0 {
		return "snapshot_history_not_local"
	}
	return ""
}
