package feishu

import (
	"context"

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
