package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	configdb "github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	"github.com/yi-nology/rainbow_bridge/biz/model/transfer"
	"gorm.io/gorm"
)

type ArchiveImportOutcome struct {
	Configs   []*common.ResourceConfig
	Resources *transfer.ResourceImportResult
}

type resourceRestorePlan struct {
	meta            model.Asset
	archivePath     string
	data            []byte
	missingMetadata bool
	outsideScope    bool
}

// importTargetChecks 汇总一次归档导入将触及的冻结检查项:
// 将写入的范围(映射后的配置目标 + 包内渠道元数据目标),以及需要做"环境内无冻结渠道"检查的环境。
type importTargetChecks struct {
	scopes          []FreezeScopeRef
	environmentKeys []string
}

// ImportArchiveWithResult 在单次写协调存续期内完成:
// 整包校验 → 冻结复查(含包准备阶段将修改的环境／渠道) → 环境／渠道准备 → 配置提交 → 资源恢复。
// 配置可提交而部分资源失败的部分成功协议保留;协调释放发生在资源处理结果确定之后。
func (s *Service) ImportArchiveWithResult(ctx context.Context, data []byte, format, targetEnv, targetPipeline string, selected func(string, string, string) bool, overwrite bool) (*ArchiveImportOutcome, error) {
	return s.ImportArchiveWithIdentity(ctx, data, format, targetEnv, targetPipeline, "", "", selected, overwrite)
}

// ImportArchiveWithIdentity validates the target generation once inside the
// write gate and binds the whole import to it.
func (s *Service) ImportArchiveWithIdentity(ctx context.Context, data []byte, format, targetEnv, targetPipeline,
	expectedEnvironmentID, expectedScopeID string, selected func(string, string, string) bool,
	overwrite bool) (*ArchiveImportOutcome, error) {
	archive, err := readConfigArchive(data, format)
	if err != nil {
		return nil, newTransferExportError(400, ErrTypeTransferInvalidRequest,
			"invalid transfer archive: "+err.Error())
	}
	configs, sourceScopes, err := collectImportConfigs(archive, selected, targetEnv, targetPipeline)
	if err != nil {
		return nil, err
	}
	plan, err := buildResourceRestorePlan(archive, configs, sourceScopes, targetEnv, targetPipeline)
	if err != nil {
		return nil, err
	}
	checks, err := collectImportTargetChecks(archive, configs, selected, targetEnv, targetPipeline)
	if err != nil {
		return nil, err
	}
	models := make([]model.Config, 0, len(configs))
	for _, cfg := range configs {
		m := pbConfigToModel(cfg)
		s.normalizeStoredConfig(m)
		models = append(models, *m)
	}

	var (
		written      []model.Config
		resources    *transfer.ResourceImportResult
		createdFiles []createdResourceFile
	)
	err = s.withWriteGate(ctx, func(gateCtx context.Context, txLogic *Logic, _ *gorm.DB) error {
		if err := ensureImportTargetsUnlocked(gateCtx, txLogic, checks); err != nil {
			return err
		}
		if expectedEnvironmentID != "" && targetEnv != "" {
			if _, err := requireEnvironmentIdentity(gateCtx, txLogic, targetEnv, expectedEnvironmentID); err != nil {
				return err
			}
		}
		if expectedScopeID != "" && targetEnv != "" && targetPipeline != "" {
			if _, err := requireScopeIdentity(gateCtx, txLogic, targetEnv, targetPipeline, expectedScopeID); err != nil {
				return err
			}
		}
		if err := s.prepareImportMetadata(gateCtx, txLogic, archive, selected, targetEnv, targetPipeline); err != nil {
			return err
		}
		if archive.transferV2 && targetEnv != "" && targetPipeline != "" {
			scopeID, err := configdb.ActiveScopeID(gateCtx, txLogic.db, targetEnv, targetPipeline)
			if err != nil {
				return err
			}
			models, plan = remapV2ImportResources(models, plan, scopeID)
		}
		if _, err := txLogic.prepareConfigImport(gateCtx, models); err != nil {
			return err
		}
		writtenConfigs, err := txLogic.importConfigs(gateCtx, models, overwrite)
		if err != nil {
			return err
		}
		written = writtenConfigs
		resources = &transfer.ResourceImportResult{
			Status:   "complete",
			Expected: int32(len(plan)), // #nosec G115 -- entry count stays far below int32
			Issues:   make([]*transfer.ResourceImportIssue, 0),
		}
		for index := range plan {
			status, reason := s.restorePlannedResource(gateCtx, txLogic, plan[index], &createdFiles)
			switch status {
			case "restored":
				resources.Restored++
			case "reused":
				resources.Reused++
			default:
				resources.Failed++
				resources.Status = "partial"
				resources.Issues = append(resources.Issues, &transfer.ResourceImportIssue{
					FileId: plan[index].meta.FileID,
					Path:   plan[index].archivePath,
					Reason: reason,
				})
			}
		}
		return nil
	})
	if err != nil {
		// 提交失败/超时回滚:清理本次新建、未被引用的文件(不删除既有共享资源)
		s.cleanupCreatedResourceFiles(ctx, createdFiles)
		return nil, err
	}
	s.logic.invalidateConfigCaches(context.WithoutCancel(ctx), written)
	return &ArchiveImportOutcome{
		Configs:   s.decorateConfigList(configSliceToPB(written)),
		Resources: resources,
	}, nil
}

// collectImportConfigs 解码 business_configs、按选择过滤并映射到目标范围。
func collectImportConfigs(archive *configArchive, selected func(string, string, string) bool, targetEnv, targetPipeline string) ([]*common.ResourceConfig, map[[2]string]bool, error) {
	raw, ok := archive.metadata["business_configs"]
	if !ok {
		return nil, nil, errors.New("business_configs required")
	}
	encoded, err := jsonContent(raw)
	if err != nil {
		return nil, nil, err
	}
	all, err := decodeArchiveConfigs([]byte(encoded))
	if err != nil {
		return nil, nil, err
	}
	configs := make([]*common.ResourceConfig, 0, len(all))
	sourceScopes := map[[2]string]bool{}
	for _, cfg := range all {
		if selected != nil && !selected(cfg.EnvironmentKey, cfg.PipelineKey, cfg.ResourceKey) {
			continue
		}
		sourceScopes[[2]string{cfg.EnvironmentKey, cfg.PipelineKey}] = true
		if targetEnv != "" && targetPipeline != "" {
			cfg.EnvironmentKey = targetEnv
			cfg.PipelineKey = targetPipeline
		}
		configs = append(configs, cfg)
	}
	if len(configs) == 0 {
		return nil, nil, errors.New("no configs selected for import")
	}
	return configs, sourceScopes, nil
}

// collectImportTargetChecks 计算冻结检查集合。
// 非选择导入时,包内 environments／pipelines 元数据也会被写入,必须一并检查;
// 目标模式(targetEnv/targetPipeline)只做存在性检查,不写元数据。
func collectImportTargetChecks(archive *configArchive, configs []*common.ResourceConfig, selected func(string, string, string) bool, targetEnv, targetPipeline string) (importTargetChecks, error) {
	checks := importTargetChecks{}
	seen := map[FreezeScopeRef]bool{}
	addPipeline := func(environmentKey, pipelineKey string) {
		if environmentKey == "" || pipelineKey == "" {
			return
		}
		ref := FreezeScopeRef{EnvironmentKey: environmentKey, PipelineKey: pipelineKey}
		if seen[ref] {
			return
		}
		seen[ref] = true
		checks.scopes = append(checks.scopes, ref)
	}
	for _, cfg := range configs {
		addPipeline(cfg.EnvironmentKey, cfg.PipelineKey)
	}
	if selected != nil {
		return checks, nil
	}
	if targetPipeline == "" {
		pipelines, err := decodeArchivePipelines(archive)
		if err != nil {
			return checks, err
		}
		for _, pipe := range pipelines {
			addPipeline(pipe.EnvironmentKey, pipe.PipelineKey)
		}
	}
	if targetEnv == "" {
		environments, err := decodeArchiveEnvironments(archive)
		if err != nil {
			return checks, err
		}
		envSeen := map[string]bool{}
		for _, env := range environments {
			if env.EnvironmentKey != "" && !envSeen[env.EnvironmentKey] {
				envSeen[env.EnvironmentKey] = true
				checks.environmentKeys = append(checks.environmentKeys, env.EnvironmentKey)
			}
		}
	}
	return checks, nil
}

func decodeArchiveEnvironments(archive *configArchive) ([]model.Environment, error) {
	value, ok := archive.metadata["environments"]
	if !ok {
		return nil, nil
	}
	encoded, err := jsonContent(value)
	if err != nil {
		return nil, err
	}
	var environments []model.Environment
	if err := DecodeConfigJSON([]byte(encoded), &environments); err != nil {
		return nil, fmt.Errorf("invalid environment metadata: %w", err)
	}
	return environments, nil
}

func decodeArchivePipelines(archive *configArchive) ([]model.Pipeline, error) {
	value, ok := archive.metadata["pipelines"]
	if !ok {
		return nil, nil
	}
	encoded, err := jsonContent(value)
	if err != nil {
		return nil, err
	}
	var pipelines []model.Pipeline
	if err := DecodeConfigJSON([]byte(encoded), &pipelines); err != nil {
		return nil, fmt.Errorf("invalid pipeline metadata: %w", err)
	}
	return pipelines, nil
}

// ensureImportTargetsUnlocked 在协调事务内复查全部将触及的目标:
// 范围级检查 + 环境元数据级检查(环境已存在且包将更新其元数据时)。
func ensureImportTargetsUnlocked(ctx context.Context, l *Logic, checks importTargetChecks) error {
	if err := l.ensureScopesUnlocked(ctx, checks.scopes); err != nil {
		return err
	}
	for _, environmentKey := range checks.environmentKeys {
		exists, err := l.environmentDAO.ExistsByKey(ctx, l.db, environmentKey)
		if err != nil {
			return err
		}
		if !exists {
			continue // 新建环境不涉及已冻结范围
		}
		if err := l.ensureEnvironmentMetadataUnlocked(ctx, environmentKey); err != nil {
			return err
		}
	}
	return nil
}

// prepareImportMetadata 执行环境/渠道元数据准备;选择性导入跳过该阶段。
func (s *Service) prepareImportMetadata(ctx context.Context, l *Logic, archive *configArchive, selected func(string, string, string) bool, targetEnv, targetPipeline string) error {
	if selected != nil {
		return nil
	}
	if targetEnv != "" {
		if err := s.ensureEnvironmentExists(ctx, l, targetEnv); err != nil {
			return err
		}
	} else if envs, ok := archive.metadata["environments"]; ok {
		if err := s.importEnvironments(ctx, l, envs); err != nil {
			return err
		}
	}
	if targetPipeline != "" {
		if err := s.ensurePipelineExists(ctx, l, targetEnv, targetPipeline); err != nil {
			return err
		}
	} else if pipes, ok := archive.metadata["pipelines"]; ok {
		if err := s.importPipelines(ctx, l, pipes); err != nil {
			return err
		}
	}
	return nil
}

// restorePlannedResource 在同一协调事务内写文件 + 元数据并回读校验。
// created 记录本次新建文件,供提交失败回滚时清理;既有文件绝不删除。
func (s *Service) restorePlannedResource(ctx context.Context, l *Logic, item resourceRestorePlan, created *[]createdResourceFile) (string, string) {
	if resourceRestoreHookForTest != nil {
		resourceRestoreHookForTest("resource_stage")
	}
	if item.missingMetadata {
		return "failed", "missing_metadata"
	}
	if item.outsideScope {
		return "failed", "resource_scope_conflict"
	}
	if item.data == nil {
		return "failed", "missing_file"
	}
	meta := item.meta
	existing, err := l.GetAsset(ctx, meta.FileID)
	if err != nil && !errors.Is(err, ErrAssetNotFound) {
		return "failed", "metadata_read_failed"
	}
	if existing != nil {
		if existing.EnvironmentKey != meta.EnvironmentKey || existing.PipelineKey != meta.PipelineKey || existing.FileName != meta.FileName {
			return "failed", "resource_conflict"
		}
		current, readErr := s.readManagedAssetFileBytes(ctx, existing)
		if readErr == nil {
			if bytes.Equal(current, item.data) {
				return "reused", ""
			}
			return "failed", "resource_conflict"
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return "failed", "file_read_failed"
		}
	}
	cleanup := func() error { return nil }
	var track *createdResourceFile
	if s.config != nil && s.config.Storage.Type == "minio" {
		client, err := s.getMinioClient()
		if err != nil {
			return "failed", "storage_unavailable"
		}
		object := path.Join(meta.EnvironmentKey, meta.PipelineKey, meta.FileID, meta.FileName)
		// Never overwrite an untracked remote object.
		if _, err := client.StatObject(ctx, s.config.Storage.Minio.Bucket, object, minio.StatObjectOptions{}); err == nil {
			return "failed", "untracked_file_conflict"
		} else if !isMissingObject(err) {
			return "failed", "file_read_failed"
		}
		if _, err := client.PutObject(ctx, s.config.Storage.Minio.Bucket, object, bytes.NewReader(item.data), int64(len(item.data)), minio.PutObjectOptions{ContentType: meta.ContentType}); err != nil {
			return "failed", "file_write_failed"
		}
		meta.Path = object
		cleanup = func() error {
			return client.RemoveObject(ctx, s.config.Storage.Minio.Bucket, object, minio.RemoveObjectOptions{})
		}
		track = &createdResourceFile{kind: "minio", path: object}
	} else {
		relative, full, err := resourceDiskPath(meta.FileID, meta.FileName)
		if err != nil {
			return "failed", "invalid_path"
		}
		if current, err := os.ReadFile(full); err == nil {
			if !bytes.Equal(current, item.data) {
				return "failed", "untracked_file_conflict"
			}
			// 既有同字节文件只读复用,不记为本次新建
		} else if !os.IsNotExist(err) {
			return "failed", "file_read_failed"
		} else {
			if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
				return "failed", "file_write_failed"
			}
			f, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
			if err != nil {
				return "failed", "file_write_failed"
			}
			_, writeErr := f.Write(item.data)
			closeErr := f.Close()
			if writeErr != nil || closeErr != nil {
				os.Remove(full)
				return "failed", "file_write_failed"
			}
			cleanup = func() error { return os.Remove(full) }
			track = &createdResourceFile{kind: "local", path: full}
		}
		meta.Path = relative
	}
	meta.FileSize = int64(len(item.data))
	if meta.ContentType == "" {
		meta.ContentType = http.DetectContentType(item.data)
	}
	// 元数据写入放在保存点内:单条资源失败不毒化外层协调事务(PG 语义),部分成功协议保持。
	writeErr := l.inSavepoint(func(sp *Logic) error {
		if existing == nil {
			return sp.CreateAsset(ctx, &meta)
		}
		return sp.UpdateAsset(ctx, &meta)
	})
	if writeErr != nil {
		if cleanupErr := cleanup(); cleanupErr != nil {
			return "failed", "metadata_write_failed_orphan_file"
		}
		return "failed", "metadata_write_failed"
	}
	if track != nil {
		*created = append(*created, *track)
	}
	// Read back metadata and bytes before reporting restoration(事务内可见自身写入)。
	_, savedBytes, err := s.readManagedAssetBytesByID(ctx, l, meta.FileID)
	if err != nil || !bytes.Equal(savedBytes, item.data) {
		return "failed", "restore_verification_failed"
	}
	return "restored", ""
}

// buildResourceRestorePlan 汇总归档内资源与引用,生成确定性恢复计划(纯解析,不写库)。
func buildResourceRestorePlan(archive *configArchive, configs []*common.ResourceConfig, scopes map[[2]string]bool, targetEnv, targetPipeline string) ([]resourceRestorePlan, error) {
	metadata := []model.Asset{}
	if value, ok := archive.metadata["assets"]; ok {
		encoded, err := jsonContent(value)
		if err != nil {
			return nil, err
		}
		if err := DecodeConfigJSON([]byte(encoded), &metadata); err != nil {
			return nil, fmt.Errorf("invalid asset metadata: %w", err)
		}
	}
	byID := map[string]model.Asset{}
	for _, meta := range metadata {
		if meta.FileID == "" {
			return nil, errors.New("asset metadata file_id required")
		}
		if _, exists := byID[meta.FileID]; exists {
			return nil, fmt.Errorf("ambiguous resource metadata file_id=%s", meta.FileID)
		}
		if meta.FileName != "" {
			if _, _, err := resourceDiskPath(meta.FileID, meta.FileName); err != nil {
				return nil, err
			}
		} else if name, err := archiveEntryName(meta.FileID); err != nil || name != meta.FileID || strings.Contains(name, "/") {
			return nil, errors.New("invalid resource file_id")
		}
		byID[meta.FileID] = meta
	}
	// The same ID cannot refer to two filenames, even if the metadata names one.
	fileByID := map[string]string{}
	for name := range archive.files {
		parts := splitResourceEntry(name)
		if old, ok := fileByID[parts[0]]; ok && old != name {
			return nil, fmt.Errorf("ambiguous resource entries file_id=%s", parts[0])
		}
		fileByID[parts[0]] = name
	}
	for id, name := range fileByID {
		if meta, ok := byID[id]; ok && meta.FileName != "" && name != path.Join("files", id, meta.FileName) {
			return nil, fmt.Errorf("resource filename disagrees with metadata: %s", id)
		}
	}
	wanted := map[string]bool{}
	for _, id := range extractAssetIDsFromCommonConfigs(configs) {
		wanted[id] = true
	}
	for _, meta := range metadata {
		if scopes[[2]string{meta.EnvironmentKey, meta.PipelineKey}] {
			wanted[meta.FileID] = true
		}
	}
	ids := make([]string, 0, len(wanted))
	for id := range wanted {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	plan := make([]resourceRestorePlan, 0, len(ids))
	for _, id := range ids {
		meta, found := byID[id]
		entry := fileByID[id]
		if !found {
			meta.FileID = id
			if entry != "" {
				meta.FileName = path.Base(entry)
			}
		} else if meta.FileName != "" {
			expected := path.Join("files", id, meta.FileName)
			if entry != "" && entry != expected {
				return nil, fmt.Errorf("resource filename disagrees with metadata: %s", id)
			}
			entry = expected
		}
		if entry == "" {
			entry = path.Join("files", id)
		}
		if targetEnv != "" && targetPipeline != "" {
			meta.EnvironmentKey = targetEnv
			meta.PipelineKey = targetPipeline
		}
		plan = append(plan, resourceRestorePlan{meta: meta, archivePath: entry, data: archive.files[entry], missingMetadata: !found || meta.FileName == "" || meta.EnvironmentKey == "" || meta.PipelineKey == "", outsideScope: found && !scopes[[2]string{byID[id].EnvironmentKey, byID[id].PipelineKey}]})
	}
	return plan, nil
}

// remapV2ImportResources gives every imported resource a target-scope identity
// and rewrites config references. Legacy archives keep their original IDs.
func remapV2ImportResources(models []model.Config, plan []resourceRestorePlan, targetScopeID string) ([]model.Config, []resourceRestorePlan) {
	mapping := make(map[string]string, len(plan))
	for i := range plan {
		oldID := plan[i].meta.FileID
		if oldID == "" {
			continue
		}
		newID := stableTransferImportFileID(targetScopeID, oldID)
		mapping[oldID] = newID
		plan[i].meta.FileID = newID
	}
	if len(mapping) == 0 {
		return models, plan
	}
	rewriteURL := regexp.MustCompile(`(/api/v1/asset/file/)([a-zA-Z0-9\-]+)`)
	rewrite := func(value string) string {
		value = assetRefRegexp.ReplaceAllStringFunc(value, func(match string) string {
			oldID := strings.TrimPrefix(match, assetScheme)
			if newID, ok := mapping[oldID]; ok {
				return assetScheme + newID
			}
			return match
		})
		return rewriteURL.ReplaceAllStringFunc(value, func(match string) string {
			parts := rewriteURL.FindStringSubmatch(match)
			if len(parts) == 3 {
				if newID, ok := mapping[parts[2]]; ok {
					return parts[1] + newID
				}
			}
			return match
		})
	}
	for i := range models {
		models[i].Content = rewrite(models[i].Content)
	}
	return models, plan
}

func stableTransferImportFileID(targetScopeID, archiveFileID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL,
		[]byte("rainbow-transfer-import-v2|"+targetScopeID+"|"+archiveFileID)).String()
}

func splitResourceEntry(name string) []string {
	relative := name[len("files/"):]
	return []string{path.Dir(relative), path.Base(relative)}
}

func isMissingObject(err error) bool {
	response := minio.ToErrorResponse(err)
	return response.Code == "NoSuchKey" || response.Code == "NoSuchObject" || response.Code == "NotFound"
}
