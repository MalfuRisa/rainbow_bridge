package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/pkg/config"
	"gorm.io/gorm"
)

type snapshotS3Fixture struct {
	mu                               sync.Mutex
	objects                          map[string][]byte
	incomplete                       map[string]string
	failPut, corruptRead, failDelete bool
	failAt, puts                     int
}

func snapshotS3(t *testing.T, s *Service) *snapshotS3Fixture {
	t.Helper()
	f := &snapshotS3Fixture{objects: map[string][]byte{}, incomplete: map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		key := strings.TrimPrefix(r.URL.Path, "/snapshots/")
		if r.Method == "GET" && (r.URL.Query().Has("list-type") || r.URL.Query().Has("uploads") || r.URL.Query().Has("versions")) {
			w.Header().Set("Content-Type", "application/xml")
			if r.URL.Query().Has("uploads") {
				fmt.Fprint(w, `<ListMultipartUploadsResult><Bucket>snapshots</Bucket><IsTruncated>false</IsTruncated>`)
				for k, id := range f.incomplete {
					if strings.HasPrefix(k, r.URL.Query().Get("prefix")) {
						var escaped bytes.Buffer
						xml.EscapeText(&escaped, []byte(k))
						fmt.Fprintf(w, `<Upload><Key>%s</Key><UploadId>%s</UploadId><Initiated>2026-01-01T00:00:00Z</Initiated></Upload>`, escaped.String(), id)
					}
				}
				fmt.Fprint(w, `</ListMultipartUploadsResult>`)
				return
			}
			root, entry := "ListBucketResult", "Contents"
			if r.URL.Query().Has("versions") {
				root, entry = "ListVersionsResult", "Version"
			}
			fmt.Fprintf(w, `<%s><Name>snapshots</Name><IsTruncated>false</IsTruncated>`, root)
			for k, b := range f.objects {
				if strings.HasPrefix(k, r.URL.Query().Get("prefix")) {
					var escaped bytes.Buffer
					xml.EscapeText(&escaped, []byte(k))
					fmt.Fprintf(w, `<%s><Key>%s</Key><VersionId>null</VersionId><IsLatest>true</IsLatest><Size>%d</Size><LastModified>2026-01-01T00:00:00Z</LastModified><ETag>"fixture"</ETag></%s>`, entry, escaped.String(), len(b), entry)
				}
			}
			fmt.Fprintf(w, `</%s>`, root)
			return
		}
		if r.Method == "PUT" {
			f.puts++
			if f.failPut || (f.failAt > 0 && f.puts == f.failAt) {
				w.WriteHeader(403)
				fmt.Fprint(w, `<Error><Code>AccessDenied</Code></Error>`)
				return
			}
			var body io.Reader = r.Body
			if strings.Contains(r.Header.Get("Content-Encoding"), "aws-chunked") || strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
				decoded := bytes.Buffer{}
				chunks := bufio.NewReader(r.Body)
				for {
					line, err := chunks.ReadString('\n')
					if err != nil {
						w.WriteHeader(500)
						return
					}
					size, err := strconv.ParseInt(strings.Split(strings.TrimSpace(line), ";")[0], 16, 64)
					if err != nil || size < 0 || size > model.ReleaseMaxAssetByte {
						w.WriteHeader(500)
						return
					}
					if size == 0 {
						break
					}
					if _, err := io.CopyN(&decoded, chunks, size); err != nil {
						w.WriteHeader(500)
						return
					}
					chunks.ReadString('\n')
				}
				body = &decoded
			}
			b, err := io.ReadAll(io.LimitReader(body, model.ReleaseMaxAssetByte+1))
			if err != nil {
				w.WriteHeader(500)
				return
			}
			f.objects[key] = b
			w.Header().Set("ETag", `"fixture"`)
			return
		}
		if r.Method == "DELETE" {
			if f.failDelete {
				w.WriteHeader(403)
				fmt.Fprint(w, `<Error><Code>AccessDenied</Code></Error>`)
				return
			}
			if r.URL.Query().Has("uploadId") {
				delete(f.incomplete, key)
				w.WriteHeader(204)
				return
			}
			delete(f.objects, key)
			w.WriteHeader(204)
			return
		}
		b, ok := f.objects[key]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(404)
			fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
			return
		}
		w.Header().Set("ETag", `"fixture"`)
		w.Header().Set("Last-Modified", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(http.TimeFormat))
		w.Header().Set("Content-Length", fmt.Sprint(len(b)))
		if r.Method != "HEAD" {
			if f.corruptRead {
				b = bytes.Repeat([]byte("x"), len(b))
			}
			w.Write(b)
		}
	}))
	t.Cleanup(server.Close)
	s.config = &config.Config{Storage: config.StorageConfig{Type: "local", Minio: config.MinioConfig{Endpoint: strings.TrimPrefix(server.URL, "http://"), AccessKey: "fixture", SecretKey: "fixture-only", Bucket: "originals", Region: "us-east-1"}}, ReleaseStorage: config.ReleaseStorageConfig{Type: "minio", Bucket: "snapshots"}}
	return f
}

func TestSnapshotMinIOCommitReadAndCleanupGuard(t *testing.T) {
	f := newReleaseFixture(t)
	fixture := snapshotS3(t, f.svc)
	// No access to a permanent snapshot root is required in MinIO mode.
	t.Setenv(releaseDirEnv, "not-an-absolute-permanent-directory")
	request := uuid.NewString()
	created, err := f.create(t, "v1", "1", request)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.create(t, "v1", "1", request)
	if err != nil || replay.Meta.SnapshotID != created.Meta.SnapshotID {
		t.Fatal("replay changed snapshot", err)
	}
	var row model.ConfigReleaseVersion
	f.svc.logic.db.First(&row, "snapshot_id = ?", created.Meta.SnapshotID)
	if row.StorageBackend != "minio" || !validSnapshotLocator(&row) {
		t.Fatal("locator not persisted")
	}
	if err := f.svc.ReconcileSnapshotAttempt(adminTestContext(), row.StorageAttempt); err == nil {
		t.Fatal("reconcile deleted committed snapshot")
	}
	if _, err := f.activate(t, "v1", "0", ""); err != nil {
		t.Fatal(err)
	}
	assets, err := f.svc.logic.releaseDAO.ListAssets(adminTestContext(), f.svc.logic.db, row.SnapshotID)
	if err != nil || len(assets) != 1 {
		t.Fatal(err)
	}
	_, payload, err := f.svc.ReleaseAssetDownload(context.Background(), row.SnapshotID, assets[0].AssetID)
	if err != nil || string(payload) != "PNG-A-BYTES" {
		t.Fatal("download", err)
	}
	fixture.mu.Lock()
	fixture.corruptRead = true
	fixture.mu.Unlock()
	if _, _, err := f.svc.ReleaseAssetDownload(context.Background(), row.SnapshotID, assets[0].AssetID); err == nil {
		t.Fatal("corruption accepted")
	}
}

func TestSnapshotMinIOFailureAndDurableAttempt(t *testing.T) {
	for _, mode := range []string{"upload", "readback", "db-rollback", "cleanup-pending"} {
		t.Run(mode, func(t *testing.T) {
			f := newReleaseFixture(t)
			fixture := snapshotS3(t, f.svc)
			switch mode {
			case "upload":
				fixture.failAt = 2
			case "readback":
				fixture.corruptRead = true
			case "cleanup-pending":
				fixture.failAt = 2
				fixture.failDelete = true
			case "db-rollback":
				f.svc.logic.db.Callback().Create().Before("gorm:create").Register("test:rollback-release", func(tx *gorm.DB) {
					if tx.Statement.Table == "config_release_version" {
						tx.AddError(fmt.Errorf("injected rollback"))
					}
				})
			}
			if _, err := f.create(t, "v1", "1", ""); err == nil {
				t.Fatal("failure accepted")
			}
			var versions int64
			f.svc.logic.db.Model(&model.ConfigReleaseVersion{}).Count(&versions)
			if versions != 0 {
				t.Fatal("partial version visible")
			}
			var attempts []model.ReleaseStorageAttempt
			f.svc.logic.db.Find(&attempts)
			if len(attempts) != 1 {
				t.Fatal("durable ownership missing")
			}
			if mode == "cleanup-pending" {
				if attempts[0].State != "cleanup_pending" {
					t.Fatalf("missing pending cleanup: %s", attempts[0].State)
				}
				fixture.mu.Lock()
				fixture.failDelete = false
				fixture.mu.Unlock()
				if err := f.svc.ReconcileSnapshotAttempt(adminTestContext(), attempts[0].ID); err != nil {
					t.Fatal(err)
				}
			} else if attempts[0].State != "cleaned" {
				t.Fatalf("cleanup incomplete: %s", attempts[0].State)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if len(fixture.objects) != 0 {
				t.Fatal("orphan objects left")
			}
		})
	}
}

func TestSnapshotMigrationMixedHistoryAndRollback(t *testing.T) {
	f := newReleaseFixture(t)
	one, err := f.create(t, "v1", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.activate(t, "v1", "0", ""); err != nil {
		t.Fatal(err)
	}
	two, err := f.create(t, "v2", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	snapshotS3(t, f.svc)
	var head model.ConfigReleaseHead
	f.svc.logic.db.First(&head)
	for _, id := range []string{one.Meta.SnapshotID, two.Meta.SnapshotID} {
		if err := f.svc.MigrateSnapshotStorage(adminTestContext(), id); err != nil {
			t.Fatal(err)
		}
		if err := f.svc.MigrateSnapshotStorage(adminTestContext(), id); err != nil {
			t.Fatal("rerun", err)
		}
	}
	var after model.ConfigReleaseHead
	f.svc.logic.db.First(&after)
	if after.ActiveSnapshotID != head.ActiveSnapshotID || after.Revision != head.Revision {
		t.Fatal("migration changed active")
	}
	if err := f.svc.RollbackSnapshotStorage(adminTestContext(), one.Meta.SnapshotID); err != nil {
		t.Fatal(err)
	}
	var row model.ConfigReleaseVersion
	f.svc.logic.db.First(&row, "snapshot_id = ?", one.Meta.SnapshotID)
	if row.StorageBackend != "local" {
		t.Fatal("rollback failed")
	}
	// Missing history must fail, never reconstructed from original files.
	local, _ := f.svc.releaseStorage()
	if err := os.RemoveAll(local.finalDir(one.Meta.SnapshotID)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.MigrateSnapshotStorage(adminTestContext(), one.Meta.SnapshotID); err == nil {
		t.Fatal("missing local history was accepted")
	}
	f.svc.logic.db.First(&row, "snapshot_id = ?", one.Meta.SnapshotID)
	if row.StorageBackend != "local" {
		t.Fatal("failed migration changed locator")
	}
}

func TestSnapshotCleanupClaimRejectsQueuedCreate(t *testing.T) {
	f := newReleaseFixture(t)
	snapshotS3(t, f.svc)
	a, err := f.svc.registerSnapshotAttempt(adminTestContext(), uuid.NewString(), uuid.NewString(), "fingerprint", "scope", "create")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ReconcileSnapshotAttempt(adminTestContext(), a.ID); err != nil {
		t.Fatal(err)
	}
	err = f.svc.withWriteGate(adminTestContext(), func(ctx context.Context, l *Logic, _ *gorm.DB) error { return checkSnapshotAttempt(ctx, l, a) })
	if err == nil {
		t.Fatal("cleaned attempt can publish")
	}
}

func TestSnapshotPlanDoesNotCreateMissingLocalRoot(t *testing.T) {
	f := newReleaseFixture(t)
	if _, err := f.create(t, "v1", "1", ""); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "absent-history")
	t.Setenv(releaseDirEnv, root)
	plan, err := f.svc.InspectSnapshotStorage(adminTestContext())
	if err != nil || len(plan.Versions) != 1 || plan.Versions[0].Status != "verification_failed" {
		t.Fatal("missing local history plan", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("read-only inventory created local root")
	}
}

func TestSnapshotPlanSeparatesOwnedHistoryFromUncommittedDirectories(t *testing.T) {
	f := newReleaseFixture(t)
	created, err := f.create(t, "v1", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	local, err := f.svc.releaseStorage()
	if err != nil {
		t.Fatal(err)
	}
	orphan, staged := uuid.NewString(), uuid.NewString()
	if err := os.MkdirAll(local.finalDir(orphan), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(local.stagingDir(staged), 0700); err != nil {
		t.Fatal(err)
	}
	plan, err := f.svc.InspectSnapshotStorage(adminTestContext())
	if err != nil {
		t.Fatal(err)
	}
	if plan.LocalDirectories.Status != "verified" || len(plan.LocalDirectories.Uncommitted) != 1 || plan.LocalDirectories.Uncommitted[0] != orphan || len(plan.LocalDirectories.Staging) != 1 || plan.LocalDirectories.Staging[0] != staged {
		t.Fatalf("bad directory classification: %+v", plan.LocalDirectories)
	}
	if _, err := os.Stat(local.finalDir(created.Meta.SnapshotID)); err != nil {
		t.Fatal("plan changed history")
	}
}

func TestSnapshotReconcileAbortsOnlyOwnedMultipart(t *testing.T) {
	f := newReleaseFixture(t)
	fixture := snapshotS3(t, f.svc)
	a, err := f.svc.registerSnapshotAttempt(adminTestContext(), uuid.NewString(), uuid.NewString(), "fingerprint", "scope", "create")
	if err != nil {
		t.Fatal(err)
	}
	owned := a.Prefix + "assets/" + uuid.NewString() + ".bin"
	fixture.mu.Lock()
	fixture.incomplete[owned] = "owned-upload"
	fixture.incomplete["another-attempt/assets/foreign.bin"] = "foreign-upload"
	fixture.failDelete = true
	fixture.mu.Unlock()
	if err := f.svc.ReconcileSnapshotAttempt(adminTestContext(), a.ID); err == nil {
		t.Fatal("abort failure reported success")
	}
	var saved model.ReleaseStorageAttempt
	f.svc.logic.db.First(&saved, "id = ?", a.ID)
	if saved.State != "cleanup_pending" {
		t.Fatal("abort failure lost durable pending")
	}
	fixture.mu.Lock()
	fixture.failDelete = false
	fixture.mu.Unlock()
	if err := f.svc.ReconcileSnapshotAttempt(adminTestContext(), a.ID); err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if _, ok := fixture.incomplete[owned]; ok || len(fixture.incomplete) != 1 {
		t.Fatal("multipart cleanup crossed ownership or left own upload")
	}
}

func TestSnapshotMinIOEmptyAndResourceFreeManifest(t *testing.T) {
	for _, text := range []bool{false, true} {
		t.Run(fmt.Sprint(text), func(t *testing.T) {
			s := newFreezeService(t)
			seedFreezeScope(t, s, "empty-minio", "default")
			fixture := snapshotS3(t, s)
			if text {
				if _, err := s.AddConfig(adminTestContext(), buildConfig("empty-minio", "default", "TEXT", "text", "only text")); err != nil {
					t.Fatal(err)
				}
			}
			mustLock(t, s, "empty-minio", "default", "empty manifest")
			created, err := s.CreateRelease(adminTestContext(), ReleaseCreateInput{EnvironmentKey: "empty-minio", PipelineKey: "default", ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString()})
			if err != nil || created.Meta.AssetCount != 0 {
				t.Fatal("resource-free MinIO snapshot", err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if len(fixture.objects) != 1 {
				t.Fatal("empty snapshot must have exactly one completeness manifest")
			}
		})
	}
}

func TestSnapshotCancelledCaptureNeverPublishes(t *testing.T) {
	f := newReleaseFixture(t)
	fixture := snapshotS3(t, f.svc)
	ctx, cancel := context.WithCancel(adminTestContext())
	defer cancel()
	releaseCopyPauseHookForTest = func(string) { cancel() }
	defer func() { releaseCopyPauseHookForTest = nil }()
	if _, err := f.svc.CreateRelease(ctx, ReleaseCreateInput{EnvironmentKey: "qa_prod", PipelineKey: "main", ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString()}); err == nil {
		t.Fatal("cancelled publication succeeded")
	}
	var versions int64
	f.svc.logic.db.Model(&model.ConfigReleaseVersion{}).Count(&versions)
	if versions != 0 {
		t.Fatal("cancel created version")
	}
	var attempts []model.ReleaseStorageAttempt
	f.svc.logic.db.Find(&attempts)
	if len(attempts) != 1 || attempts[0].State != "cleaned" {
		t.Fatal("cancel did not preserve/clean ownership")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.objects) != 0 {
		t.Fatal("cancelled capture left objects")
	}
}

func TestSnapshotHistoryIgnoresGlobalBackendAndRejectsUnknown(t *testing.T) {
	f := newReleaseFixture(t)
	fixture := snapshotS3(t, f.svc)
	created, err := f.create(t, "v1", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	f.svc.config.ReleaseStorage.Type = "local"
	f.svc.config.ReleaseStorage.Bucket = "different-new-bucket"
	if _, err := f.svc.RuntimeRelease(adminTestContext(), "qa_prod", "main", strPtr("v1")); err != nil {
		t.Fatal("history followed global setting", err)
	}
	var row model.ConfigReleaseVersion
	f.svc.logic.db.First(&row, "snapshot_id = ?", created.Meta.SnapshotID)
	fixture.mu.Lock()
	delete(fixture.objects, row.StoragePrefix+"manifest.json")
	fixture.mu.Unlock()
	if _, err := f.svc.RuntimeRelease(adminTestContext(), "qa_prod", "main", strPtr("v1")); err == nil {
		t.Fatal("missing remote manifest fell back to original/local")
	}
	f.svc.logic.db.Model(&row).Update("storage_backend", "unknown")
	if _, err := f.svc.RuntimeRelease(adminTestContext(), "qa_prod", "main", strPtr("v1")); err == nil {
		t.Fatal("unknown backend accepted")
	}
}
