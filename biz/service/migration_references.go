package service

import (
	"sort"
	"strings"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
)

// 迁移引用计划与重写(PRD RB-MIGRATE-01 §3.2)。
// 提取与重写共用同一个 token 扫描器(scanManagedReferences),不允许
// "正则识别 URL、另用 asset:// 文本替换"式的两套语义,否则正常落库的
// /api/v1/asset/file/<id>/<name> 形态会被漏改。
//
// 可识别的受管引用形态(都可带 ?query#fragment 后缀,都允许当前部署
// base_path 的单/重复前缀):
//  1. asset://<id>
//  2. /api/v1/asset/file/<id>[/<file_name>]
//
// 外部 http(s) URL、普通文本、与当前部署无关的旧前缀原样保留;
// release-asset:// 等发布内部标记不是编辑区可复制资源,出现即明确失败。

// releaseFileScheme 是发布模块的另一个内部标记前缀(与 releaseAssetScheme 同源语义)。
const releaseFileScheme = "release-file://"

// managedReferenceOccurrence 是文本中一处受管资源引用:[start,end) 为字节区间。
type managedReferenceOccurrence struct {
	start    int
	end      int
	fileID   string
	fileName string // 引用路径中出现的文件名(可能为空)
	suffix   string // 保留原样的 ?query#fragment 后缀
}

// migrationReferencePlan 是一项配置的迁移引用计划:按首次出现顺序去重的旧资源 ID。
type migrationReferencePlan struct {
	fileIDs []string
}

func (p *migrationReferencePlan) hasReferences() bool {
	return p != nil && len(p.fileIDs) > 0
}

// migrationAssetTarget 是一项配置内某个旧资源复制后的目标落点。
type migrationAssetTarget struct {
	fileID   string // 目标新 ID
	fileName string // 目标实际文件名(元数据)
}

// planMigrationReferences 生成一项配置的迁移引用计划。
// 文本类配置按整段内容扫描;object/keyvalue 递归扫描字符串值(键排序保证顺序稳定)。
func planMigrationReferences(cfg *model.Config, basePath string) (*migrationReferencePlan, error) {
	plan := &migrationReferencePlan{fileIDs: make([]string, 0)}
	if cfg == nil {
		return plan, nil
	}
	seen := make(map[string]bool)
	collect := func(value string) error {
		if containsInternalReleaseMarker(value) {
			return &migrationFailure{kind: migrationFailureInternalMarker}
		}
		for _, occ := range scanManagedReferences(value, basePath) {
			if !seen[occ.fileID] {
				seen[occ.fileID] = true
				plan.fileIDs = append(plan.fileIDs, occ.fileID)
			}
		}
		return nil
	}
	if isStructuredConfigType(cfg.Type) {
		var payload any
		if err := DecodeConfigJSON([]byte(cfg.Content), &payload); err != nil {
			return nil, &migrationFailure{kind: migrationFailureContentUnparseable, cause: err}
		}
		if err := walkConfigStrings(payload, collect); err != nil {
			return nil, err
		}
		return plan, nil
	}
	if err := collect(cfg.Content); err != nil {
		return nil, err
	}
	return plan, nil
}

// rewriteMigrationContent 依据同一 token 语义重写受管引用,使其指向目标新资源。
// 计划内每个引用都必须有映射;缺失即失败,绝不在目标内容里留下旧 ID。
func rewriteMigrationContent(cfg *model.Config, basePath string, mapping map[string]migrationAssetTarget) (string, error) {
	if cfg == nil {
		return "", nil
	}
	if isStructuredConfigType(cfg.Type) {
		var payload any
		if err := DecodeConfigJSON([]byte(cfg.Content), &payload); err != nil {
			return "", &migrationFailure{kind: migrationFailureContentUnparseable, cause: err}
		}
		rewritten, err := rewriteConfigStrings(payload, basePath, mapping)
		if err != nil {
			return "", err
		}
		encoded, err := jsonContent(rewritten)
		if err != nil {
			return "", &migrationFailure{kind: migrationFailureContentUnparseable, cause: err}
		}
		return encoded, nil
	}
	return rewriteReferenceTokens(cfg.Content, basePath, mapping)
}

// verifyMigratedReferences 校验重写后的内容里每个受管引用都指向本项新建的目标资源。
func verifyMigratedReferences(content, typ, basePath string, createdTargets map[string]migrationAssetTarget) error {
	plan, err := planMigrationReferences(&model.Config{Type: typ, Content: content}, basePath)
	if err != nil {
		return err
	}
	for _, fileID := range plan.fileIDs {
		if _, ok := createdTargets[fileID]; !ok {
			return &migrationFailure{fileID: fileID, kind: migrationFailureReferenceUnresolved}
		}
	}
	return nil
}

// isStructuredConfigType 判断配置类型是否为需要递归遍历的 JSON 结构。
func isStructuredConfigType(typ string) bool {
	normalized := normalizeConfigTypeString(typ)
	return normalized == "object" || normalized == "keyvalue"
}

// ---------------- 扫描器 ----------------

// scanManagedReferences 按明确 token 边界扫描一段文本中的受管资源引用。
// 只返回可安全解析的引用;内部标记与未知前缀由调用方另行处理。
func scanManagedReferences(value, basePath string) []managedReferenceOccurrence {
	occurrences := make([]managedReferenceOccurrence, 0)
	for i := 0; i < len(value); {
		switch {
		case strings.HasPrefix(value[i:], assetScheme):
			occurrence, ok := parseSchemeReference(value, i)
			if ok {
				occurrences = append(occurrences, occurrence)
				i = occurrence.end
				continue
			}
			i += len(assetScheme)
		case strings.HasPrefix(value[i:], fileURLPrefix):
			occurrence, ok := parseRouteReference(value, i, basePath)
			if ok {
				occurrences = append(occurrences, occurrence)
				i = occurrence.end
				continue
			}
			i += len(fileURLPrefix)
		default:
			i++
		}
	}
	return occurrences
}

// parseSchemeReference 解析 asset://<id>[/<file_name>][?query#fragment]。
func parseSchemeReference(value string, markerAt int) (managedReferenceOccurrence, bool) {
	if !isReferenceStartBoundary(value, markerAt) {
		return managedReferenceOccurrence{}, false
	}
	fileID, next := scanReferenceID(value, markerAt+len(assetScheme))
	if fileID == "" {
		return managedReferenceOccurrence{}, false
	}
	fileName, afterName := scanReferenceFileName(value, next)
	suffix, end := scanReferenceSuffix(value, afterName)
	return managedReferenceOccurrence{
		start:    markerAt,
		end:      end,
		fileID:   fileID,
		fileName: fileName,
		suffix:   suffix,
	}, true
}

// parseRouteReference 解析 [/<base_path>]*/api/v1/asset/file/<id>[/<file_name>][?query#fragment];
// 只有当前部署 base_path 的单/重复前缀被视为同一 token 的一部分。
func parseRouteReference(value string, markerAt int, basePath string) (managedReferenceOccurrence, bool) {
	start := markerAt
	if basePath != "" {
		for start >= len(basePath) && value[start-len(basePath):start] == basePath {
			start -= len(basePath)
		}
	}
	if !isReferenceStartBoundary(value, start) {
		return managedReferenceOccurrence{}, false
	}
	fileID, next := scanReferenceID(value, markerAt+len(fileURLPrefix))
	if fileID == "" {
		return managedReferenceOccurrence{}, false
	}
	fileName, afterName := scanReferenceFileName(value, next)
	suffix, end := scanReferenceSuffix(value, afterName)
	return managedReferenceOccurrence{
		start:    start,
		end:      end,
		fileID:   fileID,
		fileName: fileName,
		suffix:   suffix,
	}, true
}

// isReferenceStartBoundary 判断位置 at 之前是否为 token 边界。
// 前一字节若是 ID/域名/路径/编码字符,说明该标记属于更长的 token
// (如 release-asset://、https://cdn…/api/v1/asset/file/…),不能当作本服务资源。
func isReferenceStartBoundary(value string, at int) bool {
	if at <= 0 {
		return true
	}
	return !isReferenceContinuationByte(value[at-1])
}

func isReferenceContinuationByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '_' || b == '-' || b == '.' || b == '/' || b == ':' || b == '%':
		return true
	}
	return false
}

// scanReferenceID 读取连续的 ID 字节;空串或超长/非法字符即不可解析。
func scanReferenceID(value string, at int) (string, int) {
	end := at
	for end < len(value) && isReferenceIDByte(value[end]) {
		end++
	}
	if end == at {
		return "", at
	}
	fileID := value[at:end]
	if !isValidReleaseFileID(fileID) {
		return "", at
	}
	return fileID, end
}

func isReferenceIDByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '_' || b == '-':
		return true
	}
	return false
}

// scanReferenceFileName 解析可选的 /<file_name> 段;文件名不越过 token 终止符。
func scanReferenceFileName(value string, at int) (string, int) {
	if at >= len(value) || value[at] != '/' {
		return "", at
	}
	end := at + 1
	for end < len(value) && !isReferenceTokenTerminator(value[end]) {
		end++
	}
	if end == at+1 {
		return "", at
	}
	return value[at+1 : end], end
}

// scanReferenceSuffix 解析可选的 ?query#fragment 后缀;后缀原样保留。
func scanReferenceSuffix(value string, at int) (string, int) {
	if at >= len(value) || (value[at] != '?' && value[at] != '#') {
		return "", at
	}
	end := at
	for end < len(value) && !isReferenceTokenTerminator(value[end]) {
		end++
	}
	return value[at:end], end
}

// isReferenceTokenTerminator 判断该字节是否终止受管引用 token。
func isReferenceTokenTerminator(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\f', '\v',
		'"', '\'', '`', '<', '>', '(', ')', '[', ']', '{', '}',
		',', ';', '|', '\\', '/', '?', '#':
		return true
	}
	return false
}

// containsInternalReleaseMarker 判断文本是否含发布模块的内部版本标记。
// 这类标记不能当作可复制的编辑区资源,遇到即明确失败,不假装已处理。
func containsInternalReleaseMarker(value string) bool {
	return containsTokenPrefix(value, releaseAssetScheme) ||
		containsTokenPrefix(value, releaseFileScheme)
}

func containsTokenPrefix(value, marker string) bool {
	for at := 0; at < len(value); {
		index := strings.Index(value[at:], marker)
		if index < 0 {
			return false
		}
		position := at + index
		if isReferenceStartBoundary(value, position) {
			return true
		}
		at = position + len(marker)
	}
	return false
}

// ---------------- 重写 ----------------

// rewriteReferenceTokens 按 token 位置重写一段文本中的受管引用,从后往前替换。
func rewriteReferenceTokens(value, basePath string, mapping map[string]migrationAssetTarget) (string, error) {
	occurrences := scanManagedReferences(value, basePath)
	if len(occurrences) == 0 {
		return value, nil
	}
	rewritten := value
	for i := len(occurrences) - 1; i >= 0; i-- {
		occurrence := occurrences[i]
		target, ok := mapping[occurrence.fileID]
		if !ok {
			return "", &migrationFailure{fileID: occurrence.fileID, kind: migrationFailureReferenceUnmapped}
		}
		rewritten = rewritten[:occurrence.start] + managedReferenceURL(target, occurrence) + rewritten[occurrence.end:]
	}
	return rewritten, nil
}

// rewriteConfigStrings 递归重写 JSON 结构中的字符串值,非字符串值原样保留。
func rewriteConfigStrings(value any, basePath string, mapping map[string]migrationAssetTarget) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			rewritten, err := rewriteConfigStrings(item, basePath, mapping)
			if err != nil {
				return nil, err
			}
			v[key] = rewritten
		}
		return v, nil
	case []any:
		for i, item := range v {
			rewritten, err := rewriteConfigStrings(item, basePath, mapping)
			if err != nil {
				return nil, err
			}
			v[i] = rewritten
		}
		return v, nil
	case string:
		return rewriteReferenceTokens(v, basePath, mapping)
	default:
		return value, nil
	}
}

// managedReferenceURL 生成目标引用:统一写入不带 base_path 的受管 URL 形态,
// 使用目标新 ID 与实际文件名并保留原后缀。
// 简写 asset:// 也生成 URL 形态,避免带 query 的后缀再次进入保存期解析器时被误当作 ID。
func managedReferenceURL(target migrationAssetTarget, occurrence managedReferenceOccurrence) string {
	fileName := target.fileName
	if fileName == "" {
		fileName = occurrence.fileName
	}
	var builder strings.Builder
	builder.WriteString(fileURLPrefix)
	builder.WriteString(target.fileID)
	if fileName != "" {
		builder.WriteString("/")
		builder.WriteString(fileName)
	}
	builder.WriteString(occurrence.suffix)
	return builder.String()
}

// walkConfigStrings 递归遍历 JSON 结构中的字符串值;map 键排序保证访问顺序稳定。
func walkConfigStrings(value any, visit func(string) error) error {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := walkConfigStrings(v[key], visit); err != nil {
				return err
			}
		}
		return nil
	case []any:
		for _, item := range v {
			if err := walkConfigStrings(item, visit); err != nil {
				return err
			}
		}
		return nil
	case string:
		return visit(v)
	default:
		return nil
	}
}
