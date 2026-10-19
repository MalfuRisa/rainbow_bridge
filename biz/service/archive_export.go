package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/transfer"
)

type IncompleteExportError struct {
	Issues []*transfer.ResourceImportIssue
}

func (e *IncompleteExportError) Error() string {
	return fmt.Sprintf("export resources incomplete: %d missing", len(e.Issues))
}

type exportAsset struct {
	metadata model.Asset
	data     []byte
}

// Materialize and verify the complete resource set before constructing a package.
func (s *Service) prepareExportAssets(ctx context.Context, configs []model.Config, includeScope bool) ([]exportAsset, error) {
	ids := map[string]bool{}
	scopes := map[[2]string]bool{}
	for _, id := range extractAssetIDs(configs) {
		ids[id] = true
	}
	for _, cfg := range configs {
		scopes[[2]string{cfg.EnvironmentKey, cfg.PipelineKey}] = true
	}
	if includeScope {
		for scope := range scopes {
			assets, err := s.logic.ListAssetsByEnvironmentAndPipeline(ctx, scope[0], scope[1])
			if err != nil {
				return nil, err
			}
			for _, asset := range assets {
				ids[asset.FileID] = true
			}
		}
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	result := make([]exportAsset, 0, len(sorted))
	issues := make([]*transfer.ResourceImportIssue, 0)
	for _, id := range sorted {
		asset, file, err := s.GetAssetFile(ctx, id)
		if errors.Is(err, ErrAssetNotFound) || errors.Is(err, os.ErrNotExist) || isMissingObject(err) {
			issues = append(issues, &transfer.ResourceImportIssue{FileId: id, Path: path.Join("files", id), Reason: "missing_resource"})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read export resource %s: %w", id, err)
		}
		if _, _, err := resourceDiskPath(asset.FileID, asset.FileName); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(file)
		if s.config != nil && s.config.Storage.Type == "minio" {
			os.Remove(file)
		}
		if os.IsNotExist(err) {
			issues = append(issues, &transfer.ResourceImportIssue{FileId: id, Path: path.Join("files", id, asset.FileName), Reason: "missing_file"})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read export resource %s: %w", id, err)
		}
		result = append(result, exportAsset{metadata: *asset, data: data})
	}
	if len(issues) > 0 {
		return nil, &IncompleteExportError{Issues: issues}
	}
	return result, nil
}
