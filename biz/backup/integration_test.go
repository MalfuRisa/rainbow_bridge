package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/pkg/config"
	"github.com/yi-nology/rainbow_bridge/pkg/database"
	"gorm.io/gorm"
)

func TestBackupRestoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	layout := &Layout{
		BusinessRoot: filepath.Join(root, "data"),
		ControlDir:   filepath.Join(root, "control"),
		DatabasePath: filepath.Join(root, "data", "resource.db"),
		DatabaseRel:  "resource.db",
		UploadsDir:   filepath.Join(root, "data", "uploads"),
		ReleaseDir:   filepath.Join(root, "data", "release-snapshots"),
		ReleaseRel:   "release-snapshots",
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

	created, err := manager.CreateBackup(context.Background(), "admin", "before-change", uuid.NewString())
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	created = waitJob(t, manager, created.JobID, StateSucceeded)
	if created.Result == nil || created.Result.BackupID == "" {
		t.Fatalf("backup result missing: %+v", created)
	}
	backupID := created.Result.BackupID
	packagePath, _ := manager.backupPath(backupID)
	if _, err := os.Stat(packagePath); err != nil {
		t.Fatalf("backup package missing: %v", err)
	}

	// The downloaded database must not contain authentication tables.
	inspectDir := filepath.Join(root, "inspect")
	info, issues, err := inspectPackage(context.Background(), packagePath, inspectDir, manager.limits)
	if err != nil || len(issues) > 0 {
		t.Fatalf("inspect backup: err=%v issues=%v", err, issues)
	}
	if info.Manifest == nil || info.Manifest.Format != PackageFormat {
		t.Fatalf("unexpected manifest: %+v", info.Manifest)
	}
	if info.Manifest.Counts.DeletedVersions != 1 {
		t.Fatalf("deleted version missing from counts: %+v", info.Manifest.Counts)
	}
	// The downloaded database must not contain authentication tables at all.
	packageDB, err := sql.Open("sqlite", readOnlySQLiteDSN(
		filepath.Join(inspectDir, filepath.FromSlash(DatabaseEntry))))
	if err != nil {
		t.Fatal(err)
	}
	var authTables int
	if err := packageDB.QueryRow(
		"SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN ('auth_users','auth_sessions')").
		Scan(&authTables); err != nil {
		_ = packageDB.Close()
		t.Fatal(err)
	}
	_ = packageDB.Close()
	if authTables != 0 {
		t.Fatal("backup database contains authentication tables")
	}

	// Change live data after the backup: restore must bring the old content
	// back and reject stale edit baselines.
	if err := db.Exec("UPDATE resource_config SET content = ?, edit_revision = ? WHERE resource_key = ?",
		"changed-later", uuid.NewString(), "site").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Config{
		EnvironmentKey: "prod", PipelineKey: "main", ResourceKey: "new-after-backup",
		Content: "new", Type: "text", EditRevision: uuid.NewString(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	// Upload the backup package through the same streaming/idempotent path.
	uploadRequest := uuid.NewString()
	_, tempPath, cleanup, err := manager.BeginUpload(uploadRequest)
	if err != nil {
		t.Fatalf("begin upload: %v", err)
	}
	defer cleanup()
	payload, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tempPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	validateJob, err := manager.FinishUpload(context.Background(), "admin", uploadRequest, tempPath,
		int64(len(payload)), hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatalf("finish upload: %v", err)
	}
	validateJob = waitJob(t, manager, validateJob.JobID, StateSucceeded)
	if validateJob.PreviewID == nil {
		t.Fatalf("validation produced no preview: %+v", validateJob)
	}
	preview, err := manager.GetPreview(*validateJob.PreviewID)
	if err != nil {
		t.Fatalf("get preview: %v", err)
	}
	if !preview.Valid {
		t.Fatalf("preview invalid: %v", preview.Issues)
	}

	restoreJob, err := manager.ExecuteRestore(context.Background(), ExecuteInput{
		PreviewID:      preview.PreviewID,
		PackageSHA256:  preview.PackageSHA256,
		TargetInstance: manager.InstanceID(),
		RequestID:      uuid.NewString(),
		Confirmation:   RestoreConfirmation,
		Reason:         "round trip",
		Actor:          "admin",
	})
	if err != nil {
		t.Fatalf("execute restore: %v", err)
	}
	restoreJob = waitJob(t, manager, restoreJob.JobID, StateRestartRequired)
	if !manager.Maintenance().Active() {
		t.Fatal("maintenance should stay active until restart")
	}

	// A second process would open the same database after this point.
	closeGormOrFail(t, db)

	restartManager := newTestManager(t, layout)
	switched, err := recoverRestore(context.Background(), restartManager)
	if err != nil {
		t.Fatalf("startup switch: %v", err)
	}
	if !switched {
		t.Fatal("startup did not install the prepared candidate")
	}
	restored, err := restartManager.JobStatus(restoreJob.JobID)
	if err != nil || restored.State != StateSucceeded {
		t.Fatalf("restore job not succeeded: %+v err=%v", restored, err)
	}

	restoredDB := openTestDatabase(t, layout.DatabasePath)
	defer closeGormOrFail(t, restoredDB)
	var content, revision string
	if err := restoredDB.Raw("SELECT content, edit_revision FROM resource_config WHERE resource_key = ?", "site").
		Row().Scan(&content, &revision); err != nil {
		t.Fatal(err)
	}
	if content != "hello-backup" {
		t.Fatalf("restored content = %q", content)
	}
	if revision == "" {
		t.Fatal("edit revision was not regenerated")
	}
	var bigContent string
	if err := restoredDB.Raw("SELECT content FROM resource_config WHERE resource_key = ?", "big").
		Row().Scan(&bigContent); err != nil {
		t.Fatal(err)
	}
	if bigContent != `{"n":9223372036854775807}` {
		t.Fatalf("big integer content changed: %q", bigContent)
	}
	var deletedVersions int64
	if err := restoredDB.Raw("SELECT COUNT(*) FROM config_release_version WHERE deleted_at IS NOT NULL").
		Row().Scan(&deletedVersions); err != nil {
		t.Fatal(err)
	}
	if deletedVersions != 1 {
		t.Fatalf("deleted version history was not restored: %d", deletedVersions)
	}
	var newCount int64
	if err := restoredDB.Raw("SELECT COUNT(*) FROM resource_config WHERE resource_key = ?",
		"new-after-backup").Row().Scan(&newCount); err != nil {
		t.Fatal(err)
	}
	if newCount != 0 {
		t.Fatal("data written after the backup leaked into the restore")
	}

	var passwordHash string
	var credentialVersion int64
	if err := restoredDB.Raw("SELECT password_hash, credential_version FROM auth_users WHERE username = ?", "admin").
		Row().Scan(&passwordHash, &credentialVersion); err != nil {
		t.Fatal(err)
	}
	if passwordHash != "hash-old" || credentialVersion != 7 {
		t.Fatalf("target administrator was not preserved: hash=%q version=%d", passwordHash, credentialVersion)
	}
	var sessions int64
	if err := restoredDB.Raw("SELECT COUNT(*) FROM auth_sessions").Row().Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 {
		t.Fatal("old sessions must not survive restore")
	}

	rollbackDirs, err := os.ReadDir(filepath.Join(layout.ControlDir, "rollback"))
	if err != nil || len(rollbackDirs) != 1 {
		t.Fatalf("expected one preserved rollback copy: dirs=%v err=%v", rollbackDirs, err)
	}

	// A later boot reconciles the committed journal and continues normally.
	secondManager := newTestManager(t, layout)
	switched, err = recoverRestore(context.Background(), secondManager)
	if err != nil || switched {
		t.Fatalf("second boot: switched=%v err=%v", switched, err)
	}
	instance := &Instance{Manager: secondManager, Layout: layout, Switched: true}
	instance.CompleteStartup()
	if journal, _ := loadJournal(layout.ControlDir); journal != nil {
		t.Fatal("committed journal should be archived after successful startup")
	}
	history := loadJournalHistory(layout.ControlDir)
	if history == nil || history.Stage != StageCommitted {
		t.Fatal("committed journal history was not preserved")
	}
	proof, err := loadRollbackRetention(layout.ControlDir, history.OperationID)
	if err != nil || proof == nil || proof.Status != rollbackRetentionRetained ||
		proof.JobID != restoreJob.JobID {
		t.Fatalf("retention proof missing: %+v err=%v", proof, err)
	}
	if err := secondManager.checkRollbackState(); err != nil {
		t.Fatalf("successful retained copy must not block another restore: %v", err)
	}
	// A future version may add tables after the switch committed. Later boots
	// must not re-validate the old journal and reject them.
	if err := restoredDB.Exec("CREATE TABLE future_feature (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	thirdManager := newTestManager(t, layout)
	switched, err = recoverRestore(context.Background(), thirdManager)
	if err != nil || switched {
		t.Fatalf("third boot after schema growth: switched=%v err=%v", switched, err)
	}
}

func openTestDatabase(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := database.Open(config.DatabaseConfig{
		Driver: "sqlite", SQLite: config.SQLiteConfig{Path: path},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&model.Config{}, &model.Asset{}, &model.Environment{}, &model.Pipeline{},
		&model.ConfigFreezeState{}, &model.ConfigFreezeEvent{}, &model.ConfigWriteGate{},
		&model.ConfigReleaseVersion{}, &model.ConfigReleaseAsset{}, &model.ConfigReleaseHead{},
		&model.ConfigReleaseEvent{}, &model.ScopeLifecycleEvent{},
		&model.AuthUser{}, &model.AuthSession{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedBusiness(t *testing.T, db *gorm.DB, layout *Layout) {
	t.Helper()
	if err := db.Create(&model.Environment{EnvironmentKey: "prod", EnvironmentName: "Prod",
		SortOrder: 0, IsActive: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Pipeline{EnvironmentKey: "prod", PipelineKey: "main",
		PipelineName: "Main", SortOrder: 0, IsActive: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Config{EnvironmentKey: "prod", PipelineKey: "main",
		ResourceKey: "site", Alias: "site", Name: "Site", Content: "hello-backup",
		Type: "text", EditRevision: uuid.NewString()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Config{EnvironmentKey: "prod", PipelineKey: "main",
		ResourceKey: "big", Alias: "big", Name: "Big", Content: `{"n":9223372036854775807}`,
		Type: "object", EditRevision: uuid.NewString()}).Error; err != nil {
		t.Fatal(err)
	}

	assetID := "asset-1"
	fileName := "note.txt"
	assetBytes := []byte("asset-bytes")
	assetPath := filepath.Join(layout.UploadsDir, assetID, fileName)
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetPath, assetBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Asset{FileID: assetID, EnvironmentKey: "prod", PipelineKey: "main",
		FileName: fileName, ContentType: "text/plain", FileSize: int64(len(assetBytes)),
		Path: filepath.Join("uploads", assetID, fileName)}).Error; err != nil {
		t.Fatal(err)
	}

	snapshotID := "snapshot-1"
	releaseAssetID := "release-asset-1"
	releaseBytes := []byte("release-bytes")
	releaseSum := sha256.Sum256(releaseBytes)
	releasePath := filepath.Join(layout.ReleaseDir, snapshotID, "assets", releaseAssetID+".bin")
	if err := os.MkdirAll(filepath.Dir(releasePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(releasePath, releaseBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	payload := `{"version":"v1"}`
	payloadSum := sha256.Sum256([]byte(payload))
	manifestDigest := testManifestDigest(releaseAssetID, assetID, "release.txt", "text/plain",
		int64(len(releaseBytes)), hex.EncodeToString(releaseSum[:]))
	if err := db.Create(&model.ConfigReleaseVersion{
		SnapshotID: snapshotID, EnvironmentKey: "prod", PipelineKey: "main", Version: "v1",
		CreationRequestID: "request-1", RequestFingerprint: strings.Repeat("a", 64),
		Payload: payload, PayloadSha256: hex.EncodeToString(payloadSum[:]),
		ManifestSha256: manifestDigest, ConfigCount: 1, AssetCount: 1,
		AssetBytes: int64(len(releaseBytes)),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ConfigReleaseAsset{SnapshotID: snapshotID, AssetID: releaseAssetID,
		SourceFileID: assetID, FileName: "release.txt", ContentType: "text/plain",
		Size: int64(len(releaseBytes)), Sha256: hex.EncodeToString(releaseSum[:])}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ConfigReleaseHead{EnvironmentKey: "prod", PipelineKey: "main",
		ActiveSnapshotID: snapshotID, Revision: 3}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	deletedPayload := `{"version":"v0"}`
	deletedSum := sha256.Sum256([]byte(deletedPayload))
	emptyManifest := sha256.Sum256(nil)
	if err := db.Create(&model.ConfigReleaseVersion{
		SnapshotID: "snapshot-deleted", EnvironmentKey: "prod", PipelineKey: "main", Version: "v0",
		CreationRequestID: "request-2", RequestFingerprint: strings.Repeat("b", 64),
		Payload: deletedPayload, PayloadSha256: hex.EncodeToString(deletedSum[:]),
		ManifestSha256: hex.EncodeToString(emptyManifest[:]), ConfigCount: 1,
		DeletedAt: &now, DeletedBy: "admin", DeleteReason: "history",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AuthUser{Username: "admin", PasswordHash: "hash-old",
		Role: model.AuthRoleAdmin, CredentialVersion: 7, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AuthSession{TokenHash: "token-hash", UserID: 1, CredentialVersion: 7,
		CSRFToken: "csrf", CreatedAt: now, LastSeenAt: now,
		ExpiresAt: now.Add(time.Hour), IdleExpiresAt: now.Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
}

func testManifestDigest(assetID, sourceFileID, fileName, contentType string, size int64, sha string) string {
	hasher := sha256.New()
	fmt.Fprintf(hasher, "%s\t%s\t%s\t%s\t%d\t%s\n", assetID, sourceFileID, fileName, contentType, size, sha)
	return hex.EncodeToString(hasher.Sum(nil))
}

func newTestManager(t *testing.T, layout *Layout) *Manager {
	t.Helper()
	limits := DefaultLimits()
	limits.CreateTimeout = 30 * time.Second
	limits.ValidateTimeout = 30 * time.Second
	limits.PrepareTimeout = 30 * time.Second
	limits.MaintenanceWait = 2 * time.Second
	manager, err := NewManager(layout, limits, Producer{AppVersion: "test", GitCommit: "test"}, "instance-test")
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func waitJob(t *testing.T, manager *Manager, jobID, want string) *Job {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		job, err := manager.JobStatus(jobID)
		if err != nil {
			t.Fatal(err)
		}
		if job.State == want {
			return job
		}
		if job.State == StateFailed || job.State == StateInterrupted || job.State == StateCancelled {
			if want != job.State {
				t.Fatalf("job reached %s before %s: %+v", job.State, want, job.Error)
			}
			return job
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach %s", jobID, want)
	return nil
}

func closeGormOrFail(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := closeGorm(db); err != nil {
		t.Fatal(err)
	}
}
