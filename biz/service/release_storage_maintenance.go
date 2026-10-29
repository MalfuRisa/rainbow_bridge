package service

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

// StorageInventory is operational output, never part of the public API.
type StorageInventory struct {
	Instance          string
	Versions          []StorageVersionInspection
	Attempts          []model.ReleaseStorageAttempt
	AttemptInspection []SnapshotAttemptInspection
	LocalDirectories  LocalSnapshotDirectoryInventory
}
type SnapshotAttemptInspection struct {
	AttemptID               string
	References              int64
	Objects, Multipart      int
	Status, SuggestedAction string
}

func (s *Service) inspectSnapshotAttempt(ctx context.Context, a model.ReleaseStorageAttempt) SnapshotAttemptInspection {
	view := SnapshotAttemptInspection{AttemptID: a.ID, Status: "verified", SuggestedAction: "retain"}
	if err := s.logic.db.WithContext(ctx).Model(&model.ConfigReleaseVersion{}).Where("storage_attempt = ? OR (storage_bucket = ? AND storage_prefix = ?)", a.ID, a.Bucket, a.Prefix).Count(&view.References).Error; err != nil {
		view.Status = "reference_check_failed"
		return view
	}
	if !validSnapshotLocator(&model.ConfigReleaseVersion{SnapshotID: a.SnapshotID, StorageAttempt: a.ID, StorageBucket: a.Bucket, StoragePrefix: a.Prefix}) {
		view.Status = "invalid_locator"
		return view
	}
	client, err := s.snapshotMinioClient()
	if err != nil {
		view.Status = "connection_unavailable"
		return view
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for object := range client.ListObjects(ctx, a.Bucket, minio.ListObjectsOptions{Prefix: a.Prefix, Recursive: true, WithVersions: true}) {
		if object.Err != nil {
			view.Status = "object_inventory_failed"
			return view
		}
		view.Objects++
		if view.Objects > 10000 {
			view.Status = "inventory_limit"
			return view
		}
	}
	for object := range client.ListIncompleteUploads(ctx, a.Bucket, a.Prefix, true) {
		if object.Err != nil {
			view.Status = "multipart_inventory_failed"
			return view
		}
		view.Multipart++
		if view.Multipart > 10000 {
			view.Status = "inventory_limit"
			return view
		}
	}
	if view.References == 0 && a.State != "committed" && a.State != "cleaned" {
		view.SuggestedAction = "explicit_reconcile_under_DB_gate"
	}
	return view
}

type StorageVersionInspection struct {
	SnapshotID, Version, Backend, Status string
	Deleted                              bool
	AssetCount                           int
}

// Stable business identity, not credentials, used for explicit CLI confirmation.
func (s *Service) StorageInstanceConfirmation(ctx context.Context) (string, error) {
	var ids []string
	if err := s.logic.db.WithContext(ctx).Unscoped().Model(&model.Environment{}).Order("environment_id").Pluck("environment_id", &ids).Error; err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", errors.New("instance has no stable environment identity")
	}
	return payloadDigest(s.logic.db.Dialector.Name() + ":" + joinStorageIDs(ids))[:24], nil
}
func joinStorageIDs(ids []string) string {
	sort.Strings(ids)
	value := ""
	for _, id := range ids {
		value += "|" + id
	}
	return value
}

func (s *Service) InspectSnapshotStorage(ctx context.Context) (*StorageInventory, error) {
	token, err := s.StorageInstanceConfirmation(ctx)
	if err != nil {
		return nil, err
	}
	result := &StorageInventory{Instance: token, Versions: []StorageVersionInspection{}, Attempts: []model.ReleaseStorageAttempt{}}
	var rows []model.ConfigReleaseVersion
	if err := s.logic.db.WithContext(ctx).Unscoped().Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		backend := row.StorageBackend
		if backend == "" {
			backend = "local"
		}
		item := StorageVersionInspection{SnapshotID: row.SnapshotID, Version: row.Version, Backend: backend, Deleted: row.DeletedAt != nil, AssetCount: row.AssetCount, Status: "verified"}
		st, err := s.snapshotStore(ctx, s.logic, &row)
		if err == nil {
			err = s.verifyReleaseComplete(ctx, s.logic, st, &row)
		}
		if err != nil {
			item.Status = "verification_failed"
		}
		result.Versions = append(result.Versions, item)
	}
	result.LocalDirectories = s.inspectLocalSnapshotDirectories(rows)
	// Old live databases may not have the new table yet; plan never migrates it.
	if s.logic.db.Migrator().HasTable(&model.ReleaseStorageAttempt{}) {
		if err := s.logic.db.WithContext(ctx).Order("created_at").Find(&result.Attempts).Error; err != nil {
			return nil, err
		}
		for _, a := range result.Attempts {
			result.AttemptInspection = append(result.AttemptInspection, s.inspectSnapshotAttempt(ctx, a))
		}
	}
	return result, nil
}

// Migration takes no lifecycle decisions and changes only locator columns.
// The old files remain readable by in-flight readers and for explicit rollback.
func (s *Service) MigrateSnapshotStorage(ctx context.Context, snapshot string) error {
	ctx, cancel := context.WithTimeout(ctx, writeGateTotalTimeout)
	defer cancel()
	if _, err := uuid.Parse(snapshot); err != nil {
		return errors.New("snapshot UUID required")
	}
	backend, err := s.newSnapshotBackend()
	if err != nil {
		return err
	}
	if backend != "minio" {
		return errors.New("migration requires explicit MinIO snapshot configuration")
	}
	var original model.ConfigReleaseVersion
	if err := s.logic.db.WithContext(ctx).Unscoped().First(&original, "snapshot_id = ?", snapshot).Error; err != nil {
		return err
	}
	if original.StorageBackend == "minio" {
		st, err := s.snapshotStore(ctx, s.logic, &original)
		if err != nil {
			return err
		}
		return s.verifyReleaseComplete(ctx, s.logic, st, &original)
	}
	if original.StorageBackend != "" && original.StorageBackend != "local" {
		return errors.New("unknown snapshot backend")
	}
	a, err := s.registerSnapshotAttempt(ctx, snapshot, uuid.NewString(), original.PayloadSha256, original.ScopeID, "migration")
	if err != nil {
		return err
	}
	defer s.finishSnapshotAttempt(a)
	return s.withWriteGate(ctx, func(ctx context.Context, l *Logic, _ *gorm.DB) error {
		if err := checkSnapshotAttempt(ctx, l, a); err != nil {
			return err
		}
		var current model.ConfigReleaseVersion
		if err := l.db.WithContext(ctx).Unscoped().First(&current, "snapshot_id = ?", snapshot).Error; err != nil {
			return err
		}
		if !sameSnapshotLocator(&current, &original) || current.PayloadSha256 != original.PayloadSha256 || current.ManifestSha256 != original.ManifestSha256 {
			return errors.New("snapshot changed during migration")
		}
		local, err := s.existingReleaseStorage()
		if err != nil {
			return err
		}
		if err := s.verifyReleaseComplete(ctx, l, local, &current); err != nil {
			return err
		}
		assets, err := l.releaseDAO.ListAssets(ctx, l.db, snapshot)
		if err != nil {
			return err
		}
		if err := s.publishSnapshotObjects(ctx, local, &current, assets, a); err != nil {
			return err
		}
		result := l.db.WithContext(ctx).Model(&model.ConfigReleaseVersion{}).Where("snapshot_id = ? AND storage_backend = ? AND storage_bucket = ? AND storage_prefix = ? AND storage_attempt = ? AND payload_sha256 = ? AND manifest_sha256 = ?", snapshot, original.StorageBackend, original.StorageBucket, original.StoragePrefix, original.StorageAttempt, original.PayloadSha256, original.ManifestSha256).Updates(map[string]any{"storage_backend": current.StorageBackend, "storage_bucket": current.StorageBucket, "storage_prefix": current.StoragePrefix, "storage_attempt": current.StorageAttempt, "legacy_local": true})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("snapshot migration CAS conflict")
		}
		return commitSnapshotAttempt(ctx, l, a)
	})
}

func sameSnapshotLocator(a, b *model.ConfigReleaseVersion) bool {
	return a.StorageBackend == b.StorageBackend && a.StorageBucket == b.StorageBucket && a.StoragePrefix == b.StoragePrefix && a.StorageAttempt == b.StorageAttempt
}

func (s *Service) RollbackSnapshotStorage(ctx context.Context, snapshot string) error {
	ctx, cancel := context.WithTimeout(ctx, writeGateTotalTimeout)
	defer cancel()
	return s.withWriteGate(ctx, func(ctx context.Context, l *Logic, _ *gorm.DB) error {
		var row model.ConfigReleaseVersion
		if err := l.db.WithContext(ctx).Unscoped().First(&row, "snapshot_id = ?", snapshot).Error; err != nil {
			return err
		}
		if row.StorageBackend != "minio" || !row.LegacyLocal {
			return errors.New("no verified legacy local source recorded")
		}
		local, err := s.existingReleaseStorage()
		if err != nil {
			return err
		}
		if err := s.verifyReleaseComplete(ctx, l, local, &row); err != nil {
			return err
		}
		// Keep the remote attempt COMMITTED: rollback must never authorize its GC.
		return l.db.WithContext(ctx).Model(&row).Updates(map[string]any{"storage_backend": "local", "storage_bucket": "", "storage_prefix": "", "storage_attempt": ""}).Error
	})
}
