package service

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

// releaseExportFixture 建立"已锁定范围 + 两份资源 + 覆盖各类型引用"的导出基线。
type releaseExportFixture struct {
	svc       *Service
	token     string
	fileA     string
	fileB     string
	sourceA   string
	sourceB   string
	basePath  string
	scopeEnv  string
	scopePipe string
}

func newReleaseExportFixture(t *testing.T) *releaseExportFixture {
	t.Helper()
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	const env, pipeline = "qa_prod", "main"
	seedFreezeScope(t, svc, env, pipeline)
	ctx := adminTestContext()

	fileA, fileB := "export-asset-a", "export-asset-b"
	writeUploadedFile(t, fileA, "pic-a.png", []byte("PNG-A-BYTES"))
	writeUploadedFile(t, fileB, "note-b.txt", []byte("NOTE-B-BYTES"))
	for _, asset := range []*model.Asset{
		{FileID: fileA, EnvironmentKey: env, PipelineKey: pipeline, FileName: "pic-a.png", ContentType: "image/png", FileSize: int64(len("PNG-A-BYTES")), Path: filepath.Join(uploadDirectory, fileA, "pic-a.png")},
		{FileID: fileB, EnvironmentKey: env, PipelineKey: pipeline, FileName: "note-b.txt", ContentType: "text/plain", FileSize: int64(len("NOTE-B-BYTES")), Path: filepath.Join(uploadDirectory, fileB, "note-b.txt")},
	} {
		if err := svc.logic.CreateAsset(ctx, asset); err != nil {
			t.Fatal(err)
		}
	}
	configs := map[string]struct{ typ, content string }{
		"IMAGE_A": {"image", "asset://" + fileA},
		"FILE_B":  {"file", "/rainbow-bridge/api/v1/asset/file/" + fileB + "/note-b.txt?x=(1)#frag"},
		"OBJ":     {"object", `{"big":` + releaseBigLiteral + `,"pic":"asset://` + fileA + `","nested":{"doc":"asset://` + fileB + `"}}`},
		"RICH": {"richtext", `<p>图文</p><img src="asset://` + fileA + `">` +
			`<img srcset="asset://` + fileB + `?v=1#top 1x, asset://` + fileA + ` 2x">` +
			`<a href="https://docs.example.com/guide?x=1#top">文档</a>`},
		"TEXT": {"text", "api=https://api.example.com/v1/endpoint"},
	}
	for alias, cfg := range configs {
		if _, err := svc.AddConfig(ctx, buildConfig(env, pipeline, alias, cfg.typ, cfg.content)); err != nil {
			t.Fatal(err)
		}
	}
	mustLock(t, svc, env, pipeline, "导出基线锁定")
	return &releaseExportFixture{
		svc: svc, token: testAdminToken(), fileA: fileA, fileB: fileB,
		sourceA:   filepath.Join(dataDirectory, uploadDirectory, fileA, "pic-a.png"),
		sourceB:   filepath.Join(dataDirectory, uploadDirectory, fileB, "note-b.txt"),
		basePath:  "/rainbow-bridge",
		scopeEnv:  env,
		scopePipe: pipeline,
	}
}

// lockFromCurrent 从当前 revision 锁定(解锁/改配置之后再锁版)。
func lockFromCurrent(t *testing.T, svc *Service, environmentKey, pipelineKey, reason string) {
	t.Helper()
	status, err := svc.FreezeStatus(adminTestContext(), environmentKey, pipelineKey)
	if err != nil {
		t.Fatalf("freeze status: %v", err)
	}
	if _, err := svc.LockFreeze(adminTestContext(), environmentKey, pipelineKey, reason, strconv.FormatInt(status.Revision, 10)); err != nil {
		t.Fatalf("lock from revision %d: %v", status.Revision, err)
	}
}

func (f *releaseExportFixture) create(t *testing.T, version string) *ReleaseCreateResult {
	t.Helper()
	status, err := f.svc.FreezeStatus(adminTestContext(), f.scopeEnv, f.scopePipe)
	if err != nil {
		t.Fatalf("freeze status: %v", err)
	}
	result, err := f.svc.CreateRelease(adminTestContext(), ReleaseCreateInput{
		EnvironmentKey: f.scopeEnv, PipelineKey: f.scopePipe, Version: version,
		ExpectedFreezeRevisionRaw: strconv.FormatInt(status.Revision, 10), RequestID: uuid.NewString(), Reason: "导出版本 " + version,
	})
	if err != nil {
		t.Fatalf("create %s: %v", version, err)
	}
	return result
}

func (f *releaseExportFixture) export(t *testing.T, version, snapshotID string) *ReleaseStaticExportResult {
	t.Helper()
	result, err := f.svc.ReleaseStaticExport(adminTestContext(), ReleaseStaticExportInput{
		EnvironmentKey: f.scopeEnv, PipelineKey: f.scopePipe, Version: version, ExpectedSnapshotID: snapshotID,
	})
	if err != nil {
		t.Fatalf("export %s: %v", version, err)
	}
	t.Cleanup(func() { _ = os.Remove(result.TempPath) })
	return result
}

// readReleaseZip 解包并返回条目内容与解压前后大小。
func readReleaseZip(t *testing.T, path string) map[string][]byte {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer reader.Close()
	entries := map[string][]byte{}
	for _, file := range reader.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", file.Name, err)
		}
		var buffer bytes.Buffer
		if _, err := buffer.ReadFrom(handle); err != nil {
			t.Fatalf("read entry %s: %v", file.Name, err)
		}
		handle.Close()
		if strings.Contains(file.Name, "\\") {
			t.Fatalf("zip entry must use forward slashes: %s", file.Name)
		}
		entries[file.Name] = buffer.Bytes()
	}
	return entries
}

func exportConfigEntry(t *testing.T, entries map[string][]byte, alias string) map[string]any {
	t.Helper()
	var envelope struct {
		Data struct {
			Configs []map[string]any `json:"configs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(entries[releaseStaticConfigName], &envelope); err != nil {
		t.Fatalf("decode config.json: %v", err)
	}
	for _, cfg := range envelope.Data.Configs {
		if cfg["alias"] == alias {
			return cfg
		}
	}
	t.Fatalf("config %s missing in package: %v", alias, entries[releaseStaticConfigName])
	return nil
}

// TestReleaseStaticExportPackageContents H12/H13:包内两个 JSON 与资源逐份可核对,引用改写为包内相对路径。
func TestReleaseStaticExportPackageContents(t *testing.T) {
	fixture := newReleaseExportFixture(t)
	created := fixture.create(t, "v1.0")
	result := fixture.export(t, "v1.0", created.Meta.SnapshotID)

	if wantName := "release-" + created.Meta.SnapshotID + "-v1.0.zip"; result.FileName != wantName {
		t.Fatalf("file name = %s, want %s", result.FileName, wantName)
	}
	entries := readReleaseZip(t, result.TempPath)

	// 快照清单行:条目集合与清单一一对应
	rows, err := fixture.svc.logic.releaseDAO.ListAssets(adminTestContext(), fixture.svc.logic.db, created.Meta.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("fixture expects 2 snapshot assets, got %d", len(rows))
	}
	if len(entries) != 2+len(rows) {
		t.Fatalf("zip entries = %d, want %d", len(entries), 2+len(rows))
	}
	bySource := map[string]model.ConfigReleaseAsset{}
	for _, row := range rows {
		bySource[row.SourceFileID] = row
		entryPath := releaseStaticAssetsDirName + "/" + row.AssetID + releaseAssetBinSuffix
		content, ok := entries[entryPath]
		if !ok {
			t.Fatalf("asset entry %s missing", entryPath)
		}
		if int64(len(content)) != row.Size {
			t.Fatalf("asset %s size = %d, want %d", row.AssetID, len(content), row.Size)
		}
		if digest := payloadDigest(string(content)); digest != row.Sha256 {
			t.Fatalf("asset %s bytes changed: %s", row.AssetID, digest)
		}
	}
	if string(entries[releaseStaticAssetsDirName+"/"+bySource[fixture.fileA].AssetID+releaseAssetBinSuffix]) != "PNG-A-BYTES" {
		t.Fatal("asset A bytes must match the snapshot copy")
	}

	// manifest.json:格式、范围、版本与资源清单
	var manifest releaseStaticManifestFile
	if err := json.Unmarshal(entries[releaseStaticManifestName], &manifest); err != nil {
		t.Fatalf("decode manifest.json: %v", err)
	}
	if manifest.Format != releaseStaticFormatName || manifest.SnapshotID != created.Meta.SnapshotID ||
		manifest.Version != "v1.0" || manifest.EnvironmentKey != fixture.scopeEnv || manifest.PipelineKey != fixture.scopePipe {
		t.Fatalf("manifest metadata mismatch: %+v", manifest)
	}
	if manifest.SourceFreezeRevision != "1" || manifest.ConfigCount != 5 || len(manifest.Assets) != 2 {
		t.Fatalf("manifest counts mismatch: %+v", manifest)
	}
	for i, asset := range manifest.Assets {
		if i > 0 && manifest.Assets[i-1].AssetID > asset.AssetID {
			t.Fatalf("manifest assets must be sorted by asset_id: %+v", manifest.Assets)
		}
		if asset.Path != releaseStaticAssetsDirName+"/"+asset.AssetID+releaseAssetBinSuffix {
			t.Fatalf("manifest asset path = %s", asset.Path)
		}
		if asset.Sha256 == "" || asset.FileName == "" || asset.ContentType == "" || asset.Size == 0 {
			t.Fatalf("manifest asset entry incomplete: %+v", asset)
		}
	}

	// config.json:选择模式、摘要与逐项引用改写
	var envelope struct {
		Data struct {
			SnapshotID           string `json:"snapshot_id"`
			Version              string `json:"version"`
			SourceFreezeRevision string `json:"source_freeze_revision"`
			PayloadSha256        string `json:"payload_sha256"`
			SelectionMode        string `json:"selection_mode"`
			Environment          struct {
				EnvironmentKey  string `json:"environment_key"`
				EnvironmentName string `json:"environment_name"`
				PipelineKey     string `json:"pipeline_key"`
				PipelineName    string `json:"pipeline_name"`
			} `json:"environment"`
		} `json:"data"`
	}
	if err := json.Unmarshal(entries[releaseStaticConfigName], &envelope); err != nil {
		t.Fatalf("decode config.json: %v", err)
	}
	if envelope.Data.SelectionMode != "fixed" || envelope.Data.Version != "v1.0" ||
		envelope.Data.SnapshotID != created.Meta.SnapshotID || envelope.Data.SourceFreezeRevision != "1" {
		t.Fatalf("config.json metadata mismatch: %+v", envelope.Data)
	}
	if envelope.Data.Environment.EnvironmentKey != fixture.scopeEnv || envelope.Data.Environment.PipelineKey != fixture.scopePipe {
		t.Fatalf("config.json environment mismatch: %+v", envelope.Data.Environment)
	}

	assetA := releaseStaticAssetsDirName + "/" + bySource[fixture.fileA].AssetID + releaseAssetBinSuffix
	assetB := releaseStaticAssetsDirName + "/" + bySource[fixture.fileB].AssetID + releaseAssetBinSuffix
	image := exportConfigEntry(t, entries, "IMAGE_A")
	if got, _ := image["content"].(string); got != "."+"/"+assetA {
		t.Fatalf("image content = %v, want ./%s", image["content"], assetA)
	}
	fileB := exportConfigEntry(t, entries, "FILE_B")
	if got, _ := fileB["content"].(string); got != "./"+assetB+"?x=(1)#frag" {
		t.Fatalf("file content must keep query/fragment: %v", fileB["content"])
	}
	object := exportConfigEntry(t, entries, "OBJ")
	objectContent, ok := object["content"].(map[string]any)
	if !ok {
		t.Fatalf("object content must be a JSON object: %v", object["content"])
	}
	if objectContent["nested"].(map[string]any)["doc"] != "./"+assetB {
		t.Fatalf("nested managed reference must be rewritten: %v", objectContent)
	}
	rich := exportConfigEntry(t, entries, "RICH")
	richText, _ := rich["content"].(string)
	for _, want := range []string{
		`<img src="./` + assetA + `">`,
		`<img srcset="./` + assetB + `?v=1#top 1x, ./` + assetA + ` 2x">`,
		`href="https://docs.example.com/guide?x=1#top"`,
	} {
		if !strings.Contains(richText, want) {
			t.Fatalf("richtext missing %q: %s", want, richText)
		}
	}
	text := exportConfigEntry(t, entries, "TEXT")
	if got, _ := text["content"].(string); got != "api=https://api.example.com/v1/endpoint" {
		t.Fatalf("text content must stay literal: %v", text["content"])
	}

	// 精度与"无受管痕迹":原始字节上核对
	rawConfig := entries[releaseStaticConfigName]
	if !bytes.Contains(rawConfig, []byte(releaseBigLiteral)) {
		t.Fatalf("big integer literal must survive in config.json: %s", rawConfig)
	}
	for _, forbidden := range []string{releaseAssetScheme, fileURLPrefix, "/api/v1/release/asset/"} {
		if bytes.Contains(rawConfig, []byte(forbidden)) || bytes.Contains(entries[releaseStaticManifestName], []byte(forbidden)) {
			t.Fatalf("package must not contain managed route %q", forbidden)
		}
	}
}

// TestReleaseStaticExportIndependentOfEditorAndSourceFiles H13:
// 导出不依赖编辑区后续改动,也不依赖原始上传文件。
func TestReleaseStaticExportIndependentOfEditorAndSourceFiles(t *testing.T) {
	fixture := newReleaseExportFixture(t)
	created := fixture.create(t, "v1.0")
	svc := fixture.svc
	ctx := adminTestContext()

	// 编辑区:解锁 → 修改既有项 + 新增未发布项 → 重新锁定
	if _, err := svc.UnlockFreeze(ctx, fixture.scopeEnv, fixture.scopePipe, "导出独立性", "1"); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	rows, err := svc.logic.ListConfigs(ctx, fixture.scopeEnv, fixture.scopePipe, "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range rows {
		if cfg.Alias == "TEXT" {
			updated := buildConfig(fixture.scopeEnv, fixture.scopePipe, "TEXT", "text", "api=https://api.example.com/v2/changed")
			updated.ResourceKey = cfg.ResourceKey
			if _, err := svc.UpdateConfig(ctx, updated, currentEditRevision(t, svc, updated)); err != nil {
				t.Fatalf("update: %v", err)
			}
		}
	}
	if _, err := svc.AddConfig(ctx, buildConfig(fixture.scopeEnv, fixture.scopePipe, "UNPUBLISHED", "text", "should-not-export")); err != nil {
		t.Fatal(err)
	}
	lockFromCurrent(t, svc, fixture.scopeEnv, fixture.scopePipe, "导出独立性锁定")

	// 原始上传文件全部删除:快照副本仍可导出
	for _, path := range []string{fixture.sourceA, fixture.sourceB} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove source: %v", err)
		}
	}

	result := fixture.export(t, "v1.0", created.Meta.SnapshotID)
	entries := readReleaseZip(t, result.TempPath)
	text := exportConfigEntry(t, entries, "TEXT")
	if got, _ := text["content"].(string); got != "api=https://api.example.com/v1/endpoint" {
		t.Fatalf("package must keep the published content: %v", text["content"])
	}
	var envelope struct {
		Data struct {
			Configs []map[string]any `json:"configs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(entries[releaseStaticConfigName], &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Configs) != 5 {
		t.Fatalf("package must contain exactly the snapshot configs, got %d", len(envelope.Data.Configs))
	}
	for _, cfg := range envelope.Data.Configs {
		if cfg["alias"] == "UNPUBLISHED" {
			t.Fatal("unpublished config must not be exported")
		}
	}
	if len(entries[assetEntryName(t, entries)]) == 0 {
		t.Fatal("asset entries must be present")
	}
}

// assetEntryName 返回第一个资源条目的名字。
func assetEntryName(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	for name := range entries {
		if strings.HasPrefix(name, releaseStaticAssetsDirName+"/") {
			return name
		}
	}
	t.Fatal("no asset entry in package")
	return ""
}

// TestReleaseStaticExportValidationAndMismatch H14:参数校验、范围/版本缺失与标识不一致。
func TestReleaseStaticExportValidationAndMismatch(t *testing.T) {
	fixture := newReleaseExportFixture(t)
	created := fixture.create(t, "v1.0")
	svc := fixture.svc
	ctx := adminTestContext()
	base := ReleaseStaticExportInput{
		EnvironmentKey: fixture.scopeEnv, PipelineKey: fixture.scopePipe,
		Version: "v1.0", ExpectedSnapshotID: created.Meta.SnapshotID,
	}

	cases := []struct {
		name   string
		mutate func(input ReleaseStaticExportInput) ReleaseStaticExportInput
		expect string
	}{
		{"missing_env", func(in ReleaseStaticExportInput) ReleaseStaticExportInput { in.EnvironmentKey = ""; return in }, ErrTypeReleaseInvalidRequest},
		{"missing_version", func(in ReleaseStaticExportInput) ReleaseStaticExportInput { in.Version = ""; return in }, ErrTypeReleaseInvalidRequest},
		{"invalid_snapshot", func(in ReleaseStaticExportInput) ReleaseStaticExportInput {
			in.ExpectedSnapshotID = "not-a-uuid"
			return in
		}, ErrTypeReleaseInvalidRequest},
		{"unknown_scope", func(in ReleaseStaticExportInput) ReleaseStaticExportInput { in.PipelineKey = "beta"; return in }, ErrTypeReleaseScopeNotFound},
		{"unknown_version", func(in ReleaseStaticExportInput) ReleaseStaticExportInput { in.Version = "v9.9"; return in }, ErrTypeReleaseVersionNotFound},
		{"snapshot_mismatch", func(in ReleaseStaticExportInput) ReleaseStaticExportInput {
			in.ExpectedSnapshotID = uuid.NewString()
			return in
		}, ErrTypeReleaseSnapshotMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := svc.ReleaseStaticExport(ctx, tc.mutate(base))
			if result != nil {
				_ = os.Remove(result.TempPath)
				t.Fatalf("failed export must not return a file")
			}
			requireReleaseErrorType(t, err, tc.expect)
		})
	}

	// 跨范围:另一范围的同版本号不可用本范围的 snapshot 导出
	seedFreezeScope(t, svc, "qa_dev", "main")
	cross := base
	cross.EnvironmentKey = "qa_dev"
	if _, err := svc.ReleaseStaticExport(ctx, cross); err == nil {
		t.Fatal("cross-scope export must fail")
	} else {
		requireReleaseErrorType(t, err, ErrTypeReleaseVersionNotFound)
	}
}

// TestReleaseStaticExportIntegrityFailures H15:缺文件、同长度篡改、payload 篡改都返回 503 且不留临时包。
func TestReleaseStaticExportIntegrityFailures(t *testing.T) {
	fixture := newReleaseExportFixture(t)
	created := fixture.create(t, "v1.0")
	svc := fixture.svc
	ctx := adminTestContext()
	rows, err := svc.logic.releaseDAO.ListAssets(ctx, svc.logic.db, created.Meta.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	storage, err := svc.releaseStorage()
	if err != nil {
		t.Fatal(err)
	}
	target := storage.assetPath(created.Meta.SnapshotID, rows[0].AssetID)
	original := readFileBytes(t, target)
	t.Cleanup(func() { _ = os.WriteFile(target, original, 0o644) })

	before := countReleaseExportTempFiles(t)

	// 同长度篡改:字节不同、长度相同
	tampered := append([]byte{}, original...)
	tampered[0] ^= 0xFF
	if err := os.WriteFile(target, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReleaseStaticExport(ctx, ReleaseStaticExportInput{
		EnvironmentKey: fixture.scopeEnv, PipelineKey: fixture.scopePipe, Version: "v1.0", ExpectedSnapshotID: created.Meta.SnapshotID,
	}); err == nil {
		t.Fatal("tampered asset must fail the export")
	} else {
		requireReleaseErrorType(t, err, ErrTypeReleaseIncomplete)
	}
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}

	// 缺文件
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReleaseStaticExport(ctx, ReleaseStaticExportInput{
		EnvironmentKey: fixture.scopeEnv, PipelineKey: fixture.scopePipe, Version: "v1.0", ExpectedSnapshotID: created.Meta.SnapshotID,
	}); err == nil {
		t.Fatal("missing asset must fail the export")
	} else {
		requireReleaseErrorType(t, err, ErrTypeReleaseIncomplete)
	}
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}

	// payload 篡改
	row, err := svc.logic.releaseDAO.GetVersionByScopeAndName(ctx, svc.logic.db, fixture.scopeEnv, fixture.scopePipe, "v1.0")
	if err != nil {
		t.Fatal(err)
	}
	tamperedPayload := strings.Replace(row.Payload, "api.example.com/v1/endpoint", "api.example.com/v1/evil-url", 1)
	if tamperedPayload == row.Payload {
		t.Fatal("payload tamper fixture did not change content")
	}
	if err := svc.logic.db.Model(&model.ConfigReleaseVersion{}).
		Where("snapshot_id = ?", row.SnapshotID).Update("payload", tamperedPayload).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = svc.logic.db.Model(&model.ConfigReleaseVersion{}).
			Where("snapshot_id = ?", row.SnapshotID).Update("payload", row.Payload).Error
	})
	if _, err := svc.ReleaseStaticExport(ctx, ReleaseStaticExportInput{
		EnvironmentKey: fixture.scopeEnv, PipelineKey: fixture.scopePipe, Version: "v1.0", ExpectedSnapshotID: created.Meta.SnapshotID,
	}); err == nil {
		t.Fatal("tampered payload must fail the export")
	} else {
		requireReleaseErrorType(t, err, ErrTypeReleaseIncomplete)
	}

	if after := countReleaseExportTempFiles(t); after != before {
		t.Fatalf("failed exports must clean temp files: before=%d after=%d", before, after)
	}
}

// countReleaseExportTempFiles 统计系统临时目录中本功能的临时包。
func countReleaseExportTempFiles(t *testing.T) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), releaseExportTempPrefix+"*"))
	if err != nil {
		t.Fatal(err)
	}
	return len(matches)
}

// TestReleaseStaticExportLimitsAndTimeout H19:恰好等于上限允许,超 1 字节 413;超时 503。
func TestReleaseStaticExportLimitsAndTimeout(t *testing.T) {
	fixture := newReleaseExportFixture(t)
	created := fixture.create(t, "v1.0")
	svc := fixture.svc
	ctx := adminTestContext()
	input := ReleaseStaticExportInput{
		EnvironmentKey: fixture.scopeEnv, PipelineKey: fixture.scopePipe,
		Version: "v1.0", ExpectedSnapshotID: created.Meta.SnapshotID,
	}

	originalLimit := releaseExportMaxOutputByte
	t.Cleanup(func() { releaseExportMaxOutputByte = originalLimit })

	baseline := fixture.export(t, "v1.0", created.Meta.SnapshotID)
	reader, err := zip.OpenReader(baseline.TempPath)
	if err != nil {
		t.Fatal(err)
	}
	var uncompressed int64
	for _, file := range reader.File {
		uncompressed += int64(file.UncompressedSize64)
	}
	reader.Close()

	releaseExportMaxOutputByte = uncompressed
	if _, err := svc.ReleaseStaticExport(ctx, input); err != nil {
		t.Fatalf("exact limit must be allowed: %v", err)
	} else if _, ok := AsReleaseError(err); ok {
		t.Fatal("exact limit must not fail")
	}

	releaseExportMaxOutputByte = uncompressed - 1
	result, err := svc.ReleaseStaticExport(ctx, input)
	if result != nil {
		_ = os.Remove(result.TempPath)
		t.Fatal("over-limit export must not return a file")
	}
	releaseErr := requireReleaseErrorType(t, err, ErrTypeReleaseLimitExceeded)
	if releaseErr.Limits == nil || releaseErr.Limits.Item != "export_uncompressed_bytes" {
		t.Fatalf("limit info must name the export budget: %+v", releaseErr.Limits)
	}

	releaseExportMaxOutputByte = originalLimit
}

// TestReleaseStaticExportTimeout 构包超时在返回前明确 503。
func TestReleaseStaticExportTimeout(t *testing.T) {
	fixture := newReleaseExportFixture(t)
	created := fixture.create(t, "v1.0")
	original := releaseExportTotalTimeout
	releaseExportTotalTimeout = time.Nanosecond
	t.Cleanup(func() { releaseExportTotalTimeout = original })

	result, err := fixture.svc.ReleaseStaticExport(adminTestContext(), ReleaseStaticExportInput{
		EnvironmentKey: fixture.scopeEnv, PipelineKey: fixture.scopePipe,
		Version: "v1.0", ExpectedSnapshotID: created.Meta.SnapshotID,
	})
	if result != nil {
		_ = os.Remove(result.TempPath)
		t.Fatal("timed-out export must not return a file")
	}
	requireReleaseErrorType(t, err, ErrTypeReleaseExportTimeout)
}

// TestReleaseStaticExportDuringActiveSwitch H16:构包途中切换生效版本,整包仍是被固定下来的快照。
func TestReleaseStaticExportDuringActiveSwitch(t *testing.T) {
	fixture := newReleaseExportFixture(t)
	first := fixture.create(t, "v1.0")
	if _, err := fixture.svc.ActivateRelease(adminTestContext(), ReleaseActivateInput{
		EnvironmentKey: fixture.scopeEnv, PipelineKey: fixture.scopePipe, Version: "v1.0",
		ExpectedActiveRevisionRaw: "0", RequestID: uuid.NewString(), Reason: "导出基线",
	}); err != nil {
		t.Fatalf("activate v1.0: %v", err)
	}
	// 第二个版本内容不同,用于区分
	if _, err := fixture.svc.UnlockFreeze(adminTestContext(), fixture.scopeEnv, fixture.scopePipe, "第二版", "1"); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	rows, err := fixture.svc.logic.ListConfigs(adminTestContext(), fixture.scopeEnv, fixture.scopePipe, "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range rows {
		if cfg.Alias == "TEXT" {
			updated := buildConfig(fixture.scopeEnv, fixture.scopePipe, "TEXT", "text", "api=https://api.example.com/v2/second")
			updated.ResourceKey = cfg.ResourceKey
			if _, err := fixture.svc.UpdateConfig(adminTestContext(), updated, currentEditRevision(t, fixture.svc, updated)); err != nil {
				t.Fatalf("update: %v", err)
			}
		}
	}
	lockFromCurrent(t, fixture.svc, fixture.scopeEnv, fixture.scopePipe, "第二版锁定")
	second := fixture.create(t, "v1.1")

	paused := make(chan struct{}, 1)
	resume := make(chan struct{})
	releaseExportPauseHookForTest = func(string) {
		select {
		case paused <- struct{}{}:
		default:
		}
		<-resume
	}
	t.Cleanup(func() { releaseExportPauseHookForTest = nil })

	type exportOutcome struct {
		result *ReleaseStaticExportResult
		err    error
	}
	done := make(chan exportOutcome, 1)
	go func() {
		result, err := fixture.svc.ReleaseStaticExport(adminTestContext(), ReleaseStaticExportInput{
			EnvironmentKey: fixture.scopeEnv, PipelineKey: fixture.scopePipe,
			Version: "v1.0", ExpectedSnapshotID: first.Meta.SnapshotID,
		})
		done <- exportOutcome{result, err}
	}()
	select {
	case <-paused:
	case outcome := <-done:
		if outcome.result != nil {
			_ = os.Remove(outcome.result.TempPath)
		}
		t.Fatalf("export finished before pause: %v", outcome.err)
	case <-time.After(20 * time.Second):
		t.Fatal("export pause hook never fired")
	}

	// 构包途中把生效版本切到 v1.1
	if _, err := fixture.svc.ActivateRelease(adminTestContext(), ReleaseActivateInput{
		EnvironmentKey: fixture.scopeEnv, PipelineKey: fixture.scopePipe, Version: "v1.1",
		ExpectedActiveRevisionRaw: "1", RequestID: uuid.NewString(), Reason: "构包中切换",
	}); err != nil {
		t.Fatalf("switch during export: %v", err)
	}
	close(resume)

	outcome := <-done
	if outcome.err != nil {
		t.Fatalf("export during switch failed: %v", outcome.err)
	}
	t.Cleanup(func() { _ = os.Remove(outcome.result.TempPath) })
	if outcome.result.Version != "v1.0" || outcome.result.SnapshotID != first.Meta.SnapshotID {
		t.Fatalf("package must stay on the captured snapshot: %+v", outcome.result)
	}
	entries := readReleaseZip(t, outcome.result.TempPath)
	text := exportConfigEntry(t, entries, "TEXT")
	if got, _ := text["content"].(string); got != "api=https://api.example.com/v1/endpoint" {
		t.Fatalf("package content must come from v1.0: %v", text["content"])
	}
	// 当前生效仍是 v1.1(导出不改状态)
	active, err := fixture.svc.ReleaseActive(adminTestContext(), fixture.scopeEnv, fixture.scopePipe)
	if err != nil {
		t.Fatal(err)
	}
	if active == nil || active.Version != "v1.1" {
		t.Fatalf("export must not change active: %+v", active)
	}
	if second.Meta.SnapshotID == first.Meta.SnapshotID {
		t.Fatal("fixture error: two versions share a snapshot id")
	}
}
