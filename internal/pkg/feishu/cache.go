package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/redis"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	goredis "github.com/redis/go-redis/v9"
)

const (
	sdkTokenCacheKeyPrefix = "feishu:sdk:"
	jsapiTicketCacheKey    = "feishu:jsapi_ticket"
	messageDedupeKeyPrefix = "feishu:message:dedupe:"
)

type jsapiTicketCacheEntry struct {
	Ticket   string `json:"ticket"`
	ExpireIn int32  `json:"expire_in"`
}

type sdkTokenCache struct{}

func newSDKTokenCache() larkcore.Cache {
	return sdkTokenCache{}
}

func (sdkTokenCache) Get(ctx context.Context, key string) (string, error) {
	ctx = redis.WithProjectPrefixOnly(ctx)
	value, err := redis.Client.Get(ctx, sdkTokenCacheKeyPrefix+key).Result()
	if errors.Is(err, goredis.Nil) {
		return "", nil
	}
	return value, err
}

func (sdkTokenCache) Set(ctx context.Context, key string, value string, expireTime time.Duration) error {
	ctx = redis.WithProjectPrefixOnly(ctx)
	return redis.Client.Set(ctx, sdkTokenCacheKeyPrefix+key, value, expireTime).Err()
}

func setCachedSelfBuiltTenantAccessToken(ctx context.Context, appID string, token string, expireIn int) error {
	return newSDKTokenCache().Set(ctx, sdkSelfBuiltTenantAccessTokenKey(appID), token, cacheTTL(expireIn))
}

func sdkSelfBuiltTenantAccessTokenKey(appID string) string {
	return fmt.Sprintf("tenant_access_token-%s-", appID)
}

func GetCachedJSAPITicket(ctx context.Context) (*JSAPITicket, error) {
	ctx = redis.WithProjectPrefixOnly(ctx)
	data, err := redis.Client.Get(ctx, jsapiTicketCacheKey).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var entry jsapiTicketCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, err
	}

	return &JSAPITicket{
		Ticket:   entry.Ticket,
		ExpireIn: entry.ExpireIn,
	}, nil
}

func SetCachedJSAPITicket(ctx context.Context, ticket *JSAPITicket) error {
	ctx = redis.WithProjectPrefixOnly(ctx)
	payload, err := json.Marshal(jsapiTicketCacheEntry{
		Ticket:   ticket.Ticket,
		ExpireIn: ticket.ExpireIn,
	})
	if err != nil {
		return err
	}

	ttl := cacheTTL(int(ticket.ExpireIn))
	return redis.Client.Set(ctx, jsapiTicketCacheKey, payload, ttl).Err()
}

func AcquireMessageDedupe(ctx context.Context, bizKey string) (bool, error) {
	ctx = redis.WithProjectPrefixOnly(ctx)
	return redis.Client.SetNX(ctx, messageDedupeKeyPrefix+bizKey, "1", 10*time.Minute).Result()
}

func cacheTTL(expireIn int) time.Duration {
	ttl := time.Duration(expireIn-120) * time.Second
	if ttl <= 0 {
		return 10 * time.Minute
	}
	return ttl
}
