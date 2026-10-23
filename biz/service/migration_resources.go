package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/transfer"
	"gorm.io/gorm"
)

// 迁移资源复制、回读验证与失败清理(PRD RB-MIGRATE-01 §3.3/§3.4)。
// 资源元数据始终使用传入的事务/保存点连接;文件落点与当前 storage 模式一致,
// 新建文件登记后由项级(保存点回滚)与请求级(外层提交失败)两处清理。

// migrationCleanupTimeout 是残留清理的有界期限(独立于已取消的请求上下文)。
const migrationCleanupTimeout = 15 * time.Second

// migrationHookForTest 是测试专用确定性故障注入点(生产为 nil),
// 与 resourceRestoreHookForTest 同一约定:便于精确覆盖
// "资源已写入但元数据/配置保存失败""回读验证失败"等清理与回滚时序。
// 注入点在同一保存点连接上执行,保持事务语义。
var migrationHookForTest func(stage string, sp *Logic, cfg *model.Config) error

func runMigrationHook(stage string, sp *Logic, cfg *model.Config) error {
	if migrationHookForTest == nil {
		return nil
	}
	return migrationHookForTest(stage, sp, cfg)
}

// migrateOneConfig 在独立保存点内完成一项配置的迁移:
// 复制资源 → 重写引用 → 创建或更新目标 → 回读验证。
// 失败时保存点回滚本项的配置与资源行;返回的本项新建文件由调用方清理。
func (s *Service) migrateOneConfig(gateCtx context.Context, txLogic *Logic, srcCfg model.Config, existing *model.Config, req *transfer.MigrateRequest) (*model.Config, []createdResourceFile, error) {
	newCfg := srcCfg
	newCfg.ID = 0
	newCfg.EnvironmentKey = req.TargetEnvironmentKey
	newCfg.PipelineKey = req.TargetPipelineKey
	newCfg.ResourceKey = uuid.NewString()
	if existing != nil {
		// overwrite:保留目标主键、resource_key 与 alias,只更新内容与资源
		newCfg.ResourceKey = existing.ResourceKey
	}
	itemFiles := make([]createdResourceFile, 0)
	sourceScope := FreezeScopeRef{EnvironmentKey: srcCfg.EnvironmentKey, PipelineKey: srcCfg.PipelineKey}
	targetScope := FreezeScopeRef{EnvironmentKey: req.TargetEnvironmentKey, PipelineKey: req.TargetPipelineKey}
	err := txLogic.inSavepoint(func(sp *Logic) error {
		created, mapping, err := s.migrateConfigResources(gateCtx, sp, &newCfg, sourceScope, targetScope)
		itemFiles = append(itemFiles, created...)
		if err != nil {
			return err
		}
		if err := s.writeMigratedConfig(gateCtx, sp, &newCfg, existing != nil); err != nil {
			return err
		}
		if err := runMigrationHook("config_written", sp, &newCfg); err != nil {
			return &migrationFailure{alias: newCfg.Alias, kind: migrationFailureReadbackFailed, cause: err}
		}
		return s.verifyMigratedConfig(gateCtx, sp, newCfg.ResourceKey, req.TargetEnvironmentKey, req.TargetPipelineKey, newCfg.Content, mapping)
	})
	if err != nil {
		return nil, itemFiles, withMigrationAlias(err, newCfg.Alias)
	}
	return &newCfg, itemFiles, nil
}

// migrateConfigResources 按计划复制本项受管资源并重写 cfg.Content。
// 源资源按源范围校验,新资源落点使用目标范围;
// 返回本项新建文件清单(即使随后失败也需清理)与旧 ID→目标落点映射。
func (s *Service) migrateConfigResources(ctx context.Context, sp *Logic, cfg *model.Config, sourceScope, targetScope FreezeScopeRef) ([]createdResourceFile, map[string]migrationAssetTarget, error) {
	plan, err := planMigrationReferences(cfg, s.basePath)
	if err != nil {
		return nil, nil, err
	}
	if !plan.hasReferences() {
		return nil, nil, nil
	}
	files := make([]createdResourceFile, 0, len(plan.fileIDs))
	mapping := make(map[string]migrationAssetTarget, len(plan.fileIDs))
	for _, oldID := range plan.fileIDs {
		target, itemFiles, err := s.copyOneConfigAsset(ctx, sp, oldID, cfg.Alias, sourceScope, targetScope)
		files = append(files, itemFiles...)
		if err != nil {
			return files, nil, err
		}
		mapping[oldID] = target
	}
	if err := runMigrationHook("assets_copied", sp, cfg); err != nil {
		return files, nil, &migrationFailure{alias: cfg.Alias, kind: migrationFailureConfigWriteFailed, cause: err}
	}
	rewritten, err := rewriteMigrationContent(cfg, s.basePath, mapping)
	if err != nil {
		return files, nil, err
	}
	createdTargets := make(map[string]migrationAssetTarget, len(mapping))
	for _, target := range mapping {
		createdTargets[target.fileID] = target
	}
	if err := verifyMigratedReferences(rewritten, normalizeConfigTypeString(cfg.Type), s.basePath, createdTargets); err != nil {
		return files, nil, err
	}
	cfg.Content = rewritten
	return files, mapping, nil
}

// copyOneConfigAsset 复制单个源资源到目标范围,并回读校验目标字节一致。
func (s *Service) copyOneConfigAsset(ctx context.Context, sp *Logic, oldID, alias string, sourceScope, targetScope FreezeScopeRef) (migrationAssetTarget, []createdResourceFile, error) {
	source, payload, err := s.readMigrationSourceAsset(ctx, sp, oldID, sourceScope)
	if err != nil {
		return migrationAssetTarget{}, nil, err
	}
	fileID := uuid.NewString()
	fileName := source.FileName
	if fileName == "" {
		fileName = fileID
	}
	write, err := s.writeAssetPayload(ctx, fileID, fileName, &FileUploadInput{
		EnvironmentKey: targetScope.EnvironmentKey,
		PipelineKey:    targetScope.PipelineKey,
		FileName:       fileName,
		ContentType:    source.ContentType,
		Data:           payload,
	})
	if err != nil {
		return migrationAssetTarget{}, nil, &migrationFailure{fileID: oldID, kind: migrationFailureAssetWriteFailed, cause: err}
	}
	files := []createdResourceFile{s.trackedMigratedFile(fileID, alias, write)}
	if err := runMigrationHook("asset_written", sp, nil); err != nil {
		return migrationAssetTarget{}, files, &migrationFailure{fileID: oldID, kind: migrationFailureAssetWriteFailed, cause: err}
	}
	record := &model.Asset{
		FileID:         fileID,
		EnvironmentKey: targetScope.EnvironmentKey,
		PipelineKey:    targetScope.PipelineKey,
		FileName:       fileName,
		ContentType:    write.contentType,
		FileSize:       int64(len(payload)),
		Path:           write.relativePath,
		Remark:         source.Remark,
	}
	if err := sp.CreateAsset(ctx, record); err != nil {
		return migrationAssetTarget{}, files, &migrationFailure{fileID: oldID, kind: migrationFailureAssetWriteFailed, cause: err}
	}
	if err := s.verifyMigratedAssetBytes(ctx, write.relativePath, payload, oldID); err != nil {
		return migrationAssetTarget{}, files, err
	}
	return migrationAssetTarget{fileID: fileID, fileName: source.FileName}, files, nil
}

// readMigrationSourceAsset 校验源资源:元数据必须存在、属于源配置范围且文件可读。
// 源配置本身已跨范围引用第三方资源时明确失败,不顺带复制第三方范围资源。
func (s *Service) readMigrationSourceAsset(ctx context.Context, sp *Logic, oldID string, sourceScope FreezeScopeRef) (*model.Asset, []byte, error) {
	source, err := sp.GetAsset(ctx, oldID)
	if err != nil {
		if errors.Is(err, ErrAssetNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, &migrationFailure{fileID: oldID, kind: migrationFailureAssetNotFound, cause: err}
		}
		return nil, nil, &migrationFailure{fileID: oldID, kind: migrationFailureAssetReadFailed, cause: err}
	}
	if source.EnvironmentKey != sourceScope.EnvironmentKey || source.PipelineKey != sourceScope.PipelineKey {
		return nil, nil, &migrationFailure{fileID: oldID, kind: migrationFailureAssetScopeMismatch}
	}
	payload, err := s.readManagedAssetFileBytes(ctx, source)
	if err != nil {
		kind := migrationFailureAssetReadFailed
		if errors.Is(err, os.ErrNotExist) {
			kind = migrationFailureAssetFileMissing
		}
		return nil, nil, &migrationFailure{fileID: oldID, kind: kind, cause: err}
	}
	return source, payload, nil
}

// writeMigratedConfig 写入目标配置:overwrite 时更新既有目标,否则新增。
func (s *Service) writeMigratedConfig(ctx context.Context, sp *Logic, cfg *model.Config, overwrite bool) error {
	kind := migrationFailureConfigWriteFailed
	if overwrite {
		if err := sp.overwriteConfig(ctx, cfg); err != nil {
			return &migrationFailure{alias: cfg.Alias, kind: kind, cause: err}
		}
		return nil
	}
	if err := sp.AddConfig(ctx, cfg); err != nil {
		return &migrationFailure{alias: cfg.Alias, kind: kind, cause: err}
	}
	return nil
}

// trackedMigratedFile 把受管写入结果登记为可清理的本次新建文件。
func (s *Service) trackedMigratedFile(fileID, alias string, payload assetPayload) createdResourceFile {
	if s.config != nil && s.config.Storage.Type == "minio" {
		return createdResourceFile{kind: "minio", path: payload.relativePath, fileID: fileID, alias: alias}
	}
	return createdResourceFile{kind: "local", path: filepath.Join(dataDirectory, payload.relativePath), fileID: fileID, alias: alias}
}

// verifyMigratedAssetBytes 回读目标落点,确认文件可读且字节与源一致。
func (s *Service) verifyMigratedAssetBytes(ctx context.Context, relativePath string, want []byte, sourceID string) error {
	got, err := s.readManagedAssetFileBytes(ctx, &model.Asset{Path: relativePath})
	if err != nil {
		return &migrationFailure{fileID: sourceID, kind: migrationFailureAssetVerifyFailed, cause: err}
	}
	if !bytes.Equal(got, want) {
		return &migrationFailure{fileID: sourceID, kind: migrationFailureAssetVerifyFailed, cause: errors.New("migrated asset bytes mismatch")}
	}
	return nil
}

// verifyMigratedConfig 在同一保存点连接上回读目标配置与新建资源行(不使用缓存),
// 确认重写内容已落库且资源归属目标范围;验证失败按该项失败处理。
func (s *Service) verifyMigratedConfig(ctx context.Context, sp *Logic, resourceKey, environmentKey, pipelineKey, expectedContent string, created map[string]migrationAssetTarget) error {
	stored, err := sp.configDAO.GetByResourceKey(ctx, sp.db, environmentKey, pipelineKey, resourceKey)
	if err != nil {
		return &migrationFailure{kind: migrationFailureReadbackFailed, cause: err}
	}
	if stored.Content != strings.TrimSpace(expectedContent) {
		return &migrationFailure{kind: migrationFailureReadbackFailed, cause: errors.New("stored content mismatch")}
	}
	for _, target := range created {
		asset, err := sp.GetAsset(ctx, target.fileID)
		if err != nil {
			return &migrationFailure{fileID: target.fileID, kind: migrationFailureReadbackFailed, cause: err}
		}
		if asset.EnvironmentKey != environmentKey || asset.PipelineKey != pipelineKey {
			return &migrationFailure{fileID: target.fileID, kind: migrationFailureReadbackFailed, cause: errors.New("asset scope mismatch")}
		}
	}
	return nil
}

// cleanupMigratedItemFiles 用独立于已取消请求的有界上下文清理本项新建文件,
// 返回可定位的残留提示;清理失败不掩盖原始失败,但必须让调用方知道。
func (s *Service) cleanupMigratedItemFiles(ctx context.Context, files []createdResourceFile) []string {
	if len(files) == 0 {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), migrationCleanupTimeout)
	defer cancel()
	return s.cleanupCreatedResourceFilesReport(cleanupCtx, files)
}

// logResidualCleanupFailures 在整体失败、无法随 items 返回时记录可定位的残留日志。
func logResidualCleanupFailures(notes []string) {
	for _, note := range notes {
		fmt.Printf("migration cleanup: %s\n", note)
	}
}
