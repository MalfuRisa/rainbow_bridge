package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Job states from the management contract.
const (
	StateQueued          = "queued"
	StateRunning         = "running"
	StateSucceeded       = "succeeded"
	StateFailed          = "failed"
	StateInterrupted     = "interrupted"
	StateRestartRequired = "restart_required"
	StateCancelled       = "cancelled"

	KindCreate   = "create"
	KindValidate = "validate"
	KindRestore  = "restore"
)

// JobResult is the typed, persisted result of a completed task.
type JobResult struct {
	BackupID        string  `json:"backup_id,omitempty"`
	Sha256          string  `json:"sha256,omitempty"`
	Size            int64   `json:"size,omitempty"`
	ConsistentAt    string  `json:"consistent_at,omitempty"`
	Counts          *Counts `json:"counts,omitempty"`
	PreviewID       string  `json:"preview_id,omitempty"`
	PackageSha256   string  `json:"package_sha256,omitempty"`
	RequiresRestart bool    `json:"requires_restart,omitempty"`
	Message         string  `json:"message,omitempty"`
}

// JobError never contains SQL, absolute paths or credentials.
type JobError struct {
	Type    string `json:"error_type"`
	Message string `json:"msg"`
}

// Job is both the persisted task record and the HTTP status payload.
type Job struct {
	JobID       string     `json:"job_id"`
	RequestID   string     `json:"request_id"`
	Kind        string     `json:"kind"`
	State       string     `json:"state"`
	Phase       string     `json:"phase"`
	CreatedAt   string     `json:"created_at"`
	UpdatedAt   string     `json:"updated_at"`
	Actor       string     `json:"actor"`
	Reason      string     `json:"reason"`
	BackupID    *string    `json:"backup_id"`
	PreviewID   *string    `json:"preview_id"`
	Result      *JobResult `json:"result"`
	Error       *JobError  `json:"error"`
	Fingerprint string     `json:"-"`
	PackageSHA  string     `json:"-"`
}

func newJob(kind, requestID, actor, reason, fingerprint string) *Job {
	now := time.Now().UTC().Format(time.RFC3339)
	return &Job{
		JobID:       uuid.NewString(),
		RequestID:   requestID,
		Kind:        kind,
		State:       StateQueued,
		Phase:       "waiting",
		CreatedAt:   now,
		UpdatedAt:   now,
		Actor:       actor,
		Reason:      reason,
		Fingerprint: fingerprint,
	}
}

func isTerminal(state string) bool {
	switch state {
	case StateSucceeded, StateFailed, StateInterrupted, StateCancelled:
		return true
	default:
		return false
	}
}

// JobStore persists task records outside the replaceable business root.
type JobStore struct {
	root        string
	indexRoot   string
	maxTerminal int
	mu          sync.Mutex
}

func NewJobStore(controlDir string, maxTerminal int) *JobStore {
	return &JobStore{
		root:        filepath.Join(controlDir, "jobs"),
		indexRoot:   filepath.Join(controlDir, "jobs", "requests"),
		maxTerminal: maxTerminal,
	}
}

func (s *JobStore) ensure() error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	return os.MkdirAll(s.indexRoot, 0o700)
}

func (s *JobStore) jobPath(jobID string) string {
	return filepath.Join(s.root, jobID+".json")
}

// Create persists a new job and its request index atomically under the store
// mutex. It returns an existing job for an identical replay.
func (s *JobStore) Create(job *Job) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
		return nil, err
	}
	existing, err := s.findByRequestLocked(job.Kind, job.RequestID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.Fingerprint != job.Fingerprint {
			return nil, &Error{HTTPStatus: 409, Type: ErrRequestConflict,
				Message: "request_id was already used with different fields"}
		}
		return existing, nil
	}
	if err := s.pruneLocked(); err != nil {
		return nil, err
	}
	if err := s.saveLocked(job); err != nil {
		return nil, err
	}
	index := requestIndex{JobID: job.JobID, Fingerprint: job.Fingerprint}
	payload, err := json.Marshal(index)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(s.indexRoot, indexKey(job.Kind, job.RequestID)+".json")
	if err := writeFileAtomic(path, payload, 0o600); err != nil {
		// Without its request index the job would be invisible to replay and
		// could be duplicated by a retry.
		_ = os.Remove(s.jobPath(job.JobID))
		return nil, err
	}
	return job, nil
}

// Save updates an existing job record.
func (s *JobStore) Save(job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return s.saveLocked(job)
}

func (s *JobStore) saveLocked(job *Job) error {
	payload, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.jobPath(job.JobID), payload, 0o600)
}

func (s *JobStore) Get(jobID string) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(jobID)
}

func (s *JobStore) getLocked(jobID string) (*Job, error) {
	payload, err := os.ReadFile(s.jobPath(jobID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, notFound("backup job not found")
		}
		return nil, err
	}
	var job Job
	if err := json.Unmarshal(payload, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

// FindByRequest returns the original job for an idempotent replay.
func (s *JobStore) FindByRequest(kind, requestID string) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.findByRequestLocked(kind, requestID)
}

func (s *JobStore) findByRequestLocked(kind, requestID string) (*Job, error) {
	payload, err := os.ReadFile(filepath.Join(s.indexRoot, indexKey(kind, requestID)+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var index requestIndex
	if err := json.Unmarshal(payload, &index); err != nil {
		return nil, err
	}
	return s.getLocked(index.JobID)
}

// List returns all persisted jobs, newest first.
func (s *JobStore) List() ([]*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

func (s *JobStore) listLocked() ([]*Job, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	jobs := make([]*Job, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		job, err := s.getLocked(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt > jobs[j].CreatedAt })
	return jobs, nil
}

// RecoverInterrupted converts queued/running tasks from a previous process
// into interrupted records. Restore plans in restart_required are preserved.
func (s *JobStore) RecoverInterrupted() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs, err := s.listLocked()
	if err != nil {
		return nil, err
	}
	interrupted := make([]string, 0)
	for _, job := range jobs {
		if job.State != StateQueued && job.State != StateRunning {
			continue
		}
		job.State = StateInterrupted
		job.Phase = "interrupted"
		job.Error = &JobError{Type: ErrInternal, Message: "service stopped before the task completed"}
		job.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if err := s.saveLocked(job); err != nil {
			return interrupted, err
		}
		interrupted = append(interrupted, job.JobID)
	}
	return interrupted, nil
}

func (s *JobStore) pruneLocked() error {
	jobs, err := s.listLocked()
	if err != nil {
		return err
	}
	terminal := make([]*Job, 0)
	for _, job := range jobs {
		if isTerminal(job.State) {
			terminal = append(terminal, job)
		}
	}
	if len(terminal) < s.maxTerminal {
		return nil
	}
	for _, job := range terminal[s.maxTerminal-1:] {
		// A completed create job owns a downloadable package; pruning it would
		// hide the backup from list/download without deleting the file.
		if job.Kind == KindCreate && job.State == StateSucceeded {
			continue
		}
		_ = os.Remove(s.jobPath(job.JobID))
		_ = os.Remove(filepath.Join(s.indexRoot, indexKey(job.Kind, job.RequestID)+".json"))
	}
	return nil
}

type requestIndex struct {
	JobID       string `json:"job_id"`
	Fingerprint string `json:"fingerprint"`
}

func indexKey(kind, requestID string) string {
	digest := sha256.Sum256([]byte(kind + "\x00" + requestID))
	return hex.EncodeToString(digest[:])
}

// fingerprint hashes a canonical field sequence for request conflict checks.
func fingerprint(fields ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(fields, "\x00")))
	return hex.EncodeToString(digest[:])
}

// validateRequestID accepts a canonical UUID.
func validateRequestID(value string) error {
	if _, err := uuid.Parse(strings.TrimSpace(value)); err != nil {
		return invalidRequest("request_id must be a UUID")
	}
	return nil
}

func validReason(value string) error {
	if len([]rune(value)) > 500 {
		return invalidRequest("reason must not exceed 500 characters")
	}
	return nil
}
