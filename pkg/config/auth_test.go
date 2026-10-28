package config

import (
	"strings"
	"testing"
)

func insecureAuthConfig(serverAddress string, insecureLocal, insecureRemote bool) *Config {
	cookieSecureFalse := false
	return &Config{
		Server: ServerConfig{Address: serverAddress},
		Auth: AuthConfig{
			CookieName:          "rb_admin_session",
			CookieSecure:        &cookieSecureFalse,
			AllowInsecureLocal:  insecureLocal,
			AllowInsecureRemote: insecureRemote,
		},
	}
}

func TestValidateAuthConfigSecureCookieDefault(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{Address: ":8080"},
		Auth:   AuthConfig{CookieName: "rb_admin_session"},
	}
	origins, err := ValidateAuthConfig(cfg)
	if err != nil {
		t.Fatalf("default secure cookie should validate, got error: %v", err)
	}
	if len(origins) != 0 {
		t.Fatalf("expected empty origins, got %v", origins)
	}
}

func TestCookieSecureFalseRequiresInsecureFlag(t *testing.T) {
	cfg := insecureAuthConfig(":8080", false, false)
	_, err := ValidateAuthConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "allow_insecure") {
		t.Fatalf("expected flag requirement error, got: %v", err)
	}
}

func TestCookieSecureFalseLoopbackAllowed(t *testing.T) {
	cfg := insecureAuthConfig("127.0.0.1:8080", true, false)
	if _, err := ValidateAuthConfig(cfg); err != nil {
		t.Fatalf("loopback insecure config should validate, got error: %v", err)
	}
}

func TestCookieSecureFalseRemoteBindRejectedWithLocalFlag(t *testing.T) {
	cfg := insecureAuthConfig(":8080", true, false)
	_, err := ValidateAuthConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("allow_insecure_localhost must stay loopback-only, got error: %v", err)
	}
}

func TestAllowInsecureRemotePermitsAnyBind(t *testing.T) {
	cfg := insecureAuthConfig(":8080", false, true)
	if _, err := ValidateAuthConfig(cfg); err != nil {
		t.Fatalf("allow_insecure_remote should permit non-loopback binds, got error: %v", err)
	}
}
