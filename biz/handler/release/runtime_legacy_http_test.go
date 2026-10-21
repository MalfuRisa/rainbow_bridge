package release

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/common/ut"
	runtimehandler "github.com/yi-nology/rainbow_bridge/biz/handler/runtime"
	envpb "github.com/yi-nology/rainbow_bridge/biz/model/environment"
	plpb "github.com/yi-nology/rainbow_bridge/biz/model/pipeline"
	"github.com/yi-nology/rainbow_bridge/biz/service"
)

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
