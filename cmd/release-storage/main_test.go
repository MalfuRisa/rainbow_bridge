package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/pkg/config"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDefaultPlanUsesExistingReadOnlySQLite(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "business.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	defer pool.Close()
	if err := db.AutoMigrate(&model.Environment{}, &model.ConfigReleaseVersion{}, &model.ReleaseStorageAttempt{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Environment{EnvironmentID: uuid.NewString(), EnvironmentKey: "owned-cli", EnvironmentName: "test"}).Error; err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Database: config.DatabaseConfig{Driver: "sqlite", SQLite: config.SQLiteConfig{Path: path}}, Storage: config.StorageConfig{Type: "local"}}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	oldArgs, oldFlags := os.Args, flag.CommandLine
	defer func() { os.Args = oldArgs; flag.CommandLine = oldFlags }()
	os.Args = []string{"release-storage", "--config", file}
	flag.CommandLine = flag.NewFlagSet("release-storage", flag.ContinueOnError)
	if err := run(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil || !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatal("default plan changed database file", err)
	}
}
