package backup

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	appconfig "github.com/yi-nology/rainbow_bridge/pkg/config"
	"gorm.io/gorm"
)

// Manager owns backup control state, the single background worker and the
// process-local maintenance gate.
type Manager struct {
	layout       *Layout
	limits       Limits
	producer     Producer
	instanceID   string
	store        *JobStore
	audit        *auditLog
	maintenance  *Maintenance
	now          func() time.Time
	rollbackKeep int

	mu         sync.Mutex
	source     *gorm.DB
	workerBusy bool
	workerDone chan struct{}
	uploading  map[string]bool
	downloads  map[string]int
}

func NewManager(layout *Layout, limits Limits, producer Producer, instanceID string) (*Manager, error) {
	manager := &Manager{
		layout:       layout,
		limits:       limits,
		producer:     producer,
		instanceID:   instanceID,
		store:        NewJobStore(layout.ControlDir, limits.MaxTerminalJobs),
		audit:        newAuditLog(layout.ControlDir),
		maintenance:  NewMaintenance(),
		now:          func() time.Time { return time.Now().UTC() },
		rollbackKeep: appconfig.DefaultBackupRollbackKeepCount,
		uploading:    make(map[string]bool),
		downloads:    make(map[string]int),
	}
	for _, directory := range manager.controlDirectories() {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, err
		}
	}
	interrupted, err := manager.store.RecoverInterrupted()
	if err != nil {
		return nil, err
	}
	manager.cleanupInterruptedWork(interrupted)
	return manager, nil
}

// cleanupInterruptedWork removes private artifacts of tasks that died with the
// previous process. A restore job referenced by a prepared plan is preserved
// for the startup switch.
func (m *Manager) cleanupInterruptedWork(jobIDs []string) {
	plan, _ := loadPlan(m.layout.ControlDir)
	for _, jobID := range jobIDs {
		job, err := m.store.Get(jobID)
		if err != nil {
			continue
		}
		if job.Kind == KindRestore && plan != nil && plan.JobID == jobID {
			continue
		}
		_ = os.RemoveAll(m.stagingDir(jobID))
		_ = os.RemoveAll(m.importDir(jobID))
		if job.Kind == KindCreate && job.Result != nil && job.Result.BackupID != "" {
			_ = os.Remove(filepath.Join(m.layout.ControlDir, "backups", job.Result.BackupID+".zip.part"))
		}
	}
}

func (m *Manager) controlDirectories() []string {
	control := m.layout.ControlDir
	return []string{
		filepath.Join(control, "jobs"),
		filepath.Join(control, "backups"),
		filepath.Join(control, "imports"),
		filepath.Join(control, "staging"),
		filepath.Join(control, "rollback"),
	}
}

// AttachDatabase supplies the live business database used for VACUUM INTO.
func (m *Manager) AttachDatabase(db *gorm.DB) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.source = db
}

func (m *Manager) sourceDB() (*gorm.DB, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.source == nil {
		return nil, internalError(errors.New("backup database is not attached"))
	}
	return m.source, nil
}

func (m *Manager) Limits() Limits            { return m.limits }
func (m *Manager) InstanceID() string        { return m.instanceID }
func (m *Manager) Maintenance() *Maintenance { return m.maintenance }

// SetRollbackKeepCount applies the configured successful-copy retention.
func (m *Manager) SetRollbackKeepCount(value int) error {
	keep, err := appconfig.NormalizeBackupRollbackKeepCount(value)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rollbackKeep = keep
	return nil
}

// RollbackKeepCount returns the effective successful-copy retention.
func (m *Manager) RollbackKeepCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rollbackKeep <= 0 {
		return appconfig.DefaultBackupRollbackKeepCount
	}
	return m.rollbackKeep
}

func (m *Manager) acquireWorker() error {
	// A worker marks its job terminal just before returning; wait briefly for
	// that last step so a client polling the status does not see a spurious
	// 409 immediately after success. Longer overlapping work is still
	// rejected instead of queued indefinitely.
	deadline := time.Now().Add(2 * time.Second)
	for {
		m.mu.Lock()
		if !m.workerBusy {
			m.workerBusy = true
			m.workerDone = make(chan struct{})
			m.mu.Unlock()
			return nil
		}
		done := m.workerDone
		m.mu.Unlock()
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return busyError("another backup task is already running")
		}
		timer := time.NewTimer(remaining)
		select {
		case <-done:
			timer.Stop()
		case <-timer.C:
			return busyError("another backup task is already running")
		}
	}
}

func (m *Manager) releaseWorker() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.workerBusy {
		return
	}
	if m.workerDone != nil {
		close(m.workerDone)
		m.workerDone = nil
	}
	m.workerBusy = false
}

func (m *Manager) patchJob(jobID string, apply func(*Job)) (*Job, error) {
	job, err := m.store.Get(jobID)
	if err != nil {
		return nil, err
	}
	apply(job)
	if err := m.store.Save(job); err != nil {
		return nil, err
	}
	return job, nil
}

func (m *Manager) setPhase(jobID, phase string) (*Job, error) {
	return m.patchJob(jobID, func(job *Job) {
		job.State = StateRunning
		job.Phase = phase
		job.Error = nil
	})
}

func (m *Manager) failJob(jobID, phase string, err error) {
	classified, ok := AsError(err)
	if !ok {
		classified = internalError(err)
	}
	_, _ = m.patchJob(jobID, func(job *Job) {
		job.State = StateFailed
		job.Phase = phase
		job.Error = &JobError{Type: classified.Type, Message: classified.Message}
	})
}

// runJob executes a worker function with a deadline and panic containment.
func (m *Manager) runJob(job *Job, timeout time.Duration, work func(context.Context, string) error) {
	defer m.releaseWorker()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := m.executeJob(ctx, job, work); err != nil {
		var restart restartRequired
		if errors.As(err, &restart) {
			return
		}
		// Full diagnostics stay in the local log, correlated by job id; the
		// client only receives the classified message.
		log.Printf("[backup] job %s failed: %v", job.JobID, err)
		m.failJob(job.JobID, "failed", err)
	}
}

func (m *Manager) executeJob(ctx context.Context, job *Job, work func(context.Context, string) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = internalError(errors.New("backup worker panic"))
		}
	}()
	return work(ctx, job.JobID)
}

// restartRequired is a sentinel: preparation succeeded and the process must be
// restarted before the candidate can be installed.
type restartRequired struct{}

func (restartRequired) Error() string { return "restart required" }

// JobStatus returns a task record.
func (m *Manager) JobStatus(jobID string) (*Job, error) {
	if strings.TrimSpace(jobID) == "" {
		return nil, invalidRequest("job_id is required")
	}
	return m.store.Get(jobID)
}

// pendingRestorePlan reports whether another restore is already waiting for a
// restart. A second prepare would overwrite the plan and journal while the
// first candidate is still pending.
func (m *Manager) pendingRestorePlan() bool {
	journal, err := loadJournal(m.layout.ControlDir)
	if err == nil && journal != nil {
		switch journal.Stage {
		case StagePrepared, StageSwitching, StageOldMoved, StageNewInstalled:
			return true
		}
	}
	jobs, err := m.store.List()
	if err != nil {
		return false
	}
	for _, job := range jobs {
		if job.Kind == KindRestore && job.State == StateRestartRequired {
			return true
		}
	}
	return false
}

// CleanupAbandonedWork removes staging directories that no restart plan
// references. It is called after journal recovery during boot.
func (m *Manager) CleanupAbandonedWork() {
	keep := m.restartCandidatePath()
	entries, err := os.ReadDir(filepath.Join(m.layout.ControlDir, "staging"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(m.layout.ControlDir, "staging", entry.Name())
		if keep != "" && samePath(path, keep) {
			continue
		}
		_ = os.RemoveAll(path)
	}
}

func (m *Manager) restartCandidatePath() string {
	plan, err := loadPlan(m.layout.ControlDir)
	if err != nil || plan == nil {
		return ""
	}
	return plan.CandidateRoot
}

func (m *Manager) backupPath(backupID string) (string, bool) {
	if _, ok := safeRelativePath(backupID); !ok {
		return "", false
	}
	return filepath.Join(m.layout.ControlDir, "backups", backupID+".zip"), true
}

func (m *Manager) importDir(jobID string) string {
	return filepath.Join(m.layout.ControlDir, "imports", jobID)
}

func (m *Manager) stagingDir(jobID string) string {
	return filepath.Join(m.layout.ControlDir, "staging", jobID)
}

func (m *Manager) backupJob(backupID string) (*Job, error) {
	jobs, err := m.store.List()
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		if job.Kind == KindCreate && job.Result != nil && job.Result.BackupID == backupID {
			return job, nil
		}
	}
	return nil, notFound("backup not found")
}

// markJobSucceeded is also used by the startup switch after a restart.
func (m *Manager) markJobSucceeded(jobID, message string) error {
	_, err := m.patchJob(jobID, func(job *Job) {
		job.State = StateSucceeded
		job.Phase = "completed"
		job.Error = nil
		if job.Result == nil {
			job.Result = &JobResult{}
		}
		job.Result.RequiresRestart = false
		job.Result.Message = message
	})
	return err
}

func (m *Manager) writePreview(preview *Preview) error {
	payload, err := jsonMarshalIndent(preview)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(m.importDir(preview.JobID), "preview.json"), payload, 0o600)
}
