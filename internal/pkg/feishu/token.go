package feishu

import (
	"context"
	"time"

	pkgredis "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/redis"
	goredis "github.com/redis/go-redis/v9"
)

// getCachedToken 从 Redis 读取飞书 token 缓存。
// 返回值依次表示：token 内容、是否命中缓存、读取过程中是否发生错误。
func getCachedToken(ctx context.Context, key string) (string, bool, error) {
	ctx = pkgredis.WithProjectPrefixOnly(ctx)
	token, err := pkgredis.Client.Get(ctx, key).Result()
	if err == goredis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return token, true, nil
}

// setCachedToken 将飞书 token 写入 Redis，TTL 取 expireInSec 减去 60 秒的缓冲，
// 以保证缓存在飞书侧凭证真正过期前失效；若不足 60 秒则按 60 秒兜底。
func setCachedToken(ctx context.Context, key, token string, expireInSec int) error {
	ttl := time.Duration(expireInSec-60) * time.Second
	if ttl < 60*time.Second {
		ttl = 60 * time.Second
	}
	ctx = pkgredis.WithProjectPrefixOnly(ctx)
	return pkgredis.Client.Set(ctx, key, token, ttl).Err()
}
