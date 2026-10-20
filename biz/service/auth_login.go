package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/db"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
)

// authLoginPauseHookForTest 仅测试使用:密码校验通过后、提交会话之前调用,
// 用于确定性地复现“校验通过后恰好改密”的时序。默认 nil,生产不启用。
var authLoginPauseHookForTest func()

// errAuthCredentialSuperseded 表示提交会话时账号凭据版本已变化或账号不可用,
// 由 Login 映射为 409/403,绝不返回一个立即失效的“登录成功”。
var errAuthCredentialSuperseded = errors.New("auth credential superseded during login")

// AuthUserView 是可对外返回的账号信息;不包含密码、哈希或会话数据。
type AuthUserView struct {
	ID       int64
	Username string
	Role     string
}

// AuthSessionView 是会话有效期(服务端 UTC)。
type AuthSessionView struct {
	ExpiresAt     time.Time
	IdleExpiresAt time.Time
}

// AuthLoginResult 是登录成功结果:凭证只交给 Cookie,CSRF 交给前端内存。
type AuthLoginResult struct {
	Token     string
	User      AuthUserView
	Session   AuthSessionView
	CSRFToken string
}

// AuthMeResult 与登录成功的数据结构一致(不含凭证)。
type AuthMeResult struct {
	User      AuthUserView
	Session   AuthSessionView
	CSRFToken string
}

// AuthLoginInput 是登录请求的服务层输入。
type AuthLoginInput struct {
	Username     string
	Password     string
	ClientIP     string
	Origin       string
	HasOrigin    bool
	SessionToken string // 当前浏览器已有会话(可选);成功登录时撤销并替换
}

// AuthRequestInput 是 me/logout/password 的公共输入:会话凭证来自 Cookie,由 handler 传入。
type AuthRequestInput struct {
	SessionToken string
	CSRFToken    string
	Origin       string
	HasOrigin    bool
	ClientIP     string
}

// AuthPasswordChangeInput 是改密输入。
type AuthPasswordChangeInput struct {
	AuthRequestInput
	CurrentPassword string
	NewPassword     string
}

// Login 校验凭据并创建新会话;失败不删除浏览器现有有效会话。
func (s *Service) Login(ctx context.Context, input AuthLoginInput) (*AuthLoginResult, error) {
	username := input.Username
	password := input.Password
	if !IsValidAuthUsername(username) {
		return nil, authInvalidRequestError("账号或密码格式不正确")
	}
	if !IsValidAuthPassword(password) {
		// 长度不合规先拒绝,不启动昂贵计算
		return nil, authInvalidRequestError("账号或密码格式不正确")
	}
	if !input.HasOrigin || !s.OriginAllowed(input.Origin) {
		return nil, authOriginDeniedError()
	}
	if allowed, retryAfter := s.loginLimiter().allow(input.ClientIP); !allowed {
		return nil, authRateLimitedError(retryAfter)
	}
	if !s.passwordLimiter().tryAcquire() {
		return nil, authBusyError()
	}
	defer s.passwordLimiter().release()

	user, err := s.logic.authDAO.GetUserByUsername(ctx, s.logic.db, username)
	if err != nil {
		if !errors.Is(err, db.ErrAuthUserNotFound) {
			if authDBUnavailable(err) {
				return nil, authUnavailableError(err)
			}
			return nil, authInternalError(err)
		}
		// 未知账号也执行固定虚拟校验,避免明显的快速返回差异
		_, _ = verifyAdminPassword(authVirtualHashOnce(), password)
		return nil, authInvalidCredentialsError()
	}
	ok, verifyErr := verifyAdminPassword(user.PasswordHash, password)
	if verifyErr != nil {
		return nil, authInternalError(verifyErr)
	}
	if !ok || user.Disabled || user.Role != model.AuthRoleAdmin {
		// 账号不存在、密码错误、停用账号统一返回同一结果
		return nil, authInvalidCredentialsError()
	}

	if hook := authLoginPauseHookForTest; hook != nil {
		hook()
	}
	now := s.now()
	// 凭据版本不再在事务外比较:提交会话时由条件插入原子保证(见 createSessionWithLimit)

	token, err := newSessionToken()
	if err != nil {
		return nil, authInternalError(err)
	}
	csrf, err := newCSRFToken()
	if err != nil {
		return nil, authInternalError(err)
	}
	settings := s.AuthSettings()
	session := &model.AuthSession{
		TokenHash:         sessionTokenHash(token),
		UserID:            user.ID,
		CredentialVersion: user.CredentialVersion,
		CSRFToken:         csrf,
		CreatedAt:         now,
		LastSeenAt:        now,
		ExpiresAt:         now.Add(settings.AbsoluteTimeout),
		IdleExpiresAt:     now.Add(settings.IdleTimeout),
	}
	previousHash := ""
	if token := strings.TrimSpace(input.SessionToken); token != "" {
		previousHash = sessionTokenHash(token)
	}
	err = s.createSessionWithLimit(ctx, session, previousHash)
	if err != nil {
		if errors.Is(err, errAuthCredentialSuperseded) {
			return nil, s.credentialSupersededError(ctx, user.ID)
		}
		if authDBUnavailable(err) {
			return nil, authUnavailableError(err)
		}
		return nil, authInternalError(err)
	}
	return &AuthLoginResult{
		Token:     token,
		User:      authUserViewFromRow(authUserRowFrom(user)),
		Session:   AuthSessionView{ExpiresAt: session.ExpiresAt, IdleExpiresAt: session.IdleExpiresAt},
		CSRFToken: csrf,
	}, nil
}

// createSessionWithLimit 在事务内条件插入会话(凭据版本必须仍然一致),替换旧会话并保持每账号最多 10 个有效会话。
func (s *Service) createSessionWithLimit(ctx context.Context, session *model.AuthSession, previousTokenHash string) error {
	return s.logic.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if previousTokenHash != "" {
			if _, err := s.logic.authDAO.RevokeSession(ctx, tx, previousTokenHash, session.CreatedAt); err != nil {
				return err
			}
		}
		inserted, err := s.logic.authDAO.CreateSessionIfCredentialVersion(ctx, tx, session, model.AuthRoleAdmin)
		if err != nil {
			return err
		}
		if !inserted {
			// 账号已被停用/降权,或验证期间发生了改密:不产生任何会话行
			return errAuthCredentialSuperseded
		}
		active, err := s.logic.authDAO.ListActiveSessionsByUser(ctx, tx, session.UserID, session.CreatedAt)
		if err != nil {
			return err
		}
		if len(active) <= AuthSessionMaxPerUser {
			return nil
		}
		overflow := make([]int64, 0, len(active)-AuthSessionMaxPerUser)
		for _, item := range active[:len(active)-AuthSessionMaxPerUser] {
			overflow = append(overflow, item.ID)
		}
		return s.logic.authDAO.RevokeSessionsByIDs(ctx, tx, overflow, session.CreatedAt)
	})
}

// CurrentSession 返回当前会话的账号与期限;成功后滑动空闲期限(不轮换凭证)。
func (s *Service) CurrentSession(ctx context.Context, input AuthRequestInput) (*AuthMeResult, error) {
	row, user, err := s.resolveSession(ctx, input.SessionToken)
	if err != nil {
		return nil, err
	}
	settings := s.AuthSettings()
	now := s.now()
	idle := now.Add(settings.IdleTimeout)
	if idle.After(row.ExpiresAt) {
		idle = row.ExpiresAt
	}
	touched, err := s.logic.authDAO.TouchSession(ctx, s.logic.db, row.ID, row.CredentialVersion, idle, now)
	if err != nil {
		if authDBUnavailable(err) {
			return nil, authUnavailableError(err)
		}
		return nil, authInternalError(err)
	}
	if !touched {
		// 续期条件不满足说明会话在这期间已撤销或过期
		return nil, authRequiredError()
	}
	return &AuthMeResult{
		User:      authUserViewFromRow(user),
		Session:   AuthSessionView{ExpiresAt: row.ExpiresAt, IdleExpiresAt: idle},
		CSRFToken: row.CSRFToken,
	}, nil
}

// Logout 撤销当前会话;无会话或已失效时同样返回成功并清除 Cookie。
// 登录与退出都强制合法 Origin(失效会话的重复退出同样要求)。
func (s *Service) Logout(ctx context.Context, input AuthRequestInput) error {
	if !input.HasOrigin || !s.OriginAllowed(input.Origin) {
		return authOriginDeniedError()
	}
	token := strings.TrimSpace(input.SessionToken)
	if token == "" {
		return nil
	}
	row, user, err := s.resolveSession(ctx, token)
	if err != nil {
		// 无有效会话:重复退出按成功处理,不声称做过服务端撤销
		if authErr, ok := AsAuthError(err); ok && authErr.ErrorType == ErrTypeAuthRequired {
			return nil
		}
		return err
	}
	_ = user
	if !csrfMatches(row.CSRFToken, input.CSRFToken) {
		// 校验失败不能先撤销再报错
		return authCSRFError()
	}
	_, err = s.logic.authDAO.RevokeSession(ctx, s.logic.db, row.TokenHash, s.now())
	if err != nil {
		if authDBUnavailable(err) {
			return authUnavailableError(err)
		}
		return authInternalError(err)
	}
	return nil
}

// ChangePassword 校验当前密码后更新密码、递增凭据版本并撤销该账号全部会话。
func (s *Service) ChangePassword(ctx context.Context, input AuthPasswordChangeInput) error {
	row, user, err := s.resolveSession(ctx, input.SessionToken)
	if err != nil {
		return err
	}
	if input.HasOrigin && !s.OriginAllowed(input.Origin) {
		return authOriginDeniedError()
	}
	if !csrfMatches(row.CSRFToken, input.CSRFToken) {
		return authCSRFError()
	}
	if !IsValidAuthPassword(input.NewPassword) {
		return authInvalidRequestError("新密码需 15-128 个字符且不超过 512 字节")
	}
	if input.NewPassword == input.CurrentPassword {
		return authInvalidRequestError("新密码不能与当前密码相同")
	}
	if !IsValidAuthPassword(input.CurrentPassword) {
		return authCurrentPasswordInvalidError()
	}
	if !s.passwordLimiter().tryAcquire() {
		return authBusyError()
	}
	defer s.passwordLimiter().release()
	ok, verifyErr := verifyAdminPassword(user.PasswordHash, input.CurrentPassword)
	if verifyErr != nil {
		return authInternalError(verifyErr)
	}
	if !ok {
		// 验证失败不改动数据库,也不退出其他会话
		return authCurrentPasswordInvalidError()
	}
	hash, err := hashAdminPassword(input.NewPassword)
	if err != nil {
		return authInternalError(err)
	}
	now := s.now()
	err = s.logic.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updated, err := s.logic.authDAO.UpdatePasswordIfVersion(ctx, tx, user.ID, user.CredentialVersion, hash, now)
		if err != nil {
			return err
		}
		if !updated {
			return authCredentialChangedError()
		}
		_, err = s.logic.authDAO.RevokeSessionsByUser(ctx, tx, user.ID, now)
		return err
	})
	if err != nil {
		if _, ok := AsAuthError(err); ok {
			return err
		}
		if authDBUnavailable(err) {
			return authUnavailableError(err)
		}
		return authInternalError(err)
	}
	return nil
}

// credentialSupersededError 区分“账号不可用”(403)与“凭据已变更”(409),不回显内部细节。
func (s *Service) credentialSupersededError(ctx context.Context, userID int64) error {
	latest, err := s.logic.authDAO.GetUserByID(ctx, s.logic.db, userID)
	if err != nil {
		if authDBUnavailable(err) {
			return authUnavailableError(err)
		}
		// 读不到账号:按凭据冲突处理,避免把数据库细节暴露给调用方
		return authCredentialChangedError()
	}
	if latest.Disabled || latest.Role != model.AuthRoleAdmin {
		return authForbiddenError()
	}
	return authCredentialChangedError()
}

// resolveSession 读取并验证会话;返回会话行与账号行。
func (s *Service) resolveSession(ctx context.Context, token string) (authSessionRow, authUserRow, error) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return authSessionRow{}, authUserRow{}, authRequiredError()
	}
	session, err := s.logic.authDAO.GetSessionByTokenHash(ctx, s.logic.db, sessionTokenHash(trimmed))
	if err != nil {
		if authDBUnavailable(err) {
			return authSessionRow{}, authUserRow{}, authUnavailableError(err)
		}
		return authSessionRow{}, authUserRow{}, authInternalError(err)
	}
	if session == nil {
		return authSessionRow{}, authUserRow{}, authRequiredError()
	}
	user, err := s.logic.authDAO.GetUserByID(ctx, s.logic.db, session.UserID)
	if err != nil {
		if errors.Is(err, db.ErrAuthUserNotFound) {
			return authSessionRow{}, authUserRow{}, authRequiredError()
		}
		if authDBUnavailable(err) {
			return authSessionRow{}, authUserRow{}, authUnavailableError(err)
		}
		return authSessionRow{}, authUserRow{}, authInternalError(err)
	}
	row := authSessionRowFrom(session)
	userRow := authUserRowFrom(user)
	if authErr, ok := sessionValid(row, userRow, s.now()); !ok {
		return row, userRow, authErr
	}
	return row, userRow, nil
}

// AdminIdentityForRequest 供管理中间件使用:验证会话并返回可信身份,
// 同时按需要校验 CSRF(写操作)与请求来源。
func (s *Service) AdminIdentityForRequest(ctx context.Context, input AuthRequestInput, requireCSRF bool) (AdminIdentity, error) {
	row, user, err := s.resolveSession(ctx, input.SessionToken)
	if err != nil {
		return AdminIdentity{}, err
	}
	if input.HasOrigin && !s.OriginAllowed(input.Origin) {
		return AdminIdentity{}, authOriginDeniedError()
	}
	if requireCSRF && !csrfMatches(row.CSRFToken, input.CSRFToken) {
		return AdminIdentity{}, authCSRFError()
	}
	// 管理请求同样滑动空闲期限:与 /auth/me 一致,不突破绝对期限
	settings := s.AuthSettings()
	now := s.now()
	idle := now.Add(settings.IdleTimeout)
	if idle.After(row.ExpiresAt) {
		idle = row.ExpiresAt
	}
	touched, err := s.logic.authDAO.TouchSession(ctx, s.logic.db, row.ID, row.CredentialVersion, idle, now)
	if err != nil {
		if authDBUnavailable(err) {
			return AdminIdentity{}, authUnavailableError(err)
		}
		return AdminIdentity{}, authInternalError(err)
	}
	if !touched {
		return AdminIdentity{}, authRequiredError()
	}
	return AdminIdentity{UserID: user.ID, Username: user.Username, Role: user.Role}, nil
}

func authUserViewFromRow(user authUserRow) AuthUserView {
	return AuthUserView{ID: user.ID, Username: user.Username, Role: user.Role}
}

func authUserRowFrom(user *model.AuthUser) authUserRow {
	return authUserRow{
		ID: user.ID, Username: user.Username, Role: user.Role,
		Disabled: user.Disabled, CredentialVersion: user.CredentialVersion, PasswordHash: user.PasswordHash,
	}
}

func authSessionRowFrom(session *model.AuthSession) authSessionRow {
	return authSessionRow{
		ID: session.ID, TokenHash: session.TokenHash, UserID: session.UserID,
		CredentialVersion: session.CredentialVersion, CSRFToken: session.CSRFToken,
		CreatedAt: session.CreatedAt, ExpiresAt: session.ExpiresAt,
		IdleExpiresAt: session.IdleExpiresAt, RevokedAt: session.RevokedAt,
	}
}