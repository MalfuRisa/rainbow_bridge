package backup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestSchema2PackageStillInspectsAndUpgradesToSchema3 builds a genuine
// schema-2 package by removing the schema-3 counter column and rewrites the
// manifest/digests. It must pass validation and be upgraded in isolation.
func TestLegacySchema2And3PackagesUpgradeToSchema4(t *testing.T) {
	for _, version := range []int{2, 3} {
		t.Run(strconv.Itoa(version), func(t *testing.T) { testLegacySnapshotSchemaPackage(t, version) })
	}
}

func testLegacySnapshotSchemaPackage(t *testing.T, version int) {
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
	created, err := manager.CreateBackup(context.Background(), "admin", "", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	created = waitJob(t, manager, created.JobID, StateSucceeded)
	source, _ := manager.backupPath(created.Result.BackupID)

	work := filepath.Join(root, "schema2")
	extractZipTo(t, source, work)
	databasePath := filepath.Join(work, filepath.FromSlash(DatabaseEntry))
	for _, column := range []string{"storage_backend", "storage_bucket", "storage_prefix", "storage_attempt", "legacy_local"} {
		dropSQLiteColumn(t, databasePath, "config_release_version", column)
	}
	if version == 2 {
		dropSQLiteColumn(t, databasePath, "config_release_head", "last_version_number")
	}
	manifest := readManifestFile(t, filepath.Join(work, ManifestEntry))
	manifest.SchemaVersion = version
	manifest.BackupID = uuid.NewString()
	updateManifestDatabaseEntry(t, manifest, databasePath)
	plans := manifestFilePlans(t, work, manifest)
	packagePath := filepath.Join(root, "schema2-backup.zip")
	if _, _, err := writePackage(context.Background(), packagePath, databasePath, manifest, plans, DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	info, issues, err := inspectPackage(context.Background(), packagePath,
		filepath.Join(root, "inspect"), DefaultLimits())
	if err != nil || len(issues) > 0 {
		t.Fatalf("schema2 inspect: err=%v issues=%v", err, issues)
	}
	if info.Manifest.SchemaVersion != SchemaVersion {
		t.Fatalf("upgraded schema = %d", info.Manifest.SchemaVersion)
	}
	upgraded := filepath.Join(root, "inspect", filepath.FromSlash(DatabaseEntry))
	upgradedDB, err := sql.Open("sqlite", readOnlySQLiteDSN(upgraded))
	if err != nil {
		t.Fatal(err)
	}
	defer upgradedDB.Close()
	var counter int64
	if err := upgradedDB.QueryRow(
		"SELECT last_version_number FROM config_release_head WHERE environment_key = 'prod' AND pipeline_key = 'main'").
		Scan(&counter); err != nil {
		t.Fatal(err)
	}
	if counter < 1 {
		t.Fatalf("schema2 counter was not backfilled: %d", counter)
	}
}

func TestSchema1PackageUpgradesThroughIdentityAndNumbering(t *testing.T) {
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
	created, err := manager.CreateBackup(context.Background(), "admin", "", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	created = waitJob(t, manager, created.JobID, StateSucceeded)
	source, _ := manager.backupPath(created.Result.BackupID)
	work := filepath.Join(root, "schema1")
	extractZipTo(t, source, work)
	databasePath := filepath.Join(work, filepath.FromSlash(DatabaseEntry))
	rebuildSchemaV1(t, databasePath)
	manifest := readManifestFile(t, filepath.Join(work, ManifestEntry))
	manifest.SchemaVersion = 1
	manifest.BackupID = uuid.NewString()
	updateManifestDatabaseEntry(t, manifest, databasePath)
	plans := manifestFilePlans(t, work, manifest)
	packagePath := filepath.Join(root, "schema1-backup.zip")
	if _, _, err := writePackage(context.Background(), packagePath, databasePath, manifest, plans, DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	info, issues, err := inspectPackage(context.Background(), packagePath,
		filepath.Join(root, "inspect"), DefaultLimits())
	if err != nil || len(issues) > 0 {
		t.Fatalf("schema1 inspect: err=%v issues=%v", err, issues)
	}
	if info.Manifest.SchemaVersion != SchemaVersion {
		t.Fatalf("upgraded schema = %d", info.Manifest.SchemaVersion)
	}
	upgradedDB, err := sql.Open("sqlite", readOnlySQLiteDSN(
		filepath.Join(root, "inspect", filepath.FromSlash(DatabaseEntry))))
	if err != nil {
		t.Fatal(err)
	}
	defer upgradedDB.Close()
	var counter int64
	if err := upgradedDB.QueryRow(
		"SELECT last_version_number FROM config_release_head WHERE environment_key = 'prod' AND pipeline_key = 'main'").
		Scan(&counter); err != nil {
		t.Fatal(err)
	}
	if counter < 1 {
		t.Fatalf("schema1 counter was not backfilled: %d", counter)
	}
	var emptyScope int
	if err := upgradedDB.QueryRow(
		"SELECT COUNT(*) FROM config_release_version WHERE scope_id = ''").Scan(&emptyScope); err != nil {
		t.Fatal(err)
	}
	if emptyScope != 0 {
		t.Fatalf("identity migration left %d versions without scope", emptyScope)
	}
}

func rebuildSchemaV1(t *testing.T, databasePath string) {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("DROP TABLE scope_lifecycle_event"); err != nil {
		t.Fatal(err)
	}
	for _, spec := range schemaV1Tables {
		table := spec.Name
		temporary := "v1_" + table
		columns := make([]string, len(spec.Columns))
		for index, column := range spec.Columns {
			columns[index] = column + " TEXT"
		}
		if _, err := db.Exec("CREATE TABLE " + temporary + " (" + strings.Join(columns, ", ") + ")"); err != nil {
			t.Fatalf("create %s: %v", temporary, err)
		}
		copyColumns := strings.Join(spec.Columns, ", ")
		if _, err := db.Exec("INSERT INTO " + temporary + " (" + copyColumns + ") SELECT " +
			copyColumns + " FROM " + table); err != nil {
			t.Fatalf("copy %s: %v", table, err)
		}
		if _, err := db.Exec("DROP TABLE " + table); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("ALTER TABLE " + temporary + " RENAME TO " + table); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMergeReleaseNumberBoundsPreservesOldUpperBound(t *testing.T) {
	root := t.TempDir()
	oldRoot := filepath.Join(root, "old")
	candidateRoot := filepath.Join(root, "candidate")
	for _, dir := range []string{oldRoot, candidateRoot} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	oldDB := filepath.Join(oldRoot, "resource.db")
	candidateDB := filepath.Join(candidateRoot, "resource.db")
	createMinimalNumberingDB(t, oldDB, false)
	createMinimalNumberingDB(t, candidateDB, true)
	oldHandle, err := sql.Open("sqlite", oldDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldHandle.Exec("INSERT INTO config_release_version (scope_id, version) VALUES ('scope-a', 'v7'), ('scope-a', 'v1.0')"); err != nil {
		t.Fatal(err)
	}
	_ = oldHandle.Close()
	candidateHandle, err := sql.Open("sqlite", candidateDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidateHandle.Exec(
		"INSERT INTO config_release_head (scope_id, environment_key, pipeline_key, last_version_number) VALUES ('scope-a', 'prod', 'main', 2), ('scope-b', 'prod', 'other', 1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := candidateHandle.Exec(
		"INSERT INTO config_release_version (scope_id, version) VALUES ('scope-a', 'v2'), ('scope-a', 'v4'), ('scope-b', 'v1')"); err != nil {
		t.Fatal(err)
	}
	_ = candidateHandle.Close()

	if err := mergeReleaseNumberBounds(oldRoot, candidateRoot, "resource.db"); err != nil {
		t.Fatal(err)
	}
	merged, err := sql.Open("sqlite", readOnlySQLiteDSN(candidateDB))
	if err != nil {
		t.Fatal(err)
	}
	defer merged.Close()
	var value int64
	if err := merged.QueryRow("SELECT last_version_number FROM config_release_head WHERE scope_id = 'scope-a'").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != 7 {
		t.Fatalf("scope-a counter = %d", value)
	}
	if err := merged.QueryRow("SELECT last_version_number FROM config_release_head WHERE scope_id = 'scope-b'").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != 1 {
		t.Fatalf("scope-b must stay independent: %d", value)
	}
	var oldVersions int
	if err := merged.QueryRow("SELECT COUNT(*) FROM config_release_version WHERE scope_id = 'scope-a'").Scan(&oldVersions); err != nil {
		t.Fatal(err)
	}
	if oldVersions != 2 {
		t.Fatalf("old versions must not be imported: %d", oldVersions)
	}
}

func createMinimalNumberingDB(t *testing.T, path string, withCounter bool) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	head := "scope_id TEXT, environment_key TEXT, pipeline_key TEXT"
	if withCounter {
		head += ", last_version_number INTEGER NOT NULL DEFAULT 0"
	}
	if _, err := db.Exec("CREATE TABLE config_release_head (" + head + ")"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE config_release_version (scope_id TEXT, version TEXT)"); err != nil {
		t.Fatal(err)
	}
}

func extractZipTo(t *testing.T, packagePath, target string) {
	t.Helper()
	reader, err := zip.OpenReader(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, entry := range reader.File {
		destination, ok := joinWithin(target, entry.Name)
		if !ok {
			t.Fatalf("unsafe entry %s", entry.Name)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		source, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		payload, err := io.ReadAll(source)
		_ = source.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func dropSQLiteColumn(t *testing.T, databasePath, table, column string) {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("ALTER TABLE " + table + " DROP COLUMN " + column); err != nil {
		t.Fatalf("drop %s.%s: %v", table, column, err)
	}
}

func readManifestFile(t *testing.T, path string) *Manifest {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	return &manifest
}

func updateManifestDatabaseEntry(t *testing.T, manifest *Manifest, databasePath string) {
	t.Helper()
	payload, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	for index := range manifest.Files {
		if manifest.Files[index].Path == DatabaseEntry {
			manifest.Files[index].Size = strconv.FormatInt(int64(len(payload)), 10)
			manifest.Files[index].Sha256 = hex.EncodeToString(digest[:])
			return
		}
	}
	t.Fatal("database manifest entry missing")
}

func manifestFilePlans(t *testing.T, work string, manifest *Manifest) []filePlan {
	t.Helper()
	plans := make([]filePlan, 0, len(manifest.Files))
	for _, entry := range manifest.Files {
		if entry.Path == DatabaseEntry {
			continue
		}
		size, err := strconv.ParseInt(entry.Size, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		plans = append(plans, filePlan{
			ArchivePath: entry.Path,
			SourcePath:  filepath.Join(work, filepath.FromSlash(entry.Path)),
			Size:        size,
			Sha256:      entry.Sha256,
		})
	}
	return plans
}
