package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditEvent is append-only operator activity. It never stores credentials,
// package contents or absolute server paths.
type AuditEvent struct {
	At     string `json:"at"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Object string `json:"object"`
	Result string `json:"result"`
	Detail string `json:"detail,omitempty"`
}

type auditLog struct {
	mu   sync.Mutex
	path string
}

func newAuditLog(controlDir string) *auditLog {
	return &auditLog{path: filepath.Join(controlDir, "audit.jsonl")}
}

// Write appends one JSON line and fsyncs it. A failure is returned because a
// destructive switch must not proceed without its audit record.
func (a *auditLog) Write(event AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	event.At = time.Now().UTC().Format(time.RFC3339)
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	handle, err := os.OpenFile(a.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := handle.Write(append(payload, '\n')); err != nil {
		_ = handle.Close()
		return err
	}
	if err := handle.Sync(); err != nil {
		_ = handle.Close()
		return err
	}
	return handle.Close()
}
