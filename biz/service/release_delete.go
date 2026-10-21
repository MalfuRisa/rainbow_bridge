package service

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

type ReleaseDeleteInput struct {
	EnvironmentKey     string
	PipelineKey        string
	Version            string
	ExpectedSnapshotID string
	RequestID          string
	Reason             string
}

// ReleaseDeleteResult always emits the optional note and replay status.
type ReleaseDeleteResult struct {
	RequestID  string `json:"request_id"`
	SnapshotID string `json:"snapshot_id"`
	Version    string `json:"version"`
	Deleted    bool   `json:"deleted"`
	DeletedAt  string `json:"deleted_at"`
	DeletedBy  string `json:"deleted_by"`
	Reason     string `json:"reason"`
	Replayed   bool   `json:"replayed"`
}

// DeleteRelease serializes with activation and never touches snapshot files.
func (s *Service) DeleteRelease(ctx context.Context, input ReleaseDeleteInput) (*ReleaseDeleteResult, error) {
	env, pipeline, version, reason, requestID, _, err := validateReleaseTransitionInput(
		input.EnvironmentKey, input.PipelineKey, input.Version, "", input.RequestID, input.Reason)
	if err != nil {
		return nil, err
	}
	snapshotID := strings.TrimSpace(input.ExpectedSnapshotID)
	if _, err := uuid.Parse(snapshotID); err != nil {
		return nil, releaseInvalidRequestError("expected_snapshot_id must be a snapshot UUID")
	}
	identity, err := requireAdminIdentity(ctx)
	if err != nil {
		return nil, err
	}
	fingerprint := releaseFingerprint("delete", env, pipeline, version, snapshotID, reason)
	var result *ReleaseDeleteResult
	err = s.withWriteGate(ctx, func(gateCtx context.Context, l *Logic, _ *gorm.DB) error {
		event, err := l.releaseDAO.GetEvent(gateCtx, l.db, model.ReleaseEventDelete, requestID)
		if err != nil {
			return releaseInternalError(err)
		}
		if event != nil {
			if event.RequestFingerprint != fingerprint {
				return releaseRequestConflictError("request_id was already used with different fields")
			}
			result = releaseDeleteResultFrom(event, true)
			return nil
		}
		if err := s.assertReleaseScopeExists(gateCtx, l, env, pipeline); err != nil {
			return err
		}
		target, err := l.releaseDAO.GetVersionByScopeAndName(gateCtx, l.db, env, pipeline, version)
		if err != nil {
			if db.IsReleaseNotFound(err) {
				return releaseVersionNotFoundError(env, pipeline, version)
			}
			return releaseInternalError(err)
		}
		if target.SnapshotID != snapshotID {
			return releaseSnapshotMismatchError(env, pipeline, version, snapshotID, target.SnapshotID)
		}
		if target.DeletedAt != nil {
			return releaseVersionDeletedError()
		}
		head, err := l.releaseDAO.GetHead(gateCtx, l.db, env, pipeline)
		if err != nil {
			return releaseInternalError(err)
		}
		if head != nil && head.ActiveSnapshotID == snapshotID {
			return newReleaseError(409, ErrTypeReleaseDeleteActive, "switch to another version before deleting the active version")
		}
		now := time.Now().UTC()
		marked, err := l.releaseDAO.MarkVersionDeleted(gateCtx, l.db, snapshotID, identity.Username, reason, now)
		if err != nil {
			return releaseInternalError(err)
		}
		if !marked {
			return releaseVersionDeletedError()
		}
		event = &model.ConfigReleaseEvent{
			EventType: model.ReleaseEventDelete, RequestID: requestID, RequestFingerprint: fingerprint,
			EnvironmentKey: env, PipelineKey: pipeline, SnapshotID: snapshotID, Version: version,
			Reason: reason, Actor: identity.Username, CreatedAt: now,
		}
		if head != nil {
			event.FromActiveSnapshot, event.ToActiveSnapshot = head.ActiveSnapshotID, head.ActiveSnapshotID
			event.FromHeadRevision, event.ToHeadRevision = head.Revision, head.Revision
		}
		if err := l.releaseDAO.AppendEvent(gateCtx, l.db, event); err != nil {
			return releaseInternalError(err)
		}
		result = releaseDeleteResultFrom(event, false)
		return nil
	})
	return result, err
}

func releaseDeleteResultFrom(event *model.ConfigReleaseEvent, replayed bool) *ReleaseDeleteResult {
	return &ReleaseDeleteResult{
		RequestID: event.RequestID, SnapshotID: event.SnapshotID, Version: event.Version, Deleted: true,
		DeletedAt: event.CreatedAt.UTC().Format(time.RFC3339), DeletedBy: event.Actor, Reason: event.Reason, Replayed: replayed,
	}
}
