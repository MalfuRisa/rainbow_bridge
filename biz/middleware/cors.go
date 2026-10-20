package middleware

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/yi-nology/rainbow_bridge/pkg/config"
)

// CORS 处理跨域:公开业务接口沿用配置里的来源范围,
// 管理/auth 路由只对精确可信来源返回 ACAO 与凭据,不使用通配符、不反射任意来源。
func CORS(cfg *config.CORSConfig, trustedOrigins []string, basePath string) app.HandlerFunc {
	// Use defaults if config is nil
	allowOrigin := "*"
	allowMethods := "GET,POST,PUT,DELETE,OPTIONS"
	allowHeaders := "*"
	allowCredentials := "false"

	if cfg != nil {
		if cfg.AllowOrigin != "" {
			allowOrigin = cfg.AllowOrigin
		}
		if cfg.AllowMethods != "" {
			allowMethods = cfg.AllowMethods
		}
		if cfg.AllowHeaders != "" {
			allowHeaders = cfg.AllowHeaders
		}
		if cfg.AllowCredentials {
			allowCredentials = "true"
		}
	}

	prefix := strings.Trim(normalizeBasePath(basePath), "/")
	return func(ctx context.Context, c *app.RequestContext) {
		method := string(c.Request.Method())
		path := routedPath(c, prefix)
		origin := strings.TrimSpace(string(c.Request.Header.Peek("Origin")))
		targetMethod := method
		if method == consts.MethodOptions {
			targetMethod = string(c.GetHeader("Access-Control-Request-Method"))
		}
		management := strings.HasPrefix(path, "/api/") && !IsPublicRequest(targetMethod, path)
		if management {
			// 管理与 auth 路由:仅精确匹配的可信来源,且不使用通配符
			c.Response.Header.Add("Vary", "Origin")
			for _, trusted := range trustedOrigins {
				if strings.EqualFold(trusted, origin) {
					c.Response.Header.Set("Access-Control-Allow-Origin", trusted)
					c.Response.Header.Set("Access-Control-Allow-Credentials", "true")
					break
				}
			}
			c.Response.Header.Set("Access-Control-Allow-Methods", allowMethods)
			c.Response.Header.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token, X-Client-Version")
			c.Response.Header.Set("Access-Control-Expose-Headers", "Content-Disposition, X-Rainbow-Snapshot-Id, X-Rainbow-Version")
			if method == consts.MethodOptions {
				c.AbortWithStatus(consts.StatusNoContent)
				return
			}
			c.Next(ctx)
			return
		}
		c.Response.Header.Set("Access-Control-Allow-Origin", allowOrigin)
		c.Response.Header.Set("Access-Control-Allow-Methods", allowMethods)
		c.Response.Header.Set("Access-Control-Allow-Headers", allowHeaders)
		c.Response.Header.Set("Access-Control-Allow-Credentials", allowCredentials)
		// 版本静态导出需要跨域读取这两项版本校验头与下载文件名;仅暴露响应头,不放宽来源
		c.Response.Header.Set("Access-Control-Expose-Headers", "Content-Disposition, X-Rainbow-Snapshot-Id, X-Rainbow-Version")

		// Handle preflight requests
		if string(c.Request.Method()) == consts.MethodOptions {
			c.AbortWithStatus(consts.StatusNoContent)
			return
		}

		c.Next(ctx)
	}
}
