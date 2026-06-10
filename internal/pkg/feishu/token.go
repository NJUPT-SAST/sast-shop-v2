package feishu

import (
	"context"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
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

// GetAppAccessToken 获取飞书 app_access_token，优先读取 Redis 缓存，
// 未命中时调用 auth/v3/app_access_token/internal 接口获取并写入缓存。
func GetAppAccessToken(ctx context.Context) (string, error) {
	if token, ok, err := getCachedToken(ctx, constant.FeishuAppTokenKey); err != nil {
		return "", err
	} else if ok {
		return token, nil
	}

	var data struct {
		AppAccessToken string `json:"app_access_token"`
		Expire         int    `json:"expire"`
	}
	err := DefaultClient.postJSON(ctx,
		baseURL+"/auth/v3/app_access_token/internal",
		nil,
		map[string]string{
			"app_id":     config.AppConfig.Feishu_AppID,
			"app_secret": config.AppConfig.Feishu_AppSecret,
		},
		&data,
	)
	if err != nil {
		return "", err
	}

	if err := setCachedToken(ctx, constant.FeishuAppTokenKey, data.AppAccessToken, data.Expire); err != nil {
		return "", err
	}
	return data.AppAccessToken, nil
}

// GetTenantAccessToken 获取飞书 tenant_access_token，优先读取 Redis 缓存，
// 未命中时调用 auth/v3/tenant_access_token/internal 接口获取并写入缓存。
func GetTenantAccessToken(ctx context.Context) (string, error) {
	if token, ok, err := getCachedToken(ctx, constant.FeishuTenantTokenKey); err != nil {
		return "", err
	} else if ok {
		return token, nil
	}

	var data struct {
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	err := DefaultClient.postJSON(ctx,
		baseURL+"/auth/v3/tenant_access_token/internal",
		nil,
		map[string]string{
			"app_id":     config.AppConfig.Feishu_AppID,
			"app_secret": config.AppConfig.Feishu_AppSecret,
		},
		&data,
	)
	if err != nil {
		return "", err
	}

	if err := setCachedToken(ctx, constant.FeishuTenantTokenKey, data.TenantAccessToken, data.Expire); err != nil {
		return "", err
	}
	return data.TenantAccessToken, nil
}
