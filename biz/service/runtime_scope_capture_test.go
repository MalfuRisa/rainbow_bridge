package service

import (
	"context"
	"strconv"
	"testing"

	plpb "github.com/yi-nology/rainbow_bridge/biz/model/pipeline"
)

func deleteAndRecreateScopeForTest(t *testing.T, svc *Service, environmentKey, pipelineKey, oldScope string) {
	t.Helper()
	ctx := adminTestContext()
	status, err := svc.FreezeStatus(ctx, environmentKey, pipelineKey)
	if err != nil {
		t.Fatal(err)
	}
	if status.State == "locked" {
		if _, err := svc.UnlockFreeze(ctx, environmentKey, pipelineKey, "scope capture race",
			strconv.FormatInt(status.Revision, 10)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.DeletePipelineWithIdentity(ctx, environmentKey, pipelineKey, oldScope); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddPipeline(ctx, environmentKey, &plpb.Pipeline{
		PipelineKey: pipelineKey, PipelineName: "recreated", IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeReleaseOldPinNeverReadsRecreatedScope(t *testing.T) {
	f := newReleaseFixture(t)
	created, err := f.create(t, "v1", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.activate(t, "v1", "0", ""); err != nil {
		t.Fatal(err)
	}
	oldScope := scopeIDFor(t, f.svc, "qa_prod", "main")
	releaseRuntimeScopeHookForTest = func(scopeID string) {
		if scopeID != oldScope {
			return
		}
		releaseRuntimeScopeHookForTest = nil
		deleteAndRecreateScopeForTest(t, f.svc, "qa_prod", "main", oldScope)
	}
	t.Cleanup(func() { releaseRuntimeScopeHookForTest = nil })

	result, err := f.svc.RuntimeReleaseWithScope(adminTestContext(), "qa_prod", "main", nil, oldScope)
	if result != nil {
		t.Fatalf("old scope pin returned data after recreate: %+v", result)
	}
	requireReleaseErrorType(t, err, ErrTypeReleaseNotActive)

	newScope := scopeIDFor(t, f.svc, "qa_prod", "main")
	if newScope == oldScope {
		t.Fatal("recreated scope reused old identity")
	}
	if _, err := f.svc.RuntimeRelease(adminTestContext(), "qa_prod", "main", nil); err == nil ||
		!isReleaseType(err, ErrTypeReleaseNotActive) {
		t.Fatalf("new generation should have no active release: %v", err)
	}
	if created.Meta.ScopeID != oldScope {
		t.Fatal("fixture version did not belong to the old scope")
	}
}

func TestActiveStaticOldPinNeverBuildsRecreatedScope(t *testing.T) {
	f := newReleaseFixture(t)
	if _, err := f.create(t, "v1", "1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.activate(t, "v1", "0", ""); err != nil {
		t.Fatal(err)
	}
	oldScope := scopeIDFor(t, f.svc, "qa_prod", "main")
	releaseRuntimeScopeHookForTest = func(scopeID string) {
		if scopeID != oldScope {
			return
		}
		releaseRuntimeScopeHookForTest = nil
		deleteAndRecreateScopeForTest(t, f.svc, "qa_prod", "main", oldScope)
	}
	t.Cleanup(func() { releaseRuntimeScopeHookForTest = nil })

	result, err := f.svc.ExportActiveReleasePackageWithScope(adminTestContext(), "qa_prod", "main", oldScope)
	if result != nil {
		t.Fatalf("old scope pin built a package after recreate: %+v", result)
	}
	requireReleaseErrorType(t, err, ErrTypeReleaseNotActive)
}

func TestRuntimeReleaseDAOFailureIsNotScopeNotFound(t *testing.T) {
	f := newReleaseFixture(t)
	ctx, cancel := context.WithCancel(adminTestContext())
	cancel()
	_, err := f.svc.RuntimeReleaseWithScope(ctx, "qa_prod", "main", nil, "")
	if err == nil {
		t.Fatal("canceled context must not return a successful read")
	}
	if _, ok := AsScopeError(err); ok {
		t.Fatalf("internal failure was misclassified as a scope error: %v", err)
	}
}

func TestActiveStaticOldInvalidPinIs400(t *testing.T) {
	f := newReleaseFixture(t)
	if _, err := f.create(t, "v1", "1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.activate(t, "v1", "0", ""); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.ExportActiveReleasePackageWithScope(adminTestContext(), "qa_prod", "main", "not-a-uuid")
	requireScopeErrorType(t, err, ErrTypeScopeInvalidRequest)
}
