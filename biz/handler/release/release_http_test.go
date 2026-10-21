package release

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	commonpb "github.com/yi-nology/rainbow_bridge/biz/model/common"
	envpb "github.com/yi-nology/rainbow_bridge/biz/model/environment"
	plpb "github.com/yi-nology/rainbow_bridge/biz/model/pipeline"
	"github.com/yi-nology/rainbow_bridge/biz/service"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 版本读取/导出的 HTTP 契约:格式参数、成功头与错误状态,均在真实 handler 上验证。

const (
	handlerTestToken = "handler-test-admin-token-0123456789ab"
	handlerEnv       = "qa_prod"
	handlerPipeline  = "main"
	handlerBasePath  = "/rainbow-bridge"
	handlerBigNumber = "9007199254740993"
)

type handlerFixture struct {
	svc        *service.Service
	engine     *server.Hertz
	snapshotID string
	assetID    string
	assetBytes []byte
}

func newHandlerFixture(t *testing.T) *handlerFixture {
	t.Helper()
	t.Setenv(service.FreezeAdminTokenEnv, handlerTestToken)
	t.Chdir(t.TempDir())

	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "release-http.db")+"?_pragma=busy_timeout(10000)"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if err := conn.AutoMigrate(
		&model.Environment{}, &model.Pipeline{}, &model.Config{}, &model.Asset{},
		&model.ConfigFreezeState{}, &model.ConfigFreezeEvent{}, &model.ConfigWriteGate{},
		&model.ConfigReleaseVersion{}, &model.ConfigReleaseAsset{}, &model.ConfigReleaseHead{}, &model.ConfigReleaseEvent{},
	); err != nil {
		t.Fatal(err)
	}
	svc := service.NewService(conn, nil, handlerBasePath, nil)
	ctx := service.WithAdminIdentity(context.Background(), service.AdminIdentity{UserID: 1, Username: "admin", Role: "admin"})
	if err := svc.EnsureFreezeBootstrap(ctx); err != nil {
		t.Fatalf("freeze bootstrap: %v", err)
	}
	if err := svc.AddEnvironment(ctx, &envpb.Environment{EnvironmentKey: handlerEnv, EnvironmentName: "QA Prod", IsActive: true}); err != nil {
		t.Fatalf("add environment: %v", err)
	}
	if err := svc.AddPipeline(ctx, handlerEnv, &plpb.Pipeline{PipelineKey: handlerPipeline, PipelineName: "main", IsActive: true}); err != nil {
		t.Fatalf("add pipeline: %v", err)
	}

	assetBytes := []byte("PNG-HTTP-BYTES")
	_, reference, err := svc.UploadAsset(ctx, &service.FileUploadInput{
		EnvironmentKey: handlerEnv, PipelineKey: handlerPipeline,
		FileName: "pic.png", ContentType: "image/png", Data: assetBytes,
	})
	if err != nil {
		t.Fatalf("upload asset: %v", err)
	}

	for _, cfg := range []*commonpb.ResourceConfig{
		{Alias: "HTTP_IMAGE", Name: "pic", Type: "image", Content: reference, EnvironmentKey: handlerEnv, PipelineKey: handlerPipeline},
		{Alias: "HTTP_OBJ", Name: "obj", Type: "object", Content: `{"big":` + handlerBigNumber + `}`, EnvironmentKey: handlerEnv, PipelineKey: handlerPipeline},
	} {
		if _, err := svc.AddConfig(ctx, cfg); err != nil {
			t.Fatalf("add config %s: %v", cfg.Alias, err)
		}
	}
	created := lockAndPublish(t, svc, handlerEnv, handlerPipeline, "v1.0")

	previous := svc
	SetService(svc)
	t.Cleanup(func() { SetService(previous) })

	engine := server.New()
	engine.GET("/api/v1/runtime/release", RuntimeRelease)
	engine.GET("/api/v1/runtime/release/static", RuntimeReleaseStatic)
	return &handlerFixture{
		svc: svc, engine: engine, snapshotID: created.Meta.SnapshotID,
		assetID: assetIDOf(t, svc, created.Meta.SnapshotID), assetBytes: assetBytes,
	}
}

// TestRuntimeReleaseEmptyConfigsArray 空版本:raw/json 两种格式都必须保留 configs 为 `[]`。
func TestRuntimeReleaseEmptyConfigsArray(t *testing.T) {
	fixture := newHandlerFixture(t)
	const emptyEnv = "qa_empty"
	ctx := service.WithAdminIdentity(context.Background(), service.AdminIdentity{UserID: 1, Username: "admin", Role: "admin"})
	if err := fixture.svc.AddEnvironment(ctx, &envpb.Environment{EnvironmentKey: emptyEnv, EnvironmentName: "QA Empty", IsActive: true}); err != nil {
		t.Fatalf("add empty environment: %v", err)
	}
	if err := fixture.svc.AddPipeline(ctx, emptyEnv, &plpb.Pipeline{PipelineKey: handlerPipeline, PipelineName: "main", IsActive: true}); err != nil {
		t.Fatalf("add empty pipeline: %v", err)
	}
	lockAndPublish(t, fixture.svc, emptyEnv, handlerPipeline, "v1.0")
	active, err := fixture.svc.ReleaseActive(ctx, emptyEnv, handlerPipeline)
	if err != nil || active == nil || active.ChangedAt == "" {
		t.Fatalf("active timestamp missing: %+v %v", active, err)
	}

	headers := []ut.Header{
		{Key: "x-environment", Value: emptyEnv},
		{Key: "x-pipeline", Value: handlerPipeline},
	}
	for _, query := range []string{"", "?content_format=json", "?content_format=raw"} {
		response := ut.PerformRequest(fixture.engine.Engine, "GET", "/api/v1/runtime/release"+query, nil, headers...)
		if response.Code != 200 {
			t.Fatalf("empty read %q: %d %s", query, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), `"configs":[]`) {
			t.Fatalf("empty release must keep configs as []: %s", response.Body.String())
		}
		var body struct {
			Data struct {
				ActivatedAt string `json:"activated_at"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Data.ActivatedAt != active.ChangedAt {
			t.Fatalf("activation time must match resolved head: %s, %v", response.Body.String(), err)
		}
	}
}

// lockAndPublish 锁定范围并创建+生效一个版本(夹具与空版本用例共用)。
func lockAndPublish(t *testing.T, svc *service.Service, environmentKey, pipelineKey, version string) *service.ReleaseCreateResult {
	t.Helper()
	ctx := service.WithAdminIdentity(context.Background(), service.AdminIdentity{UserID: 1, Username: "admin", Role: "admin"})
	status, err := svc.FreezeStatus(ctx, environmentKey, pipelineKey)
	if err != nil {
		t.Fatalf("freeze status: %v", err)
	}
	if status.State != "locked" {
		if _, err := svc.LockFreeze(ctx, environmentKey, pipelineKey, "HTTP 契约锁定", strconv.FormatInt(status.Revision, 10)); err != nil {
			t.Fatalf("lock: %v", err)
		}
	}
	freeze, err := svc.FreezeStatus(ctx, environmentKey, pipelineKey)
	if err != nil {
		t.Fatalf("freeze status: %v", err)
	}
	created, err := svc.CreateRelease(ctx, service.ReleaseCreateInput{
		EnvironmentKey: environmentKey, PipelineKey: pipelineKey, Version: version,
		ExpectedFreezeRevisionRaw: strconv.FormatInt(freeze.Revision, 10), RequestID: uuid.NewString(),
		Reason: "HTTP 契约版本 " + version,
	})
	if err != nil {
		t.Fatalf("create release %s: %v", version, err)
	}
	active, err := svc.ReleaseActive(ctx, environmentKey, pipelineKey)
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	expected := "0"
	if active != nil {
		expected = strconv.FormatInt(active.Revision, 10)
	}
	if _, err := svc.ActivateRelease(ctx, service.ReleaseActivateInput{
		EnvironmentKey: environmentKey, PipelineKey: pipelineKey, Version: version,
		ExpectedActiveRevisionRaw: expected, RequestID: uuid.NewString(),
		Reason: "HTTP 契约生效 " + version,
	}); err != nil {
		t.Fatalf("activate %s: %v", version, err)
	}
	return created
}

// assetIDOf 取快照内第一份资源的 id(夹具只有一份)。
func assetIDOf(t *testing.T, svc *service.Service, snapshotID string) string {
	t.Helper()
	detail, err := svc.ReleaseDetail(context.Background(), handlerEnv, handlerPipeline, "v1.0")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail.Meta.SnapshotID != snapshotID {
		t.Fatalf("detail snapshot = %s, want %s", detail.Meta.SnapshotID, snapshotID)
	}
	if len(detail.Assets) != 1 {
		t.Fatalf("fixture expects one asset, got %d", len(detail.Assets))
	}
	return detail.Assets[0].AssetID
}

func runtimeHeaders() []ut.Header {
	return []ut.Header{
		{Key: "x-environment", Value: handlerEnv},
		{Key: "x-pipeline", Value: handlerPipeline},
	}
}

// TestRuntimeReleaseContentFormat raw 只改变 content 表示;默认与显式 json 保持原契约;非法值 400。
func TestRuntimeReleaseContentFormat(t *testing.T) {
	fixture := newHandlerFixture(t)

	// 默认(json):object 为原生 JSON 对象
	response := ut.PerformRequest(fixture.engine.Engine, "GET", "/api/v1/runtime/release", nil, runtimeHeaders()...)
	if response.Code != 200 {
		t.Fatalf("default read: %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, `"big":"`+handlerBigNumber) {
		t.Fatalf("default format must keep numbers as numbers: %s", body)
	}
	if !strings.Contains(body, handlerBigNumber) {
		t.Fatalf("default format must keep the big integer literal: %s", body)
	}
	var envelope struct {
		Data struct {
			SelectionMode string `json:"selection_mode"`
			Version       string `json:"version"`
			SnapshotID    string `json:"snapshot_id"`
			Configs       []struct {
				Alias   string `json:"alias"`
				Content any    `json:"content"`
			} `json:"configs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode default body: %v", err)
	}
	if envelope.Data.SelectionMode != "active" || envelope.Data.Version != "v1.0" || envelope.Data.SnapshotID != fixture.snapshotID {
		t.Fatalf("default body metadata mismatch: %+v", envelope.Data)
	}
	objectContent := any(nil)
	for _, cfg := range envelope.Data.Configs {
		if cfg.Alias == "HTTP_OBJ" {
			objectContent = cfg.Content
		}
	}
	if _, ok := objectContent.(map[string]any); !ok {
		t.Fatalf("default format must render object content as JSON object: %v", objectContent)
	}

	// 显式 json:与默认一致
	explicit := ut.PerformRequest(fixture.engine.Engine, "GET", "/api/v1/runtime/release?content_format=json", nil, runtimeHeaders()...)
	if explicit.Code != 200 || explicit.Body.String() != body {
		t.Fatalf("explicit json must equal default: %d", explicit.Code)
	}

	// raw:全部为字符串且保留精度
	raw := ut.PerformRequest(fixture.engine.Engine, "GET", "/api/v1/runtime/release?content_format=raw", nil, runtimeHeaders()...)
	if raw.Code != 200 {
		t.Fatalf("raw read: %d %s", raw.Code, raw.Body.String())
	}
	var rawEnvelope struct {
		Data struct {
			SelectionMode string `json:"selection_mode"`
			Configs       []struct {
				Alias   string `json:"alias"`
				Content any    `json:"content"`
			} `json:"configs"`
			ActiveRevision string `json:"active_revision"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw.Body.Bytes(), &rawEnvelope); err != nil {
		t.Fatalf("decode raw body: %v", err)
	}
	if rawEnvelope.Data.ActiveRevision == "" {
		t.Fatal("raw format must keep active_revision")
	}
	for _, cfg := range rawEnvelope.Data.Configs {
		if _, ok := cfg.Content.(string); !ok {
			t.Fatalf("raw format must return every content as string: %s = %v", cfg.Alias, cfg.Content)
		}
	}
	if !strings.Contains(raw.Body.String(), handlerBigNumber) {
		t.Fatalf("raw content must keep the big integer literal: %s", raw.Body.String())
	}
	if !strings.Contains(raw.Body.String(), `"content":"{\"big\":`+handlerBigNumber+`}"`) {
		t.Fatalf("raw object content must be a JSON string: %s", raw.Body.String())
	}

	// 非法取值与显式空值:400,不静默按默认处理
	for _, path := range []string{
		"/api/v1/runtime/release?content_format=",
		"/api/v1/runtime/release?content_format=xml",
	} {
		bad := ut.PerformRequest(fixture.engine.Engine, "GET", path, nil, runtimeHeaders()...)
		if bad.Code != 400 {
			t.Fatalf("%s must be 400, got %d %s", path, bad.Code, bad.Body.String())
		}
		if !strings.Contains(bad.Body.String(), service.ErrTypeReleaseInvalidRequest) {
			t.Fatalf("%s must return the invalid-request error type: %s", path, bad.Body.String())
		}
	}
}

// TestRuntimeReleaseStaticHTTP 版本静态导出的成功头与错误状态。
func TestRuntimeReleaseStaticHTTP(t *testing.T) {
	fixture := newHandlerFixture(t)
	basePath := "/api/v1/runtime/release/static?version=v1.0&expected_snapshot_id=" + fixture.snapshotID

	success := ut.PerformRequest(fixture.engine.Engine, "GET", basePath, nil, runtimeHeaders()...)
	if success.Code != 200 {
		t.Fatalf("export: %d %s", success.Code, success.Body.String())
	}
	if contentType := string(success.Header().ContentType()); contentType != "application/zip" {
		t.Fatalf("content type = %q", contentType)
	}
	if got := string(success.Header().Peek("X-Rainbow-Snapshot-Id")); got != fixture.snapshotID {
		t.Fatalf("snapshot header = %q", got)
	}
	if got := string(success.Header().Peek("X-Rainbow-Version")); got != "v1.0" {
		t.Fatalf("version header = %q", got)
	}
	if disposition := string(success.Header().Peek("Content-Disposition")); !strings.Contains(disposition, "attachment") ||
		!strings.Contains(disposition, fixture.snapshotID) {
		t.Fatalf("content disposition = %q", disposition)
	}
	if cache := string(success.Header().Peek("Cache-Control")); cache != "no-store" {
		t.Fatalf("cache control = %q", cache)
	}
	if success.Body.Len() < 4 || string(success.Body.Bytes()[:2]) != "PK" {
		t.Fatalf("body must be a zip archive, got %d bytes", success.Body.Len())
	}

	cases := []struct {
		name   string
		path   string
		status int
		expect string
	}{
		{"missing_version", "/api/v1/runtime/release/static?expected_snapshot_id=" + fixture.snapshotID, 400, service.ErrTypeReleaseInvalidRequest},
		{"missing_snapshot", "/api/v1/runtime/release/static?version=v1.0", 400, service.ErrTypeReleaseInvalidRequest},
		{"bad_snapshot", "/api/v1/runtime/release/static?version=v1.0&expected_snapshot_id=abc", 400, service.ErrTypeReleaseInvalidRequest},
		{"mismatch", "/api/v1/runtime/release/static?version=v1.0&expected_snapshot_id=31111111-2222-3333-4444-555555555555", 409, service.ErrTypeReleaseSnapshotMismatch},
		{"unknown_version", "/api/v1/runtime/release/static?version=v9.9&expected_snapshot_id=" + fixture.snapshotID, 404, service.ErrTypeReleaseVersionNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := ut.PerformRequest(fixture.engine.Engine, "GET", tc.path, nil, runtimeHeaders()...)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d (%s)", response.Code, tc.status, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), tc.expect) {
				t.Fatalf("error type %s missing: %s", tc.expect, response.Body.String())
			}
			if contentType := string(response.Header().ContentType()); !strings.Contains(contentType, "application/json") {
				t.Fatalf("error response content type = %q", contentType)
			}
		})
	}

	// 范围缺失:两个头都要有
	noScope := ut.PerformRequest(fixture.engine.Engine, "GET", basePath, nil)
	if noScope.Code != 400 || !strings.Contains(noScope.Body.String(), service.ErrTypeReleaseInvalidRequest) {
		t.Fatalf("missing scope headers must be 400: %d %s", noScope.Code, noScope.Body.String())
	}

	// 导出不改变 active 与事件数
	active, err := fixture.svc.ReleaseActive(context.Background(), handlerEnv, handlerPipeline)
	if err != nil || active == nil || active.Version != "v1.0" || active.Revision != 1 {
		t.Fatalf("export must not change active: %v %+v", err, active)
	}
	history, err := fixture.svc.ReleaseHistoryPage(context.Background(), handlerEnv, handlerPipeline, 10, "")
	if err != nil || len(history.Items) != 2 {
		t.Fatalf("export must not append audit events: %v %d", err, len(history.Items))
	}
}
