package v1

import (
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	rpcinterceptor "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/redis"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

func Init(e *echo.Echo) {
	sharedOpts, err := rpcinterceptor.NewValidationChain(log.Logger)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create validation chain")
	}

	sessionStore := redis.NewSessionStore()
	isDev := config.AppConfig.AppEnv == config.Development
	authOpts := connect.WithInterceptors(
		rpcinterceptor.AuthRequired(sessionStore, log.Logger, isDev),
	)

	InitCatalogServiceHandler(e, sharedOpts, authOpts)
	InitProductTemplateServiceHandler(e, sharedOpts, authOpts)
	InitCatalogInternalServiceHandler(e, sharedOpts)
}
