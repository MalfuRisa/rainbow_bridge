package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

// 内部保留标记:快照 payload 中受管资源的唯一表达。
const releaseAssetScheme = "release-asset://"

var (
	releaseExternalURLRegexp = regexp.MustCompile(`https?://[^\s"'<>()]+`)
)

// releaseAssetPlan 是计划复制的一份资源。
type releaseAssetPlan struct {
	AssetID      string
	SourceFileID string
	FileName     string
	ContentType  string
	DeclaredSize int64
	Source       releaseSource
	ResourceKey  string
	Alias        string
}

// releaseCapturePlan 是一次快照捕获的完整计划(payload 已生成,资源待复制)。
type releaseCapturePlan struct {
	Payload            string
	PayloadSha256      string
	ConfigCount        int
	Assets             []releaseAssetPlan
	ExternalReferences []string
	TotalDeclaredBytes int64
}

// captureReleasePlan 在写协调事务内捕获范围配置与资源计划:
// 内容无损解析、受管引用改写为 release-asset:// 标记、外部地址登记、问题项收集。
// 任何无法安全处理的引用都会使整次创建失败,不生成残缺版本。
func (s *Service) captureReleasePlan(ctx context.Context, l *Logic, environmentKey, pipelineKey string, scope *model.Environment, pipeline *model.Pipeline) (*releaseCapturePlan, error) {
	configs, err := l.configDAO.ListByEnvironmentAndPipelineWithFilter(ctx, l.db, environmentKey, pipelineKey, "", "", "", 0, 0)
	if err != nil {
		return nil, releaseInternalError(err)
	}
	if len(configs) > model.ReleaseMaxConfigs {
		return nil, releaseLimitExceededError(ReleaseLimitInfo{Item: "configs", Limit: model.ReleaseMaxConfigs, Actual: int64(len(configs))})
	}

	sort.Slice(configs, func(i, j int) bool { return configs[i].ResourceKey < configs[j].ResourceKey })

	builder := &releasePlanBuilder{
		ctx:            ctx,
		service:        s,
		logic:          l,
		environmentKey: environmentKey,
		pipelineKey:    pipelineKey,
		scopeID:        pipeline.ScopeID,
		assetIDs:       map[string]string{},
		assets:         []releaseAssetPlan{},
		issues:         []ReleaseResourceIssue{},
		externalSeen:   map[string]bool{},
		external:       []string{},
	}
	payload := releasePayload{
		Scope: releasePayloadScope{
			EnvironmentKey:  environmentKey,
			PipelineKey:     pipelineKey,
			EnvironmentName: scope.EnvironmentName,
			PipelineName:    pipeline.PipelineName,
		},
		Configs: make([]releasePayloadConfig, 0, len(configs)),
	}
	for i := range configs {
		content, err := builder.rewriteContent(&configs[i])
		if err != nil {
			return nil, err
		}
		payload.Configs = append(payload.Configs, releasePayloadConfig{
			ResourceKey: configs[i].ResourceKey,
			Alias:       configs[i].Alias,
			Name:        configs[i].Name,
			Type:        normalizeConfigTypeString(configs[i].Type),
			Content:     content,
			Remark:      configs[i].Remark,
			IsPerm:      configs[i].IsPerm,
			Description: configs[i].Description,
		})
	}
	if len(builder.issues) > 0 {
		sort.Slice(builder.issues, func(i, j int) bool {
			left, right := builder.issues[i], builder.issues[j]
			if left.ResourceKey != right.ResourceKey {
				return left.ResourceKey < right.ResourceKey
			}
			if left.FileID != right.FileID {
				return left.FileID < right.FileID
			}
			return left.Reason < right.Reason
		})
		return nil, releaseResourceInvalidError(builder.issues)
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, releaseInternalError(err)
	}
	if len(payloadBytes) > model.ReleaseMaxPayloadByte {
		return nil, releaseLimitExceededError(ReleaseLimitInfo{Item: "payload_bytes", Limit: model.ReleaseMaxPayloadByte, Actual: int64(len(payloadBytes))})
	}
	payloadString := string(payloadBytes)
	if err := verifyNoManagedRoutes(payloadString, s.basePath); err != nil {
		// 防御:捕获完成后不得残留任何会转向编辑区的受管资源地址
		return nil, releaseInternalError(err)
	}
	digest := sha256.Sum256(payloadBytes)
	if len(builder.assets) > model.ReleaseMaxAssets {
		return nil, releaseLimitExceededError(ReleaseLimitInfo{Item: "assets", Limit: model.ReleaseMaxAssets, Actual: int64(len(builder.assets))})
	}
	if builder.totalDeclared > model.ReleaseMaxTotalByte {
		return nil, releaseLimitExceededError(ReleaseLimitInfo{Item: "total_asset_bytes", Limit: model.ReleaseMaxTotalByte, Actual: builder.totalDeclared})
	}
	sort.Strings(builder.external)
	return &releaseCapturePlan{
		Payload:            payloadString,
		PayloadSha256:      hex.EncodeToString(digest[:]),
		ConfigCount:        len(payload.Configs),
		Assets:             builder.assets,
		ExternalReferences: builder.external,
		TotalDeclaredBytes: builder.totalDeclared,
	}, nil
}

// releasePlanBuilder 集中一次捕获的引用解析状态与问题清单。
type releasePlanBuilder struct {
	ctx            context.Context
	service        *Service
	logic          *Logic
	environmentKey string
	pipelineKey    string
	scopeID        string
	assetIDs       map[string]string // source_file_id -> release asset_id
	assets         []releaseAssetPlan
	issues         []ReleaseResourceIssue
	externalSeen   map[string]bool
	external       []string
	totalDeclared  int64
}

func (b *releasePlanBuilder) issue(resourceKey, alias, fileID, reason string) {
	b.issues = append(b.issues, ReleaseResourceIssue{ResourceKey: resourceKey, Alias: alias, FileID: fileID, Reason: reason})
}

// rewriteContent 按类型无损重写内容中的受管引用。
func (b *releasePlanBuilder) rewriteContent(cfg *model.Config) (string, error) {
	content := cfg.Content
	if strings.Contains(content, releaseAssetScheme) {
		b.issue(cfg.ResourceKey, cfg.Alias, "", "internal_marker")
		return "", nil
	}
	typ := normalizeConfigTypeString(cfg.Type)
	switch typ {
	case "image", "file":
		return b.rewriteFullReference(cfg), nil
	case "object", "keyvalue":
		return b.rewriteStructured(cfg)
	case "richtext":
		return b.rewriteRichText(cfg)
	default:
		// 普通文本等:完整受管地址映射,http(s) 作为字面值登记
		if fileID, suffix, matched := matchManagedReference(content, b.service.basePath); matched {
			assetID, err := b.resolveAsset(cfg, fileID)
			if err != nil {
				return "", err
			}
			return releaseAssetScheme + assetID + suffix, nil
		}
		if embeddedManagedReference(content, b.service.basePath) {
			b.issue(cfg.ResourceKey, cfg.Alias, "", "unresolvable_reference")
			return "", nil
		}
		b.collectExternal(content)
		return content, nil
	}
}

// rewriteFullReference 处理 image/file:必须整体是一个受管引用;外链拒绝创建。
func (b *releasePlanBuilder) rewriteFullReference(cfg *model.Config) string {
	content := strings.TrimSpace(cfg.Content)
	if fileID, suffix, matched := matchManagedReference(content, b.service.basePath); matched {
		assetID, err := b.resolveAsset(cfg, fileID)
		if err != nil {
			return ""
		}
		return releaseAssetScheme + assetID + suffix
	}
	lower := strings.ToLower(content)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		b.issue(cfg.ResourceKey, cfg.Alias, "", "external_reference")
		return content
	}
	b.issue(cfg.ResourceKey, cfg.Alias, "", "unresolvable_reference")
	return content
}

// rewriteStructured 处理 object/keyvalue:递归处理字符串,保持数字原值。
func (b *releasePlanBuilder) rewriteStructured(cfg *model.Config) (string, error) {
	var value any
	if err := DecodeConfigJSON([]byte(cfg.Content), &value); err != nil {
		// 导入前已由既有校验保证合法;此处防御性返回内部错误
		return "", releaseInternalError(fmt.Errorf("config %s content is not decodable: %w", cfg.ResourceKey, err))
	}
	rewritten, err := b.rewriteValue(cfg, value)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(rewritten)
	if err != nil {
		return "", releaseInternalError(err)
	}
	return string(encoded), nil
}

func (b *releasePlanBuilder) rewriteValue(cfg *model.Config, value any) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			next, err := b.rewriteValue(cfg, item)
			if err != nil {
				return nil, err
			}
			v[key] = next
		}
		return v, nil
	case []any:
		for i, item := range v {
			next, err := b.rewriteValue(cfg, item)
			if err != nil {
				return nil, err
			}
			v[i] = next
		}
		return v, nil
	case string:
		if strings.Contains(v, releaseAssetScheme) {
			b.issue(cfg.ResourceKey, cfg.Alias, "", "internal_marker")
			return v, nil
		}
		if fileID, suffix, matched := matchManagedReference(v, b.service.basePath); matched {
			assetID, err := b.resolveAsset(cfg, fileID)
			if err != nil {
				return nil, err
			}
			return releaseAssetScheme + assetID + suffix, nil
		}
		if embeddedManagedReference(v, b.service.basePath) {
			b.issue(cfg.ResourceKey, cfg.Alias, "", "unresolvable_reference")
			return v, nil
		}
		b.collectExternal(v)
		return v, nil
	default:
		return value, nil
	}
}

// rewriteRichText 处理富文本:按标签/属性边界识别引用。
// 受管引用改写为快照标记;嵌入媒体(src/srcset/poster/style url())是外部地址或相对路径时拒绝创建,
// 不能可靠定位的形式不放过;普通业务超链接原值保留并登记为外部提示。
func (b *releasePlanBuilder) rewriteRichText(cfg *model.Config) (string, error) {
	content := cfg.Content
	replacements := make([]releaseReplacement, 0)
	for _, ref := range scanReleaseHTMLReferences(content) {
		value := strings.TrimSpace(ref.Value)
		if isReleaseFragmentOnly(value) || isReleaseSafeInlineScheme(value) {
			continue
		}
		if fileID, suffix, matched := matchManagedReference(value, b.service.basePath); matched {
			assetID, err := b.resolveAsset(cfg, fileID)
			if err != nil {
				return "", err
			}
			replacements = append(replacements, releaseReplacement{Start: ref.Start, End: ref.End, Text: releaseAssetScheme + assetID + suffix})
			continue
		}
		if embeddedManagedReference(value, b.service.basePath) {
			b.issue(cfg.ResourceKey, cfg.Alias, "", "unresolvable_reference")
			continue
		}
		if isReleaseExternalReference(value) {
			if ref.Kind == releaseReferenceHyperlink {
				b.collectExternal(value)
				continue
			}
			b.issue(cfg.ResourceKey, cfg.Alias, "", "external_reference")
			continue
		}
		if ref.Kind == releaseReferenceMedia {
			// 相对路径等无法在快照内可靠定位的嵌入媒体:明确拒绝,不能当作普通文字放过
			b.issue(cfg.ResourceKey, cfg.Alias, "", "unresolvable_reference")
		}
	}
	return applyReleaseReplacements(content, replacements), nil
}

// resolveAsset 解析源 file_id 并分配快照内 asset_id;失败记录问题项。
func (b *releasePlanBuilder) resolveAsset(cfg *model.Config, fileID string) (string, error) {
	if existing, ok := b.assetIDs[fileID]; ok {
		return existing, nil
	}
	asset, err := b.logic.GetAsset(b.ctx, fileID)
	if err != nil {
		if errors.Is(err, ErrAssetNotFound) {
			b.issue(cfg.ResourceKey, cfg.Alias, fileID, "missing_metadata")
			return fileID, nil
		}
		return "", releaseInternalError(err)
	}
	if asset.EnvironmentKey != b.environmentKey || asset.PipelineKey != b.pipelineKey || asset.ScopeID != b.scopeID {
		b.issue(cfg.ResourceKey, cfg.Alias, fileID, "cross_scope_reference")
		return fileID, nil
	}
	if strings.TrimSpace(asset.Path) == "" {
		b.issue(cfg.ResourceKey, cfg.Alias, fileID, "unresolvable_reference")
		return fileID, nil
	}
	source, err := b.service.inspectReleaseSource(b.ctx, asset)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			b.issue(cfg.ResourceKey, cfg.Alias, fileID, "file_missing")
			return fileID, nil
		}
		return "", err
	}
	if source.Size > model.ReleaseMaxAssetByte {
		return "", releaseLimitExceededError(ReleaseLimitInfo{Item: "asset_bytes", Limit: model.ReleaseMaxAssetByte, Actual: source.Size})
	}
	if source.Size != asset.FileSize || source.Size < 0 {
		b.issue(cfg.ResourceKey, cfg.Alias, fileID, "size_mismatch")
		return fileID, nil
	}
	assetID := uuid.NewString()
	b.assetIDs[fileID] = assetID
	b.assets = append(b.assets, releaseAssetPlan{
		AssetID:      assetID,
		SourceFileID: fileID,
		FileName:     asset.FileName,
		ContentType:  asset.ContentType,
		DeclaredSize: source.Size,
		Source:       source,
		ResourceKey:  cfg.ResourceKey,
		Alias:        cfg.Alias,
	})
	b.totalDeclared += source.Size
	return assetID, nil
}

func (b *releasePlanBuilder) collectExternal(value string) {
	for _, match := range releaseExternalURLRegexp.FindAllString(value, -1) {
		cleaned := strings.TrimRight(match, `.,;)\/`)
		if cleaned == "" || b.externalSeen[cleaned] {
			continue
		}
		b.externalSeen[cleaned] = true
		b.external = append(b.external, cleaned)
	}
}

// --------------------- 引用识别 ---------------------

// matchManagedReference 判定整段值是否恰为一个受管资源引用。
// 返回 file_id 与保留的查询／fragment 后缀(例如 "?v=1#top")。
func matchManagedReference(value, basePath string) (string, string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", "", false
	}
	if rest, ok := strings.CutPrefix(trimmed, assetScheme); ok {
		fileID, suffix := splitReferenceSuffix(rest)
		if isValidReleaseFileID(fileID) {
			return fileID, suffix, true
		}
		return "", "", false
	}
	candidate := trimmed
	if basePath != "" {
		for strings.HasPrefix(candidate, basePath+"/") {
			candidate = strings.TrimPrefix(candidate, basePath)
		}
	}
	rest, ok := strings.CutPrefix(candidate, fileURLPrefix)
	if !ok {
		return "", "", false
	}
	fileID, suffix := splitReferenceSuffix(rest)
	if slash := strings.Index(fileID, "/"); slash >= 0 {
		fileID = fileID[:slash]
	}
	if !isValidReleaseFileID(fileID) {
		return "", "", false
	}
	return fileID, suffix, true
}

// splitReferenceSuffix 将路径/查询/fragment 拆成 id 与后缀;后缀保留原样。
func splitReferenceSuffix(rest string) (string, string) {
	suffixAt := len(rest)
	for _, marker := range []string{"?", "#"} {
		if idx := strings.Index(rest, marker); idx >= 0 && idx < suffixAt {
			suffixAt = idx
		}
	}
	return rest[:suffixAt], rest[suffixAt:]
}

func isValidReleaseFileID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// containsBareAssetScheme 判定是否存在不带 release- 前缀的裸 asset:// 引用。
func containsBareAssetScheme(value string) bool {
	for offset := 0; ; {
		index := strings.Index(value[offset:], assetScheme)
		if index < 0 {
			return false
		}
		absolute := offset + index
		if absolute < len("release-") || value[absolute-len("release-"):absolute] != "release-" {
			return true
		}
		offset = absolute + len(assetScheme)
	}
}

// embeddedManagedReference 判定值中是否嵌有受管路由(但整段又不是一份合法引用)。
func embeddedManagedReference(value, basePath string) bool {
	if strings.Contains(value, assetScheme) || strings.Contains(value, fileURLPrefix) {
		return true
	}
	if basePath != "" && strings.Contains(value, basePath+fileURLPrefix) {
		return true
	}
	return false
}

// verifyNoManagedRoutes 防御性校验:payload 中不得残留会转向编辑区的受管地址。
func verifyNoManagedRoutes(payload, basePath string) error {
	if containsBareAssetScheme(payload) {
		return errors.New("payload still contains asset:// managed routes")
	}
	if strings.Contains(payload, fileURLPrefix) {
		return errors.New("payload still contains managed file routes")
	}
	if basePath != "" && strings.Contains(payload, basePath+fileURLPrefix) {
		return errors.New("payload still contains prefixed managed file routes")
	}
	return nil
}

// --------------------- 读取渲染 ---------------------

// releaseMarkerRenderer 把快照内的 release-asset:// 标记解析为目标地址;
// 在线读取返回本服务下载地址,静态包返回包内相对路径。
type releaseMarkerRenderer func(assetID, suffix string) (string, bool)

// renderReleaseContent 把内部标记渲染为本快照自己的下载地址;其他内容原样返回。
func (s *Service) renderReleaseContent(snapshotID, content, typ string) (string, error) {
	return renderReleaseContentWith(content, typ, func(assetID, suffix string) (string, bool) {
		return fmt.Sprintf("%s/api/v1/release/asset/%s/%s%s", s.basePath, snapshotID, assetID, suffix), true
	})
}

// renderReleaseContentWith 按类型渲染内容中的标记;标记→目标地址由调用方决定。
// object/keyvalue 递归处理字符串字段,richtext 按标签边界处理,其余类型整段判定。
func renderReleaseContentWith(content, typ string, render releaseMarkerRenderer) (string, error) {
	renderMarker := func(value string) (string, bool) {
		assetID, suffix, matched := matchReleaseMarker(value)
		if !matched {
			return value, false
		}
		return render(assetID, suffix)
	}
	switch normalizeConfigTypeString(typ) {
	case "object", "keyvalue":
		var value any
		if err := DecodeConfigJSON([]byte(content), &value); err != nil {
			return content, nil
		}
		rendered, err := renderReleaseValue(value, renderMarker)
		if err != nil {
			return "", err
		}
		encoded, err := json.Marshal(rendered)
		if err != nil {
			return "", releaseInternalError(err)
		}
		return string(encoded), nil
	case "richtext":
		replacements := make([]releaseReplacement, 0)
		for _, ref := range scanReleaseHTMLReferences(content) {
			if rendered, matched := renderMarker(ref.Value); matched {
				replacements = append(replacements, releaseReplacement{Start: ref.Start, End: ref.End, Text: rendered})
			}
		}
		return applyReleaseReplacements(content, replacements), nil
	default:
		if rendered, matched := renderMarker(strings.TrimSpace(content)); matched {
			return rendered, nil
		}
		return content, nil
	}
}

func renderReleaseValue(value any, render func(string) (string, bool)) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			next, err := renderReleaseValue(item, render)
			if err != nil {
				return nil, err
			}
			v[key] = next
		}
		return v, nil
	case []any:
		for i, item := range v {
			next, err := renderReleaseValue(item, render)
			if err != nil {
				return nil, err
			}
			v[i] = next
		}
		return v, nil
	case string:
		if rendered, matched := render(v); matched {
			return rendered, nil
		}
		return v, nil
	default:
		return value, nil
	}
}

// matchReleaseMarker 解析 release-asset://<asset_id>[?query#fragment]。
func matchReleaseMarker(value string) (string, string, bool) {
	trimmed := strings.TrimSpace(value)
	rest, ok := strings.CutPrefix(trimmed, releaseAssetScheme)
	if !ok {
		return "", "", false
	}
	assetID, suffix := splitReferenceSuffix(rest)
	if !isValidReleaseFileID(assetID) {
		return "", "", false
	}
	return assetID, suffix, true
}

// referencedReleaseAssets 收集渲染结果中引用到的 asset_id 集合(用于读取时完整性检查)。
func referencedReleaseAssets(payload *releasePayload) map[string]bool {
	ids := map[string]bool{}
	collect := func(value string) {
		for _, match := range releaseMarkerRegexp.FindAllStringSubmatch(value, -1) {
			if len(match) >= 2 {
				ids[match[1]] = true
			}
		}
	}
	for i := range payload.Configs {
		collect(payload.Configs[i].Content)
	}
	return ids
}

var releaseMarkerRegexp = regexp.MustCompile(`release-asset://([a-zA-Z0-9_-]+)`)

// buildReleaseManifest 按 asset_id 排序生成清单摘要。
func buildReleaseManifest(assets []model.ConfigReleaseAsset) string {
	sorted := make([]model.ConfigReleaseAsset, len(assets))
	copy(sorted, assets)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].AssetID < sorted[j].AssetID })
	hasher := sha256.New()
	for _, asset := range sorted {
		fmt.Fprintf(hasher, "%s\t%s\t%s\t%s\t%d\t%s\n",
			asset.AssetID, asset.SourceFileID, asset.FileName, asset.ContentType, asset.Size, asset.Sha256)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// payloadDigest 计算 payload 字符串的 SHA-256。
func payloadDigest(payload string) string {
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:])
}
