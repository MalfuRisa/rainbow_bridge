package service

import (
	"context"
	"path"
	"regexp"
	"strings"

	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	"github.com/yi-nology/rainbow_bridge/biz/model/runtime"
)

// --------------------- Runtime operations ---------------------

// GetRuntimeOverview returns all environments and their pipelines.
func (s *Service) GetRuntimeOverview(ctx context.Context) (*runtime.RuntimeOverviewResponse, error) {
	isActive := true
	envs, err := s.logic.environmentDAO.List(ctx, s.logic.db, &isActive, 0, 0)
	if err != nil {
		return nil, err
	}

	var envOverviews []*runtime.EnvironmentOverview
	for _, env := range envs {
		pipelines, err := s.logic.pipelineDAO.List(ctx, s.logic.db, env.EnvironmentKey, &isActive, 0, 0)
		if err != nil {
			return nil, err
		}

		var pipelineOverviews []*runtime.PipelineOverview
		for _, pl := range pipelines {
			pipelineOverviews = append(pipelineOverviews, &runtime.PipelineOverview{
				ScopeId:       pl.ScopeID,
				EnvironmentId: pl.EnvironmentID,
				PipelineKey:   pl.PipelineKey,
				PipelineName:  pl.PipelineName,
			})
		}

		envOverviews = append(envOverviews, &runtime.EnvironmentOverview{
			EnvironmentId:   env.EnvironmentID,
			EnvironmentKey:  env.EnvironmentKey,
			EnvironmentName: env.EnvironmentName,
			Pipelines:       pipelineOverviews,
		})
	}

	return &runtime.RuntimeOverviewResponse{
		Code: 200,
		Msg:  "OK",
		Data: &runtime.RuntimeOverviewData{
			Total: int32(len(envOverviews)), // #nosec G115 -- count will not exceed int32
			List:  envOverviews,
		},
	}, nil
}

func extractAssetIDsFromCommonConfigs(configs []*common.ResourceConfig) []string {
	var ids []string
	for _, cfg := range configs {
		if cfg == nil {
			continue
		}
		for _, rx := range []*regexp.Regexp{assetRefRegexp, fileURLRefRegexp} {
			matches := rx.FindAllStringSubmatch(cfg.Content, -1)
			for _, match := range matches {
				if len(match) >= 2 {
					ids = append(ids, match[1])
				}
			}
		}
	}
	return ids
}

// buildFilesPrefix constructs the files directory prefix based on basePath.
// Examples:
//
//	basePath="" -> "asset/file"
//	basePath="/rainbow-bridge" -> "rainbow-bridge/api/v1/asset/file"
func buildFilesPrefix(basePath string) string {
	if basePath == "" {
		return path.Join("api", "v1", "asset", "file")
	}
	// 移除前导的 /
	cleanBase := strings.TrimPrefix(basePath, "/")
	return path.Join(cleanBase, "api", "v1", "asset", "file")
}
