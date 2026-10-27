package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	plpb "github.com/yi-nology/rainbow_bridge/biz/model/pipeline"
)

func scopeIDFor(t *testing.T, svc *Service, environmentKey, pipelineKey string) string {
	t.Helper()
	scope, err := resolveScopeByKeys(context.Background(), svc.logic, environmentKey, pipelineKey)
	if err != nil {
		t.Fatal(err)
	}
	return scope.ScopeID
}

func setHeadBoundary(t *testing.T, svc *Service, scopeID string, revision int64, activeSnapshot string) {
	t.Helper()
	now := time.Now().UTC()
	head := &model.ConfigReleaseHead{
		ScopeID: scopeID, EnvironmentKey: "qa_prod", PipelineKey: "main",
		ActiveSnapshotID: activeSnapshot, Revision: revision, ChangedAt: &now,
		ChangedBy: "fixture", ChangedReason: "boundary",
	}
	if err := svc.logic.db.Create(head).Error; err != nil {
		t.Fatal(err)
	}
}

func releaseReadCounts(t *testing.T, svc *Service, scopeID string) (int64, int64, int64) {
	t.Helper()
	var heads, versions, events int64
	if err := svc.logic.db.Model(&model.ConfigReleaseHead{}).Where("scope_id = ?", scopeID).Count(&heads).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.logic.db.Model(&model.ConfigReleaseVersion{}).Where("scope_id = ?", scopeID).Count(&versions).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.logic.db.Model(&model.ConfigReleaseEvent{}).Where("scope_id = ?", scopeID).Count(&events).Error; err != nil {
		t.Fatal(err)
	}
	return heads, versions, events
}

func TestReadReleaseActiveStateNoHeadAndRevisionZero(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()
	scopeID := scopeIDFor(t, svc, "qa_prod", "main")
	beforeHeads, beforeVersions, beforeEvents := releaseReadCounts(t, svc, scopeID)

	state, err := svc.ReadReleaseActiveState(ctx, "qa_prod", "main", scopeID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ScopeID != scopeID || state.Revision != 0 || state.Active != nil {
		t.Fatalf("no-head state = %+v", state)
	}

	if err := svc.logic.db.Create(&model.ConfigReleaseHead{
		ScopeID: scopeID, EnvironmentKey: "qa_prod", PipelineKey: "main", Revision: 0,
	}).Error; err != nil {
		t.Fatal(err)
	}
	state, err = svc.ReadReleaseActiveState(ctx, "qa_prod", "main", "")
	if err != nil || state.Revision != 0 || state.Active != nil {
		t.Fatalf("zero revision state = %+v err=%v", state, err)
	}
	afterHeads, afterVersions, afterEvents := releaseReadCounts(t, svc, scopeID)
	if beforeHeads != afterHeads-1 || beforeVersions != afterVersions || beforeEvents != afterEvents {
		t.Fatalf("queries wrote state: heads %d->%d versions %d->%d events %d->%d",
			beforeHeads, afterHeads, beforeVersions, afterVersions, beforeEvents, afterEvents)
	}
}

func TestReadReleaseActiveStateEmptyPointerKeepsNonZeroRevision(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	scopeID := scopeIDFor(t, svc, "qa_prod", "main")
	setHeadBoundary(t, svc, scopeID, 7, "")

	state, err := svc.ReadReleaseActiveState(adminTestContext(), "qa_prod", "main", scopeID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Active != nil || state.Revision != 7 || state.ScopeID != scopeID {
		t.Fatalf("boundary state = %+v", state)
	}
}

func TestReadReleaseActiveStateBoundaryRevisionCanCompareAndActivate(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()
	scopeID := scopeIDFor(t, svc, "qa_prod", "main")
	setHeadBoundary(t, svc, scopeID, 7, "")

	created, err := svc.CreateRelease(ctx, ReleaseCreateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", AutoOnly: true,
		ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString(), Reason: "boundary candidate",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := svc.ReadReleaseActiveState(ctx, "qa_prod", "main", "")
	if err != nil || state.Revision != 7 || state.Active != nil {
		t.Fatalf("state after create = %+v err=%v", state, err)
	}
	input := compareFixtureInput(t, svc, created.Meta.Version, created.Meta.SnapshotID, "7")
	input.ExpectedActiveSnapshotID = ""
	compared, err := svc.CompareRelease(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if compared.ActiveRevision != 7 || compared.Baseline != nil {
		t.Fatalf("boundary compare = %+v", compared)
	}
	if _, err := svc.ActivateRelease(ctx, ReleaseActivateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: created.Meta.Version,
		ExpectedActiveRevisionRaw: "7", RequestID: uuid.NewString(), Reason: "boundary activate",
	}); err != nil {
		t.Fatal(err)
	}
	state, err = svc.ReadReleaseActiveState(ctx, "qa_prod", "main", "")
	if err != nil || state.Revision != 8 || state.Active == nil ||
		state.Active.Version != created.Meta.Version || state.Active.Revision != 8 {
		t.Fatalf("state after activate = %+v err=%v", state, err)
	}
}

func TestReadReleaseActiveStateLargeRevisionIsNotFloat64(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	scopeID := scopeIDFor(t, svc, "qa_prod", "main")
	const large int64 = 9007199254740993
	setHeadBoundary(t, svc, scopeID, large, "")
	state, err := svc.ReadReleaseActiveState(adminTestContext(), "qa_prod", "main", "")
	if err != nil || state.Revision != large || state.Active != nil {
		t.Fatalf("large revision state = %+v err=%v", state, err)
	}
}

func TestReadReleaseActiveStateCASAfterABAReturnsConflict(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	v1, err := fixture.create(t, "v1.0", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := fixture.create(t, "v2.0", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.activate(t, v1.Meta.Version, "0", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.activate(t, v2.Meta.Version, "1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.activate(t, v1.Meta.Version, "2", ""); err != nil {
		t.Fatal(err)
	}
	state, err := svc.ReadReleaseActiveState(adminTestContext(), "qa_prod", "main", "")
	if err != nil || state.Revision != 3 || state.Active == nil || state.Active.Version != v1.Meta.Version {
		t.Fatalf("A-B-A state = %+v err=%v", state, err)
	}
	_, err = fixture.activate(t, v2.Meta.Version, "0", "")
	requireReleaseErrorType(t, err, ErrTypeReleaseActiveRevision)
}

func TestReadReleaseActiveStateScopeErrors(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()
	seedFreezeScope(t, svc, "qa_prod", "beta")
	betaScope := scopeIDFor(t, svc, "qa_prod", "beta")

	_, err := svc.ReadReleaseActiveState(ctx, "missing_env", "main", "")
	requireScopeErrorType(t, err, ErrTypeScopeNotFound)

	_, err = svc.ReadReleaseActiveState(ctx, "qa_prod", "main", betaScope)
	requireScopeErrorType(t, err, ErrTypeScopeIdentityConflict)

	_, err = svc.ReadReleaseActiveState(ctx, "qa_prod", "main", uuid.NewString())
	requireScopeErrorType(t, err, ErrTypeScopeNotFound)

	_, err = svc.ReadReleaseActiveState(ctx, "qa_prod", "main", "   ")
	requireScopeErrorType(t, err, ErrTypeScopeNotFound)

	seedFreezeScope(t, svc, "qa_scratch", "default")
	scratchScope := scopeIDFor(t, svc, "qa_scratch", "default")
	if _, err := svc.DeletePipelineWithIdentity(ctx, "qa_scratch", "default", scratchScope); err != nil {
		t.Fatal(err)
	}
	_, err = svc.ReadReleaseActiveState(ctx, "qa_scratch", "default", "")
	requireScopeErrorType(t, err, ErrTypeScopeDeleted)

	if err := svc.AddPipeline(ctx, "qa_scratch", &plpb.Pipeline{
		PipelineKey: "default", PipelineName: "recreated", IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.ReadReleaseActiveState(ctx, "qa_scratch", "default", scratchScope)
	requireScopeErrorType(t, err, ErrTypeScopeDeleted)
}

func TestReadReleaseActiveStateIncompleteAndInternal(t *testing.T) {
	t.Run("missing version", func(t *testing.T) {
		fixture := newReleaseFixture(t)
		scopeID := scopeIDFor(t, fixture.svc, "qa_prod", "main")
		setHeadBoundary(t, fixture.svc, scopeID, 1, uuid.NewString())
		_, err := fixture.svc.ReadReleaseActiveState(adminTestContext(), "qa_prod", "main", "")
		requireReleaseErrorType(t, err, ErrTypeReleaseIncomplete)
	})
	t.Run("deleted version", func(t *testing.T) {
		fixture := newReleaseFixture(t)
		created, err := fixture.create(t, "v1.0", "1", "")
		if err != nil {
			t.Fatal(err)
		}
		scopeID := scopeIDFor(t, fixture.svc, "qa_prod", "main")
		now := time.Now().UTC()
		if err := fixture.svc.logic.db.Create(&model.ConfigReleaseHead{
			ScopeID: scopeID, EnvironmentKey: "qa_prod", PipelineKey: "main",
			ActiveSnapshotID: created.Meta.SnapshotID, Revision: 1,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := fixture.svc.logic.db.Model(&model.ConfigReleaseVersion{}).
			Where("snapshot_id = ?", created.Meta.SnapshotID).
			Updates(map[string]any{"deleted_at": now, "deleted_by": "fixture"}).Error; err != nil {
			t.Fatal(err)
		}
		_, err = fixture.svc.ReadReleaseActiveState(adminTestContext(), "qa_prod", "main", "")
		requireReleaseErrorType(t, err, ErrTypeReleaseIncomplete)
	})
	t.Run("cross scope version", func(t *testing.T) {
		fixture := newReleaseFixture(t)
		created, err := fixture.create(t, "v1.0", "1", "")
		if err != nil {
			t.Fatal(err)
		}
		scopeID := scopeIDFor(t, fixture.svc, "qa_prod", "main")
		if err := fixture.svc.logic.db.Create(&model.ConfigReleaseHead{
			ScopeID: scopeID, EnvironmentKey: "qa_prod", PipelineKey: "main",
			ActiveSnapshotID: created.Meta.SnapshotID, Revision: 1,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := fixture.svc.logic.db.Model(&model.ConfigReleaseVersion{}).
			Where("snapshot_id = ?", created.Meta.SnapshotID).
			UpdateColumn("scope_id", "other-scope").Error; err != nil {
			t.Fatal(err)
		}
		_, err = fixture.svc.ReadReleaseActiveState(adminTestContext(), "qa_prod", "main", "")
		requireReleaseErrorType(t, err, ErrTypeReleaseIncomplete)
	})
	t.Run("negative revision", func(t *testing.T) {
		fixture := newReleaseFixture(t)
		scopeID := scopeIDFor(t, fixture.svc, "qa_prod", "main")
		setHeadBoundary(t, fixture.svc, scopeID, -1, "")
		_, err := fixture.svc.ReadReleaseActiveState(adminTestContext(), "qa_prod", "main", "")
		requireReleaseErrorType(t, err, ErrTypeReleaseIncomplete)
	})
	t.Run("dao failure", func(t *testing.T) {
		fixture := newReleaseFixture(t)
		ctx, cancel := context.WithCancel(adminTestContext())
		cancel()
		_, err := fixture.svc.ReadReleaseActiveState(ctx, "qa_prod", "main", "")
		requireReleaseErrorType(t, err, ErrTypeReleaseInternalError)
	})
	t.Run("max int64", func(t *testing.T) {
		fixture := newReleaseFixture(t)
		scopeID := scopeIDFor(t, fixture.svc, "qa_prod", "main")
		setHeadBoundary(t, fixture.svc, scopeID, math.MaxInt64, "")
		state, err := fixture.svc.ReadReleaseActiveState(adminTestContext(), "qa_prod", "main", "")
		if err != nil || state.Revision != math.MaxInt64 {
			t.Fatalf("max state = %+v err=%v", state, err)
		}
	})
}

func TestReadReleaseActiveStateCreateDoesNotAdvanceRevision(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	state, err := svc.ReadReleaseActiveState(adminTestContext(), "qa_prod", "main", "")
	if err != nil || state.Revision != 0 || state.Active != nil {
		t.Fatalf("initial state = %+v err=%v", state, err)
	}
	requestID := uuid.NewString()
	created, err := svc.CreateRelease(adminTestContext(), ReleaseCreateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", AutoOnly: true,
		ExpectedFreezeRevisionRaw: "1", RequestID: requestID,
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err = svc.ReadReleaseActiveState(adminTestContext(), "qa_prod", "main", "")
	if err != nil || state.Revision != 0 || state.Active != nil {
		t.Fatalf("create advanced head: %+v err=%v", state, err)
	}
	replay, err := svc.CreateRelease(adminTestContext(), ReleaseCreateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", AutoOnly: true,
		ExpectedFreezeRevisionRaw: "1", RequestID: requestID,
	})
	if err != nil || !replay.Replayed || replay.Meta.SnapshotID != created.Meta.SnapshotID {
		t.Fatalf("create replay = %+v err=%v", replay, err)
	}
	state, err = svc.ReadReleaseActiveState(adminTestContext(), "qa_prod", "main", "")
	if err != nil || state.Revision != 0 || state.Active != nil {
		t.Fatalf("replay advanced head: %+v err=%v", state, err)
	}
}
