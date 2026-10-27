package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/yi-nology/rainbow_bridge/biz/backup"
)

// BackupGateProvider is implemented by the service; nil means the feature is
// unavailable in this deployment.
type BackupGateProvider interface {
	BackupManager() *backup.Manager
}

// BackupMaintenance rejects new business writes with 503 while a backup or
// restore preparation owns the maintenance gate. Authentication and the
// backup endpoints themselves stay reachable.
func BackupMaintenance(provider BackupGateProvider) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if !mutatingMethod(string(c.Request.Method())) {
			c.Next(ctx)
			return
		}
		path := string(c.Request.URI().Path())
		if strings.Contains(path, "/api/v1/auth/") || strings.Contains(path, "/api/v1/backup/") ||
			strings.Contains(path, "/api/v1/release/compare") {
			c.Next(ctx)
			return
		}
		manager := provider.BackupManager()
		if manager == nil || !manager.Maintenance().Active() {
			c.Next(ctx)
			return
		}
		c.Header("Retry-After", "5")
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusServiceUnavailable, map[string]any{
			"code":       http.StatusServiceUnavailable,
			"msg":        "business writes are paused for backup maintenance",
			"error_type": backup.ErrMaintenance,
		})
		c.Abort()
	}
}

func mutatingMethod(method string) bool {
	switch method {
	case consts.MethodGet, consts.MethodHead, consts.MethodOptions:
		return false
	default:
		return true
	}
}
