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

type UserServer struct {
	userv1connect.UserServiceHandler
}

func (s *UserServer) GetUserInfo(
	ctx context.Context,
	r *connect.Request[userv1.GetUserInfoRequest],
) (*connect.Response[userv1.GetUserInfoResponse], error) {
	user, err := service.GetUserInfo(ctx, r.Msg.UserId)
	if err != nil {
		return nil, err
	}

	log.Debug().Msgf("GetUser called success, userID: %d, userName: %s", r.Msg.UserId, user.DisplayName)
	return connect.NewResponse(&userv1.GetUserInfoResponse{
		UserInfo: &userv1.UserInfo{
			Id:        user.ID,
			Name:      user.DisplayName,
			AvatarUrl: user.AvatarURL,
		},
	}), nil
}

func InitUserHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := userv1connect.NewUserServiceHandler(&UserServer{}, opts...)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
