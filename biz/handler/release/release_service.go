// 手写 handler:绑定、错误映射与 pb 转换。生成的路由指向本文件的入口。
package release

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/yi-nology/rainbow_bridge/biz/handler"
	"github.com/yi-nology/rainbow_bridge/biz/model/release"
	"github.com/yi-nology/rainbow_bridge/biz/service"
)

var svc *service.Service

func SetService(s *service.Service) {
	svc = s
}

// --------------------- 创建 ---------------------

// Create .
// @router /api/v1/release/create [POST]
func Create(ctx context.Context, c *app.RequestContext) {
	req := &release.ReleaseCreateRequest{}
	if err := c.BindJSON(req); err != nil {
		c.JSON(consts.StatusBadRequest, &release.ReleaseCreateResponse{
			Code: consts.StatusBadRequest, Msg: "error", Error: err.Error(), ErrorType: service.ErrTypeReleaseInvalidRequest,
		})
		return
	}
	result, err := svc.CreateRelease(handler.EnrichContext(ctx, c), service.ReleaseCreateInput{
		EnvironmentKey:            req.GetEnvironmentKey(),
		PipelineKey:               req.GetPipelineKey(),
		Version:                   req.GetVersion(),
		ExpectedFreezeRevisionRaw: req.GetExpectedFreezeRevision(),
		RequestID:                 req.GetRequestId(),
		Reason:                    req.GetReason(),
	})
	if err != nil {
		writeCreateError(c, err)
		return
	}
	c.JSON(consts.StatusOK, releaseEnvelopeJSON{
		Code: consts.StatusOK,
		Msg:  "OK",
		Data: releaseCreateDataJSON{Meta: metaJSONFrom(result.Meta), Replayed: result.Replayed},
	})
}

// --------------------- 列表 / 详情 / active / 历史 ---------------------

// List .
// @router /api/v1/release/list [GET]
func List(ctx context.Context, c *app.RequestContext) {
	limit := 0
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			c.JSON(consts.StatusBadRequest, &release.ReleaseListResponse{Code: consts.StatusBadRequest, Msg: "error", Error: "limit must be an integer", ErrorType: service.ErrTypeReleaseInvalidRequest})
			return
		}
		limit = parsed
	}
	page, err := svc.ReleaseVersions(handler.EnrichContext(ctx, c), c.Query("environment_key"), c.Query("pipeline_key"), limit, c.Query("before_id"))
	if err != nil {
		writeListError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	items := make([]releaseListItemJSON, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, releaseListItemJSON{Meta: metaJSONFrom(item.Meta), IsActive: item.IsActive})
	}
	nextBefore := ""
	if page.NextBeforeID > 0 {
		nextBefore = strconv.FormatUint(uint64(page.NextBeforeID), 10)
	}
	c.JSON(consts.StatusOK, releaseEnvelopeJSON{
		Code: consts.StatusOK,
		Msg:  "OK",
		Data: releaseListDataJSON{Items: items, HasMore: page.HasMore, NextBeforeID: nextBefore, Active: activeJSONFrom(page.Active)},
	})
}

// Detail .
// @router /api/v1/release/detail [GET]
func Detail(ctx context.Context, c *app.RequestContext) {
	detail, err := svc.ReleaseDetail(handler.EnrichContext(ctx, c), c.Query("environment_key"), c.Query("pipeline_key"), c.Query("version"))
	if err != nil {
		writeDetailError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	configs := make([]releaseDetailConfigJSON, 0, len(detail.Configs))
	for _, cfg := range detail.Configs {
		configs = append(configs, releaseDetailConfigJSON{
			ResourceKey: cfg.ResourceKey, Alias: cfg.Alias, Name: cfg.Name, Type: cfg.Type,
			Content: cfg.Content, Remark: cfg.Remark, IsPerm: cfg.IsPerm, Description: cfg.Description,
		})
	}
	assets := make([]releaseDetailAssetJSON, 0, len(detail.Assets))
	for _, asset := range detail.Assets {
		assets = append(assets, releaseDetailAssetJSON{
			AssetID: asset.AssetID, FileName: asset.FileName, ContentType: asset.ContentType,
			Size: asset.Size, Sha256: asset.Sha256, URL: asset.URL,
		})
	}
	c.JSON(consts.StatusOK, releaseEnvelopeJSON{
		Code: consts.StatusOK,
		Msg:  "OK",
		Data: releaseDetailDataJSON{Meta: metaJSONFrom(detail.Meta), Configs: configs, Assets: assets, Active: activeJSONFrom(detail.Active)},
	})
}

// Active .
// @router /api/v1/release/active [GET]
func Active(ctx context.Context, c *app.RequestContext) {
	active, err := svc.ReleaseActive(handler.EnrichContext(ctx, c), c.Query("environment_key"), c.Query("pipeline_key"))
	if err != nil {
		writeActiveError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(consts.StatusOK, releaseEnvelopeJSON{
		Code: consts.StatusOK,
		Msg:  "OK",
		Data: releaseActiveDataJSON{Active: activeJSONFrom(active)},
	})
}

// Activate .
// @router /api/v1/release/activate [POST]
func Activate(ctx context.Context, c *app.RequestContext) {
	req := &release.ReleaseActivateRequest{}
	if err := c.BindJSON(req); err != nil {
		c.JSON(consts.StatusBadRequest, &release.ReleaseActivateResponse{
			Code: consts.StatusBadRequest, Msg: "error", Error: err.Error(), ErrorType: service.ErrTypeReleaseInvalidRequest,
		})
		return
	}
	result, err := svc.ActivateRelease(handler.EnrichContext(ctx, c), service.ReleaseActivateInput{
		EnvironmentKey:            req.GetEnvironmentKey(),
		PipelineKey:               req.GetPipelineKey(),
		Version:                   req.GetVersion(),
		ExpectedActiveRevisionRaw: req.GetExpectedActiveRevision(),
		RequestID:                 req.GetRequestId(),
		Reason:                    req.GetReason(),
	})
	if err != nil {
		writeActivateError(c, err)
		return
	}
	c.JSON(consts.StatusOK, releaseEnvelopeJSON{
		Code: consts.StatusOK,
		Msg:  "OK",
		Data: releaseActivateDataJSON{
			Operation: releaseOperationJSON{
				RequestID:       result.RequestID,
				Replayed:        result.Replayed,
				AppliedRevision: strconv.FormatInt(result.AppliedRevision, 10),
			},
			Active:  activeJSONFrom(result.Active),
			Version: result.Version,
		},
	})
}

// History .
// @router /api/v1/release/history [GET]
func History(ctx context.Context, c *app.RequestContext) {
	limit := 0
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			c.JSON(consts.StatusBadRequest, &release.ReleaseHistoryResponse{Code: consts.StatusBadRequest, Msg: "error", Error: "limit must be an integer", ErrorType: service.ErrTypeReleaseInvalidRequest})
			return
		}
		limit = parsed
	}
	page, err := svc.ReleaseHistoryPage(handler.EnrichContext(ctx, c), c.Query("environment_key"), c.Query("pipeline_key"), limit, c.Query("before_event_id"))
	if err != nil {
		writeHistoryError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	items := make([]releaseEventItemJSON, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, releaseEventItemJSON{
			EventID:            strconv.FormatUint(uint64(item.EventID), 10),
			EventType:          item.EventType,
			RequestID:          item.RequestID,
			SnapshotID:         item.SnapshotID,
			Version:            item.Version,
			FromActiveSnapshot: item.FromActiveID,
			ToActiveSnapshot:   item.ToActiveID,
			FromHeadRevision:   strconv.FormatInt(item.FromRevision, 10),
			ToHeadRevision:     strconv.FormatInt(item.ToRevision, 10),
			Reason:             item.Reason,
			Actor:              item.Actor,
			CreatedAt:          item.CreatedAt,
		})
	}
	nextBefore := ""
	if page.NextBeforeEventID > 0 {
		nextBefore = strconv.FormatUint(uint64(page.NextBeforeEventID), 10)
	}
	c.JSON(consts.StatusOK, releaseEnvelopeJSON{
		Code: consts.StatusOK,
		Msg:  "OK",
		Data: releaseHistoryDataJSON{Items: items, HasMore: page.HasMore, NextBeforeEventID: nextBefore},
	})
}

// --------------------- 版本 runtime 与资源下载 ---------------------

// RuntimeRelease .
// @router /api/v1/runtime/release [GET]
func RuntimeRelease(ctx context.Context, c *app.RequestContext) {
	environmentKey := strings.TrimSpace(string(c.GetHeader("x-environment")))
	pipelineKey := strings.TrimSpace(string(c.GetHeader("x-pipeline")))
	var versionParam *string
	if c.QueryArgs().Has("version") {
		value := c.Query("version")
		versionParam = &value
	}
	rawContent, err := resolveReleaseContentFormat(c)
	if err != nil {
		writeRuntimeError(c, err)
		return
	}
	data, err := svc.RuntimeRelease(handler.EnrichContext(ctx, c), environmentKey, pipelineKey, versionParam)
	if err != nil {
		writeRuntimeError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(consts.StatusOK, &runtimeReleaseResponseJSON{
		Code: consts.StatusOK,
		Msg:  "OK",
		Data: runtimeDataJSON(data, rawContent),
	})
}

// resolveReleaseContentFormat 解析 content_format:缺失或 json 保持原契约,raw 输出字符串原文。
// 显式空值与其他取值按 400 拒绝(不能用缺省值掩盖调用方参数错误)。
func resolveReleaseContentFormat(c *app.RequestContext) (bool, error) {
	if !c.QueryArgs().Has("content_format") {
		return false, nil
	}
	switch strings.TrimSpace(c.Query("content_format")) {
	case "json":
		return false, nil
	case "raw":
		return true, nil
	default:
		return false, service.ReleaseInvalidRequestError("content_format must be json or raw")
	}
}

// RuntimeReleaseStatic .
// @router /api/v1/runtime/release/static [GET]
func RuntimeReleaseStatic(ctx context.Context, c *app.RequestContext) {
	environmentKey := strings.TrimSpace(string(c.GetHeader("x-environment")))
	pipelineKey := strings.TrimSpace(string(c.GetHeader("x-pipeline")))
	result, err := svc.ReleaseStaticExport(handler.EnrichContext(ctx, c), service.ReleaseStaticExportInput{
		EnvironmentKey:     environmentKey,
		PipelineKey:        pipelineKey,
		Version:            c.Query("version"),
		ExpectedSnapshotID: c.Query("expected_snapshot_id"),
	})
	if err != nil {
		writeStaticExportError(c, err)
		return
	}
	// 临时包在整次响应(含流式发送)结束后清理
	defer os.Remove(result.TempPath)
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Type", "application/zip")
	c.Header("X-Rainbow-Snapshot-Id", result.SnapshotID)
	c.Header("X-Rainbow-Version", result.Version)
	c.FileAttachment(result.TempPath, result.FileName)
}

// Asset .
// @router /api/v1/release/asset/*filepath [GET]
func Asset(ctx context.Context, c *app.RequestContext) {
	wildcard := strings.TrimPrefix(c.Param("filepath"), "/")
	parts := strings.Split(wildcard, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		c.JSON(consts.StatusNotFound, &release.ReleaseAssetResponse{
			Code: consts.StatusNotFound, Msg: "error", Error: "release asset not found", ErrorType: service.ErrTypeReleaseAssetNotFound,
		})
		return
	}
	contentType, payload, err := svc.ReleaseAssetDownload(handler.EnrichContext(ctx, c), parts[0], parts[1])
	if err != nil {
		writeAssetError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(consts.StatusOK, contentType, payload)
}

// --------------------- 错误映射 ---------------------

func writeCreateError(c *app.RequestContext, err error) {
	if releaseErr, ok := service.AsReleaseError(err); ok {
		issues := make([]releaseResourceIssueJSON, 0, len(releaseErr.Issues))
		for _, issue := range releaseErr.Issues {
			issues = append(issues, releaseResourceIssueJSON{
				ResourceKey: issue.ResourceKey, Alias: issue.Alias, FileID: issue.FileID, Reason: issue.Reason,
			})
		}
		response := releaseCreateErrorJSON{
			Code: int32(releaseErr.Code), Msg: "error", Error: releaseErr.Message, ErrorType: releaseErr.ErrorType,
			ResourceIssues: issues,
		}
		if releaseErr.Limits != nil {
			response.Limits = &releaseLimitJSON{Item: releaseErr.Limits.Item, Limit: releaseErr.Limits.Limit, Actual: releaseErr.Limits.Actual}
		}
		c.JSON(releaseErr.HTTPStatus, response)
		return
	}
	if freezeErr, ok := service.AsFreezeError(err); ok {
		c.JSON(freezeErr.HTTPStatus, releaseErrorJSON{Code: int32(freezeErr.Code), Msg: "error", Error: freezeErr.Message, ErrorType: freezeErr.ErrorType})
		return
	}
	c.JSON(consts.StatusInternalServerError, releaseErrorJSON{Code: consts.StatusInternalServerError, Msg: "error", Error: err.Error(), ErrorType: service.ErrTypeReleaseInternalError})
}

func writeListError(c *app.RequestContext, err error) {
	status, code, errorType, message := releaseErrorParts(err)
	c.JSON(status, &release.ReleaseListResponse{Code: code, Msg: "error", Error: message, ErrorType: errorType})
}

func writeDetailError(c *app.RequestContext, err error) {
	status, code, errorType, message := releaseErrorParts(err)
	c.JSON(status, &release.ReleaseDetailResponse{Code: code, Msg: "error", Error: message, ErrorType: errorType})
}

func writeActiveError(c *app.RequestContext, err error) {
	status, code, errorType, message := releaseErrorParts(err)
	c.JSON(status, &release.ReleaseActiveResponse{Code: code, Msg: "error", Error: message, ErrorType: errorType})
}

func writeActivateError(c *app.RequestContext, err error) {
	if releaseErr, ok := service.AsReleaseError(err); ok {
		c.JSON(releaseErr.HTTPStatus, releaseActivateErrorJSON{
			Code: int32(releaseErr.Code), Msg: "error", Error: releaseErr.Message, ErrorType: releaseErr.ErrorType,
			LatestActive: activeJSONFrom(releaseErr.LatestActive),
		})
		return
	}
	if freezeErr, ok := service.AsFreezeError(err); ok {
		c.JSON(freezeErr.HTTPStatus, releaseErrorJSON{Code: int32(freezeErr.Code), Msg: "error", Error: freezeErr.Message, ErrorType: freezeErr.ErrorType})
		return
	}
	c.JSON(consts.StatusInternalServerError, releaseErrorJSON{Code: consts.StatusInternalServerError, Msg: "error", Error: err.Error(), ErrorType: service.ErrTypeReleaseInternalError})
}

func writeHistoryError(c *app.RequestContext, err error) {
	status, code, errorType, message := releaseErrorParts(err)
	c.JSON(status, &release.ReleaseHistoryResponse{Code: code, Msg: "error", Error: message, ErrorType: errorType})
}

func writeRuntimeError(c *app.RequestContext, err error) {
	status, code, errorType, message := releaseErrorParts(err)
	c.JSON(status, &release.RuntimeReleaseResponse{Code: code, Msg: "error", Error: message, ErrorType: errorType})
}

// writeStaticExportError 版本静态导出的错误信封:与读取共用同一映射(含 409/503/413/500)。
func writeStaticExportError(c *app.RequestContext, err error) {
	status, code, errorType, message := releaseErrorParts(err)
	c.JSON(status, releaseErrorJSON{Code: code, Msg: "error", Error: message, ErrorType: errorType})
}

func writeAssetError(c *app.RequestContext, err error) {
	status, code, errorType, message := releaseErrorParts(err)
	c.JSON(status, &release.ReleaseAssetResponse{Code: code, Msg: "error", Error: message, ErrorType: errorType})
}

// releaseErrorParts 提取发布错误的 HTTP 映射;兼容写协调的冻结类错误。
func releaseErrorParts(err error) (int, int32, string, string) {
	if releaseErr, ok := service.AsReleaseError(err); ok {
		return releaseErr.HTTPStatus, int32(releaseErr.Code), releaseErr.ErrorType, releaseErr.Message
	}
	if freezeErr, ok := service.AsFreezeError(err); ok {
		return freezeErr.HTTPStatus, int32(freezeErr.Code), freezeErr.ErrorType, freezeErr.Message
	}
	return consts.StatusInternalServerError, consts.StatusInternalServerError, service.ErrTypeReleaseInternalError, err.Error()
}

// --------------------- 显式 JSON 契约 ---------------------
//
// 生成模型的数组/计数带 omitempty,空值会被省略,前端按字段必然存在读取会崩溃;
// 因此成功响应统一改用下列显式结构:数组始终输出(空为 []),计数与字符串始终输出。

type releaseEnvelopeJSON struct {
	Code int32       `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data"`
}

type releaseErrorJSON struct {
	Code      int32  `json:"code"`
	Msg       string `json:"msg"`
	Error     string `json:"error"`
	ErrorType string `json:"error_type"`
}

type releaseResourceIssueJSON struct {
	ResourceKey string `json:"resource_key"`
	Alias       string `json:"alias"`
	FileID      string `json:"file_id"`
	Reason      string `json:"reason"`
}

type releaseLimitJSON struct {
	Item   string `json:"item"`
	Limit  int64  `json:"limit"`
	Actual int64  `json:"actual"`
}

type releaseCreateErrorJSON struct {
	Code           int32                      `json:"code"`
	Msg            string                     `json:"msg"`
	Error          string                     `json:"error"`
	ErrorType      string                     `json:"error_type"`
	ResourceIssues []releaseResourceIssueJSON `json:"resource_issues"`
	Limits         *releaseLimitJSON          `json:"limits,omitempty"`
}

type releaseActivateErrorJSON struct {
	Code         int32              `json:"code"`
	Msg          string             `json:"msg"`
	Error        string             `json:"error"`
	ErrorType    string             `json:"error_type"`
	LatestActive *releaseActiveJSON `json:"latest_active,omitempty"`
}

type releaseMetaJSON struct {
	SnapshotID           string   `json:"snapshot_id"`
	EnvironmentKey       string   `json:"environment_key"`
	PipelineKey          string   `json:"pipeline_key"`
	Version              string   `json:"version"`
	EnvironmentName      string   `json:"environment_name"`
	PipelineName         string   `json:"pipeline_name"`
	SourceFreezeRevision string   `json:"source_freeze_revision"`
	CreationRequestID    string   `json:"creation_request_id"`
	ConfigCount          int      `json:"config_count"`
	AssetCount           int      `json:"asset_count"`
	AssetBytes           int64    `json:"asset_bytes"`
	PayloadSha256        string   `json:"payload_sha256"`
	ManifestSha256       string   `json:"manifest_sha256"`
	ExternalReferences   []string `json:"external_references"`
	Reason               string   `json:"reason"`
	Actor                string   `json:"actor"`
	CreatedAt            string   `json:"created_at"`
}

type releaseActiveJSON struct {
	SnapshotID    string `json:"snapshot_id"`
	Version       string `json:"version"`
	Revision      string `json:"revision"`
	ChangedAt     string `json:"changed_at"`
	ChangedBy     string `json:"changed_by"`
	ChangedReason string `json:"changed_reason"`
}

type releaseCreateDataJSON struct {
	Meta     releaseMetaJSON `json:"meta"`
	Replayed bool            `json:"replayed"`
}

type releaseListItemJSON struct {
	Meta     releaseMetaJSON `json:"meta"`
	IsActive bool            `json:"is_active"`
}

type releaseListDataJSON struct {
	Items        []releaseListItemJSON `json:"items"`
	HasMore      bool                  `json:"has_more"`
	NextBeforeID string                `json:"next_before_id"`
	Active       *releaseActiveJSON    `json:"active"`
}

type releaseDetailConfigJSON struct {
	ResourceKey string `json:"resource_key"`
	Alias       string `json:"alias"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Content     string `json:"content"`
	Remark      string `json:"remark"`
	IsPerm      bool   `json:"is_perm"`
	Description string `json:"description"`
}

type releaseDetailAssetJSON struct {
	AssetID     string `json:"asset_id"`
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Sha256      string `json:"sha256"`
	URL         string `json:"url"`
}

type releaseDetailDataJSON struct {
	Meta    releaseMetaJSON           `json:"meta"`
	Configs []releaseDetailConfigJSON `json:"configs"`
	Assets  []releaseDetailAssetJSON  `json:"assets"`
	Active  *releaseActiveJSON        `json:"active"`
}

type releaseActiveDataJSON struct {
	Active *releaseActiveJSON `json:"active"`
}

type releaseOperationJSON struct {
	RequestID       string `json:"request_id"`
	Replayed        bool   `json:"replayed"`
	AppliedRevision string `json:"applied_revision"`
}

type releaseActivateDataJSON struct {
	Operation releaseOperationJSON `json:"operation"`
	Active    *releaseActiveJSON   `json:"active"`
	Version   string               `json:"version"`
}

type releaseEventItemJSON struct {
	EventID            string `json:"event_id"`
	EventType          string `json:"event_type"`
	RequestID          string `json:"request_id"`
	SnapshotID         string `json:"snapshot_id"`
	Version            string `json:"version"`
	FromActiveSnapshot string `json:"from_active_snapshot_id"`
	ToActiveSnapshot   string `json:"to_active_snapshot_id"`
	FromHeadRevision   string `json:"from_head_revision"`
	ToHeadRevision     string `json:"to_head_revision"`
	Reason             string `json:"reason"`
	Actor              string `json:"actor"`
	CreatedAt          string `json:"created_at"`
}

type releaseHistoryDataJSON struct {
	Items             []releaseEventItemJSON `json:"items"`
	HasMore           bool                   `json:"has_more"`
	NextBeforeEventID string                 `json:"next_before_event_id"`
}

func metaJSONFrom(meta service.ReleaseVersionMeta) releaseMetaJSON {
	refs := meta.ExternalReferences
	if refs == nil {
		refs = make([]string, 0)
	}
	return releaseMetaJSON{
		SnapshotID:           meta.SnapshotID,
		EnvironmentKey:       meta.EnvironmentKey,
		PipelineKey:          meta.PipelineKey,
		Version:              meta.Version,
		EnvironmentName:      meta.EnvironmentName,
		PipelineName:         meta.PipelineName,
		SourceFreezeRevision: strconv.FormatInt(meta.SourceFreezeRevision, 10),
		CreationRequestID:    meta.CreationRequestID,
		ConfigCount:          meta.ConfigCount,
		AssetCount:           meta.AssetCount,
		AssetBytes:           meta.AssetBytes,
		PayloadSha256:        meta.PayloadSha256,
		ManifestSha256:       meta.ManifestSha256,
		ExternalReferences:   refs,
		Reason:               meta.Reason,
		Actor:                meta.Actor,
		CreatedAt:            meta.CreatedAt,
	}
}

func activeJSONFrom(active *service.ReleaseActiveInfo) *releaseActiveJSON {
	if active == nil {
		return nil
	}
	return &releaseActiveJSON{
		SnapshotID:    active.SnapshotID,
		Version:       active.Version,
		Revision:      strconv.FormatInt(active.Revision, 10),
		ChangedAt:     active.ChangedAt,
		ChangedBy:     active.ChangedBy,
		ChangedReason: active.ChangedReason,
	}
}

// runtimeConfigJSON 与既有 runtime 一致:object/keyvalue 呈现为 JSON 对象,其他类型为字符串。
type runtimeConfigJSON struct {
	ResourceKey string      `json:"resource_key"`
	Alias       string      `json:"alias"`
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Content     interface{} `json:"content"`
	Remark      string      `json:"remark"`
	IsPerm      bool        `json:"is_perm"`
	Description string      `json:"description"`
}

type runtimeReleaseEnvironmentJSON struct {
	EnvironmentKey  string `json:"environment_key"`
	EnvironmentName string `json:"environment_name"`
	PipelineKey     string `json:"pipeline_key"`
	PipelineName    string `json:"pipeline_name"`
}

type runtimeReleaseDataJSON struct {
	SnapshotID           string                        `json:"snapshot_id"`
	Version              string                        `json:"version"`
	SourceFreezeRevision string                        `json:"source_freeze_revision"`
	PayloadSha256        string                        `json:"payload_sha256"`
	SelectionMode        string                        `json:"selection_mode"`
	ActiveRevision       string                        `json:"active_revision,omitempty"`
	Environment          runtimeReleaseEnvironmentJSON `json:"environment"`
	Configs              []runtimeConfigJSON           `json:"configs"`
}

type runtimeReleaseResponseJSON struct {
	Code int32                  `json:"code"`
	Msg  string                 `json:"msg"`
	Data runtimeReleaseDataJSON `json:"data"`
}

func runtimeDataJSON(data *service.ReleaseRuntimeConfig, rawContent bool) runtimeReleaseDataJSON {
	result := runtimeReleaseDataJSON{
		SnapshotID:           data.SnapshotID,
		Version:              data.Version,
		SourceFreezeRevision: strconv.FormatInt(data.SourceFreezeRevision, 10),
		PayloadSha256:        data.PayloadSha256,
		SelectionMode:        data.SelectionMode,
		Environment: runtimeReleaseEnvironmentJSON{
			EnvironmentKey:  data.EnvironmentKey,
			EnvironmentName: data.EnvironmentName,
			PipelineKey:     data.PipelineKey,
			PipelineName:    data.PipelineName,
		},
		Configs: marshalRuntimeConfigs(data, rawContent),
	}
	if data.ActiveRevision != nil {
		result.ActiveRevision = strconv.FormatInt(*data.ActiveRevision, 10)
	}
	return result
}

// marshalRuntimeConfigs 把 object/keyvalue 的 content 字符串解析为 JSON 对象(与既有 runtime 相同形态)。
// marshalRuntimeConfigs 生成配置列表:默认把 object/keyvalue 解析成 JSON 对象,
// rawContent 时保持服务端的字符串原文(调用方自行决定是否无损解析)。
func marshalRuntimeConfigs(data *service.ReleaseRuntimeConfig, rawContent bool) []runtimeConfigJSON {
	result := make([]runtimeConfigJSON, 0, len(data.Configs))
	for _, cfg := range data.Configs {
		item := runtimeConfigJSON{
			ResourceKey: cfg.ResourceKey, Alias: cfg.Alias, Name: cfg.Name,
			Type: cfg.Type, Remark: cfg.Remark, IsPerm: cfg.IsPerm, Description: cfg.Description,
		}
		if rawContent {
			item.Content = cfg.Content
			result = append(result, item)
			continue
		}
		normalizedType := strings.ToLower(strings.TrimSpace(cfg.Type))
		if normalizedType == "object" || normalizedType == "keyvalue" {
			var parsed interface{}
			if err := service.DecodeConfigJSON([]byte(cfg.Content), &parsed); err == nil {
				item.Content = parsed
			} else {
				item.Content = cfg.Content
			}
		} else {
			item.Content = cfg.Content
		}
		result = append(result, item)
	}
	return result
}
