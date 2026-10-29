// Offline/operational storage maintenance. No automatic migration or cleanup.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/yi-nology/rainbow_bridge/biz/service"
	"github.com/yi-nology/rainbow_bridge/pkg/config"
	"github.com/yi-nology/rainbow_bridge/pkg/database"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "release-storage:", err)
		os.Exit(1)
	}
}
func run() error {
	file := flag.String("config", "config.yaml", "configuration file")
	action := flag.String("action", "plan", "plan|inspect|migrate|verify|rollback|reconcile")
	execute := flag.Bool("execute", false, "enable selected mutation (default dry-run)")
	confirm := flag.String("confirm-instance", "", "exact token printed by plan")
	snapshots := flag.String("snapshots", "", "explicit comma-separated snapshot UUIDs")
	attempt := flag.String("attempt", "", "one explicit attempt UUID for reconcile")
	flag.Parse()
	switch *action {
	case "plan", "inspect", "verify", "migrate", "rollback", "reconcile":
	default:
		return errors.New("unknown action")
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		return errors.New("configuration unavailable")
	}
	cfg := &config.Config{}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return errors.New("configuration unavailable")
	}
	if cfg.Database.Driver == "sqlite" || cfg.Database.Driver == "sqlite3" {
		if _, err := os.Stat(cfg.Database.SQLite.Path); err != nil {
			return errors.New("existing SQLite instance required")
		}
	}
	// Do not AutoMigrate, seed accounts, or run startup content migrations.
	var db *gorm.DB
	if !*execute && (cfg.Database.Driver == "sqlite" || cfg.Database.Driver == "sqlite3") {
		absolute, absErr := filepath.Abs(cfg.Database.SQLite.Path)
		if absErr != nil {
			return errors.New("SQLite path unavailable")
		}
		db, err = gorm.Open(sqlite.Open("file:"+filepath.ToSlash(absolute)+"?mode=ro&_pragma=query_only(1)&_pragma=trusted_schema(0)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	} else {
		db, err = database.Open(cfg.Database)
	}
	if err != nil {
		return errors.New("database unavailable")
	}
	db.Logger = logger.Default.LogMode(logger.Silent)
	sqlDB, err := db.DB()
	if err != nil {
		return errors.New("database unavailable")
	}
	defer sqlDB.Close()
	if !*execute {
		// DB-enforced read-only transaction, not just a README promise.
		if cfg.Database.Driver == "sqlite" || cfg.Database.Driver == "sqlite3" {
			db = db.Begin()
		} else {
			db = db.Begin(&sql.TxOptions{ReadOnly: true})
		}
		if db.Error != nil {
			return errors.New("read-only inventory transaction unavailable")
		}
		defer db.Rollback()
	}
	s := service.NewService(db, nil, cfg.Server.BasePath, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	identity, err := s.StorageInstanceConfirmation(ctx)
	if err != nil {
		return errors.New("instance identity unavailable")
	}
	if *execute {
		if *confirm != identity {
			return errors.New("exact instance confirmation required")
		}
		if *action == "reconcile" {
			if *attempt == "" {
				return errors.New("explicit attempt required")
			}
			if err := s.ReconcileSnapshotAttempt(ctx, *attempt); err != nil {
				return errors.New("reconcile refused or failed; inspect attempt state")
			}
		} else if *action == "migrate" || *action == "rollback" {
			if *snapshots == "" {
				return errors.New("explicit snapshot selection required")
			}
			failed := false
			for _, id := range strings.Split(*snapshots, ",") {
				var opErr error
				if *action == "migrate" {
					opErr = s.MigrateSnapshotStorage(ctx, id)
				} else {
					opErr = s.RollbackSnapshotStorage(ctx, id)
				}
				status := "verified"
				if opErr != nil {
					status = "failed"
					failed = true
				}
				json.NewEncoder(os.Stdout).Encode(map[string]string{"snapshot_id": id, "status": status})
			}
			if failed {
				return errors.New("one or more selected snapshots failed; other successful results retained")
			}
		} else {
			return errors.New("execute is valid only for migrate/rollback/reconcile")
		}
	}
	inventory, err := s.InspectSnapshotStorage(ctx)
	if err != nil {
		return errors.New("inventory failed; no automatic repair performed")
	}
	if err := json.NewEncoder(os.Stdout).Encode(inventory); err != nil {
		return err
	}
	if *action == "verify" {
		for _, row := range inventory.Versions {
			if row.Status != "verified" {
				return errors.New("snapshot verification failed")
			}
		}
	}
	return nil
}
