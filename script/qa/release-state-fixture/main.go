// A UI-only fixture writer, invoked after stopping the owned temporary instance.
// It never runs in the service and never accepts an arbitrary database path.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/glebarez/sqlite"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	instance := flag.String("instance", "", "owned stopped scope UI instance")
	flag.Parse()
	if err := prepare(*instance); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func prepare(instance string) error {
	root, err := filepath.Abs("output/scope-transfer-ui")
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(instance)
	if err != nil {
		return err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Dir(resolved), root) || !strings.HasPrefix(filepath.Base(resolved), "instance-") {
		return fmt.Errorf("refusing a non-test instance path")
	}
	if _, err := os.Stat(filepath.Join(resolved, "ui-fixture-server-stopped")); err != nil {
		return err
	}
	databasePath := filepath.Join(resolved, "data", "resource.db")
	if _, err := os.Stat(databasePath); err != nil {
		return err
	}
	db, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return err
	}
	connection, err := db.DB()
	if err != nil {
		return err
	}
	defer connection.Close()
	var scopeID string
	err = db.Transaction(func(tx *gorm.DB) error {
		var pipeline model.Pipeline
		if err := tx.Where("environment_key = ? AND pipeline_key = ?", "default", "default").First(&pipeline).Error; err != nil {
			return err
		}
		scopeID = pipeline.ScopeID
		if scopeID == "" {
			return fmt.Errorf("missing default scope identity")
		}
		var versions int64
		if err := tx.Model(&model.ConfigReleaseVersion{}).Where("scope_id = ?", scopeID).Count(&versions).Error; err != nil {
			return err
		}
		if versions != 0 {
			return fmt.Errorf("fixture requires a fresh scope with no versions")
		}
		var head model.ConfigReleaseHead
		if err := tx.Where("scope_id = ?", scopeID).FirstOrCreate(&head, model.ConfigReleaseHead{
			ScopeID: scopeID, EnvironmentKey: "default", PipelineKey: "default",
		}).Error; err != nil {
			return err
		}
		if head.ActiveSnapshotID != "" || head.Revision != 0 {
			return fmt.Errorf("fixture requires a pristine inactive head")
		}
		return tx.Model(&head).Update("revision", int64(7)).Error
	})
	if err != nil {
		return err
	}
	evidence, err := json.MarshalIndent(map[string]any{
		"scope_id": scopeID, "active": nil, "active_revision": "7", "fixture_only": true, "server_stopped_before_write": true,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(resolved, "state-fixture.json"), evidence, 0600)
}
