package model

import (
	"time"

	"github.com/uptrace/bun"
)

type UserAccount struct {
	bun.BaseModel `bun:"table:user.user_account,alias:ua"`

	ID           int64        `bun:"id,pk,autoincrement"`
	FeishuOpenID string       `bun:"feishu_open_id,notnull,unique"`
	Role         MemberRole   `bun:"role,notnull,default:'user'"`
	Status       MemberStatus `bun:"status,notnull,default:'active'"`
	DisplayName  string       `bun:"display_name,notnull"`
	AvatarURL    string       `bun:"avatar_url,notnull,default:''"`
	LastLoginAt  time.Time    `bun:"last_login_at,notnull,default:current_timestamp"`
	CreatedAt    time.Time    `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt    time.Time    `bun:"updated_at,notnull,default:current_timestamp"`
}

type AuthSession struct {
	bun.BaseModel `bun:"table:user.auth_session,alias:as"`

	ID               int64             `bun:"id,pk,autoincrement"`
	UserID           int64             `bun:"user_id,notnull"`
	RefreshTokenHash string            `bun:"refresh_token_hash,notnull,unique"`
	Status           AuthSessionStatus `bun:"status,notnull,default:'active'"`
	ExpiresAt        time.Time         `bun:"expires_at,notnull"`
	CreatedAt        time.Time         `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt        time.Time         `bun:"updated_at,notnull,default:current_timestamp"`
}

type MemberAddress struct {
	bun.BaseModel `bun:"table:user.member_address,alias:ma"`

	ID             int64     `bun:"id,pk,autoincrement"`
	UserID         int64     `bun:"user_id,notnull"`
	RecipientName  string    `bun:"recipient_name,notnull"`
	RecipientPhone string    `bun:"recipient_phone,notnull"`
	Province       string    `bun:"province,notnull,default:''"`
	City           string    `bun:"city,notnull,default:''"`
	District       string    `bun:"district,notnull,default:''"`
	DetailAddress  string    `bun:"detail_address,notnull"`
	IsDefault      bool      `bun:"is_default,notnull,default:false"`
	CreatedAt      time.Time `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt      time.Time `bun:"updated_at,notnull,default:current_timestamp"`
}

type MemberRole string

const (
	MemberRoleUser  MemberRole = "user"
	MemberRoleAdmin MemberRole = "admin"
)

type MemberStatus string

const (
	MemberStatusActive     MemberStatus = "active"
	MemberStatusRestricted MemberStatus = "restricted"
	MemberStatusBanned     MemberStatus = "banned"
	MemberStatusDeleted    MemberStatus = "deleted"
)

type AuthSessionStatus string

const (
	AuthSessionStatusActive  AuthSessionStatus = "active"
	AuthSessionStatusRevoked AuthSessionStatus = "revoked"
	AuthSessionStatusExpired AuthSessionStatus = "expired"
)
