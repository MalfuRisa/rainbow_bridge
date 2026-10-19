package handler

import (
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/yi-nology/rainbow_bridge/biz/service"
)

func WriteExportError(c *app.RequestContext, err error) {
	status := 500
	response := map[string]any{"code": status, "msg": "error", "error": "export failed"}
	var incomplete *service.IncompleteExportError
	if errors.As(err, &incomplete) {
		status = 409
		response["code"] = status
		response["error"] = incomplete.Error()
		response["issues"] = incomplete.Issues
	}
	c.JSON(status, response)
}
