package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

const releaseBigLiteral = "9007199254740993"

// newReleaseFixture 建立"已锁定范围 + 图片/大整数/外链文本配置"的基线。
type releaseFixture struct {
	svc       *Service
	token     string
	fileA     string
	fileAPath string
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	ctx := adminTestContext()

	fileA := "release-fixture-asset-a"
	writeUploadedFile(t, fileA, "pic-a.png", []byte("PNG-A-BYTES"))
	if err := svc.logic.CreateAsset(ctx, &model.Asset{
		FileID: fileA, EnvironmentKey: "qa_prod", PipelineKey: "main",
		FileName: "pic-a.png", ContentType: "image/png", FileSize: int64(len("PNG-A-BYTES")),
		Path: filepath.Join(uploadDirectory, fileA, "pic-a.png"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "REL_IMAGE", "image", "asset://"+fileA)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "REL_BIG", "object", `{"big":`+releaseBigLiteral+`}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "REL_TEXT", "text", "api=https://api.example.com/v1/endpoint")); err != nil {
		t.Fatal(err)
	}
	mustLock(t, svc, "qa_prod", "main", "发布基线锁定")
	return &releaseFixture{svc: svc, token: testAdminToken(), fileA: fileA,
		fileAPath: filepath.Join(dataDirectory, uploadDirectory, fileA, "pic-a.png")}
}

func (f *releaseFixture) create(t *testing.T, version, expectedRev string, requestID string) (*ReleaseCreateResult, error) {
	t.Helper()
	if requestID == "" {
		requestID = uuid.NewString()
	}
	return f.svc.CreateRelease(adminTestContext(), ReleaseCreateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: version,
		ExpectedFreezeRevisionRaw: expectedRev, RequestID: requestID, Reason: "发布 " + version,
	})
}

func (f *releaseFixture) activate(t *testing.T, version, expectedRev string, requestID string) (*ReleaseActivateResult, error) {
	t.Helper()
	if requestID == "" {
		requestID = uuid.NewString()
	}
	return f.svc.ActivateRelease(adminTestContext(), ReleaseActivateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: version,
		ExpectedActiveRevisionRaw: expectedRev, RequestID: requestID, Reason: "切换 " + version,
	})
}

// --------------------- 创建与冻结契约 ---------------------

func TestReleaseCreateRequiresFrozenScopeAndRevision(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	ctx := adminTestContext()

	_, err := svc.CreateRelease(ctx, ReleaseCreateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
		ExpectedFreezeRevisionRaw: "0", RequestID: uuid.NewString(), Reason: "未锁版",
	})
	requireReleaseErrorType(t, err, ErrTypeReleaseSourceNotFrozen)

	mustLock(t, svc, "qa_prod", "main", "锁定")
	_, err = svc.CreateRelease(ctx, ReleaseCreateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
		ExpectedFreezeRevisionRaw: "0", RequestID: uuid.NewString(), Reason: "错误修订",
	})
	requireReleaseErrorType(t, err, ErrTypeReleaseSourceRevision)

	// 缺少可信身份(未登录):401 AUTH_REQUIRED,旧 Bearer 不再授予管理权限
	_, err = svc.CreateRelease(context.Background(), ReleaseCreateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
		ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString(), Reason: "未登录",
	})
	requireAuthErrorType(t, err, ErrTypeAuthRequired)
	_, err = svc.CreateRelease(context.Background(), ReleaseCreateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
		ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString(), Reason: "旧密钥",
	})
	requireAuthErrorType(t, err, ErrTypeAuthRequired)
	// 无副作用:无版本、无快照目录、冻结 revision 不变
	versions, _, err := svc.logic.releaseDAO.ListVersions(ctx, svc.logic.db, "qa_prod", "main", 10, 0)
	if err != nil || len(versions) != 0 {
		t.Fatalf("no version may be created: %v %d", err, len(versions))
	}
	state, err := svc.logic.freezeDAO.GetState(ctx, svc.logic.db, "qa_prod", "main")
	if err != nil || state.State != model.FreezeStateLocked || state.Revision != 1 {
		t.Fatalf("freeze state must stay locked rev=1: %v %+v", err, state)
	}
}

// --------------------- 快照内容、精度与独立性 ---------------------

func TestReleaseSnapshotPayloadAndFixedRuntime(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()

	result, err := fixture.create(t, "v1.0", "1", "")
	if err != nil {
		t.Fatalf("create v1.0: %v", err)
	}
	if result.Replayed || result.Meta.ConfigCount != 3 || result.Meta.AssetCount != 1 {
		t.Fatalf("unexpected meta: %+v", result.Meta)
	}
	if !containsString(result.Meta.ExternalReferences, "https://api.example.com/v1/endpoint") {
		t.Fatalf("external reference missing: %+v", result.Meta.ExternalReferences)
	}
	if result.Meta.SourceFreezeRevision != 1 {
		t.Fatalf("source freeze revision: %d", result.Meta.SourceFreezeRevision)
	}

	row, err := svc.logic.releaseDAO.GetVersionBySnapshotID(ctx, svc.logic.db, result.Meta.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	// payload:必须保留大整数原字面量,且不残留任何受管路由
	if !strings.Contains(row.Payload, releaseBigLiteral) {
		t.Fatalf("payload must keep the big integer literal: %s", row.Payload)
	}
	if strings.Contains(row.Payload, "/api/v1/asset/file/") || containsBareAssetScheme(row.Payload) {
		t.Fatalf("payload must not contain managed routes: %s", row.Payload)
	}
	if payloadDigest(row.Payload) != row.PayloadSha256 {
		t.Fatal("payload digest mismatch")
	}
	assets, err := svc.logic.releaseDAO.ListAssets(ctx, svc.logic.db, result.Meta.SnapshotID)
	if err != nil || len(assets) != 1 {
		t.Fatalf("asset rows: %v %d", err, len(assets))
	}
	if buildReleaseManifest(assets) != row.ManifestSha256 {
		t.Fatal("manifest digest mismatch")
	}

	// 固定版本读取:大整数以原始文本保留,图片 URL 单前缀
	runtimeData, err := svc.RuntimeRelease(ctx, "qa_prod", "main", strPtr("v1.0"))
	if err != nil {
		t.Fatalf("fixed runtime: %v", err)
	}
	if runtimeData.SelectionMode != model.ReleaseSelectionFixed || runtimeData.ActiveRevision != nil || runtimeData.ActivatedAt != "" {
		t.Fatalf("fixed mode must not carry active revision: %+v", runtimeData)
	}
	bigConfig := findRuntimeConfig(t, runtimeData, "REL_BIG")
	if !strings.Contains(bigConfig.Content, releaseBigLiteral) {
		t.Fatalf("runtime content must keep the literal: %s", bigConfig.Content)
	}
	imageConfig := findRuntimeConfig(t, runtimeData, "REL_IMAGE")
	expectedURL := fmt.Sprintf("/rainbow-bridge/api/v1/release/asset/%s/%s", result.Meta.SnapshotID, assets[0].AssetID)
	if imageConfig.Content != expectedURL {
		t.Fatalf("rendered asset URL mismatch: %q != %q", imageConfig.Content, expectedURL)
	}
	if strings.Count(imageConfig.Content, "/rainbow-bridge") != 1 {
		t.Fatalf("url must carry the base path exactly once: %s", imageConfig.Content)
	}
	contentType, bytesOut, err := svc.ReleaseAssetDownload(ctx, result.Meta.SnapshotID, assets[0].AssetID)
	if err != nil || contentType != "image/png" || string(bytesOut) != "PNG-A-BYTES" {
		t.Fatalf("asset download mismatch: %v %s %q", err, contentType, string(bytesOut))
	}

	// 未设置 active 时默认读取 404,不回退
	_, err = svc.RuntimeRelease(ctx, "qa_prod", "main", nil)
	requireReleaseErrorType(t, err, ErrTypeReleaseNotActive)
	// 不存在的版本 404
	_, err = svc.RuntimeRelease(ctx, "qa_prod", "main", strPtr("v9.9"))
	requireReleaseErrorType(t, err, ErrTypeReleaseVersionNotFound)
	// 显式空 version 400
	_, err = svc.RuntimeRelease(ctx, "qa_prod", "main", strPtr("  "))
	requireReleaseErrorType(t, err, ErrTypeReleaseInvalidRequest)
}

func TestReleaseIndependentFromEditorAndSourceFiles(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()

	result, err := fixture.create(t, "v1.0", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	// 解锁并修改编辑区
	if _, err := svc.UnlockFreeze(ctx, "qa_prod", "main", "验收后调整", "1"); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.logic.ListConfigs(ctx, "qa_prod", "main", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range rows {
		if cfg.Alias != "REL_TEXT" {
			continue
		}
		updated := buildConfig("qa_prod", "main", "REL_TEXT", "text", "changed-in-editor")
		updated.ResourceKey = cfg.ResourceKey
		if _, err := svc.UpdateConfig(ctx, updated, currentEditRevision(t, svc, updated)); err != nil {
			t.Fatal(err)
		}
	}
	// 移走源文件:旧版本读取不依赖源资产行与源文件
	if err := os.Rename(fixture.fileAPath, fixture.fileAPath+".moved"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(fixture.fileAPath+".moved", fixture.fileAPath)

	runtimeData, err := svc.RuntimeRelease(ctx, "qa_prod", "main", strPtr("v1.0"))
	if err != nil {
		t.Fatalf("fixed runtime after editor change: %v", err)
	}
	textConfig := findRuntimeConfig(t, runtimeData, "REL_TEXT")
	if textConfig.Content != "api=https://api.example.com/v1/endpoint" {
		t.Fatalf("old version must keep its content: %q", textConfig.Content)
	}
	assets, err := svc.logic.releaseDAO.ListAssets(ctx, svc.logic.db, result.Meta.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if _, bytesOut, err := svc.ReleaseAssetDownload(ctx, result.Meta.SnapshotID, assets[0].AssetID); err != nil || string(bytesOut) != "PNG-A-BYTES" {
		t.Fatalf("snapshot asset must stay independent: %v", err)
	}
}

// --------------------- 幂等与冲突 ---------------------

func TestReleaseCreateIdempotencyAndConflicts(t *testing.T) {
	fixture := newReleaseFixture(t)
	ctx := adminTestContext()
	requestID := uuid.NewString()

	first, err := fixture.create(t, "v1.0", "1", requestID)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := fixture.create(t, "v1.0", "1", requestID)
	if err != nil {
		t.Fatalf("replay must succeed: %v", err)
	}
	if !replay.Replayed || replay.Meta.SnapshotID != first.Meta.SnapshotID {
		t.Fatalf("replay mismatch: %+v", replay)
	}
	// 同 request_id 不同字段 → 409 REQUEST_CONFLICT
	_, err = fixture.svc.CreateRelease(ctx, ReleaseCreateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
		ExpectedFreezeRevisionRaw: "1", RequestID: requestID, Reason: "不同原因",
	})
	requireReleaseErrorType(t, err, ErrTypeReleaseRequestConflict)
	// 同版本不同 request_id → 409 VERSION_EXISTS
	_, err = fixture.create(t, "v1.0", "1", "")
	requireReleaseErrorType(t, err, ErrTypeReleaseVersionExists)
	// 事件不重复
	events, _, err := fixture.svc.logic.releaseDAO.ListEvents(ctx, fixture.svc.logic.db, "qa_prod", "main", 10, 0)
	if err != nil || len(events) != 1 {
		t.Fatalf("exactly one create event expected: %v %d", err, len(events))
	}
}

// --------------------- 空范围与无资源版本 ---------------------

func TestReleaseEmptyAndAssetlessVersions(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_empty", "main")
	ctx := adminTestContext()

	// 空渠道:configs=[]、资源数 0,可创建
	mustLock(t, svc, "qa_empty", "main", "空范围锁定")
	result, err := svc.CreateRelease(ctx, ReleaseCreateInput{
		EnvironmentKey: "qa_empty", PipelineKey: "main", Version: "v1.0",
		ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString(), Reason: "空范围",
	})
	if err != nil {
		t.Fatalf("empty scope release: %v", err)
	}
	if result.Meta.ConfigCount != 0 || result.Meta.AssetCount != 0 {
		t.Fatalf("empty counts expected: %+v", result.Meta)
	}
	runtimeData, err := svc.RuntimeRelease(ctx, "qa_empty", "main", strPtr("v1.0"))
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimeData.Configs) != 0 {
		t.Fatalf("empty configs expected: %d", len(runtimeData.Configs))
	}

	// 纯文本(无资源)版本同样可创建与读取
	seedFreezeScope(t, svc, "qa_textonly", "main")
	if _, err := svc.AddConfig(ctx, buildConfig("qa_textonly", "main", "ONLY_TEXT", "text", "hello")); err != nil {
		t.Fatal(err)
	}
	mustLock(t, svc, "qa_textonly", "main", "文本锁定")
	if _, err := svc.CreateRelease(ctx, ReleaseCreateInput{
		EnvironmentKey: "qa_textonly", PipelineKey: "main", Version: "v1.0",
		ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString(), Reason: "纯文本",
	}); err != nil {
		t.Fatalf("assetless release: %v", err)
	}
}

// --------------------- 切换、CAS 与审计回滚 ---------------------

func TestReleaseActivateLifecycleCASAndAudit(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()

	if _, err := fixture.create(t, "v1.0", "1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UnlockFreeze(ctx, "qa_prod", "main", "二轮", "1"); err != nil {
		t.Fatal(err)
	}
	rows, _ := svc.logic.ListConfigs(ctx, "qa_prod", "main", "", "", "", false)
	for _, cfg := range rows {
		if cfg.Alias == "REL_TEXT" {
			updated := buildConfig("qa_prod", "main", "REL_TEXT", "text", "v1.1-content")
			updated.ResourceKey = cfg.ResourceKey
			if _, err := svc.UpdateConfig(ctx, updated, currentEditRevision(t, svc, updated)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := svc.LockFreeze(ctx, "qa_prod", "main", "二轮验收", "2"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.create(t, "v1.1", "3", ""); err != nil {
		t.Fatal(err)
	}

	activateRequest := uuid.NewString()
	activated, err := fixture.activate(t, "v1.1", "0", activateRequest)
	if err != nil {
		t.Fatalf("activate v1.1: %v", err)
	}
	if activated.AppliedRevision != 1 || activated.Active.Version != "v1.1" {
		t.Fatalf("unexpected activation: %+v", activated)
	}
	// 目标即当前 → 409 ACTIVE_UNCHANGED
	_, err = fixture.activate(t, "v1.1", "1", "")
	requireReleaseErrorType(t, err, ErrTypeReleaseActiveUnchanged)
	// 过期 revision → 409 + 最新状态
	conflict := func() *ReleaseError {
		_, err := fixture.activate(t, "v1.0", "0", "")
		return requireReleaseErrorType(t, err, ErrTypeReleaseActiveRevision)
	}()
	if conflict.LatestActive == nil || conflict.LatestActive.Version != "v1.1" || conflict.LatestActive.Revision != 1 {
		t.Fatalf("conflict must carry latest state: %+v", conflict.LatestActive)
	}
	// 切回 v1.0
	back, err := fixture.activate(t, "v1.0", "1", "")
	if err != nil || back.AppliedRevision != 2 || back.Active.Version != "v1.0" {
		t.Fatalf("switch back failed: %v %+v", err, back)
	}
	// 激活幂等重放:返回原 applied_revision 与最新 active
	replay, err := fixture.activate(t, "v1.1", "0", activateRequest)
	if err != nil {
		t.Fatalf("activation replay: %v", err)
	}
	if !replay.Replayed || replay.AppliedRevision != 1 || replay.Active.Version != "v1.0" {
		t.Fatalf("replay must not re-apply: %+v", replay)
	}
	// 审计失败回滚:head 保持
	headBefore, _ := svc.logic.releaseDAO.GetHead(ctx, svc.logic.db, "qa_prod", "main")
	if err := svc.logic.db.Exec("DROP TABLE config_release_event").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.activate(t, "v1.1", "2", ""); err == nil {
		t.Fatal("activation must fail when audit write fails")
	}
	headAfter, _ := svc.logic.releaseDAO.GetHead(ctx, svc.logic.db, "qa_prod", "main")
	if headAfter.Revision != headBefore.Revision || headAfter.ActiveSnapshotID != headBefore.ActiveSnapshotID {
		t.Fatalf("head must roll back: before=%+v after=%+v", headBefore, headAfter)
	}
}

// --------------------- 引用重写与拒绝清单 ---------------------

func TestReleaseReferenceRewriteAndRejections(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	seedFreezeScope(t, svc, "qa_dev", "main")
	ctx := adminTestContext()

	fileOK := "rel-ref-ok"
	writeUploadedFile(t, fileOK, "ok.png", []byte("OK-BYTES"))
	for _, asset := range []*model.Asset{
		{FileID: fileOK, EnvironmentKey: "qa_prod", PipelineKey: "main", FileName: "ok.png", ContentType: "image/png", FileSize: int64(len("OK-BYTES")), Path: filepath.Join(uploadDirectory, fileOK, "ok.png")},
		{FileID: "rel-ref-cross", EnvironmentKey: "qa_dev", PipelineKey: "main", FileName: "cross.png", ContentType: "image/png", FileSize: 1, Path: filepath.Join(uploadDirectory, "rel-ref-cross", "cross.png")},
		{FileID: "rel-ref-ghost", EnvironmentKey: "qa_prod", PipelineKey: "main", FileName: "ghost.png", ContentType: "image/png", FileSize: 1, Path: filepath.Join(uploadDirectory, "rel-ref-ghost", "ghost.png")},
	} {
		if err := svc.logic.CreateAsset(ctx, asset); err != nil {
			t.Fatal(err)
		}
	}
	create := func(alias, typ, content string) {
		if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", alias, typ, content)); err != nil {
			t.Fatal(err)
		}
	}
	// 合法:完整 URL(带 BasePath/查询/fragment)、object 嵌套、richtext 属性引用
	create("OK_FULL", "image", "/api/v1/asset/file/"+fileOK+"/ok.png?v=1#frag")
	create("OK_NESTED", "object", `{"pic":"asset://`+fileOK+`","n":1}`)
	create("OK_RICH", "richtext", `<p>文字</p><img src="/rainbow-bridge/api/v1/asset/file/`+fileOK+`/ok.png"/>`)
	// 拒绝项
	create("BAD_EMBEDDED", "text", "前缀/api/v1/asset/file/"+fileOK+" 后缀")
	create("BAD_MARKER", "text", "release-asset://user-forged")
	create("BAD_EXTERNAL_IMAGE", "image", "https://cdn.example.com/a.png")
	create("BAD_CROSS", "file", "asset://rel-ref-cross")
	create("BAD_MISSING_META", "image", "asset://rel-ref-no-such")
	create("BAD_MISSING_FILE", "file", "asset://rel-ref-ghost")

	// 坏配置无法通过冻结预检;直接调用捕获引擎验证拒绝清单
	scopeRow, err := svc.logic.environmentDAO.GetByKey(ctx, svc.logic.db, "qa_prod")
	if err != nil {
		t.Fatal(err)
	}
	pipelineRow, err := svc.logic.pipelineDAO.GetByKey(ctx, svc.logic.db, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.captureReleasePlan(ctx, svc.logic, "qa_prod", "main", scopeRow, pipelineRow)
	releaseErr := requireReleaseErrorType(t, err, ErrTypeReleaseResourceInvalid)
	reasons := map[string]int{}
	for _, issue := range releaseErr.Issues {
		reasons[issue.Reason]++
	}
	for _, expected := range []string{"unresolvable_reference", "internal_marker", "external_reference", "cross_scope_reference", "missing_metadata", "file_missing"} {
		if reasons[expected] == 0 {
			t.Fatalf("missing issue %s in %+v", expected, releaseErr.Issues)
		}
	}
	// 无完整版本残留
	versions, _, err := svc.logic.releaseDAO.ListVersions(ctx, svc.logic.db, "qa_prod", "main", 10, 0)
	if err != nil || len(versions) != 0 {
		t.Fatalf("rejected create must not leave a version: %v %d", err, len(versions))
	}

	// 修复被拒配置后可以成功,且查询参数与 fragment 保留
	rows, _ := svc.logic.ListConfigs(ctx, "qa_prod", "main", "", "", "", false)
	for _, cfg := range rows {
		switch cfg.Alias {
		case "BAD_EMBEDDED", "BAD_MARKER", "BAD_EXTERNAL_IMAGE", "BAD_CROSS", "BAD_MISSING_META", "BAD_MISSING_FILE":
			if err := svc.DeleteConfig(ctx, "qa_prod", "main", cfg.ResourceKey); err != nil {
				t.Fatal(err)
			}
		}
	}
	mustLock(t, svc, "qa_prod", "main", "引用预检修复")
	result, err := svc.CreateRelease(ctx, ReleaseCreateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
		ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString(), Reason: "预检修复",
	})
	if err != nil {
		t.Fatalf("create after fixing: %v", err)
	}
	runtimeData, err := svc.RuntimeRelease(ctx, "qa_prod", "main", strPtr("v1.0"))
	if err != nil {
		t.Fatal(err)
	}
	full := findRuntimeConfig(t, runtimeData, "OK_FULL")
	if !strings.HasSuffix(full.Content, "?v=1#frag") || strings.Count(full.Content, "/rainbow-bridge") != 1 {
		t.Fatalf("query/fragment must survive rewrite: %s", full.Content)
	}
	nested := findRuntimeConfig(t, runtimeData, "OK_NESTED")
	if !strings.Contains(nested.Content, "/api/v1/release/asset/"+result.Meta.SnapshotID) || !strings.Contains(nested.Content, `"n":1`) {
		t.Fatalf("nested rewrite mismatch: %s", nested.Content)
	}
	rich := findRuntimeConfig(t, runtimeData, "OK_RICH")
	if !strings.Contains(rich.Content, `src="/rainbow-bridge/api/v1/release/asset/`) {
		t.Fatalf("richtext attribute rewrite mismatch: %s", rich.Content)
	}
}

// --------------------- 限额与存储 ---------------------

func TestReleaseLimitsAndStorageRules(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()

	// 单份资源超限:直接以极小上限调用复制(真实字节计数)
	storage, err := svc.releaseStorage()
	if err != nil {
		t.Fatal(err)
	}
	_, err = storage.copyAssetToStaging("limit-snap", "asset", fixture.fileAPath, "pic-a.png", "image/png", 4)
	requireReleaseErrorType(t, err, ErrTypeReleaseLimitExceeded)
	// 超限文件不得残留(目录由服务层在失败回滚时清理)
	if _, statErr := os.Stat(storage.stagingAssetPath("limit-snap", "asset")); !os.IsNotExist(statErr) {
		t.Fatalf("staging file must be removed after limit failure: %v", statErr)
	}

	// 解锁后追加超大资源配置,再重新锁定(编辑区写入需未冻结)
	if _, err := svc.UnlockFreeze(ctx, "qa_prod", "main", "追加限额样本", "1"); err != nil {
		t.Fatal(err)
	}
	// 声明总量超限(4 x 30MiB 稀疏文件 > 100MiB)在复制前预查
	bigIDs := []string{"rel-big-1", "rel-big-2", "rel-big-3", "rel-big-4"}
	for _, id := range bigIDs {
		path := filepath.Join(dataDirectory, uploadDirectory, id, "big.bin")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(30 << 20); err != nil {
			t.Fatal(err)
		}
		file.Close()
		if err := svc.logic.CreateAsset(ctx, &model.Asset{
			FileID: id, EnvironmentKey: "qa_prod", PipelineKey: "main",
			FileName: "big.bin", ContentType: "application/octet-stream", FileSize: 30 << 20,
			Path: filepath.Join(uploadDirectory, id, "big.bin"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for index, id := range bigIDs {
		if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", fmt.Sprintf("REL_BIGFILE_%d", index), "file", "asset://"+id)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.LockFreeze(ctx, "qa_prod", "main", "限额样本锁定", "2"); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.create(t, "v1.0", "3", "")
	requireReleaseErrorType(t, err, ErrTypeReleaseLimitExceeded)

	// 相对 RAINBOW_RELEASE_DIR 拒绝
	t.Setenv(releaseDirEnv, "relative/path")
	_, err = svc.releaseStorage()
	requireReleaseErrorType(t, err, ErrTypeReleaseStorageError)
}

func TestReleaseStorageRootCustomAbsolute(t *testing.T) {
	fixture := newReleaseFixture(t)
	customRoot := filepath.Join(t.TempDir(), "custom-release-root")
	t.Setenv(releaseDirEnv, customRoot)
	result, err := fixture.create(t, "v1.0", "1", "")
	if err != nil {
		t.Fatalf("create with custom root: %v", err)
	}
	assets, err := fixture.svc.logic.releaseDAO.ListAssets(adminTestContext(), fixture.svc.logic.db, result.Meta.SnapshotID)
	if err != nil || len(assets) != 1 {
		t.Fatalf("asset rows: %v", err)
	}
	if _, err := os.Stat(filepath.Join(customRoot, result.Meta.SnapshotID, "assets", assets[0].AssetID+".bin")); err != nil {
		t.Fatalf("snapshot file must live under the custom root: %v", err)
	}
	// 默认根下不应出现该快照目录
	defaultDir := filepath.Join(dataDirectory, releaseDefaultDirName, result.Meta.SnapshotID)
	if _, err := os.Stat(defaultDir); !os.IsNotExist(err) {
		t.Fatalf("snapshot must not exist under default root: %v", err)
	}
}

// --------------------- 完整性失败 ---------------------

func TestReleaseRuntimeIntegrityFailures(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()
	result, err := fixture.create(t, "v1.0", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	assets, _ := svc.logic.releaseDAO.ListAssets(ctx, svc.logic.db, result.Meta.SnapshotID)
	assetPath := filepath.Join(dataDirectory, releaseDefaultDirName, result.Meta.SnapshotID, "assets", assets[0].AssetID+".bin")

	original := append([]byte(nil), readFileBytes(t, assetPath)...)

	// Both stores now verify referenced hashes for runtime as well as download/activation.
	tampered := append([]byte(nil), original...)
	tampered[0] = tampered[0] ^ 0xff
	writeFileBytes(t, assetPath, tampered)
	if _, _, err := svc.ReleaseAssetDownload(ctx, result.Meta.SnapshotID, assets[0].AssetID); err == nil {
		t.Fatal("tampered download must fail")
	} else {
		requireReleaseErrorType(t, err, ErrTypeReleaseIncomplete)
	}
	if _, err := svc.RuntimeRelease(ctx, "qa_prod", "main", strPtr("v1.0")); err == nil {
		t.Fatal("runtime accepted same-size corruption")
	} else {
		requireReleaseErrorType(t, err, ErrTypeReleaseIncomplete)
	}
	// 激活校验检出同长度损坏
	if _, err := svc.ActivateRelease(ctx, ReleaseActivateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
		ExpectedActiveRevisionRaw: "0", RequestID: uuid.NewString(), Reason: "篡改后激活",
	}); err == nil {
		t.Fatal("activation must detect tampering")
	} else {
		requireReleaseErrorType(t, err, ErrTypeReleaseIncomplete)
	}

	// 文件缺失:读取 503
	os.Remove(assetPath)
	_, err = svc.RuntimeRelease(ctx, "qa_prod", "main", strPtr("v1.0"))
	requireReleaseErrorType(t, err, ErrTypeReleaseIncomplete)
	writeFileBytes(t, assetPath, original)

	// payload 篡改:读取与激活 503
	row, _ := svc.logic.releaseDAO.GetVersionBySnapshotID(ctx, svc.logic.db, result.Meta.SnapshotID)
	if err := svc.logic.db.Model(&model.ConfigReleaseVersion{}).
		Where("snapshot_id = ?", row.SnapshotID).
		Update("payload", strings.Replace(row.Payload, "REL_TEXT", "REL_TEXTX", 1)).Error; err != nil {
		t.Fatal(err)
	}
	_, err = svc.RuntimeRelease(ctx, "qa_prod", "main", strPtr("v1.0"))
	requireReleaseErrorType(t, err, ErrTypeReleaseIncomplete)
}

// --------------------- 删除保护 ---------------------

func TestReleaseParentDeleteProtection(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()
	if _, err := fixture.create(t, "v1.0", "1", ""); err != nil {
		t.Fatal(err)
	}
	// 版本历史不再阻止删除;删除后版本保留为逻辑删除历史
	if _, err := svc.UnlockFreeze(ctx, "qa_prod", "main", "删除保护", "1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeletePipeline(ctx, "qa_prod", "main"); err != nil {
		t.Fatalf("unlocked scope with versions must be deletable: %v", err)
	}
	if err := svc.DeleteEnvironment(ctx, "qa_prod"); err != nil {
		t.Fatalf("environment with no live pipelines must be deletable: %v", err)
	}
	if _, err := svc.ReleaseVersions(ctx, "qa_prod", "main", 1, ""); err == nil {
		t.Fatal("deleted scope must no longer be listed")
	}

	// 无版本范围同样可删除
	seedFreezeScope(t, svc, "qa_plain", "main")
	if err := svc.DeletePipeline(ctx, "qa_plain", "main"); err != nil {
		t.Fatalf("delete without versions must keep old behavior: %v", err)
	}
}

// --------------------- 校验与分页 ---------------------

func TestReleaseValidationAndListPagination(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()

	for _, bad := range []struct{ version, requestID, reason, revision string }{
		{"V1.0", uuid.NewString(), "原因", "1"},
		{"active", uuid.NewString(), "原因", "1"},
		{"current", uuid.NewString(), "原因", "1"},
		{"latest", uuid.NewString(), "原因", "1"},
		{strings.Repeat("a", 65), uuid.NewString(), "原因", "1"},
		{"v1.0", "not-a-uuid", "原因", "1"},
		{"v1.0", uuid.NewString(), strings.Repeat("汉", 501), "1"},
		{"v1.0", uuid.NewString(), "原因", "abc"},
		{"v1.0", uuid.NewString(), "原因", "-1"},
	} {
		_, err := svc.CreateRelease(ctx, ReleaseCreateInput{
			EnvironmentKey: "qa_prod", PipelineKey: "main", Version: bad.version,
			ExpectedFreezeRevisionRaw: bad.revision, RequestID: bad.requestID, Reason: bad.reason,
		})
		requireReleaseErrorType(t, err, ErrTypeReleaseInvalidRequest)
	}

	if _, err := fixture.create(t, "v1.0", "1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.create(t, "v1.1", "1", ""); err != nil {
		t.Fatal(err)
	}
	page, err := svc.ReleaseVersions(ctx, "qa_prod", "main", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || !page.HasMore || page.NextBeforeID == 0 || page.Active != nil {
		t.Fatalf("first page: %+v", page)
	}
	next, err := svc.ReleaseVersions(ctx, "qa_prod", "main", 1, fmt.Sprint(page.NextBeforeID))
	if err != nil || len(next.Items) != 1 || next.HasMore {
		t.Fatalf("second page: %v %+v", err, next)
	}
	if _, err := svc.ReleaseVersions(ctx, "qa_prod", "main", 1, "abc"); err == nil {
		t.Fatal("invalid cursor must be rejected")
	}
	if _, err := svc.ReleaseVersions(ctx, "qa_prod", "main", 1, "0"); err == nil {
		t.Fatal("zero cursor must be rejected")
	}
	// 范围不存在 → 404 SCOPE_NOT_FOUND
	_, err = svc.ReleaseVersions(ctx, "no_such_env", "main", 1, "")
	requireScopeErrorType(t, err, ErrTypeScopeNotFound)
	_, err = svc.ReleaseActive(ctx, "qa_prod", "no_such_pipeline")
	requireScopeErrorType(t, err, ErrTypeScopeNotFound)
}

// --------------------- 工具 ---------------------

func requireReleaseErrorType(t *testing.T, err error, errorType string) *ReleaseError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected release error %s, got nil", errorType)
	}
	releaseErr, ok := AsReleaseError(err)
	if !ok {
		t.Fatalf("expected release error %s, got %v", errorType, err)
	}
	if releaseErr.ErrorType != errorType {
		t.Fatalf("expected error type %s, got %s (%s)", errorType, releaseErr.ErrorType, releaseErr.Message)
	}
	return releaseErr
}

func strPtr(value string) *string { return &value }

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func findRuntimeConfig(t *testing.T, data *ReleaseRuntimeConfig, alias string) releaseRuntimeConfig {
	t.Helper()
	for _, cfg := range data.Configs {
		if cfg.Alias == alias {
			return cfg
		}
	}
	t.Fatalf("runtime config %s not found", alias)
	return releaseRuntimeConfig{}
}

func readFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func writeFileBytes(t *testing.T, path string, payload []byte) {
	t.Helper()
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
}

var _ = json.Marshal
