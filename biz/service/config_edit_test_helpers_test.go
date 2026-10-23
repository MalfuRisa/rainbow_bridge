package service

import (
	"context"
	"testing"

	"github.com/yi-nology/rainbow_bridge/biz/model/common"
)

func currentEditRevision(t *testing.T, svc *Service, cfg *common.ResourceConfig) string {
	t.Helper()
	row, err := svc.GetConfigDetail(context.Background(), cfg.EnvironmentKey, cfg.PipelineKey, cfg.ResourceKey)
	if err != nil {
		t.Fatal(err)
	}
	return row.EditRevision
}
