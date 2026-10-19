package transfer

import (
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/yi-nology/rainbow_bridge/biz/handler"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
)

// The generated DTO omits zero totals and empty items. Keep the same fields
// while explicitly representing a successful import that skipped every item.
func respondImportedConfigs(c *app.RequestContext, configs []*common.ResourceConfig) {
	summary := handler.BuildConfigSummary(configs)
	c.JSON(consts.StatusOK, map[string]any{
		"code": consts.StatusOK, "msg": "OK",
		"data": map[string]any{
			"total": summary.Total, "environment_keys": summary.EnvironmentKeys,
			"pipeline_keys": summary.PipelineKeys, "items": convertToImportSummaryItems(summary.Items),
		},
	})
}
