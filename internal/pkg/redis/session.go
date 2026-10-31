package redis

import (
	"context"
	"encoding/json"
	"fmt"

	rpcinterceptor "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
)

type SessionStore struct{}

func NewSessionStore() *SessionStore {
	return &SessionStore{}
}

// 根据会话令牌获取用户信息
func (s *SessionStore) GetSession(ctx context.Context, token string) (*rpcinterceptor.AuthUser, error) {
	ctx = WithProjectPrefixOnly(ctx)
	data, err := Client.Get(ctx, constant.SessionTokenPrefix+token).Bytes()
	if err != nil {
		return nil, err
	}
	var user rpcinterceptor.AuthUser
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// 根据用户id从缓存中获取用户信息
func (s *SessionStore) GetUserByID(ctx context.Context, userID int64) (*rpcinterceptor.AuthUser, error) {
	ctx = WithProjectPrefixOnly(ctx)
	data, err := Client.Get(ctx, fmt.Sprintf("%s%d", constant.UserCachePrefix, userID)).Bytes()
	if err != nil {
		return nil, err
	}
	var user rpcinterceptor.AuthUser
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, err
	}
	return &user, nil
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
