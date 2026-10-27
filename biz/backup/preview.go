package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const previewLifetime = 24 * time.Hour

// BackupSummary is the safe package header shown during confirmation.
type BackupSummary struct {
	BackupID      string   `json:"backup_id"`
	CreatedAt     string   `json:"created_at"`
	ConsistentAt  string   `json:"consistent_at"`
	Producer      Producer `json:"producer"`
	SchemaVersion int      `json:"schema_version"`
	Counts        Counts   `json:"counts"`
	Files         int      `json:"files"`
}

// Preview is persisted under the import job directory and returned to the
// management client. Server paths are never serialized.
type Preview struct {
	PreviewID        string         `json:"preview_id"`
	JobID            string         `json:"job_id"`
	Valid            bool           `json:"valid"`
	PackageSHA256    string         `json:"package_sha256"`
	Backup           *BackupSummary `json:"backup,omitempty"`
	Warnings         []string       `json:"warnings"`
	Issues           []string       `json:"issues"`
	ExpiresAt        string         `json:"expires_at"`
	TargetInstanceID string         `json:"target_instance_id"`
	CreatedAt        string         `json:"created_at"`
}

// BeginUpload reserves a streaming upload target. An already completed
// request is returned immediately so clients can retry safely.
func (m *Manager) BeginUpload(requestID string) (existing *Job, path string, cleanup func(), err error) {
	if err := validateRequestID(requestID); err != nil {
		return nil, "", nil, err
	}
	if existing, err := m.store.FindByRequest(KindValidate, requestID); err != nil {
		return nil, "", nil, err
	} else if existing != nil {
		return existing, "", func() {}, nil
	}
	m.mu.Lock()
	if m.uploading[requestID] {
		m.mu.Unlock()
		return nil, "", nil, busyError("the same upload is already in progress")
	}
	m.uploading[requestID] = true
	m.mu.Unlock()

	directory := filepath.Join(m.layout.ControlDir, "staging", "upload-"+uuid.NewString())
	if err := os.MkdirAll(directory, 0o700); err != nil {
		m.releaseUpload(requestID)
		return nil, "", nil, storageError(err)
	}
	target := filepath.Join(directory, "package.part")
	cleanup = func() {
		m.releaseUpload(requestID)
		_ = os.RemoveAll(directory)
	}
	return nil, target, cleanup, nil
}

func (m *Manager) releaseUpload(requestID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.uploading, requestID)
}

// FinishUpload turns a fully received package into a validation job.
func (m *Manager) FinishUpload(ctx context.Context, actor, requestID, tempPath string,
	size int64, digest string) (*Job, error) {
	defer m.releaseUpload(requestID)
	if size <= 0 {
		_ = os.RemoveAll(filepath.Dir(tempPath))
		return nil, packageInvalid("uploaded package is empty")
	}
	if size > m.limits.MaxPackageBytes {
		_ = os.RemoveAll(filepath.Dir(tempPath))
		return nil, limitError("uploaded package exceeds the size limit")
	}
	if existing, err := m.store.FindByRequest(KindValidate, requestID); err != nil {
		return nil, err
	} else if existing != nil {
		_ = os.RemoveAll(filepath.Dir(tempPath))
		if !strings.EqualFold(existing.PackageSHA, digest) {
			return nil, &Error{HTTPStatus: 409, Type: ErrRequestConflict,
				Message: "request_id was already used with a different package"}
		}
		return existing, nil
	}
	if err := m.acquireWorker(); err != nil {
		_ = os.RemoveAll(filepath.Dir(tempPath))
		return nil, err
	}
	key := fingerprint(KindValidate, requestID, strings.ToLower(digest))
	job := newJob(KindValidate, requestID, actor, "", key)
	job.PackageSHA = strings.ToLower(digest)
	job.Result = &JobResult{PackageSha256: job.PackageSHA}
	directory := m.importDir(job.JobID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		m.releaseWorker()
		return nil, storageError(err)
	}
	target := filepath.Join(directory, "package.zip")
	if err := os.Rename(tempPath, target); err != nil {
		m.releaseWorker()
		_ = os.RemoveAll(filepath.Dir(tempPath))
		return nil, storageError(err)
	}
	_ = os.RemoveAll(filepath.Dir(tempPath))
	stored, err := m.store.Create(job)
	if err != nil {
		m.releaseWorker()
		if _, ok := AsError(err); ok {
			return nil, err
		}
		return nil, storageError(err)
	}
	if stored.JobID != job.JobID {
		m.releaseWorker()
		_ = os.RemoveAll(directory)
		return stored, nil
	}
	go m.runJob(job, m.limits.ValidateTimeout, m.runValidation)
	return stored, nil
}

func (m *Manager) runValidation(ctx context.Context, jobID string) error {
	job, err := m.setPhase(jobID, "validating_package")
	if err != nil {
		return err
	}
	importDir := m.importDir(jobID)
	packagePath := filepath.Join(importDir, "package.zip")
	extractDir := filepath.Join(importDir, "extracted")
	defer os.RemoveAll(extractDir)

	info, issues, err := inspectPackage(ctx, packagePath, extractDir, m.limits)
	if err != nil {
		if classified, ok := AsError(err); ok {
			m.auditWrite(job.Actor, "backup_validate", jobID, "failed", classified.Message)
			return classified
		}
		return storageError(err)
	}
	preview := m.buildPreview(jobID, info, issues)
	if err := m.writePreview(preview); err != nil {
		return storageError(err)
	}
	result := &JobResult{PreviewID: preview.PreviewID, PackageSha256: preview.PackageSHA256}
	if preview.Backup != nil {
		counts := preview.Backup.Counts
		result.Counts = &counts
	} else {
		result.Counts = nil
	}
	if _, err := m.patchJob(jobID, func(record *Job) {
		record.State = StateSucceeded
		record.Phase = "validated"
		record.Error = nil
		record.PreviewID = stringPointer(preview.PreviewID)
		record.Result = result
	}); err != nil {
		return err
	}
	status := "succeeded"
	if !preview.Valid {
		status = "invalid"
	}
	m.auditWrite(job.Actor, "backup_validate", jobID, status, strings.Join(preview.Issues, ","))
	return nil
}

func (m *Manager) buildPreview(jobID string, info *PackageInfo, issues []string) *Preview {
	now := m.now()
	preview := &Preview{
		PreviewID:        uuid.NewString(),
		JobID:            jobID,
		Valid:            len(issues) == 0,
		PackageSHA256:    info.PackageSHA256,
		Warnings:         []string{},
		Issues:           append([]string{}, issues...),
		ExpiresAt:        now.Add(previewLifetime).Format(time.RFC3339),
		TargetInstanceID: m.instanceID,
		CreatedAt:        now.Format(time.RFC3339),
	}
	if info.Manifest != nil {
		manifest := info.Manifest
		preview.Warnings = append(preview.Warnings, manifest.Warnings...)
		preview.Backup = &BackupSummary{
			BackupID:      manifest.BackupID,
			CreatedAt:     manifest.CreatedAt,
			ConsistentAt:  manifest.ConsistentAt,
			Producer:      manifest.Producer,
			SchemaVersion: manifest.SchemaVersion,
			Counts:        manifest.Counts,
			Files:         len(manifest.Files),
		}
	}
	return preview
}

// GetPreview returns a stored preview, rejecting expired ones.
func (m *Manager) GetPreview(previewID string) (*Preview, error) {
	preview, err := m.findPreview(previewID)
	if err != nil {
		return nil, err
	}
	if expires, err := time.Parse(time.RFC3339, preview.ExpiresAt); err == nil && m.now().After(expires) {
		return nil, &Error{HTTPStatus: 410, Type: ErrPreviewExpired,
			Message: "restore preview has expired; upload the package again"}
	}
	return preview, nil
}

func (m *Manager) findPreview(previewID string) (*Preview, error) {
	roots, err := os.ReadDir(filepath.Join(m.layout.ControlDir, "imports"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, previewNotFound("restore preview not found")
		}
		return nil, err
	}
	for _, entry := range roots {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(m.layout.ControlDir, "imports", entry.Name(), "preview.json")
		payload, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var preview Preview
		if err := json.Unmarshal(payload, &preview); err != nil {
			continue
		}
		if preview.PreviewID == previewID {
			return &preview, nil
		}
	}
	return nil, previewNotFound("restore preview not found")
}

// ExpireImports deletes packages whose preview expired and that no restore
// plan references. It never deletes an executed/restart_required package.
func (m *Manager) ExpireImports() {
	plan, _ := loadPlan(m.layout.ControlDir)
	referenced := ""
	if plan != nil {
		referenced = plan.JobID
	}
	roots, err := os.ReadDir(filepath.Join(m.layout.ControlDir, "imports"))
	if err != nil {
		return
	}
	for _, entry := range roots {
		if !entry.IsDir() || entry.Name() == referenced {
			continue
		}
		path := filepath.Join(m.layout.ControlDir, "imports", entry.Name(), "preview.json")
		payload, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var preview Preview
		if err := json.Unmarshal(payload, &preview); err != nil {
			continue
		}
		expires, err := time.Parse(time.RFC3339, preview.ExpiresAt)
		if err == nil && m.now().After(expires) {
			_ = os.RemoveAll(filepath.Join(m.layout.ControlDir, "imports", entry.Name()))
		}
	}
}

// HashReader returns the SHA-256 and byte count while the caller streams.
type HashReader struct {
	Reader io.Reader
	Hash   hashWriter
	Total  int64
}

type hashWriter interface {
	io.Writer
	Sum([]byte) []byte
}

func NewHashReader(reader io.Reader) *HashReader {
	return &HashReader{Reader: reader, Hash: sha256.New()}
}

func (h *HashReader) Read(buffer []byte) (int, error) {
	count, err := h.Reader.Read(buffer)
	if count > 0 {
		_, _ = h.Hash.Write(buffer[:count])
		h.Total += int64(count)
	}
	return count, err
}

func (h *HashReader) SHA256() string {
	return hex.EncodeToString(h.Hash.Sum(nil))
}
