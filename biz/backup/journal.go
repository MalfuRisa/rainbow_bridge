package backup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const (
	planFileName           = "restore-plan.json"
	journalFileName        = "restore-journal.json"
	journalHistoryFileName = "restore-journal.last.json"
	markerFileName         = ".rainbow-restore.json"
)

// Restore stages recorded in restore-journal.json. Every destructive step is
// bracketed by a durable stage update so a crash can be reconciled from the
// actual paths and the candidate marker.
const (
	StagePreparing        = "preparing"
	StagePrepared         = "prepared"
	StageSwitching        = "switching"
	StageOldMoved         = "old_moved"
	StageNewInstalled     = "new_installed"
	StageCommitted        = "committed"
	StageRolledBack       = "rolled_back"
	StageFailed           = "failed"
	StageRollbackRequired = "rollback_required"
)

// RestorePlan is the content prepared online and consumed at the next start.
type RestorePlan struct {
	OperationID   string `json:"operation_id"`
	JobID         string `json:"job_id"`
	PreviewID     string `json:"preview_id"`
	BackupID      string `json:"backup_id"`
	PackageSHA256 string `json:"package_sha256"`
	InstanceID    string `json:"instance_id"`
	BusinessRoot  string `json:"business_root"`
	CandidateRoot string `json:"candidate_root"`
	RollbackRoot  string `json:"rollback_root"`
	DatabaseRel   string `json:"database_rel"`
	CreatedAt     string `json:"created_at"`
}

// RestoreJournal is the crash-recovery record for the root swap.
type RestoreJournal struct {
	OperationID   string `json:"operation_id"`
	JobID         string `json:"job_id"`
	Stage         string `json:"stage"`
	BusinessRoot  string `json:"business_root"`
	CandidateRoot string `json:"candidate_root"`
	RollbackRoot  string `json:"rollback_root"`
	DatabaseRel   string `json:"database_rel"`
	PackageSHA256 string `json:"package_sha256"`
	UpdatedAt     string `json:"updated_at"`
	Detail        string `json:"detail,omitempty"`
}

// restoreMarker lives inside the candidate root. After a rename it proves that
// the currently installed root is the candidate from this operation.
type restoreMarker struct {
	OperationID   string `json:"operation_id"`
	JobID         string `json:"job_id"`
	BackupID      string `json:"backup_id"`
	PackageSHA256 string `json:"package_sha256"`
	CreatedAt     string `json:"created_at"`
}

func planPath(controlDir string) string    { return filepath.Join(controlDir, planFileName) }
func journalPath(controlDir string) string { return filepath.Join(controlDir, journalFileName) }

func loadPlan(controlDir string) (*RestorePlan, error) {
	payload, err := os.ReadFile(planPath(controlDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var plan RestorePlan
	if err := json.Unmarshal(payload, &plan); err != nil {
		return nil, err
	}
	if plan.OperationID == "" || plan.CandidateRoot == "" || plan.BusinessRoot == "" {
		return nil, errors.New("restore plan is incomplete")
	}
	return &plan, nil
}

func savePlan(controlDir string, plan *RestorePlan) error {
	payload, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(planPath(controlDir), payload, 0o600)
}

func removePlan(controlDir string) {
	_ = os.Remove(planPath(controlDir))
}

func loadJournal(controlDir string) (*RestoreJournal, error) {
	payload, err := os.ReadFile(journalPath(controlDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var journal RestoreJournal
	if err := json.Unmarshal(payload, &journal); err != nil {
		return nil, err
	}
	if journal.OperationID == "" || journal.Stage == "" {
		return nil, errors.New("restore journal is incomplete")
	}
	return &journal, nil
}

func loadJournalHistory(controlDir string) *RestoreJournal {
	payload, err := os.ReadFile(filepath.Join(controlDir, journalHistoryFileName))
	if err != nil {
		return nil
	}
	var journal RestoreJournal
	if err := json.Unmarshal(payload, &journal); err != nil {
		return nil
	}
	return &journal
}

func saveJournal(controlDir string, journal *RestoreJournal) error {
	journal.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	payload, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(journalPath(controlDir), payload, 0o600)
}

func readMarker(root string) (*restoreMarker, error) {
	payload, err := os.ReadFile(filepath.Join(root, markerFileName))
	if err != nil {
		return nil, err
	}
	var marker restoreMarker
	if err := json.Unmarshal(payload, &marker); err != nil {
		return nil, err
	}
	return &marker, nil
}

func writeMarker(root string, marker *restoreMarker) error {
	payload, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(root, markerFileName), payload, 0o600)
}
