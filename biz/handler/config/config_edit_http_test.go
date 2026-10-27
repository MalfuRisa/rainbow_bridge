package config

import (
	"context"
	"encoding/json"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/glebarez/sqlite"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	envpb "github.com/yi-nology/rainbow_bridge/biz/model/environment"
	"github.com/yi-nology/rainbow_bridge/biz/service"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigEditHTTPContract(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "http.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := conn.DB()
	defer sqlDB.Close()
	if err := conn.AutoMigrate(&model.Config{}, &model.Asset{}, &model.Environment{}, &model.Pipeline{}, &model.ConfigWriteGate{}, &model.ConfigFreezeState{}, &model.ConfigFreezeEvent{}); err != nil {
		t.Fatal(err)
	}
	serviceInstance := service.NewService(conn, nil, "", nil)
	if err := serviceInstance.EnsureFreezeBootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := svc
	SetService(serviceInstance)
	defer SetService(old)
	if err := serviceInstance.AddEnvironment(context.Background(), &envpb.Environment{
		EnvironmentKey: "dev", EnvironmentName: "dev", IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}
	pipeline, err := serviceInstance.GetPipeline(context.Background(), "dev", "default")
	if err != nil {
		t.Fatal(err)
	}
	scopeID := pipeline.GetScopeId()
	row, err := serviceInstance.AddConfig(context.Background(), &common.ResourceConfig{
		EnvironmentKey: "dev", PipelineKey: "default", Alias: "title", Name: "title",
		Type: "text", Content: "initial", ScopeId: scopeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := server.New()
	engine.POST("/update", Update)
	engine.GET("/detail", Detail)
	send := func(body string, want int, kind string) {
		t.Helper()
		response := ut.PerformRequest(engine.Engine, "POST", "/update", &ut.Body{Body: strings.NewReader(body), Len: len(body)}, ut.Header{Key: "Content-Type", Value: "application/json"})
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if response.Code != want || result["code"] != float64(want) || kind != "" && result["error_type"] != kind {
			t.Fatalf("HTTP=%d body=%s", response.Code, response.Body.String())
		}
		if kind == "CONFIG_EDIT_CONFLICT" && (result["data"] != nil || strings.Contains(response.Body.String(), "edit_revision")) {
			t.Fatal("conflict leaked latest token/body")
		}
	}
	configJSON, _ := json.Marshal(row)
	for _, bad := range []string{"", `,"expected_edit_revision":null`, `,"expected_edit_revision":12`, `,"expected_edit_revision":""`, `,"expected_edit_revision":"BAD"`, `,"expected_edit_revision":"` + strings.ToUpper(row.EditRevision) + `"`, `,"expected_edit_revision":{}`} {
		send(`{"config":`+string(configJSON)+`,"expected_scope_id":"`+scopeID+`"`+bad+`}`, 400, "CONFIG_EDIT_REVISION_INVALID")
	}
	body := `{"config":` + string(configJSON) + `,"expected_scope_id":"` + scopeID + `","expected_edit_revision":"` + row.EditRevision + `"}`
	send(body, 200, "")
	send(body, 409, "CONFIG_EDIT_CONFLICT")
	detail := ut.PerformRequest(engine.Engine, "GET", "/detail?environment_key=dev&pipeline_key=default&resource_key="+row.ResourceKey, nil)
	if string(detail.Header().Peek("Cache-Control")) != "no-store" {
		t.Fatal("detail is cacheable")
	}
	conn.Where("resource_key = ?", row.ResourceKey).Delete(&model.Config{})
	send(body, 404, "CONFIG_EDIT_TARGET_NOT_FOUND")
}
