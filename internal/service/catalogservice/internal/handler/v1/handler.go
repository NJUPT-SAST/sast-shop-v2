package v1

import (
	"time"

	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	rpcinterceptor "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/redis"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type UploadDependencies struct {
	SessionStore    rpcinterceptor.SessionStore
	Limiter         UploadLimiter
	Uploader        UploadService
	Ready           ReadyChecker
	AllowedRoles    map[string]struct{}
	MaxRequestBytes int64
	MaxImageBytes   int64
}

func Init(e *echo.Echo, uploadDeps ...UploadDependencies) {
	sharedOpts, err := rpcinterceptor.NewValidationChain(log.Logger)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create validation chain")
	}

	sessionStore := redis.NewSessionStore()
	isDev := config.AppConfig != nil && config.AppConfig.AppEnv == config.Development
	authOpts := connect.WithInterceptors(
		rpcinterceptor.AuthRequired(sessionStore, log.Logger, isDev),
	)

	InitCatalogServiceHandler(e, sharedOpts, authOpts)
	InitProductTemplateServiceHandler(e, sharedOpts, authOpts)
	InitCatalogInternalServiceHandler(e, sharedOpts)

	deps := UploadDependencies{
		SessionStore: sessionStore,
		Limiter:      redis.NewUploadLimiter(time.Minute, 20),
	}
	if len(uploadDeps) > 0 {
		deps = uploadDeps[0]
		if deps.SessionStore == nil {
			deps.SessionStore = sessionStore
		}
		if deps.Limiter == nil {
			deps.Limiter = redis.NewUploadLimiter(time.Minute, 20)
		}
	}
	if deps.Ready == nil {
		if checker, ok := deps.Uploader.(ReadyChecker); ok {
			deps.Ready = checker
		}
	}
	RegisterProductImageUpload(e, deps)
	RegisterHealthReady(e, deps.Ready)
}

func RegisterProductImageUpload(e *echo.Echo, deps UploadDependencies) {
	handler := &ProductImageUploadHandler{
		SessionStore:    deps.SessionStore,
		Limiter:         deps.Limiter,
		Uploader:        deps.Uploader,
		AllowedRoles:    deps.AllowedRoles,
		MaxRequestBytes: deps.MaxRequestBytes,
		MaxImageBytes:   deps.MaxImageBytes,
	}
	e.POST(ProductImageUploadPath, echo.WrapHandler(handler))
	log.Debug().Msgf("Product image upload API registered at path: %s", ProductImageUploadPath)
}
