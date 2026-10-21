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
	"path"
	"regexp"
	"strings"

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

	prepared, err := s.prepareExportAssets(ctx, configs, true)
	if err != nil {
		return nil, err
	}
	allAssets := make([]model.Asset, 0, len(prepared))
	for _, item := range prepared {
		allAssets = append(allAssets, item.metadata)
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

	for _, item := range prepared {
		name := path.Join("files", item.metadata.FileID, item.metadata.FileName)
		writer, err := zipWriter.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write(item.data); err != nil {
			return nil, err
		}
	}

	if err := zipWriter.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (s *Service) ImportConfigsArchive(ctx context.Context, data []byte, targetEnv, targetPipeline string, overwrite bool) ([]*common.ResourceConfig, error) {
	outcome, err := s.ImportArchiveWithResult(ctx, data, "zip", targetEnv, targetPipeline, nil, overwrite)
	if err != nil {
		return nil, err
	}
	return outcome.Configs, nil
}

// importEnvironments 导入环境信息(写协调事务内执行;既有环境的元数据更新需通过冻结检查)。
func (s *Service) importEnvironments(ctx context.Context, l *Logic, data any) error {
	// 将 data 转换为 JSON 再解析
	envBytes, err := json.Marshal(data)
	if err != nil {
		return err
	}

	var environments []model.Environment
	if err := DecodeConfigJSON(envBytes, &environments); err != nil {
		return err
	}

	for _, env := range environments {
		// 检查环境是否已存在
		existing, err := l.environmentDAO.GetByKey(ctx, l.db, env.EnvironmentKey)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		if existing != nil {
			// 环境内存在 locked 渠道时拒绝写入其元数据
			if err := l.ensureEnvironmentMetadataUnlocked(ctx, env.EnvironmentKey); err != nil {
				return err
			}
			env.ID = existing.ID
			env.CreatedAt = existing.CreatedAt
			if err := l.environmentDAO.Update(ctx, l.db, &env); err != nil {
				return err
			}
		} else {
			if err := l.environmentDAO.Create(ctx, l.db, &env); err != nil {
				return err
			}
		}
	}

	return nil
}

// importPipelines 导入渠道信息(写协调事务内执行;既有渠道更新需通过冻结检查)。
func (s *Service) importPipelines(ctx context.Context, l *Logic, data any) error {
	// 将 data 转换为 JSON 再解析
	pipeBytes, err := json.Marshal(data)
	if err != nil {
		return err
	}

	var pipelines []model.Pipeline
	if err := DecodeConfigJSON(pipeBytes, &pipelines); err != nil {
		return err
	}

	for _, pipe := range pipelines {
		// 检查渠道是否已存在
		existing, err := l.pipelineDAO.GetByKey(ctx, l.db, pipe.EnvironmentKey, pipe.PipelineKey)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		if existing != nil {
			if err := l.ensurePipelineUnlocked(ctx, pipe.EnvironmentKey, pipe.PipelineKey); err != nil {
				return err
			}
			pipe.ID = existing.ID
			pipe.CreatedAt = existing.CreatedAt
			if err := l.pipelineDAO.Update(ctx, l.db, &pipe); err != nil {
				return err
			}
		} else {
			if err := l.pipelineDAO.Create(ctx, l.db, &pipe); err != nil {
				return err
			}
			if err := l.ensurePipelineStateExists(ctx, pipe.EnvironmentKey, pipe.PipelineKey); err != nil {
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

// ensureEnvironmentExists 确保环境存在(只读检查,可在协调事务内复用)。
func (s *Service) ensureEnvironmentExists(ctx context.Context, l *Logic, envKey string) error {
	_, err := l.environmentDAO.GetByKey(ctx, l.db, envKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("environment %s does not exist", envKey)
		}
		return err
	}
	return nil
}

// ensurePipelineExists 确保渠道存在(只读检查,可在协调事务内复用)。
func (s *Service) ensurePipelineExists(ctx context.Context, l *Logic, envKey, pipelineKey string) error {
	_, err := l.pipelineDAO.GetByKey(ctx, l.db, envKey, pipelineKey)
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
	// 1. 只读预检:源和目标环境/渠道存在(错误语义与既有实现一致)
	if err := s.ensureEnvironmentExists(ctx, s.logic, req.SourceEnvironmentKey); err != nil {
		return nil, fmt.Errorf("source environment not found: %w", err)
	}
	if err := s.ensurePipelineExists(ctx, s.logic, req.SourceEnvironmentKey, req.SourcePipelineKey); err != nil {
		return nil, fmt.Errorf("source pipeline not found: %w", err)
	}
	if err := s.ensureEnvironmentExists(ctx, s.logic, req.TargetEnvironmentKey); err != nil {
		return nil, fmt.Errorf("target environment not found: %w", err)
	}
	if err := s.ensurePipelineExists(ctx, s.logic, req.TargetEnvironmentKey, req.TargetPipelineKey); err != nil {
		return nil, fmt.Errorf("target pipeline not found: %w", err)
	}

	var (
		summary      *transfer.MigrateSummary
		written      []model.Config
		createdFiles []createdResourceFile
	)
	// 迁移在写协调事务内执行:目标 locked 时在复制任何资源前整体拒绝;源 locked 只读复制,不受限。
	err := s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		if err := txLogic.ensurePipelineUnlocked(gateCtx, req.TargetEnvironmentKey, req.TargetPipelineKey); err != nil {
			return err
		}
		// 协调内复查存在性(TOCTOU)
		if err := s.ensureEnvironmentExists(gateCtx, txLogic, req.SourceEnvironmentKey); err != nil {
			return fmt.Errorf("source environment not found: %w", err)
		}
		if err := s.ensurePipelineExists(gateCtx, txLogic, req.SourceEnvironmentKey, req.SourcePipelineKey); err != nil {
			return fmt.Errorf("source pipeline not found: %w", err)
		}
		if err := s.ensureEnvironmentExists(gateCtx, txLogic, req.TargetEnvironmentKey); err != nil {
			return fmt.Errorf("target environment not found: %w", err)
		}
		if err := s.ensurePipelineExists(gateCtx, txLogic, req.TargetEnvironmentKey, req.TargetPipelineKey); err != nil {
			return fmt.Errorf("target pipeline not found: %w", err)
		}
		// 2. 获取源配置(保留 ResourceKey;不经缓存,作为写入依据)
		sourceConfigs, err := txLogic.configDAO.ListByEnvironmentAndPipelineWithFilter(gateCtx, txLogic.db,
			req.SourceEnvironmentKey, req.SourcePipelineKey, "", "", "", 0, 0)
		if err != nil {
			return err
		}
		// 3. 过滤指定的配置(如果提供了 resource_keys)
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
		summary = &transfer.MigrateSummary{
			Total: int32(len(sourceConfigs)), // #nosec G115 -- count will not exceed int32
			Items: make([]*transfer.MigrateResultItem, 0),
		}
		for _, srcCfg := range sourceConfigs {
			item := &transfer.MigrateResultItem{
				ResourceKey: srcCfg.ResourceKey,
				Name:        srcCfg.Name,
				Alias:       srcCfg.Alias,
			}
			// 检查目标是否存在同别名配置;非 not-found 错误不能忽略或当成不存在
			existing, err := txLogic.configDAO.GetByAlias(gateCtx, txLogic.db,
				req.TargetEnvironmentKey, req.TargetPipelineKey, srcCfg.Alias)
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("query target config alias=%s: %w", srcCfg.Alias, err)
			}

			if existing != nil && !req.Overwrite {
				item.Status = "skipped"
				item.Message = "配置已存在且 overwrite=false"
				summary.Skipped++
				summary.Items = append(summary.Items, item)
				continue
			}

			// 每项在独立保存点内完成:复制资源→重写引用→创建/更新→回读验证。
			// 失败项回滚本项配置与资源行,并清理本项新建文件;其他项继续。
			writtenCfg, itemFiles, err := s.migrateOneConfig(gateCtx, txLogic, srcCfg, existing, req)
			if err != nil {
				item.Status = "failed"
				item.Message = migrationItemMessage(err, s.cleanupMigratedItemFiles(gateCtx, itemFiles))
				summary.Failed++
				summary.Items = append(summary.Items, item)
				continue
			}
			createdFiles = append(createdFiles, itemFiles...)
			item.Status = "succeeded"
			summary.Succeeded++
			written = append(written, *writtenCfg)
			summary.Items = append(summary.Items, item)
		}
		return nil
	})
	if err != nil {
		logResidualCleanupFailures(s.cleanupMigratedItemFiles(ctx, createdFiles))
		return nil, err
	}
	s.logic.invalidateConfigCaches(context.WithoutCancel(ctx), written)
	return summary, nil
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

	prepared, err := s.prepareExportAssets(ctx, configs, true)
	if err != nil {
		return nil, err
	}
	allAssets := make([]model.Asset, 0, len(prepared))
	for _, item := range prepared {
		allAssets = append(allAssets, item.metadata)
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
	for _, item := range prepared {
		name := path.Join("files", item.metadata.FileID, item.metadata.FileName)
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(item.data))}); err != nil {
			return nil, err
		}
		if _, err := tarWriter.Write(item.data); err != nil {
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
		if err := DecodeConfigJSON(envBytes, &environments); err == nil {
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
		if err := DecodeConfigJSON(pipeBytes, &pipelines); err == nil {
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
		if err := DecodeConfigJSON(assetBytes, &assets); err == nil {
			preview.Summary.TotalAssets = int32(len(assets)) // #nosec G115 -- count will not exceed int32
		}
	}

	return preview, nil
}

// parseZip parses a zip archive and returns the archive data.
func (s *Service) parseZip(data []byte) (map[string]any, error) {
	archive, err := readConfigArchive(data, "zip")
	if err != nil {
		return nil, err
	}
	return archive.metadata, nil
}
func (s *Service) parseTarGz(data []byte) (map[string]any, error) {
	archive, err := readConfigArchive(data, "tar.gz")
	if err != nil {
		return nil, err
	}
	return archive.metadata, nil
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
	outcome, err := s.ImportConfigsSelectiveWithResult(ctx, data, filename, selections, overwrite)
	if err != nil {
		return nil, err
	}
	return outcome.Configs, nil
}

func (s *Service) ImportConfigsSelectiveWithResult(ctx context.Context, data []byte, filename string, selections []*transfer.ExportSelection, overwrite bool) (*ArchiveImportOutcome, error) {
	format := "zip"
	if strings.HasSuffix(strings.ToLower(filename), ".tar.gz") || strings.HasSuffix(strings.ToLower(filename), ".tgz") {
		format = "tar.gz"
	}
	for _, sel := range selections {
		if sel == nil {
			return nil, errors.New("nil selection")
		}
	}
	selected := func(env, pipe, key string) bool {
		if len(selections) == 0 {
			return true
		}
		for _, sel := range selections {
			if sel.EnvironmentKey != env {
				continue
			}
			if sel.PipelineKey == "" {
				return true
			}
			if sel.PipelineKey != pipe {
				continue
			}
			if len(sel.ResourceKeys) == 0 {
				return true
			}
			for _, candidate := range sel.ResourceKeys {
				if candidate == key {
					return true
				}
			}
		}
		return false
	}
	return s.ImportArchiveWithResult(ctx, data, format, "", "", selected, overwrite)
}
