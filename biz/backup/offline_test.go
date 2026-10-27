package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupRollbackRequiresExplicitConfirmation(t *testing.T) {
	root := t.TempDir()
	layout := &Layout{
		BusinessRoot: filepath.Join(root, "data"), ControlDir: filepath.Join(root, "control"),
		DatabasePath: filepath.Join(root, "data", "resource.db"), DatabaseRel: "resource.db",
		UploadsDir: filepath.Join(root, "data", "uploads"),
		ReleaseDir: filepath.Join(root, "data", "release-snapshots"), ReleaseRel: "release-snapshots",
	}
	rollbackDir := filepath.Join(layout.ControlDir, "rollback", "op-1", "data")
	if err := os.MkdirAll(rollbackDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rollbackDir, "resource.db"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t, layout)

	if err := manager.CleanupRollback("op-1", "wrong", "operator"); err == nil || !isErrorStatus(err, 400) {
		t.Fatalf("wrong confirmation should be rejected: %v", err)
	}
	if _, err := os.Stat(rollbackDir); err != nil {
		t.Fatal("rollback copy must survive a rejected cleanup")
	}
	if err := manager.CleanupRollback("op-1", RollbackCleanupConfirmation, "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(layout.ControlDir, "rollback", "op-1")); !os.IsNotExist(err) {
		t.Fatal("rollback copy was not removed")
	}
}

func TestCleanupRollbackRefusesIncompleteSwitch(t *testing.T) {
	root := t.TempDir()
	layout := &Layout{
		BusinessRoot: filepath.Join(root, "data"), ControlDir: filepath.Join(root, "control"),
		DatabasePath: filepath.Join(root, "data", "resource.db"), DatabaseRel: "resource.db",
		UploadsDir: filepath.Join(root, "data", "uploads"),
		ReleaseDir: filepath.Join(root, "data", "release-snapshots"), ReleaseRel: "release-snapshots",
	}
	rollbackDir := filepath.Join(layout.ControlDir, "rollback", "op-2", "data")
	if err := os.MkdirAll(rollbackDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t, layout)
	journal := &RestoreJournal{
		OperationID: "op-2", JobID: "job-2", Stage: StageOldMoved,
		BusinessRoot: layout.BusinessRoot, RollbackRoot: rollbackDir,
	}
	if err := saveJournal(layout.ControlDir, journal); err != nil {
		t.Fatal(err)
	}
	if err := manager.CleanupRollback("op-2", RollbackCleanupConfirmation, "operator"); err == nil ||
		!isErrorStatus(err, 409) {
		t.Fatalf("incomplete switch cleanup should be refused: %v", err)
	}
}
