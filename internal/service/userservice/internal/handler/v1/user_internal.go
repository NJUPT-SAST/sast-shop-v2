package v1

import (
	"context"
	"errors"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/user/v1/userv1connect"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/service"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type UserInternalServer struct {
	userv1connect.UserInternalServiceHandler
}

func (s *UserInternalServer) SearchUsers(
	ctx context.Context,
	r *connect.Request[userv1.SearchUsersRequest],
) (*connect.Response[userv1.SearchUsersResponse], error) {
	if err := interceptor.RequireWestPocketService(r.Header()); err != nil {
		return nil, err
	}
	users, next, err := service.SearchUsers(ctx, r.Msg.Query, r.Msg.PageSize, r.Msg.PageToken)
	if err != nil {
		var rpcErr *connect.Error
		if errors.As(err, &rpcErr) {
			return nil, rpcErr
		}
		return nil, userError()
	}
	result := make([]*userv1.UserInfo, 0, len(users))
	for _, user := range users {
		result = append(result, &userv1.UserInfo{Id: user.ID, Name: user.DisplayName, AvatarUrl: user.AvatarURL})
	}
	return connect.NewResponse(&userv1.SearchUsersResponse{Users: result, NextPageToken: next}), nil
}

func (s *UserInternalServer) GetUsers(
	ctx context.Context,
	r *connect.Request[userv1.GetUsersRequest],
) (*connect.Response[userv1.GetUsersResponse], error) {
	westPocket := r.Header().Get(interceptor.WestPocketServiceHeader) != ""
	if westPocket {
		if err := interceptor.RequireWestPocketService(r.Header()); err != nil {
			return nil, err
		}
		if len(r.Msg.UserIds) > 100 {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("too many users"))
		}
	}
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

	result := make([]*userv1.UserInfo, 0, len(users))
	for _, u := range users {
		if westPocket && u.Status != model.MemberStatusActive {
			continue
		}
		result = append(result, &userv1.UserInfo{
			Id:        u.ID,
			Name:      u.DisplayName,
			AvatarUrl: u.AvatarURL,
		})
	}

	return connect.NewResponse(&userv1.GetUsersResponse{
		Users: result,
	}), nil
}

func (s *UserInternalServer) GetUserContactOpenID(
	ctx context.Context,
	r *connect.Request[userv1.GetUserContactOpenIDRequest],
) (*connect.Response[userv1.GetUserContactOpenIDResponse], error) {
	westPocket := r.Header().Get(interceptor.WestPocketServiceHeader) != ""
	if westPocket {
		if err := interceptor.RequireWestPocketService(r.Header()); err != nil {
			return nil, err
		}
	}
	user, err := service.GetUserInfo(ctx, r.Msg.UserId)
	if err != nil {
		return nil, err
	}
	if westPocket && user.Status != model.MemberStatusActive {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("active user not found"))
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
