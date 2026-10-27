package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

func isErrorStatus(err error, status int) bool {
	classified, ok := AsError(err)
	return ok && classified.HTTPStatus == status
}

func TestCancelPreparedRestoreKeepsOnlineData(t *testing.T) {
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

	created, err := manager.CreateBackup(context.Background(), "admin", "", newUUID(t))
	if err != nil {
		t.Fatal(err)
	}
	created = waitJob(t, manager, created.JobID, StateSucceeded)
	packagePath, _ := manager.backupPath(created.Result.BackupID)

	if err := db.Exec("UPDATE resource_config SET content = ? WHERE resource_key = ?",
		"online-kept", "site").Error; err != nil {
		t.Fatal(err)
	}

	uploadRequest := newUUID(t)
	_, tempPath, cleanup, err := manager.BeginUpload(uploadRequest)
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	validateJob = waitJob(t, manager, validateJob.JobID, StateSucceeded)
	preview, err := manager.GetPreview(*validateJob.PreviewID)
	if err != nil || !preview.Valid {
		t.Fatalf("preview: %+v err=%v", preview, err)
	}
	if _, err := manager.ExecuteRestore(context.Background(), ExecuteInput{
		PreviewID: preview.PreviewID, PackageSHA256: preview.PackageSHA256,
		TargetInstance: manager.InstanceID(), RequestID: newUUID(t),
		Confirmation: "WRONG", Actor: "admin",
	}); err == nil || !isErrorStatus(err, 400) {
		t.Fatalf("wrong confirmation should be rejected, got %v", err)
	}
	if _, err := manager.ExecuteRestore(context.Background(), ExecuteInput{
		PreviewID: preview.PreviewID, PackageSHA256: preview.PackageSHA256,
		TargetInstance: "other-instance", RequestID: newUUID(t),
		Confirmation: RestoreConfirmation, Actor: "admin",
	}); err == nil || !isErrorStatus(err, 409) {
		t.Fatalf("wrong target instance should be rejected, got %v", err)
	}
	if _, err := manager.ExecuteRestore(context.Background(), ExecuteInput{
		PreviewID: preview.PreviewID, PackageSHA256: strings.Repeat("0", 64),
		TargetInstance: manager.InstanceID(), RequestID: newUUID(t),
		Confirmation: RestoreConfirmation, Actor: "admin",
	}); err == nil || !isErrorStatus(err, 409) {
		t.Fatalf("wrong package digest should be rejected, got %v", err)
	}
	// A retained rollback copy is not a task-phase conflict. Surface an
	// actionable classification without starting a job or entering maintenance.
	rollbackOperation := newUUID(t)
	rollbackCopy := filepath.Join(layout.ControlDir, "rollback", rollbackOperation, "data")
	if err := os.MkdirAll(rollbackCopy, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ExecuteRestore(context.Background(), ExecuteInput{
		PreviewID: preview.PreviewID, PackageSHA256: preview.PackageSHA256,
		TargetInstance: manager.InstanceID(), RequestID: newUUID(t),
		Confirmation: RestoreConfirmation, Actor: "admin",
	}); err == nil {
		t.Fatal("restore should require rollback review")
	} else if classified, ok := AsError(err); !ok || classified.Type != ErrRollbackReview || classified.HTTPStatus != 409 {
		t.Fatalf("wrong rollback classification: %v", err)
	}
	if _, err := manager.StagePackage(context.Background(), "admin", newUUID(t), packagePath, ""); err == nil {
		t.Fatal("offline stage should require rollback review")
	} else if classified, ok := AsError(err); !ok || classified.Type != ErrRollbackReview {
		t.Fatalf("wrong offline rollback classification: %v", err)
	}
	if manager.Maintenance().Active() {
		t.Fatal("rollback rejection must not pause business writes")
	}
	if err := manager.CleanupRollback(rollbackOperation, RollbackCleanupConfirmation, "admin"); err != nil {
		t.Fatal(err)
	}
	restoreJob, err := manager.ExecuteRestore(context.Background(), ExecuteInput{
		PreviewID: preview.PreviewID, PackageSHA256: preview.PackageSHA256,
		TargetInstance: manager.InstanceID(), RequestID: newUUID(t),
		Confirmation: RestoreConfirmation, Actor: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	restoreJob = waitJob(t, manager, restoreJob.JobID, StateRestartRequired)

	if _, err := manager.ExecuteRestore(context.Background(), ExecuteInput{
		PreviewID: preview.PreviewID, PackageSHA256: preview.PackageSHA256,
		TargetInstance: manager.InstanceID(), RequestID: newUUID(t),
		Confirmation: RestoreConfirmation, Actor: "admin",
	}); err == nil || !isErrorStatus(err, 409) {
		t.Fatalf("second restore must be rejected while one is pending: %v", err)
	} else if classified, _ := AsError(err); classified.Type != ErrRestorePending {
		t.Fatalf("wrong pending restore classification: %v", err)
	}

	cancelled, err := manager.CancelRestore(context.Background(), restoreJob.JobID, newUUID(t), "admin")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.State != StateCancelled {
		t.Fatalf("state = %s", cancelled.State)
	}
	if manager.Maintenance().Active() {
		t.Fatal("cancel must release maintenance")
	}
	if plan, _ := loadPlan(layout.ControlDir); plan != nil {
		t.Fatal("cancel must remove the restore plan")
	}
	if journal, _ := loadJournal(layout.ControlDir); journal != nil {
		t.Fatal("cancel must remove the restore journal")
	}
	if entries, _ := os.ReadDir(filepath.Join(layout.ControlDir, "staging")); len(entries) != 0 {
		t.Fatalf("cancel must clean staging directories: %v", entries)
	}
	var content string
	if err := db.Raw("SELECT content FROM resource_config WHERE resource_key = ?", "site").
		Row().Scan(&content); err != nil {
		t.Fatal(err)
	}
	if content != "online-kept" {
		t.Fatalf("online data changed by cancel: %q", content)
	}
	var versionCount int64
	if err := db.Raw("SELECT COUNT(*) FROM config_release_version").Row().Scan(&versionCount); err != nil {
		t.Fatal(err)
	}
	if versionCount != 2 {
		t.Fatalf("online versions changed: %d", versionCount)
	}
	_ = model.Config{} // keep model import for seed helpers in this file
}
