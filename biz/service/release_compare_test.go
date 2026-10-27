package service

import (
	"context"
	"testing"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
)

func compareFixtureInput(t *testing.T, svc *Service, version, snapshotID, revision string) ReleaseCompareInput {
	t.Helper()
	scope, err := resolveScopeByKeys(context.Background(), svc.logic, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	return ReleaseCompareInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main",
		ExpectedScopeID:        scope.ScopeID,
		Version:                version,
		ExpectedSnapshotID:     snapshotID,
		ExpectedActiveRevision: revision,
	}
}

func TestCompareReleaseMixedChangesAndPagination(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()
	v1, err := fixture.create(t, "v1.0", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.activate(t, "v1.0", "0", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UnlockFreeze(ctx, "qa_prod", "main", "edit", "1"); err != nil {
		t.Fatal(err)
	}
	updateEditorContent(t, svc, "REL_TEXT", "B-title")
	if _, err := svc.AddConfig(ctx, &common.ResourceConfig{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Alias: "NEW", Name: "NEW",
		Content: "new", Type: "text",
	}); err != nil {
		t.Fatal(err)
	}
	bigKey := configKeyByAlias(t, svc, "qa_prod", "main", "REL_BIG")
	if err := svc.DeleteConfig(ctx, "qa_prod", "main", bigKey); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.LockFreeze(ctx, "qa_prod", "main", "snapshot", "2"); err != nil {
		t.Fatal(err)
	}
	v2, err := fixture.create(t, "v2.0", "3", "")
	if err != nil {
		t.Fatal(err)
	}

	input := compareFixtureInput(t, svc, "v2.0", v2.Meta.SnapshotID, "1")
	input.ExpectedActiveSnapshotID = v1.Meta.SnapshotID
	result, err := svc.CompareRelease(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != (ReleaseCompareSummary{Added: 1, Modified: 1, Removed: 1, Unchanged: 1}) {
		t.Fatalf("summary = %+v", result.Summary)
	}
	if result.TotalChanges != 3 || result.HasMore || result.TargetIsActive {
		t.Fatalf("result = %+v", result)
	}
	if result.Baseline == nil || result.Baseline.Version != "v1.0" || result.Baseline.SnapshotID != v1.Meta.SnapshotID {
		t.Fatalf("baseline = %+v", result.Baseline)
	}
	if len(result.Items) != 3 {
		t.Fatalf("items = %+v", result.Items)
	}
	itemsByKey := map[string]ReleaseCompareItem{}
	for _, item := range result.Items {
		itemsByKey[item.ResourceKey] = item
	}
	newItem := itemsByKey[configKeyByAlias(t, svc, "qa_prod", "main", "NEW")]
	if newItem.ChangeType != "added" || newItem.Before != nil || newItem.After == nil {
		t.Fatalf("added item = %+v", newItem)
	}
	removedItem := itemsByKey[bigKey]
	if removedItem.ChangeType != "removed" || removedItem.After != nil || removedItem.Before == nil {
		t.Fatalf("removed item = %+v", removedItem)
	}
	modifiedItem := itemsByKey[configKeyByAlias(t, svc, "qa_prod", "main", "REL_TEXT")]
	if modifiedItem.ChangeType != "modified" || len(modifiedItem.ChangedFields) != 1 ||
		modifiedItem.ChangedFields[0] != "content" {
		t.Fatalf("modified item = %+v", modifiedItem)
	}

	// Pagination keeps the full summary and a stable order.
	input.Offset, input.Limit = 0, 1
	page1, err := svc.CompareRelease(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Items) != 1 || !page1.HasMore || page1.NextOffset == nil || *page1.NextOffset != 1 {
		t.Fatalf("page1 = %+v", page1)
	}
	input.Offset = 3
	page4, err := svc.CompareRelease(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(page4.Items) != 0 || page4.HasMore || page4.NextOffset != nil ||
		page4.Summary != result.Summary {
		t.Fatalf("page4 = %+v", page4)
	}
}

func TestCompareReleaseSameContentIgnoresSnapshotAssetIDs(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()
	v1, err := fixture.create(t, "v1.0", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.activate(t, "v1.0", "0", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UnlockFreeze(ctx, "qa_prod", "main", "same content", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.LockFreeze(ctx, "qa_prod", "main", "same content", "2"); err != nil {
		t.Fatal(err)
	}
	v2, err := fixture.create(t, "v2.0", "3", "")
	if err != nil {
		t.Fatal(err)
	}
	input := compareFixtureInput(t, svc, "v2.0", v2.Meta.SnapshotID, "1")
	input.ExpectedActiveSnapshotID = v1.Meta.SnapshotID
	result, err := svc.CompareRelease(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalChanges != 0 || result.Summary.Unchanged != v1.Meta.ConfigCount {
		t.Fatalf("same content must be unchanged: %+v", result.Summary)
	}
}

func TestCompareReleaseObjectCanonicalizationAndResourceBytes(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()
	// Seed the same object with a different key order before v1.
	updateEditorContent(t, svc, "REL_BIG", `{"nested":{"a":1,"b":2},"big":`+releaseBigLiteral+`}`)
	v1, err := fixture.create(t, "v1.0", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.activate(t, "v1.0", "0", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UnlockFreeze(ctx, "qa_prod", "main", "reorder", "1"); err != nil {
		t.Fatal(err)
	}
	// Object key order is not a change.
	updateEditorContent(t, svc, "REL_BIG", `{"big":`+releaseBigLiteral+`,"nested":{"b":2,"a":1}}`)
	if _, err := svc.LockFreeze(ctx, "qa_prod", "main", "reorder", "2"); err != nil {
		t.Fatal(err)
	}
	v2, err := fixture.create(t, "v2.0", "3", "")
	if err != nil {
		t.Fatal(err)
	}
	input := compareFixtureInput(t, svc, "v2.0", v2.Meta.SnapshotID, "1")
	input.ExpectedActiveSnapshotID = v1.Meta.SnapshotID
	result, err := svc.CompareRelease(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalChanges != 0 {
		t.Fatalf("key reorder must not be reported: %+v", result.Items)
	}

	// Real resource bytes change is content change even when filename/type stay equal.
	if _, err := svc.UnlockFreeze(ctx, "qa_prod", "main", "asset change", "3"); err != nil {
		t.Fatal(err)
	}
	scope, err := resolveScopeByKeys(ctx, svc.logic, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	newAsset, reference, err := svc.UploadAsset(ctx, &FileUploadInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", ExpectedScopeID: scope.ScopeID,
		FileName: "pic-a.png", ContentType: "image/png", Data: []byte("PNG-A-BYTES-CHANGED"),
	})
	if err != nil {
		t.Fatal(err)
	}
	imageKey := configKeyByAlias(t, svc, "qa_prod", "main", "REL_IMAGE")
	imageConfig, err := svc.GetConfigDetail(ctx, "qa_prod", "main", imageKey)
	if err != nil {
		t.Fatal(err)
	}
	imageConfig.Content = reference
	if _, err := svc.UpdateConfigWithIdentity(ctx, imageConfig, imageConfig.EditRevision, scope.ScopeID); err != nil {
		t.Fatal(err)
	}
	_ = newAsset
	if _, err := svc.LockFreeze(ctx, "qa_prod", "main", "asset change", "4"); err != nil {
		t.Fatal(err)
	}
	v3, err := fixture.create(t, "v3.0", "5", "")
	if err != nil {
		t.Fatal(err)
	}
	input = compareFixtureInput(t, svc, "v3.0", v3.Meta.SnapshotID, "1")
	input.ExpectedActiveSnapshotID = v1.Meta.SnapshotID
	result, err = svc.CompareRelease(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	// Active remains v1, so v3 is compared against v1 here; asset identity and
	// bytes both differ from v1, therefore REL_IMAGE must be modified.
	found := false
	for _, item := range result.Items {
		if item.ResourceKey == imageKey {
			found = true
			if item.ChangeType != "modified" || len(item.ChangedFields) != 1 || item.ChangedFields[0] != "content" {
				t.Fatalf("image change = %+v", item)
			}
		}
	}
	if !found {
		t.Fatal("image byte change was not reported")
	}
}

func TestCompareReleaseConflictsAndNoActiveBaseline(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()
	v1, err := fixture.create(t, "v1.0", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	// No active yet: revision 0, empty baseline, all added.
	input := compareFixtureInput(t, svc, "v1.0", v1.Meta.SnapshotID, "0")
	result, err := svc.CompareRelease(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Baseline != nil || result.Summary.Added != v1.Meta.ConfigCount ||
		result.Summary.Modified != 0 || result.Summary.Removed != 0 {
		t.Fatalf("no-active compare = %+v", result)
	}
	if _, err := fixture.activate(t, "v1.0", "0", ""); err != nil {
		t.Fatal(err)
	}
	activeCompare := compareFixtureInput(t, svc, "v1.0", v1.Meta.SnapshotID, "1")
	activeCompare.ExpectedActiveSnapshotID = v1.Meta.SnapshotID
	activeResult, err := svc.CompareRelease(ctx, activeCompare)
	if err != nil {
		t.Fatal(err)
	}
	if !activeResult.TargetIsActive || activeResult.TotalChanges != 0 ||
		activeResult.Baseline == nil || activeResult.Baseline.SnapshotID != v1.Meta.SnapshotID {
		t.Fatalf("active target compare = %+v", activeResult)
	}
	// Revision conflict.
	input = compareFixtureInput(t, svc, "v1.0", v1.Meta.SnapshotID, "0")
	if _, err := svc.CompareRelease(ctx, input); err == nil || !isReleaseType(err, ErrTypeReleaseActiveRevision) {
		t.Fatalf("expected revision conflict, got %v", err)
	}
	// Baseline snapshot conflict at the same revision.
	input = compareFixtureInput(t, svc, "v1.0", v1.Meta.SnapshotID, "1")
	input.ExpectedActiveSnapshotID = ""
	if _, err := svc.CompareRelease(ctx, input); err == nil || !isReleaseType(err, ErrTypeReleaseCompareBaseline) {
		t.Fatalf("expected baseline conflict, got %v", err)
	}
	// Target snapshot mismatch.
	input = compareFixtureInput(t, svc, "v1.0", "00000000-0000-4000-8000-000000000000", "1")
	input.ExpectedActiveSnapshotID = v1.Meta.SnapshotID
	if _, err := svc.CompareRelease(ctx, input); err == nil || !isReleaseType(err, ErrTypeReleaseSnapshotMismatch) {
		t.Fatalf("expected snapshot mismatch, got %v", err)
	}
	// Invalid pagination.
	input = compareFixtureInput(t, svc, "v1.0", v1.Meta.SnapshotID, "1")
	input.ExpectedActiveSnapshotID = v1.Meta.SnapshotID
	input.Limit = 101
	if _, err := svc.CompareRelease(ctx, input); err == nil || !isReleaseType(err, ErrTypeReleaseInvalidRequest) {
		t.Fatalf("expected invalid limit, got %v", err)
	}
}

func TestCompareReleaseFixedDirectionAndSelf(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()
	v1, err := fixture.create(t, "v1.0", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.activate(t, "v1.0", "0", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UnlockFreeze(ctx, "qa_prod", "main", "fixed", "1"); err != nil {
		t.Fatal(err)
	}
	updateEditorContent(t, svc, "REL_TEXT", "B-title")
	if _, err := svc.AddConfig(ctx, &common.ResourceConfig{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Alias: "NEW", Name: "NEW",
		Content: "new", Type: "text",
	}); err != nil {
		t.Fatal(err)
	}
	bigKey := configKeyByAlias(t, svc, "qa_prod", "main", "REL_BIG")
	if err := svc.DeleteConfig(ctx, "qa_prod", "main", bigKey); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.LockFreeze(ctx, "qa_prod", "main", "fixed", "2"); err != nil {
		t.Fatal(err)
	}
	v2, err := fixture.create(t, "v2.0", "3", "")
	if err != nil {
		t.Fatal(err)
	}

	input := compareFixtureInput(t, svc, "v2.0", v2.Meta.SnapshotID, "")
	input.ComparisonMode = "fixed"
	input.BaselineVersion = "v1.0"
	input.ExpectedBaselineSnapshotID = v1.Meta.SnapshotID
	forward, err := svc.CompareRelease(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if forward.ComparisonMode != "fixed" || forward.Summary != (ReleaseCompareSummary{Added: 1, Modified: 1, Removed: 1, Unchanged: 1}) {
		t.Fatalf("forward = %+v", forward)
	}
	if forward.Baseline == nil || forward.Baseline.Version != "v1.0" || forward.Target.Version != "v2.0" {
		t.Fatalf("forward sides = %+v baseline=%+v target=%+v", forward, forward.Baseline, forward.Target)
	}
	if forward.ActiveRevision != 0 || forward.TargetIsActive {
		t.Fatalf("fixed must not claim active context: %+v", forward)
	}

	reverse := input
	reverse.Version, reverse.ExpectedSnapshotID = "v1.0", v1.Meta.SnapshotID
	reverse.BaselineVersion, reverse.ExpectedBaselineSnapshotID = "v2.0", v2.Meta.SnapshotID
	backward, err := svc.CompareRelease(ctx, reverse)
	if err != nil {
		t.Fatal(err)
	}
	if backward.Summary != (ReleaseCompareSummary{Added: 1, Modified: 1, Removed: 1, Unchanged: 1}) {
		t.Fatalf("reverse summary = %+v", backward.Summary)
	}
	forwardTypes := map[string]string{}
	for _, item := range forward.Items {
		forwardTypes[item.ResourceKey] = item.ChangeType
	}
	for _, item := range backward.Items {
		if item.ChangeType == "added" && forwardTypes[item.ResourceKey] != "removed" {
			t.Fatalf("reverse added must mirror forward removed: %+v", item)
		}
		if item.ChangeType == "removed" && forwardTypes[item.ResourceKey] != "added" {
			t.Fatalf("reverse removed must mirror forward added: %+v", item)
		}
	}

	self := input
	self.BaselineVersion, self.ExpectedBaselineSnapshotID = "v2.0", v2.Meta.SnapshotID
	same, err := svc.CompareRelease(ctx, self)
	if err != nil {
		t.Fatal(err)
	}
	if len(same.Items) != 0 || same.TotalChanges != 0 || same.Summary.Unchanged == 0 {
		t.Fatalf("self compare = %+v", same)
	}

	badSide := input
	badSide.ExpectedBaselineSnapshotID = v2.Meta.SnapshotID
	_, err = svc.CompareRelease(ctx, badSide)
	if !isReleaseType(err, ErrTypeReleaseSnapshotMismatch) {
		t.Fatalf("snapshot mismatch = %v", err)
	}
	releaseErr, _ := AsReleaseError(err)
	if releaseErr.Side != "baseline" {
		t.Fatalf("mismatch side = %q", releaseErr.Side)
	}

	mixed := input
	mixed.ExpectedActiveRevision = "0"
	_, err = svc.CompareRelease(ctx, mixed)
	if !isReleaseType(err, ErrTypeReleaseInvalidRequest) {
		t.Fatalf("fixed mixed active field = %v", err)
	}
}

func configKeyByAlias(t *testing.T, svc *Service, environmentKey, pipelineKey, alias string) string {
	t.Helper()
	var row model.Config
	if err := svc.logic.db.Where("environment_key = ? AND pipeline_key = ? AND alias = ?",
		environmentKey, pipelineKey, alias).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row.ResourceKey
}

func isReleaseType(err error, errorType string) bool {
	releaseErr, ok := AsReleaseError(err)
	return ok && releaseErr.ErrorType == errorType
}

var _ = model.Config{}
