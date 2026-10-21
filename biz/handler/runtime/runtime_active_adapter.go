// 旧 runtime 配置接口的薄适配层:数据来自当前生效快照,保留旧成功字段并补充快照元数据。
// 不复制发布读取逻辑:选择、校验与渲染都由 service.RuntimeRelease 完成。
package runtime

import (
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/yi-nology/rainbow_bridge/biz/service"
)

// 保留选择参数:旧入口只跟随当前生效版本,显式传版本选择参数按请求错误拒绝,避免静默误读。
var reservedSelectionParams = []string{"version", "expected_snapshot_id", "content_format"}

func rejectedSelectionParam(c *app.RequestContext) string {
	for _, name := range reservedSelectionParams {
		if c.QueryArgs().Has(name) {
			return name
		}
	}
	return ""
}

type activeRuntimeConfigJSON struct {
	ResourceKey    string      `json:"resource_key"`
	Alias          string      `json:"alias"`
	Name           string      `json:"name"`
	EnvironmentKey string      `json:"environment_key"`
	PipelineKey    string      `json:"pipeline_key"`
	Content        interface{} `json:"content"`
	Type           string      `json:"type"`
	Remark         string      `json:"remark"`
	IsPerm         bool        `json:"is_perm"`
	Description    string      `json:"description"`
}

type activeRuntimeEnvironmentJSON struct {
	EnvironmentKey  string `json:"environment_key"`
	EnvironmentName string `json:"environment_name"`
	PipelineKey     string `json:"pipeline_key"`
	PipelineName    string `json:"pipeline_name"`
}

type activeRuntimeDataJSON struct {
	SnapshotID           string                       `json:"snapshot_id"`
	Version              string                       `json:"version"`
	SourceFreezeRevision string                       `json:"source_freeze_revision"`
	ActiveRevision       string                       `json:"active_revision"`
	PayloadSha256        string                       `json:"payload_sha256"`
	SelectionMode        string                       `json:"selection_mode"`
	Environment          activeRuntimeEnvironmentJSON `json:"environment"`
	Configs              []activeRuntimeConfigJSON    `json:"configs"`
}

type activeRuntimeResponseJSON struct {
	Code  int32                 `json:"code"`
	Msg   string                `json:"msg"`
	Error string                `json:"error"`
	Data  activeRuntimeDataJSON `json:"data"`
}

// runtimeConfigContent:object/keyvalue 按发布读取规则解码为原生 JSON,其余保持字符串。
func strconvFormatInt(value int64) string { return strconv.FormatInt(value, 10) }

func runtimeConfigContent(cfg service.ReleaseRuntimeConfigView) interface{} {
	normalized := strings.ToLower(strings.TrimSpace(cfg.Type))
	if normalized == "object" || normalized == "keyvalue" {
		var parsed interface{}
		if err := service.DecodeConfigJSON([]byte(cfg.Content), &parsed); err == nil {
			return parsed
		}
	}
	return cfg.Content
}

// writeActiveRuntimeConfig 输出旧字段 + 快照元数据,并带上与正文一致的版本响应头。
func writeActiveRuntimeConfig(c *app.RequestContext, data *service.ReleaseRuntimeConfig) {
	configs := make([]activeRuntimeConfigJSON, 0, len(data.Configs))
	for _, cfg := range data.Configs {
		configs = append(configs, activeRuntimeConfigJSON{
			ResourceKey:    cfg.ResourceKey,
			Alias:          cfg.Alias,
			Name:           cfg.Name,
			EnvironmentKey: data.EnvironmentKey,
			PipelineKey:    data.PipelineKey,
			Content:        runtimeConfigContent(service.ReleaseRuntimeConfigView(cfg)),
			Type:           cfg.Type,
			Remark:         cfg.Remark,
			IsPerm:         cfg.IsPerm,
			Description:    cfg.Description,
		})
	}
	activeRevision := ""
	if data.ActiveRevision != nil {
		activeRevision = strconvFormatInt(*data.ActiveRevision)
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Rainbow-Snapshot-Id", data.SnapshotID)
	c.Header("X-Rainbow-Version", data.Version)
	c.JSON(consts.StatusOK, activeRuntimeResponseJSON{
		Code:  consts.StatusOK,
		Msg:   "OK",
		Error: "",
		Data: activeRuntimeDataJSON{
			SnapshotID:           data.SnapshotID,
			Version:              data.Version,
			SourceFreezeRevision: strconvFormatInt(data.SourceFreezeRevision),
			ActiveRevision:       activeRevision,
			PayloadSha256:        data.PayloadSha256,
			SelectionMode:        data.SelectionMode,
			Environment: activeRuntimeEnvironmentJSON{
				EnvironmentKey:  data.EnvironmentKey,
				EnvironmentName: data.EnvironmentName,
				PipelineKey:     data.PipelineKey,
				PipelineName:    data.PipelineName,
			},
			Configs: configs,
		},
	})
}

// writeRuntimeErrorJSON 统一错误信封:真实 HTTP 状态 + error_type + no-store。
func writeRuntimeErrorJSON(c *app.RequestContext, status int, errorType, message string) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, map[string]interface{}{
		"code":       status,
		"msg":        "error",
		"error":      message,
		"error_type": errorType,
	})
}

// writeRuntimeServiceError 复用发布错误映射(404/503/413 等真实状态)。
func writeRuntimeServiceError(c *app.RequestContext, err error) {
	status := consts.StatusInternalServerError
	errorType := service.ErrTypeReleaseInternalError
	if releaseErr, ok := service.AsReleaseError(err); ok {
		status = releaseErr.HTTPStatus
		errorType = releaseErr.ErrorType
	}
	writeRuntimeErrorJSON(c, status, errorType, err.Error())
}
