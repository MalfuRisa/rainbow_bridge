package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

// Must be called OUTSIDE the business transaction, including on SQLite.
func (s *Service) registerSnapshotAttempt(ctx context.Context, snapshot, request, fingerprint, scope, kind string) (*model.ReleaseStorageAttempt, error) {
	if _, err := s.newSnapshotBackend(); err != nil {
		return nil, err
	}
	id := uuid.NewString()
	a := &model.ReleaseStorageAttempt{ID: id, SnapshotID: snapshot, RequestID: request, Fingerprint: fingerprint, ScopeID: scope, Kind: kind, Bucket: s.config.ReleaseStorage.Bucket, Prefix: "snapshots/v1/" + snapshot + "/" + id + "/", State: "pending"}
	if err := s.logic.db.WithContext(ctx).Create(a).Error; err != nil {
		return nil, releaseInternalError(err)
	}
	return a, nil
}

// Called only while holding the shared DB write coordination lock. Reconcile
// can claim a queued attempt first; that attempt can never subsequently publish.
func checkSnapshotAttempt(ctx context.Context, l *Logic, a *model.ReleaseStorageAttempt) error {
	var saved model.ReleaseStorageAttempt
	if err := l.db.WithContext(ctx).First(&saved, "id = ?", a.ID).Error; err != nil {
		return releaseInternalError(err)
	}
	if saved.State != "pending" || saved.Prefix != a.Prefix || saved.Bucket != a.Bucket || saved.SnapshotID != a.SnapshotID {
		return releaseStorageError(errors.New("snapshot attempt is not publishable"))
	}
	return nil
}

func commitSnapshotAttempt(ctx context.Context, l *Logic, a *model.ReleaseStorageAttempt) error {
	result := l.db.WithContext(ctx).Model(&model.ReleaseStorageAttempt{}).Where("id = ? AND state = ?", a.ID, "pending").Updates(map[string]any{"state": "committed", "diagnostic": ""})
	if result.Error != nil {
		return releaseInternalError(result.Error)
	}
	if result.RowsAffected != 1 {
		return releaseStorageError(errors.New("snapshot attempt lost publication ownership"))
	}
	return nil
}

// The gate is both the no-in-flight-writer guard and the cleanup ownership CAS.
// All object uploads/locator commits take this same gate and re-read pending.
// Includes deleted versions and deleted scopes: locator references are permanent.
func (s *Service) claimSnapshotCleanup(ctx context.Context, id string) (*model.ReleaseStorageAttempt, error) {
	var a model.ReleaseStorageAttempt
	err := s.withWriteGate(ctx, func(ctx context.Context, l *Logic, _ *gorm.DB) error {
		if err := l.db.WithContext(ctx).First(&a, "id = ?", id).Error; err != nil {
			return err
		}
		var count int64
		if err := l.db.WithContext(ctx).Model(&model.ConfigReleaseVersion{}).Where("storage_attempt = ? OR (storage_bucket = ? AND storage_prefix = ?)", a.ID, a.Bucket, a.Prefix).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return releaseStorageError(errors.New("committed snapshot references attempt; cleanup refused"))
		}
		if a.State == "committed" {
			return releaseStorageError(errors.New("committed attempt cleanup refused"))
		}
		if a.State == "cleaned" {
			return nil
		}
		if a.State != "pending" && a.State != "unknown" && a.State != "cleanup_pending" {
			return releaseStorageError(errors.New("attempt is already claimed; inspect before retry"))
		}
		result := l.db.WithContext(ctx).Model(&model.ReleaseStorageAttempt{}).Where("id = ? AND state = ?", id, a.State).Updates(map[string]any{"state": "cleanup_pending", "diagnostic": "cleanup_claimed"})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return releaseStorageError(errors.New("cleanup ownership changed"))
		}
		return nil
	})
	return &a, err
}

func (s *Service) removeSnapshotAttemptObjects(ctx context.Context, a *model.ReleaseStorageAttempt) error {
	row := &model.ConfigReleaseVersion{SnapshotID: a.SnapshotID, StorageAttempt: a.ID, StorageBucket: a.Bucket, StoragePrefix: a.Prefix}
	if !validSnapshotLocator(row) {
		return releaseStorageError(errors.New("invalid cleanup ownership"))
	}
	// Even a malformed DB entry may never direct cleanup into the original bucket.
	if s.config == nil || a.Bucket == s.config.Storage.Minio.Bucket {
		return releaseStorageError(errors.New("original bucket cleanup refused"))
	}
	client, err := s.snapshotMinioClient()
	if err != nil {
		return err
	}
	// Bounded single-object removes let each error be observed. No unbounded key list.
	for item := range client.ListObjects(ctx, a.Bucket, minio.ListObjectsOptions{Prefix: a.Prefix, Recursive: true, WithVersions: true}) {
		if item.Err != nil {
			return snapshotObjectError(ctx, item.Err)
		}
		if err := client.RemoveObject(ctx, a.Bucket, item.Key, minio.RemoveObjectOptions{VersionID: item.VersionID}); err != nil {
			return snapshotObjectError(ctx, err)
		}
	}
	for item := range client.ListIncompleteUploads(ctx, a.Bucket, a.Prefix, true) {
		if item.Err != nil {
			return snapshotObjectError(ctx, item.Err)
		}
		if err := client.RemoveIncompleteUpload(ctx, a.Bucket, item.Key); err != nil {
			return snapshotObjectError(ctx, err)
		}
	}
	for item := range client.ListObjects(ctx, a.Bucket, minio.ListObjectsOptions{Prefix: a.Prefix, Recursive: true, WithVersions: true}) {
		if item.Err != nil {
			return snapshotObjectError(ctx, item.Err)
		}
		return releaseStorageError(errors.New("snapshot objects remain after cleanup"))
	}
	for item := range client.ListIncompleteUploads(ctx, a.Bucket, a.Prefix, true) {
		if item.Err != nil {
			return snapshotObjectError(ctx, item.Err)
		}
		return releaseStorageError(errors.New("snapshot multipart uploads remain"))
	}
	return ctx.Err()
}

// ReconcileSnapshotAttempt defaults to read-only at the CLI boundary; execution
// always reacquires the DB gate. There is intentionally no force/age bypass.
func (s *Service) ReconcileSnapshotAttempt(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, writeGateTotalTimeout)
	defer cancel()
	a, err := s.claimSnapshotCleanup(ctx, id)
	if err != nil {
		return err
	}
	if a.State == "cleaned" {
		return nil
	}
	// Serialize cleanup retries too. Claim is already durable, so a cleanup
	// failure/rollback/crash cannot make a queued creator publish this attempt.
	err = s.withWriteGate(ctx, func(ctx context.Context, l *Logic, _ *gorm.DB) error {
		var current model.ReleaseStorageAttempt
		if err := l.db.WithContext(ctx).First(&current, "id = ?", id).Error; err != nil {
			return err
		}
		if current.State == "cleaned" {
			return nil
		}
		if current.State != "cleanup_pending" {
			return releaseStorageError(errors.New("cleanup ownership changed"))
		}
		var references int64
		if err := l.db.WithContext(ctx).Model(&model.ConfigReleaseVersion{}).Where("storage_attempt = ? OR (storage_bucket = ? AND storage_prefix = ?)", id, current.Bucket, current.Prefix).Count(&references).Error; err != nil {
			return err
		}
		if references != 0 {
			return releaseStorageError(errors.New("referenced snapshot cleanup refused"))
		}
		if err := s.removeSnapshotAttemptObjects(ctx, &current); err != nil {
			return err
		}
		return l.db.WithContext(ctx).Model(&current).Updates(map[string]any{"state": "cleaned", "diagnostic": ""}).Error
	})
	return err
}

// Never reuse a cancelled request to decide a commit outcome or delete objects.
func (s *Service) finishSnapshotAttempt(a *model.ReleaseStorageAttempt) {
	if a == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var count int64
	err := s.logic.db.WithContext(ctx).Model(&model.ConfigReleaseVersion{}).Where("storage_attempt = ?", a.ID).Count(&count).Error
	if err != nil {
		s.logic.db.WithContext(ctx).Model(&model.ReleaseStorageAttempt{}).Where("id = ? AND state = ?", a.ID, "pending").Updates(map[string]any{"state": "unknown", "diagnostic": "commit_confirmation_failed"})
		return
	}
	if count > 0 {
		return
	}
	// Cleanup itself rechecks references and claims under the DB gate, closing
	// the race with a queued/in-flight create or migration.
	_ = s.ReconcileSnapshotAttempt(ctx, a.ID)
}
