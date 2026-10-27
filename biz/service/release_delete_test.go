package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

func deletionInput(version, snapshot string) ReleaseDeleteInput {
	return ReleaseDeleteInput{EnvironmentKey: "qa_prod", PipelineKey: "main", Version: version,
		ExpectedSnapshotID: snapshot, RequestID: uuid.NewString()}
}

func TestOptionalReasonsAcrossTransitions(t *testing.T) {
	for _, note := range []string{"", " \t\n ", strings.Repeat("汉", 500)} {
		t.Run(fmt.Sprintf("runes_%d", len([]rune(note))), func(t *testing.T) {
			svc := newFreezeService(t)
			seedFreezeScope(t, svc, "qa_prod", "main")
			ctx := adminTestContext()
			lock, err := svc.LockFreeze(ctx, "qa_prod", "main", note, "0")
			if err != nil { t.Fatal(err) }
			if lock.LastReason != strings.TrimSpace(note) { t.Fatal("lock note was not normalized") }
			created, err := svc.CreateRelease(ctx, ReleaseCreateInput{EnvironmentKey: "qa_prod", PipelineKey: "main",
				Version: "v1", ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString(), Reason: note})
			if err != nil { t.Fatal(err) }
			if created.Meta.Reason != strings.TrimSpace(note) { t.Fatal("create note was not normalized") }
			active, err := svc.ActivateRelease(ctx, ReleaseActivateInput{EnvironmentKey: "qa_prod", PipelineKey: "main",
				Version: "v1", ExpectedActiveRevisionRaw: "0", RequestID: uuid.NewString(), Reason: note})
			if err != nil { t.Fatal(err) }
			if active.Active.ChangedReason != strings.TrimSpace(note) { t.Fatal("activate note was not normalized") }
			unlocked, err := svc.UnlockFreeze(ctx, "qa_prod", "main", note, "1")
			if err != nil { t.Fatal(err) }
			if unlocked.LastReason != strings.TrimSpace(note) { t.Fatal("unlock note was not normalized") }
			freezeEvents, err := svc.FreezeHistory(ctx, "qa_prod", "main", 20, "")
			if err != nil || len(freezeEvents.Items) != 2 { t.Fatalf("freeze history: %v", err) }
			for _, event := range freezeEvents.Items {
				if event.Reason != strings.TrimSpace(note) || event.Actor != "admin" { t.Fatal("incorrect freeze audit") }
			}
			publishEvents, err := svc.ReleaseHistoryPage(ctx, "qa_prod", "main", 20, "")
			if err != nil || len(publishEvents.Items) != 2 { t.Fatalf("release history: %v", err) }
			for _, event := range publishEvents.Items {
				if event.Reason != strings.TrimSpace(note) || event.Actor != "admin" { t.Fatal("incorrect release audit") }
			}
		})
	}
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	ctx := adminTestContext()
	tooLong := strings.Repeat("汉", 501)
	_, err := svc.LockFreeze(ctx, "qa_prod", "main", tooLong, "0")
	requireFreezeErrorType(t, err, ErrTypeFreezeInvalidReason)
	_, err = svc.UnlockFreeze(ctx, "qa_prod", "main", tooLong, "0")
	requireFreezeErrorType(t, err, ErrTypeFreezeInvalidReason)
	_, err = svc.CreateRelease(ctx, ReleaseCreateInput{EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1", RequestID: uuid.NewString(), Reason: tooLong})
	requireReleaseErrorType(t, err, ErrTypeReleaseInvalidRequest)
	_, err = svc.ActivateRelease(ctx, ReleaseActivateInput{EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1", RequestID: uuid.NewString(), Reason: tooLong})
	requireReleaseErrorType(t, err, ErrTypeReleaseInvalidRequest)
}

func TestReleaseDeleteLifecycleAndReplay(t *testing.T) {
	f := newReleaseFixture(t)
	svc, ctx := f.svc, adminTestContext()
	createID, activateID := uuid.NewString(), uuid.NewString()
	first, err := f.create(t, "v1", "1", createID)
	if err != nil { t.Fatal(err) }
	second, err := f.create(t, "v2", "1", "")
	if err != nil { t.Fatal(err) }
	if _, err := f.activate(t, "v1", "0", activateID); err != nil { t.Fatal(err) }
	if _, err := f.activate(t, "v2", "1", ""); err != nil { t.Fatal(err) }
	before, err := svc.logic.releaseDAO.GetVersionBySnapshotID(ctx, svc.logic.db, first.Meta.SnapshotID)
	if err != nil { t.Fatal(err) }
	assets, err := svc.logic.releaseDAO.ListAssets(ctx, svc.logic.db, first.Meta.SnapshotID)
	if err != nil || len(assets) != 1 { t.Fatalf("assets: %v", err) }
	storage, err := svc.releaseStorage()
	if err != nil { t.Fatal(err) }
	file := storage.assetPath(first.Meta.SnapshotID, assets[0].AssetID)
	bytesBefore, err := os.ReadFile(file)
	if err != nil { t.Fatal(err) }
	input := deletionInput("v1", first.Meta.SnapshotID)
	input.Reason = "  \n "
	actorCtx := WithAdminIdentity(ctx, AdminIdentity{UserID: 7, Username: "reviewer", Role: "admin"})
	deleted, err := svc.DeleteRelease(actorCtx, input)
	if err != nil { t.Fatal(err) }
	if !deleted.Deleted || deleted.Replayed || deleted.DeletedBy != "reviewer" || deleted.Reason != "" { t.Fatalf("delete: %+v", deleted) }
	after, err := svc.logic.releaseDAO.GetVersionBySnapshotID(ctx, svc.logic.db, first.Meta.SnapshotID)
	if err != nil { t.Fatal(err) }
	if after.DeletedAt == nil || after.Payload != before.Payload || after.PayloadSha256 != before.PayloadSha256 || after.ManifestSha256 != before.ManifestSha256 { t.Fatal("snapshot content changed") }
	bytesAfter, err := os.ReadFile(file)
	if err != nil || string(bytesBefore) != string(bytesAfter) { t.Fatal("snapshot file changed") }
	assetsAfter, err := svc.logic.releaseDAO.ListAssets(ctx, svc.logic.db, first.Meta.SnapshotID)
	if err != nil || len(assetsAfter) != len(assets) { t.Fatal("manifest removed") }
	page, err := svc.ReleaseVersions(ctx, "qa_prod", "main", 1, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].Meta.Version != "v2" || page.HasMore { t.Fatalf("filtered list: %+v %v", page, err) }
	if page.Active == nil || page.Active.SnapshotID != second.Meta.SnapshotID || page.Active.Revision != 2 { t.Fatal("head changed") }
	freeze, err := svc.FreezeStatus(ctx, "qa_prod", "main")
	if err != nil || freeze.Revision != 1 || freeze.State != model.FreezeStateLocked { t.Fatal("freeze changed") }

	// Deleted versions cannot be read, exported, downloaded, activated or recreated.
	_, err = svc.ReleaseDetail(ctx, "qa_prod", "main", "v1")
	requireReleaseErrorType(t, err, ErrTypeReleaseVersionDeleted)
	version := "v1"
	_, err = svc.RuntimeRelease(ctx, "qa_prod", "main", &version)
	requireReleaseErrorType(t, err, ErrTypeReleaseVersionDeleted)
	_, err = svc.ReleaseStaticExport(ctx, ReleaseStaticExportInput{EnvironmentKey: "qa_prod", PipelineKey: "main", Version: version, ExpectedSnapshotID: first.Meta.SnapshotID})
	requireReleaseErrorType(t, err, ErrTypeReleaseVersionDeleted)
	_, _, err = svc.ReleaseAssetDownload(ctx, first.Meta.SnapshotID, assets[0].AssetID)
	requireReleaseErrorType(t, err, ErrTypeReleaseVersionDeleted)
	_, err = f.activate(t, "v1", "2", "")
	requireReleaseErrorType(t, err, ErrTypeReleaseVersionDeleted)
	_, err = f.create(t, "v1", "1", "")
	requireReleaseErrorType(t, err, ErrTypeReleaseVersionExists)
	_, err = f.create(t, "v1", "1", createID)
	requireReleaseErrorType(t, err, ErrTypeReleaseVersionDeleted)
	if _, err := svc.RuntimeRelease(ctx, "qa_prod", "main", nil); err != nil { t.Fatal(err) }
	// 未删除版本仍可下载自己的资源(用该快照清单里的 asset_id)
	secondAssets, err := svc.logic.releaseDAO.ListAssets(ctx, svc.logic.db, second.Meta.SnapshotID)
	if err != nil || len(secondAssets) == 0 { t.Fatalf("second manifest: %v %d", err, len(secondAssets)) }
	if _, _, err := svc.ReleaseAssetDownload(ctx, second.Meta.SnapshotID, secondAssets[0].AssetID); err != nil {
		t.Fatalf("active version asset must stay downloadable: %v", err)
	}
	// 已删除版本的底层文件保留(逻辑删除只改变对外可用状态)
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("deleted version files must stay on disk: %v", err)
	}
	// Replaying a historical activation only confirms the old operation.
	replayActivation, err := f.activate(t, "v1", "0", activateID)
	if err != nil || !replayActivation.Replayed || replayActivation.Active.Version != "v2" || replayActivation.AppliedRevision != 1 { t.Fatalf("activation replay: %+v %v", replayActivation, err) }
	input.Reason = "" // normalization shares the same fingerprint
	replayed, err := svc.DeleteRelease(ctx, input)
	if err != nil || !replayed.Replayed || replayed.DeletedAt != deleted.DeletedAt || replayed.DeletedBy != "reviewer" { t.Fatalf("replay: %+v %v", replayed, err) }
	input.Reason = "changed"
	_, err = svc.DeleteRelease(ctx, input)
	requireReleaseErrorType(t, err, ErrTypeReleaseRequestConflict)
	input.Reason, input.RequestID = "", uuid.NewString()
	_, err = svc.DeleteRelease(ctx, input)
	requireReleaseErrorType(t, err, ErrTypeReleaseVersionDeleted)
	history, err := svc.ReleaseHistoryPage(ctx, "qa_prod", "main", 20, "")
	if err != nil || len(history.Items) != 5 || history.Items[0].EventType != model.ReleaseEventDelete || history.Items[0].Actor != "reviewer" { t.Fatalf("audit: %+v %v", history, err) }
	// A fresh service object sees the tombstone, independently of process-local state.
	fresh := NewService(svc.logic.db, nil, "/rainbow-bridge", nil)
	_, err = fresh.ReleaseDetail(ctx, "qa_prod", "main", "v1")
	requireReleaseErrorType(t, err, ErrTypeReleaseVersionDeleted)
}

func TestReleaseDeleteGuardsAndAuditRollback(t *testing.T) {
	f := newReleaseFixture(t)
	svc, ctx := f.svc, adminTestContext()
	created, err := f.create(t, "v1", "1", "")
	if err != nil { t.Fatal(err) }
	input := deletionInput("v1", created.Meta.SnapshotID)
	_, err = svc.DeleteRelease(context.Background(), input)
	requireAuthErrorType(t, err, ErrTypeAuthRequired)
	bad := input; bad.ExpectedSnapshotID = uuid.NewString()
	_, err = svc.DeleteRelease(ctx, bad)
	requireReleaseErrorType(t, err, ErrTypeReleaseSnapshotMismatch)
	bad = input; bad.Version = "missing"
	_, err = svc.DeleteRelease(ctx, bad)
	requireReleaseErrorType(t, err, ErrTypeReleaseVersionNotFound)
	seedFreezeScope(t, svc, "other", "main")
	bad = input; bad.EnvironmentKey = "other"
	_, err = svc.DeleteRelease(ctx, bad)
	requireReleaseErrorType(t, err, ErrTypeReleaseVersionNotFound)
	for _, mutate := range []func(*ReleaseDeleteInput){
		func(i *ReleaseDeleteInput) { i.RequestID = "bad" },
		func(i *ReleaseDeleteInput) { i.ExpectedSnapshotID = "" },
		func(i *ReleaseDeleteInput) { i.Reason = strings.Repeat("汉", 501) },
	} {
		bad = input; mutate(&bad)
		_, err = svc.DeleteRelease(ctx, bad)
		requireReleaseErrorType(t, err, ErrTypeReleaseInvalidRequest)
	}
	// Fail the event insertion after marking; the transaction must undo the mark.
	callback := "test:fail_release_delete_event"
	if err := svc.logic.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if event, ok := tx.Statement.Dest.(*model.ConfigReleaseEvent); ok && event.EventType == model.ReleaseEventDelete {
			tx.AddError(errors.New("injected delete audit failure"))
		}
	}); err != nil { t.Fatal(err) }
	t.Cleanup(func() { svc.logic.db.Callback().Create().Remove(callback) })
	_, err = svc.DeleteRelease(ctx, input)
	requireReleaseErrorType(t, err, ErrTypeReleaseInternalError)
	row, err := svc.logic.releaseDAO.GetVersionBySnapshotID(ctx, svc.logic.db, created.Meta.SnapshotID)
	if err != nil || row.DeletedAt != nil { t.Fatal("failed audit did not roll back deletion") }
	if event, err := svc.logic.releaseDAO.GetEvent(ctx, svc.logic.db, model.ReleaseEventDelete, input.RequestID); err != nil || event != nil { t.Fatal("failed audit left an event") }
	if _, err := f.activate(t, "v1", "0", ""); err != nil { t.Fatal(err) }
	_, err = svc.DeleteRelease(ctx, input)
	requireReleaseErrorType(t, err, ErrTypeReleaseDeleteActive)
}

func TestReleaseDeleteActivationAndReplayConcurrency(t *testing.T) {
	f := newReleaseFixture(t)
	svc, ctx := f.svc, adminTestContext()
	for n := 0; n < 6; n++ {
		version := fmt.Sprintf("v%d", n)
		created, err := f.create(t, version, "1", "")
		if err != nil { t.Fatal(err) }
		active, err := svc.ReleaseActive(ctx, "qa_prod", "main")
		if err != nil { t.Fatal(err) }
		revision := int64(0); if active != nil { revision = active.Revision }
		start := make(chan struct{})
		deleteErr, activateErr := make(chan error, 1), make(chan error, 1)
		go func() { <-start; _, err := svc.DeleteRelease(ctx, deletionInput(version, created.Meta.SnapshotID)); deleteErr <- err }()
		go func() { <-start; _, err := svc.ActivateRelease(ctx, ReleaseActivateInput{EnvironmentKey: "qa_prod", PipelineKey: "main", Version: version, ExpectedActiveRevisionRaw: strconv.FormatInt(revision, 10), RequestID: uuid.NewString()}); activateErr <- err }()
		close(start)
		d, a := <-deleteErr, <-activateErr
		if d == nil { requireReleaseErrorType(t, a, ErrTypeReleaseVersionDeleted) } else {
			if a != nil { t.Fatalf("both operations failed: %v / %v", d, a) }
			requireReleaseErrorType(t, d, ErrTypeReleaseDeleteActive)
		}
		if _, err := svc.ReleaseActive(ctx, "qa_prod", "main"); err != nil { t.Fatal(err) }
	}
	created, err := f.create(t, "concurrent-delete", "1", "")
	if err != nil { t.Fatal(err) }
	input := deletionInput("concurrent-delete", created.Meta.SnapshotID)
	type outcome struct { result *ReleaseDeleteResult; err error }
	results := make(chan outcome, 2)
	for n := 0; n < 2; n++ { go func() { r, e := svc.DeleteRelease(ctx, input); results <- outcome{r, e} }() }
	replays := 0
	for n := 0; n < 2; n++ { o := <-results; if o.err != nil { t.Fatal(o.err) }; if o.result.Replayed { replays++ } }
	if replays != 1 { t.Fatalf("expected exactly one replay, got %d", replays) }
	var count int64
	if err := svc.logic.db.Model(&model.ConfigReleaseEvent{}).Where("event_type = ? AND request_id = ?", model.ReleaseEventDelete, input.RequestID).Count(&count).Error; err != nil || count != 1 { t.Fatal("duplicate deletion events") }
}

func TestReleaseDeleteInFlightReadAndExport(t *testing.T) {
	for _, export := range []bool{false, true} {
		t.Run(fmt.Sprintf("export_%v", export), func(t *testing.T) {
			f := newReleaseFixture(t)
			created, err := f.create(t, "v1", "1", "")
			if err != nil { t.Fatal(err) }
			svc, ctx := f.svc, adminTestContext()
			paused, resume := make(chan struct{}), make(chan struct{})
			defer func() { releaseReadPauseHookForTest = nil; releaseExportPauseHookForTest = nil }()
			hook := func(string) { close(paused); <-resume }
			if export { releaseExportPauseHookForTest = hook } else { releaseReadPauseHookForTest = hook }
			finished := make(chan error, 1)
			go func() {
				if export {
					r, e := svc.ReleaseStaticExport(ctx, ReleaseStaticExportInput{EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1", ExpectedSnapshotID: created.Meta.SnapshotID})
					if r != nil { os.Remove(r.TempPath) }; finished <- e
				} else { version := "v1"; _, e := svc.RuntimeRelease(ctx, "qa_prod", "main", &version); finished <- e }
			}()
			<-paused
			_, deleteErr := svc.DeleteRelease(ctx, deletionInput("v1", created.Meta.SnapshotID))
			close(resume)
			readErr := <-finished
			if deleteErr != nil || readErr != nil { t.Fatalf("delete/read: %v / %v", deleteErr, readErr) }
			version := "v1"; _, err = svc.RuntimeRelease(ctx, "qa_prod", "main", &version)
			requireReleaseErrorType(t, err, ErrTypeReleaseVersionDeleted)
		})
	}
}

func TestReleaseDeleteCorruptActiveAndParentProtection(t *testing.T) {
	f := newReleaseFixture(t)
	created, err := f.create(t, "v1", "1", "")
	if err != nil { t.Fatal(err) }
	svc, ctx := f.svc, adminTestContext()
	if _, err := svc.DeleteRelease(ctx, deletionInput("v1", created.Meta.SnapshotID)); err != nil { t.Fatal(err) }
	count, err := svc.logic.releaseDAO.CountVersionsByScope(ctx, svc.logic.db, "qa_prod", "main")
	if err != nil || count != 1 { t.Fatal("tombstone must still protect parent/version identity") }
	if err := svc.logic.releaseDAO.EnsureHead(ctx, svc.logic.db, "qa_prod", "main"); err != nil { t.Fatal(err) }
	if err := svc.logic.db.Model(&model.ConfigReleaseHead{}).Where("environment_key = ? AND pipeline_key = ?", "qa_prod", "main").Update("active_snapshot_id", created.Meta.SnapshotID).Error; err != nil { t.Fatal(err) }
	_, err = svc.ReleaseActive(ctx, "qa_prod", "main")
	requireReleaseErrorType(t, err, ErrTypeReleaseIncomplete)
	_, err = svc.RuntimeRelease(ctx, "qa_prod", "main", nil)
	requireReleaseErrorType(t, err, ErrTypeReleaseIncomplete)
	if _, err := os.Stat(filepath.Join(dataDirectory, "release-snapshots", created.Meta.SnapshotID)); err != nil { t.Fatal("snapshot directory was removed") }
}
