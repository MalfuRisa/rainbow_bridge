package service

import (
	"errors"
	"fmt"
)

// 发布版本相关 machine-readable error_type(见 PRD §7.1)。
const (
	ErrTypeReleaseInvalidRequest     = "RELEASE_INVALID_REQUEST"
	ErrTypeReleaseScopeNotFound      = "RELEASE_SCOPE_NOT_FOUND"
	ErrTypeReleaseVersionNotFound    = "RELEASE_VERSION_NOT_FOUND"
	ErrTypeReleaseNotActive          = "RELEASE_NOT_ACTIVE"
	ErrTypeReleaseAssetNotFound      = "RELEASE_ASSET_NOT_FOUND"
	ErrTypeReleaseSourceNotFrozen    = "RELEASE_SOURCE_NOT_FROZEN"
	ErrTypeReleaseSourceRevision     = "RELEASE_SOURCE_REVISION_CONFLICT"
	ErrTypeReleaseVersionExists      = "RELEASE_VERSION_EXISTS"
	ErrTypeReleaseRequestConflict    = "RELEASE_REQUEST_CONFLICT"
	ErrTypeReleaseActiveRevision     = "RELEASE_ACTIVE_REVISION_CONFLICT"
	ErrTypeReleaseActiveUnchanged    = "RELEASE_ACTIVE_UNCHANGED"
	ErrTypeReleaseResourceInvalid    = "RELEASE_RESOURCE_INVALID"
	ErrTypeReleaseScopeHasVersions   = "RELEASE_SCOPE_HAS_VERSIONS"
	ErrTypeReleaseLimitExceeded      = "RELEASE_LIMIT_EXCEEDED"
	ErrTypeReleaseStorageUnsupported = "RELEASE_STORAGE_UNSUPPORTED"
	ErrTypeReleaseIncomplete         = "RELEASE_INCOMPLETE"
	ErrTypeReleaseInternalError      = "RELEASE_INTERNAL_ERROR"
	ErrTypeReleaseStorageError       = "RELEASE_STORAGE_ERROR"
)

// ReleaseError 携带 HTTP 状态、业务 code 与 error_type,供 HTTP 层统一映射。
type ReleaseError struct {
	HTTPStatus int
	Code       int
	ErrorType  string
	Message    string

	Issues       []ReleaseResourceIssue
	Limits       *ReleaseLimitInfo
	LatestActive *ReleaseActiveInfo
}

func (e *ReleaseError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// AsReleaseError 从错误链中提取 ReleaseError。
func AsReleaseError(err error) (*ReleaseError, bool) {
	var releaseErr *ReleaseError
	if errors.As(err, &releaseErr) {
		return releaseErr, true
	}
	return nil, false
}

func newReleaseError(httpStatus int, errorType, message string) *ReleaseError {
	return &ReleaseError{HTTPStatus: httpStatus, Code: httpStatus, ErrorType: errorType, Message: message}
}

func releaseInvalidRequestError(message string) *ReleaseError {
	return newReleaseError(400, ErrTypeReleaseInvalidRequest, message)
}

func releaseScopeNotFoundError(environmentKey, pipelineKey string) *ReleaseError {
	return newReleaseError(404, ErrTypeReleaseScopeNotFound, fmt.Sprintf("scope %s/%s not found", environmentKey, pipelineKey))
}

func releaseVersionNotFoundError(environmentKey, pipelineKey, version string) *ReleaseError {
	return newReleaseError(404, ErrTypeReleaseVersionNotFound, fmt.Sprintf("version %s not found in scope %s/%s", version, environmentKey, pipelineKey))
}

func releaseNotActiveError(environmentKey, pipelineKey string) *ReleaseError {
	return newReleaseError(404, ErrTypeReleaseNotActive, fmt.Sprintf("scope %s/%s has no active release", environmentKey, pipelineKey))
}

func releaseAssetNotFoundError(snapshotID, assetID string) *ReleaseError {
	return newReleaseError(404, ErrTypeReleaseAssetNotFound, fmt.Sprintf("release asset %s/%s not found", snapshotID, assetID))
}

func releaseSourceNotFrozenError(environmentKey, pipelineKey string) *ReleaseError {
	return newReleaseError(409, ErrTypeReleaseSourceNotFrozen, fmt.Sprintf("scope %s/%s is not frozen; lock it before creating a release", environmentKey, pipelineKey))
}

func releaseSourceRevisionError(expected, actual int64) *ReleaseError {
	return newReleaseError(409, ErrTypeReleaseSourceRevision, fmt.Sprintf("expected_freeze_revision %d does not match current freeze revision %d", expected, actual))
}

func releaseVersionExistsError(environmentKey, pipelineKey, version string) *ReleaseError {
	return newReleaseError(409, ErrTypeReleaseVersionExists, fmt.Sprintf("version %s already exists in scope %s/%s", version, environmentKey, pipelineKey))
}

func releaseRequestConflictError(message string) *ReleaseError {
	return newReleaseError(409, ErrTypeReleaseRequestConflict, message)
}

func releaseActiveRevisionError(expected int64, latest *ReleaseActiveInfo) *ReleaseError {
	err := newReleaseError(409, ErrTypeReleaseActiveRevision, fmt.Sprintf("expected_active_revision %d does not match current head revision", expected))
	err.LatestActive = latest
	if latest != nil {
		err.Message = fmt.Sprintf("expected_active_revision %d does not match current head revision %d", expected, latest.Revision)
	}
	return err
}

func releaseActiveUnchangedError(active *ReleaseActiveInfo) *ReleaseError {
	err := newReleaseError(409, ErrTypeReleaseActiveUnchanged, "target version is already active")
	err.LatestActive = active
	return err
}

func releaseResourceInvalidError(issues []ReleaseResourceIssue) *ReleaseError {
	err := newReleaseError(409, ErrTypeReleaseResourceInvalid, "release precheck failed; resolve reported resources before creating a version")
	if issues == nil {
		issues = []ReleaseResourceIssue{}
	}
	err.Issues = issues
	return err
}

func releaseScopeHasVersionsError(environmentKey, pipelineKey string) *ReleaseError {
	return newReleaseError(409, ErrTypeReleaseScopeHasVersions, fmt.Sprintf("scope %s/%s has committed release versions; delete is refused", environmentKey, pipelineKey))
}

func releaseLimitExceededError(limit ReleaseLimitInfo) *ReleaseError {
	err := newReleaseError(413, ErrTypeReleaseLimitExceeded, fmt.Sprintf("release limit exceeded: %s limit=%d actual=%d", limit.Item, limit.Limit, limit.Actual))
	err.Limits = &limit
	return err
}

func releaseStorageUnsupportedError() *ReleaseError {
	return newReleaseError(503, ErrTypeReleaseStorageUnsupported, "creating a release from MinIO source storage is not supported in this version")
}

func releaseIncompleteError(message string) *ReleaseError {
	return newReleaseError(503, ErrTypeReleaseIncomplete, message)
}

func releaseStorageError(err error) *ReleaseError {
	return newReleaseError(500, ErrTypeReleaseStorageError, fmt.Sprintf("release storage operation failed: %v", err))
}

func releaseInternalError(err error) *ReleaseError {
	return newReleaseError(500, ErrTypeReleaseInternalError, fmt.Sprintf("release operation failed: %v", err))
}
