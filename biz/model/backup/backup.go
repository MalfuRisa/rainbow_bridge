// Package backup contains the management API view types declared by
// idl/biz/backup.proto. The JSON field names are the published contract.
package backup

// CreateRequest starts an asynchronous whole-instance backup.
type CreateRequest struct {
	RequestID string `json:"request_id"`
	Reason    string `json:"reason"`
}

// DeleteRequest removes a completed backup after digest confirmation.
type DeleteRequest struct {
	BackupID       string `json:"backup_id"`
	ExpectedSha256 string `json:"expected_sha256"`
	RequestID      string `json:"request_id"`
}

// ExecuteRequest prepares a validated package for the next restart.
type ExecuteRequest struct {
	PreviewID        string `json:"preview_id"`
	PackageSha256    string `json:"package_sha256"`
	TargetInstanceID string `json:"target_instance_id"`
	RequestID        string `json:"request_id"`
	Confirmation     string `json:"confirmation"`
	Reason           string `json:"reason"`
}

// CancelRequest abandons a prepared restore before the restart.
type CancelRequest struct {
	JobID     string `json:"job_id"`
	RequestID string `json:"request_id"`
}

// Envelope is the shared response shape. Code mirrors the HTTP status for
// errors and uses 200/202 for accepted work.
type Envelope struct {
	Code      int32  `json:"code"`
	Msg       string `json:"msg"`
	Error     string `json:"error,omitempty"`
	ErrorType string `json:"error_type,omitempty"`
	Data      any    `json:"data,omitempty"`
}

// JobData wraps a task record.
type JobData struct {
	Job any `json:"job"`
}

// ListData is the backup list payload.
type ListData struct {
	Items        any    `json:"items"`
	HasMore      bool   `json:"has_more"`
	NextBeforeID string `json:"next_before_id"`
}

// DeleteData reports a completed deletion.
type DeleteData struct {
	Deleted bool `json:"deleted"`
}

// CancelData reports a cancelled restore plan.
type CancelData struct {
	Cancelled bool `json:"cancelled"`
}
