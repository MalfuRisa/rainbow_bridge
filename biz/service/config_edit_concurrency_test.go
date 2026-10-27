package service

import (
	"context"
	"encoding/json"
	"errors"
	"google.golang.org/protobuf/proto"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	redisclient "github.com/redis/go-redis/v9"
	configdb "github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	"github.com/yi-nology/rainbow_bridge/biz/model/transfer"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func editFixture(t *testing.T) (*Service, *common.ResourceConfig) {
	t.Helper()
	s := newFreezeService(t)
	seedFreezeScope(t, s, "edit", "default")
	row, err := s.AddConfig(adminTestContext(), buildConfig("edit", "default", "TITLE", "text", "A"))
	if err != nil {
		t.Fatal(err)
	}
	return s, row
}
func editRead(t *testing.T, s *Service, row *common.ResourceConfig) *common.ResourceConfig {
	t.Helper()
	got, err := s.GetConfigDetail(context.Background(), row.EnvironmentKey, row.PipelineKey, row.ResourceKey)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func TestConfigEditBaselinesAndABA(t *testing.T) {
	s, row := editFixture(t)
	r1 := row.EditRevision
	if !configdb.ValidEditRevision(r1) {
		t.Fatal("invalid creation token")
	}
	a, b := editRead(t, s, row), editRead(t, s, row)
	a.Content = "B"
	saved, err := s.UpdateConfig(adminTestContext(), a, r1)
	if err != nil || saved.EditRevision == r1 || saved.Content != "B" {
		t.Fatalf("save=%v err=%v", saved, err)
	}
	b.Content = "C"
	if _, err := s.UpdateConfig(adminTestContext(), b, r1); !errors.Is(err, configdb.ErrEditConflict) {
		t.Fatalf("stale: %v", err)
	}
	if got := editRead(t, s, row); got.Content != "B" || got.EditRevision != saved.EditRevision {
		t.Fatal("stale writer changed row")
	}
	// Same content and A -> B -> A are still new editing revisions.
	same, err := s.UpdateConfig(adminTestContext(), saved, saved.EditRevision)
	if err != nil || same.EditRevision == saved.EditRevision {
		t.Fatal("same content did not rotate")
	}
	same.Content = "A"
	reverted, err := s.UpdateConfig(adminTestContext(), same, same.EditRevision)
	if err != nil || reverted.EditRevision == r1 {
		t.Fatal("ABA token reused")
	}
	if err := s.DeleteConfig(adminTestContext(), row.EnvironmentKey, row.PipelineKey, row.ResourceKey); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateConfig(adminTestContext(), b, r1); !errors.Is(err, configdb.ErrEditTargetNotFound) {
		t.Fatalf("delete: %v", err)
	}
	recreated, err := s.AddConfig(adminTestContext(), row)
	if err != nil || recreated.EditRevision == r1 {
		t.Fatalf("recreate: %v", err)
	}
	if _, err := s.UpdateConfig(adminTestContext(), b, r1); !errors.Is(err, configdb.ErrEditConflict) {
		t.Fatalf("recreate stale: %v", err)
	}
}

func TestConfigEditIndependentConnections(t *testing.T) {
	s, row := editFixture(t)
	var databases []struct {
		Name string
		File string
		Seq  int
	}
	if err := s.logic.db.Raw("PRAGMA database_list").Scan(&databases).Error; err != nil {
		t.Fatal(err)
	}
	var path string
	for _, d := range databases {
		if d.Name == "main" {
			path = d.File
		}
	}
	if path == "" {
		t.Fatal("not a shared file SQLite")
	}
	conn, err := gorm.Open(sqlite.Open(path+"?_pragma=busy_timeout(10000)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := conn.DB()
	defer sqlDB.Close()
	other := NewService(conn, nil, "/rainbow-bridge", nil)
	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for i, service := range []*Service{s, other} {
		go func(i int, service *Service) {
			cfg := proto.Clone(row).(*common.ResourceConfig)
			cfg.Content = []string{"first", "second"}[i]
			ready.Done()
			<-start
			_, err := service.UpdateConfig(adminTestContext(), cfg, row.EditRevision)
			results <- err
		}(i, service)
	}
	ready.Wait()
	close(start)
	first, second := <-results, <-results
	if !((first == nil && errors.Is(second, configdb.ErrEditConflict)) || (second == nil && errors.Is(first, configdb.ErrEditConflict))) {
		t.Fatalf("must be exactly success/conflict: %v / %v", first, second)
	}
	t.Log("two independent connection pools to one SQLite file: exactly one committed edit and one conflict")
}

func TestConfigEditValidationFreezeAndRollback(t *testing.T) {
	s, row := editFixture(t)
	ctx := adminTestContext()
	for _, revision := range []string{"", "bad", strings.ToUpper(row.EditRevision), uuid.NewString() + " ", "00000000-0000-1000-8000-000000000000"} {
		if _, err := s.UpdateConfig(ctx, row, revision); !errors.Is(err, configdb.ErrEditRevisionInvalid) {
			t.Fatalf("bad revision accepted: %q %v", revision, err)
		}
		if err := s.logic.UpdateConfig(ctx, pbConfigToModel(row), revision); !errors.Is(err, configdb.ErrEditRevisionInvalid) {
			t.Fatalf("logic bypass: %v", err)
		}
	}
	invalid := proto.Clone(row).(*common.ResourceConfig)
	invalid.Type = "object"
	invalid.Content = "null"
	if _, err := s.UpdateConfig(ctx, invalid, row.EditRevision); err == nil {
		t.Fatal("bad content accepted")
	}
	if got := editRead(t, s, row); got.EditRevision != row.EditRevision {
		t.Fatal("validation rotated token")
	}
	mustLock(t, s, "edit", "default", "check priority")
	_, err := s.UpdateConfig(ctx, row, uuid.NewString())
	requireFreezeErrorType(t, err, ErrTypeScopeFrozen)
	// Existing lock/unlock operations do not edit the content revision.
	if got := editRead(t, s, row); got.EditRevision != row.EditRevision {
		t.Fatal("freeze rotated token")
	}
}

func TestConfigEditImportsAndMigrate(t *testing.T) {
	for _, mode := range []string{"json", "archive", "selective", "migrate"} {
		t.Run(mode, func(t *testing.T) {
			s, row := editFixture(t)
			seedFreezeScope(t, s, "source", "default")
			source, err := s.AddConfig(adminTestContext(), buildConfig("source", "default", "TITLE", "text", "source"))
			if err != nil {
				t.Fatal(err)
			}
			archive, err := s.writeConfigArchive(context.Background(), []model.Config{*pbConfigToModel(row)})
			if err != nil {
				t.Fatal(err)
			}
			apply := func(overwrite bool) error {
				switch mode {
				case "json":
					_, err = s.ImportConfigsWithResult(adminTestContext(), []*common.ResourceConfig{row}, overwrite)
				case "archive":
					_, err = s.ImportConfigsArchive(adminTestContext(), archive, "edit", "default", overwrite)
				case "selective":
					_, err = s.ImportConfigsSelective(adminTestContext(), archive, "configs.zip", []*transfer.ExportSelection{{EnvironmentKey: "edit", PipelineKey: "default"}}, overwrite)
				case "migrate":
					_, err = s.MigrateConfigs(adminTestContext(), &transfer.MigrateRequest{SourceEnvironmentKey: "source", SourcePipelineKey: "default", TargetEnvironmentKey: "edit", TargetPipelineKey: "default", Overwrite: overwrite})
				}
				return err
			}
			if err := apply(false); err != nil {
				t.Fatal(err)
			}
			if got := editRead(t, s, row); got.EditRevision != row.EditRevision {
				t.Fatal("skip rotated token")
			}
			if err := apply(true); err != nil {
				t.Fatal(err)
			}
			got := editRead(t, s, row)
			if !configdb.ValidEditRevision(got.EditRevision) || got.EditRevision == row.EditRevision || got.EditRevision == source.EditRevision {
				t.Fatal("overwrite did not create target token")
			}
			if _, err := s.UpdateConfig(adminTestContext(), row, row.EditRevision); !errors.Is(err, configdb.ErrEditConflict) {
				t.Fatalf("old editor accepted after %s: %v", mode, err)
			}
		})
	}
}

func TestConfigEditDetailAndOwnCommittedResponse(t *testing.T) {
	s, row := editFixture(t)
	hook := &importCacheHook{}
	client := redisclient.NewClient(&redisclient.Options{Addr: "unused"})
	client.AddHook(hook)
	defer client.Close()
	s.logic.redisClient = client
	if _, err := s.GetConfigDetail(context.Background(), row.EnvironmentKey, row.PipelineKey, row.ResourceKey); err != nil {
		t.Fatal(err)
	}
	if len(hook.calls) != 0 {
		t.Fatal("editing detail touched Redis")
	}
	// Cache invalidation executes after commit. A second writer can commit before
	// the first response is built; the first response must keep its own row.
	executed := false
	hook.check = func() {
		if executed {
			return
		}
		executed = true
		s.logic.redisClient = nil
		latest := editRead(t, s, row)
		latest.Content = "second"
		if _, err := s.UpdateConfig(adminTestContext(), latest, latest.EditRevision); err != nil {
			t.Fatal(err)
		}
	}
	first := proto.Clone(row).(*common.ResourceConfig)
	first.Content = "first"
	saved, err := s.UpdateConfig(adminTestContext(), first, row.EditRevision)
	if err != nil || saved.Content != "first" || !executed {
		t.Fatalf("own result: %+v %v", saved, err)
	}
	latest := editRead(t, s, row)
	if latest.Content != "second" || saved.EditRevision == latest.EditRevision {
		t.Fatal("returned later writer token")
	}
}

func TestConfigEditBackfillAndStartup(t *testing.T) {
	s, row := editFixture(t)
	conn := s.logic.db
	if err := conn.Model(&model.Config{}).Where("resource_key = ?", row.ResourceKey).UpdateColumn("edit_revision", "").Error; err != nil {
		t.Fatal(err)
	}
	var before model.Config
	conn.First(&before)
	if err := configdb.BackfillConfigRevisions(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	var after model.Config
	conn.First(&after)
	if !configdb.ValidEditRevision(after.EditRevision) || !before.UpdatedAt.Equal(after.UpdatedAt) || before.Content != after.Content {
		t.Fatal("bad backfill")
	}
	if err := configdb.BackfillConfigRevisions(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	if got := editRead(t, s, row); got.EditRevision != after.EditRevision {
		t.Fatal("restart rotated token")
	}
	// Only changed startup content/scope updates rotate tokens.
	img := model.Config{EnvironmentKey: "edit", PipelineKey: "default", ResourceKey: "legacy", Alias: "IMAGE", Type: "image", Content: "/rainbow-bridge/api/v1/asset/file/missing", EditRevision: uuid.NewString()}
	if err := conn.Create(&img).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateToFullAssetPaths(context.Background()); err != nil {
		t.Fatal(err)
	}
	var migrated model.Config
	conn.First(&migrated, img.ID)
	if migrated.Content != "/api/v1/asset/file/missing" || migrated.EditRevision == img.EditRevision {
		t.Fatal("content migration token unchanged")
	}
	if err := s.MigrateToFullAssetPaths(context.Background()); err != nil {
		t.Fatal(err)
	}
	var repeated model.Config
	conn.First(&repeated, img.ID)
	if repeated.EditRevision != migrated.EditRevision {
		t.Fatal("no-op migration changed token")
	}
}

func TestConfigEditSnapshotHashUnaffected(t *testing.T) {
	s, row := editFixture(t)
	scope, err := s.logic.environmentDAO.GetByKey(context.Background(), s.logic.db, "edit")
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := s.logic.pipelineDAO.GetByKey(context.Background(), s.logic.db, "edit", "default")
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.captureReleasePlan(context.Background(), s.logic, "edit", "default", scope, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateConfig(adminTestContext(), row, row.EditRevision); err != nil {
		t.Fatal(err)
	}
	after, err := s.captureReleasePlan(context.Background(), s.logic, "edit", "default", scope, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	if before.PayloadSha256 != after.PayloadSha256 || strings.Contains(after.Payload, "edit_revision") {
		t.Fatal("editing metadata changed release payload")
	}
}

func TestConfigEditAtomicFailureRollsBack(t *testing.T) {
	for _, mode := range []string{"zero", "multiple", "database-error"} {
		t.Run(mode, func(t *testing.T) {
			s, row := editFixture(t)
			before := editRead(t, s, row)
			name := "test:edit_failure"
			if mode == "multiple" {
				s.logic.db.Callback().Update().After("gorm:update").Register(name, func(tx *gorm.DB) {
					if tx.Statement.Table == "resource_config" {
						tx.RowsAffected = 2
					}
				})
			} else {
				s.logic.db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
					if tx.Statement.Table != "resource_config" {
						return
					}
					if mode == "database-error" {
						tx.AddError(errors.New("injected config write failure"))
						return
					}
					tx.Exec("UPDATE resource_config SET edit_revision = ? WHERE resource_key = ?", uuid.NewString(), row.ResourceKey)
				})
			}
			row.Content = "not committed"
			_, err := s.UpdateConfig(adminTestContext(), row, row.EditRevision)
			s.logic.db.Callback().Update().Remove(name)
			if err == nil {
				t.Fatal("injected failure accepted")
			}
			if mode == "zero" && !errors.Is(err, configdb.ErrEditConflict) {
				t.Fatalf("zero rows: %v", err)
			}
			after := editRead(t, s, row)
			if after.Content != before.Content || after.EditRevision != before.EditRevision {
				t.Fatal("failed transaction changed row or token")
			}
		})
	}
}

func TestConfigEditDifferentRows(t *testing.T) {
	s, first := editFixture(t)
	second, err := s.AddConfig(adminTestContext(), buildConfig("edit", "default", "OTHER", "text", "other"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateConfig(adminTestContext(), first, first.EditRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateConfig(adminTestContext(), second, second.EditRevision); err != nil {
		t.Fatal("unrelated row conflicted", err)
	}
}

func TestConfigEditBackfillProcess(t *testing.T) {
	file := os.Getenv("RB_EDIT_BACKFILL_DB")
	if file == "" {
		return
	}
	conn, err := gorm.Open(sqlite.Open(file+"?_pragma=busy_timeout(10000)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := conn.DB()
	defer sqlDB.Close()
	if err := os.WriteFile(os.Getenv("RB_EDIT_BACKFILL_READY"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(file + ".start"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("barrier timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := configdb.BackfillConfigRevisions(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
}
func TestConfigEditBackfillTwoProcesses(t *testing.T) {
	file := filepath.Join(t.TempDir(), "legacy.db")
	conn, err := gorm.Open(sqlite.Open(file+"?_pragma=busy_timeout(10000)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := conn.DB()
	defer sqlDB.Close()
	if err := conn.AutoMigrate(&model.Config{}); err != nil {
		t.Fatal(err)
	}
	keep := uuid.NewString()
	rows := []model.Config{{ResourceKey: "one", Alias: "one", Content: "unchanged"}, {ResourceKey: "two", Alias: "two"}, {ResourceKey: "keep", Alias: "keep", EditRevision: keep}}
	if err := conn.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var commands []*exec.Cmd
	for _, id := range []string{"a", "b"} {
		cmd := exec.Command(exe, "-test.run=^TestConfigEditBackfillProcess$", "-test.timeout=20s")
		cmd.Env = append(os.Environ(), "RB_EDIT_BACKFILL_DB="+file, "RB_EDIT_BACKFILL_READY="+file+"."+id)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, cmd)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, a := os.Stat(file + ".a")
		_, b := os.Stat(file + ".b")
		if a == nil && b == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("process barrier timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(file+".start", []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	var got []model.Config
	if err := conn.Order("id").Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got[2].EditRevision != keep || got[0].EditRevision == got[1].EditRevision {
		t.Fatal("backfill replaced existing token or shared one token")
	}
	for i, row := range got {
		if !configdb.ValidEditRevision(row.EditRevision) || !row.UpdatedAt.Equal(rows[i].UpdatedAt) || row.Content != rows[i].Content {
			t.Fatal("backfill changed business data", row)
		}
	}
	if err := configdb.BackfillConfigRevisions(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	var again []model.Config
	conn.Order("id").Find(&again)
	for i := range again {
		if again[i].EditRevision != got[i].EditRevision {
			t.Fatal("repeat startup replaced token")
		}
	}
}

func TestConfigEditLegacySchemaUpgrade(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "old-schema.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := conn.DB()
	defer sqlDB.Close()
	if err := conn.Exec("CREATE TABLE resource_config (id integer primary key, resource_key text, alias text, content text, created_at datetime, updated_at datetime)").Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec("INSERT INTO resource_config (resource_key,alias,content,created_at,updated_at) VALUES ('old','old','legacy','2020-01-01 00:00:00','2020-01-01 00:00:00')").Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&model.Config{}); err != nil {
		t.Fatal(err)
	}
	if err := configdb.BackfillConfigRevisions(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	var row model.Config
	if err := conn.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if !configdb.ValidEditRevision(row.EditRevision) || row.Content != "legacy" || row.UpdatedAt.Year() != 2020 {
		t.Fatal("upgrade changed legacy record", row)
	}
	previous := row.EditRevision
	if err := MigrateConfigDefaults(conn); err != nil {
		t.Fatal(err)
	}
	conn.First(&row)
	if row.EnvironmentKey != "default" || row.PipelineKey != "default" || row.EditRevision == previous {
		t.Fatal("scope migration did not rotate")
	}
	previous = row.EditRevision
	if err := MigrateConfigDefaults(conn); err != nil {
		t.Fatal(err)
	}
	conn.First(&row)
	if row.EditRevision != previous {
		t.Fatal("scope migration no-op rotated")
	}
}

type staleEditCacheHook struct {
	importCacheHook
	snapshot string
	reads    int
}

func (h *staleEditCacheHook) ProcessHook(_ redisclient.ProcessHook) redisclient.ProcessHook {
	return func(_ context.Context, cmd redisclient.Cmder) error {
		if value, ok := cmd.(*redisclient.StringCmd); ok && cmd.Name() == "get" {
			h.reads++
			value.SetVal(h.snapshot)
		}
		return nil
	}
}
func TestConfigEditStaleRedisAndMissingRevisionCache(t *testing.T) {
	s, row := editFixture(t)
	saved, err := s.UpdateConfig(adminTestContext(), row, row.EditRevision)
	if err != nil {
		t.Fatal(err)
	}
	stale := *pbConfigToModel(row)
	stale.EditRevision = row.EditRevision
	stale.Content = "stale Redis body"
	raw, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	hook := &staleEditCacheHook{snapshot: string(raw)}
	client := redisclient.NewClient(&redisclient.Options{Addr: "unused"})
	client.AddHook(hook)
	defer client.Close()
	s.logic.redisClient = client
	got := editRead(t, s, row)
	if hook.reads != 0 || got.EditRevision != saved.EditRevision || got.Content == stale.Content {
		t.Fatal("editing detail consumed stale cache")
	}
	stale.EditRevision = ""
	raw, err = json.Marshal([]model.Config{stale})
	if err != nil {
		t.Fatal(err)
	}
	hook.snapshot = string(raw)
	list, err := s.ListConfigs(context.Background(), row.EnvironmentKey, row.PipelineKey, "", "", "", false)
	if err != nil || len(list) != 1 || list[0].EditRevision != saved.EditRevision || list[0].Content == stale.Content {
		t.Fatalf("old list cache was not a miss: %+v %v", list, err)
	}
}
