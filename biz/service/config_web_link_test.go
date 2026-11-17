package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeWebLinkContentValid(t *testing.T) {
	tests := []struct {
		name    string
		content string
		check   func(t *testing.T, payload map[string]any)
	}{
		{
			name:    "minimal url",
			content: `{"url":"http://example.com"}`,
			check: func(t *testing.T, payload map[string]any) {
				if payload["url"] != "http://example.com" {
					t.Fatalf("url = %v", payload["url"])
				}
				if _, ok := payload["sort_order"]; ok {
					t.Fatalf("sort_order should be omitted")
				}
			},
		},
		{
			name:    "query fragment icon and zero sort",
			content: `{"url":"  https://example.com:8443/path?a=1&b=2#frag  ","icon_url":"http://127.0.0.1:8080/icon.png","sort_order":0,"extra":"drop"}`,
			check: func(t *testing.T, payload map[string]any) {
				if payload["url"] != "https://example.com:8443/path?a=1&b=2#frag" {
					t.Fatalf("url = %v", payload["url"])
				}
				if payload["icon_url"] != "http://127.0.0.1:8080/icon.png" {
					t.Fatalf("icon_url = %v", payload["icon_url"])
				}
				if payload["sort_order"] != float64(0) {
					t.Fatalf("sort_order = %v", payload["sort_order"])
				}
				if _, ok := payload["extra"]; ok {
					t.Fatalf("extra field should be dropped")
				}
			},
		},
		{
			name:    "negative sort and IPv6",
			content: `{"url":"http://[::1]:8080/x","sort_order":-1}`,
			check: func(t *testing.T, payload map[string]any) {
				if payload["url"] != "http://[::1]:8080/x" {
					t.Fatalf("url = %v", payload["url"])
				}
				if payload["sort_order"] != float64(-1) {
					t.Fatalf("sort_order = %v", payload["sort_order"])
				}
			},
		},
		{
			name:    "internal hostname",
			content: `{"url":"http://rainbow_bridge.default.svc:8080/api"}`,
			check: func(t *testing.T, payload map[string]any) {
				if payload["url"] != "http://rainbow_bridge.default.svc:8080/api" {
					t.Fatalf("url = %v", payload["url"])
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			normalized, err := normalizeWebLinkContent(test.content)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(normalized), &payload); err != nil {
				t.Fatalf("normalized content is not JSON: %v (%s)", err, normalized)
			}
			test.check(t, payload)
		})
	}
}

func TestNormalizeWebLinkContentInvalid(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "empty", content: ``, want: "JSON 对象"},
		{name: "not object", content: `[]`, want: "JSON 对象"},
		{name: "missing url", content: `{}`, want: "必须填写网址"},
		{name: "empty url", content: `{"url":"   "}`, want: "网址不能为空"},
		{name: "relative url", content: `{"url":"/path"}`, want: "http:// 或 https://"},
		{name: "protocol relative", content: `{"url":"//example.com"}`, want: "http:// 或 https://"},
		{name: "ftp url", content: `{"url":"ftp://example.com"}`, want: "http:// 或 https://"},
		{name: "userinfo", content: `{"url":"http://user:pass@example.com"}`, want: "用户信息"},
		{name: "backslash", content: `{"url":"http://example.com\\path"}`, want: "反斜杠"},
		{name: "control char", content: `{"url":"http://example.com/\npath"}`, want: "控制字符"},
		{name: "invalid port", content: `{"url":"http://example.com:99999"}`, want: "主机或端口无效"},
		{name: "non numeric port", content: `{"url":"http://example.com:abc"}`, want: "格式无效"},
		{name: "icon invalid", content: `{"url":"http://example.com","icon_url":"javascript:alert(1)"}`, want: "图标网址必须使用 http:// 或 https://"},
		{name: "sort string", content: `{"url":"http://example.com","sort_order":"10"}`, want: "int32 整数"},
		{name: "sort decimal", content: `{"url":"http://example.com","sort_order":1.5}`, want: "int32 整数"},
		{name: "sort overflow", content: `{"url":"http://example.com","sort_order":2147483648}`, want: "int32 整数"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeWebLinkContent(test.content)
			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %q, want contains %q", err.Error(), test.want)
			}
			if _, ok := AsConfigContentValidationError(err); !ok {
				t.Fatalf("error should be ConfigContentValidationError: %T", err)
			}
		})
	}
}

func TestNormalizeWebLinkContentPreservesQueryAndFragment(t *testing.T) {
	normalized, err := normalizeWebLinkContent(`{"url":"http://127.0.0.1:19080/target-a?from=ework&x=1#entry"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(normalized, `\u0026`) {
		t.Fatalf("query ampersand should not be HTML-escaped: %s", normalized)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(normalized), &payload); err != nil {
		t.Fatalf("normalized content is not JSON: %v", err)
	}
	if payload["url"] != "http://127.0.0.1:19080/target-a?from=ework&x=1#entry" {
		t.Fatalf("url = %v", payload["url"])
	}
}
