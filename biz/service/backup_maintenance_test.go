package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/backup"
	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	envpb "github.com/yi-nology/rainbow_bridge/biz/model/environment"
)

func TestServiceRejectsBusinessWritesDuringBackupMaintenance(t *testing.T) {
	conn := db.SetupTestDB(t)
	t.Cleanup(func() { db.CleanupTestDB(t, conn) })
	if err := conn.AutoMigrate(&model.ConfigWriteGate{}, &model.ConfigFreezeState{},
		&model.ConfigFreezeEvent{}); err != nil {
		t.Fatal(err)
	}
	if err := conn.Create(&model.ConfigWriteGate{ID: model.ConfigWriteGateRowID}).Error; err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	layout := &backup.Layout{
		BusinessRoot: filepath.Join(root, "data"),
		ControlDir:   filepath.Join(root, "control"),
		DatabasePath: filepath.Join(root, "data", "resource.db"),
		DatabaseRel:  "resource.db",
		UploadsDir:   filepath.Join(root, "data", "uploads"),
		ReleaseDir:   filepath.Join(root, "data", "release-snapshots"),
		ReleaseRel:   "release-snapshots",
	}
	if err := os.MkdirAll(layout.BusinessRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	manager, err := backup.NewManager(layout, backup.DefaultLimits(), backup.Producer{}, "test-instance")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(conn, nil, "", nil)
	svc.SetBackupManager(manager)
	if err := svc.AddEnvironment(context.Background(), &envpb.Environment{
		EnvironmentKey: "prod", EnvironmentName: "Prod", IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := manager.Maintenance().Enter(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = svc.AddConfig(context.Background(), &common.ResourceConfig{
		EnvironmentKey: "prod", PipelineKey: "default", ResourceKey: "blocked",
		Content: "x", Type: "text",
	})
	if !backup.IsMaintenance(err) {
		t.Fatalf("expected maintenance rejection, got %v", err)
	}
	manager.Maintenance().Leave()

	if _, err := svc.AddConfig(context.Background(), &common.ResourceConfig{
		EnvironmentKey: "prod", PipelineKey: "default", ResourceKey: "allowed",
		Content: "x", Type: "text",
	}); err != nil {
		t.Fatalf("write after maintenance should succeed: %v", err)
	}
}
