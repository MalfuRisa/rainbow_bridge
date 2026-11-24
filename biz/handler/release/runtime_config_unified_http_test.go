package release

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/google/uuid"
	runtimehandler "github.com/yi-nology/rainbow_bridge/biz/handler/runtime"
	commonpb "github.com/yi-nology/rainbow_bridge/biz/model/common"
	envpb "github.com/yi-nology/rainbow_bridge/biz/model/environment"
	plpb "github.com/yi-nology/rainbow_bridge/biz/model/pipeline"
	runtimerouter "github.com/yi-nology/rainbow_bridge/biz/router/runtime"
	"github.com/yi-nology/rainbow_bridge/biz/service"
)

// 统一读取入口:config 不传 version 读生效版本,传 version 读固定版本;
// 默认 content 呈现保持旧 config 契约,json/raw 与 release 同规则。

type unifiedRuntimeConfig struct {
	ResourceKey    string `json:"resource_key"`
	Alias          string `json:"alias"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Content        any    `json:"content"`
	Remark         string `json:"remark"`
	IsPerm         bool   `json:"is_perm"`
	Description    string `json:"description"`
	EnvironmentKey string `json:"environment_key"`
	PipelineKey    string `json:"pipeline_key"`
	ScopeID        string `json:"scope_id"`
}

type unifiedRuntimeData struct {
	SnapshotID           string `json:"snapshot_id"`
	Version              string `json:"version"`
	SourceFreezeRevision string `json:"source_freeze_revision"`
	ActiveRevision       string `json:"active_revision"`
	PayloadSha256        string `json:"payload_sha256"`
	SelectionMode        string `json:"selection_mode"`
	ScopeID              string `json:"scope_id"`
	Environment          struct {
		EnvironmentKey  string `json:"environment_key"`
		EnvironmentName string `json:"environment_name"`
		PipelineKey     string `json:"pipeline_key"`
		PipelineName    string `json:"pipeline_name"`
		ScopeID         string `json:"scope_id"`
	} `json:"environment"`
	Configs []unifiedRuntimeConfig `json:"configs"`
}

type unifiedRuntimeEnvelope struct {
	Code      int32              `json:"code"`
	Msg       string             `json:"msg"`
	Error     string             `json:"error"`
	ErrorType string             `json:"error_type"`
	Data      unifiedRuntimeData `json:"data"`
}

func readUnifiedConfig(t *testing.T, f *handlerFixture, path string, headers []ut.Header) (*ut.ResponseRecorder, unifiedRuntimeEnvelope) {
	t.Helper()
	response := ut.PerformRequest(f.engine.Engine, "GET", path, nil, headers...)
	var envelope unifiedRuntimeEnvelope
	if response.Code == 200 {
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode %s: %v (%s)", path, err, response.Body.String())
		}
	}
	return response, envelope
}

func unifiedConfigByAlias(t *testing.T, data unifiedRuntimeData, alias string) unifiedRuntimeConfig {
	t.Helper()
	for _, cfg := range data.Configs {
		if cfg.Alias == alias {
			return cfg
		}
	}
	t.Fatalf("config %s missing from %+v", alias, data.Configs)
	return unifiedRuntimeConfig{}
}

func unifiedContentString(t *testing.T, cfg unifiedRuntimeConfig) string {
	t.Helper()
	content, ok := cfg.Content.(string)
	if !ok {
		t.Fatalf("%s content must be a string, got %T (%v)", cfg.Alias, cfg.Content, cfg.Content)
	}
	return content
}

func unifiedLinkURL(t *testing.T, content string) string {
	t.Helper()
	var link map[string]any
	if err := json.Unmarshal([]byte(content), &link); err != nil {
		t.Fatalf("web_link content is not JSON: %v (%s)", err, content)
	}
	url, _ := link["url"].(string)
	if url == "" {
		t.Fatalf("web_link url missing: %s", content)
	}
	return url
}

func downloadUnifiedAsset(t *testing.T, f *handlerFixture, rawURL string) []byte {
	t.Helper()
	if !strings.HasPrefix(rawURL, handlerBasePath+"/api/v1/release/asset/") {
		t.Fatalf("asset URL must stay under the release route: %s", rawURL)
	}
	response := ut.PerformRequest(f.engine.Engine, "GET", strings.TrimPrefix(rawURL, handlerBasePath), nil)
	if response.Code != 200 {
		t.Fatalf("download %s: %d %s", rawURL, response.Code, response.Body.String())
	}
	return response.Body.Bytes()
}

func TestRuntimeConfigUnifiedReadHTTP(t *testing.T) {
	f := newHandlerFixture(t)
	runtimehandler.SetService(f.svc)
	t.Cleanup(func() { runtimehandler.SetService(nil) })
	runtimerouter.Register(f.engine)
	f.engine.GET("/api/v1/release/asset/*filepath", Asset)

	ctx := service.WithAdminIdentity(context.Background(), service.AdminIdentity{UserID: 1, Username: "admin", Role: "admin"})
	const unifiedEnv = "qa_unified"
	if err := f.svc.AddEnvironment(ctx, &envpb.Environment{EnvironmentKey: unifiedEnv, EnvironmentName: "QA Unified", IsActive: true}); err != nil {
		t.Fatalf("add environment: %v", err)
	}
	if err := f.svc.AddPipeline(ctx, unifiedEnv, &plpb.Pipeline{PipelineKey: handlerPipeline, PipelineName: "main", IsActive: true}); err != nil {
		t.Fatalf("add pipeline: %v", err)
	}
	pipeline, err := f.svc.GetPipeline(ctx, unifiedEnv, handlerPipeline)
	if err != nil {
		t.Fatal(err)
	}
	scopeID := pipeline.GetScopeId()
	headers := []ut.Header{{Key: "x-environment", Value: unifiedEnv}, {Key: "x-pipeline", Value: handlerPipeline}}

	upload := func(name string, data []byte) string {
		t.Helper()
		_, reference, err := f.svc.UploadAsset(ctx, &service.FileUploadInput{
			EnvironmentKey: unifiedEnv, PipelineKey: handlerPipeline,
			FileName: name, ContentType: "image/png", Data: data,
		})
		if err != nil {
			t.Fatalf("upload %s: %v", name, err)
		}
		return reference
	}
	add := func(alias, typ, content string) *commonpb.ResourceConfig {
		t.Helper()
		cfg, err := f.svc.AddConfig(ctx, &commonpb.ResourceConfig{
			Alias: alias, Name: alias, Type: typ, Content: content,
			EnvironmentKey: unifiedEnv, PipelineKey: handlerPipeline,
		})
		if err != nil {
			t.Fatalf("add %s: %v", alias, err)
		}
		return cfg
	}
	update := func(cfg *commonpb.ResourceConfig, content string) {
		t.Helper()
		current, err := f.svc.GetConfigDetail(ctx, unifiedEnv, handlerPipeline, cfg.ResourceKey)
		if err != nil {
			t.Fatalf("detail %s: %v", cfg.Alias, err)
		}
		current.Content = content
		if _, err := f.svc.UpdateConfig(ctx, current, current.EditRevision); err != nil {
			t.Fatalf("update %s: %v", cfg.Alias, err)
		}
	}
	unlock := func() {
		t.Helper()
		status, err := f.svc.FreezeStatus(ctx, unifiedEnv, handlerPipeline)
		if err != nil {
			t.Fatalf("freeze status: %v", err)
		}
		if status.State != "locked" {
			return
		}
		if _, err := f.svc.UnlockFreeze(ctx, unifiedEnv, handlerPipeline, "统一读取测试", strconv.FormatInt(status.Revision, 10)); err != nil {
			t.Fatalf("unlock: %v", err)
		}
	}

	logoV1 := []byte("LOGO-V1-BYTES")
	faviconV1 := []byte("FAVICON-V1-BYTES")
	text := add("QA_TEXT", "text", "A版本")
	link := add("QA_LINK", "web_link", `{"url":"https://v1.example.com/home"}`)
	logo := add("APP_LOGO", "image", upload("logo-v1.png", logoV1))
	favicon := add("APP_FAVICON", "image", upload("favicon-v1.png", faviconV1))
	v1 := lockAndPublish(t, f.svc, unifiedEnv, handlerPipeline, "v1")

	unlock()
	logoV2 := []byte("LOGO-V2-BYTES")
	faviconV2 := []byte("FAVICON-V2-BYTES")
	update(text, "B版本")
	update(link, `{"url":"https://v2.example.com/home"}`)
	update(logo, upload("logo-v2.png", logoV2))
	update(favicon, upload("favicon-v2.png", faviconV2))
	v2 := lockAndPublish(t, f.svc, unifiedEnv, handlerPipeline, "v2")

	// 1. 旧 Insights 请求:无 version/content_format,读取生效版本 v2,旧字段与类型保持不变。
	response, activeData := readUnifiedConfig(t, f, "/api/v1/runtime/config", headers)
	if response.Code != 200 {
		t.Fatalf("legacy config read: %d %s", response.Code, response.Body.String())
	}
	if got := string(response.Header().Peek("X-Rainbow-Version")); got != "v2" {
		t.Fatalf("legacy version header = %q", got)
	}
	if got := string(response.Header().Peek("X-Rainbow-Snapshot-Id")); got != v2.Meta.SnapshotID {
		t.Fatalf("legacy snapshot header = %q, want %s", got, v2.Meta.SnapshotID)
	}
	if got := string(response.Header().Peek("Cache-Control")); got != "no-store" {
		t.Fatalf("legacy cache control = %q", got)
	}
	if activeData.Code != 200 || activeData.Msg != "OK" || activeData.Error != "" {
		t.Fatalf("legacy envelope: %+v", activeData)
	}
	if activeData.Data.Version != "v2" || activeData.Data.SelectionMode != "active" ||
		activeData.Data.SnapshotID != v2.Meta.SnapshotID || activeData.Data.ScopeID != scopeID ||
		activeData.Data.ActiveRevision == "" || activeData.Data.SourceFreezeRevision == "" {
		t.Fatalf("legacy metadata: %+v", activeData.Data)
	}
	if activeData.Data.Environment.EnvironmentKey != unifiedEnv ||
		activeData.Data.Environment.PipelineKey != handlerPipeline ||
		activeData.Data.Environment.ScopeID != scopeID {
		t.Fatalf("legacy environment: %+v", activeData.Data.Environment)
	}
	for _, cfg := range activeData.Data.Configs {
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"resource_key", "alias", "name", "type", "content", "remark", "is_perm", "description", "environment_key", "pipeline_key", "scope_id"} {
			if _, ok := fields[field]; !ok {
				t.Fatalf("legacy config %s missing field %s", cfg.Alias, field)
			}
		}
		if cfg.EnvironmentKey != unifiedEnv || cfg.PipelineKey != handlerPipeline || cfg.ScopeID != scopeID {
			t.Fatalf("legacy config scope fields: %+v", cfg)
		}
	}
	if got := unifiedContentString(t, unifiedConfigByAlias(t, activeData.Data, "QA_TEXT")); got != "B版本" {
		t.Fatalf("legacy text content = %q", got)
	}
	// 默认格式下 web_link 仍是字符串;Logo/favicon 是字符串资源地址。
	linkText := unifiedContentString(t, unifiedConfigByAlias(t, activeData.Data, "QA_LINK"))
	if got := unifiedLinkURL(t, linkText); got != "https://v2.example.com/home" {
		t.Fatalf("legacy web_link url = %q", got)
	}
	if got := downloadUnifiedAsset(t, f, unifiedContentString(t, unifiedConfigByAlias(t, activeData.Data, "APP_LOGO"))); string(got) != string(logoV2) {
		t.Fatalf("logo bytes = %q", got)
	}
	if got := downloadUnifiedAsset(t, f, unifiedContentString(t, unifiedConfigByAlias(t, activeData.Data, "APP_FAVICON"))); string(got) != string(faviconV2) {
		t.Fatalf("favicon bytes = %q", got)
	}
	t.Logf("config default: status=%d version=%s snapshot=%s mode=%s text=%q link=%s logo_bytes=%d",
		response.Code, activeData.Data.Version, activeData.Data.SnapshotID, activeData.Data.SelectionMode,
		unifiedContentString(t, unifiedConfigByAlias(t, activeData.Data, "QA_TEXT")), linkText, len(logoV2))

	// 2. 指定版本读取 v1,随后不带版本仍读 v2;固定读取不改变生效指针。
	fixedPath := "/api/v1/runtime/config?version=v1&scope_id=" + scopeID
	response, fixedData := readUnifiedConfig(t, f, fixedPath, headers)
	if response.Code != 200 || fixedData.Code != 200 {
		t.Fatalf("fixed config read: %d %s", response.Code, response.Body.String())
	}
	if fixedData.Data.Version != "v1" || fixedData.Data.SelectionMode != "fixed" ||
		fixedData.Data.SnapshotID != v1.Meta.SnapshotID || fixedData.Data.ActiveRevision != "" {
		t.Fatalf("fixed metadata: %+v", fixedData.Data)
	}
	if got := string(response.Header().Peek("X-Rainbow-Version")); got != "v1" {
		t.Fatalf("fixed version header = %q", got)
	}
	if got := unifiedContentString(t, unifiedConfigByAlias(t, fixedData.Data, "QA_TEXT")); got != "A版本" {
		t.Fatalf("fixed text content = %q", got)
	}
	if got := unifiedLinkURL(t, unifiedContentString(t, unifiedConfigByAlias(t, fixedData.Data, "QA_LINK"))); got != "https://v1.example.com/home" {
		t.Fatalf("fixed web_link url = %q", got)
	}
	if got := downloadUnifiedAsset(t, f, unifiedContentString(t, unifiedConfigByAlias(t, fixedData.Data, "APP_LOGO"))); string(got) != string(logoV1) {
		t.Fatalf("fixed logo bytes = %q", got)
	}
	if got := downloadUnifiedAsset(t, f, unifiedContentString(t, unifiedConfigByAlias(t, fixedData.Data, "APP_FAVICON"))); string(got) != string(faviconV1) {
		t.Fatalf("fixed favicon bytes = %q", got)
	}
	if _, afterFixed := readUnifiedConfig(t, f, "/api/v1/runtime/config", headers); afterFixed.Data.Version != "v2" ||
		afterFixed.Data.SnapshotID != v2.Meta.SnapshotID {
		t.Fatalf("fixed read changed active: %+v", afterFixed.Data)
	}
	t.Logf("config fixed: status=%d version=%s snapshot=%s mode=%s active_revision=%q text=%q",
		response.Code, fixedData.Data.Version, fixedData.Data.SnapshotID, fixedData.Data.SelectionMode,
		fixedData.Data.ActiveRevision, unifiedContentString(t, unifiedConfigByAlias(t, fixedData.Data, "QA_TEXT")))

	// 3. 同一个 web_link 的三种表示:默认字符串、json 对象、raw 字符串。
	_, jsonData := readUnifiedConfig(t, f, "/api/v1/runtime/config?content_format=json&scope_id="+scopeID, headers)
	linkContent := unifiedConfigByAlias(t, jsonData.Data, "QA_LINK").Content
	linkObject, ok := linkContent.(map[string]any)
	if !ok {
		t.Fatalf("json format must decode web_link as object: %T (%v)", linkContent, linkContent)
	}
	if linkObject["url"] != "https://v2.example.com/home" {
		t.Fatalf("json web_link url = %v", linkObject["url"])
	}
	if _, ok := unifiedConfigByAlias(t, jsonData.Data, "QA_TEXT").Content.(string); !ok {
		t.Fatal("json format must keep text content as string")
	}

	_, rawData := readUnifiedConfig(t, f, "/api/v1/runtime/config?content_format=raw&scope_id="+scopeID, headers)
	for _, cfg := range rawData.Data.Configs {
		if _, ok := cfg.Content.(string); !ok {
			t.Fatalf("raw format must return every content as string: %s = %T", cfg.Alias, cfg.Content)
		}
	}
	if got := unifiedContentString(t, unifiedConfigByAlias(t, rawData.Data, "QA_TEXT")); got != "B版本" {
		t.Fatalf("raw text content = %q", got)
	}
	rawLink := unifiedContentString(t, unifiedConfigByAlias(t, rawData.Data, "QA_LINK"))
	if got := unifiedLinkURL(t, rawLink); got != "https://v2.example.com/home" {
		t.Fatalf("raw web_link url = %q", got)
	}
	_, rawFixed := readUnifiedConfig(t, f, "/api/v1/runtime/config?version=v1&content_format=raw&scope_id="+scopeID, headers)
	if got := unifiedContentString(t, unifiedConfigByAlias(t, rawFixed.Data, "QA_TEXT")); got != "A版本" {
		t.Fatalf("raw fixed text content = %q", got)
	}
	t.Logf("web_link formats: default=%T(%s) json=%T(%v) raw=%T(%s)",
		unifiedConfigByAlias(t, activeData.Data, "QA_LINK").Content, linkText, linkContent, linkObject, rawLink, rawLink)

	// 空配置版本仍需保留 configs 为 []。
	const emptyEnv = "qa_unified_empty"
	if err := f.svc.AddEnvironment(ctx, &envpb.Environment{EnvironmentKey: emptyEnv, EnvironmentName: "QA Unified Empty", IsActive: true}); err != nil {
		t.Fatalf("add empty environment: %v", err)
	}
	if err := f.svc.AddPipeline(ctx, emptyEnv, &plpb.Pipeline{PipelineKey: handlerPipeline, PipelineName: "main", IsActive: true}); err != nil {
		t.Fatalf("add empty pipeline: %v", err)
	}
	lockAndPublish(t, f.svc, emptyEnv, handlerPipeline, "v1")
	emptyResponse, emptyData := readUnifiedConfig(t, f, "/api/v1/runtime/config",
		[]ut.Header{{Key: "x-environment", Value: emptyEnv}, {Key: "x-pipeline", Value: handlerPipeline}})
	if emptyResponse.Code != 200 || !strings.Contains(emptyResponse.Body.String(), `"configs":[]`) ||
		emptyData.Data.Configs == nil {
		t.Fatalf("empty configs must stay []: %d %s", emptyResponse.Code, emptyResponse.Body.String())
	}

	// 4. 失败语义:空版本、未知版本、快照校验参数、非法格式、范围身份。
	otherPipeline, err := f.svc.GetPipeline(ctx, handlerEnv, handlerPipeline)
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		label  string
		path   string
		status int
		kind   string
	}{
		{"empty version", "/api/v1/runtime/config?version=", 400, service.ErrTypeReleaseInvalidRequest},
		{"unknown version", "/api/v1/runtime/config?version=v9", 404, service.ErrTypeReleaseVersionNotFound},
		{"snapshot pin", "/api/v1/runtime/config?expected_snapshot_id=" + v1.Meta.SnapshotID, 400, service.ErrTypeReleaseInvalidRequest},
		{"empty format", "/api/v1/runtime/config?content_format=", 400, service.ErrTypeReleaseInvalidRequest},
		{"unknown format", "/api/v1/runtime/config?content_format=xml", 400, service.ErrTypeReleaseInvalidRequest},
		{"other scope", "/api/v1/runtime/config?scope_id=" + otherPipeline.GetScopeId(), 409, "SCOPE_IDENTITY_CONFLICT"},
		{"missing scope", "/api/v1/runtime/config?scope_id=11111111-1111-4111-8111-111111111111", 404, "SCOPE_NOT_FOUND"},
		{"bad scope", "/api/v1/runtime/config?scope_id=not-a-uuid", 400, "SCOPE_INVALID_REQUEST"},
	} {
		response, _ := readUnifiedConfig(t, f, sample.path, headers)
		if response.Code != sample.status || legacyErrorType(t, response.Body.Bytes()) != sample.kind {
			t.Fatalf("%s: %d type=%s", sample.label, response.Code, legacyErrorType(t, response.Body.Bytes()))
		}
		if got := string(response.Header().Peek("Cache-Control")); got != "no-store" {
			t.Fatalf("%s cache control = %q", sample.label, got)
		}
		t.Logf("config error: %s -> %d %s", sample.label, response.Code, sample.kind)
	}
	if response, _ := readUnifiedConfig(t, f, "/api/v1/runtime/config", nil); response.Code != 400 ||
		legacyErrorType(t, response.Body.Bytes()) != service.ErrTypeReleaseInvalidRequest {
		t.Fatalf("missing headers must be 400 RELEASE_INVALID_REQUEST, got %d %s", response.Code, response.Body.String())
	}

	// 5. 原有入口保持不变:release 仍按 scope_id 读生效版本,链接为对象;固定与 raw 沿用原行为。
	releaseResp := ut.PerformRequest(f.engine.Engine, "GET", "/api/v1/runtime/release?scope_id="+scopeID, nil, headers...)
	if releaseResp.Code != 200 {
		t.Fatalf("release read: %d %s", releaseResp.Code, releaseResp.Body.String())
	}
	var legacyRelease struct {
		Data struct {
			SnapshotID    string `json:"snapshot_id"`
			Version       string `json:"version"`
			SelectionMode string `json:"selection_mode"`
			Configs       []struct {
				Alias   string `json:"alias"`
				Content any    `json:"content"`
			} `json:"configs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(releaseResp.Body.Bytes(), &legacyRelease); err != nil {
		t.Fatal(err)
	}
	if legacyRelease.Data.Version != "v2" || legacyRelease.Data.SelectionMode != "active" {
		t.Fatalf("release metadata: %+v", legacyRelease.Data)
	}
	t.Logf("release compat: status=%d version=%s snapshot=%s mode=%s",
		releaseResp.Code, legacyRelease.Data.Version, legacyRelease.Data.SnapshotID, legacyRelease.Data.SelectionMode)
	linkChecked := false
	for _, cfg := range legacyRelease.Data.Configs {
		if cfg.Alias != "QA_LINK" {
			continue
		}
		object, ok := cfg.Content.(map[string]any)
		if !ok || object["url"] != "https://v2.example.com/home" {
			t.Fatalf("release web_link must stay an object: %T (%v)", cfg.Content, cfg.Content)
		}
		linkChecked = true
	}
	if !linkChecked {
		t.Fatal("release response lost the web_link config")
	}

	fixedRelease := ut.PerformRequest(f.engine.Engine, "GET", "/api/v1/runtime/release?version=v1", nil, headers...)
	if fixedRelease.Code != 200 || !strings.Contains(fixedRelease.Body.String(), `"version":"v1"`) ||
		!strings.Contains(fixedRelease.Body.String(), `"selection_mode":"fixed"`) {
		t.Fatalf("release fixed read: %d %s", fixedRelease.Code, fixedRelease.Body.String())
	}
	rawRelease := ut.PerformRequest(f.engine.Engine, "GET", "/api/v1/runtime/release?version=v1&content_format=raw", nil, headers...)
	if rawRelease.Code != 200 || !strings.Contains(rawRelease.Body.String(), `"content":"A版本"`) {
		t.Fatalf("release raw read: %d %s", rawRelease.Code, rawRelease.Body.String())
	}

	// 6. 生效 ZIP 仍由 static 导出口提供,显式 version 继续按 400 拒绝。
	static := ut.PerformRequest(f.engine.Engine, "GET",
		"/api/v1/runtime/static?environment_key="+unifiedEnv+"&pipeline_key="+handlerPipeline, nil)
	if static.Code != 200 || !strings.Contains(string(static.Header().ContentType()), "application/zip") ||
		string(static.Header().Peek("X-Rainbow-Version")) != "v2" {
		t.Fatalf("active static export: %d %s", static.Code, static.Body.String())
	}
	t.Logf("runtime static: status=%d version=%s snapshot=%s zip_bytes=%d",
		static.Code, string(static.Header().Peek("X-Rainbow-Version")),
		string(static.Header().Peek("X-Rainbow-Snapshot-Id")), static.Body.Len())
	staticVersion := ut.PerformRequest(f.engine.Engine, "GET",
		"/api/v1/runtime/static?environment_key="+unifiedEnv+"&pipeline_key="+handlerPipeline+"&version=v1", nil)
	if staticVersion.Code != 400 || legacyErrorType(t, staticVersion.Body.Bytes()) != service.ErrTypeReleaseInvalidRequest {
		t.Fatalf("static must reject version: %d %s", staticVersion.Code, staticVersion.Body.String())
	}

	// 7. 逻辑删除后的固定版本仍按 410 拒绝。
	if _, err := f.svc.DeleteRelease(ctx, service.ReleaseDeleteInput{
		ExpectedScopeID: scopeID, EnvironmentKey: unifiedEnv, PipelineKey: handlerPipeline, Version: "v1",
		ExpectedSnapshotID: v1.Meta.SnapshotID, RequestID: uuid.NewString(), Reason: "统一读取测试删除旧版",
	}); err != nil {
		t.Fatalf("delete v1: %v", err)
	}
	deleted, _ := readUnifiedConfig(t, f, "/api/v1/runtime/config?version=v1", headers)
	if deleted.Code != 410 || legacyErrorType(t, deleted.Body.Bytes()) != service.ErrTypeReleaseVersionDeleted {
		t.Fatalf("deleted version must be 410: %d %s", deleted.Code, deleted.Body.String())
	}
}
