package release

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/yi-nology/rainbow_bridge/biz/handler"
	"github.com/yi-nology/rainbow_bridge/biz/model/release"
	"github.com/yi-nology/rainbow_bridge/biz/service"
)

// Delete logically removes an inactive version; the global session middleware protects it.
// @router /api/v1/release/delete [POST]
func Delete(ctx context.Context, c *app.RequestContext) {
	c.Header("Cache-Control", "no-store")
	req := &release.ReleaseDeleteRequest{}
	if err := c.BindJSON(req); err != nil {
		c.JSON(consts.StatusBadRequest, releaseErrorJSON{
			Code: 400, Msg: "error", Error: "invalid delete request", ErrorType: service.ErrTypeReleaseInvalidRequest,
		})
		return
	}
	result, err := svc.DeleteRelease(handler.EnrichContext(ctx, c), service.ReleaseDeleteInput{
		EnvironmentKey: req.GetEnvironmentKey(), PipelineKey: req.GetPipelineKey(), Version: req.GetVersion(),
		ExpectedSnapshotID: req.GetExpectedSnapshotId(), RequestID: req.GetRequestId(), Reason: req.GetReason(),
	})
	if err != nil {
		status, code, errorType, message := releaseErrorParts(err)
		c.JSON(status, releaseErrorJSON{Code: code, Msg: "error", Error: message, ErrorType: errorType})
		return
	}
	c.JSON(consts.StatusOK, releaseEnvelopeJSON{Code: 200, Msg: "OK", Data: result})
}
