package service

import (
	"testing"

	envpb "github.com/yi-nology/rainbow_bridge/biz/model/environment"
	plpb "github.com/yi-nology/rainbow_bridge/biz/model/pipeline"
)

func TestUpdatePipelineSortOrderPresence(t *testing.T) {
	svc := newFreezeService(t)
	ctx := adminTestContext()
	env := "qa_sort_order"
	if err := svc.AddEnvironment(ctx, &envpb.Environment{
		EnvironmentKey: env, EnvironmentName: "Sort Order", IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddPipeline(ctx, env, &plpb.Pipeline{
		PipelineKey: "team", PipelineName: "Original", SortOrder: 20, IsActive: true,
	}); err != nil && err != ErrPipelineKeyExists {
		t.Fatal(err)
	}
	pipeline, err := svc.GetPipeline(ctx, env, "team")
	if err != nil {
		t.Fatal(err)
	}
	scopeID := pipeline.GetScopeId()

	// Omitted sort_order preserves the existing non-zero sort.
	if err := svc.UpdatePipelineWithIdentityAndSort(ctx, env, &plpb.Pipeline{
		PipelineKey: "team", PipelineName: "Renamed", IsActive: true,
	}, scopeID, nil); err != nil {
		t.Fatal(err)
	}
	pipeline, err = svc.GetPipeline(ctx, env, "team")
	if err != nil || pipeline.GetPipelineName() != "Renamed" || pipeline.GetSortOrder() != 20 {
		t.Fatalf("omitted sort changed state: %+v err=%v", pipeline, err)
	}

	// Explicit zero must be persisted, not skipped as a Go zero value.
	zero := 0
	if err := svc.UpdatePipelineWithIdentityAndSort(ctx, env, &plpb.Pipeline{
		PipelineKey: "team", PipelineName: "Renamed", IsActive: true,
	}, scopeID, &zero); err != nil {
		t.Fatal(err)
	}
	pipeline, err = svc.GetPipeline(ctx, env, "team")
	if err != nil || pipeline.GetSortOrder() != 0 || pipeline.GetPipelineName() != "Renamed" {
		t.Fatalf("zero sort not persisted: %+v err=%v", pipeline, err)
	}

	// Other explicit integers still persist normally.
	next := 10
	if err := svc.UpdatePipelineWithIdentityAndSort(ctx, env, &plpb.Pipeline{
		PipelineKey: "team", PipelineName: "Renamed", IsActive: true,
	}, scopeID, &next); err != nil {
		t.Fatal(err)
	}
	pipeline, err = svc.GetPipeline(ctx, env, "team")
	if err != nil || pipeline.GetSortOrder() != 10 {
		t.Fatalf("sort 10 not persisted: %+v err=%v", pipeline, err)
	}
}
