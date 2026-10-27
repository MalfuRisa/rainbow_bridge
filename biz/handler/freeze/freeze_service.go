// 手写 handler:绑定、错误映射与 pb 转换。生成的路由指向本文件的四个入口。
package freeze

import (
	"context"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/yi-nology/rainbow_bridge/biz/handler"
	"github.com/yi-nology/rainbow_bridge/biz/model/freeze"
	"github.com/yi-nology/rainbow_bridge/biz/service"
)

var svc *service.Service

func SetService(s *service.Service) {
	svc = s
}

// Status .
// @router /api/v1/freeze/status [GET]
func Status(ctx context.Context, c *app.RequestContext) {
	if err := svc.ValidateReadScope(handler.EnrichContext(ctx, c), c.Query("environment_key"), c.Query("pipeline_key"), c.Query("scope_id")); err != nil {
		writeStatusError(c, err)
		return
	}
	data, err := svc.FreezeStatus(handler.EnrichContext(ctx, c), c.Query("environment_key"), c.Query("pipeline_key"))
	if err != nil {
		writeStatusError(c, err)
		return
	}
	c.JSON(consts.StatusOK, &freeze.FreezeStatusResponse{
		Code: consts.StatusOK,
		Msg:  "OK",
		Data: statusToPB(data),
	})
}

// Lock .
// @router /api/v1/freeze/lock [POST]
func Lock(ctx context.Context, c *app.RequestContext) {
	req := &freeze.LockFreezeRequest{}
	if err := c.BindJSON(req); err != nil {
		c.JSON(consts.StatusBadRequest, &freeze.FreezeTransitionResponse{
			Code:      consts.StatusBadRequest,
			Msg:       "error",
			Error:     err.Error(),
			ErrorType: service.ErrTypeFreezeInvalidRequest,
		})
		return
	}
	if strings.TrimSpace(req.GetExpectedScopeId()) == "" {
		c.JSON(400, &freeze.FreezeTransitionResponse{Code: 400, Msg: "error", Error: "expected_scope_id is required", ErrorType: service.ErrTypeScopeInvalidRequest})
		return
	}
	data, err := svc.LockFreezeWithIdentity(handler.EnrichContext(ctx, c), req.GetEnvironmentKey(), req.GetPipelineKey(), req.GetReason(), req.GetExpectedRevision(), req.GetExpectedScopeId())
	if err != nil {
		writeTransitionError(c, err)
		return
	}
	c.JSON(consts.StatusOK, &freeze.FreezeTransitionResponse{
		Code: consts.StatusOK,
		Msg:  "OK",
		Data: statusToPB(data),
	})
}

// Unlock .
// @router /api/v1/freeze/unlock [POST]
func Unlock(ctx context.Context, c *app.RequestContext) {
	req := &freeze.UnlockFreezeRequest{}
	if err := c.BindJSON(req); err != nil {
		c.JSON(consts.StatusBadRequest, &freeze.FreezeTransitionResponse{
			Code:      consts.StatusBadRequest,
			Msg:       "error",
			Error:     err.Error(),
			ErrorType: service.ErrTypeFreezeInvalidRequest,
		})
		return
	}
	if strings.TrimSpace(req.GetExpectedScopeId()) == "" {
		c.JSON(400, &freeze.FreezeTransitionResponse{Code: 400, Msg: "error", Error: "expected_scope_id is required", ErrorType: service.ErrTypeScopeInvalidRequest})
		return
	}
	data, err := svc.UnlockFreezeWithIdentity(handler.EnrichContext(ctx, c), req.GetEnvironmentKey(), req.GetPipelineKey(), req.GetReason(), req.GetExpectedRevision(), req.GetExpectedScopeId())
	if err != nil {
		writeTransitionError(c, err)
		return
	}
	c.JSON(consts.StatusOK, &freeze.FreezeTransitionResponse{
		Code: consts.StatusOK,
		Msg:  "OK",
		Data: statusToPB(data),
	})
}

// History .
// @router /api/v1/freeze/history [GET]
func History(ctx context.Context, c *app.RequestContext) {
	limit := 0
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			c.JSON(consts.StatusBadRequest, &freeze.FreezeHistoryResponse{
				Code:      consts.StatusBadRequest,
				Msg:       "error",
				Error:     "limit must be an integer",
				ErrorType: service.ErrTypeFreezeInvalidRequest,
			})
			return
		}
		limit = parsed
	}
	if err := svc.ValidateReadScope(handler.EnrichContext(ctx, c), c.Query("environment_key"), c.Query("pipeline_key"), c.Query("scope_id")); err != nil {
		writeHistoryError(c, err)
		return
	}
	data, err := svc.FreezeHistory(handler.EnrichContext(ctx, c), c.Query("environment_key"), c.Query("pipeline_key"), limit, c.Query("before_event_id"))
	if err != nil {
		writeHistoryError(c, err)
		return
	}
	items := make([]*freeze.FreezeEventItem, 0, len(data.Items))
	for _, item := range data.Items {
		items = append(items, &freeze.FreezeEventItem{
			ScopeId:   item.ScopeID,
			EventId:   strconv.FormatUint(uint64(item.EventID), 10),
			EventType: item.EventType,
			FromState: item.FromState,
			ToState:   item.ToState,
			Revision:  strconv.FormatInt(item.Revision, 10),
			Reason:    item.Reason,
			Actor:     item.Actor,
			CreatedAt: item.CreatedAt,
		})
	}
	nextBefore := ""
	if data.NextBeforeEventID > 0 {
		nextBefore = strconv.FormatUint(uint64(data.NextBeforeEventID), 10)
	}
	c.JSON(consts.StatusOK, &freeze.FreezeHistoryResponse{
		Code: consts.StatusOK,
		Msg:  "OK",
		Data: &freeze.FreezeHistoryData{
			Items:             items,
			HasMore:           data.HasMore,
			NextBeforeEventId: nextBefore,
		},
	})
}

// --------------------- 错误映射 ---------------------

func writeStatusError(c *app.RequestContext, err error) {
	if scopeErr, ok := service.AsScopeError(err); ok {
		c.JSON(scopeErr.HTTPStatus, &freeze.FreezeStatusResponse{Code: int32(scopeErr.Code), Msg: "error", Error: scopeErr.Message, ErrorType: scopeErr.ErrorType})
		return
	}
	freezeErr, ok := service.AsFreezeError(err)
	if !ok {
		c.JSON(consts.StatusInternalServerError, &freeze.FreezeStatusResponse{Code: consts.StatusInternalServerError, Msg: "error", Error: err.Error(), ErrorType: service.ErrTypeFreezeInternal})
		return
	}
	c.JSON(freezeErr.HTTPStatus, &freeze.FreezeStatusResponse{
		Code:      int32(freezeErr.Code),
		Msg:       "error",
		Error:     freezeErr.Message,
		ErrorType: freezeErr.ErrorType,
	})
}

func writeTransitionError(c *app.RequestContext, err error) {
	if scopeErr, ok := service.AsScopeError(err); ok {
		c.JSON(scopeErr.HTTPStatus, &freeze.FreezeTransitionResponse{Code: int32(scopeErr.Code), Msg: "error", Error: scopeErr.Message, ErrorType: scopeErr.ErrorType})
		return
	}
	freezeErr, ok := service.AsFreezeError(err)
	if !ok {
		c.JSON(consts.StatusInternalServerError, &freeze.FreezeTransitionResponse{Code: consts.StatusInternalServerError, Msg: "error", Error: err.Error(), ErrorType: service.ErrTypeFreezeInternal})
		return
	}
	blocked := make([]*freeze.FreezeBlockedScope, 0, len(freezeErr.BlockedScopes))
	for _, scope := range freezeErr.BlockedScopes {
		blocked = append(blocked, &freeze.FreezeBlockedScope{
			ScopeId:        scope.ScopeID,
			EnvironmentKey: scope.EnvironmentKey,
			PipelineKey:    scope.PipelineKey,
			State:          scope.State,
			Revision:       strconv.FormatInt(scope.Revision, 10),
		})
	}
	issues := make([]*freeze.FreezeResourceIssue, 0, len(freezeErr.Issues))
	for _, issue := range freezeErr.Issues {
		issues = append(issues, &freeze.FreezeResourceIssue{
			ResourceKey: issue.ResourceKey,
			Alias:       issue.Alias,
			FileId:      issue.FileID,
			Reason:      issue.Reason,
		})
	}
	latestRevision := ""
	if freezeErr.LatestState != "" {
		latestRevision = strconv.FormatInt(freezeErr.LatestRevision, 10)
	}
	c.JSON(freezeErr.HTTPStatus, &freeze.FreezeTransitionResponse{
		Code:               int32(freezeErr.Code),
		Msg:                "error",
		Error:              freezeErr.Message,
		ErrorType:          freezeErr.ErrorType,
		BlockedScopes:      blocked,
		ResourceIssues:     issues,
		ExternalReferences: freezeErr.ExternalReferences,
		LatestState:        freezeErr.LatestState,
		LatestRevision:     latestRevision,
	})
}

func writeHistoryError(c *app.RequestContext, err error) {
	if scopeErr, ok := service.AsScopeError(err); ok {
		c.JSON(scopeErr.HTTPStatus, &freeze.FreezeHistoryResponse{Code: int32(scopeErr.Code), Msg: "error", Error: scopeErr.Message, ErrorType: scopeErr.ErrorType})
		return
	}
	freezeErr, ok := service.AsFreezeError(err)
	if !ok {
		c.JSON(consts.StatusInternalServerError, &freeze.FreezeHistoryResponse{Code: consts.StatusInternalServerError, Msg: "error", Error: err.Error(), ErrorType: service.ErrTypeFreezeInternal})
		return
	}
	c.JSON(freezeErr.HTTPStatus, &freeze.FreezeHistoryResponse{
		Code:      int32(freezeErr.Code),
		Msg:       "error",
		Error:     freezeErr.Message,
		ErrorType: freezeErr.ErrorType,
	})
}

// --------------------- pb 转换 ---------------------

func statusToPB(data *service.FreezeStatusData) *freeze.FreezeStatusData {
	if data == nil {
		return nil
	}
	return &freeze.FreezeStatusData{
		ScopeId:         data.ScopeID,
		EnvironmentKey:  data.EnvironmentKey,
		PipelineKey:     data.PipelineKey,
		State:           data.State,
		Revision:        strconv.FormatInt(data.Revision, 10),
		LastChangedAt:   data.LastChangedAt,
		LastChangedBy:   data.LastChangedBy,
		LastReason:      data.LastReason,
		AdminConfigured: data.AdminConfigured,
	}
}
