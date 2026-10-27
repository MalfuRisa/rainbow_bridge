package service

import "net/http"

const (
	ErrTypeTransferInvalidRequest   = "TRANSFER_INVALID_REQUEST"
	ErrTypeTransferSelectionMissing = "TRANSFER_SELECTION_NOT_FOUND"
	ErrTypeTransferActiveChanged    = "TRANSFER_ACTIVE_CHANGED"
	ErrTypeTransferSnapshotMismatch = "RELEASE_SNAPSHOT_MISMATCH"
	ErrTypeTransferLimitExceeded    = "TRANSFER_EXPORT_LIMIT_EXCEEDED"
	ErrTypeTransferIncomplete       = "TRANSFER_RESOURCE_INCOMPLETE"
	ErrTypeTransferTimeout          = "TRANSFER_EXPORT_TIMEOUT"
)

func newTransferExportError(status int, errorType, message string) *TransferExportError {
	return &TransferExportError{HTTPStatus: status, Code: status, ErrorType: errorType, Message: message}
}

func transferInvalidRequest(message string) error {
	return newTransferExportError(http.StatusBadRequest, ErrTypeTransferInvalidRequest, message)
}

func transferSelectionNotFound(message string) error {
	return newTransferExportError(http.StatusNotFound, ErrTypeTransferSelectionMissing, message)
}

func transferActiveChanged(message string) error {
	return newTransferExportError(http.StatusConflict, ErrTypeTransferActiveChanged, message)
}

func transferSnapshotMismatch(message string) error {
	return newTransferExportError(http.StatusConflict, ErrTypeTransferSnapshotMismatch, message)
}

func transferLimitExceeded(message string) error {
	return newTransferExportError(http.StatusRequestEntityTooLarge, ErrTypeTransferLimitExceeded, message)
}

func transferIncomplete(message string) error {
	return newTransferExportError(http.StatusServiceUnavailable, ErrTypeTransferIncomplete, message)
}

func transferTimeout(message string) error {
	return newTransferExportError(http.StatusServiceUnavailable, ErrTypeTransferTimeout, message)
}

func AsTransferExportError(err error) (*TransferExportError, bool) {
	target, ok := err.(*TransferExportError)
	return target, ok
}