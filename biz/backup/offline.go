package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/pkg/config"
)

// OpenOffline resolves the same layout and file lock as the server. The
// returned close function must be called before the operator starts the
// service.
func OpenOffline(cfg *config.Config, producer Producer) (*Manager, func(), error) {
	layout, reasons := ResolveLayout(cfg)
	if layout == nil {
		return nil, nil, fmt.Errorf("backup is not supported: %v", reasons)
	}
	lock, err := acquireInstanceLock(layout.ControlDir)
	if err != nil {
		return nil, nil, err
	}
	closeLock := func() {
		unlockFile(lock)
		_ = lock.Close()
	}
	instanceID, err := loadOrCreateIdentity(layout.ControlDir)
	if err != nil {
		closeLock()
		return nil, nil, err
	}
	manager, err := NewManager(layout, DefaultLimits(), producer, instanceID)
	if err != nil {
		closeLock()
		return nil, nil, err
	}
	return manager, closeLock, nil
}

// RunStartupRecovery exposes the journal reconciliation to the offline tool.
func RunStartupRecovery(ctx context.Context, manager *Manager) (bool, error) {
	return recoverRestore(ctx, manager)
}

// RollbackCleanupConfirmation is required before deleting a preserved copy.
const RollbackCleanupConfirmation = "DELETE_ROLLBACK_COPY"

// CleanupRollback deletes one preserved rollback copy after an explicit local
// confirmation. It never deletes the active business root or a candidate that
// is still referenced by a pending switch.
func (m *Manager) CleanupRollback(operationID, confirmation, actor string) error {
	if confirmation != RollbackCleanupConfirmation {
		return invalidRequest("confirmation phrase does not match")
	}
	if !safeSegment(operationID) {
		return invalidRequest("operation id is invalid")
	}
	rollbackRoot := filepath.Join(m.layout.ControlDir, "rollback")
	target := filepath.Join(rollbackRoot, operationID)
	if !isWithin(rollbackRoot, target) || !dirExists(target) {
		return notFound("rollback copy not found")
	}
	journal, err := loadJournal(m.layout.ControlDir)
	if err != nil {
		return storageError(err)
	}
	if journal != nil && journal.OperationID == operationID &&
		journal.Stage != StageCommitted && journal.Stage != StageRolledBack && journal.Stage != StageFailed {
		return stateConflict("the rollback copy belongs to an incomplete switch")
	}
	if err := os.RemoveAll(target); err != nil {
		return storageError(err)
	}
	m.auditWrite(actor, "rollback_cleanup", operationID, "succeeded", "")
	return nil
}

// ControlStatus is the offline inspection payload.
type ControlStatus struct {
	Journal   *RestoreJournal `json:"journal"`
	Jobs      []*Job          `json:"jobs"`
	Rollbacks []string        `json:"rollbacks"`
}

func InspectManager(manager *Manager) (*ControlStatus, error) {
	journal, err := loadJournal(manager.layout.ControlDir)
	if err != nil {
		return nil, err
	}
	if journal == nil {
		journal = loadJournalHistory(manager.layout.ControlDir)
	}
	jobs, err := manager.store.List()
	if err != nil {
		return nil, err
	}
	if jobs == nil {
		jobs = []*Job{}
	}
	rollbacks := make([]string, 0)
	entries, err := os.ReadDir(filepath.Join(manager.layout.ControlDir, "rollback"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			rollbacks = append(rollbacks, entry.Name())
		}
	}
	return &ControlStatus{Journal: journal, Jobs: jobs, Rollbacks: rollbacks}, nil
}

// ValidateFile validates a package without touching the target instance.
func (m *Manager) ValidateFile(ctx context.Context, packagePath string) (*PackageInfo, []string, error) {
	work := filepath.Join(m.layout.ControlDir, "staging", "cli-validate-"+uuid.NewString())
	defer os.RemoveAll(work)
	return inspectPackage(ctx, packagePath, work, m.limits)
}

// StagePackage validates a package and prepares a candidate for the next
// service start. It is the offline equivalent of preview + execute.
func (m *Manager) StagePackage(ctx context.Context, actor, requestID, packagePath, reason string) (*Job, error) {
	if err := validateRequestID(requestID); err != nil {
		return nil, err
	}
	if err := validReason(reason); err != nil {
		return nil, err
	}
	if m.pendingRestorePlan() {
		return nil, restorePendingError()
	}
	if err := m.checkRollbackState(); err != nil {
		return nil, err
	}
	if existing, err := m.store.FindByRequest(KindRestore, requestID); err != nil {
		return nil, err
	} else if existing != nil && existing.State == StateRestartRequired {
		return existing, nil
	}
	if err := m.acquireWorker(); err != nil {
		return nil, err
	}
	defer m.releaseWorker()

	size, digest, err := verifyRegularFile(packagePath)
	if err != nil {
		return nil, storageError(err)
	}
	if size > m.limits.MaxPackageBytes {
		return nil, limitError("package exceeds the size limit")
	}
	validationJob := newJob(KindValidate, requestID, actor, reason,
		fingerprint(KindValidate, requestID, "offline", digest))
	validationJob.PackageSHA = digest
	validationJob.Result = &JobResult{PackageSha256: digest}
	importDir := m.importDir(validationJob.JobID)
	if err := os.MkdirAll(importDir, 0o700); err != nil {
		return nil, storageError(err)
	}
	target := filepath.Join(importDir, "package.zip")
	if err := copyFileVerified(packagePath, target, size, digest, 0o600); err != nil {
		return nil, storageError(err)
	}
	stored, err := m.store.Create(validationJob)
	if err != nil {
		return nil, err
	}
	if stored.JobID != validationJob.JobID {
		return nil, stateConflict("request_id was already used with different fields")
	}
	if err := m.runValidation(ctx, validationJob.JobID); err != nil {
		m.failJob(validationJob.JobID, "validation_failed", err)
		return nil, err
	}
	validated, err := m.store.Get(validationJob.JobID)
	if err != nil {
		return nil, err
	}
	if validated.PreviewID == nil {
		return nil, packageInvalid("package validation produced no preview")
	}
	preview, err := m.GetPreview(*validated.PreviewID)
	if err != nil {
		return nil, err
	}
	if !preview.Valid {
		return nil, packageInvalid("package is invalid: " + summarizeIssues(preview.Issues))
	}
	if preview.Backup == nil {
		return nil, packageInvalid("package summary is missing")
	}

	restoreJob := newJob(KindRestore, requestID, actor, reason,
		fingerprint(KindRestore, requestID, preview.PreviewID, preview.PackageSHA256))
	restoreJob.PreviewID = stringPointer(preview.PreviewID)
	restoreJob.PackageSHA = preview.PackageSHA256
	restoreJob.Result = &JobResult{
		PreviewID: preview.PreviewID, PackageSha256: preview.PackageSHA256, RequiresRestart: true,
	}
	storedRestore, err := m.store.Create(restoreJob)
	if err != nil {
		return nil, err
	}
	if storedRestore.JobID != restoreJob.JobID {
		return storedRestore, nil
	}
	prepareCtx, cancel := context.WithTimeout(ctx, m.limits.PrepareTimeout)
	defer cancel()
	err = m.prepareCandidate(prepareCtx, restoreJob.JobID, preview)
	var restart restartRequired
	if err == nil || errors.As(err, &restart) {
		return m.store.Get(restoreJob.JobID)
	}
	m.failJob(restoreJob.JobID, "prepare_failed", err)
	return nil, err
}

// CancelPrepared abandons a prepared plan from the offline tool.
func (m *Manager) CancelPrepared(ctx context.Context, jobID string) (*Job, error) {
	if jobID == "" {
		journal, err := loadJournal(m.layout.ControlDir)
		if err != nil {
			return nil, err
		}
		if journal == nil {
			return nil, stateConflict("no prepared restore plan exists")
		}
		jobID = journal.JobID
	}
	return m.CancelRestore(ctx, jobID, uuid.NewString(), "offline")
}

// OfflineRollback explicitly returns to the data preserved by the last
// committed restore. It rebuilds a candidate, preserves the current
// administrator, revokes sessions and regenerates edit revisions.
func (m *Manager) OfflineRollback(ctx context.Context, actor, reason string) (*Job, error) {
	journal, err := loadJournal(m.layout.ControlDir)
	if err != nil {
		return nil, err
	}
	if journal == nil {
		journal = loadJournalHistory(m.layout.ControlDir)
	}
	if journal == nil || journal.Stage != StageCommitted {
		return nil, stateConflict("no committed restore journal exists")
	}
	if !dirExists(journal.RollbackRoot) {
		return nil, stateConflict("the rollback copy is missing")
	}
	operationID := uuid.NewString()
	staging := filepath.Join(m.layout.ControlDir, "staging", "rollback-"+operationID)
	candidateRoot := filepath.Join(staging, "candidate")
	defer os.RemoveAll(staging)
	if err := m.buildCandidateFromRoot(ctx, journal.RollbackRoot, candidateRoot); err != nil {
		return nil, err
	}

	job := newJob(KindRestore, uuid.NewString(), actor, reason,
		fingerprint(KindRestore, operationID, "offline-rollback"))
	job.State = StateRunning
	job.Phase = "installing_rollback"
	if _, err := m.store.Create(job); err != nil {
		return nil, err
	}
	plan := &RestorePlan{
		OperationID:   operationID,
		JobID:         job.JobID,
		BackupID:      "",
		InstanceID:    m.instanceID,
		BusinessRoot:  m.layout.BusinessRoot,
		CandidateRoot: candidateRoot,
		RollbackRoot:  filepath.Join(m.layout.ControlDir, "rollback", operationID, "data"),
		DatabaseRel:   m.layout.DatabaseRel,
		CreatedAt:     m.now().Format(time.RFC3339),
	}
	if err := writeMarker(candidateRoot, &restoreMarker{
		OperationID: operationID, JobID: job.JobID, CreatedAt: plan.CreatedAt,
	}); err != nil {
		return nil, storageError(err)
	}
	rsJournal := &RestoreJournal{
		OperationID: operationID, JobID: job.JobID, Stage: StagePrepared,
		BusinessRoot: plan.BusinessRoot, CandidateRoot: plan.CandidateRoot,
		RollbackRoot: plan.RollbackRoot, DatabaseRel: plan.DatabaseRel,
	}
	if err := m.audit.Write(AuditEvent{
		Actor: actor, Action: "restore_rollback", Object: operationID,
		Result: "starting", Detail: reason,
	}); err != nil {
		return nil, storageError(err)
	}
	if err := savePlan(m.layout.ControlDir, plan); err != nil {
		return nil, storageError(err)
	}
	if err := saveJournal(m.layout.ControlDir, rsJournal); err != nil {
		return nil, storageError(err)
	}
	if _, err := switchRestore(ctx, m, plan, rsJournal); err != nil {
		return nil, err
	}
	return m.store.Get(job.JobID)
}

func (m *Manager) buildCandidateFromRoot(ctx context.Context, sourceRoot, candidateRoot string) error {
	sourceDatabase, ok := joinWithin(sourceRoot, m.layout.DatabaseRel)
	if !ok {
		return internalError(errors.New("rollback database path is invalid"))
	}
	if _, err := os.Stat(sourceDatabase); err != nil {
		return stateConflict("rollback database is missing")
	}
	source, err := openSQLite(sourceDatabase)
	if err != nil {
		return storageError(err)
	}
	defer closeGorm(source)

	candidateDatabase, ok := joinWithin(candidateRoot, m.layout.DatabaseRel)
	if !ok {
		return internalError(errors.New("candidate database path is invalid"))
	}
	if err := os.MkdirAll(filepath.Dir(candidateDatabase), 0o700); err != nil {
		return storageError(err)
	}
	candidate, err := openBusinessDB(candidateDatabase)
	if err != nil {
		return storageError(err)
	}
	if err := copyBusinessTables(ctx, source, candidate); err != nil {
		_ = closeGorm(candidate)
		return storageError(err)
	}
	if err := regenerateRevisions(ctx, candidate); err != nil {
		_ = closeGorm(candidate)
		return storageError(err)
	}
	if err := normalizeAssetPaths(ctx, candidate); err != nil {
		_ = closeGorm(candidate)
		return storageError(err)
	}
	if err := checkIntegrity(ctx, candidate); err != nil {
		_ = closeGorm(candidate)
		return storageError(err)
	}
	sourceLayout := &Layout{
		BusinessRoot: sourceRoot,
		ControlDir:   m.layout.ControlDir,
		DatabaseRel:  m.layout.DatabaseRel,
		UploadsDir:   filepath.Join(sourceRoot, "uploads"),
		ReleaseDir:   filepath.Join(sourceRoot, filepath.FromSlash(m.layout.ReleaseRel)),
		ReleaseRel:   m.layout.ReleaseRel,
	}
	plans, _, err := collectFilePlans(ctx, candidate, sourceLayout, m.limits)
	if err != nil {
		_ = closeGorm(candidate)
		return err
	}
	if err := m.copyPlansToRoot(plans, candidateRoot); err != nil {
		_ = closeGorm(candidate)
		return err
	}
	if err := closeGorm(candidate); err != nil {
		return storageError(err)
	}
	issues := validateRootBusiness(ctx, candidateRoot, m.layout.DatabaseRel,
		m.layout.ReleaseRel, m.limits, false)
	if len(issues) > 0 {
		return packageInvalid("rollback candidate failed validation: " + summarizeIssues(issues))
	}
	return nil
}

func (m *Manager) copyPlansToRoot(plans []filePlan, root string) error {
	for _, plan := range plans {
		targetPath := plan.ArchivePath
		if len(targetPath) >= len(SnapshotsPrefix) && targetPath[:len(SnapshotsPrefix)] == SnapshotsPrefix {
			targetPath = m.layout.ReleaseRel + "/" + targetPath[len(SnapshotsPrefix):]
		}
		target, ok := joinWithin(root, targetPath)
		if !ok {
			return internalError(errors.New("rollback file path is invalid"))
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return storageError(err)
		}
		if err := copyFileVerified(plan.SourcePath, target, plan.Size, plan.Sha256, 0o600); err != nil {
			return storageError(err)
		}
	}
	return nil
}

func summarizeIssues(issues []string) string {
	if len(issues) > 5 {
		issues = issues[:5]
	}
	payload, _ := json.Marshal(issues)
	return string(payload)
}
