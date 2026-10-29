package backup

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/pkg/config"
)

func TestBackupRejectsConfiguredOrHistoricalMinIOSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "business.db")
	db := openTestDatabase(t, path)
	defer closeGorm(db)
	cfg := &config.Config{Database: config.DatabaseConfig{Driver: "sqlite", SQLite: config.SQLiteConfig{Path: path}}, Storage: config.StorageConfig{Type: "local"}, Backup: config.BackupConfig{Enabled: true, ControlDir: t.TempDir()}, ReleaseStorage: config.ReleaseStorageConfig{Type: "minio", Bucket: "snapshots"}}
	if caps := CapabilitiesFor(cfg, DefaultLimits()); caps.Supported || !containsIssue(caps.Reasons, "snapshot_storage_not_local") {
		t.Fatalf("original local + snapshot minio claimed support: %+v", caps)
	}
	cfg.ReleaseStorage.Type = "local"
	deleted := time.Now()
	if err := db.Create(&model.ConfigReleaseVersion{SnapshotID: uuid.NewString(), CreationRequestID: uuid.NewString(), StorageBackend: "minio", DeletedAt: &deleted}).Error; err != nil {
		t.Fatal(err)
	}
	if caps := CapabilitiesFor(cfg, DefaultLimits()); caps.Supported || !containsIssue(caps.Reasons, "snapshot_history_not_local") {
		t.Fatalf("local config ignored deleted MinIO history: %+v", caps)
	}
}
