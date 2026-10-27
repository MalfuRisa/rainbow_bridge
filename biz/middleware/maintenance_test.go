package middleware

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/yi-nology/rainbow_bridge/biz/backup"
)

type maintenanceTestProvider struct {
	manager *backup.Manager
}

func (p maintenanceTestProvider) BackupManager() *backup.Manager { return p.manager }

func TestBackupMaintenanceAllowsCompareButBlocksWrites(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Maintenance().Enter(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Maintenance().Leave()

	engine := server.New()
	engine.Use(BackupMaintenance(maintenanceTestProvider{manager: manager}))
	engine.POST("/api/v1/release/compare", func(ctx context.Context, c *app.RequestContext) { c.Status(200) })
	engine.GET("/api/v1/release/active", func(ctx context.Context, c *app.RequestContext) { c.Status(200) })
	engine.POST("/api/v1/config/create", func(ctx context.Context, c *app.RequestContext) { c.Status(200) })

	compare := ut.PerformRequest(engine.Engine, "POST", "/api/v1/release/compare", nil)
	if compare.Code != 200 {
		t.Fatalf("compare must stay readable during maintenance: %d", compare.Code)
	}
	active := ut.PerformRequest(engine.Engine, "GET", "/api/v1/release/active", nil)
	if active.Code != 200 {
		t.Fatalf("active state read must stay readable during maintenance: %d", active.Code)
	}
	write := ut.PerformRequest(engine.Engine, "POST", "/api/v1/config/create", nil)
	if write.Code != 503 {
		t.Fatalf("business write must be blocked: %d", write.Code)
	}
	if !strings.Contains(write.Body.String(), "BACKUP_MAINTENANCE") {
		t.Fatalf("write error type missing: %s", write.Body.String())
	}
}
