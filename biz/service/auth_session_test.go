package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 会话规则:空闲/绝对期限、滑动续期、上限 10、撤销不可复活与并发改密。
const (
	sessionTestUser     = "admin"
	sessionTestPassword = "unit test passphrase value"
	sessionTestNextPass = "another unit test passphrase"
)

type sessionFixture struct {
	svc   *Service
	clock *time.Time
	origins []string
}

func newSessionFixture(t *testing.T) *sessionFixture {
	t.Helper()
	t.Chdir(t.TempDir())
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth-session.db")+"?_pragma=busy_timeout(10000)"),
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
	svc := NewService(conn, nil, "", nil)
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	clock := now
	svc.SetAuthClockForTest(func() time.Time { return clock })
	svc.SetAuthSettings(AuthSettings{
		CookieName:      "rb_admin_auth_dev",
		CookieSecure:    false,
		IdleTimeout:     2 * time.Minute,
		AbsoluteTimeout: 8 * time.Minute,
		TrustedOrigins:  []string{"http://127.0.0.1:18081"},
	})
	svc.SetAuthClockForTest(func() time.Time { return clock })
	t.Setenv(AdminInitialPasswordEnv, sessionTestPassword)
	t.Setenv(AdminUsernameEnv, sessionTestUser)
	if err := svc.EnsureAuthBootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	fixture := &sessionFixture{svc: svc, clock: &clock}
	// 时钟指针经 SetAuthClockForTest 闭包读取
	svc.SetAuthClockForTest(func() time.Time { return *fixture.clock })
	return fixture
}

func (f *sessionFixture) advance(d time.Duration) {
	*f.clock = f.clock.Add(d)
}

func (f *sessionFixture) login(t *testing.T) *AuthLoginResult {
	t.Helper()
	return f.loginFrom(t, "127.0.0.1")
}

// loginFrom 用指定客户端来源登录(多会话场景避免触发同 IP 限流)。
func (f *sessionFixture) loginFrom(t *testing.T, clientIP string) *AuthLoginResult {
	t.Helper()
	result, err := f.svc.Login(context.Background(), AuthLoginInput{
		Username:  sessionTestUser,
		Password:  sessionTestPassword,
		ClientIP:  clientIP,
		Origin:    "http://127.0.0.1:18081",
		HasOrigin: true,
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return result
}

func (f *sessionFixture) me(t *testing.T, token string) (*AuthMeResult, error) {
	t.Helper()
	return f.svc.CurrentSession(context.Background(), AuthRequestInput{SessionToken: token})
}

// TestAuthSessionIdleAndAbsoluteExpiry A07:空闲到期、滑动续期与绝对期限。
func TestAuthSessionIdleAndAbsoluteExpiry(t *testing.T) {
	fixture := newSessionFixture(t)
	session := fixture.login(t)

	// 1 分钟后活动:空闲期限滑动,总期限不突破绝对上限
	fixture.advance(time.Minute)
	me, err := fixture.me(t, session.Token)
	if err != nil {
		t.Fatalf("me within idle window: %v", err)
	}
	if !me.Session.IdleExpiresAt.After(session.Session.IdleExpiresAt) {
		t.Fatal("valid request must slide the idle deadline")
	}
	if me.Session.ExpiresAt.After(session.Session.ExpiresAt) {
		t.Fatal("sliding must not extend the absolute deadline")
	}

	// 空闲超过 2 分钟未活动 → 401
	silent := fixture.login(t)
	fixture.advance(3 * time.Minute)
	if _, err := fixture.me(t, silent.Token); err == nil {
		t.Fatal("idle-expired session must be rejected")
	} else if authErr, ok := AsAuthError(err); !ok || authErr.ErrorType != ErrTypeAuthRequired {
		t.Fatalf("idle expiry error = %v", err)
	}

	// 持续推进到绝对期限之后:即使一直有活动也必须失效
	active := fixture.login(t)
	for i := 0; i < 9; i++ {
		fixture.advance(time.Minute)
		if _, err := fixture.me(t, active.Token); err != nil {
			break
		}
	}
	fixture.advance(time.Minute)
	if _, err := fixture.me(t, active.Token); err == nil {
		t.Fatal("session must expire at the absolute deadline")
	}
}

// TestAuthSessionLimitPerUser A17:每账号最多 10 个有效会话,超出撤销最早创建的。
func TestAuthSessionLimitPerUser(t *testing.T) {
	fixture := newSessionFixture(t)
	tokens := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		tokens = append(tokens, fixture.loginFrom(t, fmt.Sprintf("127.0.0.%d", i+1)).Token)
		fixture.advance(time.Second)
	}
	count, err := fixture.svc.logic.authDAO.CountUsers(context.Background(), fixture.svc.logic.db)
	if err != nil || count != 1 {
		t.Fatalf("fixture expects one account: %d %v", count, err)
	}
	user, err := fixture.svc.logic.authDAO.GetUserByUsername(context.Background(), fixture.svc.logic.db, sessionTestUser)
	if err != nil {
		t.Fatal(err)
	}
	active, err := fixture.svc.logic.authDAO.ListActiveSessionsByUser(context.Background(), fixture.svc.logic.db, user.ID, *fixture.clock)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) > AuthSessionMaxPerUser {
		t.Fatalf("active sessions = %d, want <= %d", len(active), AuthSessionMaxPerUser)
	}
	// 最早的两个必须已失效,最新的仍可用
	if _, err := fixture.me(t, tokens[0]); err == nil {
		t.Fatal("oldest session must be revoked when the limit is exceeded")
	}
	if _, err := fixture.me(t, tokens[len(tokens)-1]); err != nil {
		t.Fatalf("newest session must stay valid: %v", err)
	}
}

// TestAuthSessionRevocationIsFinal:撤销或过期的会话不能被续期复活。
func TestAuthSessionRevocationIsFinal(t *testing.T) {
	fixture := newSessionFixture(t)
	session := fixture.login(t)
	tokenHash := sessionTokenHash(session.Token)
	now := *fixture.clock
	if _, err := fixture.svc.logic.authDAO.RevokeSession(context.Background(), fixture.svc.logic.db, tokenHash, now); err != nil {
		t.Fatal(err)
	}
	row, err := fixture.svc.logic.authDAO.GetSessionByTokenHash(context.Background(), fixture.svc.logic.db, tokenHash)
	if err != nil || row == nil {
		t.Fatalf("session row: %v", err)
	}
	touched, err := fixture.svc.logic.authDAO.TouchSession(context.Background(), fixture.svc.logic.db, row.ID, row.CredentialVersion, now.Add(time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if touched {
		t.Fatal("revoked session must not be refreshed")
	}
	if _, err := fixture.me(t, session.Token); err == nil {
		t.Fatal("revoked session must stay invalid")
	}
}

// TestAuthConcurrentPasswordChange A09:并发改密只有一项成功,另一项返回凭据冲突。
func TestAuthConcurrentPasswordChange(t *testing.T) {
	fixture := newSessionFixture(t)
	first := fixture.login(t)
	second := fixture.login(t)
	type outcome struct {
		err error
	}
	results := make(chan outcome, 2)
	var start sync.WaitGroup
	start.Add(1)
	change := func(token, csrf, newPassword string) {
		start.Wait()
		err := fixture.svc.ChangePassword(context.Background(), AuthPasswordChangeInput{
			AuthRequestInput: AuthRequestInput{SessionToken: token, CSRFToken: csrf},
			CurrentPassword:  sessionTestPassword,
			NewPassword:      newPassword,
		})
		results <- outcome{err: err}
	}
	go change(first.Token, first.CSRFToken, sessionTestNextPass)
	go change(second.Token, second.CSRFToken, sessionTestNextPass+" two")
	start.Done()

	var okCount, conflictCount int
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err == nil {
			okCount++
			continue
		}
		authErr, ok := AsAuthError(result.err)
		if !ok {
			t.Fatalf("unexpected error: %v", result.err)
		}
		switch authErr.ErrorType {
		case ErrTypeAuthCredentialChanged, ErrTypeAuthRequired, ErrTypeAuthCSRFInvalid:
			conflictCount++
		default:
			t.Fatalf("unexpected error type: %s (%v)", authErr.ErrorType, authErr)
		}
	}
	if okCount != 1 || conflictCount != 1 {
		t.Fatalf("concurrent password change must have exactly one winner: ok=%d conflict=%d", okCount, conflictCount)
	}
	// 全部会话失效,且只有新密码可用(具体哪个新密码取决于胜者)
	active, err := fixture.svc.logic.authDAO.ListActiveSessionsByUser(context.Background(), fixture.svc.logic.db, first.User.ID, *fixture.clock)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("password change must revoke every session, got %d active", len(active))
	}
	oldLogin, err := fixture.svc.Login(context.Background(), AuthLoginInput{
		Username: sessionTestUser, Password: sessionTestPassword, ClientIP: "127.0.0.1", HasOrigin: true, Origin: "http://127.0.0.1:18081",
	})
	if err == nil || oldLogin != nil {
		t.Fatal("old password must not work after the change")
	}
}

// TestAuthPasswordAndUsernameRules:长度与字符规则先校验,不启动昂贵计算。
func TestAuthPasswordAndUsernameRules(t *testing.T) {
	cases := []struct {
		username string
		password string
		valid    bool
	}{
		{"admin", strings.Repeat("a", 14), false},
		{"admin", strings.Repeat("a", 15), true},
		{"admin", strings.Repeat("汉", 15), true},
		{"admin", strings.Repeat("x", 129), false},
		{"admin", strings.Repeat("x", 600), false},
		{"ab", strings.Repeat("a", 15), false},
		{"admin!", strings.Repeat("a", 15), false},
		{"Admin", strings.Repeat("a", 15), true},
		{strings.Repeat("a", 33), strings.Repeat("a", 15), false},
	}
	for _, tc := range cases {
		got := IsValidAuthUsername(tc.username) && IsValidAuthPassword(tc.password)
		if got != tc.valid {
			t.Fatalf("rules(%q,%d chars) = %v, want %v", tc.username, len(tc.password), got, tc.valid)
		}
	}
	hash, err := hashAdminPassword(sessionTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash must be PHC argon2id: %s", hash)
	}
	if strings.Contains(hash, sessionTestPassword) {
		t.Fatal("hash must not contain the password")
	}
	ok, err := verifyAdminPassword(hash, sessionTestPassword)
	if err != nil || !ok {
		t.Fatalf("verify: %v %v", ok, err)
	}
	if ok, _ := verifyAdminPassword(hash, sessionTestPassword+"x"); ok {
		t.Fatal("wrong password must not verify")
	}
	// 参数超范围的记录按不可用处理,不触发无界计算
	if _, err := verifyAdminPassword("$argon2id$v=19$m=999999,t=2,p=1$AAAA$BBBB", sessionTestPassword); err == nil {
		t.Fatal("out-of-range parameters must be rejected")
	}
}