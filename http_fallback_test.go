package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestAPIFallbackJSONWithoutFrontend(t *testing.T) {
	for _, prefix := range []string{"", "/rainbow-bridge"} {
		h := server.New()
		h.GET(prefix+"/api/v1/version", func(_ context.Context, c *app.RequestContext) {
			c.JSON(200, map[string]int{"code": 200})
		})
		setupHTTPFallback(h, prefix, nil)

		for _, method := range []string{"GET", "POST", "HEAD"} {
			r := ut.PerformRequest(h.Engine, method, prefix+"/api/v1/unknown", nil)
			if r.Code != 404 || !strings.Contains(string(r.Header().ContentType()), "application/json") ||
				string(r.Header().Peek("Cache-Control")) != "no-store" {
				t.Fatalf("%s %s -> %d type=%q cache=%q", prefix, method, r.Code,
					r.Header().ContentType(), r.Header().Peek("Cache-Control"))
			}
			if method == "HEAD" {
				if r.Body.Len() != 0 {
					t.Fatalf("%s HEAD body must be empty", prefix)
				}
				continue
			}
			var body struct {
				Code      int    `json:"code"`
				Msg       string `json:"msg"`
				Error     string `json:"error"`
				ErrorType string `json:"error_type"`
			}
			if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
				t.Fatalf("%s %s invalid JSON: %v", prefix, method, err)
			}
			if body.Code != 404 || body.ErrorType != apiNotFoundType ||
				body.Msg != "not found" || body.Error != "API endpoint not found" {
				t.Fatalf("%s %s body=%+v", prefix, method, body)
			}
		}
		if r := ut.PerformRequest(h.Engine, "OPTIONS", prefix+"/api/v1/unknown", nil); r.Code != 204 {
			t.Fatalf("%s OPTIONS -> %d", prefix, r.Code)
		}
		if r := ut.PerformRequest(h.Engine, "GET", prefix+"/api/v1/version", nil); r.Code != 200 {
			t.Fatalf("%s registered route shadowed", prefix)
		}
		if r := ut.PerformRequest(h.Engine, "GET", prefix+"/not-api", nil); r.Code != 404 {
			t.Fatalf("%s non-API unknown path should stay a normal 404: %d", prefix, r.Code)
		}
	}
}

func TestAPIFallbackPathSegments(t *testing.T) {
	base := "/rainbow-bridge"
	cases := []struct {
		path string
		api  bool
	}{
		{"/api", true},
		{"/api/v1/version", true},
		{"/rainbow-bridge/api", true},
		{"/rainbow-bridge/api/v1/unknown", true},
		{"/rainbow-bridge/rainbow-bridge/api/v1/unknown", true},
		{"/rainbow-bridge/rainbow-bridge/rainbow-bridge/api/v1/unknown", true},
		{"/apix", false},
		{"/apiary", false},
		{"/docs/api-guide", false},
		{"/rainbow-bridge-other/api/v1/unknown", false},
		{"/rainbow-bridge/config", false},
	}
	for _, item := range cases {
		if got := isAPIPath(item.path, base); got != item.api {
			t.Fatalf("isAPIPath(%q)=%v want %v", item.path, got, item.api)
		}
	}
}

func TestAPIFallbackDuplicatePrefixDoesNotExecuteRealHandler(t *testing.T) {
	h := server.New()
	called := false
	h.GET("/rainbow-bridge/api/v1/version", func(_ context.Context, c *app.RequestContext) {
		called = true
		c.JSON(200, map[string]int{"code": 200})
	})
	setupHTTPFallback(h, "/rainbow-bridge", nil)
	r := ut.PerformRequest(h.Engine, "GET", "/rainbow-bridge/rainbow-bridge/api/v1/version", nil)
	if r.Code != 404 || called {
		t.Fatalf("duplicate prefix returned %d called=%v", r.Code, called)
	}
	if !strings.Contains(r.Body.String(), apiNotFoundType) {
		t.Fatalf("missing API_NOT_FOUND: %s", r.Body.String())
	}
}
