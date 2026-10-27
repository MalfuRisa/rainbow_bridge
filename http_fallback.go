package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

const apiNotFoundType = "API_NOT_FOUND"

// setupHTTPFallback registers the API-local 404 independently of frontend
// initialization. setupStaticFileServer may provide a SPA fallback for non-API
// page paths; that fallback must not replace this API classification.
func setupHTTPFallback(h interface {
	NoRoute(handlers ...app.HandlerFunc)
}, basePath string, spa func(context.Context, *app.RequestContext)) {
	h.NoRoute(func(ctx context.Context, c *app.RequestContext) {
		if isAPIPath(string(c.URI().Path()), basePath) {
			writeAPI404(c)
			return
		}
		if spa != nil {
			spa(ctx, c)
			return
		}
		c.Status(consts.StatusNotFound)
	})
}

// isAPIPath classifies only complete path segments. It intentionally treats
// the root /api namespace, a correctly prefixed /api, and an accidentally
// duplicated deployment prefix before /api as API misses. Similar names such
// as /apix, /apiary, /docs/api-guide, or /rainbow-bridge-other/api stay pages.
func isAPIPath(requestPath, basePath string) bool {
	if hasAPISegment(requestPath) {
		return true
	}
	base := "/" + strings.Trim(basePath, "/")
	if base == "/" {
		return false
	}
	candidate := requestPath
	for depth := 0; depth < 4; depth++ {
		if candidate == base+"/api" || strings.HasPrefix(candidate, base+"/api/") {
			return true
		}
		if !strings.HasPrefix(candidate, base+"/") {
			break
		}
		candidate = strings.TrimPrefix(candidate, base)
		if hasAPISegment(candidate) {
			return true
		}
	}
	return false
}

func hasAPISegment(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

// writeAPI404 is the single unknown-API response. It never echoes the URL,
// query, body, or a filesystem path.
func writeAPI404(c *app.RequestContext) {
	c.Header("Cache-Control", "no-store")
	if string(c.Method()) == consts.MethodHead {
		c.Status(consts.StatusNotFound)
		// Hertz's NoRoute helper replaces an empty 404 with text/plain after the
		// handler. An empty body stream keeps the JSON content type while HEAD
		// still carries no bytes.
		c.Response.SetBodyStream(http.NoBody, 0)
		c.Response.Header.SetContentType("application/json; charset=utf-8")
		return
	}
	if string(c.Method()) == consts.MethodOptions {
		c.Status(consts.StatusNoContent)
		return
	}
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.JSON(consts.StatusNotFound, map[string]any{
		"code":       consts.StatusNotFound,
		"msg":        "not found",
		"error":      "API endpoint not found",
		"error_type": apiNotFoundType,
	})
}
