package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	envpb "github.com/yi-nology/rainbow_bridge/biz/model/environment"
	plpb "github.com/yi-nology/rainbow_bridge/biz/model/pipeline"
	"github.com/yi-nology/rainbow_bridge/biz/model/transfer"
	"gorm.io/gorm"
)

// --------------------- 写入口拦截与隔离 ---------------------

func TestFreezeBlocksAllWriteEntries(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	ctx := context.Background()

	// 锁定前创建一条配置,用于更新/删除拦截验证
	existing, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "BEFORE_LOCK", "text", "original"))
	if err != nil {
		t.Fatal(err)
	}
	filesBefore := countUploadFiles(t)

	locked := mustLock(t, svc, "qa_prod", "main", "拦截验证")
	if locked.State != model.FreezeStateLocked {
		t.Fatalf("lock failed: %+v", locked)
	}

	// 新增/更新/删除 全部 409,内容与数量不变
	_, err = svc.AddConfig(ctx, buildConfig("qa_prod", "main", "AFTER_LOCK", "text", "x"))
	requireFreezeErrorType(t, err, ErrTypeScopeFrozen)

	update := buildConfig("qa_prod", "main", "BEFORE_LOCK", "text", "changed")
	update.ResourceKey = existing.ResourceKey
	_, err = svc.UpdateConfig(ctx, update)
	requireFreezeErrorType(t, err, ErrTypeScopeFrozen)

	err = svc.DeleteConfig(ctx, "qa_prod", "main", existing.ResourceKey)
	requireFreezeErrorType(t, err, ErrTypeScopeFrozen)

	// 上传资源:拒绝且不能先创建目录/文件
	_, _, err = svc.UploadAsset(ctx, &FileUploadInput{
		EnvironmentKey: "qa_prod",
		PipelineKey:    "main",
		FileName:       "frozen.txt",
		ContentType:    "text/plain",
		Data:           []byte("hello"),
	})
	requireFreezeErrorType(t, err, ErrTypeScopeFrozen)
	if got := countUploadFiles(t); got != filesBefore {
		t.Fatalf("frozen upload must not create files: before=%d after=%d", filesBefore, got)
	}

	// 渠道属性与删除、父环境更新/删除 全部拒绝
	if err := svc.UpdatePipeline(ctx, "qa_prod", &plpb.Pipeline{PipelineKey: "main", PipelineName: "renamed", IsActive: true}); err == nil {
		t.Fatal("update locked pipeline must fail")
	} else {
		requireFreezeErrorType(t, err, ErrTypeScopeFrozen)
	}
	err = svc.DeletePipeline(ctx, "qa_prod", "main")
	requireFreezeErrorType(t, err, ErrTypeScopeFrozen)
	err = svc.UpdateEnvironment(ctx, &envpb.Environment{EnvironmentKey: "qa_prod", EnvironmentName: "renamed", IsActive: true})
	requireFreezeErrorType(t, err, ErrTypeScopeFrozen)
	err = svc.DeleteEnvironment(ctx, "qa_prod")
	requireFreezeErrorType(t, err, ErrTypeScopeFrozen)

	// 在同环境新建其他渠道允许,不修改已冻结渠道
	if err := svc.AddPipeline(ctx, "qa_prod", &plpb.Pipeline{PipelineKey: "extra", PipelineName: "extra", IsActive: true}); err != nil {
		t.Fatalf("creating a sibling pipeline must be allowed: %v", err)
	}

	// 被拒操作后的库内状态
	rows, err := svc.logic.ListConfigs(ctx, "qa_prod", "main", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Content != "original" || rows[0].Alias != "BEFORE_LOCK" {
		t.Fatalf("locked scope data changed: %+v", rows)
	}

	// 解锁后恢复可编辑
	if _, err := svc.UnlockFreeze(ctx, "qa_prod", "main", "验证完成", "1", "Bearer "+testAdminToken()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "AFTER_UNLOCK", "text", "ok")); err != nil {
		t.Fatalf("writes must succeed after unlock: %v", err)
	}
	if _, _, err := svc.UploadAsset(ctx, &FileUploadInput{
		EnvironmentKey: "qa_prod",
		PipelineKey:    "main",
		FileName:       "after-unlock.txt",
		ContentType:    "text/plain",
		Data:           []byte("hello"),
	}); err != nil {
		t.Fatalf("upload must succeed after unlock: %v", err)
	}
}

func TestFreezeScopeIsolation(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	seedFreezeScope(t, svc, "qa_prod", "beta")
	seedFreezeScope(t, svc, "qa_dev", "main")
	ctx := context.Background()

	mustLock(t, svc, "qa_prod", "main", "仅冻结 prod/main")

	if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "beta", "BETA_OK", "text", "x")); err != nil {
		t.Fatalf("sibling pipeline must stay writable: %v", err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("qa_dev", "main", "DEV_OK", "text", "x")); err != nil {
		t.Fatalf("other environment must stay writable: %v", err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "PROD_BLOCKED", "text", "x")); err == nil {
		t.Fatal("locked scope must reject writes")
	}
}

// --------------------- 导入 / 迁移 规则 ---------------------

func TestFreezeImportAndMigrateRules(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_dev", "main")
	seedFreezeScope(t, svc, "qa_dev", "beta")
	seedFreezeScope(t, svc, "qa_prod", "main")
	ctx := context.Background()

	// dev/main 资源(文件真实落盘)
	assetID := "freeze-test-asset-1"
	writeUploadedFile(t, assetID, "pic.txt", []byte("asset-bytes"))
	if err := svc.logic.CreateAsset(ctx, &model.Asset{
		FileID:         assetID,
		EnvironmentKey: "qa_dev",
		PipelineKey:    "main",
		FileName:       "pic.txt",
		ContentType:    "text/plain",
		FileSize:       int64(len("asset-bytes")),
		Path:           filepath.Join(uploadDirectory, assetID, "pic.txt"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("qa_dev", "main", "SRC_WITH_ASSET", "file", "asset://"+assetID)); err != nil {
		t.Fatal(err)
	}
	// prod/main 哨兵
	if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "PROD_SENTINEL", "text", "sentinel")); err != nil {
		t.Fatal(err)
	}

	devRowsBefore := countScopeConfigs(t, svc, "qa_dev", "main")
	prodRowsBefore := countScopeConfigs(t, svc, "qa_prod", "main")
	filesBefore := countUploadFiles(t)

	mustLock(t, svc, "qa_prod", "main", "导入迁移规则")

	// F10: JSON 导入命中 locked 目标 → 整请求 409(包括 overwrite=false)
	_, err := svc.ImportConfigsWithResult(ctx, []*common.ResourceConfig{
		buildConfig("qa_prod", "main", "IMPORT_INTO_LOCKED", "text", "x"),
	}, false)
	requireFreezeErrorType(t, err, ErrTypeScopeFrozen)

	// F09: 混合目标(一个 locked 一个正常)→ 写前整体拒绝,正常目标也不落库
	_, err = svc.ImportConfigsWithResult(ctx, []*common.ResourceConfig{
		buildConfig("qa_dev", "main", "MIXED_OK", "text", "x"),
		buildConfig("qa_prod", "main", "MIXED_BLOCKED", "text", "x"),
	}, false)
	requireFreezeErrorType(t, err, ErrTypeScopeFrozen)

	// 归档导入:包含 locked 目标与包内渠道元数据 → 整体拒绝,资源文件不变
	archive := buildFreezeTestArchive(t,
		[]map[string]any{
			{"resource_key": uuid.NewString(), "alias": "ARCH_OK", "name": "ok", "environment_key": "qa_dev", "pipeline_key": "main", "type": "text", "content": "x"},
			{"resource_key": uuid.NewString(), "alias": "ARCH_LOCKED", "name": "locked", "environment_key": "qa_prod", "pipeline_key": "main", "type": "text", "content": "x"},
		},
		map[string][]byte{"files/" + assetID + "/pic.txt": []byte("asset-bytes")},
		nil,
	)
	_, err = svc.ImportArchiveWithResult(ctx, archive, "zip", "", "", nil, true)
	requireFreezeErrorType(t, err, ErrTypeScopeFrozen)

	if got := countScopeConfigs(t, svc, "qa_dev", "main"); got != devRowsBefore {
		t.Fatalf("dev/main must be untouched by rejected import: %d != %d", got, devRowsBefore)
	}
	if got := countScopeConfigs(t, svc, "qa_prod", "main"); got != prodRowsBefore {
		t.Fatalf("prod/main must be untouched by rejected import: %d != %d", got, prodRowsBefore)
	}
	if got := countUploadFiles(t); got != filesBefore {
		t.Fatalf("rejected import must not create files: %d != %d", got, filesBefore)
	}

	// F11: 迁移目标 locked → 复制资源前拒绝,目标无新增文件
	_, err = svc.MigrateConfigs(ctx, &transfer.MigrateRequest{SourceEnvironmentKey: "qa_dev", SourcePipelineKey: "main", TargetEnvironmentKey: "qa_prod", TargetPipelineKey: "main"})
	requireFreezeErrorType(t, err, ErrTypeScopeFrozen)
	if got := countUploadFiles(t); got != filesBefore {
		t.Fatalf("blocked migration must not copy resources: %d != %d", got, filesBefore)
	}

	// 允许方向:locked 源 → unlocked 目标;源数据与资源不变
	sourceBefore := countScopeConfigs(t, svc, "qa_dev", "main")
	summary, err := svc.MigrateConfigs(ctx, &transfer.MigrateRequest{SourceEnvironmentKey: "qa_prod", SourcePipelineKey: "main", TargetEnvironmentKey: "qa_dev", TargetPipelineKey: "beta"})
	if err != nil {
		t.Fatalf("migrate from locked source must be allowed: %v", err)
	}
	if summary.Total != 1 || summary.Succeeded != 1 {
		t.Fatalf("unexpected migrate summary: %+v", summary)
	}
	if got := countScopeConfigs(t, svc, "qa_dev", "main"); got != sourceBefore {
		t.Fatalf("source scope changed during migration: %d != %d", got, sourceBefore)
	}
	prodRows, err := svc.logic.ListConfigs(ctx, "qa_prod", "main", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(prodRows) != prodRowsBefore || prodRows[0].Content != "sentinel" {
		t.Fatalf("locked source content changed: %+v", prodRows)
	}
}

// --------------------- 并发与协调时序 ---------------------

func TestFreezeConcurrencyRejectsAfterLock(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	ctx := context.Background()
	mustLock(t, svc, "qa_prod", "main", "并发拒绝")

	var wg sync.WaitGroup
	blocked := make([]bool, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "CONCURRENT_"+uuid.NewString(), "text", "x"))
			freezeErr, ok := AsFreezeError(err)
			blocked[index] = ok && freezeErr.ErrorType == ErrTypeScopeFrozen
		}(i)
	}
	wg.Wait()
	for index, wasBlocked := range blocked {
		if !wasBlocked {
			t.Fatalf("concurrent write %d was not blocked by the freeze", index)
		}
	}
	if got := countScopeConfigs(t, svc, "qa_prod", "main"); got != 0 {
		t.Fatalf("no config may be written into the locked scope: %d", got)
	}
	if got := countUploadFiles(t); got != 0 {
		t.Fatalf("no resource file may be created in the locked scope: %d", got)
	}
}

func TestFreezeGateSerializationAndBusy(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	ctx := context.Background()

	oldAcquire := writeGateAcquireTimeout
	writeGateAcquireTimeout = 300 * time.Millisecond
	defer func() { writeGateAcquireTimeout = oldAcquire }()

	entered := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- svc.withWriteGate(ctx, func(_ context.Context, _ *Logic, _ *gorm.DB) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	// 协调被旧写持有:锁版请求等待超时返回 503,且不产生虚假 locked 状态
	_, err := svc.LockFreeze(ctx, "qa_prod", "main", "并发锁版", "0", "Bearer "+testAdminToken())
	requireFreezeErrorType(t, err, ErrTypeFreezeGateBusy)

	// 普通写入同样等待超时返回 503(不静默成功)
	_, err = svc.AddConfig(ctx, buildConfig("qa_prod", "main", "GATE_BUSY", "text", "x"))
	requireFreezeErrorType(t, err, ErrTypeFreezeGateBusy)

	close(release)
	if err := <-holderDone; err != nil {
		t.Fatalf("holder write failed: %v", err)
	}

	// 连接释放后读取:协调被拒期间不得产生虚假 locked 状态
	state, err := svc.logic.freezeDAO.GetState(ctx, svc.logic.db, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	if state.State != model.FreezeStateUnlocked || state.Revision != 0 {
		t.Fatalf("busy lock attempt must not change state: %+v", state)
	}

	// 旧写完成后,锁版成功(F15:先写完再锁成功)
	locked := mustLock(t, svc, "qa_prod", "main", "旧写完成后锁版")
	if locked.Revision != 1 {
		t.Fatalf("unexpected revision after serialized lock: %+v", locked)
	}
}

func TestFreezeResourceStageTiming(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_dev", "main")
	seedFreezeScope(t, svc, "qa_prod", "main")
	ctx := context.Background()

	oldAcquire := writeGateAcquireTimeout
	writeGateAcquireTimeout = 300 * time.Millisecond
	defer func() { writeGateAcquireTimeout = oldAcquire }()

	files := map[string][]byte{}
	assets := make([]map[string]any, 0, 2)
	configs := make([]map[string]any, 0, 2)
	for i := 0; i < 2; i++ {
		assetID := "freeze-stage-asset-" + string(rune('a'+i))
		fileName := "stage-" + assetID + ".txt"
		files["files/"+assetID+"/"+fileName] = []byte("bytes-" + assetID)
		assets = append(assets, map[string]any{
			"file_id": assetID, "environment_key": "qa_dev", "pipeline_key": "main",
			"file_name": fileName, "content_type": "text/plain", "file_size": len("bytes-" + assetID),
		})
		configs = append(configs, map[string]any{
			"resource_key": uuid.NewString(), "alias": "STAGE_" + assetID, "name": assetID,
			"environment_key": "qa_dev", "pipeline_key": "main", "type": "file", "content": "asset://" + assetID,
		})
	}
	archive := buildFreezeTestArchive(t, configs, files, assets)

	var calls int32
	blocked := make(chan struct{})
	resume := make(chan struct{})
	resourceRestoreHookForTest = func(string) {
		if atomic.AddInt32(&calls, 1) == 2 {
			close(blocked)
			<-resume
		}
	}
	defer func() { resourceRestoreHookForTest = nil }()

	importDone := make(chan error, 1)
	go func() {
		_, err := svc.ImportArchiveWithResult(ctx, archive, "zip", "", "", nil, true)
		importDone <- err
	}()

	<-blocked // 资源阶段被人工阻塞:第一个资源已写入,协调仍被导入持有

	// F17:资源阶段未结束前,锁版不能提前成功
	_, err := svc.LockFreeze(ctx, "qa_prod", "main", "资源阶段阻塞时锁版", "0", "Bearer "+testAdminToken())
	requireFreezeErrorType(t, err, ErrTypeFreezeGateBusy)

	close(resume)
	if err := <-importDone; err != nil {
		t.Fatalf("import must complete after resource stage resumes: %v", err)
	}
	if got := countUploadFiles(t); got != len(files) {
		t.Fatalf("restored resources mismatch: %d != %d", got, len(files))
	}

	// 资源结果确定后,锁版成功
	if _, err := svc.LockFreeze(ctx, "qa_prod", "main", "资源完成后锁版", "0", "Bearer "+testAdminToken()); err != nil {
		t.Fatalf("lock after completed write must succeed: %v", err)
	}
}

func TestFreezeImportCancelCleansCreatedFiles(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_dev", "main")
	baseCtx := context.Background()

	oldAcquire := writeGateAcquireTimeout
	writeGateAcquireTimeout = 500 * time.Millisecond
	defer func() { writeGateAcquireTimeout = oldAcquire }()

	files := map[string][]byte{}
	assets := make([]map[string]any, 0, 2)
	configs := make([]map[string]any, 0, 2)
	for i := 0; i < 2; i++ {
		assetID := "freeze-cancel-asset-" + string(rune('a'+i))
		fileName := "cancel-" + assetID + ".txt"
		files["files/"+assetID+"/"+fileName] = []byte("cancel-bytes-" + assetID)
		assets = append(assets, map[string]any{
			"file_id": assetID, "environment_key": "qa_dev", "pipeline_key": "main",
			"file_name": fileName, "content_type": "text/plain", "file_size": len("cancel-bytes-" + assetID),
		})
		configs = append(configs, map[string]any{
			"resource_key": uuid.NewString(), "alias": "CANCEL_" + assetID, "name": assetID,
			"environment_key": "qa_dev", "pipeline_key": "main", "type": "file", "content": "asset://" + assetID,
		})
	}
	archive := buildFreezeTestArchive(t, configs, files, assets)

	var calls int32
	blocked := make(chan struct{})
	resume := make(chan struct{})
	resourceRestoreHookForTest = func(string) {
		if atomic.AddInt32(&calls, 1) == 2 {
			close(blocked)
			<-resume
		}
	}
	defer func() { resourceRestoreHookForTest = nil }()

	ctx, cancel := context.WithCancel(baseCtx)
	importDone := make(chan error, 1)
	go func() {
		_, err := svc.ImportArchiveWithResult(ctx, archive, "zip", "", "", nil, true)
		importDone <- err
	}()

	<-blocked // 第一个资源已落盘,第二个被阻塞
	cancel()
	close(resume)

	err := <-importDone
	if err == nil {
		t.Fatal("canceled import must fail")
	}
	if got := countUploadFiles(t); got != 0 {
		t.Fatalf("canceled import must clean files created by itself: %d", got)
	}
	rows := countScopeConfigs(t, svc, "qa_dev", "main")
	if rows != 0 {
		t.Fatalf("canceled import must roll back configs: %d", rows)
	}
}

// --------------------- 测试辅助 ---------------------

func buildFreezeTestArchive(t *testing.T, configs []map[string]any, files map[string][]byte, assets []map[string]any) []byte {
	t.Helper()
	if assets == nil {
		assets = []map[string]any{}
	}
	metadata := map[string]any{
		"environments":     []any{},
		"pipelines":        []any{},
		"business_configs": configs,
		"assets":           assets,
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	buf := &bytes.Buffer{}
	writer := zip.NewWriter(buf)
	entry, err := writer.Create("configs.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(payload); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func countUploadFiles(t *testing.T) int {
	t.Helper()
	root := filepath.Join(dataDirectory, uploadDirectory)
	count := 0
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if !info.IsDir() {
			count++
		}
		return nil
	})
	return count
}

func countScopeConfigs(t *testing.T, svc *Service, environmentKey, pipelineKey string) int {
	t.Helper()
	rows, err := svc.logic.ListConfigs(context.Background(), environmentKey, pipelineKey, "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

func writeUploadedFile(t *testing.T, fileID, fileName string, data []byte) {
	t.Helper()
	dir := filepath.Join(dataDirectory, uploadDirectory, fileID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
