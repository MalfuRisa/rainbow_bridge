package service

import (
	"errors"
)

// 认证相关 machine-readable error_type(见 PRD §7.5)。
const (
	ErrTypeAuthInvalidRequest     = "AUTH_INVALID_REQUEST"
	ErrTypeAuthCurrentPassword    = "AUTH_CURRENT_PASSWORD_INVALID"
	ErrTypeAuthInvalidCredentials = "AUTH_INVALID_CREDENTIALS"
	ErrTypeAuthRequired           = "AUTH_REQUIRED"
	ErrTypeAuthForbidden          = "AUTH_FORBIDDEN"
	ErrTypeAuthCSRFInvalid        = "AUTH_CSRF_INVALID"
	ErrTypeAuthOriginDenied       = "AUTH_ORIGIN_DENIED"
	ErrTypeAuthCredentialChanged  = "AUTH_CREDENTIAL_CHANGED"
	ErrTypeAuthRateLimited        = "AUTH_RATE_LIMITED"
	ErrTypeAuthUnavailable        = "AUTH_UNAVAILABLE"
	ErrTypeAuthBusy               = "AUTH_BUSY"
)

// AuthError 携带 HTTP 状态、业务 code 与 error_type,供 HTTP 层统一映射。
type AuthError struct {
	HTTPStatus int
	Code       int
	ErrorType  string
	Message    string
	RetryAfter int // 仅 429 使用,单位秒
}

func (e *AuthError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// AsAuthError 从错误链中提取 AuthError。
func AsAuthError(err error) (*AuthError, bool) {
	var authErr *AuthError
	if errors.As(err, &authErr) {
		return authErr, true
	}
	return nil, false
}

func newAuthError(httpStatus int, errorType, message string) *AuthError {
	return &AuthError{HTTPStatus: httpStatus, Code: httpStatus, ErrorType: errorType, Message: message}
}

func authInvalidRequestError(message string) *AuthError {
	return newAuthError(400, ErrTypeAuthInvalidRequest, message)
}

func authCurrentPasswordInvalidError() *AuthError {
	return newAuthError(400, ErrTypeAuthCurrentPassword, "当前密码不正确")
}

func authInvalidCredentialsError() *AuthError {
	return newAuthError(401, ErrTypeAuthInvalidCredentials, "账号或密码错误")
}

func authRequiredError() *AuthError {
	return newAuthError(401, ErrTypeAuthRequired, "请先登录")
}

func authForbiddenError() *AuthError {
	return newAuthError(403, ErrTypeAuthForbidden, "账号不可用或权限不足")
}

func authCSRFError() *AuthError {
	return newAuthError(403, ErrTypeAuthCSRFInvalid, "会话校验失败,请刷新后重试")
}

func authOriginDeniedError() *AuthError {
	return newAuthError(403, ErrTypeAuthOriginDenied, "请求来源不被允许")
}

func authCredentialChangedError() *AuthError {
	return newAuthError(409, ErrTypeAuthCredentialChanged, "凭据已变更,请重新登录")
}

func authBusyError() *AuthError {
	return newAuthError(503, ErrTypeAuthBusy, "密码校验繁忙,请稍后重试")
}

func authRateLimitedError(retryAfter int) *AuthError {
	err := newAuthError(429, ErrTypeAuthRateLimited, "登录尝试过于频繁,请稍后再试")
	err.RetryAfter = retryAfter
	return err
}

// AuthInvalidRequestError 供 HTTP 层复用同一错误契约(参数/长度不合规)。
func AuthInvalidRequestError(message string) *AuthError {
	return authInvalidRequestError(message)
}

// AuthUnsupportedMediaError 415:仅接受 application/json。
func AuthUnsupportedMediaError() *AuthError {
	return authUnsupportedMediaError()
}

// AuthRequestTooLargeError 413:请求体超过上限。
func AuthRequestTooLargeError() *AuthError {
	return authRequestTooLargeError()
}

// AuthUnavailableError 把未知错误按 503 认证不可用表达(不降级匿名)。
func AuthUnavailableError(err error) *AuthError {
	return authUnavailableError(err)
}

// authUnsupportedMediaError 415:仅接受 application/json。
func authUnsupportedMediaError() *AuthError {
	return newAuthError(415, ErrTypeAuthInvalidRequest, "仅支持 application/json")
}

// authRequestTooLargeError 413:登录/改密请求体超过 4 KiB。
func authRequestTooLargeError() *AuthError {
	return newAuthError(413, ErrTypeAuthInvalidRequest, "请求体过大")
}

// authInternalError 未知错误:对外只给固定提示,底层细节既不进入响应也不进入日志消息。
func authInternalError(_ error) *AuthError {
	return newAuthError(500, "AUTH_INTERNAL_ERROR", "认证操作失败,请稍后重试")
}