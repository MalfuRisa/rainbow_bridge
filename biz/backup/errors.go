package backup

import (
	"errors"
	"fmt"
	"net/http"
)

// Error is the machine-readable failure surface shared by the HTTP handlers,
// the asynchronous job records and the offline CLI.
type Error struct {
	HTTPStatus int
	Type       string
	Message    string
	RetryAfter int
}

func (e *Error) Error() string { return e.Message }

// Error classification values from the management API contract.
const (
	ErrInvalidRequest    = "BACKUP_INVALID_REQUEST"
	ErrPackageInvalid    = "BACKUP_PACKAGE_INVALID"
	ErrNotFound          = "BACKUP_NOT_FOUND"
	ErrPreviewNotFound   = "BACKUP_PREVIEW_NOT_FOUND"
	ErrBusy              = "BACKUP_BUSY"
	ErrRequestConflict   = "BACKUP_REQUEST_CONFLICT"
	ErrStateConflict     = "BACKUP_STATE_CONFLICT"
	ErrRestorePending    = "BACKUP_RESTORE_PENDING"
	ErrRollbackReview    = "BACKUP_ROLLBACK_REVIEW_REQUIRED"
	ErrTargetMismatch    = "BACKUP_TARGET_MISMATCH"
	ErrInUse             = "BACKUP_IN_USE"
	ErrPreviewExpired    = "BACKUP_PREVIEW_EXPIRED"
	ErrLimitExceeded     = "BACKUP_LIMIT_EXCEEDED"
	ErrUnsupported       = "BACKUP_UNSUPPORTED"
	ErrMaintenance       = "BACKUP_MAINTENANCE"
	ErrSpaceInsufficient = "BACKUP_SPACE_INSUFFICIENT"
	ErrStorage           = "BACKUP_STORAGE_ERROR"
	ErrInternal          = "BACKUP_INTERNAL_ERROR"
)

func newError(status int, kind, message string) *Error {
	return &Error{HTTPStatus: status, Type: kind, Message: message}
}

func invalidRequest(message string) *Error {
	return newError(http.StatusBadRequest, ErrInvalidRequest, message)
}

func packageInvalid(message string) *Error {
	return newError(http.StatusBadRequest, ErrPackageInvalid, message)
}

func notFound(message string) *Error {
	return newError(http.StatusNotFound, ErrNotFound, message)
}

func previewNotFound(message string) *Error {
	return newError(http.StatusNotFound, ErrPreviewNotFound, message)
}

func busyError(message string) *Error {
	return newError(http.StatusConflict, ErrBusy, message)
}

func stateConflict(message string) *Error {
	return newError(http.StatusConflict, ErrStateConflict, message)
}

func restorePendingError() *Error {
	return newError(http.StatusConflict, ErrRestorePending,
		"a restore plan is waiting for a restart; restart the service to apply it or cancel that plan before restoring again")
}

func rollbackReviewError() *Error {
	return newError(http.StatusConflict, ErrRollbackReview,
		"a previous restore left a rollback copy; ask the deployment administrator to review and archive or clean up that copy before restoring again")
}

func targetMismatch(message string) *Error {
	return newError(http.StatusConflict, ErrTargetMismatch, message)
}

func inUseError(message string) *Error {
	return newError(http.StatusConflict, ErrInUse, message)
}

func limitError(message string) *Error {
	return newError(http.StatusRequestEntityTooLarge, ErrLimitExceeded, message)
}

func unsupportedError(reasons []string) *Error {
	return newError(http.StatusServiceUnavailable, ErrUnsupported,
		fmt.Sprintf("backup is not supported in this deployment: %v", reasons))
}

func spaceError(message string) *Error {
	return newError(http.StatusServiceUnavailable, ErrSpaceInsufficient, message)
}

func storageError(err error) *Error {
	// Keep SQL, absolute paths and credentials out of the client surface.
	return &Error{HTTPStatus: http.StatusInternalServerError, Type: ErrStorage,
		Message: "backup storage operation failed"}
}

func internalError(err error) *Error {
	_ = err
	return &Error{HTTPStatus: http.StatusInternalServerError, Type: ErrInternal,
		Message: "internal backup error"}
}

// AsError extracts a classified backup error.
func AsError(err error) (*Error, bool) {
	var target *Error
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// MaintenanceError is exposed so the service write gate can reject new
// business writes with the documented 503 response.
func MaintenanceError() error {
	return &Error{HTTPStatus: http.StatusServiceUnavailable, Type: ErrMaintenance,
		Message: "business writes are paused for backup maintenance", RetryAfter: 5}
}

// IsMaintenance reports whether err is the maintenance rejection.
func IsMaintenance(err error) bool {
	classified, ok := AsError(err)
	return ok && classified.Type == ErrMaintenance
}
