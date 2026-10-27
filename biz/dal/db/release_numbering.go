package db

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

// releaseSequenceVersionRegexp matches only the canonical auto-number form.
// Legacy names such as v1.0, v01, v0 or test-1 never advance the sequence.
var releaseSequenceVersionRegexp = regexp.MustCompile(`^v([1-9][0-9]*)$`)

type releaseNumberScope struct {
	ScopeID        string
	EnvironmentKey string
	PipelineKey    string
}

// EnsureReleaseNumbering backfills one head row per known scope and raises each
// counter to the highest canonical version ever committed, including deleted
// versions. It is idempotent and never changes active/revision/changed fields.
func EnsureReleaseNumbering(ctx context.Context, database *gorm.DB) error {
	if database == nil {
		return nil
	}
	scopes, err := listReleaseNumberScopes(ctx, database)
	if err != nil {
		return err
	}
	dao := NewReleaseDAO()
	for _, scope := range scopes {
		historyMax, err := MaxReleaseSequenceVersion(ctx, database, scope.ScopeID)
		if err != nil {
			return err
		}
		head, err := dao.GetHeadByScopeID(ctx, database, scope.ScopeID)
		if err != nil {
			return err
		}
		current := int64(0)
		if head != nil {
			current = head.LastVersionNumber
		} else if err := dao.EnsureHeadByScope(ctx, database, scope.ScopeID,
			scope.EnvironmentKey, scope.PipelineKey); err != nil {
			return fmt.Errorf("release numbering: create head for scope %s: %w", scope.ScopeID, err)
		}
		if historyMax <= current {
			continue
		}
		if err := database.WithContext(ctx).Model(&model.ConfigReleaseHead{}).
			Where("scope_id = ? AND last_version_number < ?", scope.ScopeID, historyMax).
			UpdateColumn("last_version_number", historyMax).Error; err != nil {
			return fmt.Errorf("release numbering: backfill scope %s: %w", scope.ScopeID, err)
		}
	}
	return nil
}

func listReleaseNumberScopes(ctx context.Context, database *gorm.DB) ([]releaseNumberScope, error) {
	var pipelines []releaseNumberScope
	if err := database.WithContext(ctx).Unscoped().Table("pipeline").
		Select("scope_id, environment_key, pipeline_key").
		Where("scope_id <> ''").
		Scan(&pipelines).Error; err != nil {
		return nil, fmt.Errorf("release numbering: list pipelines: %w", err)
	}
	var versions []releaseNumberScope
	if err := database.WithContext(ctx).Table("config_release_version").
		Select("scope_id, environment_key, pipeline_key").
		Where("scope_id <> ''").
		Group("scope_id, environment_key, pipeline_key").
		Scan(&versions).Error; err != nil {
		return nil, fmt.Errorf("release numbering: list version scopes: %w", err)
	}
	var heads []releaseNumberScope
	if err := database.WithContext(ctx).Table("config_release_head").
		Select("scope_id, environment_key, pipeline_key").
		Where("scope_id <> ''").
		Scan(&heads).Error; err != nil {
		return nil, fmt.Errorf("release numbering: list heads: %w", err)
	}
	seen := make(map[string]releaseNumberScope)
	for _, list := range [][]releaseNumberScope{pipelines, versions, heads} {
		for _, scope := range list {
			if _, exists := seen[scope.ScopeID]; !exists {
				seen[scope.ScopeID] = scope
			}
		}
	}
	scopes := make([]releaseNumberScope, 0, len(seen))
	for _, scope := range seen {
		scopes = append(scopes, scope)
	}
	return scopes, nil
}

// MaxReleaseSequenceVersion returns the highest canonical vN version in a
// scope. Deleted versions are included; legacy names are ignored.
func MaxReleaseSequenceVersion(ctx context.Context, database *gorm.DB, scopeID string) (int64, error) {
	var versions []string
	if err := database.WithContext(ctx).Table("config_release_version").
		Where("scope_id = ?", scopeID).
		Pluck("version", &versions).Error; err != nil {
		return 0, fmt.Errorf("release numbering: list versions for scope %s: %w", scopeID, err)
	}
	maximum := int64(0)
	for _, version := range versions {
		value, canonical, err := ParseReleaseSequenceVersion(version)
		if err != nil {
			return 0, fmt.Errorf("release numbering: scope %s version %q: %w", scopeID, version, err)
		}
		if canonical && value > maximum {
			maximum = value
		}
	}
	return maximum, nil
}

// ParseReleaseSequenceVersion reports whether a name is canonical vN and
// returns its int64 value. Overflow is an explicit error, never a silent skip.
func ParseReleaseSequenceVersion(version string) (int64, bool, error) {
	matches := releaseSequenceVersionRegexp.FindStringSubmatch(version)
	if matches == nil {
		return 0, false, nil
	}
	value, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("canonical version exceeds int64: %w", err)
	}
	return value, true, nil
}

// ReleaseVersionFromNumber formats an allocated positive number as vN.
func ReleaseVersionFromNumber(number int64) string {
	return "v" + strconv.FormatInt(number, 10)
}
