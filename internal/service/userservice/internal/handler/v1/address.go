package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/user/v1/userv1connect"
	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type AddressServer struct {
	userv1connect.AddressServiceHandler
}

func (s *AddressServer) CreateAddress(
	ctx context.Context,
	r *connect.Request[userv1.CreateAddressRequest],
) (*connect.Response[userv1.CreateAddressResponse], error) {
	return nil, rpcerror.NewInternalError(&commonv1.BusinessError_UserError{
		UserError: &userv1.UserError{
			Code: userv1.UserErrorCode_USER_ERROR_CODE_INTERNAL_ERROR,
		},
	}, "To be implemented")
}

func (s *AddressServer) UpdateAddress(
	ctx context.Context,
	r *connect.Request[userv1.UpdateAddressRequest],
) (*connect.Response[userv1.UpdateAddressResponse], error) {
	return nil, rpcerror.NewInternalError(&commonv1.BusinessError_UserError{
		UserError: &userv1.UserError{
			Code: userv1.UserErrorCode_USER_ERROR_CODE_INTERNAL_ERROR,
		},
	}, "To be implemented")
}

func (s *AddressServer) GetAddress(
	ctx context.Context,
	r *connect.Request[userv1.GetAddressRequest],
) (*connect.Response[userv1.GetAddressResponse], error) {
	return nil, rpcerror.NewInternalError(&commonv1.BusinessError_UserError{
		UserError: &userv1.UserError{
			Code: userv1.UserErrorCode_USER_ERROR_CODE_INTERNAL_ERROR,
		},
	}, "To be implemented")
}

func (s *AddressServer) DeleteAddress(
	ctx context.Context,
	r *connect.Request[userv1.DeleteAddressRequest],
) (*connect.Response[userv1.DeleteAddressResponse], error) {
	return nil, rpcerror.NewInternalError(&commonv1.BusinessError_UserError{
		UserError: &userv1.UserError{
			Code: userv1.UserErrorCode_USER_ERROR_CODE_INTERNAL_ERROR,
		},
	}, "To be implemented")
}

func InitAddressHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := userv1connect.NewAddressServiceHandler(&AddressServer{}, opts...)
	log.Debug().Msgf("AddressService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
