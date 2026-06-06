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

func (s *SessionStore) GetSession(ctx context.Context, token string) (*rpcinterceptor.AuthUser, error) {
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

func (s *SessionStore) GetUserByID(ctx context.Context, userID int64) (*rpcinterceptor.AuthUser, error) {
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

func (s *SessionStore) SaveSession(ctx context.Context, token string, user *rpcinterceptor.AuthUser) error {
	//nolint:gosec // AccessToken is part of session stored in Redis, not logged
	data, err := json.Marshal(user)
	if err != nil {
		return err
	}
	return Client.Set(ctx, constant.SessionTokenPrefix+token, data, constant.SessionTTL).Err()
}
