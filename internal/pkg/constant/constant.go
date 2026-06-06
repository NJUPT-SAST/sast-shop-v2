package constant

import "time"

const (
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
)
