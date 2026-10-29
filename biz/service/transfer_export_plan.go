package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	configdb "github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

// PreviewExport resolves the requested source and reports counts without
// generating an archive.
func (s *Service) PreviewExport(ctx context.Context, request ExportRequest) (*ExportPreview, error) {
	plan, err := s.resolveExportPlan(ctx, request, false)
	if err != nil {
		return nil, err
	}
	return &ExportPreview{
		Source:     plan.Source,
		CapturedAt: plan.CapturedAt.UTC().Format(time.RFC3339),
		Scopes:     plan.Scopes,
		Configs:    len(plan.Configs),
		Assets:     len(plan.Assets),
		Bytes:      plan.Estimated,
		Warnings:   append([]string{}, plan.Warnings...),
		Version:    plan.Version,
		SnapshotID: plan.SnapshotID,
	}, nil
}

// ExportWithSource captures a source and writes one migration archive.
func (s *Service) ExportWithSource(ctx context.Context, request ExportRequest) (*ExportResult, error) {
	ctx, cancel := context.WithTimeout(ctx, transferExportDefaultTime)
	defer cancel()
	plan, err := s.resolveExportPlan(ctx, request, true)
	if err == nil {
		return writeTransferArchive(ctx, plan, s.producerVersion, s.producerCommit)
	}
	if ctx.Err() != nil {
		return nil, transferTimeout("export timed out")
	}
	return nil, err
}

func (s *Service) resolveExportPlan(ctx context.Context, request ExportRequest, loadAssets bool) (*ExportPlan, error) {
	request.Source = strings.ToLower(strings.TrimSpace(request.Source))
	if request.Source == "" {
		request.Source = TransferSourceEditor
	}
	if request.Format = strings.ToLower(strings.TrimSpace(request.Format)); request.Format == "" {
		request.Format = "zip"
	}
	if request.Format != "zip" && request.Format != "tar.gz" {
		return nil, transferInvalidRequest("format must be zip or tar.gz")
	}
	switch request.Source {
	case TransferSourceEditor:
		if strings.TrimSpace(request.Version) != "" || strings.TrimSpace(request.ExpectedSnapshotID) != "" {
			return nil, transferInvalidRequest("editor source must not include version or expected_snapshot_id")
		}
		return s.resolveEditorExportPlan(ctx, request, loadAssets)
	case TransferSourceActive, TransferSourceVersion:
		if len(request.Selections) > 0 {
			return nil, transferInvalidRequest("release sources must not include editor selections")
		}
		return s.resolveReleaseExportPlan(ctx, request, loadAssets)
	default:
		return nil, transferInvalidRequest("source must be editor, active or version")
	}
}

type editorScopeSelection struct {
	scope        model.Pipeline
	environment  model.Environment
	resourceKeys map[string]bool
}

func (s *Service) resolveEditorExportPlan(ctx context.Context, request ExportRequest, loadAssets bool) (*ExportPlan, error) {
	selections := request.Selections
	if len(selections) == 0 {
		return nil, transferInvalidRequest("selections cannot be empty for editor source")
	}
	plan := &ExportPlan{
		Source: TransferSourceEditor, CapturedAt: time.Now().UTC(),
		Scopes: []ExportScopeRef{}, Configs: []model.Config{}, Assets: []exportAssetData{},
		Warnings: []string{},
	}
	var err error
	capture := func(l *Logic) error {
		seenConfigs := map[[2]string]bool{}
		seenAssets := map[string]bool{}
		scopeSet := map[string]bool{}
		for _, selection := range selections {
			resolved, err := s.resolveEditorSelections(ctx, l, selection)
			if err != nil {
				return err
			}
			for _, item := range resolved {
				if !scopeSet[item.scope.ScopeID] {
					scopeSet[item.scope.ScopeID] = true
					plan.Scopes = append(plan.Scopes, exportScopeFromRows(item.environment, item.scope))
				}
				configs, err := s.captureEditorConfigs(ctx, l, item)
				if err != nil {
					return err
				}
				for _, cfg := range configs {
					key := [2]string{cfg.ScopeID, cfg.ResourceKey}
					if seenConfigs[key] {
						continue
					}
					seenConfigs[key] = true
					plan.Configs = append(plan.Configs, cfg)
					for _, fileID := range extractAssetIDsFromContent(cfg.Content) {
						if seenAssets[fileID] {
							continue
						}
						asset, err := l.GetAsset(ctx, fileID)
						if err != nil {
							return transferIncomplete("resource " + fileID + " is missing")
						}
						if asset.ScopeID != cfg.ScopeID {
							return transferIncomplete("resource " + fileID + " belongs to another scope")
						}
						entry := exportAssetData{metadata: *asset}
						if loadAssets {
							data, err := s.readManagedAssetFileBytes(ctx, asset)
							if err != nil {
								return transferIncomplete("resource " + fileID + " is unreadable")
							}
							entry.data = data
							entry.metadata.FileSize = int64(len(data))
							digest := sha256.Sum256(data)
							entry.sha256 = hex.EncodeToString(digest[:])
						}
						seenAssets[fileID] = true
						plan.Assets = append(plan.Assets, entry)
						plan.Estimated += asset.FileSize + int64(len(cfg.Content))
					}
				}
			}
		}
		return nil
	}
	if loadAssets {
		err = s.withWriteGate(ctx, func(gateCtx context.Context, l *Logic, _ *gorm.DB) error {
			return capture(l)
		})
	} else {
		err = capture(s.logic)
	}
	if err != nil {
		return nil, err
	}
	plan.Estimated += int64(len(plan.Configs) * 256)
	if plan.Estimated > transferExportLimitBytes {
		return nil, transferLimitExceeded("export plan exceeds the output limit")
	}
	plan.Format = request.Format
	plan.Warnings = append(plan.Warnings, editorWarnings(plan.Configs)...)
	return plan, nil
}

// resolveEditorSelections expands one selection to concrete live scopes.
func (s *Service) resolveEditorSelections(ctx context.Context, l *Logic, selection *ExportSelection) ([]editorScopeSelection, error) {
	if selection == nil {
		return nil, transferInvalidRequest("selection must not be nil")
	}
	envKey := strings.TrimSpace(selection.EnvironmentKey)
	pipelineKey := strings.TrimSpace(selection.PipelineKey)
	expectedEnvID := strings.TrimSpace(selection.ExpectedEnvironmentID)
	expectedScopeID := strings.TrimSpace(selection.ExpectedScopeID)
	resourceKeys := make(map[string]bool, len(selection.ResourceKeys))
	for _, key := range selection.ResourceKeys {
		if strings.TrimSpace(key) != "" {
			resourceKeys[key] = true
		}
	}

	var environments []model.Environment
	switch {
	case expectedEnvID != "":
		env, err := configdb.EnvironmentByIDIncludingDeleted(ctx, l.db, expectedEnvID)
		if err != nil {
			return nil, scopeNotFoundError("environment not found")
		}
		if env.DeletedAt.Valid || env.ActiveSlot == nil || envKey != "" && env.EnvironmentKey != envKey {
			return nil, scopeDeletedError("environment has been deleted")
		}
		environments = []model.Environment{*env}
	case envKey == "":
		all, err := l.environmentDAO.List(ctx, l.db, nil, 0, 0)
		if err != nil {
			return nil, err
		}
		environments = all
	default:
		env, err := resolveEnvironmentByKey(ctx, l, envKey)
		if err != nil {
			return nil, err
		}
		environments = []model.Environment{*env}
	}
	if len(environments) == 0 {
		return nil, transferSelectionNotFound("no environment matches the selection")
	}

	result := make([]editorScopeSelection, 0)
	for _, env := range environments {
		if expectedScopeID != "" {
			scope, err := configdb.ScopeByIDIncludingDeleted(ctx, l.db, expectedScopeID)
			if err != nil {
				return nil, scopeNotFoundError("scope not found")
			}
			if scope.DeletedAt.Valid || scope.ActiveSlot == nil || scope.EnvironmentID != env.EnvironmentID ||
				pipelineKey != "" && scope.PipelineKey != pipelineKey {
				return nil, scopeDeletedError("scope has been deleted")
			}
			result = append(result, editorScopeSelection{scope: *scope, environment: env, resourceKeys: resourceKeys})
			continue
		}
		if pipelineKey != "" {
			scope, err := configdb.ActivePipeline(ctx, l.db, env.EnvironmentID, pipelineKey)
			if err != nil {
				return nil, mapScopeReadError(err, env.EnvironmentKey, pipelineKey)
			}
			result = append(result, editorScopeSelection{scope: *scope, environment: env, resourceKeys: resourceKeys})
			continue
		}
		pipelines, err := l.pipelineDAO.ListByEnvironmentID(ctx, l.db, env.EnvironmentID, nil, 0, 0)
		if err != nil {
			return nil, err
		}
		for _, pipeline := range pipelines {
			result = append(result, editorScopeSelection{scope: pipeline, environment: env, resourceKeys: resourceKeys})
		}
	}
	if len(result) == 0 {
		return nil, transferSelectionNotFound("no scope matches the selection")
	}
	return result, nil
}

func (s *Service) captureEditorConfigs(ctx context.Context, l *Logic, item editorScopeSelection) ([]model.Config, error) {
	configs, err := l.configDAO.ListByScopeWithFilter(ctx, l.db, item.scope.ScopeID, "", 0, 0)
	if err != nil {
		return nil, err
	}
	if len(item.resourceKeys) == 0 {
		return configs, nil
	}
	found := make(map[string]bool, len(item.resourceKeys))
	result := make([]model.Config, 0, len(item.resourceKeys))
	for _, cfg := range configs {
		if item.resourceKeys[cfg.ResourceKey] {
			found[cfg.ResourceKey] = true
			result = append(result, cfg)
		}
	}
	for key := range item.resourceKeys {
		if !found[key] {
			return nil, transferSelectionNotFound("resource_key " + key + " was not found")
		}
	}
	return result, nil
}

func exportScopeFromRows(env model.Environment, pipeline model.Pipeline) ExportScopeRef {
	return ExportScopeRef{
		EnvironmentKey: env.EnvironmentKey, EnvironmentName: env.EnvironmentName,
		PipelineKey: pipeline.PipelineKey, PipelineName: pipeline.PipelineName,
		EnvironmentID: env.EnvironmentID, ScopeID: pipeline.ScopeID,
	}
}

func (s *Service) resolveReleaseExportPlan(ctx context.Context, request ExportRequest, loadAssets bool) (*ExportPlan, error) {
	envKey := strings.TrimSpace(request.EnvironmentKey)
	pipelineKey := strings.TrimSpace(request.PipelineKey)
	scopeID := strings.TrimSpace(request.ExpectedScopeID)
	snapshotID := strings.TrimSpace(request.ExpectedSnapshotID)
	versionName := strings.TrimSpace(request.Version)
	if envKey == "" || pipelineKey == "" {
		return nil, transferInvalidRequest("environment_key and pipeline_key are required")
	}
	if scopeID == "" || snapshotID == "" {
		return nil, transferInvalidRequest("expected_scope_id and expected_snapshot_id are required")
	}
	if request.Source == TransferSourceVersion && versionName == "" {
		return nil, transferInvalidRequest("version is required for version source")
	}
	if request.Source == TransferSourceActive && versionName != "" {
		return nil, transferInvalidRequest("active source must not include version")
	}
	if err := s.ValidateReadScope(ctx, envKey, pipelineKey, scopeID); err != nil {
		return nil, err
	}
	scope, err := resolveScopeByKeys(ctx, s.logic, envKey, pipelineKey)
	if err != nil {
		return nil, err
	}
	env, err := s.logic.environmentDAO.GetByID(ctx, s.logic.db, scope.EnvironmentID)
	if err != nil {
		return nil, scopeNotFoundError("environment not found")
	}

	var row *model.ConfigReleaseVersion
	if request.Source == TransferSourceActive {
		head, err := s.logic.releaseDAO.GetHeadByScopeID(ctx, s.logic.db, scope.ScopeID)
		if err != nil {
			return nil, err
		}
		if head == nil || head.ActiveSnapshotID == "" {
			return nil, releaseNotActiveError(envKey, pipelineKey)
		}
		if head.ActiveSnapshotID != snapshotID {
			return nil, transferActiveChanged("active snapshot changed; refresh before exporting")
		}
		row, err = s.logic.releaseDAO.GetVersionBySnapshotID(ctx, s.logic.db, snapshotID)
		if err != nil {
			return nil, releaseVersionNotFoundError(envKey, pipelineKey, "")
		}
	} else {
		row, err = s.logic.releaseDAO.GetVersionByScopeIDAndName(ctx, s.logic.db, scope.ScopeID, versionName)
		if err != nil {
			return nil, releaseVersionNotFoundError(envKey, pipelineKey, versionName)
		}
		if row.SnapshotID != snapshotID {
			return nil, transferSnapshotMismatch("version no longer matches the selected snapshot")
		}
	}
	if row.DeletedAt != nil {
		return nil, releaseVersionDeletedError()
	}
	if row.ScopeID != scope.ScopeID {
		return nil, scopeIdentityConflict("version belongs to another scope generation")
	}
	plan, err := s.buildReleaseExportPlan(ctx, env, scope, row, loadAssets)
	if err != nil {
		return nil, err
	}
	plan.Source = request.Source
	plan.Format = request.Format
	plan.Scopes[0].Version = row.Version
	plan.Scopes[0].SnapshotID = row.SnapshotID
	return plan, nil
}

func (s *Service) buildReleaseExportPlan(ctx context.Context, env *model.Environment, scope *model.Pipeline,
	row *model.ConfigReleaseVersion, loadAssets bool) (*ExportPlan, error) {
	if payloadDigest(row.Payload) != row.PayloadSha256 {
		return nil, transferIncomplete("release payload digest mismatch")
	}
	assets, err := s.logic.releaseDAO.ListAssets(ctx, s.logic.db, row.SnapshotID)
	if err != nil {
		return nil, err
	}
	if buildReleaseManifest(assets) != row.ManifestSha256 {
		return nil, transferIncomplete("release manifest digest mismatch")
	}
	payload, err := decodeReleasePayload(row.Payload)
	if err != nil {
		return nil, transferIncomplete("release payload is not decodable")
	}
	byAssetID := make(map[string]model.ConfigReleaseAsset, len(assets))
	for _, asset := range assets {
		byAssetID[asset.AssetID] = asset
	}
	referenced := referencedReleaseAssets(payload)
	plan := &ExportPlan{
		Source: TransferSourceVersion, CapturedAt: time.Now().UTC(),
		Scopes: []ExportScopeRef{{
			EnvironmentKey: row.EnvironmentKey, EnvironmentName: payload.Scope.EnvironmentName,
			PipelineKey: row.PipelineKey, PipelineName: payload.Scope.PipelineName,
			EnvironmentID: env.EnvironmentID, ScopeID: scope.ScopeID,
			Version: row.Version, SnapshotID: row.SnapshotID,
		}},
		Configs: []model.Config{}, Assets: []exportAssetData{}, Warnings: []string{},
		Version: row.Version, SnapshotID: row.SnapshotID,
	}
	storage, err := s.snapshotStore(ctx, s.logic, row)
	if err != nil {
		return nil, err
	}
	for assetID := range referenced {
		asset, ok := byAssetID[assetID]
		if !ok {
			return nil, transferIncomplete("release asset manifest row is missing")
		}
		fileID := stableTransferFileID(row.SnapshotID, assetID)
		item := exportAssetData{sha256: asset.Sha256, metadata: model.Asset{
			FileID: fileID, ScopeID: scope.ScopeID,
			EnvironmentKey: row.EnvironmentKey, PipelineKey: row.PipelineKey,
			FileName: asset.FileName, ContentType: asset.ContentType, FileSize: asset.Size,
		}}
		if loadAssets {
			data, err := storage.readAssetBytes(row.SnapshotID, assetID, asset.Size, asset.Sha256)
			if err != nil {
				return nil, transferIncomplete("release asset is unreadable")
			}
			item.data = data
		}
		plan.Assets = append(plan.Assets, item)
		plan.Estimated += asset.Size
	}
	for _, cfg := range payload.Configs {
		content, err := renderReleaseContentWith(cfg.Content, cfg.Type, func(assetID, suffix string) (string, bool) {
			if _, ok := byAssetID[assetID]; !ok {
				return "", false
			}
			return "asset://" + stableTransferFileID(row.SnapshotID, assetID) + suffix, true
		})
		if err != nil {
			return nil, transferIncomplete("release content cannot be rendered")
		}
		plan.Configs = append(plan.Configs, model.Config{
			ScopeID: scope.ScopeID, EnvironmentKey: row.EnvironmentKey, PipelineKey: row.PipelineKey,
			ResourceKey: cfg.ResourceKey, Alias: cfg.Alias, Name: cfg.Name, Type: cfg.Type,
			Content: content, Remark: cfg.Remark, IsPerm: cfg.IsPerm, Description: cfg.Description,
		})
		plan.Estimated += int64(len(content))
	}
	if len(row.ExternalReferences) > 0 && row.ExternalReferences != "[]" {
		plan.Warnings = append(plan.Warnings, "external_references_not_included")
	}
	plan.Estimated += int64(len(plan.Configs) * 256)
	if plan.Estimated > transferExportLimitBytes {
		return nil, transferLimitExceeded("export plan exceeds the output limit")
	}
	return plan, nil
}

func stableTransferFileID(snapshotID, assetID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("rainbow-transfer-v2|"+snapshotID+"|"+assetID)).String()
}

func editorWarnings(configs []model.Config) []string {
	count := 0
	for _, cfg := range configs {
		count += len(externalURLRegexp.FindAllString(cfg.Content, -1))
	}
	if count > 0 {
		return []string{fmt.Sprintf("external_references_not_included:%d", count)}
	}
	return nil
}
