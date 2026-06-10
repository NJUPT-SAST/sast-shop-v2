package constant

import "time"

const (
	ProjectName        = "sast-shop-v2"
	Dev                = "development"
	Test               = "test"
	Prod               = "production"
	AppEnv             = "APP_ENV"
	UserServiceName    = "user-service"
	CatalogServiceName = "catalog-service"
	PaymentServiceName = "payment-service"
	SpotServiceName    = "spot-service"
	ErrandServiceName  = "errand-service"
	XDevUserIDHeader   = "X-Dev-User-ID"
	SessionTokenPrefix = "session:"
	UserCachePrefix    = "user:"
	// TODO: set session TTL before feishu access token expiration time
	SessionTTL          = 30 * time.Minute
	UnknownErrorMessage = "An unknown error occurred. Please try again later."

	// Feishu 配置占位符（与 config envDefault 保持一致，用于 Init 校验）
	FeishuDefaultAppID     = "your_feishu_app_id"
	FeishuDefaultAppSecret = "your_feishu_app_secret"

	// Feishu SDK 与缓存 Redis key（项目级前缀，不含服务名）
	FeishuSDKTokenKeyPrefix      = "feishu:sdk:"
	FeishuJSAPITicketKey         = "feishu:jsapi_ticket"
	FeishuMessageDedupeKeyPrefix = "feishu:message:dedupe:"

	// Feishu 开放平台 API 根地址
	FeishuOpenAPIBaseURL  = "https://open.feishu.cn"
	FeishuAccountBaseURL  = "https://accounts.feishu.cn"
)
