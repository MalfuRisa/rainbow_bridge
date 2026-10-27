package release

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/yi-nology/rainbow_bridge/biz/handler"
	"github.com/yi-nology/rainbow_bridge/biz/model/release"
	"github.com/yi-nology/rainbow_bridge/biz/service"
)

const (
	compareModeActive = "active"
	compareModeFixed  = "fixed"
)

// Compare compares snapshots in active mode (default) or between two fixed
// versions. Fixed mode never reads the head row to choose either side.
// @router /api/v1/release/compare [POST]
func Compare(ctx context.Context, c *app.RequestContext) {
	c.Header("Cache-Control", "no-store")
	req := &release.ReleaseCompareRequest{}
	body := c.Request.Body()
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(body, &fields); err != nil {
		writeCompareBadRequest(c, "invalid compare request")
		return
	}
	if err := json.Unmarshal(body, req); err != nil {
		writeCompareBadRequest(c, "invalid compare request")
		return
	}
	mode, err := compareModeFromRequest(fields, req.GetComparisonMode())
	if err != nil {
		writeCompareError(c, err)
		return
	}
	if err := validateCompareFieldCombination(fields, mode); err != nil {
		writeCompareError(c, err)
		return
	}
	result, err := svc.CompareRelease(handler.EnrichContext(ctx, c), service.ReleaseCompareInput{
		EnvironmentKey:             req.GetEnvironmentKey(),
		PipelineKey:                req.GetPipelineKey(),
		ExpectedScopeID:            req.GetExpectedScopeId(),
		ComparisonMode:             mode,
		Version:                    req.GetVersion(),
		ExpectedSnapshotID:         req.GetExpectedSnapshotId(),
		BaselineVersion:            req.GetBaselineVersion(),
		ExpectedBaselineSnapshotID: req.GetExpectedBaselineSnapshotId(),
		ExpectedActiveRevision:     req.GetExpectedActiveRevision(),
		ExpectedActiveSnapshotID:   req.GetExpectedActiveSnapshotId(),
		Offset:                     int(req.GetOffset()),
		Limit:                      int(req.GetLimit()),
	})
	if err != nil {
		writeCompareError(c, err)
		return
	}
	c.JSON(consts.StatusOK, releaseEnvelopeJSON{
		Code: consts.StatusOK,
		Msg:  "OK",
		Data: compareDataJSONFrom(result),
	})
}

func writeCompareBadRequest(c *app.RequestContext, message string) {
	c.JSON(consts.StatusBadRequest, releaseCompareErrorJSON{
		Code: consts.StatusBadRequest, Msg: "error", Error: message,
		ErrorType: service.ErrTypeReleaseInvalidRequest,
	})
}

func compareModeFromRequest(fields map[string]json.RawMessage, rawMode string) (string, error) {
	raw, present := fields["comparison_mode"]
	if !present {
		return compareModeActive, nil
	}
	var mode string
	if err := json.Unmarshal(raw, &mode); err != nil {
		return "", service.ReleaseInvalidRequestError("comparison_mode must be active or fixed")
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return "", service.ReleaseInvalidRequestError("comparison_mode must be active or fixed")
	}
	return mode, nil
}

func validateCompareFieldCombination(fields map[string]json.RawMessage, mode string) error {
	_, hasActiveRevision := fields["expected_active_revision"]
	_, hasActiveSnapshot := fields["expected_active_snapshot_id"]
	_, hasBaselineVersion := fields["baseline_version"]
	_, hasBaselineSnapshot := fields["expected_baseline_snapshot_id"]
	if mode == compareModeFixed {
		if hasActiveRevision || hasActiveSnapshot {
			return service.ReleaseInvalidRequestError(
				"expected_active_revision and expected_active_snapshot_id are only valid in active mode")
		}
		return nil
	}
	if mode == compareModeActive {
		if hasBaselineVersion || hasBaselineSnapshot {
			return service.ReleaseInvalidRequestError(
				"baseline_version and expected_baseline_snapshot_id are only valid in fixed mode")
		}
	}
	return nil
}

func writeCompareError(c *app.RequestContext, err error) {
	status, code, errorType, message := releaseErrorParts(err)
	response := releaseCompareErrorJSON{
		Code: code, Msg: "error", Error: message, ErrorType: errorType,
	}
	if releaseErr, ok := service.AsReleaseError(err); ok {
		response.LatestActive = activeJSONFrom(releaseErr.LatestActive)
		response.Side = releaseErr.Side
	}
	c.JSON(status, response)
}

type releaseCompareVersionJSON struct {
	Version    string `json:"version"`
	SnapshotID string `json:"snapshot_id"`
}

type releaseCompareSummaryJSON struct {
	Added     int32 `json:"added"`
	Modified  int32 `json:"modified"`
	Removed   int32 `json:"removed"`
	Unchanged int32 `json:"unchanged"`
}

type releaseCompareSideJSON struct {
	Alias string `json:"alias"`
	Name  string `json:"name"`
	Type  string `json:"type"`
}

type releaseCompareItemJSON struct {
	ResourceKey   string                  `json:"resource_key"`
	ChangeType    string                  `json:"change_type"`
	ChangedFields []string                `json:"changed_fields"`
	Before        *releaseCompareSideJSON `json:"before"`
	After         *releaseCompareSideJSON `json:"after"`
}

type releaseCompareDataJSON struct {
	ScopeID        string                     `json:"scope_id"`
	ComparisonMode string                     `json:"comparison_mode"`
	Target         releaseCompareVersionJSON  `json:"target"`
	Baseline       *releaseCompareVersionJSON `json:"baseline"`
	ActiveRevision *string                    `json:"active_revision"`
	TargetIsActive *bool                      `json:"target_is_active"`
	HasChanges     bool                       `json:"has_changes"`
	Summary        releaseCompareSummaryJSON  `json:"summary"`
	Items          []releaseCompareItemJSON   `json:"items"`
	TotalChanges   int32                      `json:"total_changes"`
	Offset         int32                      `json:"offset"`
	Limit          int32                      `json:"limit"`
	HasMore        bool                       `json:"has_more"`
	NextOffset     *int32                     `json:"next_offset"`
}

type releaseCompareErrorJSON struct {
	Code         int32              `json:"code"`
	Msg          string             `json:"msg"`
	Error        string             `json:"error"`
	ErrorType    string             `json:"error_type"`
	LatestActive *releaseActiveJSON `json:"latest_active"`
	Side         string             `json:"side,omitempty"`
}

func compareDataJSONFrom(result *service.ReleaseCompareResult) releaseCompareDataJSON {
	baseline := (*releaseCompareVersionJSON)(nil)
	if result.Baseline != nil {
		baseline = &releaseCompareVersionJSON{
			Version: result.Baseline.Version, SnapshotID: result.Baseline.SnapshotID,
		}
	}
	items := make([]releaseCompareItemJSON, 0, len(result.Items))
	for _, item := range result.Items {
		changed := item.ChangedFields
		if changed == nil {
			changed = []string{}
		}
		items = append(items, releaseCompareItemJSON{
			ResourceKey: item.ResourceKey, ChangeType: item.ChangeType,
			ChangedFields: changed,
			Before:        compareSideJSONFrom(item.Before),
			After:         compareSideJSONFrom(item.After),
		})
	}
	var nextOffset *int32
	if result.NextOffset != nil {
		value := int32(*result.NextOffset)
		nextOffset = &value
	}
	mode := result.ComparisonMode
	if mode == "" {
		mode = compareModeActive
	}
	var activeRevision *string
	var targetIsActive *bool
	if mode == compareModeActive {
		revision := strconv.FormatInt(result.ActiveRevision, 10)
		isActive := result.TargetIsActive
		activeRevision = &revision
		targetIsActive = &isActive
	}
	return releaseCompareDataJSON{
		ScopeID:        result.ScopeID,
		ComparisonMode: mode,
		Target: releaseCompareVersionJSON{
			Version: result.Target.Version, SnapshotID: result.Target.SnapshotID,
		},
		Baseline:       baseline,
		ActiveRevision: activeRevision,
		TargetIsActive: targetIsActive,
		HasChanges:     result.TotalChanges > 0,
		Summary: releaseCompareSummaryJSON{
			Added: int32(result.Summary.Added), Modified: int32(result.Summary.Modified),
			Removed: int32(result.Summary.Removed), Unchanged: int32(result.Summary.Unchanged),
		},
		Items:        items,
		TotalChanges: int32(result.TotalChanges),
		Offset:       int32(result.Offset), Limit: int32(result.Limit),
		HasMore: result.HasMore, NextOffset: nextOffset,
	}
}

func compareSideJSONFrom(side *service.ReleaseCompareSide) *releaseCompareSideJSON {
	if side == nil {
		return nil
	}
	return &releaseCompareSideJSON{Alias: side.Alias, Name: side.Name, Type: side.Type}
}
