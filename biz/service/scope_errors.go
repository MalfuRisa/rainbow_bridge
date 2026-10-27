package service

import (
	"errors"
	"net/http"
)

const (
	ErrTypeScopeInvalidRequest  = "SCOPE_INVALID_REQUEST"
	ErrTypeScopeIdentityConflict = "SCOPE_IDENTITY_CONFLICT"
	ErrTypeScopeDeleted         = "SCOPE_DELETED"
	ErrTypeScopeNotFound        = "SCOPE_NOT_FOUND"
)

// ScopeError is the lifecycle/identity failure surface for management APIs.
type ScopeError struct {
	HTTPStatus int
	Code       int
	ErrorType  string
	Message    string
}

func (e *ScopeError) Error() string { return e.Message }

func AsScopeError(err error) (*ScopeError, bool) {
	var target *ScopeError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

func scopeInvalidRequest(message string) error {
	return &ScopeError{HTTPStatus: http.StatusBadRequest, Code: http.StatusBadRequest,
		ErrorType: ErrTypeScopeInvalidRequest, Message: message}
}

func scopeIdentityConflict(message string) error {
	return &ScopeError{HTTPStatus: http.StatusConflict, Code: http.StatusConflict,
		ErrorType: ErrTypeScopeIdentityConflict, Message: message}
}

func scopeDeletedError(message string) error {
	return &ScopeError{HTTPStatus: http.StatusGone, Code: http.StatusGone,
		ErrorType: ErrTypeScopeDeleted, Message: message}
}

func scopeNotFoundError(message string) error {
	return &ScopeError{HTTPStatus: http.StatusNotFound, Code: http.StatusNotFound,
		ErrorType: ErrTypeScopeNotFound, Message: message}
}