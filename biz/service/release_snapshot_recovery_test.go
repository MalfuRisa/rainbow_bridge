package service

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/pkg/config"
	"github.com/yi-nology/rainbow_bridge/pkg/database"
)

func TestSnapshotCrashChild(t *testing.T) {
	file := os.Getenv("RB_SNAPSHOT_CRASH_FIXTURE")
	if file == "" {
		t.Skip("isolated subprocess only")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(db, nil, "/rainbow-bridge", &cfg)
	_, err = s.CreateRelease(adminTestContext(), ReleaseCreateInput{EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1", ExpectedFreezeRevisionRaw: "1", RequestID: os.Getenv("RB_SNAPSHOT_CRASH_REQUEST")})
	t.Fatalf("expected process crash, create returned %v", err)
}

func TestSnapshotProcessCrashOwnershipAndUnknownCommit(t *testing.T) {
	for _, point := range []string{"after_snapshot_upload", "after_commit"} {
		t.Run(point, func(t *testing.T) {
			f := newReleaseFixture(t)
			fixture := snapshotS3(t, f.svc)
			var paths []struct {
				Seq        int
				Name, File string
			}
			if err := f.svc.logic.db.Raw("PRAGMA database_list").Scan(&paths).Error; err != nil || len(paths) == 0 {
				t.Fatal("SQLite fixture path", err)
			}
			cfg := *f.svc.config
			cfg.Database = config.DatabaseConfig{Driver: "sqlite", SQLite: config.SQLiteConfig{Path: paths[0].File}}
			raw, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "child.json")
			if err := os.WriteFile(file, raw, 0600); err != nil {
				t.Fatal(err)
			}
			request := uuid.NewString()
			temp := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestSnapshotCrashChild$")
			cmd.Env = append(os.Environ(), "RB_SNAPSHOT_CRASH_FIXTURE="+file, "RB_SNAPSHOT_CRASH_REQUEST="+request, releaseCrashEnv+"="+point, "TEMP="+temp, "TMP="+temp, "TMPDIR="+temp)
			if err := cmd.Run(); err == nil {
				t.Fatal("child did not crash")
			} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 97 {
				t.Fatalf("unexpected child exit: %v", err)
			}
			var attempts []model.ReleaseStorageAttempt
			if err := f.svc.logic.db.Find(&attempts).Error; err != nil || len(attempts) != 1 {
				t.Fatal("crash ownership lost", err)
			}
			var versions int64
			f.svc.logic.db.Model(&model.ConfigReleaseVersion{}).Count(&versions)
			if point == "after_commit" {
				if versions != 1 {
					t.Fatal("committed version missing")
				}
				// An outcome marker may be unknown while the formal locator is authoritative.
				f.svc.logic.db.Model(&attempts[0]).Update("state", "unknown")
				if err := f.svc.ReconcileSnapshotAttempt(adminTestContext(), attempts[0].ID); err == nil {
					t.Fatal("unknown outcome deleted committed objects")
				}
				replay, err := f.svc.CreateRelease(adminTestContext(), ReleaseCreateInput{EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1", ExpectedFreezeRevisionRaw: "1", RequestID: request})
				if err != nil || !replay.Replayed {
					t.Fatal("response lost replay", err)
				}
			} else {
				if versions != 0 || attempts[0].State != "pending" {
					t.Fatal("half-committed version or ownership missing")
				}
				if err := f.svc.ReconcileSnapshotAttempt(adminTestContext(), attempts[0].ID); err != nil {
					t.Fatal("crash reconcile", err)
				}
				fixture.mu.Lock()
				defer fixture.mu.Unlock()
				if len(fixture.objects) != 0 {
					t.Fatal("crash objects remain")
				}
			}
		})
	}
}

func TestSnapshotFailedPublicationPreservesOtherAttemptAndActive(t *testing.T) {
	f := newReleaseFixture(t)
	fixture := snapshotS3(t, f.svc)
	one, err := f.create(t, "v1", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.activate(t, "v1", "0", ""); err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.failAt = fixture.puts + 2
	fixture.mu.Unlock()
	if _, err := f.create(t, "v2", "1", ""); err == nil {
		t.Fatal("injected manifest failure accepted")
	}
	active, err := f.svc.ReleaseActive(adminTestContext(), "qa_prod", "main")
	if err != nil || active.SnapshotID != one.Meta.SnapshotID || active.Revision != 1 {
		t.Fatal("failure changed active", err)
	}
	fixed := "v1"
	if _, err := f.svc.RuntimeRelease(context.Background(), "qa_prod", "main", &fixed); err != nil {
		t.Fatal("failure cleaned another attempt", err)
	}
}

func TestSnapshotConcurrentRequestReplay(t *testing.T) {
	f := newReleaseFixture(t)
	snapshotS3(t, f.svc)
	request := uuid.NewString()
	type outcome struct {
		result *ReleaseCreateResult
		err    error
	}
	results := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() { r, e := f.create(t, "v1", "1", request); results <- outcome{r, e} }()
	}
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || a.result.Meta.SnapshotID != b.result.Meta.SnapshotID || a.result.Replayed == b.result.Replayed {
		t.Fatal("concurrent request did not converge", a.err, b.err)
	}
	var count int64
	f.svc.logic.db.Model(&model.ConfigReleaseVersion{}).Count(&count)
	if count != 1 {
		t.Fatal("duplicate visible versions")
	}
}
