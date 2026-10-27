package service

import (
	"crypto/subtle"
	"os"
	"strings"
)

// 管理凭据只从进程环境读取,不落库、不入日志、不进任何响应体。
const (
	FreezeAdminTokenEnv      = "RAINBOW_FREEZE_ADMIN_TOKEN"
	freezeAdminMinTokenBytes = 32
	bearerTokenPrefix        = "Bearer "
)

func freezeAdminToken() string {
	return os.Getenv(FreezeAdminTokenEnv)
}

// freezeAdminConfigured 表示服务端是否配置了可用的管理令牌。
func freezeAdminConfigured() bool {
	return len(freezeAdminToken()) >= freezeAdminMinTokenBytes
}

// verifyFreezeAdminCredential 校验 Authorization 头中的管理令牌。
// 常量时间比较;错误凭据返回 401,未配置返回 503。
func verifyFreezeAdminCredential(authorization string) error {
	if !freezeAdminConfigured() {
		return freezeAdminNotConfiguredError()
	}
	if !strings.HasPrefix(authorization, bearerTokenPrefix) {
		return freezeUnauthorizedError()
	}
	presented := strings.TrimPrefix(authorization, bearerTokenPrefix)
	expected := freezeAdminToken()
	if subtle.ConstantTimeCompare([]byte(presented), []byte(expected)) != 1 {
		return freezeUnauthorizedError()
	}
	return nil
}
