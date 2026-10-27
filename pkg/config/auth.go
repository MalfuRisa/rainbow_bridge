package config

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// 会话与 Cookie 的默认值:属于本项目易用性取舍,生产建议显式配置。
const (
	DefaultAuthCookieName      = "rb_admin_session"
	DefaultAuthIdleTimeout     = 2 * time.Hour
	DefaultAuthAbsoluteTimeout = 8 * time.Hour
	maxAuthAbsoluteTimeout     = 24 * time.Hour
)

// AuthCookieSecure 解析 Cookie 的 Secure 属性:未配置时默认开启。
func AuthCookieSecure(cfg *Config) bool {
	if cfg.Auth.CookieSecure == nil {
		return true
	}
	return *cfg.Auth.CookieSecure
}

// ApplyAuthDefaults 补齐 auth 段缺省值;不改变已显式配置的值。
func ApplyAuthDefaults(cfg *Config) {
	if cfg.Auth.CookieName == "" {
		cfg.Auth.CookieName = DefaultAuthCookieName
	}
	if cfg.Auth.IdleTimeout == "" {
		cfg.Auth.IdleTimeout = DefaultAuthIdleTimeout.String()
	}
	if cfg.Auth.AbsoluteTimeout == "" {
		cfg.Auth.AbsoluteTimeout = DefaultAuthAbsoluteTimeout.String()
	}
}

// AuthTimeoutPair 解析并校验时长:0 < idle <= absolute <= 24h。
func AuthTimeoutPair(cfg *Config) (time.Duration, time.Duration, error) {
	idle, err := time.ParseDuration(cfg.Auth.IdleTimeout)
	if err != nil {
		return 0, 0, fmt.Errorf("auth.idle_timeout must be a duration: %w", err)
	}
	absolute, err := time.ParseDuration(cfg.Auth.AbsoluteTimeout)
	if err != nil {
		return 0, 0, fmt.Errorf("auth.absolute_timeout must be a duration: %w", err)
	}
	if idle <= 0 {
		return 0, 0, errors.New("auth.idle_timeout must be greater than zero")
	}
	if absolute < idle {
		return 0, 0, errors.New("auth.absolute_timeout must not be shorter than auth.idle_timeout")
	}
	if absolute > maxAuthAbsoluteTimeout {
		return 0, 0, fmt.Errorf("auth.absolute_timeout must not exceed %s", maxAuthAbsoluteTimeout)
	}
	return idle, absolute, nil
}

// ValidateAuthConfig 校验 Cookie 名、可信来源与"非回环 HTTP 必须开启 Secure"规则。
// 返回的 origins 已规范化为精确 Origin。
func ValidateAuthConfig(cfg *Config) ([]string, error) {
	cookieName := strings.TrimSpace(cfg.Auth.CookieName)
	if cookieName == "" {
		return nil, errors.New("auth.cookie_name must not be empty")
	}
	for _, r := range cookieName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return nil, fmt.Errorf("auth.cookie_name contains unsupported character %q", r)
		}
	}
	origins := make([]string, 0, len(cfg.Auth.TrustedOrigins))
	for _, raw := range cfg.Auth.TrustedOrigins {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if strings.Contains(value, "*") {
			return nil, fmt.Errorf("auth.trusted_origins must not contain wildcards: %q", raw)
		}
		normalized, err := normalizeOrigin(value)
		if err != nil {
			return nil, fmt.Errorf("auth.trusted_origins: %w", err)
		}
		origins = append(origins, normalized)
	}
	if !AuthCookieSecure(cfg) && !cfg.Auth.AllowInsecureLocal {
		return nil, errors.New("auth.cookie_secure=false requires auth.allow_insecure_localhost=true")
	}
	if !AuthCookieSecure(cfg) && cfg.Auth.AllowInsecureLocal && !isLoopbackAddress(cfg.Server.Address) {
		return nil, errors.New("insecure auth configuration requires the server to bind a loopback address")
	}
	return origins, nil
}

// isLoopbackAddress 判定监听地址是否只绑定回环。
func isLoopbackAddress(address string) bool {
	value := strings.TrimSpace(address)
	if value == "" {
		return false
	}
	host := value
	if strings.HasPrefix(value, "[") {
		if end := strings.Index(value, "]"); end > 0 {
			host = value[1:end]
		}
	} else if idx := strings.LastIndex(value, ":"); idx > 0 {
		host = value[:idx]
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(host, "localhost")
}

// normalizeOrigin 与 service 层保持同一规则(精确 Origin,含协议与端口)。
func normalizeOrigin(value string) (string, error) {
	lower := strings.ToLower(value)
	schemeEnd := strings.Index(lower, "://")
	if schemeEnd <= 0 {
		return "", fmt.Errorf("origin %q must include scheme", value)
	}
	scheme := lower[:schemeEnd]
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("origin %q scheme must be http or https", value)
	}
	rest := value[schemeEnd+3:]
	if rest == "" || strings.ContainsAny(rest, "/?#") {
		return "", fmt.Errorf("origin %q must not contain path, query or fragment", value)
	}
	return scheme + "://" + strings.ToLower(rest), nil
}