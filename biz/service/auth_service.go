package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

// 管理员初始化与身份契约。
const (
	AdminUsernameEnv        = "RAINBOW_ADMIN_USERNAME"
	AdminInitialPasswordEnv = "RAINBOW_ADMIN_INITIAL_PASSWORD"
	AdminResetPasswordEnv   = "RAINBOW_ADMIN_RESET_PASSWORD"
	defaultAdminUsername    = "admin"

	AuthSessionMaxPerUser   = 10
	authSessionCleanupBatch = 200
)

// AuthSettings 是会话与 Cookie 的运行参数,由配置注入(测试可替换窄字段)。
type AuthSettings struct {
	CookieName       string
	CookieSecure     bool
	IdleTimeout      time.Duration
	AbsoluteTimeout  time.Duration
	TrustedOrigins   []string
	BasePath         string
	AllowInsecureDev bool
}

// AdminIdentity 是服务端验证后的可信管理员身份;只能由会话校验组件创建。
type AdminIdentity struct {
	UserID   int64
	Username string
	Role     string
}

type adminIdentityContextKey struct{}

// WithAdminIdentity 由会话校验组件调用;handler、中间件与服务入口共用。
func WithAdminIdentity(ctx context.Context, identity AdminIdentity) context.Context {
	return context.WithValue(ctx, adminIdentityContextKey{}, identity)
}

// AdminIdentityFrom 读取可信身份;缺失返回 false(调用方必须拒绝,不能回退自报身份)。
func AdminIdentityFrom(ctx context.Context) (AdminIdentity, bool) {
	identity, ok := ctx.Value(adminIdentityContextKey{}).(AdminIdentity)
	if !ok || identity.Username == "" {
		return AdminIdentity{}, false
	}
	return identity, true
}

// requireAdminIdentity 是管理操作唯一身份入口:只接受会话校验组件写入的可信身份。
// 请求自报头、旧 JWT 上下文与旧管理 Bearer 都不能生成它;以后替换为外部 IAM 时只换身份来源。
func requireAdminIdentity(ctx context.Context) (AdminIdentity, error) {
	identity, ok := AdminIdentityFrom(ctx)
	if !ok {
		return AdminIdentity{}, authRequiredError()
	}
	return identity, nil
}

// EnsureAuthBootstrap 在启动时初始化管理员账号:
// 账号表为空时必须提供合法首次密码,否则失败退出;已有账号则不因环境变量变化覆盖。
func (s *Service) EnsureAuthBootstrap(ctx context.Context) error {
	count, err := s.logic.authDAO.CountUsers(ctx, s.logic.db)
	if err != nil {
		return fmt.Errorf("auth bootstrap: count users: %w", err)
	}
	if count > 0 {
		return nil
	}
	username := strings.TrimSpace(os.Getenv(AdminUsernameEnv))
	if username == "" {
		username = defaultAdminUsername
	}
	if !IsValidAuthUsername(username) {
		return fmt.Errorf("auth bootstrap: %s is not a valid username (3-32 of [a-zA-Z0-9_-])", AdminUsernameEnv)
	}
	password := os.Getenv(AdminInitialPasswordEnv)
	if password == "" {
		return fmt.Errorf("auth bootstrap: %s is required when no admin account exists", AdminInitialPasswordEnv)
	}
	if !IsValidAuthPassword(password) {
		return fmt.Errorf("auth bootstrap: %s must be 5-128 characters and at most 512 bytes", AdminInitialPasswordEnv)
	}
	hash, err := hashAdminPassword(password)
	if err != nil {
		return fmt.Errorf("auth bootstrap: hash password: %w", err)
	}
	user := &model.AuthUser{Username: username, PasswordHash: hash, Role: model.AuthRoleAdmin}
	created, isNew, err := s.logic.authDAO.CreateUserIfEmpty(ctx, s.logic.db, user)
	if err != nil {
		return fmt.Errorf("auth bootstrap: create admin: %w", err)
	}
	if created == nil {
		return errors.New("auth bootstrap: admin account missing after create")
	}
	if isNew {
		log.Printf("auth bootstrap: admin account %q created", created.Username)
	} else {
		// 并发启动:另一个进程已创建,保留其密码
		log.Printf("auth bootstrap: admin account %q already exists", created.Username)
	}
	return nil
}

// ResetAdminPassword 是恢复命令用的路径:只重置既有管理员,递增凭据版本并撤销全部会话。
func (s *Service) ResetAdminPassword(ctx context.Context, newPassword string) (string, error) {
	if !IsValidAuthPassword(newPassword) {
		return "", errAuthPasswordInvalid
	}
	count, err := s.logic.authDAO.CountUsers(ctx, s.logic.db)
	if err != nil {
		return "", err
	}
	if count == 0 {
		return "", errors.New("no admin account exists; start the service once with a valid initial password instead")
	}
	user, err := s.logic.authDAO.GetUserByUsername(ctx, s.logic.db, s.adminUsernameForReset(ctx))
	if err != nil {
		return "", err
	}
	hash, err := hashAdminPassword(newPassword)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	err = s.logic.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updated, err := s.logic.authDAO.UpdatePasswordIfVersion(ctx, tx, user.ID, user.CredentialVersion, hash, now)
		if err != nil {
			return err
		}
		if !updated {
			return errors.New("credential version changed during reset; retry")
		}
		if _, err := s.logic.authDAO.RevokeSessionsByUser(ctx, tx, user.ID, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return user.Username, nil
}

// adminUsernameForReset 解析恢复目标账号:默认取唯一账号,允许用环境变量指定。
func (s *Service) adminUsernameForReset(ctx context.Context) string {
	username := strings.TrimSpace(os.Getenv(AdminUsernameEnv))
	if username != "" {
		return username
	}
	var user model.AuthUser
	if err := s.logic.db.WithContext(ctx).Order("id ASC").First(&user).Error; err == nil {
		return user.Username
	}
	return defaultAdminUsername
}

// adminAccountAvailable 表示是否存在可用管理员账号(不读取旧管理密钥)。
func (s *Service) adminAccountAvailable(ctx context.Context) bool {
	count, err := s.logic.authDAO.CountUsers(ctx, s.logic.db)
	return err == nil && count > 0
}

// sessionTokenHash 计算会话凭证摘要:数据库只保存摘要。
func sessionTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

// authUnavailableError 把数据库不可用表达为 503,不降级匿名。
func authUnavailableError(err error) *AuthError {
	if err == nil {
		return newAuthError(503, ErrTypeAuthUnavailable, "认证服务暂不可用")
	}
	return newAuthError(503, ErrTypeAuthUnavailable, "认证服务暂不可用")
}

// authDBUnavailable 判断错误是否属于数据库不可用范畴(连接失败、上下文取消等)。
func authDBUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	if errors.Is(err, db.ErrAuthUserNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"database is closed", "connection refused", "no such host", "unable to open database", "database is locked", "sql: database"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
