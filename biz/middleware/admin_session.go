package middleware

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/yi-nology/rainbow_bridge/biz/service"
)

// 管理鉴权:默认拒绝,公开范围按"方法 + 路由"精确匹配。
// 身份只来自服务端会话;请求自报头(X-User-Id/role)、旧 JWT 上下文与旧管理 Bearer 都不参与判定。
// 以后替换为外部 IAM 时,只需替换这里的身份来源(service.SessionAuthenticator)。

// SessionAuthenticator 是中间件依赖的唯一身份入口。
type SessionAuthenticator interface {
	// AdminIdentityForRequest 校验会话、来源与 CSRF,并在成功时滑动空闲期限。
	AdminIdentityForRequest(ctx context.Context, input service.AuthRequestInput, requireCSRF bool) (service.AdminIdentity, error)
	AuthSettings() service.AuthSettings
}

// AdminSession 返回统一管理鉴权中间件。
func AdminSession(auth SessionAuthenticator, basePath string) app.HandlerFunc {
	prefix := strings.Trim(normalizeBasePath(basePath), "/")
	return func(ctx context.Context, c *app.RequestContext) {
		method := string(c.Request.Method())
		path := routedPath(c, prefix)
		if method == consts.MethodOptions || IsPublicRequest(method, path) || isAuthEndpoint(method, path) {
			c.Next(ctx)
			return
		}
		if !strings.HasPrefix(path, "/api/") {
			// SPA 页面与静态资源公开:登录页必须能加载,数据访问再由 API 鉴权
			c.Next(ctx)
			return
		}
		// 未注册的 API 交给原有 404 处理。SPA 的根通配路由不是管理接口。
		fullPath := c.FullPath()
		if fullPath == "" || fullPath == "/*filepath" || (prefix != "" && fullPath == "/"+prefix+"/*filepath") {
			c.Next(ctx)
			return
		}
		settings := auth.AuthSettings()
		cookieValue := c.Cookie(settings.CookieName)
		origin, hasOrigin := requestOriginValue(c)
		identity, err := auth.AdminIdentityForRequest(ctx, service.AuthRequestInput{
			SessionToken: string(cookieValue),
			CSRFToken:    strings.TrimSpace(string(c.GetHeader("X-CSRF-Token"))),
			Origin:       origin,
			HasOrigin:    hasOrigin,
		}, requiresCSRF(method))
		if err != nil {
			writeAdminAuthError(c, err)
			return
		}
		ctx = service.WithAdminIdentity(ctx, identity)
		c.Next(ctx)
	}
}

// IsPublicRequest 只列业务公开读取。auth 虽由自身处理身份,仍需要管理来源的 CORS。
func IsPublicRequest(method, path string) bool {
	if method != consts.MethodGet {
		return false
	}
	switch path {
	case "/ping", "/api/v1/version", "/api/v1/version/latest",
		"/api/v1/runtime/overview", "/api/v1/runtime/config", "/api/v1/runtime/static",
		"/api/v1/runtime/release", "/api/v1/runtime/release/static":
		return true
	}
	return strings.HasPrefix(path, "/api/v1/asset/file/") || strings.HasPrefix(path, "/api/v1/release/asset/")
}

func isAuthEndpoint(method, path string) bool {
	if method == consts.MethodGet {
		return path == "/api/v1/auth/me"
	}
	if method == consts.MethodPost {
		return path == "/api/v1/auth/login" || path == "/api/v1/auth/logout" || path == "/api/v1/auth/password"
	}
	return false
}

// requiresCSRF 判定该方法是否属于会改变状态的管理操作。
func requiresCSRF(method string) bool {
	switch method {
	case consts.MethodGet, consts.MethodHead, consts.MethodOptions:
		return false
	default:
		return true
	}
}

// routedPath 去掉部署前缀,得到路由使用的路径。
func routedPath(c *app.RequestContext, basePath string) string {
	path := string(c.Request.URI().Path())
	if basePath == "" {
		return path
	}
	prefix := "/" + basePath
	if path != prefix && !strings.HasPrefix(path, prefix+"/") {
		return path
	}
	trimmed := strings.TrimPrefix(path, prefix)
	if trimmed == "" {
		return "/"
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	return trimmed
}

func normalizeBasePath(basePath string) string {
	value := strings.TrimSpace(basePath)
	return strings.TrimRight(value, "/")
}

func requestOriginValue(c *app.RequestContext) (string, bool) {
	raw := c.Request.Header.Peek("Origin")
	if len(raw) == 0 {
		return "", false
	}
	return strings.TrimSpace(string(raw)), true
}

// writeAdminAuthError 输出与 auth 接口一致的错误信封(真实 HTTP 状态 + error_type)。
func writeAdminAuthError(c *app.RequestContext, err error) {
	authErr, ok := service.AsAuthError(err)
	if !ok {
		authErr = service.AuthUnavailableError(err)
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(authErr.HTTPStatus, map[string]any{
		"code":       authErr.Code,
		"msg":        authErr.Error(),
		"error_type": authErr.ErrorType,
	})
	c.Abort()
}
