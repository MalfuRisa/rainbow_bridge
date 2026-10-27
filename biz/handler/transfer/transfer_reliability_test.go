package transfer

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/glebarez/sqlite"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	confighandler "github.com/yi-nology/rainbow_bridge/biz/handler/config"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	configpb "github.com/yi-nology/rainbow_bridge/biz/model/config"
	envpb "github.com/yi-nology/rainbow_bridge/biz/model/environment"
	plpb "github.com/yi-nology/rainbow_bridge/biz/model/pipeline"
	transferpb "github.com/yi-nology/rainbow_bridge/biz/model/transfer"
	"github.com/yi-nology/rainbow_bridge/biz/service"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestHTTPConfigCreateAndImportSummary(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	if err := conn.AutoMigrate(
		&model.Config{}, &model.Environment{}, &model.Pipeline{},
		&model.ConfigFreezeState{}, &model.ConfigFreezeEvent{}, &model.ConfigWriteGate{},
	); err != nil {
		t.Fatal(err)
	}
	s := service.NewService(conn, nil, "", nil)
	if err := s.EnsureFreezeBootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEnvironment(context.Background(), &envpb.Environment{
		EnvironmentKey: "dev", EnvironmentName: "dev", IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPipeline(context.Background(), "dev", &plpb.Pipeline{
		PipelineKey: "main", PipelineName: "main", IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}
	pipeline, err := s.GetPipeline(context.Background(), "dev", "main")
	if err != nil {
		t.Fatal(err)
	}
	scopeID := pipeline.GetScopeId()
	previous := svc
	SetService(s)
	confighandler.SetService(s)
	t.Cleanup(func() { SetService(previous); confighandler.SetService(nil) })
	h := server.New()
	h.POST("/api/v1/config/create", confighandler.Create)
	h.POST("/api/v1/transfer/import", Import)
	post := func(path string, input any, output any) []byte {
		t.Helper()
		payload, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		response := ut.PerformRequest(h.Engine, "POST", path, &ut.Body{Body: bytes.NewReader(payload), Len: len(payload)}, ut.Header{Key: "Content-Type", Value: "application/json"})
		if response.Code != 200 {
			t.Fatalf("HTTP code=%d", response.Code)
		}
		if err := json.Unmarshal(response.Body.Bytes(), output); err != nil {
			t.Fatalf("decode %s: %v", response.Body.String(), err)
		}
		return response.Body.Bytes()
	}
	cfg := &common.ResourceConfig{ScopeId: scopeID, EnvironmentKey: "dev", PipelineKey: "main", Alias: "number", Type: "number", Content: "42", Description: "initial"}
	var created configpb.ConfigResponse
	post("/api/v1/config/create", &configpb.CreateConfigRequest{Config: cfg, ExpectedScopeId: scopeID}, &created)
	if created.Code != 200 || created.Data == nil || created.Data.Config.Content != "42" {
		t.Fatalf("create=%+v", &created)
	}
	key := created.Data.Config.ResourceKey
	cfg.Content = "1.2"
	cfg.Alias = "invalid"
	post("/api/v1/config/create", &configpb.CreateConfigRequest{Config: cfg, ExpectedScopeId: scopeID}, &created)
	if created.Code == 200 {
		t.Fatal("invalid number HTTP accepted")
	}
	incoming := &common.ResourceConfig{EnvironmentKey: "dev", PipelineKey: "main", Alias: "number", ResourceKey: "foreign", Type: "number", Content: "43", Description: ""}
	var imported transferpb.ImportResponse
	body := post("/api/v1/transfer/import", &transferpb.ImportRequest{Configs: []*common.ResourceConfig{incoming}, Overwrite: false}, &imported)
	if imported.Code != 200 || imported.Data == nil || imported.Data.Total != 0 || len(imported.Data.Items) != 0 {
		t.Fatalf("skip summary=%s", body)
	}
	var raw struct {
		Data struct {
			Items json.RawMessage `json:"items"`
		}
	}
	if err := json.Unmarshal(body, &raw); err != nil || string(raw.Data.Items) != "[]" {
		t.Fatalf("empty items must be []: %s", body)
	}
	body = post("/api/v1/transfer/import", &transferpb.ImportRequest{Configs: []*common.ResourceConfig{incoming}, Overwrite: true}, &imported)
	if imported.Code != 200 || imported.Data.Total != 1 || len(imported.Data.Items) != 1 || imported.Data.Items[0].ResourceKey != key {
		t.Fatalf("write summary=%s", body)
	}
	got, err := s.GetConfigDetail(context.Background(), "dev", "main", key)
	if err != nil || got.Content != "43" || got.Description != "" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	bad := &common.ResourceConfig{EnvironmentKey: "dev", PipelineKey: "main", Alias: "bad", Type: "object", Content: "null"}
	incoming.Content = "44"
	body = post("/api/v1/transfer/import", &transferpb.ImportRequest{Configs: []*common.ResourceConfig{incoming, bad}, Overwrite: true}, &imported)
	if imported.Code == 200 || imported.Error == "" {
		t.Fatalf("failure summary=%s", body)
	}
	var failed map[string]json.RawMessage
	json.Unmarshal(body, &failed)
	if data, ok := failed["data"]; ok && string(data) != "null" {
		t.Fatalf("failure has a success summary: %s", body)
	}
	got, err = s.GetConfigDetail(context.Background(), "dev", "main", key)
	if err != nil || got.Content != "43" {
		t.Fatalf("HTTP failure did not preserve old value: %+v %v", got, err)
	}
	var count int64
	conn.Model(&model.Config{}).Count(&count)
	if count != 1 {
		t.Fatalf("unexpected records=%d", count)
	}
}
