package middleware

import (
	"context"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestUnknownAPIDoesNotBecomeAuthenticationFailure(t *testing.T) {
	engine := server.New()
	engine.Use(AdminSession(nil, "/rainbow-bridge"))
	engine.GET("/*filepath", func(ctx context.Context, c *app.RequestContext) { c.Status(404) })
	response := ut.PerformRequest(engine.Engine, "GET", "/rainbow-bridge/api/v1/unknown", nil)
	if response.Code != 404 {
		t.Fatalf("unknown API status=%d, want 404", response.Code)
	}
}

func TestPublicRoutesAreExact(t *testing.T) {
	for _, item := range []struct {
		method string
		path   string
		public bool
	}{
		{"GET", "/api/v1/runtime/release/static", true},
		{"GET", "/api/v1/asset/file/id/image.png", true},
		{"GET", "/api/v1/version/latest", true},
		{"GET", "/api/v1/version-admin", false},
		{"GET", "/api/v1/runtime/admin", false},
		{"POST", "/api/v1/runtime/release", false},
		{"GET", "/api/v1/auth/me", false},
		{"OPTIONS", "/api/v1/config/create", false},
	} {
		if got := IsPublicRequest(item.method, item.path); got != item.public {
			t.Errorf("%s %s: public=%v, want %v", item.method, item.path, got, item.public)
		}
	}
}

func TestManagementAndAuthCORSUseTrustedOrigin(t *testing.T) {
	const trusted = "http://127.0.0.1:3001"
	for _, item := range []struct {
		method string
		path   string
		origin string
		want   string
		creds  string
	}{
		{"POST", "/api/v1/auth/login", trusted, trusted, "true"},
		{"GET", "/api/v1/auth/me", trusted, trusted, "true"},
		{"OPTIONS", "/api/v1/auth/login", trusted, trusted, "true"},
		{"OPTIONS", "/api/v1/config/create", trusted, trusted, "true"},
		{"POST", "/api/v1/config/create", "https://untrusted.example", "", ""},
		{"GET", "/api/v1/runtime/release", "https://business.example", "*", "false"},
	} {
		t.Run(item.method+item.path+item.origin, func(t *testing.T) {
			engine := server.New()
			engine.Use(CORS(nil, []string{trusted}, "/rainbow-bridge"))
			response := ut.PerformRequest(engine.Engine, item.method, "/rainbow-bridge"+item.path, nil,
				ut.Header{Key: "Origin", Value: item.origin},
				ut.Header{Key: "Access-Control-Request-Method", Value: "POST"})
			if got := response.Header().Get("Access-Control-Allow-Origin"); got != item.want {
				t.Errorf("allowed origin=%q, want %q", got, item.want)
			}
			if got := response.Header().Get("Access-Control-Allow-Credentials"); got != item.creds {
				t.Errorf("credentials=%q, want %q", got, item.creds)
			}
			if item.method == "OPTIONS" && response.Code != 204 {
				t.Errorf("preflight status=%d, want 204", response.Code)
			}
		})
	}
}
