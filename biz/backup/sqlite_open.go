package backup

import (
	appconfig "github.com/yi-nology/rainbow_bridge/pkg/config"
	"github.com/yi-nology/rainbow_bridge/pkg/database"
	"gorm.io/gorm"
)

// openSQLiteConfig opens an existing SQLite database without AutoMigrate.
func openSQLiteConfig(path string) (*gorm.DB, error) {
	return database.Open(appconfig.DatabaseConfig{
		Driver: "sqlite",
		SQLite: appconfig.SQLiteConfig{Path: path},
	})
}
