package db

import (
	"context"
	"errors"
	"time"

	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AuthDAO 读写管理员账号与数据库会话;事务、唯一约束与条件更新都集中在这里。
type AuthDAO struct{}

func NewAuthDAO() *AuthDAO { return &AuthDAO{} }

var ErrAuthUserNotFound = errors.New("auth user not found")

// --------------------- 账号 ---------------------

// CountUsers 返回账号数量(初始化判定与 admin_configured 使用)。
func (dao *AuthDAO) CountUsers(ctx context.Context, db *gorm.DB) (int64, error) {
	var count int64
	err := db.WithContext(ctx).Model(&model.AuthUser{}).Count(&count).Error
	return count, err
}

// CreateUserIfEmpty 只在没有账号时创建第一个管理员。
// 用单条 INSERT ... ON CONFLICT DO NOTHING 让唯一约束决定胜者:
// 并发进程不会因“先读后写”的事务升级而拿到 SQLITE_BUSY,冲突方读取既有行且不覆盖密码。
func (dao *AuthDAO) CreateUserIfEmpty(ctx context.Context, db *gorm.DB, user *model.AuthUser) (*model.AuthUser, bool, error) {
	candidate := *user
	result := db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&candidate)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		return &candidate, true, nil
	}
	// 已有账号(可能是另一个进程刚创建):读取既有行,不改动它
	existing, err := dao.GetUserByUsername(ctx, db, user.Username)
	if err != nil {
		return nil, false, err
	}
	return existing, false, nil
}

// GetUserByUsername 精确匹配用户名(大小写敏感,不做隐式转换)。
func (dao *AuthDAO) GetUserByUsername(ctx context.Context, db *gorm.DB, username string) (*model.AuthUser, error) {
	var user model.AuthUser
	err := db.WithContext(ctx).Where("username = ?", username).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAuthUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// GetUserByID 读取账号;不存在返回 ErrAuthUserNotFound。
func (dao *AuthDAO) GetUserByID(ctx context.Context, db *gorm.DB, id int64) (*model.AuthUser, error) {
	var user model.AuthUser
	err := db.WithContext(ctx).Where("id = ?", id).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAuthUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// UpdatePasswordIfVersion 以 credential_version 为条件更新密码并递增版本。
// 返回 false 表示版本已变化(并发改密),由调用方返回冲突错误。
func (dao *AuthDAO) UpdatePasswordIfVersion(ctx context.Context, db *gorm.DB, userID, expectedVersion int64, passwordHash string, now time.Time) (bool, error) {
	result := db.WithContext(ctx).
		Model(&model.AuthUser{}).
		Where("id = ? AND credential_version = ?", userID, expectedVersion).
		Updates(map[string]any{
			"password_hash":      passwordHash,
			"credential_version": expectedVersion + 1,
			"updated_at":         now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// --------------------- 会话 ---------------------

// CreateSession 写入会话行。
func (dao *AuthDAO) CreateSession(ctx context.Context, db *gorm.DB, session *model.AuthSession) error {
	return db.WithContext(ctx).Create(session).Error
}

// CreateSessionIfCredentialVersion 在事务内条件插入会话:
// 只有账号仍然可用、角色正确且 credential_version 未变化时才写入,返回是否成功。
// 该语句与改密的条件更新互斥,消除“检查版本后再插入”之间的空窗。
func (dao *AuthDAO) CreateSessionIfCredentialVersion(ctx context.Context, db *gorm.DB, session *model.AuthSession, role string) (bool, error) {
	result := db.WithContext(ctx).Exec(
		`INSERT INTO auth_sessions (token_hash, user_id, credential_version, csrf_token, created_at, last_seen_at, expires_at, idle_expires_at)
SELECT ?, id, credential_version, ?, ?, ?, ?, ?
FROM auth_users WHERE id = ? AND credential_version = ? AND disabled = ? AND role = ?`,
		session.TokenHash, session.CSRFToken, session.CreatedAt, session.LastSeenAt,
		session.ExpiresAt, session.IdleExpiresAt, session.UserID, session.CredentialVersion, false, role)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// GetSessionByTokenHash 按摘要读取会话;不存在返回 nil。
func (dao *AuthDAO) GetSessionByTokenHash(ctx context.Context, db *gorm.DB, tokenHash string) (*model.AuthSession, error) {
	var session model.AuthSession
	err := db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&session).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &session, nil
}

// TouchSession 原子续期:只有仍然有效(未撤销、未到期、凭据版本一致)的会话才更新空闲期限。
// 撤销与过期状态不会被续期复活。
func (dao *AuthDAO) TouchSession(ctx context.Context, db *gorm.DB, sessionID, credentialVersion int64, idleExpiresAt time.Time, now time.Time) (bool, error) {
	result := db.WithContext(ctx).
		Model(&model.AuthSession{}).
		Where("id = ? AND revoked_at IS NULL AND credential_version = ? AND expires_at > ? AND idle_expires_at > ?",
			sessionID, credentialVersion, now, now).
		Updates(map[string]any{
			"last_seen_at":    now,
			"idle_expires_at": idleExpiresAt,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// RevokeSession 撤销单个会话;返回是否真的从有效变为撤销。
func (dao *AuthDAO) RevokeSession(ctx context.Context, db *gorm.DB, tokenHash string, now time.Time) (bool, error) {
	result := db.WithContext(ctx).
		Model(&model.AuthSession{}).
		Where("token_hash = ? AND revoked_at IS NULL", tokenHash).
		Update("revoked_at", now)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// RevokeSessionsByUser 撤销账号的全部有效会话(改密、停用与恢复命令共用)。
func (dao *AuthDAO) RevokeSessionsByUser(ctx context.Context, db *gorm.DB, userID int64, now time.Time) (int64, error) {
	result := db.WithContext(ctx).
		Model(&model.AuthSession{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", now)
	return result.RowsAffected, result.Error
}

// ListActiveSessionsByUser 按创建时间升序返回账号的有效会话(view 内部使用)。
func (dao *AuthDAO) ListActiveSessionsByUser(ctx context.Context, db *gorm.DB, userID int64, now time.Time) ([]model.AuthSession, error) {
	var sessions []model.AuthSession
	err := db.WithContext(ctx).
		Where("user_id = ? AND revoked_at IS NULL AND expires_at > ? AND idle_expires_at > ?", userID, now, now).
		Order("created_at ASC, id ASC").
		Find(&sessions).Error
	if err != nil {
		return nil, err
	}
	return sessions, nil
}

// RevokeSessionsByIDs 批量撤销指定会话(超出上限时只撤销最旧的若干条)。
func (dao *AuthDAO) RevokeSessionsByIDs(ctx context.Context, db *gorm.DB, ids []int64, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	return db.WithContext(ctx).
		Model(&model.AuthSession{}).
		Where("id IN ? AND revoked_at IS NULL", ids).
		Update("revoked_at", now).Error
}

// DeleteSessionsBefore 按有界批次清理过期或已撤销的历史行;不启动后台协程。
func (dao *AuthDAO) DeleteSessionsBefore(ctx context.Context, db *gorm.DB, now time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	var stale []model.AuthSession
	err := db.WithContext(ctx).
		Where("expires_at < ? OR idle_expires_at < ? OR revoked_at IS NOT NULL", now, now).
		Order("id ASC").
		Limit(limit).
		Find(&stale).Error
	if err != nil {
		return 0, err
	}
	if len(stale) == 0 {
		return 0, nil
	}
	ids := make([]int64, 0, len(stale))
	for _, session := range stale {
		ids = append(ids, session.ID)
	}
	result := db.WithContext(ctx).Where("id IN ?", ids).Delete(&model.AuthSession{})
	return result.RowsAffected, result.Error
}