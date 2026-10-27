package service

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path"
	"strings"
	"time"
)

type transferExportProducer struct {
	AppVersion string `json:"app_version"`
	GitCommit  string `json:"git_commit"`
}

type transferExportSource struct {
	Kind         string                 `json:"kind"`
	CapturedAt   string                 `json:"captured_at"`
	Producer     transferExportProducer `json:"producer"`
	Scopes       []ExportScopeRef       `json:"scopes"`
	SourceFormat string                 `json:"source_archive_format"`
}

func writeTransferArchive(ctx context.Context, plan *ExportPlan,
	producerVersion, producerCommit string) (*ExportResult, error) {
	if plan.Estimated > transferExportLimitBytes {
		return nil, transferLimitExceeded("export exceeds the uncompressed output limit")
	}
	data, err := buildTransferArchiveJSON(plan, producerVersion, producerCommit)
	if err != nil {
		return nil, err
	}
	if int64(len(data))+plan.Estimated > transferExportLimitBytes {
		return nil, transferLimitExceeded("export exceeds the uncompressed output limit")
	}

	extension := ".zip"
	if plan.Format == "tar.gz" {
		extension = ".tar.gz"
	}
	fileName := planFileName(plan, extension)
	temp, err := os.CreateTemp("", transferExportTempPrefix+"*"+extension)
	if err != nil {
		return nil, err
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = temp.Close()
			_ = os.Remove(tempPath)
		}
	}()

	if plan.Format == "tar.gz" {
		err = writeTarGzArchive(ctx, temp, data, plan)
	} else {
		err = writeZipArchive(ctx, temp, data, plan)
	}
	if err != nil {
		return nil, err
	}
	if err := temp.Sync(); err != nil {
		return nil, err
	}
	if err := temp.Close(); err != nil {
		return nil, err
	}
	info, err := os.Stat(tempPath)
	if err != nil {
		return nil, err
	}
	cleanup = false
	return &ExportResult{
		TempPath: tempPath, FileName: fileName, Source: plan.Source,
		Version: plan.Version, SnapshotID: plan.SnapshotID, Size: info.Size(),
	}, nil
}

func writeZipArchive(ctx context.Context, target *os.File, configJSON []byte, plan *ExportPlan) error {
	writer := zip.NewWriter(target)
	if err := writeZipBytes(writer, "configs.json", configJSON); err != nil {
		return err
	}
	for _, asset := range plan.Assets {
		if err := ctx.Err(); err != nil {
			return transferTimeout("export cancelled")
		}
		header := &zip.FileHeader{
			Name:   archiveResourceName(asset.metadata.FileID, asset.metadata.FileName),
			Method: zip.Deflate,
		}
		header.Modified = time.Now().UTC()
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		if _, err := entry.Write(asset.data); err != nil {
			return err
		}
	}
	return writer.Close()
}

func writeZipBytes(writer *zip.Writer, name string, data []byte) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.Modified = time.Now().UTC()
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}

func writeTarGzArchive(ctx context.Context, target *os.File, configJSON []byte, plan *ExportPlan) error {
	gzipWriter := gzip.NewWriter(target)
	writer := tar.NewWriter(gzipWriter)
	if err := writeTarBytes(writer, "configs.json", configJSON); err != nil {
		return err
	}
	for _, asset := range plan.Assets {
		if err := ctx.Err(); err != nil {
			return transferTimeout("export cancelled")
		}
		if err := writeTarBytes(writer, archiveResourceName(asset.metadata.FileID, asset.metadata.FileName), asset.data); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return gzipWriter.Close()
}

func writeTarBytes(writer *tar.Writer, name string, data []byte) error {
	header := &tar.Header{
		Name: name, Mode: 0o600, Size: int64(len(data)),
		ModTime: time.Now().UTC(),
	}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	_, err := writer.Write(data)
	return err
}

func buildTransferArchiveJSON(plan *ExportPlan, version, commit string) ([]byte, error) {
	environments := make([]map[string]any, 0)
	pipelines := make([]map[string]any, 0)
	seenEnv := map[string]bool{}
	seenPipeline := map[string]bool{}
	for _, scope := range plan.Scopes {
		if !seenEnv[scope.EnvironmentID] {
			seenEnv[scope.EnvironmentID] = true
			environments = append(environments, map[string]any{
				"environment_id": scope.EnvironmentID, "environment_key": scope.EnvironmentKey,
				"environment_name": scope.EnvironmentName, "is_active": true,
			})
		}
		key := scope.EnvironmentID + "\x00" + scope.ScopeID
		if !seenPipeline[key] {
			seenPipeline[key] = true
			pipelines = append(pipelines, map[string]any{
				"scope_id": scope.ScopeID, "environment_id": scope.EnvironmentID,
				"environment_key": scope.EnvironmentKey, "pipeline_key": scope.PipelineKey,
				"pipeline_name": scope.PipelineName, "is_active": true,
			})
		}
	}
	assetMetaList := make([]map[string]any, 0, len(plan.Assets))
	resources := make([]map[string]any, 0, len(plan.Assets))
	for _, asset := range plan.Assets {
		assetMetaList = append(assetMetaList, map[string]any{
			"file_id": asset.metadata.FileID, "scope_id": asset.metadata.ScopeID,
			"environment_key": asset.metadata.EnvironmentKey,
			"pipeline_key":    asset.metadata.PipelineKey,
			"file_name":       asset.metadata.FileName, "content_type": asset.metadata.ContentType,
			"file_size": asset.metadata.FileSize, "remark": asset.metadata.Remark,
		})
		resources = append(resources, map[string]any{
			"file_id": asset.metadata.FileID, "file_name": asset.metadata.FileName,
			"content_type": asset.metadata.ContentType,
			"size":         asset.metadata.FileSize, "sha256": asset.sha256,
		})
	}
	source := transferExportSource{
		Kind: plan.Source, CapturedAt: plan.CapturedAt.UTC().Format(time.RFC3339),
		Producer: transferExportProducer{AppVersion: version, GitCommit: commit},
		Scopes:   plan.Scopes, SourceFormat: transferArchiveFormatV2,
	}
	archive := map[string]any{
		"archive_format":   transferArchiveFormatV2,
		"export_source":    source,
		"environments":     environments,
		"pipelines":        pipelines,
		"business_configs": archiveConfigData(plan.Configs),
		"assets":           assetMetaList,
		"resources":        resources,
		"warnings":         append([]string{}, plan.Warnings...),
	}
	return json.MarshalIndent(archive, "", "  ")
}

func archiveBaseName(fileName string) string {
	name := strings.ReplaceAll(fileName, "\\", "/")
	name = path.Base(name)
	if name == "." || name == "/" || name == "" {
		return "file"
	}
	return name
}

func archiveResourceName(fileID, fileName string) string {
	return path.Join("files", fileID, archiveBaseName(fileName))
}

func planFileName(plan *ExportPlan, extension string) string {
	parts := []string{"rainbow-transfer", plan.Source}
	if plan.Version != "" {
		parts = append(parts, sanitizeFileNamePart(plan.Version))
	}
	if plan.SnapshotID != "" {
		short := plan.SnapshotID
		if len(short) > 8 {
			short = short[:8]
		}
		parts = append(parts, sanitizeFileNamePart(short))
	}
	return strings.Join(parts, "-") + extension
}

func sanitizeFileNamePart(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	if builder.Len() == 0 {
		return "export"
	}
	return builder.String()
}
