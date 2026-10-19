package asset

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/glebarez/sqlite"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/service"
	"gorm.io/gorm"
)

func TestBinaryDownloadHTTPStatus(t *testing.T) {
	t.Chdir(t.TempDir())
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := conn.DB()
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	if err := conn.AutoMigrate(&model.Asset{}); err != nil {
		t.Fatal(err)
	}
	previous := svc
	SetService(service.NewService(conn, nil, "", nil))
	t.Cleanup(func() { SetService(previous) })
	h := server.New()
	h.GET("/file/:file_id", GetFile)
	response := ut.PerformRequest(h.Engine, "GET", "/file/missing", nil)
	if response.Code != 404 {
		t.Fatalf("missing row: %d %s", response.Code, response.Body.String())
	}
	row := model.Asset{FileID: "asset-id", FileName: "image.png", Path: filepath.Join("uploads", "asset-id", "image.png"), ContentType: "image/png"}
	if err := conn.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	response = ut.PerformRequest(h.Engine, "GET", "/file/asset-id", nil)
	if response.Code != 404 {
		t.Fatalf("missing file: %d", response.Code)
	}
	full := filepath.Join("data", row.Path)
	os.MkdirAll(filepath.Dir(full), 0755)
	// A directory at the file path deterministically triggers a genuine read IO error on Windows and Linux.
	if err := os.Mkdir(full, 0755); err != nil {
		t.Fatal(err)
	}
	response = ut.PerformRequest(h.Engine, "GET", "/file/asset-id", nil)
	if response.Code != 500 {
		t.Fatalf("IO failure: %d", response.Code)
	}
	os.Remove(full)
	data := []byte("original bytes")
	os.WriteFile(full, data, 0644)
	response = ut.PerformRequest(h.Engine, "GET", "/file/asset-id", nil)
	if response.Code != 200 || response.Body.String() != string(data) || string(response.Header().ContentType()) != "image/png" {
		t.Fatalf("success: %d %s", response.Code, response.Body.String())
	}
	if _, _, err := svc.GetAssetFile(context.Background(), "missing"); err == nil {
		t.Fatal("missing service resource accepted")
	}
}
