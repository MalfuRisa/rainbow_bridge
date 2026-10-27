package service

import (
	"errors"
	"fmt"
)

// 冻结相关 machine-readable error_type。
const (
	ErrTypeScopeFrozen            = "SCOPE_FROZEN"
	ErrTypeFreezeRevisionConflict = "FREEZE_REVISION_CONFLICT"
	ErrTypeFreezeStateUnchanged   = "FREEZE_STATE_UNCHANGED"
	ErrTypeFreezeAdminNotSet      = "FREEZE_ADMIN_NOT_CONFIGURED"
	ErrTypeFreezeUnauthorized     = "FREEZE_UNAUTHORIZED"
	ErrTypeFreezeScopeNotFound    = "FREEZE_SCOPE_NOT_FOUND"
	ErrTypeFreezeInvalidRequest   = "FREEZE_INVALID_REQUEST"
	ErrTypeFreezeInvalidReason    = "FREEZE_INVALID_REASON"
	ErrTypeFreezeInvalidRevision  = "FREEZE_INVALID_REVISION"
	ErrTypeFreezeResourcePrecheck = "FREEZE_RESOURCE_PRECHECK_FAILED"
	ErrTypeFreezeGateBusy         = "FREEZE_WRITE_GATE_BUSY"
	ErrTypeFreezeWriteTimeout     = "FREEZE_WRITE_TIMEOUT"
	ErrTypeFreezeInternal         = "FREEZE_INTERNAL"
)

// FreezeBlockedScope 描述被冻结而阻止写入的范围。
type FreezeBlockedScope struct {
	ScopeID        string
	EnvironmentKey string
	PipelineKey    string
	State          string
	Revision       int64
}

// FreezeError 携带 HTTP 状态、业务 code 与 error_type,供 HTTP 层统一映射。
type FreezeError struct {
	HTTPStatus int
	Code       int
	ErrorType  string
	Message    string

	// 409 SCOPE_FROZEN:实际受阻范围(不含资源绝对路径)
	BlockedScopes []FreezeBlockedScope
	// 409 状态冲突:最新状态
	LatestState    string
	LatestRevision int64
	// 400 锁定前资源检查失败
	Issues             []FreezeResourceIssue
	ExternalReferences []string
}

func (e *FreezeError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// AsFreezeError 从错误链中提取 FreezeError。
func AsFreezeError(err error) (*FreezeError, bool) {
	var freezeErr *FreezeError
	if errors.As(err, &freezeErr) {
		return freezeErr, true
	}
	return nil, false
}

func newFreezeError(httpStatus int, errorType, message string) *FreezeError {
	return &FreezeError{HTTPStatus: httpStatus, Code: httpStatus, ErrorType: errorType, Message: message}
}

// scopeFrozenError 构造 409 SCOPE_FROZEN(含实际受阻范围)。
func scopeFrozenError(blocked []FreezeBlockedScope) *FreezeError {
	blocked = nonEmptyBlocked(blocked)
	message := "scope is frozen"
	if len(blocked) > 0 {
		first := blocked[0]
		message = fmt.Sprintf("scope %s/%s is frozen (state=%s revision=%d)", first.EnvironmentKey, first.PipelineKey, first.State, first.Revision)
	}
	err := newFreezeError(409, ErrTypeScopeFrozen, message)
	err.BlockedScopes = blocked
	return err
}

func nonEmptyBlocked(blocked []FreezeBlockedScope) []FreezeBlockedScope {
	if blocked == nil {
		return []FreezeBlockedScope{}
	}
	return blocked
}

// freezeConflictError 构造状态冲突错误(目标状态已是当前状态,或 revision 过期)。
func freezeConflictError(errorType, message, latestState string, latestRevision int64) *FreezeError {
	err := newFreezeError(409, errorType, message)
	err.LatestState = latestState
	err.LatestRevision = latestRevision
	return err
}

func freezeAdminNotConfiguredError() *FreezeError {
	return newFreezeError(503, ErrTypeFreezeAdminNotSet, "freeze admin token is not configured")
}

func freezeUnauthorizedError() *FreezeError {
	return newFreezeError(401, ErrTypeFreezeUnauthorized, "invalid freeze admin credentials")
}

func freezeScopeNotFoundError(environmentKey, pipelineKey string) *FreezeError {
	return newFreezeError(404, ErrTypeFreezeScopeNotFound, fmt.Sprintf("scope %s/%s not found", environmentKey, pipelineKey))
}

func freezeInvalidRequestError(message string) *FreezeError {
	return newFreezeError(400, ErrTypeFreezeInvalidRequest, message)
}

func freezeInvalidReasonError() *FreezeError {
	return newFreezeError(400, ErrTypeFreezeInvalidReason, "reason must not exceed 500 characters after trimming")
}

func freezeInvalidRevisionError(value string) *FreezeError {
	return newFreezeError(400, ErrTypeFreezeInvalidRevision, fmt.Sprintf("expected_revision must be a non-negative decimal string, got %q", value))
}

func freezePrecheckError(precheck FreezeLockPrecheck) *FreezeError {
	err := newFreezeError(400, ErrTypeFreezeResourcePrecheck, "resource precheck failed; resolve reported resources before locking")
	err.Issues = precheck.Issues
	err.ExternalReferences = precheck.ExternalReferences
	return err
}

func freezeGateBusyError() *FreezeError {
	return newFreezeError(503, ErrTypeFreezeGateBusy, "write coordination is busy; retry after current writes finish")
}

func freezeWriteTimeoutError() *FreezeError {
	return newFreezeError(503, ErrTypeFreezeWriteTimeout, "write operation exceeded the 60s design deadline")
}

func freezeInternalError(err error) *FreezeError {
	return newFreezeError(500, ErrTypeFreezeInternal, fmt.Sprintf("freeze operation failed: %v", err))
}
