package backup

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/yi-nology/rainbow_bridge/pkg/config"
)

// Instance keeps the process-wide file lock for the life of the application.
// It must be acquired before any database open or migration.
type Instance struct {
	Manager  *Manager
	Layout   *Layout
	Switched bool
	lock     *os.File
}

// Boot resolves and locks the supported deployment, then reconciles a pending
// restore before the caller opens the business database.
func Boot(cfg *config.Config, producer Producer) (*Instance, error) {
	if cfg == nil || !cfg.Backup.Enabled {
		return nil, nil
	}
	layout, reasons := ResolveLayout(cfg)
	if len(reasons) > 0 {
		log.Printf("[backup] feature unavailable: %v", reasons)
		return nil, nil
	}
	lock, err := acquireInstanceLock(layout.ControlDir)
	if err != nil {
		return nil, err
	}
	instance := &Instance{Layout: layout, lock: lock}
	cleanup := func() {
		instance.Close()
	}
	instanceID, err := loadOrCreateIdentity(layout.ControlDir)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("backup identity: %w", err)
	}
	manager, err := NewManager(layout, DefaultLimits(), producer, instanceID)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("backup manager: %w", err)
	}
	instance.Manager = manager
	if err := manager.SetRollbackKeepCount(cfg.Backup.RollbackKeepCount); err != nil {
		cleanup()
		return nil, err
	}
	switched, err := recoverRestore(context.Background(), manager)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("restore recovery: %w", err)
	}
	instance.Switched = switched
	manager.CleanupAbandonedWork()
	manager.ExpireImports()
	return instance, nil
}

// CompleteStartup archives the committed journal after the normal
// initialization succeeded. A permanently active "committed" journal would
// otherwise re-validate the schema on every later boot and reject tables added
// by a future version.
func (i *Instance) CompleteStartup() {
	if i == nil || i.Layout == nil || i.Manager == nil {
		return
	}
	control := i.Layout.ControlDir
	journal, err := loadJournal(control)
	if err != nil {
		log.Printf("[backup] committed restore journal is unreadable: %v", err)
		return
	}
	if journal != nil && journal.Stage == StageCommitted {
		if err := i.recordRetentionProof(journal); err != nil {
			log.Printf("[backup] failed to persist rollback retention proof: %v", err)
			return
		}
		_ = i.Manager.markJobSucceeded(journal.JobID, "restore completed")
		removePlan(control)
		target := filepath.Join(control, journalHistoryFileName)
		_ = os.Remove(target)
		if err := os.Rename(journalPath(control), target); err != nil {
			log.Printf("[backup] failed to archive committed restore journal: %v", err)
		}
	}
	i.Manager.CleanupSuccessfulRollbacks()
}

// recordRetentionProof is written only after the normal database open and
// identity/numbering migrations completed. It is idempotent and must succeed
// before the journal is archived.
func (i *Instance) recordRetentionProof(journal *RestoreJournal) error {
	if info, err := os.Stat(rollbackOperationDir(i.Layout.ControlDir, journal.OperationID)); err != nil || !info.IsDir() {
		return errors.New("rollback copy for the committed restore is missing")
	}
	existing, err := loadRollbackRetention(i.Layout.ControlDir, journal.OperationID)
	if err != nil {
		return err
	}
	if existing != nil {
		if existing.JobID != journal.JobID {
			return errors.New("retention proof belongs to a different restore job")
		}
		return nil
	}
	return writeRollbackRetention(i.Layout.ControlDir, journal.OperationID, journal.JobID,
		time.Now().UTC().Format(time.RFC3339), rollbackRetentionRetained)
}

// Close releases the instance lock (the OS also releases it on exit).
func (i *Instance) Close() {
	if i == nil || i.lock == nil {
		return
	}
	unlockFile(i.lock)
	_ = i.lock.Close()
	i.lock = nil
}

// AbortInitialization restores the previous business root when normal startup
// fails after this boot installed a prepared candidate but before HTTP opened.
func (i *Instance) AbortInitialization() error {
	if i == nil || i.Manager == nil || !i.Switched {
		return nil
	}
	journal, err := loadJournal(i.Layout.ControlDir)
	if err != nil {
		return err
	}
	if journal == nil || journal.Stage != StageCommitted {
		return nil
	}
	planned := &RestorePlan{
		OperationID:   journal.OperationID,
		JobID:         journal.JobID,
		BusinessRoot:  journal.BusinessRoot,
		CandidateRoot: journal.CandidateRoot,
		RollbackRoot:  journal.RollbackRoot,
		DatabaseRel:   journal.DatabaseRel,
		PackageSHA256: journal.PackageSHA256,
	}
	if err := restoreOldRoot(planned); err != nil {
		journal.Stage = StageRollbackRequired
		journal.Detail = "initialization failed and rollback failed"
		_ = saveJournal(i.Layout.ControlDir, journal)
		return err
	}
	journal.Stage = StageRolledBack
	journal.Detail = "initialization failed after install"
	if err := saveJournal(i.Layout.ControlDir, journal); err != nil {
		return err
	}
	i.Manager.failRestoreJob(journal.JobID, "service initialization failed; previous data restored")
	i.Manager.auditWrite("system", "restore_switch", journal.OperationID, "rolled_back", "initialization failed")
	removePlan(i.Layout.ControlDir)
	return nil
}

func acquireInstanceLock(controlDir string) (*os.File, error) {
	path := filepath.Join(controlDir, "instance.lock")
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(handle); err != nil {
		_ = handle.Close()
		return nil, fmt.Errorf("another instance is already running: %w", err)
	}
	_ = handle.Truncate(0)
	_, _ = handle.Seek(0, 0)
	_, _ = handle.WriteString(fmt.Sprintf("pid=%d started=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339)))
	_ = handle.Sync()
	return handle, nil
}
