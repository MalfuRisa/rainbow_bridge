package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// RestoreConfirmation is the fixed phrase required by the execute endpoint.
const RestoreConfirmation = "RESTORE_ALL_BUSINESS_DATA"

// ExecuteInput is the service-layer input for POST /backup/restore/execute.
type ExecuteInput struct {
	PreviewID      string
	PackageSHA256  string
	TargetInstance string
	RequestID      string
	Confirmation   string
	Reason         string
	Actor          string
}

// ExecuteRestore validates the preview and launches candidate preparation.
func (m *Manager) ExecuteRestore(ctx context.Context, input ExecuteInput) (*Job, error) {
	if err := validateRequestID(input.RequestID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Confirmation) != RestoreConfirmation {
		return nil, invalidRequest("confirmation phrase does not match")
	}
	if err := validReason(input.Reason); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.PreviewID) == "" || strings.TrimSpace(input.PackageSHA256) == "" {
		return nil, invalidRequest("preview_id and package_sha256 are required")
	}
	if strings.TrimSpace(input.TargetInstance) == "" {
		return nil, invalidRequest("target_instance_id is required")
	}
	key := fingerprint(KindRestore, input.RequestID, input.PreviewID, input.PackageSHA256,
		input.TargetInstance, input.Reason)
	if existing, err := m.store.FindByRequest(KindRestore, input.RequestID); err != nil {
		return nil, err
	} else if existing != nil {
		if existing.Fingerprint != key {
			return nil, &Error{HTTPStatus: 409, Type: ErrRequestConflict,
				Message: "request_id was already used with different fields"}
		}
		return existing, nil
	}

	preview, err := m.GetPreview(input.PreviewID)
	if err != nil {
		return nil, err
	}
	if !preview.Valid {
		return nil, packageInvalid("restore preview is not valid")
	}
	if preview.Backup == nil {
		return nil, packageInvalid("restore preview has no backup summary")
	}
	if !strings.EqualFold(preview.PackageSHA256, input.PackageSHA256) {
		return nil, targetMismatch("package digest does not match the preview")
	}
	if input.TargetInstance != preview.TargetInstanceID {
		return nil, &Error{HTTPStatus: 409, Type: ErrTargetMismatch,
			Message: "target_instance_id does not match this instance"}
	}
	if m.pendingRestorePlan() {
		return nil, restorePendingError()
	}
	if err := m.checkRollbackState(); err != nil {
		return nil, err
	}
	packagePath := filepath.Join(m.importDir(preview.JobID), "package.zip")
	size, digest, err := verifyRegularFile(packagePath)
	if err != nil {
		return nil, storageError(err)
	}
	if size > m.limits.MaxPackageBytes || !strings.EqualFold(digest, preview.PackageSHA256) {
		return nil, targetMismatch("uploaded package content changed after validation")
	}
	if err := m.acquireWorker(); err != nil {
		return nil, err
	}
	job := newJob(KindRestore, input.RequestID, input.Actor, strings.TrimSpace(input.Reason), key)
	job.PreviewID = stringPointer(preview.PreviewID)
	job.BackupID = stringPointer(preview.Backup.BackupID)
	job.PackageSHA = preview.PackageSHA256
	job.Result = &JobResult{
		BackupID:        preview.Backup.BackupID,
		PreviewID:       preview.PreviewID,
		PackageSha256:   preview.PackageSHA256,
		RequiresRestart: true,
	}
	stored, err := m.store.Create(job)
	if err != nil {
		m.releaseWorker()
		return nil, err
	}
	if stored.JobID != job.JobID {
		m.releaseWorker()
		return stored, nil
	}
	go m.runJob(job, m.limits.PrepareTimeout, func(workerCtx context.Context, jobID string) error {
		return m.prepareCandidate(workerCtx, jobID, preview)
	})
	return stored, nil
}

func (m *Manager) prepareCandidate(ctx context.Context, jobID string, preview *Preview) error {
	job, err := m.setPhase(jobID, "preparing_candidate")
	if err != nil {
		return err
	}
	staging := m.stagingDir(jobID)
	packageRoot := filepath.Join(staging, "package")
	candidateRoot := filepath.Join(staging, "candidate")
	defer func() {
		_ = os.RemoveAll(packageRoot)
	}()

	packagePath := filepath.Join(m.importDir(preview.JobID), "package.zip")
	info, issues, err := inspectPackage(ctx, packagePath, packageRoot, m.limits)
	if err != nil {
		m.auditWrite(job.Actor, "restore_prepare", jobID, "failed", "package validation error")
		return err
	}
	if len(issues) > 0 || info.Manifest == nil {
		m.auditWrite(job.Actor, "restore_prepare", jobID, "failed", "package rejected")
		return packageInvalid("package no longer validates")
	}
	if !strings.EqualFold(info.PackageSHA256, preview.PackageSHA256) {
		return targetMismatch("package digest changed after preview")
	}
	required := info.ExtractedSize*2 + fileSize(m.layout.DatabasePath)
	if err := m.ensureFreeSpace(required); err != nil {
		return err
	}

	if err := m.buildCandidate(ctx, candidateRoot, packageRoot, info, preview); err != nil {
		m.auditWrite(job.Actor, "restore_prepare", jobID, "failed", "candidate build failed")
		_ = os.RemoveAll(candidateRoot)
		return err
	}

	// Only finalize under maintenance; candidate construction above never
	// reads the live business root.
	maintenanceCtx, cancel := context.WithTimeout(ctx, m.limits.MaintenanceWait)
	defer cancel()
	if err := m.maintenance.Enter(maintenanceCtx); err != nil {
		_ = os.RemoveAll(candidateRoot)
		m.auditWrite(job.Actor, "restore_prepare", jobID, "failed", "maintenance drain failed")
		return err
	}
	held := true
	fail := func(err error) error {
		if held {
			m.maintenance.Leave()
			held = false
		}
		_ = os.RemoveAll(candidateRoot)
		removePlan(m.layout.ControlDir)
		_ = os.Remove(journalPath(m.layout.ControlDir))
		return err
	}

	if _, digest, err := verifyRegularFile(packagePath); err != nil ||
		!strings.EqualFold(digest, preview.PackageSHA256) {
		return fail(targetMismatch("package digest changed during preparation"))
	}

	operationID := uuid.NewString()
	plan := &RestorePlan{
		OperationID:   operationID,
		JobID:         jobID,
		PreviewID:     preview.PreviewID,
		BackupID:      preview.Backup.BackupID,
		PackageSHA256: preview.PackageSHA256,
		InstanceID:    m.instanceID,
		BusinessRoot:  m.layout.BusinessRoot,
		CandidateRoot: candidateRoot,
		RollbackRoot:  filepath.Join(m.layout.ControlDir, "rollback", operationID, "data"),
		DatabaseRel:   m.layout.DatabaseRel,
		CreatedAt:     m.now().Format(time.RFC3339),
	}
	journal := &RestoreJournal{
		OperationID:   operationID,
		JobID:         jobID,
		Stage:         StagePreparing,
		BusinessRoot:  plan.BusinessRoot,
		CandidateRoot: plan.CandidateRoot,
		RollbackRoot:  plan.RollbackRoot,
		DatabaseRel:   plan.DatabaseRel,
		PackageSHA256: plan.PackageSHA256,
	}
	if err := m.audit.Write(AuditEvent{
		Actor: job.Actor, Action: "restore_prepare", Object: operationID,
		Result: "prepared", Detail: job.Reason,
	}); err != nil {
		return fail(storageError(err))
	}
	if err := saveJournal(m.layout.ControlDir, journal); err != nil {
		return fail(storageError(err))
	}
	if err := writeMarker(candidateRoot, &restoreMarker{
		OperationID:   operationID,
		JobID:         jobID,
		BackupID:      preview.Backup.BackupID,
		PackageSHA256: preview.PackageSHA256,
		CreatedAt:     plan.CreatedAt,
	}); err != nil {
		return fail(storageError(err))
	}
	if err := savePlan(m.layout.ControlDir, plan); err != nil {
		return fail(storageError(err))
	}
	journal.Stage = StagePrepared
	if err := saveJournal(m.layout.ControlDir, journal); err != nil {
		return fail(storageError(err))
	}
	if _, err := m.patchJob(jobID, func(record *Job) {
		record.State = StateRestartRequired
		record.Phase = "waiting_for_restart"
		record.Error = nil
		record.Result = &JobResult{
			PreviewID:       preview.PreviewID,
			PackageSha256:   preview.PackageSHA256,
			BackupID:        preview.Backup.BackupID,
			RequiresRestart: true,
			Message:         "restart the service to install the prepared data",
		}
	}); err != nil {
		return fail(storageError(err))
	}
	return restartRequired{}
}

func (m *Manager) buildCandidate(ctx context.Context, candidateRoot, packageRoot string,
	info *PackageInfo, preview *Preview) error {
	if err := os.MkdirAll(candidateRoot, 0o700); err != nil {
		return storageError(err)
	}
	if err := m.copyCandidateFiles(ctx, packageRoot, candidateRoot, info.Manifest); err != nil {
		return storageError(err)
	}

	sourcePath := filepath.Join(packageRoot, filepath.FromSlash(DatabaseEntry))
	source, err := openSQLite(sourcePath)
	if err != nil {
		return packageInvalid("package database cannot be opened")
	}
	defer closeGorm(source)

	databasePath, ok := joinWithin(candidateRoot, m.layout.DatabaseRel)
	if !ok {
		return internalError(errors.New("invalid database path"))
	}
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		return storageError(err)
	}
	_ = os.Remove(databasePath)
	candidate, err := openBusinessDB(databasePath)
	if err != nil {
		return storageError(err)
	}
	defer closeGorm(candidate)
	if err := copyBusinessTables(ctx, source, candidate); err != nil {
		return storageError(err)
	}
	if err := regenerateRevisions(ctx, candidate); err != nil {
		return storageError(err)
	}
	if err := normalizeAssetPaths(ctx, candidate); err != nil {
		return storageError(err)
	}
	if err := checkIntegrity(ctx, candidate); err != nil {
		return packageInvalid("candidate database failed integrity check")
	}
	issues, err := validateExtractedDatabase(ctx, databasePath, info.Manifest, m.limits)
	if err != nil {
		return storageError(err)
	}
	if len(issues) > 0 {
		return packageInvalid("candidate database failed validation")
	}
	_ = os.RemoveAll(filepath.Join(candidateRoot, "database"))
	return nil
}

func (m *Manager) copyCandidateFiles(ctx context.Context, packageRoot, candidateRoot string,
	manifest *Manifest) error {
	for _, file := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if file.Path == DatabaseEntry {
			continue
		}
		source, ok := joinWithin(packageRoot, file.Path)
		if !ok {
			return fmt.Errorf("invalid package path")
		}
		// The logical ZIP layout stores snapshots under release-snapshots/.
		// The restored instance keeps its configured (possibly custom) root.
		targetPath := file.Path
		if strings.HasPrefix(file.Path, SnapshotsPrefix) {
			targetPath = m.layout.ReleaseRel + "/" + strings.TrimPrefix(file.Path, SnapshotsPrefix)
		}
		target, ok := joinWithin(candidateRoot, targetPath)
		if !ok {
			return fmt.Errorf("invalid candidate path")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		size, _ := parseInt64(file.Size)
		if err := copyFileVerified(source, target, size, file.Sha256, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// regenerateRevisions gives every restored configuration a fresh edit
// baseline so stale edit pages cannot write against the restored data.
func regenerateRevisions(ctx context.Context, db *gorm.DB) error {
	ids := make([]uint, 0)
	rows, err := db.WithContext(ctx).Raw("SELECT id FROM resource_config").Rows()
	if err != nil {
		return err
	}
	for rows.Next() {
		var id uint
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			if err := tx.Exec("UPDATE resource_config SET edit_revision = ? WHERE id = ?",
				uuid.NewString(), id).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// CancelRestore abandons a prepared plan before any root swap. It is only
// legal while the journal has not started switching.
func (m *Manager) CancelRestore(ctx context.Context, jobID, requestID, actor string) (*Job, error) {
	if err := validateRequestID(requestID); err != nil {
		return nil, err
	}
	job, err := m.store.Get(jobID)
	if err != nil {
		return nil, err
	}
	if job.Kind != KindRestore || job.State != StateRestartRequired {
		return nil, stateConflict("restore job is not waiting for a restart")
	}
	journal, err := loadJournal(m.layout.ControlDir)
	if err != nil {
		return nil, storageError(err)
	}
	if journal != nil && journal.Stage != StagePrepared && journal.Stage != StagePreparing {
		return nil, stateConflict("restore switching has already started")
	}
	plan, err := loadPlan(m.layout.ControlDir)
	if err != nil {
		return nil, storageError(err)
	}
	if plan != nil {
		_ = os.RemoveAll(plan.CandidateRoot)
		stagingRoot := filepath.Join(m.layout.ControlDir, "staging")
		if parent := filepath.Dir(plan.CandidateRoot); isWithin(stagingRoot, parent) {
			_ = os.RemoveAll(parent)
		}
	}
	removePlan(m.layout.ControlDir)
	_ = os.Remove(journalPath(m.layout.ControlDir))
	m.maintenance.Leave()
	updated, err := m.patchJob(jobID, func(record *Job) {
		record.State = StateCancelled
		record.Phase = "cancelled"
		record.Error = nil
		if record.Result == nil {
			record.Result = &JobResult{}
		}
		record.Result.RequiresRestart = false
		record.Result.Message = "restore plan cancelled before restart"
	})
	if err != nil {
		return nil, err
	}
	m.auditWrite(actor, "restore_cancel", jobID, "succeeded", "plan cancelled before switching")
	return updated, nil
}

func parseInt64(value string) (int64, error) {
	return strconv.ParseInt(value, 10, 64)
}

// normalizeAssetPaths makes restored metadata portable between Windows and
// Unix: the stored relative path uses '/' and is updated with the copied file
// location. File bytes and identifiers are unchanged.
func normalizeAssetPaths(ctx context.Context, db *gorm.DB) error {
	type assetPath struct {
		ID   uint
		Path string
	}
	records := make([]assetPath, 0)
	rows, err := db.WithContext(ctx).Raw("SELECT id, path FROM asset").Rows()
	if err != nil {
		return err
	}
	for rows.Next() {
		var record assetPath
		if err := rows.Scan(&record.ID, &record.Path); err != nil {
			_ = rows.Close()
			return err
		}
		records = append(records, record)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, record := range records {
			normalized := normalizeStoredRelative(record.Path)
			if normalized == record.Path {
				continue
			}
			if err := tx.Exec("UPDATE asset SET path = ? WHERE id = ?", normalized, record.ID).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
