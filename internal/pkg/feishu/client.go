package feishu

import (
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	lark "github.com/larksuite/oapi-sdk-go/v3"
)

// Client 是包级共享的飞书 SDK 客户端单例，由 Init 在服务启动期初始化。
// SDK 默认开启 token 自动管理（EnableTokenCache=true），
// app_access_token / tenant_access_token 的获取与缓存全部由 SDK 处理，
// 缓存载体通过 WithTokenCache 接入项目 Redis（见 RedisCache）。
var Client *lark.Client

// Init 初始化飞书 SDK 客户端单例。
// 必须在 config 与 redis 初始化之后调用（依赖配置与 Redis 缓存）。
func Init() {
	cfg := config.AppConfig
	if cfg.Feishu_AppID == "" || cfg.Feishu_AppSecret == "" {
		panic("feishu: FEISHU_APP_ID / FEISHU_APP_SECRET must be configured")
	}

	Client = lark.NewClient(
		cfg.Feishu_AppID,
		cfg.Feishu_AppSecret,
		lark.WithTokenCache(RedisCache{}),
		lark.WithReqTimeout(10*time.Second),
	)
}
