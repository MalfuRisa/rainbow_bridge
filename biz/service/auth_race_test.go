package service

import (
	"bytes"
	"context"
	stdlog "log"

	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 复核补测:登录与改密竞争、双进程初始化、跨进程撤销,以及日志与错误脱敏。
const (
	raceTestUser     = "admin"
	raceTestPassword = "race test passphrase value"
	raceTestNextPass = "race test next passphrase"
	authHelperEnvKey = "RB_AUTH_HELPER_MODE"
)

// openAuthTestDB 打开独立 SQLite;maxConns 决定是否真的存在并发窗口。
func openAuthTestDB(t *testing.T, path string, maxConns int, logWriter *bytes.Buffer) *gorm.DB {
	t.Helper()
	options := "?_pragma=busy_timeout(10000)"
	config := &gorm.Config{}
	if logWriter != nil {
		config.Logger = logger.New(stdlog.New(logWriter, "", 0), logger.Config{
			SlowThreshold:             0,
			LogLevel:                  logger.Info,
			IgnoreRecordNotFoundError: false,
			ParameterizedQueries:      true,
		})
	} else {
		config.Logger = logger.Default.LogMode(logger.Silent)
	}
	conn, err := gorm.Open(sqlite.Open(path+options), config)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	sqlDB.SetMaxOpenConns(maxConns)
	sqlDB.SetMaxIdleConns(maxConns)
	if err := conn.AutoMigrate(&model.AuthUser{}, &model.AuthSession{}); err != nil {
		t.Fatal(err)
	}
	return conn
}

// newRaceService 用给定连接与可控时钟组装服务。
func newRaceService(t *testing.T, conn *gorm.DB, clock func() time.Time) *Service {
	t.Helper()
	svc := NewService(conn, nil, "", nil)
	svc.SetAuthSettings(AuthSettings{
		CookieName:      "rb_admin_auth_dev",
		CookieSecure:    false,
		IdleTimeout:     time.Hour,
		AbsoluteTimeout: 4 * time.Hour,
		TrustedOrigins:  []string{"http://127.0.0.1:18081"},
	})
	svc.SetAuthClockForTest(clock)
	return svc
}

func raceLoginInput(password string) AuthLoginInput {
	return raceLoginInputFrom(password, "127.0.0.1")
}

// raceLoginInputFrom 用指定来源构造登录输入:竞态用例逐轮换来源,避免触发同 IP 登录限流。
func raceLoginInputFrom(password, clientIP string) AuthLoginInput {
	return AuthLoginInput{
		Username: raceTestUser, Password: password, ClientIP: clientIP,
		Origin: "http://127.0.0.1:18081", HasOrigin: true,
	}
}

// TestAuthLoginVersusPasswordChangeRace 复核项一:登录与改密并发时,
// 不允许出现“登录成功但会话立即无效”,也不允许两个请求都成功。
func TestAuthLoginVersusPasswordChangeRace(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "race.db")
	conn := openAuthTestDB(t, dbPath, 2, nil)
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	svc := newRaceService(t, conn, func() time.Time { return now })
	t.Setenv(AdminInitialPasswordEnv, raceTestPassword)
	t.Setenv(AdminUsernameEnv, raceTestUser)
	if err := svc.EnsureAuthBootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	currentPassword := raceTestPassword
	for round := 0; round < 8; round++ {
		clientIP := fmt.Sprintf("127.0.0.%d", round+1)
		// 每轮准备一个有效会话用于发起改密
		starter, err := svc.Login(context.Background(), raceLoginInputFrom(currentPassword, clientIP))
		if err != nil {
			t.Fatalf("round %d starter login: %v", round, err)
		}
		nextPassword := fmt.Sprintf("%s round %d", raceTestNextPass, round)

		var start sync.WaitGroup
		start.Add(1)
		type outcome struct {
			kind  string
			err   error
			token string
		}
		results := make(chan outcome, 2)
		go func() {
			start.Wait()
			result, err := svc.Login(context.Background(), raceLoginInputFrom(currentPassword, clientIP))
			token := ""
			if result != nil {
				token = result.Token
			}
			results <- outcome{kind: "login", err: err, token: token}
		}()
		go func() {
			start.Wait()
			err := svc.ChangePassword(context.Background(), AuthPasswordChangeInput{
				AuthRequestInput: AuthRequestInput{SessionToken: starter.Token, CSRFToken: starter.CSRFToken},
				CurrentPassword:  currentPassword,
				NewPassword:      nextPassword,
			})
			results <- outcome{kind: "change", err: err}
		}()
		start.Done()

		collected := map[string]outcome{}
		for i := 0; i < 2; i++ {
			result := <-results
			collected[result.kind] = result
		}
		changeErr := collected["change"].err
		loginErr := collected["login"].err
		if changeErr != nil {
			t.Fatalf("round %d: password change must win its own session: %v", round, changeErr)
		}
		if loginErr == nil {
			// 登录成功只允许两种情形:会话仍有效(改密尚未提交),或已被随后的改密撤销。
			// 不允许出现“未撤销但凭据版本已过期”的会话行——那正是原子保护要消除的半状态。
			user, err := svc.logic.authDAO.GetUserByUsername(context.Background(), conn, raceTestUser)
			if err != nil {
				t.Fatal(err)
			}
			row, err := svc.logic.authDAO.GetSessionByTokenHash(context.Background(), conn, sessionTokenHash(collected["login"].token))
			if err != nil {
				t.Fatal(err)
			}
			if row == nil {
				t.Fatalf("round %d: successful login must have a session row", round)
			}
			revoked := row.RevokedAt != nil
			versionMatches := row.CredentialVersion == user.CredentialVersion
			if !revoked && !versionMatches {
				t.Fatalf("round %d: session must not be left unrevoked with a superseded credential version (row=%d user=%d)",
					round, row.CredentialVersion, user.CredentialVersion)
			}
		} else {
			authErr, ok := AsAuthError(loginErr)
			if !ok {
				t.Fatalf("round %d: unexpected login error: %v", round, loginErr)
			}
			switch authErr.ErrorType {
			case ErrTypeAuthCredentialChanged, ErrTypeAuthForbidden, ErrTypeAuthUnavailable, ErrTypeAuthInvalidCredentials, ErrTypeAuthRateLimited:
			default:
				t.Fatalf("round %d: unexpected login error type %s", round, authErr.ErrorType)
			}
		}
		currentPassword = nextPassword
	}
}

// TestAuthLoginSessionStaysValidWhenPasswordChangesAfterCommit:改密发生在会话提交之后时,
// 登录返回的会话由改密负责撤销,调用方拿到的是明确的失败或有效会话,不存在半状态。
func TestAuthLoginSessionStaysValidWhenPasswordChangesAfterCommit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "after.db")
	conn := openAuthTestDB(t, dbPath, 1, nil)
	svc := newRaceService(t, conn, func() time.Time { return time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC) })
	t.Setenv(AdminInitialPasswordEnv, raceTestPassword)
	t.Setenv(AdminUsernameEnv, raceTestUser)
	if err := svc.EnsureAuthBootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := svc.Login(context.Background(), raceLoginInput(raceTestPassword))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CurrentSession(context.Background(), AuthRequestInput{SessionToken: session.Token}); err != nil {
		t.Fatalf("fresh session must be valid: %v", err)
	}
}

// TestAuthLoginCommitIsAtomic 复核项一(确定性时序):密码校验通过后、提交会话之前发生改密,
// 登录必须明确失败(409/403/503),绝不能返回一个带过期凭据版本的会话。
func TestAuthLoginCommitIsAtomic(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "atomic.db")
	conn := openAuthTestDB(t, dbPath, 2, nil)
	svc := newRaceService(t, conn, func() time.Time { return time.Now().UTC() })
	t.Setenv(AdminInitialPasswordEnv, raceTestPassword)
	t.Setenv(AdminUsernameEnv, raceTestUser)
	if err := svc.EnsureAuthBootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}

	reached := make(chan struct{}, 1)
	resume := make(chan struct{})
	// 只暂停第一个登录:后续登录立即通过(不能用 sync.Once,它会让并发调用一起等待)
	var paused atomic.Bool
	authLoginPauseHookForTest = func() {
		if paused.CompareAndSwap(false, true) {
			reached <- struct{}{}
			<-resume
		}
	}
	t.Cleanup(func() { authLoginPauseHookForTest = nil })

	type loginOutcome struct {
		result *AuthLoginResult
		err    error
	}
	loginDone := make(chan loginOutcome, 1)
	go func() {
		result, err := svc.Login(context.Background(), raceLoginInputFrom(raceTestPassword, "127.0.0.9"))
		loginDone <- loginOutcome{result: result, err: err}
	}()
	select {
	case <-reached:
	case outcome := <-loginDone:
		t.Fatalf("login finished before the pause hook: %v", outcome.err)
	case <-time.After(20 * time.Second):
		t.Fatal("login never reached the pause point")
	}

	// 在窗口内完成改密(用一个有效会话发起)
	starter, err := svc.Login(context.Background(), raceLoginInputFrom(raceTestPassword, "127.0.0.10"))
	if err != nil {
		t.Fatalf("starter login: %v", err)
	}
	if err := svc.ChangePassword(context.Background(), AuthPasswordChangeInput{
		AuthRequestInput: AuthRequestInput{SessionToken: starter.Token, CSRFToken: starter.CSRFToken},
		CurrentPassword:  raceTestPassword,
		NewPassword:      raceTestNextPass,
	}); err != nil {
		t.Fatalf("password change inside the window: %v", err)
	}
	close(resume)

	outcome := <-loginDone
	if outcome.err == nil {
		// 不允许:会话行携带已被取代的凭据版本
		row, lookupErr := svc.logic.authDAO.GetSessionByTokenHash(context.Background(), conn, sessionTokenHash(outcome.result.Token))
		if lookupErr != nil {
			t.Fatal(lookupErr)
		}
		user, userErr := svc.logic.authDAO.GetUserByUsername(context.Background(), conn, raceTestUser)
		if userErr != nil {
			t.Fatal(userErr)
		}
		t.Fatalf("login must fail when the credential version changed during commit (sessionVersion=%v userVersion=%d, revoked=%v)",
			row, user.CredentialVersion, row != nil && row.RevokedAt != nil)
	}
	authErr, ok := AsAuthError(outcome.err)
	if !ok {
		t.Fatalf("unexpected login error: %v", outcome.err)
	}
	switch authErr.ErrorType {
	case ErrTypeAuthCredentialChanged, ErrTypeAuthForbidden, ErrTypeAuthUnavailable:
	default:
		t.Fatalf("unexpected login error type %s", authErr.ErrorType)
	}
}

// TestAuthBootstrapTwoProcesses A02:两个进程同时初始化同一 SQLite,只产生一个管理员且不覆盖密码。
func TestAuthBootstrapTwoProcesses(t *testing.T) {
	if os.Getenv(authHelperEnvKey) == "bootstrap" {
		runBootstrapHelper(t)
		return
	}
	dbPath := filepath.Join(t.TempDir(), "two-process.db")
	conn := openAuthTestDB(t, dbPath, 1, nil)
	sqlDB, _ := conn.DB()
	_ = sqlDB.Close()

	type child struct {
		cmd      *exec.Cmd
		password string
		output   *bytes.Buffer
	}
	children := make([]*child, 0, 2)
	for index, password := range []string{"first process passphrase", "second process passphrase"} {
		output := &bytes.Buffer{}
		cmd := exec.Command(os.Args[0], "-test.run=TestAuthBootstrapTwoProcesses", "-test.v")
		cmd.Env = append(os.Environ(),
			authHelperEnvKey+"=bootstrap",
			"RB_AUTH_TEST_DB="+dbPath,
			"RB_AUTH_TEST_PASSWORD="+password,
			fmt.Sprintf("RB_AUTH_TEST_INDEX=%d", index),
		)
		cmd.Stdout = output
		cmd.Stderr = output
		if err := cmd.Start(); err != nil {
			t.Fatalf("start child %d: %v", index, err)
		}
		children = append(children, &child{cmd: cmd, password: password, output: output})
	}
	for _, item := range children {
		if err := item.cmd.Wait(); err != nil {
			t.Fatalf("child failed: %v\n%s", err, item.output.String())
		}
	}
	// 两个进程都成功(一个创建、一个读到既有账号),但只有一个账号且密码是其中之一的胜者
	verify := openAuthTestDB(t, dbPath, 1, nil)
	reopened := newRaceService(t, verify, func() time.Time { return time.Now().UTC() })
	count, err := reopened.logic.authDAO.CountUsers(context.Background(), verify)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("two-process bootstrap must leave exactly one account, got %d", count)
	}
	user, err := reopened.logic.authDAO.GetUserByUsername(context.Background(), verify, raceTestUser)
	if err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, item := range children {
		ok, err := verifyAdminPassword(user.PasswordHash, item.password)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			matched++
		}
	}
	if matched != 1 {
		t.Fatalf("exactly one child password must remain valid, matched=%d", matched)
	}
}

// runBootstrapHelper 是子进程侧:用独立连接对同一库执行初始化。
func runBootstrapHelper(t *testing.T) {
	dbPath := os.Getenv("RB_AUTH_TEST_DB")
	password := os.Getenv("RB_AUTH_TEST_PASSWORD")
	if dbPath == "" || password == "" {
		t.Fatalf("helper requires %s and password env", dbPath)
	}
	conn, err := gorm.Open(sqlite.Open(dbPath+"?_pragma=busy_timeout(10000)"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("helper open db: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	if err := conn.AutoMigrate(&model.AuthUser{}, &model.AuthSession{}); err != nil {
		t.Fatalf("helper migrate: %v", err)
	}
	svc := NewService(conn, nil, "", nil)
	os.Setenv(AdminInitialPasswordEnv, password)
	os.Setenv(AdminUsernameEnv, raceTestUser)
	if err := svc.EnsureAuthBootstrap(context.Background()); err != nil {
		t.Fatalf("helper bootstrap: %v", err)
	}
}

// TestAuthCrossProcessRevocation A10:别的进程撤销会话后,本进程的下一次请求立即被拒绝。
func TestAuthCrossProcessRevocation(t *testing.T) {
	if os.Getenv(authHelperEnvKey) == "logout" {
		runLogoutHelper(t)
		return
	}
	dbPath := filepath.Join(t.TempDir(), "cross-process.db")
	conn := openAuthTestDB(t, dbPath, 1, nil)
	svc := newRaceService(t, conn, func() time.Time { return time.Now().UTC() })
	t.Setenv(AdminInitialPasswordEnv, raceTestPassword)
	t.Setenv(AdminUsernameEnv, raceTestUser)
	if err := svc.EnsureAuthBootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := svc.Login(context.Background(), raceLoginInput(raceTestPassword))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CurrentSession(context.Background(), AuthRequestInput{SessionToken: session.Token}); err != nil {
		t.Fatalf("session must start valid: %v", err)
	}

	output := &bytes.Buffer{}
	cmd := exec.Command(os.Args[0], "-test.run=TestAuthCrossProcessRevocation", "-test.v")
	cmd.Env = append(os.Environ(),
		authHelperEnvKey+"=logout",
		"RB_AUTH_TEST_DB="+dbPath,
		"RB_AUTH_TEST_TOKEN="+session.Token,
	)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		t.Fatalf("logout helper failed: %v\n%s", err, output.String())
	}
	// 本进程没有缓存:同一会话立即失效
	if _, err := svc.CurrentSession(context.Background(), AuthRequestInput{SessionToken: session.Token}); err == nil {
		t.Fatal("session revoked by another process must be rejected without waiting for a cache to expire")
	} else if authErr, ok := AsAuthError(err); !ok || authErr.ErrorType != ErrTypeAuthRequired {
		t.Fatalf("cross-process revocation error = %v", err)
	}
}

// runLogoutHelper 是子进程侧:用独立连接撤销给定会话。
func runLogoutHelper(t *testing.T) {
	dbPath := os.Getenv("RB_AUTH_TEST_DB")
	token := os.Getenv("RB_AUTH_TEST_TOKEN")
	if dbPath == "" || token == "" {
		t.Fatal("logout helper requires database and token env")
	}
	conn, err := gorm.Open(sqlite.Open(dbPath+"?_pragma=busy_timeout(10000)"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("helper open db: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	dao := db.NewAuthDAO()
	if _, err := dao.RevokeSession(context.Background(), conn, sessionTokenHash(token), time.Now().UTC()); err != nil {
		t.Fatalf("helper revoke: %v", err)
	}
}

// TestAuthLogsAndErrorsDoNotLeakSecrets A20:SQL 日志、错误响应都不含密码、哈希、会话凭证与 CSRF。
func TestAuthLogsAndErrorsDoNotLeakSecrets(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "redaction.db")
	logBuffer := &bytes.Buffer{}
	conn := openAuthTestDB(t, dbPath, 1, logBuffer)
	svc := newRaceService(t, conn, func() time.Time { return time.Now().UTC() })
	t.Setenv(AdminInitialPasswordEnv, raceTestPassword)
	t.Setenv(AdminUsernameEnv, raceTestUser)
	if err := svc.EnsureAuthBootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := svc.Login(context.Background(), raceLoginInput(raceTestPassword))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ChangePassword(context.Background(), AuthPasswordChangeInput{
		AuthRequestInput: AuthRequestInput{SessionToken: session.Token, CSRFToken: session.CSRFToken},
		CurrentPassword:  raceTestPassword,
		NewPassword:      raceTestNextPass,
	}); err != nil {
		t.Fatalf("change password: %v", err)
	}
	logs := logBuffer.String()
	for name, secret := range map[string]string{
		"password":       raceTestPassword,
		"new password":   raceTestNextPass,
		"hash":           "$argon2id$",
		"session token":  session.Token,
		"session digest": sessionTokenHash(session.Token),
		"csrf":           session.CSRFToken,
	} {
		if secret != "" && strings.Contains(logs, secret) {
			t.Fatalf("SQL logs must not contain the %s", name)
		}
	}

	// 内部错误响应只给固定提示,不回显底层细节
	if err := conn.Migrator().DropTable(&model.AuthSession{}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Login(context.Background(), raceLoginInput(raceTestNextPass))
	if err == nil {
		t.Fatal("login must fail once the session table is gone")
	}
	authErr, ok := AsAuthError(err)
	if !ok {
		t.Fatalf("unexpected error type: %v", err)
	}
	message := authErr.Error()
	for _, leak := range []string{"no such table", "auth_sessions", "SELECT", "INSERT"} {
		if strings.Contains(message, leak) {
			t.Fatalf("error message leaks internal detail %q: %s", leak, message)
		}
	}
}