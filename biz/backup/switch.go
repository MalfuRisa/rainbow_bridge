package backup

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// recoverRestore reconciles restore-journal.json before the database is
// opened. It returns true only when this boot actually installed a candidate,
// which lets startup roll back if later initialization fails.
func recoverRestore(ctx context.Context, manager *Manager) (bool, error) {
	control := manager.layout.ControlDir
	plan, err := loadPlan(control)
	if err != nil {
		return false, fmt.Errorf("restore plan is unreadable: %w", err)
	}
	journal, err := loadJournal(control)
	if err != nil {
		return false, fmt.Errorf("restore journal is unreadable: %w", err)
	}
	if journal == nil {
		if plan != nil {
			return false, errors.New("restore plan exists without a journal; manual review required")
		}
		return false, nil
	}

	switch journal.Stage {
	case StagePreparing:
		abortPreparing(manager, journal)
		return false, nil
	case StagePrepared, StageSwitching, StageOldMoved, StageNewInstalled:
		if plan == nil {
			return false, errors.New("restore journal has no matching plan; manual review required")
		}
		return switchRestore(ctx, manager, plan, journal)
	case StageCommitted:
		issues := validateRootBusiness(ctx, journal.BusinessRoot, journal.DatabaseRel,
			manager.layout.ReleaseRel, manager.limits, true)
		if len(issues) > 0 {
			return false, fmt.Errorf("committed restore root failed validation: %v", issues)
		}
		manager.markJobSucceeded(journal.JobID, "restore completed")
		if plan != nil {
			removePlan(control)
		}
		return false, nil
	case StageRolledBack, StageFailed:
		removePlan(control)
		return false, nil
	case StageRollbackRequired:
		return false, errors.New("restore rollback failed previously; keep the service stopped and use cmd/backup-restore rollback")
	default:
		return false, fmt.Errorf("unknown restore journal stage %q", journal.Stage)
	}
}

func abortPreparing(manager *Manager, journal *RestoreJournal) {
	if isWithin(filepath.Join(manager.layout.ControlDir, "staging"), journal.CandidateRoot) {
		_ = os.RemoveAll(journal.CandidateRoot)
	}
	removePlan(manager.layout.ControlDir)
	_ = os.Remove(journalPath(manager.layout.ControlDir))
	manager.failRestoreJob(journal.JobID, "preparation did not finish before the service stopped")
	manager.auditWrite("system", "restore_prepare", journal.OperationID, "failed", "preparation was incomplete")
}

func switchRestore(ctx context.Context, manager *Manager, plan *RestorePlan,
	journal *RestoreJournal) (bool, error) {
	if err := validatePlanPaths(manager, plan); err != nil {
		return false, err
	}
	rootInstalled := markerMatches(plan.BusinessRoot, plan.OperationID)
	candidateExists := dirExists(plan.CandidateRoot)
	rollbackExists := dirExists(plan.RollbackRoot)

	if rootInstalled {
		return true, commitInstalled(ctx, manager, plan, journal, rollbackExists)
	}
	if !candidateExists {
		return false, abandonWithoutCandidate(manager, plan, journal, rollbackExists)
	}
	if !markerMatches(plan.CandidateRoot, plan.OperationID) {
		return false, errors.New("candidate root does not match the prepared operation")
	}

	authAllowed := databaseHasAuthTables(plan.CandidateRoot, plan.DatabaseRel)
	issues := validateRootBusiness(ctx, plan.CandidateRoot, plan.DatabaseRel,
		manager.layout.ReleaseRel, manager.limits, authAllowed)
	if len(issues) > 0 {
		log.Printf("[backup] candidate validation failed: %v", issues)
		manager.failRestoreJob(plan.JobID, "prepared candidate failed validation")
		manager.auditWrite("system", "restore_switch", plan.OperationID, "failed", "candidate validation failed")
		_ = os.RemoveAll(plan.CandidateRoot)
		removePlan(manager.layout.ControlDir)
		markJournal(manager, journal, StageRolledBack, "candidate failed validation")
		return false, nil
	}

	if rollbackExists && dirExists(plan.BusinessRoot) {
		return false, errors.New("both the business root and a rollback copy exist; manual review required")
	}
	oldRoot := plan.BusinessRoot
	if rollbackExists {
		oldRoot = plan.RollbackRoot
	}
	authRows, err := readAuthRows(oldRoot, plan.DatabaseRel)
	if err != nil {
		return false, fmt.Errorf("read target administrator: %w", err)
	}
	if err := applyAuthRows(plan.CandidateRoot, plan.DatabaseRel, authRows); err != nil {
		return false, fmt.Errorf("preserve target administrator: %w", err)
	}
	if err := mergeReleaseNumberBounds(oldRoot, plan.CandidateRoot, plan.DatabaseRel); err != nil {
		log.Printf("[backup] release number merge failed: %v", err)
		manager.failRestoreJob(plan.JobID, "candidate numbering merge failed")
		manager.auditWrite("system", "restore_switch", plan.OperationID, "failed", "release number merge failed")
		_ = os.RemoveAll(plan.CandidateRoot)
		removePlan(manager.layout.ControlDir)
		markJournal(manager, journal, StageRolledBack, "candidate numbering merge failed")
		return false, nil
	}
	if issues := validateRootBusiness(ctx, plan.CandidateRoot, plan.DatabaseRel,
		manager.layout.ReleaseRel, manager.limits, true); len(issues) > 0 {
		manager.failRestoreJob(plan.JobID, "candidate failed validation after administrator preservation")
		_ = os.RemoveAll(plan.CandidateRoot)
		removePlan(manager.layout.ControlDir)
		markJournal(manager, journal, StageRolledBack, "candidate validation failed")
		return false, nil
	}

	if err := manager.audit.Write(AuditEvent{
		Actor: "system", Action: "restore_switch", Object: plan.OperationID,
		Result: "starting", Detail: "installing prepared candidate",
	}); err != nil {
		return false, fmt.Errorf("audit write failed; refusing destructive switch: %w", err)
	}

	if !rollbackExists {
		if !dirExists(plan.BusinessRoot) {
			return false, errors.New("business root is missing before the switch")
		}
		if err := markJournal(manager, journal, StageSwitching, "moving previous business root"); err != nil {
			return false, fmt.Errorf("persist switching stage: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(plan.RollbackRoot), 0o700); err != nil {
			return false, err
		}
		if _, err := os.Stat(plan.RollbackRoot); err == nil {
			return false, errors.New("rollback target already exists")
		}
		if err := os.Rename(plan.BusinessRoot, plan.RollbackRoot); err != nil {
			return false, fmt.Errorf("move previous business root: %w", err)
		}
		if err := syncDir(filepath.Dir(plan.RollbackRoot)); err != nil {
			return false, err
		}
		if err := markJournal(manager, journal, StageOldMoved, "previous business root preserved"); err != nil {
			return false, fmt.Errorf("persist old_moved stage: %w", err)
		}
	}

	if dirExists(plan.BusinessRoot) {
		return false, errors.New("business root unexpectedly exists during install")
	}
	if err := os.Rename(plan.CandidateRoot, plan.BusinessRoot); err != nil {
		rollbackErr := restoreOldRoot(plan)
		if rollbackErr != nil {
			markJournal(manager, journal, StageRollbackRequired, "candidate install and rollback both failed")
			return false, fmt.Errorf("install candidate: %v; rollback failed: %w", err, rollbackErr)
		}
		if err := markJournal(manager, journal, StageRolledBack, "candidate install failed; previous root restored"); err != nil {
			return false, fmt.Errorf("persist rolled_back stage: %w", err)
		}
		manager.failRestoreJob(plan.JobID, "candidate install failed; previous data restored")
		return false, nil
	}
	if err := syncDir(filepath.Dir(plan.BusinessRoot)); err != nil {
		return false, err
	}
	if err := markJournal(manager, journal, StageNewInstalled, "candidate installed"); err != nil {
		return false, fmt.Errorf("persist new_installed stage: %w", err)
	}

	if issues := validateRootBusiness(ctx, plan.BusinessRoot, plan.DatabaseRel,
		manager.layout.ReleaseRel, manager.limits, true); len(issues) > 0 {
		log.Printf("[backup] installed root validation failed: %v", issues)
		if err := restoreOldRoot(plan); err != nil {
			markJournal(manager, journal, StageRollbackRequired, "installed data invalid and rollback failed")
			return false, fmt.Errorf("installed data invalid: %v; rollback failed: %w", issues, err)
		}
		if err := markJournal(manager, journal, StageRolledBack, "installed data invalid; previous root restored"); err != nil {
			return false, fmt.Errorf("persist rolled_back stage: %w", err)
		}
		manager.failRestoreJob(plan.JobID, "restored data failed validation; previous data restored")
		manager.auditWrite("system", "restore_switch", plan.OperationID, "rolled_back", "installed data invalid")
		return false, nil
	}

	if err := markJournal(manager, journal, StageCommitted, "restore committed"); err != nil {
		return false, err
	}
	manager.markJobSucceeded(plan.JobID, "restore completed")
	removePlan(manager.layout.ControlDir)
	manager.auditWrite("system", "restore_switch", plan.OperationID, "succeeded", "candidate installed")
	return true, nil
}

func commitInstalled(ctx context.Context, manager *Manager, plan *RestorePlan,
	journal *RestoreJournal, rollbackExists bool) error {
	if rollbackExists {
		authRows, err := readAuthRows(plan.RollbackRoot, plan.DatabaseRel)
		if err != nil {
			return err
		}
		if err := applyAuthRows(plan.BusinessRoot, plan.DatabaseRel, authRows); err != nil {
			return err
		}
		if err := mergeReleaseNumberBounds(plan.RollbackRoot, plan.BusinessRoot, plan.DatabaseRel); err != nil {
			return fmt.Errorf("preserve release numbering across restart: %w", err)
		}
	}
	issues := validateRootBusiness(ctx, plan.BusinessRoot, plan.DatabaseRel,
		manager.layout.ReleaseRel, manager.limits, true)
	if len(issues) > 0 {
		if err := restoreOldRoot(plan); err != nil {
			markJournal(manager, journal, StageRollbackRequired, "final validation and rollback failed")
			return fmt.Errorf("final validation failed: %v; rollback failed: %w", issues, err)
		}
		markJournal(manager, journal, StageRolledBack, "final validation failed; previous root restored")
		manager.failRestoreJob(plan.JobID, "restored data failed final validation; previous data restored")
		return nil
	}
	if err := markJournal(manager, journal, StageCommitted, "restore committed"); err != nil {
		return err
	}
	manager.markJobSucceeded(plan.JobID, "restore completed")
	removePlan(manager.layout.ControlDir)
	manager.auditWrite("system", "restore_switch", plan.OperationID, "succeeded", "candidate installed")
	return nil
}

func abandonWithoutCandidate(manager *Manager, plan *RestorePlan,
	journal *RestoreJournal, rollbackExists bool) error {
	if rollbackExists && dirExists(plan.BusinessRoot) {
		return errors.New("candidate is missing and both roots exist; manual review required")
	}
	if rollbackExists {
		if err := restoreOldRoot(plan); err != nil {
			markJournal(manager, journal, StageRollbackRequired, "missing candidate and rollback failed")
			return err
		}
	}
	manager.failRestoreJob(plan.JobID, "prepared candidate is missing; previous data kept")
	manager.auditWrite("system", "restore_switch", plan.OperationID, "failed", "candidate missing")
	removePlan(manager.layout.ControlDir)
	markJournal(manager, journal, StageRolledBack, "candidate missing")
	return nil
}

// restoreOldRoot moves an installed candidate aside and renames the preserved
// rollback copy back into place. It never deletes the failed candidate.
func restoreOldRoot(plan *RestorePlan) error {
	if dirExists(plan.BusinessRoot) {
		if !markerMatches(plan.BusinessRoot, plan.OperationID) {
			return errors.New("business root is not the prepared candidate")
		}
		failedRoot := filepath.Join(filepath.Dir(plan.CandidateRoot),
			"failed-new-root-"+plan.OperationID)
		if err := os.MkdirAll(filepath.Dir(failedRoot), 0o700); err != nil {
			return err
		}
		if _, err := os.Stat(failedRoot); err == nil {
			return errors.New("failed candidate destination already exists")
		}
		if err := os.Rename(plan.BusinessRoot, failedRoot); err != nil {
			return err
		}
	}
	if dirExists(plan.RollbackRoot) {
		if err := os.MkdirAll(filepath.Dir(plan.BusinessRoot), 0o700); err != nil {
			return err
		}
		if err := os.Rename(plan.RollbackRoot, plan.BusinessRoot); err != nil {
			return err
		}
		return syncDir(filepath.Dir(plan.BusinessRoot))
	}
	if !dirExists(plan.BusinessRoot) {
		return errors.New("no business root or rollback copy remains")
	}
	return nil
}

func markJournal(manager *Manager, journal *RestoreJournal, stage, detail string) error {
	journal.Stage = stage
	journal.Detail = detail
	return saveJournal(manager.layout.ControlDir, journal)
}

func markerMatches(root, operationID string) bool {
	marker, err := readMarker(root)
	if err != nil {
		return false
	}
	return marker.OperationID == operationID
}

func validatePlanPaths(manager *Manager, plan *RestorePlan) error {
	if plan.InstanceID != manager.instanceID {
		return errors.New("restore plan belongs to another instance")
	}
	if !samePath(plan.BusinessRoot, manager.layout.BusinessRoot) {
		return errors.New("restore plan business root does not match this instance")
	}
	stagingRoot := filepath.Join(manager.layout.ControlDir, "staging")
	if !isWithin(stagingRoot, plan.CandidateRoot) {
		return errors.New("restore candidate is outside the control staging root")
	}
	rollbackRoot := filepath.Join(manager.layout.ControlDir, "rollback")
	if !isWithin(rollbackRoot, plan.RollbackRoot) {
		return errors.New("restore rollback path is outside the control rollback root")
	}
	if _, ok := safeRelativePath(plan.DatabaseRel); !ok {
		return errors.New("restore plan database path is invalid")
	}
	return nil
}

func (m *Manager) failRestoreJob(jobID, message string) {
	_, _ = m.patchJob(jobID, func(job *Job) {
		job.State = StateFailed
		job.Phase = "failed"
		job.Error = &JobError{Type: ErrInternal, Message: message}
		if job.Result == nil {
			job.Result = &JobResult{}
		}
		job.Result.RequiresRestart = false
	})
}
