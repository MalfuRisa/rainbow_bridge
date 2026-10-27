package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	envpb "github.com/yi-nology/rainbow_bridge/biz/model/environment"
	plpb "github.com/yi-nology/rainbow_bridge/biz/model/pipeline"
)

func TestDeletePipelineAllowsVersionsAndSameKeyRebuildIsIsolated(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	ctx := adminTestContext()

	created, err := svc.AddConfig(ctx, &common.ResourceConfig{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Alias: "site",
		Content: "old generation", Type: "text",
	})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := resolveScopeByKeys(ctx, svc.logic, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	oldScopeID := scope.ScopeID
	asset := &model.Asset{
		FileID: uuid.NewString(), ScopeID: oldScopeID, EnvironmentKey: "qa_prod",
		PipelineKey: "main", FileName: "a.txt", ContentType: "text/plain",
	}
	if err := svc.logic.db.Create(asset).Error; err != nil {
		t.Fatal(err)
	}
	payload := `{"scope":{"environment_key":"qa_prod","pipeline_key":"main"},"configs":[]}`
	digest := payloadDigest(payload)
	version := &model.ConfigReleaseVersion{
		SnapshotID: uuid.NewString(), ScopeID: oldScopeID,
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
		CreationRequestID: uuid.NewString(), RequestFingerprint: "fingerprint",
		Payload: payload, PayloadSha256: digest, ManifestSha256: digest,
	}
	if err := svc.logic.db.Create(version).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.logic.db.Create(&model.ConfigReleaseHead{
		ScopeID: oldScopeID, EnvironmentKey: "qa_prod", PipelineKey: "main",
		ActiveSnapshotID: version.SnapshotID,
	}).Error; err != nil {
		t.Fatal(err)
	}

	result, err := svc.DeletePipelineWithIdentity(ctx, "qa_prod", "main", oldScopeID)
	if err != nil {
		t.Fatalf("unlocked scope with versions must be deletable: %v", err)
	}
	if result.Versions != 1 || result.Configs != 1 {
		t.Fatalf("unexpected affected counts: %+v", result)
	}
	if _, err := svc.GetConfigDetail(ctx, "qa_prod", "main", created.ResourceKey); err == nil {
		t.Fatal("config detail must not resolve a deleted scope")
	}
	if _, _, err := svc.GetAssetFile(ctx, asset.FileID); err == nil {
		t.Fatal("old asset must not be downloadable after scope deletion")
	}
	var headCount int64
	if err := svc.logic.db.Model(&model.ConfigReleaseHead{}).
		Where("scope_id = ? AND active_snapshot_id <> ''", oldScopeID).Count(&headCount).Error; err != nil {
		t.Fatal(err)
	}
	if headCount != 0 {
		t.Fatal("active pointer must be cleared on scope deletion")
	}

	// Recreate the same business key; it receives a fresh identity and empty data.
	if err := svc.AddPipeline(ctx, "qa_prod", &plpb.Pipeline{
		PipelineKey: "main", PipelineName: "main again", IsActive: true,
	}); err != nil {
		t.Fatalf("same-key rebuild: %v", err)
	}
	newScope, err := resolveScopeByKeys(ctx, svc.logic, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	if newScope.ScopeID == oldScopeID {
		t.Fatal("recreated scope reused the old identity")
	}
	configs, err := svc.ListConfigs(ctx, "qa_prod", "main", "", "", "", false)
	if err != nil || len(configs) != 0 {
		t.Fatalf("new generation must be empty: %v %+v", err, configs)
	}
	page, err := svc.ReleaseVersions(ctx, "qa_prod", "main", 10, "")
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("new generation must not see old versions: %v %+v", err, page)
	}
	if _, err := svc.AddConfigWithIdentity(ctx, &common.ResourceConfig{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Alias: "stale",
		Content: "stale page", Type: "text",
	}, oldScopeID); err == nil || !isScopeType(err, ErrTypeScopeDeleted) {
		t.Fatalf("stale write must be rejected with SCOPE_DELETED, got %v", err)
	}
	if _, err := svc.AddConfigWithIdentity(ctx, &common.ResourceConfig{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Alias: "fresh",
		Content: "fresh", Type: "text",
	}, newScope.ScopeID); err != nil {
		t.Fatalf("current generation write must succeed: %v", err)
	}
}

func TestDeleteEnvironmentRejectsAnyLockedLivePipeline(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	seedFreezeScope(t, svc, "qa_prod", "extra")
	ctx := adminTestContext()
	if _, err := svc.LockFreeze(ctx, "qa_prod", "extra", "locked", "0"); err != nil {
		t.Fatal(err)
	}
	env, err := resolveEnvironmentByKey(ctx, svc.logic, "qa_prod")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeleteEnvironmentWithIdentity(ctx, "qa_prod", env.EnvironmentID); err == nil || !isScopeFrozen(err) {
		t.Fatalf("locked live pipeline must block environment deletion, got %v", err)
	}
	if _, err := svc.GetPipeline(ctx, "qa_prod", "main"); err != nil {
		t.Fatal("snapshot must remain unchanged after refused delete")
	}
	if _, err := svc.UnlockFreeze(ctx, "qa_prod", "extra", "unlock", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeleteEnvironmentWithIdentity(ctx, "qa_prod", env.EnvironmentID); err != nil {
		t.Fatalf("unlocked environment delete: %v", err)
	}
}

func TestDeletedDefaultIsNotResurrectedOnRestart(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	ensureTestAdminAccount(t, svc)
	ctx := adminTestContext()
	if err := EnsureSystemDefaults(ctx, svc.logic.db); err != nil {
		t.Fatal(err)
	}
	env, err := resolveEnvironmentByKey(ctx, svc.logic, DefaultEnvironmentKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeleteEnvironmentWithIdentity(ctx, DefaultEnvironmentKey, env.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSystemDefaults(ctx, svc.logic.db); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := svc.logic.db.Unscoped().Model(&model.Environment{}).
		Where("environment_key = ?", DefaultEnvironmentKey).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("deleted default must not be recreated: %d rows", count)
	}
	if _, err := svc.GetEnvironment(ctx, DefaultEnvironmentKey); err == nil {
		t.Fatal("deleted default must stay invisible")
	}
	if err := svc.AddEnvironment(ctx, &envpb.Environment{
		EnvironmentKey: DefaultEnvironmentKey, EnvironmentName: "new", IsActive: true,
	}); err != nil {
		t.Fatalf("explicit same-key rebuild must work: %v", err)
	}
}

func isScopeType(err error, errorType string) bool {
	scopeErr, ok := AsScopeError(err)
	return ok && scopeErr.ErrorType == errorType
}

func isScopeFrozen(err error) bool {
	freezeErr, ok := AsFreezeError(err)
	return ok && freezeErr.ErrorType == ErrTypeScopeFrozen
}

var _ = time.Now
