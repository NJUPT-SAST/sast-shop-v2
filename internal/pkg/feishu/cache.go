package feishu

import (
	"context"
	"time"

	pkgredis "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/redis"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	goredis "github.com/redis/go-redis/v9"
)

// RedisCache 实现飞书 SDK 的 larkcore.Cache 接口，
// 将 SDK 自动管理的 app/tenant token 缓存写入项目 Redis。
// 只加项目级前缀（不含服务名），保证多个服务共享同一份飞书 token 缓存。
type RedisCache struct{}

// 编译期断言：RedisCache 必须满足 larkcore.Cache 接口。
var _ larkcore.Cache = RedisCache{}

// Set 将 SDK 的缓存项写入 Redis；expireTime 为 0 时表示永不过期。
func (RedisCache) Set(ctx context.Context, key, value string, expireTime time.Duration) error {
	ctx = pkgredis.WithProjectPrefixOnly(ctx)
	return pkgredis.Client.Set(ctx, key, value, expireTime).Err()
}

// Get 从 Redis 读取缓存项；未命中时按 SDK 约定返回空串而非 error，
// 这样 SDK 会判定缓存缺失并自动重新获取 token。
func (RedisCache) Get(ctx context.Context, key string) (string, error) {
	ctx = pkgredis.WithProjectPrefixOnly(ctx)
	value, err := pkgredis.Client.Get(ctx, key).Result()
	if err == goredis.Nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}
