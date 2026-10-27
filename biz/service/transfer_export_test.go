package service

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

func TestExportSourcesEditorActiveAndVersionAreDistinct(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()
	scope, err := resolveScopeByKeys(ctx, svc.logic, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}

	v1, err := fixture.create(t, "v1.0", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.activate(t, "v1.0", "0", ""); err != nil {
		t.Fatal(err)
	}
	// B: second frozen snapshot, left inactive.
	if _, err := svc.UnlockFreeze(ctx, "qa_prod", "main", "edit B", "1"); err != nil {
		t.Fatal(err)
	}
	updateEditorContent(t, svc, "REL_TEXT", "B-active-but-not-current")
	if _, err := svc.LockFreeze(ctx, "qa_prod", "main", "snapshot B", "2"); err != nil {
		t.Fatal(err)
	}
	v2, err := fixture.create(t, "v2.0", "3", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UnlockFreeze(ctx, "qa_prod", "main", "edit C", "3"); err != nil {
		t.Fatal(err)
	}
	updateEditorContent(t, svc, "REL_TEXT", "C-editor-current")

	editor := exportToMap(t, svc, ExportRequest{
		Format: "zip", Source: TransferSourceEditor,
		Selections: []*ExportSelection{{EnvironmentKey: "qa_prod", PipelineKey: "main"}},
	})
	active := exportToMap(t, svc, ExportRequest{
		Format: "zip", Source: TransferSourceActive,
		EnvironmentKey: "qa_prod", PipelineKey: "main",
		ExpectedScopeID: scope.ScopeID, ExpectedSnapshotID: v1.Meta.SnapshotID,
	})
	versionPlan := exportToMap(t, svc, ExportRequest{
		Format: "zip", Source: TransferSourceVersion,
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v2.0",
		ExpectedScopeID: scope.ScopeID, ExpectedSnapshotID: v2.Meta.SnapshotID,
	})

	if contentOf(t, editor, "REL_TEXT") != "C-editor-current" {
		t.Fatalf("editor export content = %q", contentOf(t, editor, "REL_TEXT"))
	}
	if contentOf(t, active, "REL_TEXT") != "api=https://api.example.com/v1/endpoint" {
		t.Fatalf("active export content = %q", contentOf(t, active, "REL_TEXT"))
	}
	if contentOf(t, versionPlan, "REL_TEXT") != "B-active-but-not-current" {
		t.Fatalf("version export content = %q", contentOf(t, versionPlan, "REL_TEXT"))
	}
	if sourceKind(t, active) != TransferSourceActive || sourceKind(t, versionPlan) != TransferSourceVersion {
		t.Fatal("export_source.kind is not recorded")
	}

	preview, err := svc.PreviewExport(ctx, ExportRequest{
		Source: TransferSourceVersion, EnvironmentKey: "qa_prod", PipelineKey: "main",
		Version: "v2.0", ExpectedScopeID: scope.ScopeID, ExpectedSnapshotID: v2.Meta.SnapshotID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Configs != v2.Meta.ConfigCount || preview.Assets != v2.Meta.AssetCount {
		t.Fatalf("preview counts = %+v", preview)
	}
}

func TestVersionArchiveImportsIntoAnotherScopeWithMappedResources(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()
	scope, err := resolveScopeByKeys(ctx, svc.logic, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	created, err := fixture.create(t, "v1.0", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.ExportWithSource(ctx, ExportRequest{
		Format: "zip", Source: TransferSourceVersion,
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
		ExpectedScopeID: scope.ScopeID, ExpectedSnapshotID: created.Meta.SnapshotID,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(result.TempPath)
	data, err := os.ReadFile(result.TempPath)
	if err != nil {
		t.Fatal(err)
	}

	seedFreezeScope(t, svc, "qa_import", "main")
	targetEnv, err := resolveEnvironmentByKey(ctx, svc.logic, "qa_import")
	if err != nil {
		t.Fatal(err)
	}
	targetScope, err := resolveScopeByKeys(ctx, svc.logic, "qa_import", "main")
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := svc.ImportArchiveWithIdentity(ctx, data, "zip", "qa_import", "main",
		targetEnv.EnvironmentID, targetScope.ScopeID, nil, false)
	if err != nil {
		t.Fatalf("import v2 archive: %v", err)
	}
	if len(outcome.Configs) != created.Meta.ConfigCount {
		t.Fatalf("imported configs = %d", len(outcome.Configs))
	}
	var imported model.Config
	if err := svc.logic.db.Where("environment_key = ? AND pipeline_key = ? AND alias = ?",
		"qa_import", "main", "REL_IMAGE").First(&imported).Error; err != nil {
		t.Fatal(err)
	}
	newFileID := ""
	if strings.HasPrefix(imported.Content, "asset://") {
		newFileID = strings.TrimPrefix(imported.Content, "asset://")
	} else if idx := strings.Index(imported.Content, "/api/v1/asset/file/"); idx >= 0 {
		rest := imported.Content[idx+len("/api/v1/asset/file/"):]
		newFileID = strings.Split(rest, "/")[0]
	}
	if newFileID == "" {
		t.Fatalf("imported resource reference = %q", imported.Content)
	}
	oldFileID := fixture.fileA
	if newFileID == oldFileID {
		t.Fatal("source file id must not be reused across scopes")
	}
	asset, filePath, err := svc.GetAssetFile(ctx, newFileID)
	if err != nil {
		t.Fatalf("imported asset unreadable: %v", err)
	}
	if asset.EnvironmentKey != "qa_import" || asset.PipelineKey != "main" {
		t.Fatalf("imported asset scope = %s/%s", asset.EnvironmentKey, asset.PipelineKey)
	}
	payload, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "PNG-A-BYTES" {
		t.Fatalf("imported asset bytes = %q", payload)
	}
	// Re-import is idempotent for the same target scope.
	again, err := svc.ImportArchiveWithIdentity(ctx, data, "zip", "qa_import", "main",
		targetEnv.EnvironmentID, targetScope.ScopeID, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Configs) != 0 {
		t.Fatalf("duplicate import unexpectedly wrote configs: %d", len(again.Configs))
	}
}

func updateEditorContent(t *testing.T, svc *Service, alias, content string) {
	t.Helper()
	if err := svc.logic.db.Model(&model.Config{}).
		Where("alias = ?", alias).
		Update("content", content).Error; err != nil {
		t.Fatal(err)
	}
}

func exportToMap(t *testing.T, svc *Service, request ExportRequest) map[string]any {
	t.Helper()
	result, err := svc.ExportWithSource(adminTestContext(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(result.TempPath)
	reader, err := zip.OpenReader(result.TempPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, entry := range reader.File {
		if entry.Name != "configs.json" {
			continue
		}
		handle, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(handle)
		_ = handle.Close()
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	t.Fatal("configs.json missing")
	return nil
}

func contentOf(t *testing.T, archive map[string]any, resourceKey string) string {
	t.Helper()
	configs, _ := archive["business_configs"].([]any)
	for _, item := range configs {
		row, _ := item.(map[string]any)
		if row["resource_key"] == resourceKey || row["alias"] == resourceKey {
			value, _ := row["content"].(string)
			return value
		}
	}
	t.Fatalf("resource %s missing", resourceKey)
	return ""
}

func sourceKind(t *testing.T, archive map[string]any) string {
	t.Helper()
	source, _ := archive["export_source"].(map[string]any)
	kind, _ := source["kind"].(string)
	return kind
}

var _ = context.Background
var _ = path.Base
