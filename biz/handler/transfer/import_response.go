package transfer

import (
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/yi-nology/rainbow_bridge/biz/handler"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	"github.com/yi-nology/rainbow_bridge/biz/model/transfer"
)

// The generated DTO omits zero totals and empty items. Keep the same fields
// while explicitly representing a successful import that skipped every item.
func respondImportedConfigs(c *app.RequestContext, configs []*common.ResourceConfig, results ...*transfer.ResourceImportResult) {
	summary := handler.BuildConfigSummary(configs)
	result := &transfer.ResourceImportResult{Status: "not_applicable"}
	if len(results) > 0 && results[0] != nil {
		result = results[0]
	}
	issues := result.Issues
	if issues == nil {
		issues = []*transfer.ResourceImportIssue{}
	}
	msg := "OK"
	if result.Status == "partial" {
		msg = "partial"
	}
	c.JSON(consts.StatusOK, map[string]any{
		"code": consts.StatusOK, "msg": msg,
		"data": map[string]any{
			"resource_import": map[string]any{"status": result.Status, "expected": result.Expected, "restored": result.Restored, "reused": result.Reused, "failed": result.Failed, "issues": issues},
			"total":           summary.Total, "environment_keys": summary.EnvironmentKeys,
			"pipeline_keys": summary.PipelineKeys, "items": convertToImportSummaryItems(summary.Items),
		},
	})
}
