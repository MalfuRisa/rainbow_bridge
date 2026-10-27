package service

import (
	"context"
	"strings"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
)

// ReleaseActiveState pairs the captured scope revision with an optional active
// pointer. A non-zero revision with nil Active is a supported boundary state.
type ReleaseActiveState struct {
	ScopeID  string
	Revision int64
	Active   *ReleaseActiveInfo
}

// ReadReleaseActiveState validates the optional scope pin and reads scope/head/
// active metadata in one SELECT. It never creates or repairs a head row.
func (s *Service) ReadReleaseActiveState(ctx context.Context,
	environmentKey, pipelineKey, requestedScopeID string) (*ReleaseActiveState, error) {
	environmentKey = strings.TrimSpace(environmentKey)
	pipelineKey = strings.TrimSpace(pipelineKey)
	row, err := s.logic.releaseDAO.ReadActiveStateSnapshot(ctx, s.logic.db,
		environmentKey, pipelineKey, requestedScopeID)
	if err != nil {
		return nil, releaseInternalError(err)
	}

	if requestedScopeID != "" {
		if !row.PinnedPresent {
			return nil, scopeNotFoundError("scope not found")
		}
		if !row.PinnedActive {
			return nil, scopeDeletedError("scope has been deleted")
		}
	}
	state, err := releaseActiveStateFromRow(row, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	if requestedScopeID != "" && state.ScopeID != requestedScopeID {
		return nil, scopeIdentityConflict("scope_id does not identify the live generation")
	}
	return state, nil
}

func releaseActiveStateFromRow(row *db.ReleaseActiveStateRow,
	environmentKey, pipelineKey string) (*ReleaseActiveState, error) {
	if !row.CurrentPresent {
		// Only a never-initialized database keeps the synthetic legacy scope.
		if row.TotalEnvironment == 0 && row.TotalPipeline == 0 {
			return &ReleaseActiveState{
				ScopeID:  db.LegacyScopeID(environmentKey, pipelineKey),
				Revision: 0,
			}, nil
		}
		if row.KeyEnvironmentCount == 0 || row.KeyPipelineCount == 0 {
			return nil, scopeNotFoundError("scope " + environmentKey + "/" + pipelineKey + " not found")
		}
		return nil, scopeDeletedError("scope " + environmentKey + "/" + pipelineKey + " has been deleted")
	}

	state := &ReleaseActiveState{ScopeID: row.CurrentScopeID, Revision: 0}
	if !row.HeadPresent {
		return state, nil
	}
	if row.HeadRevision < 0 {
		return nil, releaseIncompleteError("active head revision is invalid")
	}
	state.Revision = row.HeadRevision
	if row.HeadActiveSnapshotID == "" {
		return state, nil
	}
	if !row.VersionPresent || row.VersionDeletedAt != nil {
		return nil, releaseIncompleteError("active snapshot row is missing or deleted")
	}
	if row.VersionScopeID != state.ScopeID || row.VersionSnapshot != row.HeadActiveSnapshotID {
		return nil, releaseIncompleteError("active snapshot belongs to another scope generation")
	}
	changedAt := ""
	if row.HeadChangedAt != nil {
		changedAt = row.HeadChangedAt.UTC().Format(time.RFC3339)
	}
	state.Active = &ReleaseActiveInfo{
		ScopeID:       state.ScopeID,
		SnapshotID:    row.VersionSnapshot,
		Version:       row.VersionName,
		Revision:      state.Revision,
		ChangedAt:     changedAt,
		ChangedBy:     row.HeadChangedBy,
		ChangedReason: row.HeadChangedReason,
	}
	return state, nil
}
