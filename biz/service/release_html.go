package service

import (
	"sort"
	"strings"
)

// releaseReferenceKind 区分富文本中的嵌入媒体与普通业务超链接:
// 媒体引用必须可安全定位(受管资源或明确外部),超链接按业务原值保留。
type releaseReferenceKind int

const (
	releaseReferenceMedia releaseReferenceKind = iota
	releaseReferenceHyperlink
)

// releaseReference 是一处引用(值在原文中的字节区间,可原地替换)。
type releaseReference struct {
	Start int
	End   int
	Value string
	Kind  releaseReferenceKind
}

// releaseReplacement 是一次文本替换(区间 + 新文本)。
type releaseReplacement struct {
	Start int
	End   int
	Text  string
}

var (
	// 媒体属性:这些属性的值必须能可靠定位,否则拒绝创建。
	releaseMediaAttributeNames = map[string]bool{"src": true, "poster": true, "background": true}
	// 普通超链接标签:其 href 属于业务链接,原值保留。
	releaseHyperlinkTagNames = map[string]bool{"a": true, "area": true}
)

// scanReleaseHTMLReferences 按标签与属性边界扫描富文本引用:
// 覆盖单/双引号与不带引号的属性值、srcset 候选列表、style 属性与 <style> 块内的 url()。
// 只做语法边界识别,不做值语义判断(由调用方按 capture/render 规则处理)。
func scanReleaseHTMLReferences(content string) []releaseReference {
	refs := make([]releaseReference, 0)
	length := len(content)
	index := 0
	for index < length {
		open := strings.IndexByte(content[index:], '<')
		if open < 0 {
			break
		}
		tagStart := index + open
		if tagStart+1 >= length {
			break
		}
		switch content[tagStart+1] {
		case '/', '!', '?':
			// 结束标签、注释、声明:整体跳过
			close := strings.IndexByte(content[tagStart:], '>')
			if close < 0 {
				return refs
			}
			index = tagStart + close + 1
			continue
		}
		nameEnd := tagStart + 1
		for nameEnd < length && isReleaseTagNameByte(content[nameEnd]) {
			nameEnd++
		}
		if nameEnd == tagStart+1 {
			// 不是标签(例如数学表达式 "1 < 2"):按普通文本继续扫描
			index = tagStart + 1
			continue
		}
		tagName := strings.ToLower(content[tagStart+1 : nameEnd])
		refs = append(refs, scanReleaseTagAttributes(content, tagName, nameEnd)...)
		tagEnd := findReleaseTagEnd(content, nameEnd)
		if tagEnd < 0 {
			return refs
		}
		index = tagEnd + 1
		if tagName == "style" {
			close := indexOfReleaseClosing(content[index:], "style")
			stop := length
			if close >= 0 {
				stop = index + close
			}
			refs = append(refs, scanReleaseCSSReferences(content, index, stop)...)
			index = stop
		}
	}
	return refs
}

// scanReleaseTagAttributes 解析一个标签内的属性值,记录媒体与超链接引用。
func scanReleaseTagAttributes(content, tagName string, from int) []releaseReference {
	refs := make([]releaseReference, 0)
	length := len(content)
	index := from
	for index < length {
		for index < length && isReleaseSpace(content[index]) {
			index++
		}
		if index >= length || content[index] == '>' {
			break
		}
		if content[index] == '/' {
			index++
			continue
		}
		nameStart := index
		for index < length && !isReleaseSpace(content[index]) && content[index] != '=' && content[index] != '>' && content[index] != '/' {
			index++
		}
		name := strings.ToLower(content[nameStart:index])
		for index < length && isReleaseSpace(content[index]) {
			index++
		}
		if index >= length || content[index] != '=' {
			continue
		}
		index++
		for index < length && isReleaseSpace(content[index]) {
			index++
		}
		if index >= length {
			break
		}
		valueStart := index
		valueEnd := index
		switch content[index] {
		case '"', '\'':
			quote := content[index]
			index++
			valueStart = index
			// 引号内的 '>' 是合法字符(例如 alt=">"),不得据此提前结束属性;
			// 未闭合引号则值延伸到文档末尾,由调用方按整段判定并明确拒绝
			for index < length && content[index] != quote {
				index++
			}
			valueEnd = index
			if index < length && content[index] == quote {
				index++
			}
		default:
			for index < length && !isReleaseSpace(content[index]) && content[index] != '>' {
				index++
			}
			valueEnd = index
		}
		value := content[valueStart:valueEnd]
		switch {
		case name == "style":
			refs = append(refs, scanReleaseCSSReferences(content, valueStart, valueEnd)...)
		case name == "srcset":
			refs = append(refs, scanReleaseSrcset(value, valueStart)...)
		case releaseMediaAttributeNames[name]:
			refs = append(refs, releaseReference{Start: valueStart, End: valueEnd, Value: value, Kind: releaseReferenceMedia})
		case name == "href":
			kind := releaseReferenceMedia
			if releaseHyperlinkTagNames[tagName] {
				kind = releaseReferenceHyperlink
			}
			refs = append(refs, releaseReference{Start: valueStart, End: valueEnd, Value: value, Kind: kind})
		}
	}
	return refs
}

// scanReleaseSrcset 按浏览器规则拆 srcset:URL 到空白为止,随后是描述符到下一逗号。
// 若 URL 词元本身以逗号结尾(不带描述符的前项),该逗号即为候选分隔符,
// 不能继续"跳到下一逗号",否则会吞掉后面的候选。
func scanReleaseSrcset(value string, base int) []releaseReference {
	refs := make([]releaseReference, 0)
	index := 0
	for index < len(value) {
		for index < len(value) && (isReleaseSpace(value[index]) || value[index] == ',') {
			index++
		}
		if index >= len(value) {
			break
		}
		start := index
		for index < len(value) && !isReleaseSpace(value[index]) {
			index++
		}
		end := index
		separatorConsumed := false
		for end > start && value[end-1] == ',' {
			end--
			separatorConsumed = true
		}
		if end > start {
			refs = append(refs, releaseReference{Start: base + start, End: base + end, Value: value[start:end], Kind: releaseReferenceMedia})
		}
		if separatorConsumed {
			// 结尾逗号已经结束该候选:从当前位置继续读下一个候选
			continue
		}
		// 跳过该候选的描述符(1x/2x/100w)直到下一个逗号
		for index < len(value) && value[index] != ',' {
			index++
		}
	}
	return refs
}

// scanReleaseCSSReferences 扫描 style 属性或 <style> 块中的 url(...) 与 @import "..." 目标。
func scanReleaseCSSReferences(content string, from, to int) []releaseReference {
	refs := make([]releaseReference, 0)
	index := from
	for index < to {
		if relative := indexOfReleaseCSSURL(content[index:to]); relative >= 0 {
			ref := scanReleaseCSSURLValue(content, index+relative, to)
			index = ref.next
			if ref.ref != nil {
				refs = append(refs, *ref.ref)
			}
			continue
		}
		break
	}
	index = from
	for index < to {
		relative := indexOfReleaseCSSImport(content[index:to])
		if relative < 0 {
			break
		}
		ref := scanReleaseCSSImportValue(content, index+relative, to)
		index = ref.next
		if ref.ref != nil {
			refs = append(refs, *ref.ref)
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Start < refs[j].Start })
	return refs
}

type releaseScanStep struct {
	ref  *releaseReference
	next int
}

// scanReleaseCSSURLValue 解析一处 url(...) 并返回扫描游标。
func scanReleaseCSSURLValue(content string, openAt int, to int) releaseScanStep {
	index := openAt + len("url(")
	for index < to && isReleaseSpace(content[index]) {
		index++
	}
	if index >= to {
		return releaseScanStep{next: to}
	}
	valueStart := index
	valueEnd := index
	switch content[index] {
	case '"', '\'':
		quote := content[index]
		index++
		valueStart = index
		// CSS 字符串里的 ')' 是合法字符(url(")banner.png")):
		// 引号内只以匹配引号结束,闭合括号在引号之后再找;未闭合引号则值延伸到片段末尾,交由整段判定拒绝
		for index < to && content[index] != quote {
			index++
		}
		valueEnd = index
		if index < to && content[index] == quote {
			index++
		}
	default:
		for index < to && content[index] != ')' && !isReleaseSpace(content[index]) {
			index++
		}
		valueEnd = index
	}
	next := to
	if closeParen := strings.IndexByte(content[index:to], ')'); closeParen >= 0 {
		next = index + closeParen + 1
	}
	if valueEnd <= valueStart {
		return releaseScanStep{next: next}
	}
	return releaseScanStep{
		ref:  &releaseReference{Start: valueStart, End: valueEnd, Value: content[valueStart:valueEnd], Kind: releaseReferenceMedia},
		next: next,
	}
}

// scanReleaseCSSImportValue 解析 @import "..." 的字符串形式目标(带引号)。
func scanReleaseCSSImportValue(content string, at int, to int) releaseScanStep {
	index := at + len("@import")
	for index < to && isReleaseSpace(content[index]) {
		index++
	}
	if index >= to || (content[index] != '"' && content[index] != '\'') {
		// url(...) 形式已由 url() 扫描覆盖
		return releaseScanStep{next: min(to, at+len("@import"))}
	}
	quote := content[index]
	index++
	valueStart := index
	for index < to && content[index] != quote {
		index++
	}
	valueEnd := index
	next := min(to, index+1)
	if valueEnd <= valueStart {
		return releaseScanStep{next: next}
	}
	return releaseScanStep{
		ref:  &releaseReference{Start: valueStart, End: valueEnd, Value: content[valueStart:valueEnd], Kind: releaseReferenceMedia},
		next: next,
	}
}

// applyReleaseReplacements 从后向前应用替换,避免位移影响;重叠区间按先出现的保留。
func applyReleaseReplacements(content string, replacements []releaseReplacement) string {
	if len(replacements) == 0 {
		return content
	}
	sorted := make([]releaseReplacement, 0, len(replacements))
	for _, item := range replacements {
		if item.Start >= 0 && item.End >= item.Start && item.End <= len(content) {
			sorted = append(sorted, item)
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start > sorted[j].Start })
	out := content
	previousStart := len(content) + 1
	for _, item := range sorted {
		if item.End > previousStart {
			continue
		}
		out = out[:item.Start] + item.Text + out[item.End:]
		previousStart = item.Start
	}
	return out
}

// --------------------- 值语义 ---------------------

// isReleaseSafeInlineScheme 判定值是否自包含且不依赖外部内容(内联数据)。
func isReleaseSafeInlineScheme(value string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "data:")
}

// isReleaseFragmentOnly 判定值是否只有 fragment 或为空(不产生外部依赖)。
func isReleaseFragmentOnly(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == "" || strings.HasPrefix(trimmed, "#")
}

// isReleaseExternalReference 判定绝对或相对协议(http(s)://、//host)的外部地址。
func isReleaseExternalReference(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "//")
}

// --------------------- 字节工具 ---------------------

func isReleaseSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

func isReleaseTagNameByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' || b == ':' || b == '_'
}

func isReleaseIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' || b == '_' || b >= 0x80
}

// findReleaseTagEnd 找到标签结束的 '>',引号内的 '>' 不算结束;未闭合返回 -1。
func findReleaseTagEnd(content string, from int) int {
	index := from
	for index < len(content) {
		switch content[index] {
		case '"', '\'':
			quote := content[index]
			index++
			for index < len(content) && content[index] != quote {
				index++
			}
			if index >= len(content) {
				return -1
			}
		case '>':
			return index
		}
		index++
	}
	return -1
}

// indexOfReleaseClosing 查找 </name(忽略大小写)。
func indexOfReleaseClosing(value, name string) int {
	lower := strings.ToLower(value)
	return strings.Index(lower, "</"+name)
}

// indexOfReleaseCSSURL 查找函数形式的 url(;标识符内部出现的 "url(" 不算。
func indexOfReleaseCSSURL(value string) int {
	lower := strings.ToLower(value)
	offset := 0
	for offset < len(lower) {
		found := strings.Index(lower[offset:], "url(")
		if found < 0 {
			return -1
		}
		absolute := offset + found
		if absolute == 0 || !isReleaseIdentByte(lower[absolute-1]) {
			return absolute
		}
		offset = absolute + len("url(")
	}
	return -1
}

// indexOfReleaseCSSImport 查找 @import;标识符内部出现的 @import 不算。
func indexOfReleaseCSSImport(value string) int {
	lower := strings.ToLower(value)
	offset := 0
	for offset < len(lower) {
		found := strings.Index(lower[offset:], "@import")
		if found < 0 {
			return -1
		}
		absolute := offset + found
		if absolute == 0 || !isReleaseIdentByte(lower[absolute-1]) {
			return absolute
		}
		offset = absolute + len("@import")
	}
	return -1
}