package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/glebarez/sqlite"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/biz/service"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 认证 HTTP 契约:登录、me、退出、改密,以及 Cookie/CSRF/限流/错误映射。
const (
	authTestUsername = "admin"
	authTestPassword = "correct horse battery staple"
	authTestNewPass  = "second passphrase value 42"
	authTestOrigin   = "http://127.0.0.1:18081"
)

type authFixture struct {
	svc    *service.Service
	engine *server.Hertz
}

func newAuthFixture(t *testing.T, withAdmin bool) *authFixture {
	t.Helper()
	t.Chdir(t.TempDir())
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth-http.db")+"?_pragma=busy_timeout(10000)"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if err := conn.AutoMigrate(&model.AuthUser{}, &model.AuthSession{}); err != nil {
		t.Fatal(err)
	}
	svc := service.NewService(conn, nil, "", nil)
	svc.SetAuthSettings(service.AuthSettings{
		CookieName:      "rb_admin_auth_dev",
		CookieSecure:    false,
		IdleTimeout:     0,
		AbsoluteTimeout: 0,
		TrustedOrigins:  []string{authTestOrigin},
	})
	// 测试用短期限:验证过期语义但不必等待太久
	svc.SetAuthTimeouts(2e9, 8e9) // 2s / 8s
	if withAdmin {
		t.Setenv(service.AdminInitialPasswordEnv, authTestPassword)
		t.Setenv(service.AdminUsernameEnv, authTestUsername)
		if err := svc.EnsureAuthBootstrap(context.Background()); err != nil {
			t.Fatalf("auth bootstrap: %v", err)
		}
	}

	SetService(svc)
	t.Cleanup(func() { SetService(nil) })

	engine := server.New()
	engine.POST("/api/v1/auth/login", Login)
	engine.GET("/api/v1/auth/me", Me)
	engine.POST("/api/v1/auth/logout", Logout)
	engine.POST("/api/v1/auth/password", Password)
	return &authFixture{svc: svc, engine: engine}
}


func originHeader() ut.Header {
	return ut.Header{Key: "Origin", Value: authTestOrigin}
}

func jsonHeader() ut.Header {
	return ut.Header{Key: "Content-Type", Value: "application/json"}
}

func cookieHeader(token string) ut.Header {
	return ut.Header{Key: "Cookie", Value: "rb_admin_auth_dev=" + token}
}

func csrfHeader(value string) ut.Header {
	return ut.Header{Key: "X-CSRF-Token", Value: value}
}

type loginPayload struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type sessionBody struct {
	Code int32 `json:"code"`
	Data struct {
		User struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"user"`
		Session struct {
			ExpiresAt     string `json:"expires_at"`
			IdleExpiresAt string `json:"idle_expires_at"`
		} `json:"session"`
		CSRFToken string `json:"csrf_token"`
	} `json:"data"`
}

type errorBody struct {
	Code      int32  `json:"code"`
	Msg       string `json:"msg"`
	ErrorType string `json:"error_type"`
}

// login 执行一次登录并返回响应、Cookie 值与解析后的正文。
func login(t *testing.T, fixture *authFixture, password string, extraHeaders ...ut.Header) (*ut.ResponseRecorder, string, sessionBody) {
	t.Helper()
	body, err := json.Marshal(loginPayload{Username: authTestUsername, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	headers := append([]ut.Header{jsonHeader(), originHeader()}, extraHeaders...)
	response := ut.PerformRequest(fixture.engine.Engine, "POST", "/api/v1/auth/login", &ut.Body{Body: bytes.NewReader(body), Len: len(body)}, headers...)
	parsed := sessionBody{}
	_ = json.Unmarshal(response.Body.Bytes(), &parsed)
	token := ""
	if setCookie := string(response.Header().Get("Set-Cookie")); setCookie != "" {
		value := strings.SplitN(setCookie, ";", 2)[0]
		token = strings.TrimPrefix(value, "rb_admin_auth_dev=")
	}
	return response, token, parsed
}

func errorOf(t *testing.T, response *ut.ResponseRecorder) errorBody {
	t.Helper()
	parsed := errorBody{}
	if err := json.Unmarshal(response.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, response.Body.String())
	}
	return parsed
}

// TestAuthBootstrapRequiresInitialPassword A01:空库缺首次密码拒绝启动;再次启动不覆盖账号。
func TestAuthBootstrapRequiresInitialPassword(t *testing.T) {
	fixture := newAuthFixture(t, false)
	ctx := context.Background()
	if err := fixture.svc.EnsureAuthBootstrap(ctx); err == nil {
		t.Fatal("bootstrap without initial password must fail")
	} else if !strings.Contains(err.Error(), service.AdminInitialPasswordEnv) {
		t.Fatalf("error must name the missing setting, got: %v", err)
	}
	t.Setenv(service.AdminInitialPasswordEnv, "abcd")
	if err := fixture.svc.EnsureAuthBootstrap(ctx); err == nil {
		t.Fatal("bootstrap with invalid password must fail")
	}
	t.Setenv(service.AdminInitialPasswordEnv, authTestPassword)
	if err := fixture.svc.EnsureAuthBootstrap(ctx); err != nil {
		t.Fatalf("valid bootstrap: %v", err)
	}
	_, token, _ := login(t, fixture, authTestPassword)
	if token == "" {
		t.Fatal("login after bootstrap must set a cookie")
	}
	// 再次启动改变环境变量不能覆盖既有密码
	t.Setenv(service.AdminInitialPasswordEnv, authTestNewPass)
	if err := fixture.svc.EnsureAuthBootstrap(ctx); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	response, _, _ := login(t, fixture, authTestPassword)
	if response.Code != 200 {
		t.Fatalf("original password must keep working after restart: %d %s", response.Code, response.Body.String())
	}
}

// TestAuthLoginCookieAndMe A03/A04:成功登录设置 Cookie,me 恢复同一会话与 CSRF。
func TestAuthLoginCookieAndMe(t *testing.T) {
	fixture := newAuthFixture(t, true)

	// 缺 Origin 的浏览器式请求被拒绝(登录必须带合法来源)
	body, _ := json.Marshal(loginPayload{Username: authTestUsername, Password: authTestPassword})
	noOrigin := ut.PerformRequest(fixture.engine.Engine, "POST", "/api/v1/auth/login", &ut.Body{Body: bytes.NewReader(body), Len: len(body)}, jsonHeader())
	if noOrigin.Code != 403 {
		t.Fatalf("login without origin must be 403, got %d %s", noOrigin.Code, noOrigin.Body.String())
	}
	if parsed := errorOf(t, noOrigin); parsed.ErrorType != service.ErrTypeAuthOriginDenied {
		t.Fatalf("origin error type = %s", parsed.ErrorType)
	}

	response, token, parsed := login(t, fixture, authTestPassword)
	if response.Code != 200 {
		t.Fatalf("login: %d %s", response.Code, response.Body.String())
	}
	if parsed.Code != 0 || parsed.Data.User.Username != authTestUsername || parsed.Data.User.Role != "admin" {
		t.Fatalf("login payload mismatch: %s", response.Body.String())
	}
	if parsed.Data.User.ID == "" || parsed.Data.CSRFToken == "" || parsed.Data.Session.ExpiresAt == "" {
		t.Fatalf("login payload missing fields: %s", response.Body.String())
	}
	setCookie := string(response.Header().Get("Set-Cookie"))
	for _, attribute := range []string{"HttpOnly", "SameSite=Lax", "Path=/", "Max-Age="} {
		if !strings.Contains(setCookie, attribute) {
			t.Fatalf("cookie missing %s: %s", attribute, setCookie)
		}
	}
	if strings.Contains(response.Body.String(), authTestPassword) || strings.Contains(response.Body.String(), "argon2") {
		t.Fatalf("login response must not leak secrets: %s", response.Body.String())
	}
	if cache := string(response.Header().Get("Cache-Control")); cache != "no-store" {
		t.Fatalf("login must not be cached: %q", cache)
	}

	me := ut.PerformRequest(fixture.engine.Engine, "GET", "/api/v1/auth/me", nil, cookieHeader(token))
	if me.Code != 200 {
		t.Fatalf("me: %d %s", me.Code, me.Body.String())
	}
	meBody := sessionBody{}
	if err := json.Unmarshal(me.Body.Bytes(), &meBody); err != nil {
		t.Fatal(err)
	}
	if meBody.Data.CSRFToken != parsed.Data.CSRFToken {
		t.Fatalf("me must reuse the session CSRF value without rotating it")
	}

	// 未知账号与错误密码提示一致
	unknownBody, _ := json.Marshal(loginPayload{Username: "ghost_user", Password: authTestPassword})
	unknown := ut.PerformRequest(fixture.engine.Engine, "POST", "/api/v1/auth/login", &ut.Body{Body: bytes.NewReader(unknownBody), Len: len(unknownBody)}, jsonHeader(), originHeader())
	wrongBody, _ := json.Marshal(loginPayload{Username: authTestUsername, Password: "definitely wrong password"})
	wrong := ut.PerformRequest(fixture.engine.Engine, "POST", "/api/v1/auth/login", &ut.Body{Body: bytes.NewReader(wrongBody), Len: len(wrongBody)}, jsonHeader(), originHeader())
	for name, response := range map[string]*ut.ResponseRecorder{"unknown": unknown, "wrong": wrong} {
		if response.Code != 401 {
			t.Fatalf("%s must be 401: %d", name, response.Code)
		}
		parsed := errorOf(t, response)
		if parsed.ErrorType != service.ErrTypeAuthInvalidCredentials {
			t.Fatalf("%s error type = %s", name, parsed.ErrorType)
		}
	}
	if errorOf(t, unknown).Msg != errorOf(t, wrong).Msg {
		t.Fatal("unknown account and wrong password must share the same message")
	}
}

// TestAuthLogoutRequiresCSRFAndRevokes A06/A13:退出先校验 CSRF,成功后同一 Cookie 立即失效。
func TestAuthLogoutRequiresCSRFAndRevokes(t *testing.T) {
	fixture := newAuthFixture(t, true)
	_, token, parsed := login(t, fixture, authTestPassword)

	noCSRF := ut.PerformRequest(fixture.engine.Engine, "POST", "/api/v1/auth/logout", &ut.Body{Body: bytes.NewReader([]byte("{}")), Len: 2}, jsonHeader(), originHeader(), cookieHeader(token))
	if noCSRF.Code != 403 {
		t.Fatalf("logout without CSRF must be 403, got %d %s", noCSRF.Code, noCSRF.Body.String())
	}
	if parsedType := errorOf(t, noCSRF).ErrorType; parsedType != service.ErrTypeAuthCSRFInvalid {
		t.Fatalf("csrf error type = %s", parsedType)
	}
	stillValid := ut.PerformRequest(fixture.engine.Engine, "GET", "/api/v1/auth/me", nil, cookieHeader(token))
	if stillValid.Code != 200 {
		t.Fatalf("failed CSRF check must not revoke the session: %d", stillValid.Code)
	}

	logout := ut.PerformRequest(fixture.engine.Engine, "POST", "/api/v1/auth/logout", &ut.Body{Body: bytes.NewReader([]byte("{}")), Len: 2}, jsonHeader(), originHeader(), cookieHeader(token), csrfHeader(parsed.Data.CSRFToken))
	if logout.Code != 200 || !strings.Contains(logout.Body.String(), `"logged_out":true`) {
		t.Fatalf("logout: %d %s", logout.Code, logout.Body.String())
	}
	cleared := string(logout.Header().Get("Set-Cookie"))
	if !strings.Contains(cleared, "rb_admin_auth_dev=") || !strings.Contains(cleared, "Path=/") {
		t.Fatalf("logout must clear the cookie with the same path: %s", cleared)
	}
	after := ut.PerformRequest(fixture.engine.Engine, "GET", "/api/v1/auth/me", nil, cookieHeader(token))
	if after.Code != 401 {
		t.Fatalf("revoked session must fail: %d", after.Code)
	}
	repeat := ut.PerformRequest(fixture.engine.Engine, "POST", "/api/v1/auth/logout", &ut.Body{Body: bytes.NewReader([]byte("{}")), Len: 2}, jsonHeader(), originHeader(), cookieHeader(token), csrfHeader(parsed.Data.CSRFToken))
	if repeat.Code != 200 {
		t.Fatalf("repeated logout must stay 200: %d %s", repeat.Code, repeat.Body.String())
	}
}

// TestAuthPasswordChangeRevokesEverything A08:改密撤销全部会话,旧密码失效、新密码可用。
func TestAuthPasswordChangeRevokesEverything(t *testing.T) {
	fixture := newAuthFixture(t, true)
	_, firstToken, first := login(t, fixture, authTestPassword)
	_, secondToken, second := login(t, fixture, authTestPassword)

	shortBody, _ := json.Marshal(map[string]string{"current_password": authTestPassword, "new_password": "abcd"})
	short := ut.PerformRequest(fixture.engine.Engine, "POST", "/api/v1/auth/password", &ut.Body{Body: bytes.NewReader(shortBody), Len: len(shortBody)}, jsonHeader(), originHeader(), cookieHeader(firstToken), csrfHeader(first.Data.CSRFToken))
	if short.Code != 400 || errorOf(t, short).ErrorType != service.ErrTypeAuthInvalidRequest {
		t.Fatalf("short password must be 400 AUTH_INVALID_REQUEST: %d %s", short.Code, short.Body.String())
	}
	sameBody, _ := json.Marshal(map[string]string{"current_password": authTestPassword, "new_password": authTestPassword})
	same := ut.PerformRequest(fixture.engine.Engine, "POST", "/api/v1/auth/password", &ut.Body{Body: bytes.NewReader(sameBody), Len: len(sameBody)}, jsonHeader(), originHeader(), cookieHeader(firstToken), csrfHeader(first.Data.CSRFToken))
	if same.Code != 400 {
		t.Fatalf("same password must be rejected: %d", same.Code)
	}
	wrongBody, _ := json.Marshal(map[string]string{"current_password": "not the current one", "new_password": authTestNewPass})
	wrong := ut.PerformRequest(fixture.engine.Engine, "POST", "/api/v1/auth/password", &ut.Body{Body: bytes.NewReader(wrongBody), Len: len(wrongBody)}, jsonHeader(), originHeader(), cookieHeader(firstToken), csrfHeader(first.Data.CSRFToken))
	if wrong.Code != 400 || errorOf(t, wrong).ErrorType != service.ErrTypeAuthCurrentPassword {
		t.Fatalf("wrong current password must be 400 AUTH_CURRENT_PASSWORD_INVALID: %d %s", wrong.Code, wrong.Body.String())
	}
	if check := ut.PerformRequest(fixture.engine.Engine, "GET", "/api/v1/auth/me", nil, cookieHeader(secondToken)); check.Code != 200 {
		t.Fatal("failed password change must not revoke other sessions")
	}
	_ = second

	changeBody, _ := json.Marshal(map[string]string{"current_password": authTestPassword, "new_password": authTestNewPass})
	change := ut.PerformRequest(fixture.engine.Engine, "POST", "/api/v1/auth/password", &ut.Body{Body: bytes.NewReader(changeBody), Len: len(changeBody)}, jsonHeader(), originHeader(), cookieHeader(firstToken), csrfHeader(first.Data.CSRFToken))
	if change.Code != 200 || !strings.Contains(change.Body.String(), `"reauth_required":true`) {
		t.Fatalf("password change: %d %s", change.Code, change.Body.String())
	}
	for name, token := range map[string]string{"first": firstToken, "second": secondToken} {
		if response := ut.PerformRequest(fixture.engine.Engine, "GET", "/api/v1/auth/me", nil, cookieHeader(token)); response.Code != 401 {
			t.Fatalf("%s session must be revoked after password change: %d", name, response.Code)
		}
	}
	if oldLogin, _, _ := login(t, fixture, authTestPassword); oldLogin.Code != 401 {
		t.Fatalf("old password must fail: %d", oldLogin.Code)
	}
	if newLogin, _, _ := login(t, fixture, authTestNewPass); newLogin.Code != 200 {
		t.Fatalf("new password must work: %d %s", newLogin.Code, newLogin.Body.String())
	}
}

// TestAuthRequestGuardsAndRateLimit A04/A16:类型、体积、格式与登录限流。
func TestAuthRequestGuardsAndRateLimit(t *testing.T) {
	fixture := newAuthFixture(t, true)
	body, _ := json.Marshal(loginPayload{Username: authTestUsername, Password: authTestPassword})

	notJSON := ut.PerformRequest(fixture.engine.Engine, "POST", "/api/v1/auth/login", &ut.Body{Body: bytes.NewReader(body), Len: len(body)}, ut.Header{Key: "Content-Type", Value: "text/plain"}, originHeader())
	if notJSON.Code != 415 {
		t.Fatalf("non-JSON content type must be 415: %d %s", notJSON.Code, notJSON.Body.String())
	}
	unknownField, _ := json.Marshal(map[string]string{"username": authTestUsername, "password": authTestPassword, "extra": "x"})
	strict := ut.PerformRequest(fixture.engine.Engine, "POST", "/api/v1/auth/login", &ut.Body{Body: bytes.NewReader(unknownField), Len: len(unknownField)}, jsonHeader(), originHeader())
	if strict.Code != 400 {
		t.Fatalf("unknown fields must be rejected: %d %s", strict.Code, strict.Body.String())
	}
	oversized := `{"username":"` + authTestUsername + `","password":"` + strings.Repeat("x", 4200) + `"}`
	large := ut.PerformRequest(fixture.engine.Engine, "POST", "/api/v1/auth/login", &ut.Body{Body: bytes.NewReader([]byte(oversized)), Len: len(oversized)}, jsonHeader(), originHeader())
	if large.Code != 413 {
		t.Fatalf("oversized body must be 413: %d %s", large.Code, large.Body.String())
	}

	// 同一来源第 6 次尝试被限流,并给出 Retry-After
	statuses := make([]int, 0, 6)
	for i := 0; i < 6; i++ {
		response, _, _ := login(t, fixture, authTestPassword)
		statuses = append(statuses, response.Code)
		if response.Code == 429 {
			if after := string(response.Header().Get("Retry-After")); after == "" {
				t.Fatal("429 must carry Retry-After")
			}
			if errorOf(t, response).ErrorType != service.ErrTypeAuthRateLimited {
				t.Fatalf("429 error type = %s", errorOf(t, response).ErrorType)
			}
		}
	}
	if statuses[5] != 429 {
		t.Fatalf("sixth attempt must be rate limited: %v", statuses)
	}
}
