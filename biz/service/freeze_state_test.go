package service

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	envpb "github.com/yi-nology/rainbow_bridge/biz/model/environment"
	plpb "github.com/yi-nology/rainbow_bridge/biz/model/pipeline"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// --------------------- 测试基座 ---------------------

// newFreezeService 打开独立的文件型 SQLite(与生产本地路径一致,含 busy_timeout)并完成冻结引导。
func newFreezeService(t *testing.T) *Service {
	t.Helper()
	// 资源相对路径(data/uploads)隔离到临时目录,避免写进仓库
	t.Chdir(t.TempDir())
	dbPath := filepath.Join(t.TempDir(), "freeze-"+uuid.NewString()+".db")
	conn, err := gorm.Open(
		sqlite.Open(dbPath+"?_pragma=busy_timeout(10000)"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := conn.AutoMigrate(
		&model.Environment{}, &model.Pipeline{}, &model.Config{}, &model.Asset{},
		&model.ConfigFreezeState{}, &model.ConfigFreezeEvent{}, &model.ConfigWriteGate{},
		&model.ConfigReleaseVersion{}, &model.ConfigReleaseAsset{}, &model.ConfigReleaseHead{}, &model.ConfigReleaseEvent{},
		&model.ReleaseStorageAttempt{},
		&model.ScopeLifecycleEvent{},
		&model.AuthUser{}, &model.AuthSession{},
	); err != nil {
		t.Fatal(err)
	}
	// 与生产 SQLite 一致的单连接池:并发写通过连接等待 + 写协调串行化
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	svc := NewService(conn, nil, "/rainbow-bridge", nil)
	if err := svc.EnsureFreezeBootstrap(adminTestContext()); err != nil {
		t.Fatalf("freeze bootstrap: %v", err)
	}
	return svc
}

func seedFreezeScope(t *testing.T, svc *Service, environmentKey, pipelineKey string) {
	t.Helper()
	ctx := adminTestContext()
	if err := svc.AddEnvironment(ctx, &envpb.Environment{
		EnvironmentKey:  environmentKey,
		EnvironmentName: environmentKey,
		IsActive:        true,
	}); err != nil && !errors.Is(err, ErrEnvironmentKeyExists) {
		t.Fatalf("add environment %s: %v", environmentKey, err)
	}
	if pipelineKey == "default" {
		return
	}
	if err := svc.AddPipeline(ctx, environmentKey, &plpb.Pipeline{
		PipelineKey:  pipelineKey,
		PipelineName: pipelineKey,
		IsActive:     true,
	}); err != nil && !errors.Is(err, ErrPipelineKeyExists) {
		t.Fatalf("add pipeline %s/%s: %v", environmentKey, pipelineKey, err)
	}
}

func requireScopeErrorType(t *testing.T, err error, errorType string) *ScopeError {
	t.Helper()
	scopeErr, ok := AsScopeError(err)
	if !ok || scopeErr.ErrorType != errorType {
		t.Fatalf("expected scope error %s, got %v", errorType, err)
	}
	return scopeErr
}

func requireFreezeErrorType(t *testing.T, err error, errorType string) *FreezeError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected freeze error %s, got nil", errorType)
	}
	freezeErr, ok := AsFreezeError(err)
	if !ok {
		t.Fatalf("expected freeze error %s, got %v", errorType, err)
	}
	if freezeErr.ErrorType != errorType {
		t.Fatalf("expected error type %s, got %s (%s)", errorType, freezeErr.ErrorType, freezeErr.Message)
	}
	return freezeErr
}

func testAdminToken() string { return "unit-test-admin-token-0123456789abcdef" }

func mustLock(t *testing.T, svc *Service, environmentKey, pipelineKey, reason string) *FreezeStatusData {
	t.Helper()
	status, err := svc.LockFreeze(adminTestContext(), environmentKey, pipelineKey, reason, "0")
	if err != nil {
		t.Fatalf("lock %s/%s: %v", environmentKey, pipelineKey, err)
	}
	return status
}

// --------------------- 状态机 / 凭据 / 预检 ---------------------

func TestFreezeLifecycleAndHistory(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	ctx := adminTestContext()

	initial, err := svc.FreezeStatus(ctx, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	if initial.State != model.FreezeStateUnlocked || initial.Revision != 0 {
		t.Fatalf("unexpected initial status: %+v", initial)
	}
	if initial.AdminConfigured {
		t.Fatal("admin_configured must be false before any admin account exists")
	}
	ensureTestAdminAccount(t, svc)

	locked := mustLock(t, svc, "qa_prod", "main", "上线前验收")
	if locked.State != model.FreezeStateLocked || locked.Revision != 1 || locked.LastChangedBy != "admin" {
		t.Fatalf("unexpected locked status: %+v", locked)
	}
	if locked.LastChangedAt == "" || locked.LastReason != "上线前验收" {
		t.Fatalf("missing change metadata: %+v", locked)
	}

	// 再次锁定:目标状态已是当前状态 → 409 STATE_UNCHANGED,且不追加虚假事件
	_, err = svc.LockFreeze(ctx, "qa_prod", "main", "重复锁定", "1")
	requireFreezeErrorType(t, err, ErrTypeFreezeStateUnchanged)

	// 过期 revision 解锁 → 409 REVISION_CONFLICT 且带最新状态
	conflict := func() *FreezeError {
		_, err := svc.UnlockFreeze(ctx, "qa_prod", "main", "过期解锁", "0")
		return requireFreezeErrorType(t, err, ErrTypeFreezeRevisionConflict)
	}()
	if conflict.LatestState != model.FreezeStateLocked || conflict.LatestRevision != 1 {
		t.Fatalf("conflict payload missing latest state: %+v", conflict)
	}

	unlocked, err := svc.UnlockFreeze(ctx, "qa_prod", "main", "验收调整", "1")
	if err != nil {
		t.Fatal(err)
	}
	if unlocked.State != model.FreezeStateUnlocked || unlocked.Revision != 2 {
		t.Fatalf("unexpected unlocked status: %+v", unlocked)
	}

	history, err := svc.FreezeHistory(ctx, "qa_prod", "main", 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 2 {
		t.Fatalf("expected exactly 2 events (lock,unlock), got %d", len(history.Items))
	}
	if history.Items[0].EventType != FreezeEventUnlock || history.Items[0].Revision != 2 {
		t.Fatalf("history not ordered desc by event id: %+v", history.Items[0])
	}
	if history.Items[1].EventType != FreezeEventLock || history.Items[1].Revision != 1 {
		t.Fatalf("unexpected lock event: %+v", history.Items[1])
	}
	if history.HasMore {
		t.Fatal("unexpected has_more for 2 events")
	}

	// 分页:limit=1 时 has_more 与游标
	page, err := svc.FreezeHistory(ctx, "qa_prod", "main", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || !page.HasMore || page.NextBeforeEventID == 0 {
		t.Fatalf("unexpected pagination: %+v", page)
	}
	next, err := svc.FreezeHistory(ctx, "qa_prod", "main", 1, formatUint(page.NextBeforeEventID))
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 1 || next.Items[0].EventID == page.Items[0].EventID {
		t.Fatalf("cursor pagination unstable: %+v", next)
	}
}

func TestFreezeAdminCredentialAndValidation(t *testing.T) {
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	ctx := adminTestContext()

	// 无可信身份:状态可读但管理操作 401,且不产生任何副作用
	status, err := svc.FreezeStatus(ctx, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	if status.AdminConfigured {
		t.Fatal("admin_configured must be false before any admin account exists")
	}
	_, err = svc.LockFreeze(context.Background(), "qa_prod", "main", "原因", "0")
	requireAuthErrorType(t, err, ErrTypeAuthRequired)

	// 存在管理员账号后 admin_configured 为真,旧式凭据字符串不再被接受
	ensureTestAdminAccount(t, svc)
	configured, err := svc.FreezeStatus(ctx, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	if !configured.AdminConfigured {
		t.Fatal("admin_configured must be true once an admin account exists")
	}

	// 超长原因 → 400;空原因在独立的选填契约测试覆盖。
	_, err = svc.LockFreeze(ctx, "qa_prod", "main", string(make([]byte, 501)), "0")
	requireFreezeErrorType(t, err, ErrTypeFreezeInvalidReason)

	// 非法 revision → 400
	_, err = svc.LockFreeze(ctx, "qa_prod", "main", "原因", "abc")
	requireFreezeErrorType(t, err, ErrTypeFreezeInvalidRevision)

	// 不存在的范围 → 404 SCOPE_NOT_FOUND
	_, err = svc.LockFreeze(ctx, "no_such_env", "main", "原因", "0")
	requireScopeErrorType(t, err, ErrTypeScopeNotFound)
	_, err = svc.FreezeStatus(ctx, "qa_prod", "no_such_pipeline")
	requireScopeErrorType(t, err, ErrTypeScopeNotFound)

	// 全程无状态变化与伪审计
	current, err := svc.FreezeStatus(ctx, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	if current.State != model.FreezeStateUnlocked || current.Revision != 0 {
		t.Fatalf("unexpected state after rejected operations: %+v", current)
	}
	history, _ := svc.FreezeHistory(ctx, "qa_prod", "main", 20, "")
	if len(history.Items) != 0 {
		t.Fatalf("rejected operations must not create audit events: %+v", history.Items)
	}
}

func TestFreezeLockPrecheck(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	seedFreezeScope(t, svc, "qa_dev", "main")
	ctx := adminTestContext()

	// 跨范围引用:qa_prod/main 的配置引用 qa_dev/main 的资源
	crossScopeAsset := &model.Asset{
		FileID:         "cross-scope-asset",
		EnvironmentKey: "qa_dev",
		PipelineKey:    "main",
		FileName:       "cross.txt",
		ContentType:    "text/plain",
		FileSize:       1,
		Path:           filepath.Join(uploadDirectory, "cross-scope-asset", "cross.txt"),
	}
	if err := svc.logic.CreateAsset(ctx, crossScopeAsset); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "CROSS_REF", "image", "asset://cross-scope-asset")); err != nil {
		t.Fatal(err)
	}
	// 缺失元数据
	if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "MISSING_REF", "file", "asset://missing-asset-id")); err != nil {
		t.Fatal(err)
	}
	// 外部 URL:允许锁定,但要登记提示
	if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "EXTERNAL_URL", "image", "https://example.com/pic.png")); err != nil {
		t.Fatal(err)
	}
	// 无资源的文本配置
	if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "PLAIN_TEXT", "text", "hello")); err != nil {
		t.Fatal(err)
	}
	// 资源存在但文件缺失
	if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "FILE_MISSING", "image", "asset://cross-scope-asset-unused")); err != nil {
		t.Fatal(err)
	}
	missingFileAsset := &model.Asset{
		FileID:         "cross-scope-asset-unused",
		EnvironmentKey: "qa_prod",
		PipelineKey:    "main",
		FileName:       "ghost.txt",
		ContentType:    "text/plain",
		FileSize:       1,
		Path:           filepath.Join(uploadDirectory, "cross-scope-asset-unused", "ghost.txt"),
	}
	if err := svc.logic.CreateAsset(ctx, missingFileAsset); err != nil {
		t.Fatal(err)
	}

	_, err := svc.LockFreeze(ctx, "qa_prod", "main", "预检失败样本", "0")
	freezeErr := requireFreezeErrorType(t, err, ErrTypeFreezeResourcePrecheck)
	reasons := map[string]bool{}
	for _, issue := range freezeErr.Issues {
		reasons[issue.Reason] = true
	}
	if !reasons["cross_scope_reference"] || !reasons["missing_metadata"] || !reasons["file_missing"] {
		t.Fatalf("expected cross_scope/missing_metadata/file_missing issues, got %+v", freezeErr.Issues)
	}
	if len(freezeErr.ExternalReferences) != 1 || freezeErr.ExternalReferences[0] != "https://example.com/pic.png" {
		t.Fatalf("external reference should be reported: %+v", freezeErr.ExternalReferences)
	}

	// 处理问题后重新锁定:移除跨范围/缺失引用,保留外部 URL 与无资源配置
	for _, alias := range []string{"CROSS_REF", "MISSING_REF", "FILE_MISSING"} {
		list, err := svc.logic.ListConfigs(ctx, "qa_prod", "main", "", "", "", false)
		if err != nil {
			t.Fatal(err)
		}
		for _, cfg := range list {
			if cfg.Alias == alias {
				if err := svc.DeleteConfig(ctx, "qa_prod", "main", cfg.ResourceKey); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	locked, err := svc.LockFreeze(ctx, "qa_prod", "main", "外部引用允许锁定", "0")
	if err != nil {
		t.Fatalf("lock should succeed after resolving managed resource issues: %v", err)
	}
	if locked.State != model.FreezeStateLocked {
		t.Fatalf("unexpected state: %+v", locked)
	}

	// 无配置的已存在渠道也可以锁
	emptyStatus, err := svc.LockFreeze(ctx, "qa_dev", "main", "空渠道可锁", "0")
	if err != nil || emptyStatus.State != model.FreezeStateLocked {
		t.Fatalf("empty pipeline lock failed: %v %+v", err, emptyStatus)
	}
}

func TestFreezeAuditFailureRollsBackState(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	ctx := adminTestContext()

	// 审计表缺失:状态迁移必须整体回滚,不允许"有锁无事件"
	if err := svc.logic.db.Exec("DROP TABLE config_freeze_event").Error; err != nil {
		t.Fatal(err)
	}
	_, err := svc.LockFreeze(ctx, "qa_prod", "main", "审计失败", "0")
	if err == nil {
		t.Fatal("lock must fail when audit write fails")
	}
	state, err := svc.logic.freezeDAO.GetState(ctx, svc.logic.db, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	if state.State != model.FreezeStateUnlocked || state.Revision != 0 {
		t.Fatalf("state must roll back when audit write fails: %+v", state)
	}
}

// --------------------- 小工具 ---------------------

func formatUint(value uint) string {
	return strconv.FormatUint(uint64(value), 10)
}

func buildConfig(environmentKey, pipelineKey, alias, typ, content string) *common.ResourceConfig {
	return &common.ResourceConfig{
		ResourceKey:    uuid.NewString(),
		Alias:          alias,
		Name:           alias,
		EnvironmentKey: environmentKey,
		PipelineKey:    pipelineKey,
		Type:           typ,
		Content:        content,
	}
}

// adminTestContext 注入可信管理员身份:服务层不再接受请求自报的凭据。
func adminTestContext() context.Context {
	return WithAdminIdentity(context.Background(), AdminIdentity{UserID: 1, Username: "admin", Role: "admin"})
}

// requireAuthErrorType 断言错误类型并返回 AuthError。
func requireAuthErrorType(t *testing.T, err error, errorType string) *AuthError {
	t.Helper()
	authErr, ok := AsAuthError(err)
	if !ok {
		t.Fatalf("expected auth error %s, got %v", errorType, err)
	}
	if authErr.ErrorType != errorType {
		t.Fatalf("error type = %s, want %s (%v)", authErr.ErrorType, errorType, authErr)
	}
	return authErr
}

// ensureTestAdminAccount 建立测试管理员账号(admin_configured 与新身份契约的前提)。
func ensureTestAdminAccount(t *testing.T, svc *Service) {
	t.Helper()
	t.Setenv(AdminInitialPasswordEnv, "unit test admin passphrase")
	t.Setenv(AdminUsernameEnv, "admin")
	if err := svc.EnsureAuthBootstrap(context.Background()); err != nil {
		t.Fatalf("ensure test admin: %v", err)
	}
}
