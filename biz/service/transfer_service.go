package service

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	"github.com/yi-nology/rainbow_bridge/biz/model/transfer"
	"gorm.io/gorm"
)

// --------------------- Export/Import operations ---------------------

func (s *Service) ExportConfigs(ctx context.Context, environmentKey, pipelineKey string) ([]*common.ResourceConfig, error) {
	if environmentKey == "" || pipelineKey == "" {
		return nil, errors.New("environment_key and pipeline_key required")
	}

	configs, err := s.logic.ExportConfigs(ctx, environmentKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	return s.decorateConfigList(configSliceToPB(configs)), nil
}

func (s *Service) ImportConfigs(ctx context.Context, configs []*common.ResourceConfig, overwrite bool) error {
	_, err := s.ImportConfigsWithResult(ctx, configs, overwrite)
	return err
}

func (s *Service) writeConfigArchive(ctx context.Context, configs []model.Config) ([]byte, error) {
	buf := &bytes.Buffer{}
	zipWriter := zip.NewWriter(buf)

	// 收集环境和渠道信息
	envSet := make(map[string]bool)
	pipelineSet := make(map[string]map[string]bool) // environment_key -> pipeline_key -> true

	for _, cfg := range configs {
		if cfg.EnvironmentKey != "" {
			envSet[cfg.EnvironmentKey] = true
			if cfg.PipelineKey != "" {
				if _, exists := pipelineSet[cfg.EnvironmentKey]; !exists {
					pipelineSet[cfg.EnvironmentKey] = make(map[string]bool)
				}
				pipelineSet[cfg.EnvironmentKey][cfg.PipelineKey] = true
			}
		}
	}

	// 获取环境详细信息
	envList := make([]model.Environment, 0)
	allEnvs, err := s.logic.ListEnvironments(ctx)
	if err == nil {
		for _, env := range allEnvs {
			if envSet[env.EnvironmentKey] {
				envList = append(envList, env)
			}
		}
	}

	// 获取渠道详细信息
	pipelineList := make([]model.Pipeline, 0)
	for envKey := range pipelineSet {
		envPipelines, err := s.ListPipelines(ctx, envKey, nil)
		if err == nil {
			for _, pl := range envPipelines {
				if pipelineSet[envKey][pl.GetPipelineKey()] {
					pipelineList = append(pipelineList, model.Pipeline{
						EnvironmentKey: envKey,
						PipelineKey:    pl.GetPipelineKey(),
						PipelineName:   pl.GetPipelineName(),
						Description:    pl.GetDescription(),
						IsActive:       pl.GetIsActive(),
					})
				}
			}
		}
	}

	// 收集所有相关资产（包括配置中引用的和环境/渠道下的所有资产）
	allAssets := make([]model.Asset, 0)
	assetIDSet := make(map[string]bool)

	// 1. 收集配置中引用的资产
	assetIDs := extractAssetIDs(configs)
	for _, assetID := range assetIDs {
		if assetIDSet[assetID] {
			continue
		}
		asset, err := s.logic.GetAsset(ctx, assetID)
		if err != nil {
			continue // 跳过不存在的资产
		}
		assetIDSet[assetID] = true
		allAssets = append(allAssets, *asset)
	}

	// 2. 收集环境/渠道下的所有资产
	for envKey, pipes := range pipelineSet {
		for pipeKey := range pipes {
			assets, err := s.logic.ListAssetsByEnvironmentAndPipeline(ctx, envKey, pipeKey)
			if err != nil {
				continue
			}
			for _, asset := range assets {
				if assetIDSet[asset.FileID] {
					continue
				}
				assetIDSet[asset.FileID] = true
				allAssets = append(allAssets, asset)
			}
		}
	}

	// 构建资产元数据列表
	assetMetaList := make([]map[string]any, 0, len(allAssets))
	for _, asset := range allAssets {
		assetMetaList = append(assetMetaList, map[string]any{
			"file_id":         asset.FileID,
			"environment_key": asset.EnvironmentKey,
			"pipeline_key":    asset.PipelineKey,
			"file_name":       asset.FileName,
			"content_type":    asset.ContentType,
			"file_size":       asset.FileSize,
			"remark":          asset.Remark,
		})
	}

	// 构建完整的数据结构
	// Convert configs to a format that handles JSON objects properly
	configsData := archiveConfigData(configs)

	archiveData := map[string]any{
		"environments":     envList,
		"pipelines":        pipelineList,
		"business_configs": configsData,
		"assets":           assetMetaList,
	}

	configData, err := json.MarshalIndent(archiveData, "", "  ")
	if err != nil {
		return nil, err
	}
	metaWriter, err := zipWriter.Create("configs.json")
	if err != nil {
		return nil, err
	}
	if _, err := metaWriter.Write(configData); err != nil {
		return nil, err
	}

	// 写入所有资产文件
	for _, asset := range allAssets {
		fullPath := filepath.Join(dataDirectory, asset.Path)
		file, err := os.Open(fullPath)
		if err != nil {
			continue // 跳过无法读取的文件
		}
		zipPath := filepath.Join("files", asset.FileID, asset.FileName)
		writer, err := zipWriter.CreateHeader(&zip.FileHeader{Name: zipPath, Method: zip.Deflate})
		if err != nil {
			file.Close()
			continue
		}
		if _, err := io.Copy(writer, file); err != nil {
			file.Close()
			continue
		}
		file.Close()
	}

	if err := zipWriter.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (s *Service) ImportConfigsArchive(ctx context.Context, data []byte, targetEnv, targetPipeline string, overwrite bool) ([]*common.ResourceConfig, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	var configs []*common.ResourceConfig
	var archiveData map[string]any
	assetFiles := make(map[string]*zip.File)

	for _, f := range reader.File {
		cleanName := filepath.Clean(f.Name)
		if cleanName == "configs.json" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			payload, err := io.ReadAll(rc)
			closeQuietly(rc)
			if err != nil {
				return nil, err
			}

			// 解析新格式（包含 environments, pipelines, business_configs, assets）
			if err := json.Unmarshal(payload, &archiveData); err != nil {
				return nil, errors.New("invalid configs.json format")
			}

			businessConfigs, ok := archiveData["business_configs"]
			if !ok {
				return nil, errors.New("business_configs field not found in configs.json")
			}

			// 解析 business_configs 字段
			configBytes, _ := json.Marshal(businessConfigs)
			configs, err = decodeArchiveConfigs(configBytes)
			if err != nil {
				return nil, err
			}

			// 规范化图片和文件类型配置的引用格式
			for i := range configs {
				if (configs[i].Type == "image" || configs[i].Type == "file") && configs[i].Content != "" {
					configs[i].Content = normalizeImageReference(configs[i].Content)
				}
			}

			// 如果指定了目标环境和渠道，将所有配置映射到目标环境/渠道
			if targetEnv != "" && targetPipeline != "" {
				for i := range configs {
					configs[i].EnvironmentKey = targetEnv
					configs[i].PipelineKey = targetPipeline
				}
			}

			// 导入 environments（仅当未指定目标环境时）
			if targetEnv == "" {
				if envs, ok := archiveData["environments"]; ok {
					if err := s.importEnvironments(ctx, envs); err != nil {
						// 记录错误但继续导入
						fmt.Printf("Warning: failed to import environments: %v\n", err)
					}
				}
			} else {
				// 确保目标环境存在
				if err := s.ensureEnvironmentExists(ctx, targetEnv); err != nil {
					fmt.Printf("Warning: target environment may not exist: %v\n", err)
				}
			}

			// 导入 pipelines（仅当未指定目标渠道时）
			if targetPipeline == "" {
				if pipes, ok := archiveData["pipelines"]; ok {
					if err := s.importPipelines(ctx, pipes); err != nil {
						// 记录错误但继续导入
						fmt.Printf("Warning: failed to import pipelines: %v\n", err)
					}
				}
			} else {
				// 确保目标渠道存在
				if err := s.ensurePipelineExists(ctx, targetEnv, targetPipeline); err != nil {
					fmt.Printf("Warning: target pipeline may not exist: %v\n", err)
				}
			}
			continue
		}
		if strings.HasPrefix(cleanName, "files/") {
			assetFiles[cleanName] = f
		}
	}

	if configs == nil {
		return nil, errors.New("config content not found in archive")
	}

	// Parse asset metadata from archive
	assetMetaMap := make(map[string]map[string]any) // fileID -> metadata
	if assetsData, ok := archiveData["assets"]; ok {
		assetsBytes, _ := json.Marshal(assetsData)
		var assetMetas []map[string]any
		if err := json.Unmarshal(assetsBytes, &assetMetas); err == nil {
			for _, meta := range assetMetas {
				if fileID, ok := meta["file_id"].(string); ok && fileID != "" {
					assetMetaMap[fileID] = meta
				}
			}
		}
	}

	// Import configs
	configModels := make([]model.Config, 0, len(configs))
	for _, cfg := range configs {
		configModels = append(configModels, *pbConfigToModel(cfg))
	}
	written, err := s.logic.importConfigs(ctx, configModels, overwrite)
	if err != nil {
		return nil, err
	}

	// Restore all assets present in archive
	for path, f := range assetFiles {
		fileID := filepath.Base(filepath.Dir(path))
		fileName := filepath.Base(path)
		rc, err := f.Open()
		if err != nil {
			continue
		}
		fileData, err := io.ReadAll(rc)
		closeQuietly(rc)
		if err != nil {
			continue
		}

		if err := ensureUploadDir(fileID); err != nil {
			continue
		}
		relativePath := filepath.Join(uploadDirectory, fileID, fileName)
		fullPath := filepath.Join(dataDirectory, relativePath)
		if err := os.WriteFile(fullPath, fileData, 0o644); err != nil {
			continue
		}

		// Get asset metadata
		meta, hasMeta := assetMetaMap[fileID]
		envKey := ""
		pipeKey := ""
		contentType := ""
		remark := ""

		if hasMeta {
			if v, ok := meta["environment_key"].(string); ok {
				envKey = v
			}
			if v, ok := meta["pipeline_key"].(string); ok {
				pipeKey = v
			}
			if v, ok := meta["content_type"].(string); ok {
				contentType = v
			}
			if v, ok := meta["remark"].(string); ok {
				remark = v
			}
		}

		// If target env/pipeline specified, use those instead
		if targetEnv != "" && targetPipeline != "" {
			envKey = targetEnv
			pipeKey = targetPipeline
		}

		// Use metadata content type if available, otherwise detect
		if contentType == "" {
			contentType = http.DetectContentType(fileData)
		}

		asset := &model.Asset{
			FileID:         fileID,
			EnvironmentKey: envKey,
			PipelineKey:    pipeKey,
			FileName:       fileName,
			FileSize:       int64(len(fileData)),
			ContentType:    contentType,
			Path:           relativePath,
			Remark:         remark,
		}

		if err := s.logic.UpdateAsset(ctx, asset); err != nil {
			if !errors.Is(err, ErrAssetNotFound) {
				continue
			}
			if err := s.logic.CreateAsset(ctx, asset); err != nil {
				continue
			}
		}
	}

	return s.decorateConfigList(configSliceToPB(written)), nil
}

// importEnvironments 导入环境信息
func (s *Service) importEnvironments(ctx context.Context, data any) error {
	// 将 data 转换为 JSON 再解析
	envBytes, err := json.Marshal(data)
	if err != nil {
		return err
	}

	var environments []model.Environment
	if err := json.Unmarshal(envBytes, &environments); err != nil {
		return err
	}

	for _, env := range environments {
		// 检查环境是否已存在
		existing, err := s.logic.environmentDAO.GetByKey(ctx, s.logic.db, env.EnvironmentKey)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		if existing != nil {
			// 更新已存在的环境
			env.ID = existing.ID
			env.CreatedAt = existing.CreatedAt
			if err := s.logic.environmentDAO.Update(ctx, s.logic.db, &env); err != nil {
				return err
			}
		} else {
			// 创建新环境
			if err := s.logic.environmentDAO.Create(ctx, s.logic.db, &env); err != nil {
				return err
			}
		}
	}

	return nil
}

// importPipelines 导入渠道信息
func (s *Service) importPipelines(ctx context.Context, data any) error {
	// 将 data 转换为 JSON 再解析
	pipeBytes, err := json.Marshal(data)
	if err != nil {
		return err
	}

	var pipelines []model.Pipeline
	if err := json.Unmarshal(pipeBytes, &pipelines); err != nil {
		return err
	}

	for _, pipe := range pipelines {
		// 检查渠道是否已存在
		existing, err := s.logic.pipelineDAO.GetByKey(ctx, s.logic.db, pipe.EnvironmentKey, pipe.PipelineKey)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		if existing != nil {
			// 更新已存在的渠道
			pipe.ID = existing.ID
			pipe.CreatedAt = existing.CreatedAt
			if err := s.logic.pipelineDAO.Update(ctx, s.logic.db, &pipe); err != nil {
				return err
			}
		} else {
			// 创建新渠道
			if err := s.logic.pipelineDAO.Create(ctx, s.logic.db, &pipe); err != nil {
				return err
			}
		}
	}

	return nil
}

// --------------------- Transfer helpers ---------------------

func extractAssetIDs(configs []model.Config) []string {
	ids := make([]string, 0)
	seen := make(map[string]struct{})
	for _, cfg := range configs {
		extract := func(content string) {
			for _, rx := range []*regexp.Regexp{assetRefRegexp, fileURLRefRegexp} {
				matches := rx.FindAllStringSubmatch(content, -1)
				for _, match := range matches {
					if len(match) < 2 {
						continue
					}
					id := match[1]
					if _, ok := seen[id]; ok {
						continue
					}
					seen[id] = struct{}{}
					ids = append(ids, id)
				}
			}
		}
		extract(cfg.Content)
	}
	return ids
}

// ensureEnvironmentExists 确保环境存在，如果不存在则返回错误
func (s *Service) ensureEnvironmentExists(ctx context.Context, envKey string) error {
	_, err := s.logic.environmentDAO.GetByKey(ctx, s.logic.db, envKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("environment %s does not exist", envKey)
		}
		return err
	}
	return nil
}

// ensurePipelineExists 确保渠道存在，如果不存在则返回错误
func (s *Service) ensurePipelineExists(ctx context.Context, envKey, pipelineKey string) error {
	_, err := s.logic.pipelineDAO.GetByKey(ctx, s.logic.db, envKey, pipelineKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("pipeline %s/%s does not exist", envKey, pipelineKey)
		}
		return err
	}
	return nil
}

// normalizeImageReference 规范化图片引用格式
// 支持将旧格式转换为新格式：
// - /api/v1/files/{fileId} -> asset://{fileId}
// - /api/v1/asset/file/{fileId}/{fileName} -> asset://{fileId}
// - /api/v1/asset/file/{fileId} -> asset://{fileId}
// - asset://{fileId} -> asset://{fileId} (保持不变)
// - http(s)://... -> http(s)://... (保持不变)
func normalizeImageReference(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ref
	}

	// 如果已经是 asset:// 格式，保持不变
	if strings.HasPrefix(ref, "asset://") {
		return ref
	}

	// 如果是 http:// 或 https://，保持不变
	if strings.HasPrefix(strings.ToLower(ref), "http://") ||
		strings.HasPrefix(strings.ToLower(ref), "https://") {
		return ref
	}

	// 转换 /api/v1/files/{fileId} 格式
	if strings.HasPrefix(ref, "/api/v1/files/") {
		fileID := strings.TrimPrefix(ref, "/api/v1/files/")
		// 移除可能的文件名部分
		if idx := strings.Index(fileID, "/"); idx > 0 {
			fileID = fileID[:idx]
		}
		return "asset://" + fileID
	}

	// 转换 /api/v1/asset/file/{fileId}/{fileName} 或 /api/v1/asset/file/{fileId} 格式
	if strings.HasPrefix(ref, "/api/v1/asset/file/") {
		parts := strings.Split(strings.TrimPrefix(ref, "/api/v1/asset/file/"), "/")
		if len(parts) > 0 && parts[0] != "" {
			return "asset://" + parts[0]
		}
	}

	// 其他格式保持不变
	return ref
}

// MigrateConfigs migrates configurations between environments/pipelines.
func (s *Service) MigrateConfigs(ctx context.Context, req *transfer.MigrateRequest) (*transfer.MigrateSummary, error) {
	// 1. 验证源和目标环境/渠道存在
	if err := s.ensureEnvironmentExists(ctx, req.SourceEnvironmentKey); err != nil {
		return nil, fmt.Errorf("source environment not found: %w", err)
	}
	if err := s.ensurePipelineExists(ctx, req.SourceEnvironmentKey, req.SourcePipelineKey); err != nil {
		return nil, fmt.Errorf("source pipeline not found: %w", err)
	}
	if err := s.ensureEnvironmentExists(ctx, req.TargetEnvironmentKey); err != nil {
		return nil, fmt.Errorf("target environment not found: %w", err)
	}
	if err := s.ensurePipelineExists(ctx, req.TargetEnvironmentKey, req.TargetPipelineKey); err != nil {
		return nil, fmt.Errorf("target pipeline not found: %w", err)
	}

	// 2. 获取源配置（使用 ListConfigs 而不是 ExportConfigs，因为需要保留 ResourceKey）
	sourceConfigs, err := s.logic.ListConfigs(ctx, req.SourceEnvironmentKey, req.SourcePipelineKey, "", "", "", false)
	if err != nil {
		return nil, err
	}

	// 3. 过滤指定的配置（如果提供了 resource_keys）
	if len(req.ResourceKeys) > 0 {
		resourceKeySet := make(map[string]bool)
		for _, key := range req.ResourceKeys {
			resourceKeySet[key] = true
		}
		filtered := make([]model.Config, 0)
		for _, cfg := range sourceConfigs {
			if resourceKeySet[cfg.ResourceKey] {
				filtered = append(filtered, cfg)
			}
		}
		sourceConfigs = filtered
	}

	// 4. 执行迁移
	summary := &transfer.MigrateSummary{
		Total: int32(len(sourceConfigs)), // #nosec G115 -- count will not exceed int32
		Items: make([]*transfer.MigrateResultItem, 0),
	}

	for _, srcCfg := range sourceConfigs {
		item := &transfer.MigrateResultItem{
			ResourceKey: srcCfg.ResourceKey,
			Name:        srcCfg.Name,
			Alias:       srcCfg.Alias,
		}

		// 检查目标是否存在同别名配置
		existing, _ := s.logic.configDAO.GetByAlias(ctx, s.logic.db,
			req.TargetEnvironmentKey, req.TargetPipelineKey, srcCfg.Alias)

		if existing != nil && !req.Overwrite {
			item.Status = "skipped"
			item.Message = "配置已存在且 overwrite=false"
			summary.Skipped++
			summary.Items = append(summary.Items, item)
			continue
		}

		// 复制配置
		newCfg := srcCfg
		newCfg.ID = 0
		newCfg.EnvironmentKey = req.TargetEnvironmentKey
		newCfg.PipelineKey = req.TargetPipelineKey
		newCfg.ResourceKey = uuid.NewString() // 生成新的 resource_key

		// 复制关联的资源文件
		if err := s.copyConfigAssets(ctx, &newCfg, req.TargetEnvironmentKey, req.TargetPipelineKey); err != nil {
			item.Status = "failed"
			item.Message = err.Error()
			summary.Failed++
			summary.Items = append(summary.Items, item)
			continue
		}

		// 保存配置
		if existing != nil {
			// 更新现有配置
			newCfg.ResourceKey = existing.ResourceKey
			if err := s.logic.UpdateConfig(ctx, &newCfg); err != nil {
				item.Status = "failed"
				item.Message = err.Error()
				summary.Failed++
			} else {
				item.Status = "succeeded"
				summary.Succeeded++
			}
		} else {
			// 创建新配置
			if err := s.logic.AddConfig(ctx, &newCfg); err != nil {
				item.Status = "failed"
				item.Message = err.Error()
				summary.Failed++
			} else {
				item.Status = "succeeded"
				summary.Succeeded++
			}
		}

		summary.Items = append(summary.Items, item)
	}

	return summary, nil
}

// copyConfigAssets copies assets referenced in the config content.
func (s *Service) copyConfigAssets(ctx context.Context, cfg *model.Config, targetEnv, targetPipeline string) error {
	// 提取资源引用
	assetIDs := extractAssetIDsFromContent(cfg.Content)
	if len(assetIDs) == 0 {
		return nil
	}

	// 记录旧 ID 到新 ID 的映射
	idMapping := make(map[string]string)

	for _, oldID := range assetIDs {
		// 获取原资源
		oldAsset, err := s.logic.GetAsset(ctx, oldID)
		if err != nil {
			continue // 跳过不存在的资源
		}

		// 读取原文件
		oldPath := filepath.Join(dataDirectory, oldAsset.Path)
		data, err := os.ReadFile(oldPath)
		if err != nil {
			continue
		}

		// 生成新 ID 并写入文件
		newID := uuid.NewString()
		if err := ensureUploadDir(newID); err != nil {
			return err
		}

		newRelPath := filepath.Join(uploadDirectory, newID, oldAsset.FileName)
		newFullPath := filepath.Join(dataDirectory, newRelPath)
		if err := os.WriteFile(newFullPath, data, 0o644); err != nil {
			return err
		}

		// 创建新资源记录
		newAsset := &model.Asset{
			FileID:         newID,
			EnvironmentKey: targetEnv,
			PipelineKey:    targetPipeline,
			FileName:       oldAsset.FileName,
			FileSize:       oldAsset.FileSize,
			ContentType:    oldAsset.ContentType,
			Path:           newRelPath,
		}
		if err := s.logic.CreateAsset(ctx, newAsset); err != nil {
			return err
		}

		idMapping[oldID] = newID
	}

	// 更新配置中的资源引用
	for oldID, newID := range idMapping {
		cfg.Content = strings.ReplaceAll(cfg.Content, "asset://"+oldID, "asset://"+newID)
	}

	return nil
}

// extractAssetIDsFromContent extracts asset IDs from config content.
func extractAssetIDsFromContent(content string) []string {
	ids := make([]string, 0)
	seen := make(map[string]bool)

	for _, rx := range []*regexp.Regexp{assetRefRegexp, fileURLRefRegexp} {
		matches := rx.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) >= 2 && !seen[match[1]] {
				seen[match[1]] = true
				ids = append(ids, match[1])
			}
		}
	}
	return ids
}

// ==================== Export Tree ====================

// GetExportTree returns the tree structure of environments, pipelines, and configs.
func (s *Service) GetExportTree(ctx context.Context) (*transfer.ExportTreeData, error) {
	// Get all environments
	environments, err := s.logic.ListEnvironments(ctx)
	if err != nil {
		return nil, err
	}

	tree := &transfer.ExportTreeData{
		Environments: make([]*transfer.ExportTreeEnvironment, 0),
	}

	for _, env := range environments {
		// Get pipelines for this environment
		pipelines, err := s.ListPipelines(ctx, env.EnvironmentKey, nil)
		if err != nil {
			continue
		}

		treeEnv := &transfer.ExportTreeEnvironment{
			EnvironmentKey:  env.EnvironmentKey,
			EnvironmentName: env.EnvironmentName,
			Description:     env.Description,
			IsActive:        env.IsActive,
			Pipelines:       make([]*transfer.ExportTreePipeline, 0),
		}

		for _, pipe := range pipelines {
			// Get configs for this pipeline
			configs, err := s.logic.ListConfigs(ctx, env.EnvironmentKey, pipe.GetPipelineKey(), "", "", "", false)
			if err != nil {
				continue
			}

			treePipe := &transfer.ExportTreePipeline{
				PipelineKey:  pipe.GetPipelineKey(),
				PipelineName: pipe.GetPipelineName(),
				Description:  pipe.GetDescription(),
				IsActive:     pipe.GetIsActive(),
				ConfigCount:  int32(len(configs)), // #nosec G115 -- count will not exceed int32
				Configs:      make([]*transfer.ExportTreeConfig, 0),
			}

			// Add config details
			for _, cfg := range configs {
				treePipe.Configs = append(treePipe.Configs, &transfer.ExportTreeConfig{
					ResourceKey: cfg.ResourceKey,
					Name:        cfg.Name,
					Alias:       cfg.Alias,
					Type:        cfg.Type,
				})
			}

			treeEnv.Pipelines = append(treeEnv.Pipelines, treePipe)
		}

		tree.Environments = append(tree.Environments, treeEnv)
	}

	return tree, nil
}

// ==================== Selective Export ====================

// ExportConfigsSelective exports selected configurations.
func (s *Service) ExportConfigsSelective(ctx context.Context, selections []*transfer.ExportSelection, format string) ([]byte, string, error) {
	if len(selections) == 0 {
		return nil, "", errors.New("no selections provided")
	}

	// Collect all configs based on selections
	allConfigs := make([]model.Config, 0)
	processedKeys := make(map[string]bool) // Avoid duplicates

	for _, sel := range selections {
		if sel.EnvironmentKey == "" {
			continue
		}

		if sel.PipelineKey == "" {
			// Export entire environment
			pipelines, err := s.ListPipelines(ctx, sel.EnvironmentKey, nil)
			if err != nil {
				// Skip environment if pipelines cannot be retrieved
				continue
			}
			if len(pipelines) == 0 {
				// Skip environment if no pipelines exist
				continue
			}
			for _, pipe := range pipelines {
				configs, err := s.logic.ExportConfigs(ctx, sel.EnvironmentKey, pipe.GetPipelineKey())
				if err != nil {
					continue
				}
				for _, cfg := range configs {
					// Use Alias for deduplication since ResourceKey is cleared by ExportConfigs
					key := fmt.Sprintf("%s:%s:%s", cfg.EnvironmentKey, cfg.PipelineKey, cfg.Alias)
					if !processedKeys[key] {
						processedKeys[key] = true
						allConfigs = append(allConfigs, cfg)
					}
				}
			}
		} else if len(sel.ResourceKeys) == 0 {
			// Export entire pipeline
			configs, err := s.logic.ExportConfigs(ctx, sel.EnvironmentKey, sel.PipelineKey)
			if err != nil {
				continue
			}
			for _, cfg := range configs {
				// Use Alias for deduplication since ResourceKey is cleared by ExportConfigs
				key := fmt.Sprintf("%s:%s:%s", cfg.EnvironmentKey, cfg.PipelineKey, cfg.Alias)
				if !processedKeys[key] {
					processedKeys[key] = true
					allConfigs = append(allConfigs, cfg)
				}
			}
		} else {
			// Export specific configs
			configs, err := s.logic.ListConfigs(ctx, sel.EnvironmentKey, sel.PipelineKey, "", "", "", false)
			if err != nil {
				continue
			}
			resourceKeySet := make(map[string]bool)
			for _, key := range sel.ResourceKeys {
				resourceKeySet[key] = true
			}
			for _, cfg := range configs {
				if resourceKeySet[cfg.ResourceKey] {
					// Use Alias for deduplication
					key := fmt.Sprintf("%s:%s:%s", cfg.EnvironmentKey, cfg.PipelineKey, cfg.Alias)
					if !processedKeys[key] {
						processedKeys[key] = true
						allConfigs = append(allConfigs, cfg)
					}
				}
			}
		}
	}

	if len(allConfigs) == 0 {
		return nil, "", errors.New("no configs found for the given selections")
	}

	var data []byte
	var filename string
	var err error

	switch format {
	case "tar.gz":
		data, err = s.writeConfigTarGz(ctx, allConfigs)
		filename = fmt.Sprintf("export_%d.tar.gz", len(allConfigs))
	default: // zip
		data, err = s.writeConfigArchive(ctx, allConfigs)
		filename = fmt.Sprintf("export_%d.zip", len(allConfigs))
	}

	if err != nil {
		return nil, "", err
	}

	return data, filename, nil
}

// writeConfigTarGz writes configs to a tar.gz archive.
func (s *Service) writeConfigTarGz(ctx context.Context, configs []model.Config) ([]byte, error) {
	buf := &bytes.Buffer{}
	gzWriter := gzip.NewWriter(buf)
	tarWriter := tar.NewWriter(gzWriter)

	// Collect environment and pipeline info
	envSet := make(map[string]bool)
	pipelineSet := make(map[string]map[string]bool)

	for _, cfg := range configs {
		if cfg.EnvironmentKey != "" {
			envSet[cfg.EnvironmentKey] = true
			if cfg.PipelineKey != "" {
				if _, exists := pipelineSet[cfg.EnvironmentKey]; !exists {
					pipelineSet[cfg.EnvironmentKey] = make(map[string]bool)
				}
				pipelineSet[cfg.EnvironmentKey][cfg.PipelineKey] = true
			}
		}
	}

	// Get environment details
	envList := make([]model.Environment, 0)
	allEnvs, err := s.logic.ListEnvironments(ctx)
	if err == nil {
		for _, env := range allEnvs {
			if envSet[env.EnvironmentKey] {
				envList = append(envList, env)
			}
		}
	}

	// Get pipeline details
	pipelineList := make([]model.Pipeline, 0)
	for envKey := range pipelineSet {
		envPipelines, err := s.ListPipelines(ctx, envKey, nil)
		if err == nil {
			for _, pl := range envPipelines {
				if pipelineSet[envKey][pl.GetPipelineKey()] {
					pipelineList = append(pipelineList, model.Pipeline{
						EnvironmentKey: envKey,
						PipelineKey:    pl.GetPipelineKey(),
						PipelineName:   pl.GetPipelineName(),
						Description:    pl.GetDescription(),
						IsActive:       pl.GetIsActive(),
					})
				}
			}
		}
	}

	// 收集所有相关资产（包括配置中引用的和环境/渠道下的所有资产）
	allAssets := make([]model.Asset, 0)
	assetIDSet := make(map[string]bool)

	// 1. 收集配置中引用的资产
	assetIDs := extractAssetIDs(configs)
	for _, assetID := range assetIDs {
		if assetIDSet[assetID] {
			continue
		}
		asset, err := s.logic.GetAsset(ctx, assetID)
		if err != nil {
			continue
		}
		assetIDSet[assetID] = true
		allAssets = append(allAssets, *asset)
	}

	// 2. 收集环境/渠道下的所有资产
	for envKey, pipes := range pipelineSet {
		for pipeKey := range pipes {
			assets, err := s.logic.ListAssetsByEnvironmentAndPipeline(ctx, envKey, pipeKey)
			if err != nil {
				continue
			}
			for _, asset := range assets {
				if assetIDSet[asset.FileID] {
					continue
				}
				assetIDSet[asset.FileID] = true
				allAssets = append(allAssets, asset)
			}
		}
	}

	// 构建资产元数据列表
	assetMetaList := make([]map[string]any, 0, len(allAssets))
	for _, asset := range allAssets {
		assetMetaList = append(assetMetaList, map[string]any{
			"file_id":         asset.FileID,
			"environment_key": asset.EnvironmentKey,
			"pipeline_key":    asset.PipelineKey,
			"file_name":       asset.FileName,
			"content_type":    asset.ContentType,
			"file_size":       asset.FileSize,
			"remark":          asset.Remark,
		})
	}

	// Build complete data structure
	// Convert configs to a format that handles JSON objects properly
	configsData := archiveConfigData(configs)

	archiveData := map[string]any{
		"environments":     envList,
		"pipelines":        pipelineList,
		"business_configs": configsData,
		"assets":           assetMetaList,
	}

	configData, err := json.MarshalIndent(archiveData, "", "  ")
	if err != nil {
		return nil, err
	}

	// Write configs.json
	header := &tar.Header{
		Name: "configs.json",
		Mode: 0644,
		Size: int64(len(configData)),
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		return nil, err
	}
	if _, err := tarWriter.Write(configData); err != nil {
		return nil, err
	}

	// Write all asset files
	for _, asset := range allAssets {
		fullPath := filepath.Join(dataDirectory, asset.Path)
		fileData, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}

		tarPath := filepath.Join("files", asset.FileID, asset.FileName)
		fileHeader := &tar.Header{
			Name: tarPath,
			Mode: 0644,
			Size: int64(len(fileData)),
		}
		if err := tarWriter.WriteHeader(fileHeader); err != nil {
			return nil, err
		}
		if _, err := tarWriter.Write(fileData); err != nil {
			return nil, err
		}
	}

	if err := tarWriter.Close(); err != nil {
		return nil, err
	}
	if err := gzWriter.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// ==================== Import Preview ====================

// ImportConfigsPreview previews the import file contents and detects conflicts.
func (s *Service) ImportConfigsPreview(ctx context.Context, data []byte, filename string) (*transfer.ImportPreviewData, error) {
	// Detect format from filename
	format := "zip"
	if strings.HasSuffix(strings.ToLower(filename), ".tar.gz") || strings.HasSuffix(strings.ToLower(filename), ".tgz") {
		format = "tar.gz"
	}

	// Parse archive
	var archiveData map[string]any
	var err error

	if format == "tar.gz" {
		archiveData, err = s.parseTarGz(data)
	} else {
		archiveData, err = s.parseZip(data)
	}
	if err != nil {
		return nil, err
	}

	preview := &transfer.ImportPreviewData{
		Format:       format,
		Environments: make([]*transfer.ImportPreviewEnvironment, 0),
		Summary:      &transfer.ImportPreviewSummary{},
	}

	// Parse environments
	envMap := make(map[string]*transfer.ImportPreviewEnvironment)
	if envData, ok := archiveData["environments"]; ok {
		envBytes, _ := json.Marshal(envData)
		var environments []model.Environment
		if err := json.Unmarshal(envBytes, &environments); err == nil {
			for _, env := range environments {
				status := s.checkEnvironmentStatus(ctx, env.EnvironmentKey)
				previewEnv := &transfer.ImportPreviewEnvironment{
					EnvironmentKey:  env.EnvironmentKey,
					EnvironmentName: env.EnvironmentName,
					Status:          status,
					Pipelines:       make([]*transfer.ImportPreviewPipeline, 0),
				}
				envMap[env.EnvironmentKey] = previewEnv
				preview.Environments = append(preview.Environments, previewEnv)
				preview.Summary.TotalEnvironments++
			}
		}
	}

	// Parse pipelines
	pipeMap := make(map[string]*transfer.ImportPreviewPipeline) // key: env_key:pipe_key
	if pipeData, ok := archiveData["pipelines"]; ok {
		pipeBytes, _ := json.Marshal(pipeData)
		var pipelines []model.Pipeline
		if err := json.Unmarshal(pipeBytes, &pipelines); err == nil {
			for _, pipe := range pipelines {
				status := s.checkPipelineStatus(ctx, pipe.EnvironmentKey, pipe.PipelineKey)
				previewPipe := &transfer.ImportPreviewPipeline{
					PipelineKey:  pipe.PipelineKey,
					PipelineName: pipe.PipelineName,
					Status:       status,
					Configs:      make([]*transfer.ImportPreviewConfig, 0),
				}
				pipeKey := fmt.Sprintf("%s:%s", pipe.EnvironmentKey, pipe.PipelineKey)
				pipeMap[pipeKey] = previewPipe

				// Add to environment
				if env, exists := envMap[pipe.EnvironmentKey]; exists {
					env.Pipelines = append(env.Pipelines, previewPipe)
				} else {
					// Environment not in archive, create a placeholder
					newEnv := &transfer.ImportPreviewEnvironment{
						EnvironmentKey:  pipe.EnvironmentKey,
						EnvironmentName: pipe.EnvironmentKey,
						Status:          s.checkEnvironmentStatus(ctx, pipe.EnvironmentKey),
						Pipelines:       []*transfer.ImportPreviewPipeline{previewPipe},
					}
					envMap[pipe.EnvironmentKey] = newEnv
					preview.Environments = append(preview.Environments, newEnv)
				}
				preview.Summary.TotalPipelines++
			}
		}
	}

	// Parse configs
	if configData, ok := archiveData["business_configs"]; ok {
		configBytes, _ := json.Marshal(configData)
		configs, err := decodeArchiveConfigs(configBytes)
		if err != nil {
			return nil, err
		}
		for _, cfg := range configs {
			status := s.checkConfigStatus(ctx, cfg.GetEnvironmentKey(), cfg.GetPipelineKey(), cfg.GetAlias())
			previewCfg := &transfer.ImportPreviewConfig{
				ResourceKey: cfg.GetResourceKey(),
				Name:        cfg.GetName(),
				Alias:       cfg.GetAlias(),
				Type:        cfg.GetType(),
				Status:      status,
			}

			// Add to pipeline
			pipeKey := fmt.Sprintf("%s:%s", cfg.GetEnvironmentKey(), cfg.GetPipelineKey())
			if pipe, exists := pipeMap[pipeKey]; exists {
				pipe.Configs = append(pipe.Configs, previewCfg)
			} else {
				// Pipeline not in archive, create a placeholder
				newPipe := &transfer.ImportPreviewPipeline{
					PipelineKey:  cfg.GetPipelineKey(),
					PipelineName: cfg.GetPipelineKey(),
					Status:       s.checkPipelineStatus(ctx, cfg.GetEnvironmentKey(), cfg.GetPipelineKey()),
					Configs:      []*transfer.ImportPreviewConfig{previewCfg},
				}
				pipeMap[pipeKey] = newPipe

				// Add to environment
				if env, exists := envMap[cfg.GetEnvironmentKey()]; exists {
					env.Pipelines = append(env.Pipelines, newPipe)
				} else {
					newEnv := &transfer.ImportPreviewEnvironment{
						EnvironmentKey:  cfg.GetEnvironmentKey(),
						EnvironmentName: cfg.GetEnvironmentKey(),
						Status:          s.checkEnvironmentStatus(ctx, cfg.GetEnvironmentKey()),
						Pipelines:       []*transfer.ImportPreviewPipeline{newPipe},
					}
					envMap[cfg.GetEnvironmentKey()] = newEnv
					preview.Environments = append(preview.Environments, newEnv)
				}
			}

			preview.Summary.TotalConfigs++
			switch status {
			case "new":
				preview.Summary.NewCount++
			case "exists":
				preview.Summary.ExistingCount++
			case "conflict":
				preview.Summary.ConflictCount++
			}
		}
	}

	// Parse assets (count only, not added to tree)
	if assetData, ok := archiveData["assets"]; ok {
		assetBytes, _ := json.Marshal(assetData)
		var assets []map[string]any
		if err := json.Unmarshal(assetBytes, &assets); err == nil {
			preview.Summary.TotalAssets = int32(len(assets)) // #nosec G115 -- count will not exceed int32
		}
	}

	return preview, nil
}

// parseZip parses a zip archive and returns the archive data.
func (s *Service) parseZip(data []byte) (map[string]any, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	for _, f := range reader.File {
		if filepath.Clean(f.Name) == "configs.json" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			payload, err := io.ReadAll(rc)
			closeQuietly(rc)
			if err != nil {
				return nil, err
			}

			var archiveData map[string]any
			if err := json.Unmarshal(payload, &archiveData); err != nil {
				return nil, errors.New("invalid configs.json format")
			}
			return archiveData, nil
		}
	}

	return nil, errors.New("configs.json not found in archive")
}

// parseTarGz parses a tar.gz archive and returns the archive data.
func (s *Service) parseTarGz(data []byte) (map[string]any, error) {
	gzReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gzReader.Close() }()

	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if filepath.Clean(header.Name) == "configs.json" {
			payload, err := io.ReadAll(tarReader)
			if err != nil {
				return nil, err
			}

			var archiveData map[string]any
			if err := json.Unmarshal(payload, &archiveData); err != nil {
				return nil, errors.New("invalid configs.json format")
			}
			return archiveData, nil
		}
	}

	return nil, errors.New("configs.json not found in archive")
}

// checkEnvironmentStatus checks if an environment exists.
func (s *Service) checkEnvironmentStatus(ctx context.Context, envKey string) string {
	_, err := s.logic.environmentDAO.GetByKey(ctx, s.logic.db, envKey)
	if err != nil {
		return "new"
	}
	return "exists"
}

// checkPipelineStatus checks if a pipeline exists.
func (s *Service) checkPipelineStatus(ctx context.Context, envKey, pipeKey string) string {
	_, err := s.logic.pipelineDAO.GetByKey(ctx, s.logic.db, envKey, pipeKey)
	if err != nil {
		return "new"
	}
	return "exists"
}

// checkConfigStatus checks if a config exists and returns its status.
func (s *Service) checkConfigStatus(ctx context.Context, envKey, pipeKey, alias string) string {
	existing, err := s.logic.configDAO.GetByAlias(ctx, s.logic.db, envKey, pipeKey, alias)
	if err != nil || existing == nil {
		return "new"
	}
	return "conflict"
}

// ==================== Selective Import ====================

// ImportConfigsSelective imports selected configurations from an archive.
func (s *Service) ImportConfigsSelective(ctx context.Context, data []byte, filename string, selections []*transfer.ExportSelection, overwrite bool) ([]*common.ResourceConfig, error) {
	// Detect format from filename
	format := "zip"
	if strings.HasSuffix(strings.ToLower(filename), ".tar.gz") || strings.HasSuffix(strings.ToLower(filename), ".tgz") {
		format = "tar.gz"
	}

	// Build selection set for quick lookup
	selectionSet := make(map[string]map[string]map[string]bool) // env -> pipe -> resourceKey
	selectAll := len(selections) == 0

	for _, sel := range selections {
		if _, exists := selectionSet[sel.EnvironmentKey]; !exists {
			selectionSet[sel.EnvironmentKey] = make(map[string]map[string]bool)
		}
		if sel.PipelineKey == "" {
			// Select all pipelines in this environment
			selectionSet[sel.EnvironmentKey]["*"] = map[string]bool{"*": true}
		} else {
			if _, exists := selectionSet[sel.EnvironmentKey][sel.PipelineKey]; !exists {
				selectionSet[sel.EnvironmentKey][sel.PipelineKey] = make(map[string]bool)
			}
			if len(sel.ResourceKeys) == 0 {
				// Select all configs in this pipeline
				selectionSet[sel.EnvironmentKey][sel.PipelineKey]["*"] = true
			} else {
				for _, key := range sel.ResourceKeys {
					selectionSet[sel.EnvironmentKey][sel.PipelineKey][key] = true
				}
			}
		}
	}

	// Check if a config should be imported
	shouldImport := func(envKey, pipeKey, resourceKey string) bool {
		if selectAll {
			return true
		}
		// Check if environment is selected
		envSel, envExists := selectionSet[envKey]
		if !envExists {
			return false
		}
		// Check if all pipelines are selected
		if _, allPipes := envSel["*"]; allPipes {
			return true
		}
		// Check if pipeline is selected
		pipeSel, pipeExists := envSel[pipeKey]
		if !pipeExists {
			return false
		}
		// Check if all configs are selected
		if pipeSel["*"] {
			return true
		}
		// Check if specific config is selected
		return pipeSel[resourceKey]
	}

	// Parse archive based on format
	if format == "tar.gz" {
		return s.importConfigsFromTarGz(ctx, data, shouldImport, overwrite)
	}
	return s.importConfigsFromZip(ctx, data, shouldImport, overwrite)
}

// importConfigsFromZip imports configs from a zip archive with selection filter.
func (s *Service) importConfigsFromZip(ctx context.Context, data []byte, shouldImport func(string, string, string) bool, overwrite bool) ([]*common.ResourceConfig, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	var allConfigs []*common.ResourceConfig
	var archiveData map[string]any
	assetFiles := make(map[string]*zip.File)

	for _, f := range reader.File {
		cleanName := filepath.Clean(f.Name)
		if cleanName == "configs.json" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			payload, err := io.ReadAll(rc)
			closeQuietly(rc)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(payload, &archiveData); err != nil {
				return nil, errors.New("invalid configs.json format")
			}
		} else if strings.HasPrefix(cleanName, "files/") {
			assetFiles[cleanName] = f
		}
	}

	if archiveData == nil {
		return nil, errors.New("configs.json not found in archive")
	}

	// Import environments
	if envData, ok := archiveData["environments"]; ok {
		if err := s.importEnvironments(ctx, envData); err != nil {
			fmt.Printf("Warning: failed to import environments: %v\n", err)
		}
	}

	// Import pipelines
	if pipeData, ok := archiveData["pipelines"]; ok {
		if err := s.importPipelines(ctx, pipeData); err != nil {
			fmt.Printf("Warning: failed to import pipelines: %v\n", err)
		}
	}

	// Parse asset metadata from archive
	assetMetaMap := make(map[string]map[string]any) // fileID -> metadata
	if assetsData, ok := archiveData["assets"]; ok {
		assetsBytes, _ := json.Marshal(assetsData)
		var assetMetas []map[string]any
		if err := json.Unmarshal(assetsBytes, &assetMetas); err == nil {
			for _, meta := range assetMetas {
				if fileID, ok := meta["file_id"].(string); ok && fileID != "" {
					assetMetaMap[fileID] = meta
				}
			}
		}
	}

	// Parse configs
	if configData, ok := archiveData["business_configs"]; ok {
		configBytes, _ := json.Marshal(configData)
		var err error
		allConfigs, err = decodeArchiveConfigs(configBytes)
		if err != nil {
			return nil, err
		}
	}

	// Filter configs based on selections
	filteredConfigs := make([]*common.ResourceConfig, 0)
	selectedEnvPipes := make(map[string]bool) // "env:pipe" -> true
	for _, cfg := range allConfigs {
		if shouldImport(cfg.GetEnvironmentKey(), cfg.GetPipelineKey(), cfg.GetResourceKey()) {
			// Normalize image and file references
			if (cfg.Type == "image" || cfg.Type == "file") && cfg.Content != "" {
				cfg.Content = normalizeImageReference(cfg.Content)
			}
			filteredConfigs = append(filteredConfigs, cfg)
			// Track selected environment/pipeline combinations
			key := fmt.Sprintf("%s:%s", cfg.GetEnvironmentKey(), cfg.GetPipelineKey())
			selectedEnvPipes[key] = true
		}
	}

	if len(filteredConfigs) == 0 {
		return nil, errors.New("no configs selected for import")
	}

	// Import configs
	configModels := make([]model.Config, 0, len(filteredConfigs))
	for _, cfg := range filteredConfigs {
		configModels = append(configModels, *pbConfigToModel(cfg))
	}

	written, err := s.logic.importConfigs(ctx, configModels, overwrite)
	if err != nil {
		return nil, err
	}

	// Restore all assets that belong to selected environments/pipelines
	for path, f := range assetFiles {
		fileID := filepath.Base(filepath.Dir(path))
		fileName := filepath.Base(path)

		// Get asset metadata
		meta, hasMeta := assetMetaMap[fileID]
		envKey := ""
		pipeKey := ""
		contentType := ""
		remark := ""

		if hasMeta {
			if v, ok := meta["environment_key"].(string); ok {
				envKey = v
			}
			if v, ok := meta["pipeline_key"].(string); ok {
				pipeKey = v
			}
			if v, ok := meta["content_type"].(string); ok {
				contentType = v
			}
			if v, ok := meta["remark"].(string); ok {
				remark = v
			}
		}

		// Check if this asset belongs to a selected environment/pipeline
		key := fmt.Sprintf("%s:%s", envKey, pipeKey)
		if !selectedEnvPipes[key] && envKey != "" && pipeKey != "" {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			continue
		}
		fileData, err := io.ReadAll(rc)
		closeQuietly(rc)
		if err != nil {
			continue
		}

		if err := ensureUploadDir(fileID); err != nil {
			continue
		}

		relativePath := filepath.Join(uploadDirectory, fileID, fileName)
		fullPath := filepath.Join(dataDirectory, relativePath)
		if err := os.WriteFile(fullPath, fileData, 0o644); err != nil {
			continue
		}

		// Use metadata content type if available, otherwise detect
		if contentType == "" {
			contentType = http.DetectContentType(fileData)
		}

		asset := &model.Asset{
			FileID:         fileID,
			EnvironmentKey: envKey,
			PipelineKey:    pipeKey,
			FileName:       fileName,
			FileSize:       int64(len(fileData)),
			ContentType:    contentType,
			Path:           relativePath,
			Remark:         remark,
		}

		if err := s.logic.UpdateAsset(ctx, asset); err != nil {
			if !errors.Is(err, ErrAssetNotFound) {
				continue
			}
			// 资产不存在,创建新资产
			if err := s.logic.CreateAsset(ctx, asset); err != nil {
				// 忽略创建失败的资产
				continue
			}
		}
	}

	return s.decorateConfigList(configSliceToPB(written)), nil
}

// importConfigsFromTarGz imports configs from a tar.gz archive with selection filter.
func (s *Service) importConfigsFromTarGz(ctx context.Context, data []byte, shouldImport func(string, string, string) bool, overwrite bool) ([]*common.ResourceConfig, error) {
	gzReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gzReader.Close() }()

	var allConfigs []*common.ResourceConfig
	var archiveData map[string]any
	assetFiles := make(map[string][]byte) // path -> data

	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		cleanName := filepath.Clean(header.Name)
		if cleanName == "configs.json" {
			payload, err := io.ReadAll(tarReader)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(payload, &archiveData); err != nil {
				return nil, errors.New("invalid configs.json format")
			}
		} else if strings.HasPrefix(cleanName, "files/") {
			fileData, err := io.ReadAll(tarReader)
			if err != nil {
				continue
			}
			assetFiles[cleanName] = fileData
		}
	}

	if archiveData == nil {
		return nil, errors.New("configs.json not found in archive")
	}

	// Import environments
	if envData, ok := archiveData["environments"]; ok {
		if err := s.importEnvironments(ctx, envData); err != nil {
			fmt.Printf("Warning: failed to import environments: %v\n", err)
		}
	}

	// Import pipelines
	if pipeData, ok := archiveData["pipelines"]; ok {
		if err := s.importPipelines(ctx, pipeData); err != nil {
			fmt.Printf("Warning: failed to import pipelines: %v\n", err)
		}
	}

	// Parse asset metadata from archive
	assetMetaMap := make(map[string]map[string]any) // fileID -> metadata
	if assetsData, ok := archiveData["assets"]; ok {
		assetsBytes, _ := json.Marshal(assetsData)
		var assetMetas []map[string]any
		if err := json.Unmarshal(assetsBytes, &assetMetas); err == nil {
			for _, meta := range assetMetas {
				if fileID, ok := meta["file_id"].(string); ok && fileID != "" {
					assetMetaMap[fileID] = meta
				}
			}
		}
	}

	// Parse configs
	if configData, ok := archiveData["business_configs"]; ok {
		configBytes, _ := json.Marshal(configData)
		var err error
		allConfigs, err = decodeArchiveConfigs(configBytes)
		if err != nil {
			return nil, err
		}
	}

	// Filter configs based on selections
	filteredConfigs := make([]*common.ResourceConfig, 0)
	selectedEnvPipes := make(map[string]bool) // "env:pipe" -> true
	for _, cfg := range allConfigs {
		if shouldImport(cfg.GetEnvironmentKey(), cfg.GetPipelineKey(), cfg.GetResourceKey()) {
			if (cfg.Type == "image" || cfg.Type == "file") && cfg.Content != "" {
				cfg.Content = normalizeImageReference(cfg.Content)
			}
			filteredConfigs = append(filteredConfigs, cfg)
			// Track selected environment/pipeline combinations
			key := fmt.Sprintf("%s:%s", cfg.GetEnvironmentKey(), cfg.GetPipelineKey())
			selectedEnvPipes[key] = true
		}
	}

	if len(filteredConfigs) == 0 {
		return nil, errors.New("no configs selected for import")
	}

	// Import configs
	configModels := make([]model.Config, 0, len(filteredConfigs))
	for _, cfg := range filteredConfigs {
		configModels = append(configModels, *pbConfigToModel(cfg))
	}

	written, err := s.logic.importConfigs(ctx, configModels, overwrite)
	if err != nil {
		return nil, err
	}

	// Restore all assets that belong to selected environments/pipelines
	for path, fileData := range assetFiles {
		fileID := filepath.Base(filepath.Dir(path))
		fileName := filepath.Base(path)

		// Get asset metadata
		meta, hasMeta := assetMetaMap[fileID]
		envKey := ""
		pipeKey := ""
		contentType := ""
		remark := ""

		if hasMeta {
			if v, ok := meta["environment_key"].(string); ok {
				envKey = v
			}
			if v, ok := meta["pipeline_key"].(string); ok {
				pipeKey = v
			}
			if v, ok := meta["content_type"].(string); ok {
				contentType = v
			}
			if v, ok := meta["remark"].(string); ok {
				remark = v
			}
		}

		// Check if this asset belongs to a selected environment/pipeline
		key := fmt.Sprintf("%s:%s", envKey, pipeKey)
		if !selectedEnvPipes[key] && envKey != "" && pipeKey != "" {
			continue
		}

		if err := ensureUploadDir(fileID); err != nil {
			continue
		}

		relativePath := filepath.Join(uploadDirectory, fileID, fileName)
		fullPath := filepath.Join(dataDirectory, relativePath)
		if err := os.WriteFile(fullPath, fileData, 0o644); err != nil {
			continue
		}

		// Use metadata content type if available, otherwise detect
		if contentType == "" {
			contentType = http.DetectContentType(fileData)
		}

		asset := &model.Asset{
			FileID:         fileID,
			EnvironmentKey: envKey,
			PipelineKey:    pipeKey,
			FileName:       fileName,
			FileSize:       int64(len(fileData)),
			ContentType:    contentType,
			Path:           relativePath,
			Remark:         remark,
		}

		if err := s.logic.UpdateAsset(ctx, asset); err != nil {
			if !errors.Is(err, ErrAssetNotFound) {
				continue
			}
			// 资产不存在,创建新资产
			if err := s.logic.CreateAsset(ctx, asset); err != nil {
				// 忽略创建失败的资产
				continue
			}
		}
	}

	return s.decorateConfigList(configSliceToPB(written)), nil
}
