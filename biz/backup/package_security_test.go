package backup

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/pkg/config"
)

func TestInspectPackageRejectsUnsafeZipEntries(t *testing.T) {
	cases := []struct {
		name    string
		entries []zipTestEntry
		want    string
	}{
		{"traversal", []zipTestEntry{{Name: "../evil.txt", Body: "x"}}, "unsafe_entry_path"},
		{"absolute", []zipTestEntry{{Name: "/etc/passwd", Body: "x"}}, "unsafe_entry_path"},
		{"drive", []zipTestEntry{{Name: "C:/Windows/x", Body: "x"}}, "unsafe_entry_path"},
		{"backslash", []zipTestEntry{{Name: "uploads\\x", Body: "x"}}, "unsafe_entry_path"},
		{"symlink", []zipTestEntry{{Name: "uploads/link", Body: "/etc/passwd", Mode: os.ModeSymlink | 0o777}}, "non_regular_entry"},
		{"duplicate-fold", []zipTestEntry{
			{Name: "uploads/A.txt", Body: "a"}, {Name: "uploads/a.txt", Body: "b"},
		}, "duplicate_entry"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "evil.zip")
			writeTestZip(t, path, testCase.entries)
			_, issues, err := inspectPackage(context.Background(), path, filepath.Join(dir, "work"), DefaultLimits())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !containsIssue(issues, testCase.want) {
				t.Fatalf("issues = %v, want %s", issues, testCase.want)
			}
		})
	}
}

func TestInspectPackageRejectsCorruptedDatabaseEntry(t *testing.T) {
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
	seedBusiness(t, db, layout)
	manager := newTestManager(t, layout)
	manager.AttachDatabase(db)
	job, err := manager.CreateBackup(context.Background(), "admin", "", newUUID(t))
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, manager, job.JobID, StateSucceeded)
	path, _ := manager.backupPath(job.Result.BackupID)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the database member while keeping the ZIP structure readable.
	index := strings.Index(string(payload), "database/business.sqlite")
	if index < 0 {
		t.Fatal("database entry not found")
	}
	corrupt := append([]byte{}, payload...)
	for i := index + 200; i < len(corrupt) && i < index+260; i++ {
		corrupt[i] ^= 0xff
	}
	corruptPath := filepath.Join(root, "corrupt.zip")
	if err := os.WriteFile(corruptPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	_, issues, err := inspectPackage(context.Background(), corruptPath, filepath.Join(root, "work"), manager.limits)
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("corrupted package must produce validation issues")
	}
}

func TestCapabilitiesReportUnsupportedDeployment(t *testing.T) {
	cfg := &config.Config{
		Database: config.DatabaseConfig{Driver: "postgres"},
		Storage:  config.StorageConfig{Type: "minio"},
		Redis:    config.RedisConfig{Enabled: true},
		Backup:   config.BackupConfig{Enabled: true, ControlDir: t.TempDir()},
	}
	capabilities := CapabilitiesFor(cfg, DefaultLimits())
	if capabilities.Supported {
		t.Fatal("postgres/minio/redis deployment must not claim support")
	}
	for _, reason := range []string{"database_driver_not_supported", "storage_not_local", "redis_enabled"} {
		if !containsIssue(capabilities.Reasons, reason) {
			t.Fatalf("capability reasons = %v, missing %s", capabilities.Reasons, reason)
		}
	}
}

func newUUID(t *testing.T) string {
	t.Helper()
	return uuid.NewString()
}

type zipTestEntry struct {
	Name string
	Body string
	Mode os.FileMode
}

func writeTestZip(t *testing.T, path string, entries []zipTestEntry) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.Name, Method: zip.Deflate}
		if entry.Mode != 0 {
			header.SetMode(entry.Mode)
		}
		part, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(entry.Body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func containsIssue(issues []string, value string) bool {
	for _, issue := range issues {
		if strings.HasPrefix(issue, value) {
			return true
		}
	}
	return false
}
