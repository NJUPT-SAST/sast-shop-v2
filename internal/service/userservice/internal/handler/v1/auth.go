package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/user/v1/userv1connect"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/service"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type AuthServer struct {
	userv1connect.AuthServiceHandler
}

func (s *AuthServer) Login(
	ctx context.Context,
	r *connect.Request[userv1.LoginRequest],
) (*connect.Response[userv1.LoginResponse], error) {
	resp, err := service.Login(ctx, r.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *AuthServer) GetJSAPIAuthConfig(
	ctx context.Context,
	r *connect.Request[userv1.GetJSAPIAuthConfigRequest],
) (*connect.Response[userv1.GetJSAPIAuthConfigResponse], error) {
	resp, err := service.GetJSAPIAuthConfig(ctx, r.Msg.GetUrl())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func InitAuthHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := userv1connect.NewAuthServiceHandler(&AuthServer{}, opts...)
	log.Debug().Msgf("AuthService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
