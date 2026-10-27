package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

func TestBackupIncludesCommittedWALData(t *testing.T) {
	root := t.TempDir()
	layout := &Layout{
		BusinessRoot: filepath.Join(root, "data"), ControlDir: filepath.Join(root, "control"),
		DatabasePath: filepath.Join(root, "data", "resource.db"), DatabaseRel: "resource.db",
		UploadsDir: filepath.Join(root, "data", "uploads"),
		ReleaseDir: filepath.Join(root, "data", "release-snapshots"), ReleaseRel: "release-snapshots",
	}
	for _, dir := range []string{layout.BusinessRoot, layout.UploadsDir, layout.ReleaseDir, layout.ControlDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	db := openTestDatabase(t, layout.DatabasePath)
	t.Cleanup(func() { _ = closeGorm(db) })
	if err := db.Exec("PRAGMA journal_mode = WAL").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("PRAGMA wal_autocheckpoint = 0").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Environment{EnvironmentKey: "prod", EnvironmentName: "Prod", IsActive: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Pipeline{EnvironmentKey: "prod", PipelineKey: "main", PipelineName: "Main", IsActive: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Config{EnvironmentKey: "prod", PipelineKey: "main",
		ResourceKey: "wal", Alias: "wal", Name: "WAL", Content: "wal-committed",
		Type: "text", EditRevision: uuid.NewString()}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(layout.DatabasePath + "-wal"); err != nil {
		t.Fatalf("expected an uncheckpointed WAL file: %v", err)
	}

	manager := newTestManager(t, layout)
	manager.AttachDatabase(db)
	job, err := manager.CreateBackup(context.Background(), "admin", "", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, manager, job.JobID, StateSucceeded)
	packagePath, _ := manager.backupPath(job.Result.BackupID)
	work := filepath.Join(root, "inspect")
	_, issues, err := inspectPackage(context.Background(), packagePath, work, manager.limits)
	if err != nil || len(issues) > 0 {
		t.Fatalf("inspect: err=%v issues=%v", err, issues)
	}
	dbPath := filepath.Join(work, filepath.FromSlash(DatabaseEntry))
	readOnly, err := sql.Open("sqlite", readOnlySQLiteDSN(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	var content string
	if err := readOnly.QueryRow(
		"SELECT content FROM resource_config WHERE resource_key = 'wal'").Scan(&content); err != nil {
		t.Fatal(err)
	}
	if content != "wal-committed" {
		t.Fatalf("WAL-committed row missing from backup: %q", content)
	}
}
