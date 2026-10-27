package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/transfer"
	"gorm.io/gorm"
)

// --------------------- 迁移资源引用与锁版(PRD RB-MIGRATE-01 §6/§7) ---------------------

// migrationRefTokenRegexp 是独立于生产扫描器的断言用提取器:
// 只认可 token 边界处的受管引用,外部 URL(https://cdn…/api/v1/asset/file/…)不计入。
var migrationRefTokenRegexp = regexp.MustCompile(`(?:^|[^a-zA-Z0-9_./:%-])(?:asset://|/api/v1/asset/file/)([a-zA-Z0-9_-]+)`)

// migrationRefIDs 从内容中提取受管引用 ID,供断言使用。
func migrationRefIDs(content string) []string {
	seen := map[string]bool{}
	ids := make([]string, 0)
	for _, match := range migrationRefTokenRegexp.FindAllStringSubmatch(content, -1) {
		if !seen[match[1]] {
			seen[match[1]] = true
			ids = append(ids, match[1])
		}
	}
	return ids
}

func mustConfigByAlias(t *testing.T, svc *Service, environmentKey, pipelineKey, alias string) *model.Config {
	t.Helper()
	cfg, err := svc.logic.configDAO.GetByAlias(adminTestContext(), svc.logic.db, environmentKey, pipelineKey, alias)
	if err != nil {
		t.Fatalf("config alias=%s in %s/%s: %v", alias, environmentKey, pipelineKey, err)
	}
	return cfg
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// mustAssetBytes 读取资源元数据与实际字节;要求归属指定范围。
func mustAssetBytes(t *testing.T, svc *Service, fileID, environmentKey, pipelineKey string) (*model.Asset, []byte) {
	t.Helper()
	asset, err := svc.logic.GetAsset(adminTestContext(), fileID)
	if err != nil {
		t.Fatalf("asset %s not found: %v", fileID, err)
	}
	if asset.EnvironmentKey != environmentKey || asset.PipelineKey != pipelineKey {
		t.Fatalf("asset %s belongs to %s/%s, want %s/%s", fileID,
			asset.EnvironmentKey, asset.PipelineKey, environmentKey, pipelineKey)
	}
	payload, err := svc.readManagedAssetFileBytes(adminTestContext(), asset)
	if err != nil {
		t.Fatalf("read asset %s bytes: %v", fileID, err)
	}
	return asset, payload
}

func migrateAll(sourceEnv, sourcePipeline, targetEnv, targetPipeline string, overwrite bool) *transfer.MigrateRequest {
	return &transfer.MigrateRequest{
		SourceEnvironmentKey: sourceEnv,
		SourcePipelineKey:    sourcePipeline,
		TargetEnvironmentKey: targetEnv,
		TargetPipelineKey:    targetPipeline,
		Overwrite:            overwrite,
	}
}

func requireSucceededSummary(t *testing.T, summary *transfer.MigrateSummary, err error, wantSucceeded, wantFailed int) {
	t.Helper()
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if summary == nil {
		t.Fatal("migrate returned nil summary")
	}
	if int(summary.Succeeded) != wantSucceeded || int(summary.Failed) != wantFailed {
		t.Fatalf("unexpected summary: succeeded=%d failed=%d skipped=%d items=%+v",
			summary.Succeeded, summary.Failed, summary.Skipped, summary.Items)
	}
	if int(summary.Total) != len(summary.Items) {
		t.Fatalf("total=%d must equal len(items)=%d", summary.Total, len(summary.Items))
	}
}

// TestMigrateRewritesStoredAssetURLAndAllowsFreeze 覆盖 PRD §7 主链路:
// 正常上传保存的图片迁移到目标范围后,目标配置使用目标新 ID、新资源行归属目标范围、
// 源引用不变、目标字节一致,且目标可以成功锁版。
func TestMigrateRewritesStoredAssetURLAndAllowsFreeze(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "123", "default")
	seedFreezeScope(t, svc, "456", "default")
	ctx := adminTestContext()

	payload := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 'm', 'i', 'g', 'r', 'a', 't', 'e'}
	uploaded, reference, err := svc.UploadAsset(ctx, &FileUploadInput{
		EnvironmentKey: "123",
		PipelineKey:    "default",
		FileName:       "pic.png",
		ContentType:    "image/png",
		Data:           payload,
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	oldID := uploaded.GetFileId()
	if want := assetScheme + oldID; reference != want {
		t.Fatalf("unexpected upload reference %q, want %q", reference, want)
	}

	if _, err := svc.AddConfig(ctx, buildConfig("123", "default", "pic", "image", reference)); err != nil {
		t.Fatalf("add config: %v", err)
	}
	stored := mustConfigByAlias(t, svc, "123", "default", "pic")
	wantStored := "/api/v1/asset/file/" + oldID + "/pic.png"
	if stored.Content != wantStored {
		t.Fatalf("stored source content = %q, want %q", stored.Content, wantStored)
	}

	summary, err := svc.MigrateConfigs(ctx, migrateAll("123", "default", "456", "default", false))
	requireSucceededSummary(t, summary, err, 1, 0)

	target := mustConfigByAlias(t, svc, "456", "default", "pic")
	targetIDs := migrationRefIDs(target.Content)
	if len(targetIDs) != 1 {
		t.Fatalf("target content must reference exactly one asset: %q", target.Content)
	}
	newID := targetIDs[0]
	if newID == oldID {
		t.Fatalf("target content still references source asset: %q", target.Content)
	}
	wantTarget := "/api/v1/asset/file/" + newID + "/pic.png"
	if target.Content != wantTarget {
		t.Fatalf("target content = %q, want %q", target.Content, wantTarget)
	}
	if strings.Contains(target.Content, svc.basePath) {
		t.Fatalf("persisted content must not carry base_path: %q", target.Content)
	}

	asset, gotBytes := mustAssetBytes(t, svc, newID, "456", "default")
	if asset.FileName != "pic.png" || asset.ContentType != "image/png" {
		t.Fatalf("target asset metadata mismatch: %+v", asset)
	}
	if asset.FileSize != int64(len(payload)) {
		t.Fatalf("target asset file_size=%d, want %d", asset.FileSize, len(payload))
	}
	if !bytes.Equal(gotBytes, payload) {
		t.Fatalf("target bytes differ: sha=%s want=%s", sha256Hex(gotBytes), sha256Hex(payload))
	}

	// 源配置与源资源不变
	unchanged := mustConfigByAlias(t, svc, "123", "default", "pic")
	if unchanged.Content != wantStored {
		t.Fatalf("source content changed: %q", unchanged.Content)
	}
	if _, sourceBytes := mustAssetBytes(t, svc, oldID, "123", "default"); !bytes.Equal(sourceBytes, payload) {
		t.Fatal("source asset bytes changed")
	}

	// 目标锁版:不得再报 cross_scope_reference
	locked := mustLock(t, svc, "456", "default", "迁移后锁版")
	if locked.State != model.FreezeStateLocked {
		t.Fatalf("target lock state = %s", locked.State)
	}
	// 源仍可锁版,说明源资源归属未被破坏
	mustLock(t, svc, "123", "default", "源范围锁版")
}

// TestMigrateMissingSourceAssetFailsItem 覆盖 PRD §3.3/§7:
// 源文件缺失时必须计入 failed,不得 succeeded,且目标不写入该配置。
func TestMigrateMissingSourceAssetFailsItem(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "123", "default")
	seedFreezeScope(t, svc, "456", "default")
	ctx := adminTestContext()

	// 资源元数据存在但文件不在磁盘(模拟源文件缺失)
	missingID := "missing-file-asset-1"
	if err := svc.logic.CreateAsset(ctx, &model.Asset{
		FileID:         missingID,
		EnvironmentKey: "123",
		PipelineKey:    "default",
		FileName:       "gone.png",
		ContentType:    "image/png",
		FileSize:       32,
		Path:           "uploads/" + missingID + "/gone.png",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("123", "default", "broken", "image", assetScheme+missingID)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("123", "default", "healthy", "text", "普通文本无引用")); err != nil {
		t.Fatal(err)
	}

	targetRowsBefore := countScopeConfigs(t, svc, "456", "default")
	filesBefore := countUploadFiles(t)

	summary, err := svc.MigrateConfigs(ctx, migrateAll("123", "default", "456", "default", false))
	requireSucceededSummary(t, summary, err, 1, 1)

	var failedItem *transfer.MigrateResultItem
	for _, item := range summary.Items {
		if item.Status == "failed" {
			failedItem = item
		}
	}
	if failedItem == nil || failedItem.Alias != "broken" {
		t.Fatalf("expected failed item for broken alias: %+v", summary.Items)
	}
	if !strings.Contains(failedItem.Message, "broken") || !strings.Contains(failedItem.Message, missingID) {
		t.Fatalf("failure message must locate alias and file_id: %q", failedItem.Message)
	}
	// 不得返回服务器绝对路径
	if strings.Contains(failedItem.Message, "uploads/") || strings.Contains(failedItem.Message, ":\\") {
		t.Fatalf("failure message leaks path detail: %q", failedItem.Message)
	}

	if got := countScopeConfigs(t, svc, "456", "default"); got != targetRowsBefore+1 {
		t.Fatalf("target must contain only the healthy item: %d != %d", got, targetRowsBefore+1)
	}
	if _, err := svc.logic.configDAO.GetByAlias(ctx, svc.logic.db, "456", "default", "broken"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("failed item must not write target config: %v", err)
	}
	if got := countUploadFiles(t); got != filesBefore {
		t.Fatalf("failed item must not leave new files: %d != %d", got, filesBefore)
	}
}

// TestMigrateRewritesTextAndNestedReferences 覆盖 PRD §3.2/§7:
// 文本 token 边界、单/双 base_path、query/fragment 后缀保持、同项去重、
// object 中嵌套引用与数字字面量保真、相似 ID 不串改。
func TestMigrateRewritesTextAndNestedReferences(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "123", "default")
	seedFreezeScope(t, svc, "456", "default")
	ctx := adminTestContext()

	registerAsset := func(fileID, fileName string, data []byte) {
		writeUploadedFile(t, fileID, fileName, data)
		if err := svc.logic.CreateAsset(ctx, &model.Asset{
			FileID:         fileID,
			EnvironmentKey: "123",
			PipelineKey:    "default",
			FileName:       fileName,
			ContentType:    "image/png",
			FileSize:       int64(len(data)),
			Path:           "uploads/" + fileID + "/" + fileName,
		}); err != nil {
			t.Fatal(err)
		}
	}
	registerAsset("sim-id", "a.png", []byte("bytes-a"))
	registerAsset("sim-id-2", "b.png", []byte("bytes-b"))

	// 文本:同项两次引用(简写带 query + 前缀 URL 带 fragment),加相似 ID 前后缀干扰
	textContent := "首图 asset://sim-id?w=1 ,次级 /rainbow-bridge/api/v1/asset/file/sim-id/a.png#top ;" +
		"相似 /rainbow-bridge/rainbow-bridge/api/v1/asset/file/sim-id-2/b.png ;" +
		"外部 https://cdn.example.com/static/api/v1/asset/file/sim-id-2/b.png ;" +
		"正文 UUID 8f14e45f-ceea-667a-1111-000000000000 保持原文"
	if _, err := svc.AddConfig(ctx, buildConfig("123", "default", "note", "text", textContent)); err != nil {
		t.Fatal(err)
	}

	// object:嵌套数组引用 + 大整数/长小数保真
	objectContent := `{"title":"卡片","images":["asset://sim-id?x=2"],"nested":{"file":"/api/v1/asset/file/sim-id-2/b.png"},"big":9007199254740993,"dec":0.10000000000000001,"float":1e21}`
	if _, err := svc.AddConfig(ctx, buildConfig("123", "default", "card", "object", objectContent)); err != nil {
		t.Fatal(err)
	}

	filesBefore := countUploadFiles(t)
	summary, err := svc.MigrateConfigs(ctx, migrateAll("123", "default", "456", "default", false))
	requireSucceededSummary(t, summary, err, 2, 0)

	// 文本:两个新 ID(各复制一次),后缀保留,外部 URL 与内部标记不被改写
	note := mustConfigByAlias(t, svc, "456", "default", "note")
	noteIDs := migrationRefIDs(note.Content)
	if len(noteIDs) != 2 {
		t.Fatalf("text item must map to exactly 2 target assets: %q", note.Content)
	}
	if noteIDs[0] == noteIDs[1] || noteIDs[0] == "sim-id" || noteIDs[1] == "sim-id-2" {
		t.Fatalf("text references not rewritten: %q", note.Content)
	}
	if !strings.Contains(note.Content, "?w=1") || !strings.Contains(note.Content, "#top") {
		t.Fatalf("query/fragment suffix must be preserved: %q", note.Content)
	}
	if !strings.Contains(note.Content, "https://cdn.example.com/static/api/v1/asset/file/sim-id-2/b.png") {
		t.Fatalf("external URL must stay untouched: %q", note.Content)
	}
	if !strings.Contains(note.Content, "8f14e45f-ceea-667a-1111-000000000000") {
		t.Fatalf("plain text uuid must stay untouched: %q", note.Content)
	}
	if strings.Contains(note.Content, "/rainbow-bridge/api/v1/asset/file/") {
		t.Fatalf("rewritten content must not keep base_path prefix: %q", note.Content)
	}
	// 相似 ID 不串改:sim-id 的目标 ID 不得是 sim-id-2 目标 ID 的前缀混写
	if wantA := "/api/v1/asset/file/" + noteIDs[0] + "/a.png"; !strings.Contains(note.Content, wantA) {
		t.Fatalf("prefix-similar rewrite mismatch: %q want %q", note.Content, wantA)
	}
	if wantB := "/api/v1/asset/file/" + noteIDs[1] + "/b.png"; !strings.Contains(note.Content, wantB) {
		t.Fatalf("prefix-similar rewrite mismatch: %q want %q", note.Content, wantB)
	}
	_, bytesA := mustAssetBytes(t, svc, noteIDs[0], "456", "default")
	_, bytesB := mustAssetBytes(t, svc, noteIDs[1], "456", "default")
	if !bytes.Equal(bytesA, []byte("bytes-a")) || !bytes.Equal(bytesB, []byte("bytes-b")) {
		t.Fatalf("similar ids cross-wired: a=%q b=%q", bytesA, bytesB)
	}

	// object:嵌套引用全部映射到同一目标 ID,数字字面量逐字保留
	card := mustConfigByAlias(t, svc, "456", "default", "card")
	cardIDs := migrationRefIDs(card.Content)
	if len(cardIDs) != 2 {
		t.Fatalf("object item must map to exactly 2 target assets: %q", card.Content)
	}
	var decoded map[string]any
	decoder := json.NewDecoder(strings.NewReader(card.Content))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("target object content must stay valid JSON: %v (%q)", err, card.Content)
	}
	for _, raw := range []string{"9007199254740993", "0.10000000000000001", "1e21"} {
		if !strings.Contains(card.Content, raw) {
			t.Fatalf("number literal %s lost in %q", raw, card.Content)
		}
	}
	if decoded["title"] != "卡片" {
		t.Fatalf("non-asset values must be preserved: %+v", decoded)
	}
	if !strings.Contains(card.Content, "?x=2") {
		t.Fatalf("nested reference suffix must be preserved: %q", card.Content)
	}

	// 同项去重:文本项 2 处引用只复制 2 份文件(每 ID 一份),object 2 份
	if got := countUploadFiles(t) - filesBefore; got != 4 {
		t.Fatalf("dedupe expectation: new files=%d want 4", got)
	}
}

// TestMigrateInternalReleaseMarkerFails 覆盖 PRD §3.2:
// 配置内容含 release-asset:// 等发布内部标记时,该项明确失败,不当作可复制资源。
func TestMigrateInternalReleaseMarkerFails(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "123", "default")
	seedFreezeScope(t, svc, "456", "default")
	ctx := adminTestContext()

	if _, err := svc.AddConfig(ctx, buildConfig("123", "default", "marker", "text",
		"从发布包粘贴的标记 release-asset://deadbeef 需要人工确认")); err != nil {
		t.Fatal(err)
	}
	filesBefore := countUploadFiles(t)
	summary, err := svc.MigrateConfigs(ctx, migrateAll("123", "default", "456", "default", false))
	requireSucceededSummary(t, summary, err, 0, 1)
	if !strings.Contains(summary.Items[0].Message, migrationFailureInternalMarker) {
		t.Fatalf("failure must report internal marker category: %q", summary.Items[0].Message)
	}
	if _, err := svc.logic.configDAO.GetByAlias(ctx, svc.logic.db, "456", "default", "marker"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("internal marker item must not be written: %v", err)
	}
	if got := countUploadFiles(t); got != filesBefore {
		t.Fatalf("internal marker item must not copy files: %d != %d", got, filesBefore)
	}
}

// TestMigrateOverwriteRepairsBrokenTargetReference 覆盖 PRD §4/§7:
// 历史上迁坏的目标(引用源范围资源)可用 overwrite 重迁移修复,保留目标 key/alias。
func TestMigrateOverwriteRepairsBrokenTargetReference(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "123", "default")
	seedFreezeScope(t, svc, "456", "default")
	ctx := adminTestContext()

	payload := []byte("repair-bytes")
	uploaded, reference, err := svc.UploadAsset(ctx, &FileUploadInput{
		EnvironmentKey: "123",
		PipelineKey:    "default",
		FileName:       "badge.png",
		ContentType:    "image/png",
		Data:           payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("123", "default", "badge", "image", reference)); err != nil {
		t.Fatal(err)
	}

	// 模拟历史缺陷:目标存在同 alias 但引用源资源
	brokenTarget, err := svc.AddConfig(ctx, buildConfig("456", "default", "badge", "image", reference))
	if err != nil {
		t.Fatal(err)
	}
	brokenKey := brokenTarget.ResourceKey

	// overwrite=false:跳过且不新增资源
	filesBefore := countUploadFiles(t)
	summary, err := svc.MigrateConfigs(ctx, migrateAll("123", "default", "456", "default", false))
	requireSucceededSummary(t, summary, err, 0, 0)
	if summary.Skipped != 1 || summary.Items[0].Status != "skipped" {
		t.Fatalf("expected skip: %+v", summary.Items)
	}
	if got := countUploadFiles(t); got != filesBefore {
		t.Fatalf("skip must not copy files: %d != %d", got, filesBefore)
	}

	// overwrite=true:修复引用,保留目标 identity
	summary, err = svc.MigrateConfigs(ctx, migrateAll("123", "default", "456", "default", true))
	requireSucceededSummary(t, summary, err, 1, 0)
	repaired := mustConfigByAlias(t, svc, "456", "default", "badge")
	if repaired.ResourceKey != brokenKey {
		t.Fatalf("overwrite must keep target resource_key: %s != %s", repaired.ResourceKey, brokenKey)
	}
	repairedIDs := migrationRefIDs(repaired.Content)
	if len(repairedIDs) != 1 || repairedIDs[0] == uploaded.GetFileId() {
		t.Fatalf("broken reference not repaired: %q", repaired.Content)
	}
	if _, bytesOut := mustAssetBytes(t, svc, repairedIDs[0], "456", "default"); !bytes.Equal(bytesOut, payload) {
		t.Fatal("repaired asset bytes differ")
	}

	// 重复覆盖同一输入(锁版前):配置数量不增加,引用仍有效(允许新复制资源,不承诺跨次去重)
	rowsBefore := countScopeConfigs(t, svc, "456", "default")
	summary, err = svc.MigrateConfigs(ctx, migrateAll("123", "default", "456", "default", true))
	requireSucceededSummary(t, summary, err, 1, 0)
	if got := countScopeConfigs(t, svc, "456", "default"); got != rowsBefore {
		t.Fatalf("repeated overwrite must not add configs: %d != %d", got, rowsBefore)
	}
	again := mustConfigByAlias(t, svc, "456", "default", "badge")
	againIDs := migrationRefIDs(again.Content)
	if len(againIDs) != 1 {
		t.Fatalf("repeated overwrite must keep exactly one reference: %q", again.Content)
	}
	if _, bytesAgain := mustAssetBytes(t, svc, againIDs[0], "456", "default"); !bytes.Equal(bytesAgain, payload) {
		t.Fatal("repeated overwrite bytes differ")
	}

	mustLock(t, svc, "456", "default", "修复后锁版")
}

// TestMigrateAcrossPipelinesWithinSameEnvironment 覆盖 PRD §7:
// 同环境不同渠道迁移时资源归属按渠道判定,目标渠道锁版通过。
func TestMigrateAcrossPipelinesWithinSameEnvironment(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_same", "main")
	seedFreezeScope(t, svc, "qa_same", "beta")
	ctx := adminTestContext()

	payload := []byte("channel-scope-bytes")
	_, reference, err := svc.UploadAsset(ctx, &FileUploadInput{
		EnvironmentKey: "qa_same",
		PipelineKey:    "main",
		FileName:       "chan.png",
		ContentType:    "image/png",
		Data:           payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("qa_same", "main", "chan", "image", reference)); err != nil {
		t.Fatal(err)
	}

	summary, err := svc.MigrateConfigs(ctx, migrateAll("qa_same", "main", "qa_same", "beta", false))
	requireSucceededSummary(t, summary, err, 1, 0)

	target := mustConfigByAlias(t, svc, "qa_same", "beta", "chan")
	ids := migrationRefIDs(target.Content)
	if len(ids) != 1 {
		t.Fatalf("target must reference exactly one asset: %q", target.Content)
	}
	// 必须按渠道(而不是仅环境名)判定归属
	if _, bytesOut := mustAssetBytes(t, svc, ids[0], "qa_same", "beta"); !bytes.Equal(bytesOut, payload) {
		t.Fatal("migrated bytes differ")
	}
	if _, err := svc.logic.GetAsset(ctx, ids[0]); err != nil {
		t.Fatalf("target asset missing: %v", err)
	}
	mustLock(t, svc, "qa_same", "beta", "同环境跨渠道后锁版")
	// 源渠道内容与状态不变
	unchanged := mustConfigByAlias(t, svc, "qa_same", "main", "chan")
	if len(migrationRefIDs(unchanged.Content)) != 1 || !strings.Contains(unchanged.Content, reference[len(assetScheme):]) {
		t.Fatalf("source content changed: %q", unchanged.Content)
	}
}

// TestMigrateCrossScopeSourceReferenceFails 覆盖 PRD §3.3/§7:
// 源配置引用第三方范围资源时必须失败,不擅自复制第三方资源。
func TestMigrateCrossScopeSourceReferenceFails(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "123", "default")
	seedFreezeScope(t, svc, "456", "default")
	seedFreezeScope(t, svc, "789", "default")
	ctx := adminTestContext()

	foreignID := "foreign-scope-asset-1"
	writeUploadedFile(t, foreignID, "third.png", []byte("third-party-bytes"))
	if err := svc.logic.CreateAsset(ctx, &model.Asset{
		FileID:         foreignID,
		EnvironmentKey: "789",
		PipelineKey:    "default",
		FileName:       "third.png",
		ContentType:    "image/png",
		FileSize:       int64(len("third-party-bytes")),
		Path:           "uploads/" + foreignID + "/third.png",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("123", "default", "borrowed", "image", assetScheme+foreignID)); err != nil {
		t.Fatal(err)
	}

	filesBefore := countUploadFiles(t)
	summary, err := svc.MigrateConfigs(ctx, migrateAll("123", "default", "456", "default", false))
	requireSucceededSummary(t, summary, err, 0, 1)
	if !strings.Contains(summary.Items[0].Message, foreignID) {
		t.Fatalf("failure must locate file_id: %q", summary.Items[0].Message)
	}
	if _, err := svc.logic.configDAO.GetByAlias(ctx, svc.logic.db, "456", "default", "borrowed"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-scope item must not be written: %v", err)
	}
	if got := countUploadFiles(t); got != filesBefore {
		t.Fatalf("cross-scope item must not copy foreign resources: %d != %d", got, filesBefore)
	}
	mustLock(t, svc, "789", "default", "第三方范围不受影响")
}

// requireNoMigrationHook 确保测试结束后清除故障注入点。
func requireNoMigrationHook(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { migrationHookForTest = nil })
}

// newestUploadFile 返回受管上传目录中最近写入的文件路径(用于清理注入)。
func newestUploadFile(t *testing.T) string {
	t.Helper()
	newest := ""
	var newestMod time.Time
	root := filepath.Join(dataDirectory, uploadDirectory)
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if newest == "" || info.ModTime().After(newestMod) {
			newest = path
			newestMod = info.ModTime()
		}
		return nil
	})
	if newest == "" {
		t.Fatal("no uploaded file found for injection")
	}
	return newest
}

// TestMigrateConfigSaveFailureRollsBackResources 覆盖 PRD §7:
// 资源复制完成后配置保存失败 → 该项保存点回滚,新资源行与文件都清理,目标旧内容不变。
func TestMigrateConfigSaveFailureRollsBackResources(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "123", "default")
	seedFreezeScope(t, svc, "456", "default")
	ctx := adminTestContext()

	payload := []byte("rollback-on-config-write")
	uploaded, reference, err := svc.UploadAsset(ctx, &FileUploadInput{
		EnvironmentKey: "123",
		PipelineKey:    "default",
		FileName:       "pic.png",
		ContentType:    "image/png",
		Data:           payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("123", "default", "pic", "image", reference)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("123", "default", "plain", "text", "无引用文本")); err != nil {
		t.Fatal(err)
	}

	requireNoMigrationHook(t)
	migrationHookForTest = func(stage string, _ *Logic, _ *model.Config) error {
		if stage == "assets_copied" {
			return errors.New("injected config write failure")
		}
		return nil
	}

	filesBefore := countUploadFiles(t)
	assetsBefore := countScopeAssets(t, svc, "456", "default")
	summary, err := svc.MigrateConfigs(ctx, migrateAll("123", "default", "456", "default", false))
	requireSucceededSummary(t, summary, err, 1, 1)

	if _, err := svc.logic.configDAO.GetByAlias(ctx, svc.logic.db, "456", "default", "pic"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("failed item must roll back target config: %v", err)
	}
	if got := countScopeAssets(t, svc, "456", "default"); got != assetsBefore {
		t.Fatalf("savepoint rollback must remove new asset rows: %d != %d", got, assetsBefore)
	}
	if got := countUploadFiles(t); got != filesBefore {
		t.Fatalf("rollback must clean the item's new files: %d != %d", got, filesBefore)
	}
	// 源资源未受影响
	if _, bytesOut := mustAssetBytes(t, svc, uploaded.GetFileId(), "123", "default"); !bytes.Equal(bytesOut, payload) {
		t.Fatal("source asset must stay intact")
	}
	// 健康项已提交且可锁版
	mustLock(t, svc, "456", "default", "部分失败后锁版")
}

// TestMigrateCleanupResidueIsReported 覆盖 PRD §3.4/§7:
// 清理失败不能静默:item 消息给出 file_id 与残留类别,且不删除旧共享文件。
func TestMigrateCleanupResidueIsReported(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "123", "default")
	seedFreezeScope(t, svc, "456", "default")
	ctx := adminTestContext()

	_, reference, err := svc.UploadAsset(ctx, &FileUploadInput{
		EnvironmentKey: "123",
		PipelineKey:    "default",
		FileName:       "held.png",
		ContentType:    "image/png",
		Data:           []byte("held-bytes"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("123", "default", "held", "image", reference)); err != nil {
		t.Fatal(err)
	}

	requireNoMigrationHook(t)
	var held *os.File
	t.Cleanup(func() {
		if held != nil {
			_ = held.Close()
		}
	})
	migrationHookForTest = func(stage string, _ *Logic, _ *model.Config) error {
		if stage != "assets_copied" {
			return nil
		}
		// 打开新建文件句柄(Windows 下阻止删除),再让配置保存失败
		file, err := os.Open(newestUploadFile(t))
		if err != nil {
			return err
		}
		held = file
		return errors.New("injected failure with locked target file")
	}

	summary, err := svc.MigrateConfigs(ctx, migrateAll("123", "default", "456", "default", false))
	requireSucceededSummary(t, summary, err, 0, 1)
	message := summary.Items[0].Message
	if !strings.Contains(message, migrationFailureCleanupResidue) || !strings.Contains(message, "file_id=") {
		t.Fatalf("cleanup failure must be reported with locators: %q", message)
	}
	if !strings.Contains(message, "held") {
		t.Fatalf("cleanup failure must locate the alias: %q", message)
	}
}

// TestMigrateReadbackMismatchFailsItem 覆盖 PRD §3.3/§7:
// 保存后回读内容与重写结果不一致 → 该项失败并回滚,不等到锁版才暴露。
func TestMigrateReadbackMismatchFailsItem(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "123", "default")
	seedFreezeScope(t, svc, "456", "default")
	ctx := adminTestContext()

	_, reference, err := svc.UploadAsset(ctx, &FileUploadInput{
		EnvironmentKey: "123",
		PipelineKey:    "default",
		FileName:       "verify.png",
		ContentType:    "image/png",
		Data:           []byte("verify-bytes"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("123", "default", "verify", "image", reference)); err != nil {
		t.Fatal(err)
	}

	requireNoMigrationHook(t)
	migrationHookForTest = func(stage string, sp *Logic, cfg *model.Config) error {
		if stage != "config_written" || cfg == nil {
			return nil
		}
		// 在同一保存点连接上篡改已落库内容,模拟落库与重写结果不一致
		return sp.db.WithContext(adminTestContext()).Model(&model.Config{}).
			Where("resource_key = ?", cfg.ResourceKey).Update("content", "tampered").Error
	}

	filesBefore := countUploadFiles(t)
	summary, err := svc.MigrateConfigs(ctx, migrateAll("123", "default", "456", "default", false))
	requireSucceededSummary(t, summary, err, 0, 1)
	if !strings.Contains(summary.Items[0].Message, migrationFailureReadbackFailed) {
		t.Fatalf("readback mismatch must fail the item: %q", summary.Items[0].Message)
	}
	if _, err := svc.logic.configDAO.GetByAlias(ctx, svc.logic.db, "456", "default", "verify"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("readback failure must roll back target config: %v", err)
	}
	if got := countUploadFiles(t); got != filesBefore {
		t.Fatalf("readback failure must clean new files: %d != %d", got, filesBefore)
	}
}

// countScopeAssets 统计某范围的资源行数。
func countScopeAssets(t *testing.T, svc *Service, environmentKey, pipelineKey string) int {
	t.Helper()
	assets, err := svc.logic.ListAssetsByEnvironmentAndPipeline(adminTestContext(), environmentKey, pipelineKey)
	if err != nil {
		t.Fatalf("list assets %s/%s: %v", environmentKey, pipelineKey, err)
	}
	return len(assets)
}

// TestMigrateItemRollbackKeepsTargetIntact 覆盖 PRD §3.4/§7:
// 项内第二份资源失败时整项回滚,第一份本次新资源也被清理,其他项可提交。
func TestMigrateItemRollbackKeepsTargetIntact(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "123", "default")
	seedFreezeScope(t, svc, "456", "default")
	ctx := adminTestContext()

	firstID := "rollback-first-1"
	secondID := "rollback-second-2"
	writeUploadedFile(t, firstID, "first.png", []byte("first-bytes"))
	// second 只有元数据,文件缺失 → 第二份复制失败
	for _, spec := range []struct {
		fileID string
		name   string
		data   []byte
	}{{firstID, "first.png", []byte("first-bytes")}, {secondID, "second.png", nil}} {
		payload := spec.data
		if err := svc.logic.CreateAsset(ctx, &model.Asset{
			FileID:         spec.fileID,
			EnvironmentKey: "123",
			PipelineKey:    "default",
			FileName:       spec.name,
			ContentType:    "image/png",
			FileSize:       int64(len(payload)),
			Path:           "uploads/" + spec.fileID + "/" + spec.name,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mixed := fmt.Sprintf(`{"ok":%q,"broken":%q}`, assetScheme+firstID, assetScheme+secondID)
	if _, err := svc.AddConfig(ctx, buildConfig("123", "default", "mixed", "object", mixed)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("123", "default", "plain", "text", "无引用文本")); err != nil {
		t.Fatal(err)
	}

	filesBefore := countUploadFiles(t)
	summary, err := svc.MigrateConfigs(ctx, migrateAll("123", "default", "456", "default", false))
	requireSucceededSummary(t, summary, err, 1, 1)

	if _, err := svc.logic.configDAO.GetByAlias(ctx, svc.logic.db, "456", "default", "mixed"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("failed item must be rolled back: %v", err)
	}
	if got := countUploadFiles(t); got != filesBefore {
		t.Fatalf("savepoint rollback must clean its new files: %d != %d", got, filesBefore)
	}
	if got := countScopeConfigs(t, svc, "456", "default"); got != 1 {
		t.Fatalf("target must only contain the healthy item: %d", got)
	}
}
