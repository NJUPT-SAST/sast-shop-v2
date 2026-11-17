package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	rpcinterceptor "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
	"github.com/uptrace/bun"
)

type SessionStore struct{ DB bun.IDB }

func NewSessionStore() *SessionStore {
	return &SessionStore{}
}

// 根据会话令牌获取用户信息
func (s *SessionStore) GetSession(ctx context.Context, token string) (*rpcinterceptor.AuthUser, error) {
	if Client == nil {
		return nil, errors.New("session storage is unavailable")
	}
	ctx = WithProjectPrefixOnly(ctx)
	data, err := Client.Get(ctx, constant.SessionTokenPrefix+token).Bytes()
	if err != nil {
		return nil, err
	}
	var user rpcinterceptor.AuthUser
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, err
	}
	return s.resolveSession(ctx, &user)
}

// Development impersonation also resolves the current authoritative identity.
func (s *SessionStore) GetUserByID(ctx context.Context, userID int64) (*rpcinterceptor.AuthUser, error) {
	db := s.DB
	if db == nil {
		if postgres.DB == nil {
			return nil, errors.New("identity storage is unavailable")
		}
		db = postgres.DB
	}
	var user rpcinterceptor.AuthUser
	err := db.NewSelect().TableExpr(`"user".user_account`).
		ColumnExpr("id AS user_id").Column("role", "status", "auth_version").
		Where("id = ?", userID).Scan(ctx, &user)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *SessionStore) resolveSession(
	ctx context.Context,
	snapshot *rpcinterceptor.AuthUser,
) (*rpcinterceptor.AuthUser, error) {
	if snapshot == nil || snapshot.UserID <= 0 {
		return nil, errors.New("invalid session identity")
	}
	current, err := s.GetUserByID(ctx, snapshot.UserID)
	if err != nil {
		return nil, err
	}
	version := snapshot.AuthVersion
	if version == 0 {
		version = 1
	}
	if current.Status != "active" || current.AuthVersion != version || current.Role != snapshot.Role {
		return nil, errors.New("session identity was revoked")
	}
	current.AccessToken = snapshot.AccessToken
	return current, nil
}

// 保存会话信息到redis
// 键是token
// 保存当前登录会话的用户信息，用于请求认证（拦截器拿token查会话）
func (s *SessionStore) SaveSession(ctx context.Context, token string, user *rpcinterceptor.AuthUser) error {
	//nolint:gosec // AccessToken 存入 Redis 会话，不会打到日志
	data, err := json.Marshal(user)
	if err != nil {
		return err
	}
	ctx = WithProjectPrefixOnly(ctx)
	return Client.Set(ctx, constant.SessionTokenPrefix+token, data, constant.SessionTTL).Err()
}

// 将用户信息写入缓存
// 键是userID
// 缓存用户资料，用于根据用户id快速查询用户信息
func (s *SessionStore) SaveUserCache(ctx context.Context, user *rpcinterceptor.AuthUser) error {
	//nolint:gosec // AccessToken 存入 Redis 用户缓存，不会打到日志
	data, err := json.Marshal(user)
	if err != nil {
		return err
	}
	ctx = WithProjectPrefixOnly(ctx)
	key := fmt.Sprintf("%s%d", constant.UserCachePrefix, user.UserID)
	return Client.Set(ctx, key, data, constant.SessionTTL).Err()
}
