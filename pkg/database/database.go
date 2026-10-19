package database

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yi-nology/rainbow_bridge/pkg/config"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open initialises a gorm.DB according to the supplied configuration.
func Open(cfg config.DatabaseConfig) (*gorm.DB, error) {
	driver := strings.ToLower(cfg.Driver)
	var (
		dialector gorm.Dialector
		err       error
	)

	switch driver {
	case "sqlite", "sqlite3":
		if cfg.SQLite.Path == "" {
			return nil, fmt.Errorf("sqlite path must be configured")
		}
		if err := ensureDir(filepath.Dir(cfg.SQLite.Path)); err != nil {
			return nil, err
		}
		dialector = sqlite.Open(cfg.SQLite.Path)
	case "mysql":
		if cfg.MySQL.DSN == "" {
			return nil, fmt.Errorf("mysql dsn must be configured")
		}
		dialector = mysql.Open(cfg.MySQL.DSN)
	case "postgres", "postgresql":
		if cfg.Postgres.DSN == "" {
			return nil, fmt.Errorf("postgres dsn must be configured")
		}
		dialector = postgres.Open(cfg.Postgres.DSN)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}

	// 配置自定义日志记录器，启用慢查询日志
	newLogger := logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags), // io writer
		logger.Config{
			SlowThreshold:             200 * time.Millisecond, // 慢查询阈值
			LogLevel:                  logger.Warn,            // 日志级别
			IgnoreRecordNotFoundError: true,                   // 忽略记录未找到错误
			Colorful:                  false,                  // 禁用彩色输出
		},
	)

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: newLogger,
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	if driver == "sqlite" || driver == "sqlite3" {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	} else {
		sqlDB.SetMaxIdleConns(20)
		sqlDB.SetMaxOpenConns(100)
	}
	sqlDB.SetConnMaxLifetime(1 * time.Hour)
	sqlDB.SetConnMaxIdleTime(30 * time.Minute)

	return db, nil
}

func ensureDir(dir string) error {
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}
