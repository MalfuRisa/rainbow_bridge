package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	errCorruptRetention = errors.New("rollback retention record is corrupt")
	errUnsafeRollback   = errors.New("rollback control entry is unsafe")
)

const (
	rollbackRetentionFileName    = "retention.json"
	rollbackRetentionFormat      = 1
	rollbackRetentionRetained    = "retained"
	rollbackRetentionPruning     = "pruning"
	rollbackCleanupPendingAction = "rollback_cleanup_pending"
)

// rollbackRetentionRecord is the local proof that a rollback copy came from a
// fully initialized successful restore. It lives in the control directory and
// is never part of a business backup package.
type rollbackRetentionRecord struct {
	FormatVersion int    `json:"format_version"`
	OperationID   string `json:"operation_id"`
	JobID         string `json:"job_id"`
	CompletedAt   string `json:"completed_at"`
	Status        string `json:"status"`
}

func rollbackOperationDir(controlDir, operationID string) string {
	return filepath.Join(controlDir, "rollback", operationID)
}

func retentionRecordPath(controlDir, operationID string) string {
	return filepath.Join(rollbackOperationDir(controlDir, operationID), rollbackRetentionFileName)
}

func loadRollbackRetention(controlDir, operationID string) (*rollbackRetentionRecord, error) {
	if !safeSegment(operationID) {
		return nil, errors.New("rollback operation id is invalid")
	}
	payload, err := os.ReadFile(retentionRecordPath(controlDir, operationID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var record rollbackRetentionRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return nil, fmt.Errorf("%w: %v", errCorruptRetention, err)
	}
	if err := validateRetentionRecord(&record, operationID); err != nil {
		return nil, fmt.Errorf("%w: %v", errCorruptRetention, err)
	}
	return &record, nil
}

func validateRetentionRecord(record *rollbackRetentionRecord, operationID string) error {
	if record.FormatVersion != rollbackRetentionFormat {
		return errors.New("retention record format is unsupported")
	}
	if record.OperationID != operationID {
		return errors.New("retention record operation does not match its directory")
	}
	if strings.TrimSpace(record.JobID) == "" {
		return errors.New("retention record job is missing")
	}
	if record.Status != rollbackRetentionRetained && record.Status != rollbackRetentionPruning {
		return errors.New("retention record status is invalid")
	}
	if _, err := time.Parse(time.RFC3339, record.CompletedAt); err != nil {
		return errors.New("retention record completion time is invalid")
	}
	return nil
}

func writeRollbackRetention(controlDir, operationID, jobID, completedAt, status string) error {
	if !safeSegment(operationID) {
		return errors.New("rollback operation id is invalid")
	}
	if _, err := time.Parse(time.RFC3339, completedAt); err != nil {
		return errors.New("retention completion time is invalid")
	}
	record := &rollbackRetentionRecord{
		FormatVersion: rollbackRetentionFormat,
		OperationID:   operationID,
		JobID:         jobID,
		CompletedAt:   completedAt,
		Status:        status,
	}
	payload, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(retentionRecordPath(controlDir, operationID), payload, 0o600)
}

// checkRollbackState is shared by online prepare, offline stage and the CLI.
// Successful retained copies no longer block; pending or unverifiable state
// still does.
func (m *Manager) checkRollbackState() error {
	control := m.layout.ControlDir
	plan, err := loadPlan(control)
	if err != nil {
		return storageError(err)
	}
	if plan != nil {
		return restorePendingError()
	}
	journal, err := loadJournal(control)
	if err != nil {
		return storageError(err)
	}
	if journal != nil {
		switch journal.Stage {
		case StagePreparing, StagePrepared, StageSwitching, StageOldMoved, StageNewInstalled:
			return restorePendingError()
		case StageRollbackRequired:
			return rollbackReviewError()
		case StageCommitted:
			// A committed journal that survived startup means finalization did
			// not complete. Refuse a new restore instead of guessing.
			return storageError(errors.New("committed restore journal is awaiting finalization"))
		case StageRolledBack, StageFailed:
			// Terminal; no blocking copy remains.
		default:
			return rollbackReviewError()
		}
	}
	entries, err := listRollbackOperations(control)
	if err != nil {
		if errors.Is(err, errUnsafeRollback) {
			return rollbackReviewError()
		}
		return storageError(err)
	}
	for _, operationID := range entries {
		record, err := m.loadOrLegacyRetention(operationID)
		if err != nil {
			if errors.Is(err, errCorruptRetention) {
				return rollbackReviewError()
			}
			return storageError(err)
		}
		if record == nil {
			return rollbackReviewError()
		}
	}
	return nil
}

func listRollbackOperations(controlDir string) ([]string, error) {
	root := filepath.Join(controlDir, "rollback")
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !safeSegment(entry.Name()) {
			return nil, fmt.Errorf("%w: invalid directory name", errUnsafeRollback)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: symbolic link is not allowed", errUnsafeRollback)
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			continue
		}
		result = append(result, entry.Name())
	}
	sort.Strings(result)
	return result, nil
}

func (m *Manager) loadOrLegacyRetention(operationID string) (*rollbackRetentionRecord, error) {
	record, err := loadRollbackRetention(m.layout.ControlDir, operationID)
	if err != nil {
		return nil, err
	}
	if record != nil {
		return record, nil
	}
	return m.legacyRetention(operationID)
}

// legacyRetention recognizes only a historical copy whose archived journal,
// successful job, current installed-root marker and operation id agree. A
// directory name or timestamp alone is never evidence.
func (m *Manager) legacyRetention(operationID string) (*rollbackRetentionRecord, error) {
	history := loadJournalHistory(m.layout.ControlDir)
	if history == nil || history.OperationID != operationID || history.Stage != StageCommitted {
		return nil, nil
	}
	if !markerMatches(m.layout.BusinessRoot, operationID) {
		return nil, nil
	}
	job, err := m.store.Get(history.JobID)
	if err != nil {
		if classified, ok := AsError(err); ok && classified.Type == ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	if job.Kind != KindRestore || job.State != StateSucceeded {
		return nil, nil
	}
	completedAt := history.UpdatedAt
	if completedAt == "" {
		completedAt = job.UpdatedAt
	}
	if _, err := time.Parse(time.RFC3339, completedAt); err != nil {
		return nil, nil
	}
	return &rollbackRetentionRecord{
		FormatVersion: rollbackRetentionFormat,
		OperationID:   operationID,
		JobID:         history.JobID,
		CompletedAt:   completedAt,
		Status:        rollbackRetentionRetained,
	}, nil
}

type retainedRollback struct {
	operationID string
	record      *rollbackRetentionRecord
}

// CleanupSuccessfulRollbacks keeps the newest N fully verified successful
// copies and prunes older ones. Unknown copies are never deleted here.
func (m *Manager) CleanupSuccessfulRollbacks() {
	if m == nil || m.layout == nil {
		return
	}
	plan, planErr := loadPlan(m.layout.ControlDir)
	journal, journalErr := loadJournal(m.layout.ControlDir)
	if planErr != nil || journalErr != nil || plan != nil {
		return
	}
	if journal != nil && journal.Stage != StageRolledBack && journal.Stage != StageFailed {
		return
	}
	operations, err := listRollbackOperations(m.layout.ControlDir)
	if err != nil {
		log.Printf("[backup] rollback retention scan failed: %v", err)
		return
	}
	retained := make([]retainedRollback, 0, len(operations))
	for _, operationID := range operations {
		if referencedRollback(m.layout, operationID, plan, journal) {
			continue
		}
		record, err := m.loadOrLegacyRetention(operationID)
		if err != nil || record == nil {
			continue
		}
		if record.Status == rollbackRetentionPruning {
			// A previous cleanup was interrupted after persisting its intent.
			m.pruneRollbackCopy(retainedRollback{operationID: operationID, record: record})
			continue
		}
		if _, err := os.Stat(retentionRecordPath(m.layout.ControlDir, operationID)); err != nil {
			// Persist legacy evidence before it can be pruned.
			if err := writeRollbackRetention(m.layout.ControlDir, operationID,
				record.JobID, record.CompletedAt, rollbackRetentionRetained); err != nil {
				log.Printf("[backup] rollback retention proof write failed: %v", err)
				continue
			}
		}
		retained = append(retained, retainedRollback{operationID: operationID, record: record})
	}
	sort.Slice(retained, func(i, j int) bool {
		left, leftErr := time.Parse(time.RFC3339, retained[i].record.CompletedAt)
		right, rightErr := time.Parse(time.RFC3339, retained[j].record.CompletedAt)
		if leftErr == nil && rightErr == nil && !left.Equal(right) {
			return left.After(right)
		}
		return retained[i].operationID < retained[j].operationID
	})
	keep := m.RollbackKeepCount()
	for index := keep; index < len(retained); index++ {
		m.pruneRollbackCopy(retained[index])
	}
}

func referencedRollback(layout *Layout, operationID string, plan *RestorePlan, journal *RestoreJournal) bool {
	candidate := rollbackOperationDir(layout.ControlDir, operationID)
	if plan != nil && sameCleanPath(filepath.Dir(plan.RollbackRoot), candidate) {
		return true
	}
	if journal != nil && sameCleanPath(filepath.Dir(journal.RollbackRoot), candidate) {
		return true
	}
	return false
}

func sameCleanPath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}

func (m *Manager) pruneRollbackCopy(copy retainedRollback) {
	if !safeSegment(copy.operationID) {
		return
	}
	root := filepath.Join(m.layout.ControlDir, "rollback")
	target := rollbackOperationDir(m.layout.ControlDir, copy.operationID)
	if !isWithin(root, target) {
		return
	}
	info, err := os.Lstat(target)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	if err := writeRollbackRetention(m.layout.ControlDir, copy.operationID,
		copy.record.JobID, copy.record.CompletedAt, rollbackRetentionPruning); err != nil {
		log.Printf("[backup] rollback retention pruning state failed: %v", err)
		m.auditWrite("system", rollbackCleanupPendingAction, copy.operationID, "failed",
			"could not persist pruning state")
		return
	}
	if err := os.RemoveAll(target); err != nil {
		log.Printf("[backup] rollback retention cleanup failed: %v", err)
		m.auditWrite("system", rollbackCleanupPendingAction, copy.operationID, "failed",
			"rollback copy could not be removed")
		return
	}
	m.auditWrite("system", "rollback_cleanup", copy.operationID, "succeeded",
		fmt.Sprintf("kept newest %d successful copies", m.RollbackKeepCount()))
}
