package model

import "time"

// 管理员角色:本版只有 admin 一种。
const (
	AuthRoleAdmin = "admin"
)

// AuthUser 是管理员账号;第一版只初始化一行。
// 密码只保存 PHC 编码后的哈希(算法、参数、盐、摘要),从不保存明文。
type AuthUser struct {
	ID                int64     `gorm:"primaryKey" json:"id"`
	Username          string    `gorm:"uniqueIndex:uk_auth_user_username;size:32;not null" json:"username"`
	PasswordHash      string    `gorm:"size:255;not null" json:"-"`
	Role              string    `gorm:"size:16;not null;default:admin" json:"role"`
	Disabled          bool      `gorm:"not null;default:false" json:"disabled"`
	CredentialVersion int64     `gorm:"not null;default:1" json:"credential_version"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func (AuthUser) TableName() string { return "auth_users" }

// AuthSession 是数据库会话:只保存会话凭证摘要与 CSRF 值,凭证本身只在 Cookie 与请求内存中。
type AuthSession struct {
	ID                int64      `gorm:"primaryKey" json:"id"`
	TokenHash         string     `gorm:"uniqueIndex:uk_auth_session_token;size:64;not null" json:"-"`
	UserID            int64      `gorm:"index:idx_auth_session_user;not null" json:"user_id"`
	CredentialVersion int64      `gorm:"not null" json:"credential_version"`
	CSRFToken         string     `gorm:"size:64;not null" json:"-"`
	CreatedAt         time.Time  `json:"created_at"`
	LastSeenAt        time.Time  `json:"last_seen_at"`
	ExpiresAt         time.Time  `gorm:"index:idx_auth_session_expires" json:"expires_at"`
	IdleExpiresAt     time.Time  `gorm:"index:idx_auth_session_idle" json:"idle_expires_at"`
	RevokedAt         *time.Time `gorm:"index:idx_auth_session_revoked" json:"revoked_at"`
}

func (AuthSession) TableName() string { return "auth_sessions" }