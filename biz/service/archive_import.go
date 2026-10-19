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
	"sort"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/model/common"
	"github.com/yi-nology/rainbow_bridge/biz/model/transfer"
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

func (s *Service) ImportArchiveWithResult(ctx context.Context, data []byte, format, targetEnv, targetPipeline string, selected func(string, string, string) bool, overwrite bool) (*ArchiveImportOutcome, error) {
	archive, err := readConfigArchive(data, format)
	if err != nil {
		return nil, err
	}
	raw, ok := archive.metadata["business_configs"]
	if !ok {
		return nil, errors.New("business_configs required")
	}
	encoded, err := jsonContent(raw)
	if err != nil {
		return nil, err
	}
	all, err := decodeArchiveConfigs([]byte(encoded))
	if err != nil {
		return nil, err
	}
	configs := make([]*common.ResourceConfig, 0, len(all))
	scopes := map[[2]string]bool{}
	for _, cfg := range all {
		if selected != nil && !selected(cfg.EnvironmentKey, cfg.PipelineKey, cfg.ResourceKey) {
			continue
		}
		scopes[[2]string{cfg.EnvironmentKey, cfg.PipelineKey}] = true
		if targetEnv != "" && targetPipeline != "" {
			cfg.EnvironmentKey = targetEnv
			cfg.PipelineKey = targetPipeline
		}
		configs = append(configs, cfg)
	}
	if len(configs) == 0 {
		return nil, errors.New("no configs selected for import")
	}
	plan, err := buildResourceRestorePlan(archive, configs, scopes, targetEnv, targetPipeline)
	if err != nil {
		return nil, err
	}
	// Validate the entire config plan before even the independent scope setup.
	models := make([]model.Config, 0, len(configs))
	for _, cfg := range configs {
		m := pbConfigToModel(cfg)
		s.normalizeStoredConfig(m)
		models = append(models, *m)
	}
	if _, err := s.logic.prepareConfigImport(ctx, models); err != nil {
		return nil, err
	}
	if selected == nil {
		if targetEnv != "" {
			if err := s.ensureEnvironmentExists(ctx, targetEnv); err != nil {
				return nil, err
			}
		} else if envs, ok := archive.metadata["environments"]; ok {
			if err := s.importEnvironments(ctx, envs); err != nil {
				return nil, err
			}
		}
		if targetPipeline != "" {
			if err := s.ensurePipelineExists(ctx, targetEnv, targetPipeline); err != nil {
				return nil, err
			}
		} else if pipes, ok := archive.metadata["pipelines"]; ok {
			if err := s.importPipelines(ctx, pipes); err != nil {
				return nil, err
			}
		}
	}
	written, err := s.ImportConfigsWithResult(ctx, configs, overwrite)
	if err != nil {
		return nil, err
	}
	resources := &transfer.ResourceImportResult{Status: "complete", Expected: int32(len(plan)), Issues: make([]*transfer.ResourceImportIssue, 0)}
	for _, item := range plan {
		status, reason := s.restorePlannedResource(ctx, item)
		switch status {
		case "restored":
			resources.Restored++
		case "reused":
			resources.Reused++
		default:
			resources.Failed++
			resources.Status = "partial"
			resources.Issues = append(resources.Issues, &transfer.ResourceImportIssue{FileId: item.meta.FileID, Path: item.archivePath, Reason: reason})
		}
	}
	return &ArchiveImportOutcome{Configs: written, Resources: resources}, nil
}

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

func splitResourceEntry(name string) []string {
	relative := name[len("files/"):]
	return []string{path.Dir(relative), path.Base(relative)}
}

func (s *Service) restorePlannedResource(ctx context.Context, item resourceRestorePlan) (string, string) {
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
	existing, err := s.logic.GetAsset(ctx, meta.FileID)
	if err != nil && !errors.Is(err, ErrAssetNotFound) {
		return "failed", "metadata_read_failed"
	}
	if existing != nil {
		if existing.EnvironmentKey != meta.EnvironmentKey || existing.PipelineKey != meta.PipelineKey || existing.FileName != meta.FileName {
			return "failed", "resource_conflict"
		}
		_, existingPath, readErr := s.GetAssetFile(ctx, meta.FileID)
		if readErr == nil {
			current, err := os.ReadFile(existingPath)
			if s.config != nil && s.config.Storage.Type == "minio" {
				os.Remove(existingPath)
			}
			if err != nil {
				return "failed", "file_read_failed"
			}
			if bytes.Equal(current, item.data) {
				return "reused", ""
			}
			return "failed", "resource_conflict"
		}
		if !errors.Is(readErr, os.ErrNotExist) && !errors.Is(readErr, ErrAssetNotFound) {
			return "failed", "file_read_failed"
		}
	}
	relative, full, err := resourceDiskPath(meta.FileID, meta.FileName)
	if err != nil {
		return "failed", "invalid_path"
	}
	cleanup := func() error { return nil }
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
	} else {
		if current, err := os.ReadFile(full); err == nil {
			if !bytes.Equal(current, item.data) {
				return "failed", "untracked_file_conflict"
			}
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
		}
		meta.Path = relative
	}
	meta.FileSize = int64(len(item.data))
	if meta.ContentType == "" {
		meta.ContentType = http.DetectContentType(item.data)
	}
	if existing == nil {
		err = s.logic.CreateAsset(ctx, &meta)
	} else {
		err = s.logic.UpdateAsset(ctx, &meta)
	}
	if err != nil {
		if cleanupErr := cleanup(); cleanupErr != nil {
			return "failed", "metadata_write_failed_orphan_file"
		}
		return "failed", "metadata_write_failed"
	}
	// Read back metadata and bytes before reporting restoration.
	saved, savedPath, err := s.GetAssetFile(ctx, meta.FileID)
	if err != nil || saved == nil {
		return "failed", "restore_verification_failed"
	}
	actual, err := os.ReadFile(savedPath)
	if s.config != nil && s.config.Storage.Type == "minio" {
		os.Remove(savedPath)
	}
	if err != nil || !bytes.Equal(actual, item.data) {
		return "failed", "restore_verification_failed"
	}
	return "restored", ""
}

func isMissingObject(err error) bool {
	response := minio.ToErrorResponse(err)
	return response.Code == "NoSuchKey" || response.Code == "NoSuchObject" || response.Code == "NotFound"
}
