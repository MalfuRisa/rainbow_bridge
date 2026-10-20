package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 登录限流:按可信客户端 IP 的固定窗口计数,有界内存,不建风控平台。
const (
	authLoginWindow        = time.Minute
	authLoginMaxAttempts   = 5
	authRateLimiterMaxKeys = 4096
	authPasswordConcurrent = 2
)

type authRateEntry struct {
	windowStart time.Time
	attempts    int
}

// authRateLimiter 是单进程限流器;容量满且无过期键时,新来源返回 429 而不是无条件放行。
type authRateLimiter struct {
	mu      sync.Mutex
	entries map[string]authRateEntry
	now     func() time.Time
}

func newAuthRateLimiter(now func() time.Time) *authRateLimiter {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &authRateLimiter{entries: make(map[string]authRateEntry), now: now}
}

// allow 记一次尝试并返回是否允许;被拒绝时给出建议的 Retry-After 秒数。
func (l *authRateLimiter) allow(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	entry, exists := l.entries[key]
	if !exists {
		if len(l.entries) >= authRateLimiterMaxKeys {
			l.evictExpiredLocked(now)
			if len(l.entries) >= authRateLimiterMaxKeys {
				return false, 60
			}
		}
		l.entries[key] = authRateEntry{windowStart: now, attempts: 1}
		return true, 0
	}
	if now.Sub(entry.windowStart) >= authLoginWindow {
		l.entries[key] = authRateEntry{windowStart: now, attempts: 1}
		return true, 0
	}
	if entry.attempts >= authLoginMaxAttempts {
		remaining := int((authLoginWindow - now.Sub(entry.windowStart)).Seconds())
		if remaining < 1 {
			remaining = 1
		}
		return false, remaining
	}
	entry.attempts++
	l.entries[key] = entry
	return true, 0
}

func (l *authRateLimiter) evictExpiredLocked(now time.Time) {
	for key, entry := range l.entries {
		if now.Sub(entry.windowStart) >= authLoginWindow {
			delete(l.entries, key)
		}
	}
}

// authComputeLimiter 限制每进程密码计算并发数;繁忙立即返回 503,不无界排队。
type authComputeLimiter struct {
	slots chan struct{}
}

func newAuthComputeLimiter(limit int) *authComputeLimiter {
	if limit <= 0 {
		limit = authPasswordConcurrent
	}
	return &authComputeLimiter{slots: make(chan struct{}, limit)}
}

func (l *authComputeLimiter) tryAcquire() bool {
	select {
	case l.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *authComputeLimiter) release() {
	select {
	case <-l.slots:
	default:
	}
}

// --------------------- 会话与 CSRF 值 ---------------------

// newSessionToken 生成会话凭证:32 字节密码学随机,只出现在 Cookie 与请求内存。
func newSessionToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// newCSRFToken 生成独立于凭证的 CSRF 值:同一会话内保持稳定,避免多标签页互相失效。
func newCSRFToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// csrfMatches 常量时间比较 CSRF 值。
func csrfMatches(expected, presented string) bool {
	if expected == "" || presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(presented)) == 1
}

// --------------------- 会话判定 ---------------------

// sessionValidity 判定一个已读取的会话是否可用(未撤销、未到期、账号可用、凭据版本一致)。
func sessionValid(session authSessionRow, user authUserRow, now time.Time) (*AuthError, bool) {
	if session.RevokedAt != nil {
		return authRequiredError(), false
	}
	if !now.Before(session.ExpiresAt) || !now.Before(session.IdleExpiresAt) {
		return authRequiredError(), false
	}
	if user.Username == "" {
		return authRequiredError(), false
	}
	if user.Disabled {
		return authForbiddenError(), false
	}
	if user.Role != "admin" {
		return authForbiddenError(), false
	}
	if session.CredentialVersion != user.CredentialVersion {
		return authRequiredError(), false
	}
	return nil, true
}

// 会话/账号行在本包内以最小字段传递,避免把模型细节暴露给 HTTP 层。
type authSessionRow struct {
	ID                int64
	TokenHash         string
	UserID            int64
	CredentialVersion int64
	CSRFToken         string
	CreatedAt         time.Time
	ExpiresAt         time.Time
	IdleExpiresAt     time.Time
	RevokedAt         *time.Time
}

type authUserRow struct {
	ID                int64
	Username          string
	Role              string
	Disabled          bool
	CredentialVersion int64
	PasswordHash      string
}

// --------------------- Cookie ---------------------

// authCookiePath 是部署前缀加 "/"(根部署为 "/")。
func authCookiePath(basePath string) string {
	trimmed := strings.Trim(strings.TrimSpace(basePath), "/")
	if trimmed == "" {
		return "/"
	}
	return "/" + trimmed + "/"
}

// BuildSessionCookie 生成登录/续期用的 Cookie;属性与删除时完全一致。
func BuildSessionCookie(settings AuthSettings, token string, expiresAt time.Time, now time.Time) *http.Cookie {
	maxAge := int(expiresAt.Sub(now).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	return &http.Cookie{
		Name:     settings.CookieName,
		Value:    token,
		Path:     authCookiePath(settings.BasePath),
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   settings.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

// BuildExpiredSessionCookie 生成删除 Cookie:名称、路径与安全属性必须与设置时相同。
func BuildExpiredSessionCookie(settings AuthSettings) *http.Cookie {
	return &http.Cookie{
		Name:     settings.CookieName,
		Value:    "",
		Path:     authCookiePath(settings.BasePath),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   settings.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

// NormalizeTrustedOrigins 校验可信来源:必须是精确 Origin(含协议、主机、端口,不含路径与通配符)。
func NormalizeTrustedOrigins(origins []string) ([]string, error) {
	normalized := make([]string, 0, len(origins))
	for _, raw := range origins {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if strings.Contains(value, "*") {
			return nil, errors.New("trusted origin must not contain wildcards")
		}
		parsed, err := parseOrigin(value)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, parsed)
	}
	return normalized, nil
}

// parseOrigin 只接受 scheme://host[:port] 形式。
func parseOrigin(value string) (string, error) {
	lower := strings.ToLower(value)
	schemeEnd := strings.Index(lower, "://")
	if schemeEnd <= 0 {
		return "", errors.New("trusted origin must include scheme")
	}
	scheme := lower[:schemeEnd]
	if scheme != "http" && scheme != "https" {
		return "", errors.New("trusted origin scheme must be http or https")
	}
	rest := value[schemeEnd+3:]
	if rest == "" || strings.ContainsAny(rest, "/?#") {
		return "", errors.New("trusted origin must not contain path, query or fragment")
	}
	return scheme + "://" + strings.ToLower(rest), nil
}

// OriginAllowed 判定请求 Origin 是否可信;空 Origin(非浏览器脚本)由调用方按各接口规则处理。
func (s *Service) OriginAllowed(origin string) bool {
	value := strings.TrimSpace(origin)
	if value == "" {
		return false
	}
	normalized, err := parseOrigin(value)
	if err != nil {
		return false
	}
	settings := s.AuthSettings()
	for _, allowed := range settings.TrustedOrigins {
		if strings.EqualFold(allowed, normalized) {
			return true
		}
	}
	return false
}

// AuthSettings 返回当前会话/ Cookie 参数;未配置时给出安全默认值。
func (s *Service) AuthSettings() AuthSettings {
	if s.auth != nil {
		return *s.auth
	}
	return AuthSettings{
		CookieName:      "rb_admin_session",
		CookieSecure:    true,
		IdleTimeout:     2 * time.Hour,
		AbsoluteTimeout: 8 * time.Hour,
		BasePath:        s.basePath,
	}
}

// SetAuthSettings 在启动装配时注入会话参数。
func (s *Service) SetAuthSettings(settings AuthSettings) {
	s.auth = &settings
}

// EnsureAuthSessionsReady 在启动时按有界批次清理过期与已撤销会话;不为每个请求启动协程。
func (s *Service) EnsureAuthSessionsReady(ctx context.Context) error {
	now := s.now()
	_, err := s.logic.authDAO.DeleteSessionsBefore(ctx, s.logic.db, now, authSessionCleanupBatch)
	if err != nil && !authDBUnavailable(err) {
		return err
	}
	return nil
}

// loginLimiter 惰性初始化登录限流器:测试可直接构造 Service 字面量。
func (s *Service) loginLimiter() *authRateLimiter {
	if s.loginRate == nil {
		s.loginRate = newAuthRateLimiter(s.clock)
	}
	return s.loginRate
}

// passwordLimiter 惰性初始化密码计算并发限制。
func (s *Service) passwordLimiter() *authComputeLimiter {
	if s.passwordOps == nil {
		s.passwordOps = newAuthComputeLimiter(authPasswordConcurrent)
	}
	return s.passwordOps
}

// SetAuthTimeouts 覆盖空闲/绝对期限(仅测试使用;生产由配置注入)。
func (s *Service) SetAuthTimeouts(idle, absolute time.Duration) {
	settings := s.AuthSettings()
	settings.IdleTimeout = idle
	settings.AbsoluteTimeout = absolute
	s.SetAuthSettings(settings)
}

// SetAuthClockForTest 替换时间来源(仅测试使用,不改变生产行为)。
func (s *Service) SetAuthClockForTest(clock func() time.Time) {
	s.clock = clock
	if s.loginRate != nil {
		s.loginRate.now = func() time.Time { return s.now() }
	}
}

// now 是时间来源,测试可替换为可控时钟。
func (s *Service) now() time.Time {
	if s.clock != nil {
		return s.clock().UTC()
	}
	return time.Now().UTC()
}