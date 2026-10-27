package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newRetentionManager(t *testing.T) (*Manager, *Layout) {
	t.Helper()
	root := t.TempDir()
	layout := &Layout{
		BusinessRoot: filepath.Join(root, "data"),
		ControlDir:   filepath.Join(root, "control"),
		DatabaseRel:  "resource.db",
		ReleaseRel:   "release-snapshots",
	}
	if err := os.MkdirAll(layout.BusinessRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	return newTestManager(t, layout), layout
}

func createRetainedCopy(t *testing.T, layout *Layout, completedAt time.Time, withRecord bool) string {
	t.Helper()
	operationID := newUUID(t)
	dir := rollbackOperationDir(layout.ControlDir, operationID)
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "resource.db"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if withRecord {
		if err := writeRollbackRetention(layout.ControlDir, operationID, newUUID(t),
			completedAt.UTC().Format(time.RFC3339), rollbackRetentionRetained); err != nil {
			t.Fatal(err)
		}
	}
	return operationID
}

func TestRollbackRetentionDistinguishesSuccessfulAndUnknownCopies(t *testing.T) {
	manager, layout := newRetentionManager(t)
	createRetainedCopy(t, layout, time.Now(), true)
	if err := manager.checkRollbackState(); err != nil {
		t.Fatalf("retained copy should not block: %v", err)
	}

	unknown := createRetainedCopy(t, layout, time.Now(), false)
	err := manager.checkRollbackState()
	if err == nil || !isErrorStatus(err, 409) {
		t.Fatalf("unknown copy must block: %v", err)
	}
	if classified, ok := AsError(err); !ok || classified.Type != ErrRollbackReview {
		t.Fatalf("unknown copy error = %+v", err)
	}

	if err := writeRollbackRetention(layout.ControlDir, unknown, "job", time.Now().UTC().Format(time.RFC3339), rollbackRetentionRetained); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(retentionRecordPath(layout.ControlDir, unknown), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = manager.checkRollbackState()
	if classified, ok := AsError(err); !ok || classified.Type != ErrRollbackReview {
		t.Fatalf("corrupt retention must require review: %v", err)
	}
}

func TestRollbackRetentionPendingPlanStillBlocks(t *testing.T) {
	manager, layout := newRetentionManager(t)
	createRetainedCopy(t, layout, time.Now(), true)
	plan := &RestorePlan{
		OperationID: newUUID(t), JobID: newUUID(t),
		BusinessRoot: layout.BusinessRoot, CandidateRoot: filepath.Join(layout.ControlDir, "staging", "candidate"),
		RollbackRoot: filepath.Join(layout.ControlDir, "rollback", "pending"),
	}
	if err := savePlan(layout.ControlDir, plan); err != nil {
		t.Fatal(err)
	}
	err := manager.checkRollbackState()
	if classified, ok := AsError(err); !ok || classified.Type != ErrRestorePending {
		t.Fatalf("pending plan must win over retained proof: %v", err)
	}
}

func TestRollbackRetentionKeepsNewestConfiguredCopies(t *testing.T) {
	manager, layout := newRetentionManager(t)
	if err := manager.SetRollbackKeepCount(3); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-time.Hour)
	operations := make([]string, 0, 6)
	for index := 0; index < 6; index++ {
		operation := createRetainedCopy(t, layout, base.Add(time.Duration(index)*time.Minute), true)
		operations = append(operations, operation)
	}
	manager.CleanupSuccessfulRollbacks()
	remaining, err := listRollbackOperations(layout.ControlDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 3 {
		t.Fatalf("remaining copies = %v", remaining)
	}
	for _, expected := range operations[3:] {
		found := false
		for _, actual := range remaining {
			if actual == expected {
				found = true
			}
		}
		if !found {
			t.Fatalf("new copy %s was pruned; remaining=%v", expected, remaining)
		}
	}
	for _, expected := range operations[:3] {
		for _, actual := range remaining {
			if actual == expected {
				t.Fatalf("old copy %s was kept; remaining=%v", expected, remaining)
			}
		}
	}
	if err := manager.checkRollbackState(); err != nil {
		t.Fatalf("kept copies must remain non-blocking: %v", err)
	}
}

func TestRollbackRetentionCompletesInterruptedPruning(t *testing.T) {
	manager, layout := newRetentionManager(t)
	operation := createRetainedCopy(t, layout, time.Now().UTC(), true)
	if err := writeRollbackRetention(layout.ControlDir, operation, newUUID(t),
		time.Now().UTC().Format(time.RFC3339), rollbackRetentionPruning); err != nil {
		t.Fatal(err)
	}
	manager.CleanupSuccessfulRollbacks()
	if _, err := os.Stat(rollbackOperationDir(layout.ControlDir, operation)); !os.IsNotExist(err) {
		t.Fatalf("pruning copy should be removed: %v", err)
	}
}

func TestRollbackKeepCountValidation(t *testing.T) {
	manager, _ := newRetentionManager(t)
	for _, value := range []int{1, 3, 20} {
		if err := manager.SetRollbackKeepCount(value); err != nil {
			t.Fatalf("keep=%d: %v", value, err)
		}
		if manager.RollbackKeepCount() != value {
			t.Fatalf("keep=%d got %d", value, manager.RollbackKeepCount())
		}
	}
	for _, value := range []int{-1, 21} {
		if err := manager.SetRollbackKeepCount(value); err == nil {
			t.Fatalf("keep=%d should be rejected", value)
		}
	}
	if err := manager.SetRollbackKeepCount(0); err != nil || manager.RollbackKeepCount() != 3 {
		t.Fatalf("zero must default to 3: %v %d", err, manager.RollbackKeepCount())
	}
}

func TestRollbackKeepCountDefaultAndCapabilities(t *testing.T) {
	view := rollbackRetentionView(nil)
	if view.KeepCount != 3 || !view.Managed {
		t.Fatalf("default view = %+v", view)
	}
}
