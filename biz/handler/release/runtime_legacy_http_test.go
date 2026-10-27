package release

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/google/uuid"
	runtimehandler "github.com/yi-nology/rainbow_bridge/biz/handler/runtime"
	envpb "github.com/yi-nology/rainbow_bridge/biz/model/environment"
	plpb "github.com/yi-nology/rainbow_bridge/biz/model/pipeline"
	"github.com/yi-nology/rainbow_bridge/biz/service"
)

func legacyErrorType(t *testing.T, body []byte) string {
	t.Helper()
	var envelope struct {
		ErrorType string `json:"error_type"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("legacy error is not JSON: %s", body)
	}
	return envelope.ErrorType
}

func TestLegacyRuntimeScopePinContract(t *testing.T) {
	f := newHandlerFixture(t)
	runtimehandler.SetService(f.svc)
	t.Cleanup(func() { runtimehandler.SetService(nil) })
	f.engine.GET("/api/v1/runtime/config", runtimehandler.GetConfig)
	f.engine.GET("/api/v1/runtime/static", runtimehandler.ExportStatic)
	pipeline, err := f.svc.GetPipeline(context.Background(), handlerEnv, handlerPipeline)
	if err != nil {
		t.Fatal(err)
	}
	scopeID := pipeline.GetScopeId()
	headers := []ut.Header{{Key: "x-environment", Value: handlerEnv}, {Key: "x-pipeline", Value: handlerPipeline}}
	ctx := service.WithAdminIdentity(context.Background(), service.AdminIdentity{UserID: 1, Username: "admin", Role: "admin"})
	if err := f.svc.AddPipeline(ctx, handlerEnv, &plpb.Pipeline{PipelineKey: "other", PipelineName: "other", IsActive: true}); err != nil && err != service.ErrPipelineKeyExists {
		t.Fatal(err)
	}
	other, err := f.svc.GetPipeline(context.Background(), handlerEnv, "other")
	if err != nil {
		t.Fatal(err)
	}

	configPin := "/api/v1/runtime/config?scope_id=" + scopeID
	staticPin := "/api/v1/runtime/static?environment_key=" + handlerEnv + "&pipeline_key=" + handlerPipeline + "&scope_id=" + scopeID
	if r := ut.PerformRequest(f.engine.Engine, "GET", configPin, nil, headers...); r.Code != 200 ||
		string(r.Header().Peek("X-Rainbow-Snapshot-Id")) != f.snapshotID {
		t.Fatalf("correct config pin: %d %s", r.Code, r.Body.String())
	}
	if r := ut.PerformRequest(f.engine.Engine, "GET", staticPin, nil); r.Code != 200 ||
		!strings.Contains(string(r.Header().ContentType()), "application/zip") {
		t.Fatalf("correct static pin: %d %s", r.Code, r.Body.String())
	}

	// scope_id pins the generation, not a snapshot: a correct pin follows the
	// newly activated version in the same scope.
	second, err := f.svc.CreateRelease(ctx, service.ReleaseCreateInput{
		EnvironmentKey: handlerEnv, PipelineKey: handlerPipeline, Version: "v2.0",
		ExpectedScopeID: scopeID, ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	active, err := f.svc.ReleaseActive(ctx, handlerEnv, handlerPipeline)
	if err != nil || active == nil {
		t.Fatalf("active: %+v %v", active, err)
	}
	if _, err := f.svc.ActivateRelease(ctx, service.ReleaseActivateInput{
		EnvironmentKey: handlerEnv, PipelineKey: handlerPipeline, Version: "v2.0",
		ExpectedScopeID: scopeID, ExpectedActiveRevisionRaw: strconv.FormatInt(active.Revision, 10),
		RequestID: uuid.NewString(),
	}); err != nil {
		t.Fatal(err)
	}
	newPin := ut.PerformRequest(f.engine.Engine, "GET", configPin, nil, headers...)
	if newPin.Code != 200 || string(newPin.Header().Peek("X-Rainbow-Version")) != "v2.0" ||
		string(newPin.Header().Peek("X-Rainbow-Snapshot-Id")) != second.Meta.SnapshotID {
		t.Fatalf("pin did not follow new active: %d %s", newPin.Code, newPin.Body.String())
	}
	newStatic := ut.PerformRequest(f.engine.Engine, "GET", staticPin, nil)
	if newStatic.Code != 200 || string(newStatic.Header().Peek("X-Rainbow-Version")) != "v2.0" ||
		string(newStatic.Header().Peek("X-Rainbow-Snapshot-Id")) != second.Meta.SnapshotID {
		t.Fatalf("static pin did not follow new active: %d %s", newStatic.Code, newStatic.Body.String())
	}

	for _, suffix := range []string{"?scope_id=", "?scope_id=%20%20"} {
		if r := ut.PerformRequest(f.engine.Engine, "GET", "/api/v1/runtime/config"+suffix, nil, headers...); r.Code != 200 {
			t.Fatalf("empty config pin %q: %d %s", suffix, r.Code, r.Body.String())
		}
		staticPath := "/api/v1/runtime/static?environment_key=" + handlerEnv + "&pipeline_key=" + handlerPipeline
		if suffix == "?scope_id=" {
			staticPath += "&scope_id="
		} else {
			staticPath += "&scope_id=%20%20"
		}
		if r := ut.PerformRequest(f.engine.Engine, "GET", staticPath, nil); r.Code != 200 {
			t.Fatalf("empty static pin %q: %d %s", suffix, r.Code, r.Body.String())
		}
	}

	for _, item := range []struct {
		label  string
		path   string
		status int
		kind   string
	}{
		{"config conflict", "/api/v1/runtime/config?scope_id=" + other.GetScopeId(), 409, "SCOPE_IDENTITY_CONFLICT"},
		{"static conflict", "/api/v1/runtime/static?environment_key=" + handlerEnv + "&pipeline_key=" + handlerPipeline + "&scope_id=" + other.GetScopeId(), 409, "SCOPE_IDENTITY_CONFLICT"},
		{"config missing", "/api/v1/runtime/config?scope_id=" + "11111111-1111-4111-8111-111111111111", 404, "SCOPE_NOT_FOUND"},
		{"static missing", "/api/v1/runtime/static?environment_key=" + handlerEnv + "&pipeline_key=" + handlerPipeline + "&scope_id=11111111-1111-4111-8111-111111111111", 404, "SCOPE_NOT_FOUND"},
		{"config invalid", "/api/v1/runtime/config?scope_id=not-a-uuid", 400, "SCOPE_INVALID_REQUEST"},
		{"static invalid", "/api/v1/runtime/static?environment_key=" + handlerEnv + "&pipeline_key=" + handlerPipeline + "&scope_id=not-a-uuid", 400, "SCOPE_INVALID_REQUEST"},
	} {
		r := ut.PerformRequest(f.engine.Engine, "GET", item.path, nil, headers...)
		if r.Code != item.status || legacyErrorType(t, r.Body.Bytes()) != item.kind ||
			string(r.Header().Peek("Cache-Control")) != "no-store" {
			t.Fatalf("%s: %d type=%s cache=%s", item.label, r.Code, legacyErrorType(t, r.Body.Bytes()), r.Header().Peek("Cache-Control"))
		}
		if strings.Contains(string(r.Header().ContentType()), "application/zip") ||
			len(r.Header().Peek("Content-Disposition")) > 0 {
			t.Fatalf("%s returned a download", item.label)
		}
	}

	// Same-key delete/recreate: old pin must stay deleted, new pin is a new empty generation.
	recreateEnv := "recreate_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	if err := f.svc.AddEnvironment(ctx, &envpb.Environment{EnvironmentKey: recreateEnv, EnvironmentName: recreateEnv, IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AddPipeline(ctx, recreateEnv, &plpb.Pipeline{PipelineKey: handlerPipeline, PipelineName: "main", IsActive: true}); err != nil && err != service.ErrPipelineKeyExists {
		t.Fatal(err)
	}
	created := lockAndPublish(t, f.svc, recreateEnv, handlerPipeline, "scope-pin-v1")
	oldPipeline, err := f.svc.GetPipeline(context.Background(), recreateEnv, handlerPipeline)
	if err != nil {
		t.Fatal(err)
	}
	oldScope := oldPipeline.GetScopeId()
	status, err := f.svc.FreezeStatus(ctx, recreateEnv, handlerPipeline)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.UnlockFreeze(ctx, recreateEnv, handlerPipeline, "test", strconv.FormatInt(status.Revision, 10)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.DeletePipelineWithIdentity(ctx, recreateEnv, handlerPipeline, oldScope); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AddPipeline(ctx, recreateEnv, &plpb.Pipeline{PipelineKey: handlerPipeline, PipelineName: "recreated", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	newPipeline, err := f.svc.GetPipeline(context.Background(), recreateEnv, handlerPipeline)
	if err != nil {
		t.Fatal(err)
	}
	if newPipeline.GetScopeId() == oldScope {
		t.Fatal("recreated scope reused old identity")
	}
	recreateHeaders := []ut.Header{{Key: "x-environment", Value: recreateEnv}, {Key: "x-pipeline", Value: handlerPipeline}}
	for _, path := range []string{
		"/api/v1/runtime/config?scope_id=" + oldScope,
		"/api/v1/runtime/static?environment_key=" + recreateEnv + "&pipeline_key=" + handlerPipeline + "&scope_id=" + oldScope,
	} {
		r := ut.PerformRequest(f.engine.Engine, "GET", path, nil, recreateHeaders...)
		if r.Code != 410 || legacyErrorType(t, r.Body.Bytes()) != "SCOPE_DELETED" {
			t.Fatalf("old pin after recreate: %d %s", r.Code, r.Body.String())
		}
	}
	for _, path := range []string{
		"/api/v1/runtime/config?scope_id=" + newPipeline.GetScopeId(),
		"/api/v1/runtime/static?environment_key=" + recreateEnv + "&pipeline_key=" + handlerPipeline + "&scope_id=" + newPipeline.GetScopeId(),
	} {
		r := ut.PerformRequest(f.engine.Engine, "GET", path, nil, recreateHeaders...)
		if r.Code != 404 || legacyErrorType(t, r.Body.Bytes()) != "RELEASE_NOT_ACTIVE" {
			t.Fatalf("new pin without active: %d %s", r.Code, r.Body.String())
		}
	}
	if created.Meta.ScopeID != oldScope {
		t.Fatal("fixture version did not belong to the old scope")
	}
}

func TestLegacyRuntimeUsesPublishedConfigAndStaticAssets(t *testing.T) {
	f := newHandlerFixture(t)
	runtimehandler.SetService(f.svc)
	t.Cleanup(func() { runtimehandler.SetService(nil) })
	f.engine.GET("/api/v1/runtime/config", runtimehandler.GetConfig)
	f.engine.GET("/api/v1/runtime/static", runtimehandler.ExportStatic)
	headers := []ut.Header{{Key: "x-environment", Value: handlerEnv}, {Key: "x-pipeline", Value: handlerPipeline}}
	read := ut.PerformRequest(f.engine.Engine, "GET", "/api/v1/runtime/config", nil, headers...)
	if read.Code != 200 || !strings.Contains(read.Body.String(), handlerBigNumber) {
		t.Fatalf("legacy read: %d %s", read.Code, read.Body.String())
	}
	var body struct {
		Data struct {
			Snapshot string           `json:"snapshot_id"`
			Configs  []map[string]any `json:"configs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Snapshot != f.snapshotID || string(read.Header().Peek("X-Rainbow-Snapshot-Id")) != f.snapshotID {
		t.Fatal("legacy response snapshot mismatch")
	}
	for _, config := range body.Data.Configs {
		for _, name := range []string{"environment_key", "pipeline_key", "description", "remark", "is_perm", "content"} {
			if _, ok := config[name]; !ok {
				t.Fatalf("missing legacy field %s", name)
			}
		}
	}
	response := ut.PerformRequest(f.engine.Engine, "GET", "/api/v1/runtime/static?environment_key="+handlerEnv+"&pipeline_key="+handlerPipeline, nil)
	if response.Code != 200 || !strings.Contains(string(response.Header().ContentType()), "application/zip") {
		t.Fatalf("legacy export: %d %s", response.Code, response.Body.String())
	}
	archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	assetFound, manifestFound := false, false
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if file.Name == "manifest.json" {
			manifestFound = true
		}
		if strings.HasPrefix(file.Name, "assets/") && bytes.Equal(data, f.assetBytes) {
			assetFound = true
		}
		if file.Name == "config.json" {
			if !bytes.Contains(data, []byte(handlerBigNumber)) || bytes.Contains(data, []byte("/api/v1/asset/file/")) || bytes.Contains(data, []byte("/api/v1/release/asset/")) {
				t.Fatal("legacy ZIP leaks online paths or loses precision")
			}
		}
	}
	if !assetFound || !manifestFound {
		t.Fatal("legacy ZIP missing snapshot asset or manifest")
	}
	ctx := service.WithAdminIdentity(context.Background(), service.AdminIdentity{UserID: 1, Username: "admin", Role: "admin"})
	if err := f.svc.AddEnvironment(ctx, &envpb.Environment{EnvironmentKey: "unpublished", EnvironmentName: "unpublished", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AddPipeline(ctx, "unpublished", &plpb.Pipeline{PipelineKey: "main", PipelineName: "main", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		path    string
		headers []ut.Header
		status  int
	}{
		{"/api/v1/runtime/config", nil, 400},
		{"/api/v1/runtime/config?version=v1.0", headers, 400},
		{"/api/v1/runtime/static?environment_key=" + handlerEnv + "&pipeline_key=main&version=v1.0", nil, 400},
		{"/api/v1/runtime/config", []ut.Header{{Key: "x-environment", Value: "unpublished"}, {Key: "x-pipeline", Value: "main"}}, 404},
		{"/api/v1/runtime/static?environment_key=unpublished&pipeline_key=main", nil, 404},
	} {
		got := ut.PerformRequest(f.engine.Engine, "GET", sample.path, nil, sample.headers...)
		if got.Code != sample.status || string(got.Header().Peek("Cache-Control")) != "no-store" || !strings.Contains(got.Body.String(), "error_type") {
			t.Fatalf("legacy error: %s -> %d %s", sample.path, got.Code, got.Body.String())
		}
	}
}
