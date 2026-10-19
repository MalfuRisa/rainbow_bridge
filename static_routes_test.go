//go:build dev

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestStaticFallbackDoesNotSucceedForUnknownAPI(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "index.html"), []byte("<html>SPA</html>"), 0644)
	t.Setenv("FRONTEND_DIR", root)
	for _, prefix := range []string{"", "/rainbow-bridge"} {
		h := server.New()
		h.GET(prefix+"/api/v1/version", func(_ context.Context, c *app.RequestContext) { c.JSON(200, map[string]int{"code": 200}) })
		setupStaticFileServer(h, prefix)
		for _, method := range []string{"GET", "POST", "HEAD"} {
			r := ut.PerformRequest(h.Engine, method, prefix+"/api/v1/unknown", nil)
			if r.Code != 404 {
				t.Fatalf("%s %s code=%d", prefix, method, r.Code)
			}
			if method == "HEAD" && r.Body.Len() != 0 {
				t.Fatal("HEAD has body")
			}
			if method != "HEAD" && r.Body.Len() == 0 {
				t.Fatal("error JSON missing")
			}
		}
		if r := ut.PerformRequest(h.Engine, "GET", prefix+"/api/v1/version", nil); r.Code != 200 {
			t.Fatal("registered API shadowed")
		}
		if r := ut.PerformRequest(h.Engine, "GET", prefix+"/config", nil); r.Code != 200 || r.Body.String() != "<html>SPA</html>" {
			t.Fatalf("SPA refresh: %d %s", r.Code, r.Body.String())
		}
	}
}
