// runtime 配置接口的薄适配层:数据来自生效或指定版本快照,保留旧成功字段并补充快照元数据。
// 快照选择、校验和渲染复用发布服务。
package runtime

import (
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/service"
)

// 静态导出口只跟随当前生效版本,显式传版本选择参数按请求错误拒绝,避免静默误读。
var reservedSelectionParams = []string{"version", "expected_snapshot_id", "content_format"}

func rejectedSelectionParam(c *app.RequestContext) string {
	for _, name := range reservedSelectionParams {
		if c.QueryArgs().Has(name) {
			return name
		}
	}
	return ""
}

// runtimeContentMode 解析 config 读取的内容格式:缺省沿用旧 config 契约,显式取值与 release 同规则。
func runtimeContentMode(c *app.RequestContext) (service.RuntimeContentMode, bool) {
	if !c.QueryArgs().Has("content_format") {
		return service.RuntimeContentLegacy, true
	}
	mode, err := service.ParseRuntimeContentFormat(c.Query("content_format"))
	if err != nil {
		writeRuntimeServiceError(c, err)
		return service.RuntimeContentLegacy, false
	}
	return mode, true
}

// legacyRuntimeScopeID 保留旧接口对非法 scope_id 返回 400 的约定。
func legacyRuntimeScopeID(c *app.RequestContext) (string, bool) {
	scopeID := strings.TrimSpace(c.Query("scope_id"))
	if scopeID == "" {
		return "", true
	}
	if _, err := uuid.Parse(scopeID); err != nil {
		writeRuntimeErrorJSON(c, consts.StatusBadRequest, service.ErrTypeScopeInvalidRequest, "scope_id must be a UUID")
		return "", false
	}
	return scopeID, true
}

type activeRuntimeConfigJSON struct {
	ResourceKey    string      `json:"resource_key"`
	Alias          string      `json:"alias"`
	Name           string      `json:"name"`
	EnvironmentKey string      `json:"environment_key"`
	PipelineKey    string      `json:"pipeline_key"`
	ScopeID        string      `json:"scope_id"`
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
	ScopeID         string `json:"scope_id"`
}

type activeRuntimeDataJSON struct {
	SnapshotID           string                       `json:"snapshot_id"`
	Version              string                       `json:"version"`
	SourceFreezeRevision string                       `json:"source_freeze_revision"`
	ActiveRevision       string                       `json:"active_revision"`
	PayloadSha256        string                       `json:"payload_sha256"`
	SelectionMode        string                       `json:"selection_mode"`
	ScopeID              string                       `json:"scope_id"`
	Environment          activeRuntimeEnvironmentJSON `json:"environment"`
	Configs              []activeRuntimeConfigJSON    `json:"configs"`
}

type activeRuntimeResponseJSON struct {
	Code  int32                 `json:"code"`
	Msg   string                `json:"msg"`
	Error string                `json:"error"`
	Data  activeRuntimeDataJSON `json:"data"`
}

// writeRuntimeConfig 输出旧字段 + 快照元数据,并带上与正文一致的版本响应头。
func writeRuntimeConfig(c *app.RequestContext, data *service.ReleaseRuntimeConfig, contentMode service.RuntimeContentMode) {
	configs := make([]activeRuntimeConfigJSON, 0, len(data.Configs))
	for _, cfg := range data.Configs {
		configs = append(configs, activeRuntimeConfigJSON{
			ResourceKey:    cfg.ResourceKey,
			Alias:          cfg.Alias,
			Name:           cfg.Name,
			EnvironmentKey: data.EnvironmentKey,
			PipelineKey:    data.PipelineKey,
			ScopeID:        data.ScopeID,
			Content:        service.RenderRuntimeContent(cfg.Content, cfg.Type, contentMode),
			Type:           cfg.Type,
			Remark:         cfg.Remark,
			IsPerm:         cfg.IsPerm,
			Description:    cfg.Description,
		})
	}
	activeRevision := ""
	if data.ActiveRevision != nil {
		activeRevision = strconv.FormatInt(*data.ActiveRevision, 10)
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
			SourceFreezeRevision: strconv.FormatInt(data.SourceFreezeRevision, 10),
			ActiveRevision:       activeRevision,
			PayloadSha256:        data.PayloadSha256,
			SelectionMode:        data.SelectionMode,
			ScopeID:              data.ScopeID,
			Environment: activeRuntimeEnvironmentJSON{
				EnvironmentKey:  data.EnvironmentKey,
				EnvironmentName: data.EnvironmentName,
				PipelineKey:     data.PipelineKey,
				PipelineName:    data.PipelineName,
				ScopeID:         data.ScopeID,
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
	if scopeErr, ok := service.AsScopeError(err); ok {
		status = scopeErr.HTTPStatus
		errorType = scopeErr.ErrorType
	} else if releaseErr, ok := service.AsReleaseError(err); ok {
		status = releaseErr.HTTPStatus
		errorType = releaseErr.ErrorType
	}
	writeRuntimeErrorJSON(c, status, errorType, err.Error())
}
