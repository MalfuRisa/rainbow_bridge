package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// ErrTypeConfigInvalidRequest 表示配置内容未通过结构校验。
const ErrTypeConfigInvalidRequest = "CONFIG_INVALID_REQUEST"

// ConfigContentValidationError 让 handler 把内容校验失败映射为真实 HTTP 400，
// 其他历史错误仍沿用原有 HTTP 200 + 业务 code 的兼容路径。
type ConfigContentValidationError struct {
	Message string
}

func (e *ConfigContentValidationError) Error() string { return e.Message }

func newConfigContentValidationError(format string, args ...any) error {
	return &ConfigContentValidationError{Message: fmt.Sprintf(format, args...)}
}

// AsConfigContentValidationError 供 handler 识别内容校验错误。
func AsConfigContentValidationError(err error) (*ConfigContentValidationError, bool) {
	var target *ConfigContentValidationError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

type webLinkPayload struct {
	URL       string `json:"url"`
	IconURL   string `json:"icon_url,omitempty"`
	SortOrder *int32 `json:"sort_order,omitempty"`
}

// normalizeWebLinkContent 校验并规范化网页链接配置。
// content 在管理 API 中是 JSON 字符串；输出只保留 url、icon_url、sort_order。
func normalizeWebLinkContent(content string) (string, error) {
	var fields map[string]any
	if err := DecodeConfigJSON([]byte(content), &fields); err != nil {
		return "", newConfigContentValidationError("网页链接内容必须为 JSON 对象")
	}
	if fields == nil {
		return "", newConfigContentValidationError("网页链接内容不能为空")
	}

	rawURL, exists := fields["url"]
	if !exists || rawURL == nil {
		return "", newConfigContentValidationError("网页链接必须填写网址")
	}
	urlText, ok := rawURL.(string)
	if !ok {
		return "", newConfigContentValidationError("网页链接网址必须为字符串")
	}
	normalizedURL, err := normalizeWebLinkURL(urlText, "网址")
	if err != nil {
		return "", err
	}

	iconURL := ""
	if rawIcon, exists := fields["icon_url"]; exists && rawIcon != nil {
		iconText, ok := rawIcon.(string)
		if !ok {
			return "", newConfigContentValidationError("图标网址必须为字符串")
		}
		if strings.TrimSpace(iconText) != "" {
			iconURL, err = normalizeWebLinkURL(iconText, "图标网址")
			if err != nil {
				return "", err
			}
		}
	}

	var sortOrder *int32
	if rawSort, exists := fields["sort_order"]; exists && rawSort != nil {
		number, ok := rawSort.(json.Number)
		if !ok {
			return "", newConfigContentValidationError("排序值必须为 int32 整数")
		}
		parsed, err := strconv.ParseInt(number.String(), 10, 32)
		if err != nil {
			return "", newConfigContentValidationError("排序值必须为 int32 整数")
		}
		value := int32(parsed)
		sortOrder = &value
	}

	// 关闭 HTML 转义，避免把查询参数中的 & 改写成 \u0026。
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(webLinkPayload{URL: normalizedURL, IconURL: iconURL, SortOrder: sortOrder}); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}

func normalizeWebLinkURL(value, field string) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return "", newConfigContentValidationError("%s不能为空", field)
	}
	if strings.Contains(raw, "\\") {
		return "", newConfigContentValidationError("%s不能包含反斜杠", field)
	}
	if hasControlCharacter(raw) {
		return "", newConfigContentValidationError("%s不能包含控制字符", field)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", newConfigContentValidationError("%s格式无效", field)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", newConfigContentValidationError("%s必须使用 http:// 或 https://", field)
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return "", newConfigContentValidationError("%s缺少有效主机", field)
	}
	if parsed.User != nil {
		return "", newConfigContentValidationError("%s不能包含用户信息", field)
	}
	if err := validateWebLinkHost(parsed.Host); err != nil {
		return "", newConfigContentValidationError("%s主机或端口无效", field)
	}
	// 保留调用方原文，避免丢失查询参数和片段的编码形式。
	return raw, nil
}

func hasControlCharacter(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func validateWebLinkHost(host string) error {
	if strings.HasPrefix(host, "[") {
		if strings.HasSuffix(host, "]") {
			ip := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
			if net.ParseIP(ip) == nil {
				return errors.New("invalid IPv6 host")
			}
			return nil
		}
		hostname, port, err := net.SplitHostPort(host)
		if err != nil {
			return err
		}
		if net.ParseIP(hostname) == nil {
			return errors.New("invalid IPv6 host")
		}
		return validateWebLinkPort(port)
	}

	if hostname, port, err := net.SplitHostPort(host); err == nil {
		if strings.TrimSpace(hostname) == "" {
			return errors.New("empty host")
		}
		return validateWebLinkPort(port)
	}
	// 没有端口时，主机名中不应再出现冒号；否则是非法端口或 IPv6 写法。
	if strings.Contains(host, ":") {
		return errors.New("invalid host or port")
	}
	return nil
}

func validateWebLinkPort(port string) error {
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return errors.New("invalid port")
	}
	return nil
}
