//go:build dev

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestStaticFallbackDoesNotSucceedForUnknownAPI(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html>SPA</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.js"), []byte("console.log('qa')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.css"), []byte("body{color:#111}"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FRONTEND_DIR", root)
	for _, prefix := range []string{"", "/rainbow-bridge"} {
		h := server.New()
		h.GET(prefix+"/api/v1/version", func(_ context.Context, c *app.RequestContext) { c.JSON(200, map[string]int{"code": 200}) })
		spa := setupStaticFileServer(h, prefix)
		setupHTTPFallback(h, prefix, spa)
		for _, method := range []string{"GET", "POST", "HEAD"} {
			r := ut.PerformRequest(h.Engine, method, prefix+"/api/v1/unknown", nil)
			if r.Code != 404 {
				t.Fatalf("%s %s code=%d", prefix, method, r.Code)
			}
			if !strings.Contains(string(r.Header().ContentType()), "application/json") {
				t.Fatalf("%s %s content-type=%q", prefix, method, r.Header().ContentType())
			}
			if string(r.Header().Peek("Cache-Control")) != "no-store" {
				t.Fatalf("%s %s missing no-store", prefix, method)
			}
			if method == "HEAD" {
				if r.Body.Len() != 0 {
					t.Fatal("HEAD has body")
				}
				continue
			}
			var body struct {
				Code      int    `json:"code"`
				ErrorType string `json:"error_type"`
			}
			if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
				t.Fatalf("%s %s invalid JSON: %v", prefix, method, err)
			}
			if body.Code != 404 || body.ErrorType != apiNotFoundType {
				t.Fatalf("%s %s body=%+v", prefix, method, body)
			}
		}
		if r := ut.PerformRequest(h.Engine, "OPTIONS", prefix+"/api/v1/unknown", nil); r.Code != 204 {
			t.Fatalf("%s OPTIONS code=%d", prefix, r.Code)
		}
		if r := ut.PerformRequest(h.Engine, "GET", prefix+"/api/v1/version", nil); r.Code != 200 {
			t.Fatal("registered API shadowed")
		}
		if r := ut.PerformRequest(h.Engine, "GET", prefix+"/config", nil); r.Code != 200 || r.Body.String() != "<html>SPA</html>" {
			t.Fatalf("SPA refresh: %d %s", r.Code, r.Body.String())
		}
		if r := ut.PerformRequest(h.Engine, "GET", prefix+"/app.js", nil); r.Code != 200 ||
			!strings.Contains(string(r.Header().ContentType()), "javascript") || r.Body.String() != "console.log('qa')" {
			t.Fatalf("JS asset: %d type=%s body=%s", r.Code, r.Header().ContentType(), r.Body.String())
		}
		if r := ut.PerformRequest(h.Engine, "GET", prefix+"/app.css", nil); r.Code != 200 ||
			!strings.Contains(string(r.Header().ContentType()), "text/css") {
			t.Fatalf("CSS asset: %d type=%s", r.Code, r.Header().ContentType())
		}
		if r := ut.PerformRequest(h.Engine, "GET", prefix+"/missing.js", nil); r.Code != 404 ||
			r.Body.String() == "<html>SPA</html>" {
			t.Fatalf("missing static file must keep 404: %d %s", r.Code, r.Body.String())
		}
		if r := ut.PerformRequest(h.Engine, "GET", prefix+"/apix", nil); r.Code != 200 || r.Body.String() != "<html>SPA</html>" {
			t.Fatalf("similar segment must remain a page: %d %s", r.Code, r.Body.String())
		}
	}
}

func TestAPIFallbackSurvivesMissingIndex(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FRONTEND_DIR", root)
	h := server.New()
	spa := setupStaticFileServer(h, "/rainbow-bridge")
	setupHTTPFallback(h, "/rainbow-bridge", spa)
	if r := ut.PerformRequest(h.Engine, "GET", "/rainbow-bridge/config", nil); r.Code != 404 {
		t.Fatalf("missing index should be plain 404, got %d %s", r.Code, r.Body.String())
	}
	r := ut.PerformRequest(h.Engine, "GET", "/rainbow-bridge/api/v1/unknown", nil)
	if r.Code != 404 || !strings.Contains(r.Body.String(), apiNotFoundType) {
		t.Fatalf("API fallback must survive missing index: %d %s", r.Code, r.Body.String())
	}
}

func TestStaticFallbackClassifiesDuplicatePrefixAsAPI(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html>SPA</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FRONTEND_DIR", root)
	h := server.New()
	spa := setupStaticFileServer(h, "/rainbow-bridge")
	setupHTTPFallback(h, "/rainbow-bridge", spa)
	for _, path := range []string{
		"/rainbow-bridge/rainbow-bridge/api/v1/unknown",
		"/api/v1/unknown",
	} {
		r := ut.PerformRequest(h.Engine, "GET", path, nil)
		if r.Code != 404 || !strings.Contains(string(r.Header().ContentType()), "application/json") {
			t.Fatalf("%s -> %d %s", path, r.Code, r.Body.String())
		}
	}
	if r := ut.PerformRequest(h.Engine, "GET", "/rainbow-bridge-other/api/v1/unknown", nil); r.Code != 200 || r.Body.String() != "<html>SPA</html>" {
		t.Fatalf("similar prefix must remain a page: %d %s", r.Code, r.Body.String())
	}
}
