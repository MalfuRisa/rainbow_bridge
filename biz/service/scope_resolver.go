package service

import (
	"context"
	"errors"
	"strings"

	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

// resolveEnvironmentByKey maps the database lifecycle signal to the API
// contract: hidden/deleted scope → 410, never-existing key → 404.
func resolveEnvironmentByKey(ctx context.Context, l *Logic, environmentKey string) (*model.Environment, error) {
	env, err := db.ActiveEnvironment(ctx, l.db, environmentKey)
	if err == nil {
		return env, nil
	}
	switch {
	case errors.Is(err, db.ErrScopeDeleted):
		return nil, scopeDeletedError("environment " + environmentKey + " has been deleted")
	case errors.Is(err, db.ErrNoActiveScope):
		return nil, scopeNotFoundError("environment " + environmentKey + " not found")
	default:
		return nil, err
	}
}

func resolveScopeByKeys(ctx context.Context, l *Logic, environmentKey, pipelineKey string) (*model.Pipeline, error) {
	pipeline, err := db.ActiveScope(ctx, l.db, environmentKey, pipelineKey)
	if err == nil {
		return pipeline, nil
	}
	if (errors.Is(err, db.ErrNoActiveScope) || errors.Is(err, db.ErrScopeDeleted)) && db.Uninitialized(ctx, l.db) {
		return &model.Pipeline{
			ScopeID:        db.LegacyScopeID(environmentKey, pipelineKey),
			EnvironmentKey: environmentKey, PipelineKey: pipelineKey,
		}, nil
	}
	switch {
	case errors.Is(err, db.ErrScopeDeleted):
		return nil, scopeDeletedError("scope " + environmentKey + "/" + pipelineKey + " has been deleted")
	case errors.Is(err, db.ErrNoActiveScope):
		return nil, scopeNotFoundError("scope " + environmentKey + "/" + pipelineKey + " not found")
	default:
		return nil, err
	}
}

// resolveReadScope captures the live pipeline generation for a read. A non-empty
// pin must still be the current generation; an absent/empty pin follows the
// current keys. The returned pipeline is the identity subsequent head/version
// reads must use.
func resolveReadScope(ctx context.Context, l *Logic, environmentKey, pipelineKey, requestedScopeID string) (*model.Pipeline, error) {
	if requestedScopeID == "" {
		return resolveScopeByKeys(ctx, l, environmentKey, pipelineKey)
	}
	pinned, err := db.ScopeByIDIncludingDeleted(ctx, l.db, requestedScopeID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, scopeNotFoundError("scope not found")
		}
		return nil, err
	}
	if pinned.ActiveSlot == nil || *pinned.ActiveSlot != model.ActiveSlotActive || pinned.DeletedAt.Valid {
		return nil, scopeDeletedError("scope has been deleted")
	}
	current, err := resolveScopeByKeys(ctx, l, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	if current.ScopeID != requestedScopeID {
		return nil, scopeIdentityConflict("scope_id does not identify the live generation")
	}
	return current, nil
}

// requireEnvironmentIdentity validates the client's expected environment id
// before any write. Missing identity is a 400 rather than a silent default.
func requireEnvironmentIdentity(ctx context.Context, l *Logic, environmentKey, expected string) (*model.Environment, error) {
	env, err := resolveEnvironmentByKey(ctx, l, environmentKey)
	if err != nil {
		if deleted := expectedEnvironmentDeleted(ctx, l, environmentKey, expected); deleted {
			return nil, scopeDeletedError("environment " + environmentKey + " has been deleted")
		}
		if conflicts := expectedEnvironmentReplaced(ctx, l, environmentKey, expected); conflicts {
			return nil, scopeIdentityConflict("environment_key now identifies a different generation")
		}
		return nil, err
	}
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return nil, scopeInvalidRequest("expected_environment_id is required")
	}
	if env.EnvironmentID != expected {
		if deleted := expectedEnvironmentDeleted(ctx, l, environmentKey, expected); deleted {
			return nil, scopeDeletedError("environment " + environmentKey + " has been deleted")
		}
		return nil, scopeIdentityConflict("environment_key now identifies a different generation")
	}
	return env, nil
}

func expectedEnvironmentDeleted(ctx context.Context, l *Logic, environmentKey, expected string) bool {
	if strings.TrimSpace(expected) == "" {
		return false
	}
	env, err := db.EnvironmentByIDIncludingDeleted(ctx, l.db, expected)
	return err == nil && env.EnvironmentKey == environmentKey && (!env.DeletedAt.Valid || env.ActiveSlot == nil)
}

func expectedEnvironmentReplaced(ctx context.Context, l *Logic, environmentKey, expected string) bool {
	if strings.TrimSpace(expected) == "" {
		return false
	}
	env, err := db.EnvironmentByIDIncludingDeleted(ctx, l.db, expected)
	return err == nil && env.EnvironmentKey == environmentKey
}

// resolveScopeForWrite keeps legacy internal wrappers working while the HTTP
// layer always supplies expected ids. Empty expected resolves the live scope
// once inside the write gate.
func resolveScopeForWrite(ctx context.Context, l *Logic, environmentKey, pipelineKey, expected string) (*model.Pipeline, error) {
	if strings.TrimSpace(expected) == "" {
		return resolveScopeByKeys(ctx, l, environmentKey, pipelineKey)
	}
	return requireScopeIdentity(ctx, l, environmentKey, pipelineKey, expected)
}

func resolveEnvironmentForWrite(ctx context.Context, l *Logic, environmentKey, expected string) (*model.Environment, error) {
	if strings.TrimSpace(expected) == "" {
		return resolveEnvironmentByKey(ctx, l, environmentKey)
	}
	return requireEnvironmentIdentity(ctx, l, environmentKey, expected)
}

// requireScopeIdentity validates expected_scope_id for a write on one scope.
func requireScopeIdentity(ctx context.Context, l *Logic, environmentKey, pipelineKey, expected string) (*model.Pipeline, error) {
	pipeline, err := resolveScopeByKeys(ctx, l, environmentKey, pipelineKey)
	if err != nil {
		if deleted := expectedScopeDeleted(ctx, l, expected); deleted {
			return nil, scopeDeletedError("scope " + environmentKey + "/" + pipelineKey + " has been deleted")
		}
		if replaced := expectedScopeReplaced(ctx, l, expected); replaced {
			return nil, scopeIdentityConflict("scope keys now identify a different generation")
		}
		return nil, err
	}
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return nil, scopeInvalidRequest("expected_scope_id is required")
	}
	if pipeline.ScopeID != expected {
		if deleted := expectedScopeDeleted(ctx, l, expected); deleted {
			return nil, scopeDeletedError("scope " + environmentKey + "/" + pipelineKey + " has been deleted")
		}
		return nil, scopeIdentityConflict("scope keys now identify a different generation")
	}
	return pipeline, nil
}

func expectedScopeDeleted(ctx context.Context, l *Logic, expected string) bool {
	if strings.TrimSpace(expected) == "" {
		return false
	}
	pipeline, err := db.ScopeByIDIncludingDeleted(ctx, l.db, expected)
	return err == nil && (pipeline.ActiveSlot == nil || pipeline.DeletedAt.Valid)
}

func expectedScopeReplaced(ctx context.Context, l *Logic, expected string) bool {
	if strings.TrimSpace(expected) == "" {
		return false
	}
	_, err := db.ScopeByIDIncludingDeleted(ctx, l.db, expected)
	return err == nil
}

// optionalScopeDeletion maps database lifecycle errors on read-only paths.
func mapScopeReadError(err error, environmentKey, pipelineKey string) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, db.ErrScopeDeleted):
		return scopeDeletedError("scope " + environmentKey + "/" + pipelineKey + " has been deleted")
	case errors.Is(err, db.ErrNoActiveScope):
		return scopeNotFoundError("scope " + environmentKey + "/" + pipelineKey + " not found")
	case errors.Is(err, gorm.ErrRecordNotFound):
		return scopeNotFoundError("scope " + environmentKey + "/" + pipelineKey + " not found")
	default:
		return err
	}
}
