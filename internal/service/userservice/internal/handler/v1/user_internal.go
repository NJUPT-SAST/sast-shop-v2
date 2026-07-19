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

type UserInternalServer struct {
	userv1connect.UserInternalServiceHandler
}

func (s *UserInternalServer) GetUsers(
	ctx context.Context,
	r *connect.Request[userv1.GetUsersRequest],
) (*connect.Response[userv1.GetUsersResponse], error) {
	log.Debug().Msgf("GetUsers called with protocol: %s, userIDs: %v", r.Peer().Protocol, r.Msg.UserIds)
	userIDs := r.Msg.UserIds
	if len(userIDs) == 0 {
		return connect.NewResponse(&userv1.GetUsersResponse{}), nil
	}

	users, err := service.GetByUserIDs(ctx, userIDs)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get users for userIDs: %v", userIDs)
		return nil, userError()
	}

	result := make([]*userv1.UserInfo, len(users))
	for i, u := range users {
		result[i] = &userv1.UserInfo{
			Id:        u.ID,
			Name:      u.DisplayName,
			AvatarUrl: u.AvatarURL,
		}
	}

	return connect.NewResponse(&userv1.GetUsersResponse{
		Users: result,
	}), nil
}

func (s *UserInternalServer) GetUserContactOpenID(
	ctx context.Context,
	r *connect.Request[userv1.GetUserContactOpenIDRequest],
) (*connect.Response[userv1.GetUserContactOpenIDResponse], error) {
	user, err := service.GetUserInfo(ctx, r.Msg.UserId)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&userv1.GetUserContactOpenIDResponse{
		FeishuOpenId: user.FeishuOpenID,
	}), nil
}

func InitUserInternalHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := userv1connect.NewUserInternalServiceHandler(&UserInternalServer{}, opts...)
	log.Debug().Msgf("UserInternalService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
