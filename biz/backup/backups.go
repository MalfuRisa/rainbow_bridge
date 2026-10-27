package backup

import (
	"os"
	"sort"
	"strings"
)

// BackupItem is the downloadable-backup summary returned by GET /backup/list.
type BackupItem struct {
	BackupID     string  `json:"backup_id"`
	CreatedAt    string  `json:"created_at"`
	ConsistentAt string  `json:"consistent_at"`
	Sha256       string  `json:"sha256"`
	Size         int64   `json:"size"`
	Counts       *Counts `json:"counts,omitempty"`
	Actor        string  `json:"actor"`
	Reason       string  `json:"reason"`
	CursorID     string  `json:"-"`
}

// BackupPage is the list response payload.
type BackupPage struct {
	Items        []BackupItem `json:"items"`
	HasMore      bool         `json:"has_more"`
	NextBeforeID string       `json:"next_before_id,omitempty"`
}

func (m *Manager) ListBackups(limit int, beforeID string) (*BackupPage, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	jobs, err := m.store.List()
	if err != nil {
		return nil, err
	}
	items := make([]BackupItem, 0)
	for _, job := range jobs {
		if job.Kind != KindCreate || job.State != StateSucceeded || job.Result == nil {
			continue
		}
		path, ok := m.backupPath(job.Result.BackupID)
		if !ok {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		items = append(items, BackupItem{
			BackupID:     job.Result.BackupID,
			CreatedAt:    job.CreatedAt,
			ConsistentAt: consistentAtFromJob(job),
			Sha256:       job.Result.Sha256,
			Size:         job.Result.Size,
			Counts:       job.Result.Counts,
			Actor:        job.Actor,
			Reason:       job.Reason,
			CursorID:     job.JobID,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt > items[j].CreatedAt })

	start := 0
	if strings.TrimSpace(beforeID) != "" {
		found := false
		for index, item := range items {
			if item.CursorID == beforeID || item.BackupID == beforeID {
				start = index + 1
				found = true
				break
			}
		}
		if !found {
			return nil, invalidRequest("before_id does not identify an earlier backup")
		}
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := &BackupPage{Items: items[start:end], HasMore: end < len(items)}
	if page.HasMore && len(page.Items) > 0 {
		page.NextBeforeID = page.Items[len(page.Items)-1].CursorID
	}
	if page.Items == nil {
		page.Items = []BackupItem{}
	}
	return page, nil
}

func consistentAtFromJob(job *Job) string {
	if job.Result != nil && job.Result.ConsistentAt != "" {
		return job.Result.ConsistentAt
	}
	return job.CreatedAt
}

// Download is an in-progress download handle.
type Download struct {
	Path     string
	FileName string
	Sha256   string
	Size     int64
	release  func()
}

// OpenDownload locates a completed backup and accounts for the concurrent
// download ceiling. The caller must call Close when streaming finishes.
func (m *Manager) OpenDownload(backupID string) (*Download, error) {
	if strings.TrimSpace(backupID) == "" {
		return nil, invalidRequest("backup_id is required")
	}
	job, err := m.backupJob(backupID)
	if err != nil {
		return nil, err
	}
	if job.State != StateSucceeded || job.Result == nil {
		return nil, stateConflict("backup is not completed")
	}
	path, ok := m.backupPath(backupID)
	if !ok {
		return nil, invalidRequest("backup_id is invalid")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, notFound("backup file not found")
	}
	m.mu.Lock()
	if m.downloads[backupID] >= m.limits.MaxConcurrentDownloads {
		m.mu.Unlock()
		return nil, busyError("too many concurrent downloads")
	}
	m.downloads[backupID]++
	m.mu.Unlock()

	released := false
	release := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if released {
			return
		}
		released = true
		m.downloads[backupID]--
		if m.downloads[backupID] <= 0 {
			delete(m.downloads, backupID)
		}
	}
	return &Download{
		Path:     path,
		FileName: "rainbow-business-backup-" + backupID + ".zip",
		Sha256:   job.Result.Sha256,
		Size:     info.Size(),
		release:  release,
	}, nil
}

func (d *Download) Close() {
	if d != nil && d.release != nil {
		d.release()
	}
}

// DeleteBackup removes a completed package after the caller confirms its
// digest. A backup referenced by a pending restore or an active download is
// never removed.
func (m *Manager) DeleteBackup(backupID, expectedSha, requestID, actor string) error {
	if err := validateRequestID(requestID); err != nil {
		return err
	}
	job, err := m.backupJob(backupID)
	if err != nil {
		return err
	}
	if job.Result == nil || !strings.EqualFold(job.Result.Sha256, expectedSha) {
		return targetMismatch("expected_sha256 does not match the backup")
	}
	m.mu.Lock()
	active := m.downloads[backupID] > 0
	m.mu.Unlock()
	if active {
		return inUseError("backup is currently being downloaded")
	}
	if m.backupReferencedByRestore(backupID) {
		return inUseError("backup is referenced by a restore job")
	}
	path, ok := m.backupPath(backupID)
	if !ok {
		return invalidRequest("backup_id is invalid")
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return storageError(err)
	}
	m.auditWrite(actor, "backup_delete", backupID, "succeeded", "")
	return nil
}

func (m *Manager) backupReferencedByRestore(backupID string) bool {
	jobs, err := m.store.List()
	if err != nil {
		return true
	}
	for _, job := range jobs {
		if job.Kind != KindRestore || job.Result == nil {
			continue
		}
		if job.State == StateQueued || job.State == StateRunning || job.State == StateRestartRequired {
			if job.Result.BackupID == backupID || job.BackupID != nil && *job.BackupID == backupID {
				return true
			}
		}
	}
	return false
}
