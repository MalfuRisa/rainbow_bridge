package service

import "strings"

// RuntimeContentMode 决定 runtime 读取时单条配置 content 的呈现方式。
type RuntimeContentMode int

const (
	// RuntimeContentJSON 把 object、keyvalue、web_link 解码为 JSON 值,其余类型保持字符串。
	RuntimeContentJSON RuntimeContentMode = iota
	// RuntimeContentLegacy 只解码 object、keyvalue;web_link 等其余类型保持字符串。
	RuntimeContentLegacy
	// RuntimeContentRaw 所有类型都保持快照渲染后的字符串。
	RuntimeContentRaw
)

// ParseRuntimeContentFormat 解析显式 content_format:json/raw 以外的取值按请求错误拒绝。
func ParseRuntimeContentFormat(raw string) (RuntimeContentMode, error) {
	switch strings.TrimSpace(raw) {
	case "json":
		return RuntimeContentJSON, nil
	case "raw":
		return RuntimeContentRaw, nil
	default:
		return RuntimeContentJSON, ReleaseInvalidRequestError("content_format must be json or raw")
	}
}

// RenderRuntimeContent 按配置类型和读取模式呈现 content。
// 结构化内容无损解码,解码失败时保留字符串,避免单条内容影响整次读取。
func RenderRuntimeContent(content, configType string, mode RuntimeContentMode) interface{} {
	if mode == RuntimeContentRaw {
		return content
	}
	normalized := strings.ToLower(strings.TrimSpace(configType))
	decode := normalized == "object" || normalized == "keyvalue"
	if mode == RuntimeContentJSON {
		decode = decode || normalized == "web_link"
	}
	if !decode {
		return content
	}
	var parsed interface{}
	if err := DecodeConfigJSON([]byte(content), &parsed); err != nil {
		return content
	}
	return parsed
}
