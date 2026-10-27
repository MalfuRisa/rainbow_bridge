package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/yi-nology/rainbow_bridge/biz/backup"
	"github.com/yi-nology/rainbow_bridge/biz/service"
)

// WriteExportError maps transfer export failures to the documented HTTP
// contract. A partially generated archive is never returned.
func WriteExportError(c *app.RequestContext, err error) {
	c.Header("Cache-Control", "no-store")
	if transferErr, ok := service.AsTransferExportError(err); ok {
		c.JSON(transferErr.HTTPStatus, map[string]any{
			"code": transferErr.Code, "msg": "error", "error": transferErr.Message,
			"error_type": transferErr.ErrorType,
		})
		return
	}
	if scopeErr, ok := service.AsScopeError(err); ok {
		c.JSON(scopeErr.HTTPStatus, map[string]any{
			"code": scopeErr.Code, "msg": "error", "error": scopeErr.Message,
			"error_type": scopeErr.ErrorType,
		})
		return
	}
	if releaseErr, ok := service.AsReleaseError(err); ok {
		c.JSON(releaseErr.HTTPStatus, map[string]any{
			"code": releaseErr.Code, "msg": "error", "error": releaseErr.Message,
			"error_type": releaseErr.ErrorType,
		})
		return
	}
	var incomplete *service.IncompleteExportError
	if errors.As(err, &incomplete) {
		c.JSON(http.StatusServiceUnavailable, map[string]any{
			"code": http.StatusServiceUnavailable, "msg": "error",
			"error": incomplete.Error(), "error_type": service.ErrTypeTransferIncomplete,
			"issues": incomplete.Issues,
		})
		return
	}
	if backupErr, ok := backup.AsError(err); ok {
		c.JSON(backupErr.HTTPStatus, map[string]any{
			"code": backupErr.HTTPStatus, "msg": "error", "error": backupErr.Message,
			"error_type": backupErr.Type,
		})
		return
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		c.JSON(http.StatusServiceUnavailable, map[string]any{
			"code": http.StatusServiceUnavailable, "msg": "error",
			"error": "export timed out", "error_type": service.ErrTypeTransferTimeout,
		})
		return
	}
	c.JSON(http.StatusInternalServerError, map[string]any{
		"code": http.StatusInternalServerError, "msg": "error", "error": "export failed",
	})
}
