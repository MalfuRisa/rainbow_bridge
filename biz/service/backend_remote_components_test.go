package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/minio/minio-go/v7"
	redisclient "github.com/redis/go-redis/v9"
	configdb "github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	"github.com/yi-nology/rainbow_bridge/biz/model/transfer"
	appconfig "github.com/yi-nology/rainbow_bridge/pkg/config"
	appredis "github.com/yi-nology/rainbow_bridge/pkg/redis"
	"gopkg.in/yaml.v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Opt-in only. The SSH harness supplies a private remote config, unique Redis
// process, and disposable work root. This test never migrates the business schema.
func TestBackendRemoteComponents(t *testing.T) {
	path := os.Getenv("RB_REMOTE_COMPONENT_CONFIG")
	if path == "" {
		t.Skip("requires owned remote component fixture")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("private fixture config unavailable")
	}
	var cfg appconfig.Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal("fixture config invalid")
	}
	id := strings.ReplaceAll(uuid.NewString(), "-", "")
	schema := "rbqa_" + id
	bucket := "rbqa-" + id
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	ctx = WithAdminIdentity(ctx, AdminIdentity{UserID: 1, Username: "owned-test", Role: "admin"})
	pgcfg, err := pgx.ParseConfig(cfg.Database.Postgres.DSN)
	if err != nil {
		t.Fatal("PG config parse failed")
	}
	admin, err := pgx.ConnectConfig(ctx, pgcfg)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) {
			t.Fatalf("remote PG connection unavailable, SQLSTATE=%s", pgError.Code)
		}
		var types []string
		for current := err; current != nil; current = errors.Unwrap(current) {
			types = append(types, fmt.Sprintf("%T", current))
		}
		t.Fatalf("remote PG connection unavailable host=%s port=%d error-types=%v", pgcfg.Host, pgcfg.Port, types)
	}
	defer admin.Close(context.Background())
	manifest := map[string]any{"schema": schema, "bucket": bucket, "redis": "dedicated loopback process", "schemaCleanup": false, "bucketCleanup": false}
	defer func() {
		payload, _ := json.MarshalIndent(manifest, "", "  ")
		os.WriteFile("component-cleanup.json", payload, 0600)
	}()
	// Record intent before creating either resource.
	payload, _ := json.Marshal(manifest)
	os.WriteFile("component-cleanup.json", payload, 0600)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal("permission to create isolated PG schema unavailable")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE")
		manifest["schemaCleanup"] = err == nil
		if err != nil {
			t.Error("owned schema cleanup failed")
		}
	}()
	pgcfg.RuntimeParams["search_path"] = schema
	sqlDB := stdlib.OpenDB(*pgcfg)
	defer sqlDB.Close()
	conn, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("isolated PG connection failed")
	}
	if err := conn.AutoMigrate(&model.Config{}, &model.Asset{}, &model.Environment{}, &model.Pipeline{}, &model.ConfigFreezeState{}, &model.ConfigFreezeEvent{}, &model.ConfigWriteGate{}, &model.ConfigReleaseVersion{}, &model.ConfigReleaseAsset{}, &model.ConfigReleaseHead{}, &model.ConfigReleaseEvent{}, &model.ReleaseStorageAttempt{}, &model.ScopeLifecycleEvent{}, &model.AuthUser{}, &model.AuthSession{}); err != nil {
		t.Fatal("isolated schema migration failed")
	}
	if err := configdb.MigrateScopeIdentity(ctx, conn); err != nil {
		t.Fatal("isolated identity initialization failed")
	}
	client := redisclient.NewClient(&redisclient.Options{Addr: cfg.Redis.Address, DB: cfg.Redis.DB})
	defer client.Close()
	if n, err := client.DBSize(ctx).Result(); err != nil || n != 0 {
		t.Fatal("dedicated Redis is unavailable or not empty; refusing fixture")
	}
	cfg.Storage.Minio.Bucket = bucket
	s := NewService(conn, client, "/rainbow-bridge", &cfg)
	if err := s.EnsureFreezeBootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	minioClient, err := s.releaseMinioClient()
	if err != nil {
		t.Fatal("MinIO config unavailable")
	}
	if exists, err := minioClient.BucketExists(ctx, bucket); err != nil || exists {
		t.Fatal("cannot exclusively own test bucket")
	}
	if err := minioClient.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: cfg.Storage.Minio.Region}); err != nil {
		t.Fatal("permission for independent MinIO bucket unavailable")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		failed := false
		for item := range minioClient.ListObjects(cleanup, bucket, minio.ListObjectsOptions{Recursive: true}) {
			if item.Err != nil {
				failed = true
				continue
			}
			if err := minioClient.RemoveObject(cleanup, bucket, item.Key, minio.RemoveObjectOptions{}); err != nil {
				failed = true
			}
		}
		if err := minioClient.RemoveBucket(cleanup, bucket); err != nil {
			failed = true
		}
		manifest["bucketCleanup"] = !failed
		if failed {
			t.Error("owned bucket cleanup failed")
		}
	}()
	work, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(releaseDirEnv, filepath.Join(work, "owned-snapshots"))
	seedFreezeScope(t, s, "qa_"+id, "default")
	seedFreezeScope(t, s, "target_"+id, "default")
	env, targetEnv := "qa_"+id, "target_"+id
	checkList := func(e string, want int) {
		t.Helper()
		rows, err := s.ListConfigs(ctx, e, "default", "", "", "", false)
		if err != nil || len(rows) != want {
			t.Fatalf("immediate cached list expected=%d actual=%d error=%v", want, len(rows), err)
		}
	}
	checkList(env, 0)
	checkList(targetEnv, 0)
	checkFiltered := func(typ string, expected ...*common.ResourceConfig) {
		t.Helper()
		rows, err := s.ListConfigs(ctx, env, "default", typ, "", "", false)
		if err != nil || len(rows) != len(expected) {
			t.Fatalf("type=%q latest=false expected=%d actual=%d error=%v", typ, len(expected), len(rows), err)
		}
		want := make(map[string]*common.ResourceConfig, len(expected))
		for _, item := range expected {
			want[item.Alias] = item
		}
		for _, item := range rows {
			baseline, ok := want[item.Alias]
			if !ok || item.ResourceKey != baseline.ResourceKey || item.Content != baseline.Content || item.EditRevision != baseline.EditRevision {
				t.Fatalf("type=%q returned unexpected member/content/edit_revision alias=%q", typ, item.Alias)
			}
			delete(want, item.Alias)
		}
		if len(want) != 0 {
			t.Fatalf("type=%q missing expected members", typ)
		}
	}
	checkFiltered("text")
	checkFiltered("object")
	created, err := s.AddConfig(ctx, buildConfig(env, "default", "TEXT", "text", "before"))
	if err != nil {
		t.Fatal(err)
	}
	checkList(env, 1)
	checkFiltered("text", created)
	checkFiltered("object")
	if _, err := s.logic.GetConfig(ctx, env, "default", created.ResourceKey); err != nil {
		t.Fatal(err)
	}
	if _, err := s.logic.ListConfigsAsMap(ctx, env, "default"); err != nil {
		t.Fatal(err)
	}
	created.Content = "after"
	updated, err := s.UpdateConfig(ctx, created, created.EditRevision)
	if err != nil {
		t.Fatal(err)
	}
	fromCache, err := s.logic.GetConfig(ctx, env, "default", created.ResourceKey)
	if err != nil || fromCache.Content != "after" || fromCache.EditRevision != updated.EditRevision {
		t.Fatal("cached detail was not invalidated")
	}
	mapping, err := s.logic.ListConfigsAsMap(ctx, env, "default")
	if err != nil || mapping["TEXT"] != "after" {
		t.Fatal("map cache was not invalidated")
	}
	checkFiltered("text", updated)
	checkFiltered("object")
	imported, err := s.ImportConfigsWithResult(ctx, []*common.ResourceConfig{buildConfig(env, "default", "JSON", "object", `{"big":900719925474099312345}`)}, true)
	if err != nil || len(imported) != 1 {
		t.Fatal(err)
	}
	checkFiltered("text", updated)
	checkFiltered("object", imported[0])
	checkList(env, 2)
	rows, err := s.logic.configDAO.ListByEnvironmentAndPipelineWithFilter(ctx, conn, env, "default", "", "", "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := s.writeConfigArchive(ctx, rows)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportConfigsArchive(ctx, archive, targetEnv, "default", false); err != nil {
		t.Fatal(err)
	}
	checkList(targetEnv, 2)
	if _, err := s.ImportConfigsSelective(ctx, archive, "configs.zip", []*transfer.ExportSelection{{EnvironmentKey: env, PipelineKey: "default"}}, true); err != nil {
		t.Fatal(err)
	}
	checkList(env, 2)
	if _, err := s.MigrateConfigs(ctx, &transfer.MigrateRequest{SourceEnvironmentKey: env, SourcePipelineKey: "default", TargetEnvironmentKey: targetEnv, TargetPipelineKey: "default", Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	checkList(targetEnv, 2)
	otherScope, err := s.GetPipeline(ctx, targetEnv, "default")
	if err != nil {
		t.Fatal(err)
	}
	otherKey := fmt.Sprintf("%s::::false", appredis.GenerateScopedConfigListPrefix(otherScope.ScopeId))
	if n, _ := client.Exists(ctx, otherKey).Result(); n != 1 {
		t.Fatal("other scope cache was not warmed")
	}
	// Imports may refresh revisions; compare exact persisted identities before
	// warming both type caches for delete, rather than accepting absent TEXT.
	textBeforeDelete, err := s.logic.GetConfig(ctx, env, "default", created.ResourceKey)
	if err != nil {
		t.Fatal(err)
	}
	objectBeforeDelete, err := s.logic.GetConfig(ctx, env, "default", imported[0].ResourceKey)
	if err != nil {
		t.Fatal(err)
	}
	checkFiltered("text", modelConfigToPB(textBeforeDelete))
	checkFiltered("object", modelConfigToPB(objectBeforeDelete))
	if err := s.DeleteConfig(ctx, env, "default", created.ResourceKey); err != nil {
		t.Fatal(err)
	}
	checkList(env, 1)
	checkFiltered("text")
	checkFiltered("object", modelConfigToPB(objectBeforeDelete))
	if n, _ := client.Exists(ctx, otherKey).Result(); n != 1 {
		t.Fatal("write evicted another scope")
	}
	if _, err := s.logic.GetConfig(ctx, env, "default", created.ResourceKey); err == nil {
		t.Fatal("deleted detail was cached")
	}
	if view, err := s.logic.ListConfigsAsMap(ctx, env, "default"); err != nil || view["TEXT"] != nil {
		t.Fatal("deleted key remained in map cache")
	}
	manifest["cacheReadback"] = "create/update/delete/json/archive/selective/migrate/detail/filter/map passed without TTL wait or manual deletion"
	t.Log("A: real PostgreSQL + dedicated Redis immediate cache readback passed; latest=false text/object exact membership, content and edit_revision after update/delete passed")

	// Real object bytes, not just resource-free MinIO configuration.
	image, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")
	for _, input := range []struct {
		alias, name, typ, contentType string
		data                          []byte
	}{{"IMG", "pixel.png", "image", "image/png", image}, {"FILE", "note.txt", "file", "text/plain", []byte("published text")}} {
		_, reference, err := s.UploadAsset(ctx, &FileUploadInput{EnvironmentKey: env, PipelineKey: "default", FileName: input.name, ContentType: input.contentType, Data: input.data})
		if err != nil {
			t.Fatal("owned MinIO upload failed")
		}
		if _, err := s.AddConfig(ctx, buildConfig(env, "default", input.alias, input.typ, reference)); err != nil {
			t.Fatal(err)
		}
	}
	freeze := mustLock(t, s, env, "default", "owned remote test")
	input := ReleaseCreateInput{EnvironmentKey: env, PipelineKey: "default", ExpectedFreezeRevisionRaw: fmt.Sprint(freeze.Revision), RequestID: uuid.NewString(), AutoOnly: true}
	version, err := s.CreateRelease(ctx, input)
	if err != nil {
		t.Fatal("MinIO release creation:", err)
	}
	if version.Meta.Version != "v1" || version.Meta.AssetCount != 2 {
		t.Fatalf("unexpected release metadata: %+v", version.Meta)
	}
	if active, err := s.ReleaseActive(ctx, env, "default"); err != nil || active != nil {
		t.Fatal("create unexpectedly activated")
	}
	replay, err := s.CreateRelease(ctx, input)
	if err != nil || !replay.Replayed || replay.Meta.SnapshotID != version.Meta.SnapshotID {
		t.Fatal("create replay changed snapshot")
	}
	if _, err := s.ActivateRelease(ctx, ReleaseActivateInput{EnvironmentKey: env, PipelineKey: "default", Version: version.Meta.Version, ExpectedActiveRevisionRaw: "0", RequestID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	runtime, err := s.RuntimeRelease(ctx, env, "default", nil)
	if err != nil || runtime.SnapshotID != version.Meta.SnapshotID {
		t.Fatal("active runtime mismatch")
	}
	var assets []model.ConfigReleaseAsset
	if err := conn.Where("snapshot_id = ?", version.Meta.SnapshotID).Find(&assets).Error; err != nil {
		t.Fatal(err)
	}
	storage, err := s.releaseStorage()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		b, err := storage.readAssetBytes(asset.SnapshotID, asset.AssetID, asset.Size, asset.Sha256)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != asset.Sha256 {
			t.Fatal("copied digest mismatch")
		}
	}
	var original []model.Asset
	conn.Where("scope_id = ?", freeze.ScopeID).Find(&original)
	for _, asset := range original {
		if err := minioClient.RemoveObject(ctx, bucket, asset.Path, minio.RemoveObjectOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	missingInput := input
	missingInput.RequestID = uuid.NewString()
	if _, err := s.CreateRelease(ctx, missingInput); err == nil {
		t.Fatal("missing MinIO originals accepted into another version")
	} else {
		requireReleaseErrorType(t, err, ErrTypeReleaseResourceInvalid)
	}
	if active, err := s.ReleaseActive(ctx, env, "default"); err != nil || active == nil || active.SnapshotID != version.Meta.SnapshotID {
		t.Fatal("failed source capture changed active")
	}
	// Re-open service and the same persistent snapshot directory, not the source objects.
	reopened := NewService(conn, client, "/rainbow-bridge", &cfg)
	fixed := version.Meta.Version
	old, err := reopened.RuntimeRelease(ctx, env, "default", &fixed)
	if err != nil || old.PayloadSha256 != runtime.PayloadSha256 {
		t.Fatal("fixed snapshot changed after deleting originals")
	}
	export, err := reopened.ReleaseStaticExport(ctx, ReleaseStaticExportInput{EnvironmentKey: env, PipelineKey: "default", Version: fixed, ExpectedSnapshotID: version.Meta.SnapshotID})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(export.TempPath)
	zipBytes, err := os.ReadFile(export.TempPath)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatal(err)
	}
	if len(z.File) < 4 {
		t.Fatal("static zip lacks immutable resources")
	}
	for _, file := range z.File {
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(io.LimitReader(stream, 1<<20))
		stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		if file.Name == "config.json" && !bytes.Contains(body, []byte("900719925474099312345")) {
			t.Fatal("large number lost in snapshot zip")
		}
	}
	manifest["releaseReadback"] = "MinIO image/text -> immutable v1, inactive create, replay, activate, fixed runtime and ZIP after deleting sources passed"
	manifest["snapshotDirectory"] = filepath.Join(work, "owned-snapshots")
	t.Log("B: real MinIO source image/text, immutable snapshot, activation, fixed runtime and ZIP passed")
	snapshotBucket, cleanupSnapshots := ownedSnapshotBucket(t, s, manifest)
	defer cleanupSnapshots()
	verifyRemoteSnapshotMigration(t, s, snapshotBucket, manifest)
	verifyRemoteCandidateHTTP(t, cfg, schema, work)
	manifest["candidateHTTP"] = "real registered backend HTTP including owned process restart passed"
}
