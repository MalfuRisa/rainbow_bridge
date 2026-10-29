package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	configdb "github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"gorm.io/gorm"
)

// CreateBackup validates, persists and launches an asynchronous backup task.
func (m *Manager) CreateBackup(ctx context.Context, actor, reason, requestID string) (*Job, error) {
	if err := validateRequestID(requestID); err != nil {
		return nil, err
	}
	if err := validReason(reason); err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	key := fingerprint(KindCreate, requestID, reason)
	if existing, err := m.store.FindByRequest(KindCreate, requestID); err != nil {
		return nil, err
	} else if existing != nil {
		if existing.Fingerprint != key {
			return nil, &Error{HTTPStatus: 409, Type: ErrRequestConflict,
				Message: "request_id was already used with different fields"}
		}
		return existing, nil
	}
	if m.maintenance.Active() {
		return nil, stateConflict("backup maintenance is active; retry after it is released")
	}
	if err := m.acquireWorker(); err != nil {
		return nil, err
	}
	job := newJob(KindCreate, requestID, actor, reason, key)
	backupID := uuid.NewString()
	job.Result = &JobResult{BackupID: backupID}
	job.BackupID = stringPointer(backupID)
	stored, err := m.store.Create(job)
	if err != nil {
		m.releaseWorker()
		return nil, err
	}
	if stored.JobID != job.JobID {
		m.releaseWorker()
		return stored, nil
	}
	if err := m.checkRetention(); err != nil {
		m.failJob(job.JobID, "rejected", err)
		m.releaseWorker()
		return nil, err
	}
	if err := m.checkSpaceForBackup(ctx); err != nil {
		m.failJob(job.JobID, "rejected", err)
		m.releaseWorker()
		return nil, err
	}
	go m.runJob(job, m.limits.CreateTimeout, m.runCreate)
	return stored, nil
}

func (m *Manager) runCreate(ctx context.Context, jobID string) error {
	job, err := m.setPhase(jobID, "draining_writes")
	if err != nil {
		return err
	}
	backupID := ""
	if job.Result != nil {
		backupID = job.Result.BackupID
	}
	maintenanceCtx, cancel := context.WithTimeout(ctx, m.limits.MaintenanceWait)
	defer cancel()
	if err := m.maintenance.Enter(maintenanceCtx); err != nil {
		m.auditWrite(job.Actor, "backup_create", job.JobID, "failed", err.Error())
		return err
	}
	held := true
	defer func() {
		if held {
			m.maintenance.Leave()
		}
	}()

	staging := m.stagingDir(jobID)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return storageError(err)
	}
	defer os.RemoveAll(staging)

	consistentAt := m.now().Format(time.RFC3339)
	if _, err := m.setPhase(jobID, "snapshotting_database"); err != nil {
		return err
	}
	source, err := m.sourceDB()
	if err != nil {
		return err
	}
	snapshotPath := filepath.Join(staging, "consistent.sqlite")
	if err := vacuumInto(ctx, source, snapshotPath); err != nil {
		m.auditWrite(job.Actor, "backup_create", job.JobID, "failed", "database snapshot failed")
		return storageError(err)
	}
	snapshot, err := openSQLite(snapshotPath)
	if err != nil {
		return storageError(err)
	}
	defer closeGorm(snapshot)
	if snapshot.Migrator().HasColumn("config_release_version", "storage_backend") {
		var remote int64
		if err := snapshot.WithContext(ctx).Table("config_release_version").Where("storage_backend NOT IN ?", []string{"", "local"}).Count(&remote).Error; err != nil {
			return storageError(err)
		}
		if remote != 0 {
			return &Error{HTTPStatus: 503, Type: ErrUnsupported, Message: "complete backup does not support remote snapshot history"}
		}
	}

	cleanPath := filepath.Join(staging, "business.sqlite")
	clean, err := openBusinessDB(cleanPath)
	if err != nil {
		return storageError(err)
	}
	defer closeGorm(clean)
	if err := copyBusinessTables(ctx, snapshot, clean); err != nil {
		return storageError(err)
	}
	// The private copy carries the schema-3 numbering invariant even if the
	// live database predates the startup backfill.
	if err := configdb.EnsureReleaseNumbering(ctx, clean); err != nil {
		return storageError(err)
	}
	if err := checkIntegrity(ctx, clean); err != nil {
		return storageError(err)
	}

	if _, err := m.setPhase(jobID, "copying_resources"); err != nil {
		return err
	}
	plans, warnings, err := collectFilePlans(ctx, clean, m.layout, m.limits)
	if err != nil {
		if classified, ok := AsError(err); ok {
			return classified
		}
		return storageError(err)
	}
	if err := copyPlansToStaging(ctx, plans, staging); err != nil {
		return storageError(err)
	}
	counts, err := collectCounts(ctx, clean)
	if err != nil {
		return storageError(err)
	}

	// From here on only the private copies are read; online writes resume.
	m.maintenance.Leave()
	held = false
	if _, err := m.setPhase(jobID, "packaging"); err != nil {
		return err
	}

	dbSize, dbSha, err := verifyRegularFile(cleanPath)
	if err != nil {
		return storageError(err)
	}
	entries := make([]FileEntry, 0, len(plans)+1)
	entries = append(entries, FileEntry{Path: DatabaseEntry, Size: decimalString(dbSize), Sha256: dbSha})
	entries = append(entries, buildFileEntries(plans)...)
	if backupID == "" {
		backupID = uuid.NewString()
	}
	manifest := newManifest(backupID, consistentAt, m.producer, counts, entries, warnings)
	finalPath := filepath.Join(m.layout.ControlDir, "backups", backupID+".zip")
	packageSize, packageSha, err := writePackage(ctx, finalPath, cleanPath, manifest, plans, m.limits)
	if err != nil {
		if classified, ok := AsError(err); ok {
			return classified
		}
		return storageError(err)
	}

	if _, err := m.patchJob(jobID, func(record *Job) {
		record.State = StateSucceeded
		record.Phase = "completed"
		record.Error = nil
		record.Result = &JobResult{
			BackupID:     backupID,
			Sha256:       packageSha,
			Size:         packageSize,
			ConsistentAt: consistentAt,
			Counts:       &counts,
		}
		record.BackupID = stringPointer(backupID)
	}); err != nil {
		_ = os.Remove(finalPath)
		return err
	}
	m.auditWrite(job.Actor, "backup_create", backupID, "succeeded", job.Reason)
	return nil
}

func vacuumInto(ctx context.Context, source *gorm.DB, target string) error {
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return source.WithContext(ctx).Exec("VACUUM INTO ?", target).Error
}

func openSQLite(path string) (*gorm.DB, error) {
	return openSQLiteConfig(path)
}

func (m *Manager) checkRetention() error {
	jobs, err := m.store.List()
	if err != nil {
		return err
	}
	count := 0
	for _, job := range jobs {
		if job.Kind != KindCreate || job.State != StateSucceeded || job.Result == nil {
			continue
		}
		path, ok := m.backupPath(job.Result.BackupID)
		if !ok {
			continue
		}
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			count++
		}
	}
	if count >= m.limits.MaxBackups {
		return limitError("backup retention limit reached; download and delete an old backup first")
	}
	return nil
}

func (m *Manager) checkSpaceForBackup(ctx context.Context) error {
	source, err := m.sourceDB()
	if err != nil {
		return err
	}
	var uploadBytes, snapshotBytes int64
	if err := source.WithContext(ctx).Raw(
		"SELECT COALESCE(SUM(CASE WHEN file_size > 0 THEN file_size ELSE 0 END), 0) FROM asset").
		Row().Scan(&uploadBytes); err != nil {
		return storageError(err)
	}
	if err := source.WithContext(ctx).Raw(
		"SELECT COALESCE(SUM(size), 0) FROM config_release_asset").Row().Scan(&snapshotBytes); err != nil {
		return storageError(err)
	}
	databaseBytes := fileSize(m.layout.DatabasePath)
	required := databaseBytes*2 + uploadBytes + snapshotBytes
	return m.ensureFreeSpace(required)
}

func (m *Manager) ensureFreeSpace(required int64) error {
	free, err := diskFree(m.layout.BusinessRoot)
	if err != nil {
		// Measurement failure is not proof of insufficient space; the real
		// write path still reports ENOSPC correctly.
		return nil
	}
	if free < required+m.limits.SpaceReserveBytes {
		return spaceError(fmt.Sprintf("insufficient free disk space for this operation"))
	}
	return nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func stringPointer(value string) *string { return &value }

func (m *Manager) auditWrite(actor, action, object, result, detail string) {
	_ = m.audit.Write(AuditEvent{
		Actor:  actor,
		Action: action,
		Object: object,
		Result: result,
		Detail: sanitizeAuditDetail(detail),
	})
}

func sanitizeAuditDetail(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) > 300 {
		return trimmed[:300]
	}
	return trimmed
}
