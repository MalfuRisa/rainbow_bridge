package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	configdb "github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

func createAutoRelease(t *testing.T, fixture *releaseFixture, requestID string) *ReleaseCreateResult {
	t.Helper()
	result, err := fixture.svc.CreateRelease(adminTestContext(), ReleaseCreateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main",
		AutoOnly: true, ExpectedFreezeRevisionRaw: "1", RequestID: requestID,
		Reason: "自动编号",
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCreateReleaseAutoNumbersPerScope(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := context.Background()

	firstID := uuid.NewString()
	first := createAutoRelease(t, fixture, firstID)
	if first.Meta.Version != "v1" || first.Replayed {
		t.Fatalf("first = %+v", first)
	}
	second := createAutoRelease(t, fixture, uuid.NewString())
	if second.Meta.Version != "v2" {
		t.Fatalf("second version = %s", second.Meta.Version)
	}
	replay := createAutoRelease(t, fixture, firstID)
	if !replay.Replayed || replay.Meta.Version != "v1" || replay.Meta.SnapshotID != first.Meta.SnapshotID {
		t.Fatalf("replay = %+v", replay)
	}
	active, err := svc.ReleaseActive(ctx, "qa_prod", "main")
	if err != nil || active != nil {
		t.Fatalf("creation must not activate: %+v %v", active, err)
	}

	scope, err := resolveScopeByKeys(ctx, svc.logic, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	head, err := svc.logic.releaseDAO.GetHeadByScopeID(ctx, svc.logic.db, scope.ScopeID)
	if err != nil || head == nil || head.LastVersionNumber != 2 || head.Revision != 0 {
		t.Fatalf("head after auto create = %+v %v", head, err)
	}

	// Deleting the highest version must not reuse its number.
	if _, err := svc.DeleteRelease(adminTestContext(), ReleaseDeleteInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v2",
		ExpectedSnapshotID: second.Meta.SnapshotID, RequestID: uuid.NewString(),
	}); err != nil {
		t.Fatal(err)
	}
	third := createAutoRelease(t, fixture, uuid.NewString())
	if third.Meta.Version != "v3" {
		t.Fatalf("after delete version = %s", third.Meta.Version)
	}
}

func TestCreateReleaseAutoNumberExhaustion(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	scope, err := resolveScopeByKeys(context.Background(), svc.logic, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.logic.releaseDAO.EnsureHeadByScope(context.Background(), svc.logic.db,
		scope.ScopeID, "qa_prod", "main"); err != nil {
		t.Fatal(err)
	}
	if err := svc.logic.db.Model(&model.ConfigReleaseHead{}).
		Where("scope_id = ?", scope.ScopeID).
		UpdateColumn("last_version_number", int64(math.MaxInt64)).Error; err != nil {
		t.Fatal(err)
	}
	_, err = svc.CreateRelease(adminTestContext(), ReleaseCreateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", AutoOnly: true,
		ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString(),
	})
	requireReleaseErrorType(t, err, ErrTypeReleaseVersionExhausted)
}

func TestEnsureReleaseNumberingUsesCanonicalHistoryAndIsIdempotent(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := context.Background()
	scope, err := resolveScopeByKeys(ctx, svc.logic, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	changedAt := now.Add(-time.Hour)
	if err := svc.logic.db.Create(&model.ConfigReleaseHead{
		ScopeID: scope.ScopeID, EnvironmentKey: "qa_prod", PipelineKey: "main",
		ActiveSnapshotID: "snap-old", Revision: 7, ChangedAt: &changedAt,
		ChangedBy: "old-actor", ChangedReason: "keep me",
	}).Error; err != nil {
		t.Fatal(err)
	}
	versions := []struct {
		name    string
		deleted bool
	}{
		{"v1.0", false}, {"v01", false}, {"v0", false}, {"v2", false},
		{"v5", true}, {"v9", true}, {"test-1", false},
	}
	for index, item := range versions {
		row := &model.ConfigReleaseVersion{
			SnapshotID: uuid.NewString(), EnvironmentKey: "qa_prod", PipelineKey: "main",
			Version: item.name, CreationRequestID: uuid.NewString(),
			RequestFingerprint: "f", Payload: "{}", PayloadSha256: "p", ManifestSha256: "m",
		}
		if item.deleted {
			row.DeletedAt = &now
		}
		if err := svc.logic.db.Create(row).Error; err != nil {
			t.Fatalf("seed version %d: %v", index, err)
		}
	}
	if err := configdb.EnsureReleaseNumbering(ctx, svc.logic.db); err != nil {
		t.Fatal(err)
	}
	head, err := svc.logic.releaseDAO.GetHeadByScopeID(ctx, svc.logic.db, scope.ScopeID)
	if err != nil || head == nil || head.LastVersionNumber != 9 {
		t.Fatalf("head after backfill = %+v %v", head, err)
	}
	if head.Revision != 7 || head.ActiveSnapshotID != "snap-old" ||
		head.ChangedBy != "old-actor" || head.ChangedReason != "keep me" {
		t.Fatalf("backfill changed active state: %+v", head)
	}
	if err := configdb.EnsureReleaseNumbering(ctx, svc.logic.db); err != nil {
		t.Fatal(err)
	}
	head, err = svc.logic.releaseDAO.GetHeadByScopeID(ctx, svc.logic.db, scope.ScopeID)
	if err != nil || head.LastVersionNumber != 9 {
		t.Fatalf("idempotent backfill = %+v %v", head, err)
	}
	next := createAutoRelease(t, fixture, uuid.NewString())
	if next.Meta.Version != "v10" {
		t.Fatalf("next canonical version = %s", next.Meta.Version)
	}
}
