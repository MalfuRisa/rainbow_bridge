package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gopkg.in/yaml.v3"
)

func runRemoteMaintenance(t *testing.T, s *Service, action, snapshot string) {
	t.Helper()
	binary := os.Getenv("RB_REMOTE_MAINTENANCE_BINARY")
	if binary == "" {
		t.Fatal("maintenance binary required")
	}
	cfg := *s.config
	var schema string
	if err := s.logic.db.Raw("SELECT current_schema()").Scan(&schema).Error; err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(cfg.Database.Postgres.DSN)
	if err != nil {
		t.Fatal("private DSN invalid")
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	cfg.Database.Postgres.DSN = u.String()
	raw, err := yaml.Marshal(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "maintenance.yaml")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--config", file, "--action", action}
	if snapshot != "" {
		token, err := s.StorageInstanceConfirmation(adminTestContext())
		if err != nil {
			t.Fatal(err)
		}
		args = append(args, "--execute", "--confirm-instance", token, "--snapshots", snapshot)
	}
	cmd := exec.Command(binary, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("owned maintenance CLI %s failed (diagnostic length %d)", action, len(output))
	}
}

func ownedSnapshotBucket(t *testing.T, s *Service, manifest map[string]any) (string, func()) {
	t.Helper()
	bucket := "rbsnap-" + uuid.NewString()
	client, err := s.snapshotMinioClient()
	if err != nil {
		t.Fatal(err)
	}
	manifest["snapshotBucket"] = bucket
	manifest["snapshotBucketCleanup"] = false
	if exists, err := client.BucketExists(context.Background(), bucket); err != nil || exists {
		t.Fatal("cannot own snapshot bucket")
	}
	if err := client.MakeBucket(context.Background(), bucket, minio.MakeBucketOptions{Region: s.config.Storage.Minio.Region}); err != nil {
		t.Fatal("snapshot bucket creation unavailable")
	}
	return bucket, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		failed := false
		for item := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
			if item.Err != nil {
				failed = true
				continue
			}
			if err := client.RemoveObject(ctx, bucket, item.Key, minio.RemoveObjectOptions{}); err != nil {
				failed = true
			}
		}
		for item := range client.ListIncompleteUploads(ctx, bucket, "", true) {
			if item.Err != nil {
				failed = true
				continue
			}
			if err := client.RemoveIncompleteUpload(ctx, bucket, item.Key); err != nil {
				failed = true
			}
		}
		if err := client.RemoveBucket(ctx, bucket); err != nil {
			failed = true
		}
		manifest["snapshotBucketCleanup"] = !failed
		if failed {
			t.Error("owned snapshot bucket cleanup failed")
		}
	}
}

func verifyRemoteSnapshotMigration(t *testing.T, s *Service, bucket string, manifest map[string]any) {
	t.Helper()
	ctx := adminTestContext()
	env := "snap_" + uuid.NewString()
	seedFreezeScope(t, s, env, "default")
	image, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		_, ref, err := s.UploadAsset(ctx, &FileUploadInput{EnvironmentKey: env, PipelineKey: "default", FileName: fmt.Sprintf("pixel-%d.png", i), ContentType: "image/png", Data: image})
		if err != nil {
			t.Fatal("ten resource upload", err)
		}
		if _, err := s.AddConfig(ctx, buildConfig(env, "default", fmt.Sprintf("IMG%d", i), "image", ref)); err != nil {
			t.Fatal(err)
		}
	}
	mustLock(t, s, env, "default", "snapshot migration fixture")
	// Real local history: active, inactive and logically deleted, each with images.
	s.config.ReleaseStorage.Type = "local"
	versions := []*ReleaseCreateResult{}
	for i := 0; i < 3; i++ {
		v, err := s.CreateRelease(ctx, ReleaseCreateInput{EnvironmentKey: env, PipelineKey: "default", ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v)
	}
	if _, err := s.ActivateRelease(ctx, ReleaseActivateInput{EnvironmentKey: env, PipelineKey: "default", Version: versions[0].Meta.Version, ExpectedActiveRevisionRaw: "0", RequestID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteRelease(ctx, ReleaseDeleteInput{EnvironmentKey: env, PipelineKey: "default", Version: versions[2].Meta.Version, ExpectedSnapshotID: versions[2].Meta.SnapshotID, RequestID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	var before []model.ConfigReleaseVersion
	s.logic.db.Where("scope_id = ?", versions[0].Meta.ScopeID).Order("id").Find(&before)
	var headBefore model.ConfigReleaseHead
	s.logic.db.Where("scope_id = ?", versions[0].Meta.ScopeID).First(&headBefore)
	readPackage := func(version *ReleaseCreateResult) []byte {
		t.Helper()
		p, err := s.ReleaseStaticExport(ctx, ReleaseStaticExportInput{EnvironmentKey: env, PipelineKey: "default", Version: version.Meta.Version, ExpectedSnapshotID: version.Meta.SnapshotID})
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(p.TempPath)
		b, err := os.ReadFile(p.TempPath)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	zipBefore := readPackage(versions[0])
	runtimeBefore, err := s.RuntimeRelease(ctx, env, "default", nil)
	if err != nil {
		t.Fatal(err)
	}
	transferRequest := ExportRequest{Source: TransferSourceVersion, Format: "zip", EnvironmentKey: env, PipelineKey: "default", ExpectedScopeID: versions[0].Meta.ScopeID, ExpectedSnapshotID: versions[0].Meta.SnapshotID, Version: versions[0].Meta.Version}
	transferBefore, err := s.resolveExportPlan(ctx, transferRequest, true)
	if err != nil {
		t.Fatal(err)
	}
	s.config.ReleaseStorage.Type = "minio"
	s.config.ReleaseStorage.Bucket = bucket
	local, err := s.releaseStorage()
	if err != nil {
		t.Fatal(err)
	}
	// Deterministic missing-history interruption: no source reconstruction, no pointer change.
	missing := versions[1].Meta.SnapshotID
	if err := os.Rename(local.finalDir(missing), local.finalDir(missing)+".held"); err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateSnapshotStorage(ctx, missing); err == nil {
		t.Fatal("missing history migration accepted")
	}
	if err := os.Rename(local.finalDir(missing)+".held", local.finalDir(missing)); err != nil {
		t.Fatal(err)
	}
	for i, v := range versions {
		runRemoteMaintenance(t, s, "migrate", v.Meta.SnapshotID)
		if err := s.MigrateSnapshotStorage(ctx, v.Meta.SnapshotID); err != nil {
			t.Fatal("migration rerun", err)
		}
		if i == 0 {
			// Mixed history remains readable before remaining locators switch.
			fixed := versions[1].Meta.Version
			if _, err := s.RuntimeRelease(ctx, env, "default", &fixed); err != nil {
				t.Fatal("mixed local history", err)
			}
		}
	}
	if !bytes.Equal(zipBefore, readPackage(versions[0])) {
		t.Fatal("migration changed static ZIP bytes")
	}
	runtimeAfter, err := s.RuntimeRelease(ctx, env, "default", nil)
	if err != nil || !reflect.DeepEqual(runtimeBefore, runtimeAfter) {
		t.Fatal("migration changed runtime", err)
	}
	transferAfter, err := s.resolveExportPlan(ctx, transferRequest, true)
	if err != nil || len(transferBefore.Assets) != len(transferAfter.Assets) {
		t.Fatal("migration changed transfer assets", err)
	}
	transferBytes := map[string][]byte{}
	for _, a := range transferBefore.Assets {
		transferBytes[a.metadata.FileID] = a.data
	}
	for _, a := range transferAfter.Assets {
		if !bytes.Equal(transferBytes[a.metadata.FileID], a.data) {
			t.Fatal("migration changed transfer version bytes")
		}
	}
	var after []model.ConfigReleaseVersion
	s.logic.db.Where("scope_id = ?", versions[0].Meta.ScopeID).Order("id").Find(&after)
	if len(before) != len(after) {
		t.Fatal("migration changed version count")
	}
	for i := range after {
		if after[i].StorageBackend != "minio" {
			t.Fatal("migration locator not switched")
		}
		after[i].StorageBackend = before[i].StorageBackend
		after[i].StorageBucket = before[i].StorageBucket
		after[i].StoragePrefix = before[i].StoragePrefix
		after[i].StorageAttempt = before[i].StorageAttempt
		after[i].LegacyLocal = before[i].LegacyLocal
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("migration altered immutable or lifecycle metadata")
	}
	var headAfter model.ConfigReleaseHead
	s.logic.db.Where("scope_id = ?", versions[0].Meta.ScopeID).First(&headAfter)
	if !reflect.DeepEqual(headBefore, headAfter) {
		t.Fatal("migration changed active/revision")
	}
	for _, v := range versions {
		if _, err := os.Stat(local.finalDir(v.Meta.SnapshotID)); err != nil {
			t.Fatal("migration deleted local files")
		}
	}
	// New snapshots no longer need any permanent local snapshot directory.
	oldRoot := os.Getenv(releaseDirEnv)
	os.Setenv(releaseDirEnv, "unavailable-permanent-root")
	defer os.Setenv(releaseDirEnv, oldRoot)
	created, err := s.CreateRelease(ctx, ReleaseCreateInput{EnvironmentKey: env, PipelineKey: "default", ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString()})
	if err != nil || created.Meta.AssetCount != 10 {
		t.Fatal("ten MinIO snapshot assets", err)
	}
	var row model.ConfigReleaseVersion
	s.logic.db.First(&row, "snapshot_id = ?", created.Meta.SnapshotID)
	assets, err := s.logic.releaseDAO.ListAssets(ctx, s.logic.db, row.SnapshotID)
	if err != nil || len(assets) != 10 {
		t.Fatal("ten object DB manifest", err)
	}
	store, err := s.snapshotStore(ctx, s.logic, &row)
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		b, err := store.readAssetBytes(row.SnapshotID, asset.AssetID, asset.Size, asset.Sha256)
		if err != nil || !bytes.Equal(b, image) {
			t.Fatal("ten object hash/readback", err)
		}
	}
	scheme := "http"
	if s.config.Storage.Minio.UseSSL {
		scheme = "https"
	}
	anonymous := http.Client{Timeout: 5 * time.Second}
	response, err := anonymous.Get(scheme + "://" + s.config.Storage.Minio.Endpoint + "/" + row.StorageBucket + "/" + row.StoragePrefix + "assets/" + assets[0].AssetID + ".bin")
	if err != nil {
		t.Fatal("private bucket anonymous probe failed")
	}
	response.Body.Close()
	if response.StatusCode != 403 && response.StatusCode != 404 {
		t.Fatal("snapshot bucket unexpectedly allows anonymous direct reads")
	}
	// A second service instance has no local state; actual process cross-read is below in HTTP fixture.
	other := NewService(s.logic.db, s.redisClient, s.basePath, s.config)
	if _, err := other.ActivateRelease(ctx, ReleaseActivateInput{EnvironmentKey: env, PipelineKey: "default", Version: created.Meta.Version, ExpectedActiveRevisionRaw: "1", RequestID: uuid.NewString()}); err != nil {
		t.Fatal("second instance activation", err)
	}
	originals, err := s.releaseMinioClient()
	if err != nil {
		t.Fatal(err)
	}
	var sourceRows []model.Asset
	s.logic.db.Where("scope_id = ?", row.ScopeID).Find(&sourceRows)
	for _, a := range sourceRows {
		if err := originals.RemoveObject(ctx, s.config.Storage.Minio.Bucket, a.Path, minio.RemoveObjectOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []string{TransferSourceActive, TransferSourceVersion} {
		request := ExportRequest{Source: source, Format: "zip", EnvironmentKey: env, PipelineKey: "default", ExpectedScopeID: row.ScopeID, ExpectedSnapshotID: row.SnapshotID}
		if source == TransferSourceVersion {
			request.Version = row.Version
		}
		plan, err := other.resolveExportPlan(ctx, request, true)
		if err != nil || len(plan.Assets) != 10 {
			t.Fatal("remote transfer export", err)
		}
		for _, a := range plan.Assets {
			if !bytes.Equal(a.data, image) {
				t.Fatal("remote transfer bytes")
			}
		}
		exported, err := other.ExportWithSource(ctx, request)
		if err != nil {
			t.Fatal("remote transfer ZIP", err)
		}
		os.Remove(exported.TempPath)
	}
	fixed := created.Meta.Version
	if _, err := other.RuntimeRelease(ctx, env, "default", &fixed); err != nil {
		t.Fatal("fixed after source deletion", err)
	}
	readPackage(created)
	manifest["minioSnapshots"] = "ten resources GET size/SHA/manifest, no permanent local root, source deletion, second instance activation/runtime/static/transfer active+version passed"
	manifest["localMigration"] = "three real local versions: active/inactive/deleted; missing-source retry, mixed read, rerun, identical ZIP/metadata/head, retained local files passed"
	manifest["localHistoryDirectory"] = filepath.Clean(local.root)
	t.Log("MINIO SNAPSHOTS: ten resources, all GET SHA/size, two stores, original deletion, runtime/static/transfer passed")
	t.Log("MIGRATION: real PG local active/inactive/deleted history, missing-source failure/resume, mixed locators, byte-identical ZIP and unchanged version/head passed")
}
