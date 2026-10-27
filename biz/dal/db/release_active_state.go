package db

import (
	"context"
	"database/sql"
	"time"

	"gorm.io/gorm"
)

// ReleaseActiveStateRow is the single-statement snapshot behind
// GET /api/v1/release/active. Nullable SQL fields preserve the distinction
// between "no head row" and "head row with revision 0 / empty pointer".
type ReleaseActiveStateRow struct {
	CurrentPresent       bool
	CurrentScopeID       string
	CurrentEnvironmentID string
	CurrentEnvironment   string
	CurrentPipeline      string
	CurrentPipelineName  string

	PinnedPresent     bool
	PinnedActive      bool
	PinnedEnvironment string
	PinnedPipeline    string

	KeyEnvironmentCount int64
	KeyPipelineCount    int64
	TotalEnvironment    int64
	TotalPipeline       int64

	HeadPresent          bool
	HeadScopeID          string
	HeadActiveSnapshotID string
	HeadRevision         int64
	HeadChangedAt        *time.Time
	HeadChangedBy        string
	HeadChangedReason    string

	VersionPresent   bool
	VersionScopeID   string
	VersionSnapshot  string
	VersionName      string
	VersionDeletedAt *time.Time
}

type activeStateSQLRow struct {
	CurrentScopeID       sql.NullString
	CurrentEnvironmentID sql.NullString
	CurrentEnvironment   sql.NullString
	CurrentPipeline      sql.NullString
	CurrentPipelineName  sql.NullString

	HeadScopeID          sql.NullString
	HeadActiveSnapshotID sql.NullString
	HeadRevision         sql.NullInt64
	HeadChangedAt        sql.NullTime
	HeadChangedBy        sql.NullString
	HeadChangedReason    sql.NullString

	VersionSnapshotID sql.NullString
	VersionScopeID    sql.NullString
	VersionName       sql.NullString
	VersionDeletedAt  sql.NullTime

	PinnedScopeID     sql.NullString
	PinnedActiveSlot  sql.NullString
	PinnedDeletedAt   sql.NullTime
	PinnedEnvironment sql.NullString
	PinnedPipeline    sql.NullString

	KeyEnvironmentCount int64
	KeyPipelineCount    int64
	TotalEnvironment    int64
	TotalPipeline       int64
}

const releaseActiveStateSQL = `
SELECT
  cur.scope_id AS current_scope_id,
  cur.environment_id AS current_environment_id,
  cur.environment_key AS current_environment,
  cur.pipeline_key AS current_pipeline,
  cur.pipeline_name AS current_pipeline_name,
  h.scope_id AS head_scope_id,
  h.active_snapshot_id AS head_active_snapshot_id,
  h.revision AS head_revision,
  h.changed_at AS head_changed_at,
  h.changed_by AS head_changed_by,
  h.changed_reason AS head_changed_reason,
  v.snapshot_id AS version_snapshot_id,
  v.scope_id AS version_scope_id,
  v.version AS version_name,
  v.deleted_at AS version_deleted_at,
  pin.scope_id AS pinned_scope_id,
  pin.active_slot AS pinned_active_slot,
  pin.deleted_at AS pinned_deleted_at,
  pin.environment_key AS pinned_environment,
  pin.pipeline_key AS pinned_pipeline,
  (SELECT COUNT(*) FROM environment e2 WHERE e2.environment_key = ?) AS key_environment_count,
  (SELECT COUNT(*) FROM pipeline p2 WHERE p2.environment_key = ? AND p2.pipeline_key = ?) AS key_pipeline_count,
  (SELECT COUNT(*) FROM environment) AS total_environment,
  (SELECT COUNT(*) FROM pipeline) AS total_pipeline
FROM (SELECT 1 AS one) AS seed
LEFT JOIN environment env
  ON env.environment_key = ? AND env.active_slot = ? AND env.deleted_at IS NULL
LEFT JOIN pipeline cur
  ON cur.environment_id = env.environment_id AND cur.pipeline_key = ?
 AND cur.active_slot = ? AND cur.deleted_at IS NULL
LEFT JOIN config_release_head h ON h.scope_id = cur.scope_id
LEFT JOIN config_release_version v ON v.snapshot_id = h.active_snapshot_id
LEFT JOIN pipeline pin ON pin.scope_id = ?
LIMIT 1`

// ReadActiveStateSnapshot captures the live scope, head and referenced version
// in one SELECT. It never creates a head row or repairs state.
func (dao *ReleaseDAO) ReadActiveStateSnapshot(ctx context.Context, database *gorm.DB,
	environmentKey, pipelineKey, pinnedScopeID string) (*ReleaseActiveStateRow, error) {
	var scanned activeStateSQLRow
	result := database.WithContext(ctx).Raw(releaseActiveStateSQL,
		environmentKey,
		environmentKey, pipelineKey,
		environmentKey, "active",
		pipelineKey, "active",
		pinnedScopeID,
	).Scan(&scanned)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	row := &ReleaseActiveStateRow{
		CurrentPresent:       scanned.CurrentScopeID.Valid,
		CurrentScopeID:       scanned.CurrentScopeID.String,
		CurrentEnvironmentID: scanned.CurrentEnvironmentID.String,
		CurrentEnvironment:   scanned.CurrentEnvironment.String,
		CurrentPipeline:      scanned.CurrentPipeline.String,
		CurrentPipelineName:  scanned.CurrentPipelineName.String,
		PinnedPresent:        scanned.PinnedScopeID.Valid,
		PinnedEnvironment:    scanned.PinnedEnvironment.String,
		PinnedPipeline:       scanned.PinnedPipeline.String,
		KeyEnvironmentCount:  scanned.KeyEnvironmentCount,
		KeyPipelineCount:     scanned.KeyPipelineCount,
		TotalEnvironment:     scanned.TotalEnvironment,
		TotalPipeline:        scanned.TotalPipeline,
		HeadPresent:          scanned.HeadRevision.Valid,
		HeadScopeID:          scanned.HeadScopeID.String,
		HeadActiveSnapshotID: scanned.HeadActiveSnapshotID.String,
		HeadRevision:         scanned.HeadRevision.Int64,
		HeadChangedBy:        scanned.HeadChangedBy.String,
		HeadChangedReason:    scanned.HeadChangedReason.String,
		VersionPresent:       scanned.VersionSnapshotID.Valid,
		VersionScopeID:       scanned.VersionScopeID.String,
		VersionSnapshot:      scanned.VersionSnapshotID.String,
		VersionName:          scanned.VersionName.String,
	}
	if scanned.HeadChangedAt.Valid {
		changedAt := scanned.HeadChangedAt.Time
		row.HeadChangedAt = &changedAt
	}
	if scanned.VersionDeletedAt.Valid {
		deletedAt := scanned.VersionDeletedAt.Time
		row.VersionDeletedAt = &deletedAt
	}
	row.PinnedActive = scanned.PinnedScopeID.Valid &&
		scanned.PinnedActiveSlot.Valid && scanned.PinnedActiveSlot.String == "active" &&
		!scanned.PinnedDeletedAt.Valid
	return row, nil
}
