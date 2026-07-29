package v1

import (
	"context"
	"errors"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/user/v1/userv1connect"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	rpcinterceptor "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/service"
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
	user, ok := rpcinterceptor.UserFromContext(ctx)
	if !ok {
		return nil, userError()
	}

	addr, err := service.CreateAddress(ctx, user.UserID, r.Msg)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to create address for userID: %d", user.UserID)
		return nil, mapAddressError(err)
	}

	return connect.NewResponse(&userv1.CreateAddressResponse{
		ShippingAddresses: addr,
	}), nil
}

func (s *AddressServer) UpdateAddress(
	ctx context.Context,
	r *connect.Request[userv1.UpdateAddressRequest],
) (*connect.Response[userv1.UpdateAddressResponse], error) {
	user, ok := rpcinterceptor.UserFromContext(ctx)
	if !ok {
		return nil, userError()
	}

	addr, err := service.UpdateAddress(ctx, user.UserID, r.Msg)
	if err != nil {
		log.Error().
			Err(err).
			Msgf("Failed to update address for userID: %d, addressID: %d", user.UserID, r.Msg.AddressId)
		return nil, mapAddressError(err)
	}

	return connect.NewResponse(&userv1.UpdateAddressResponse{
		ShippingAddresses: addr,
	}), nil
}

func (s *AddressServer) GetAddress(
	ctx context.Context,
	r *connect.Request[userv1.GetAddressRequest],
) (*connect.Response[userv1.GetAddressResponse], error) {
	user, ok := rpcinterceptor.UserFromContext(ctx)
	if !ok {
		return nil, userError()
	}

	addrs, err := service.GetAddress(ctx, user.UserID, r.Msg.AddressId)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get address for userID: %d", user.UserID)
		return nil, mapAddressError(err)
	}

	return connect.NewResponse(&userv1.GetAddressResponse{
		ShippingAddresses: addrs,
	}), nil
}

func (s *AddressServer) DeleteAddress(
	ctx context.Context,
	r *connect.Request[userv1.DeleteAddressRequest],
) (*connect.Response[userv1.DeleteAddressResponse], error) {
	user, ok := rpcinterceptor.UserFromContext(ctx)
	if !ok {
		return nil, userError()
	}

	err := service.DeleteAddress(ctx, user.UserID, r.Msg.AddressId)
	if err != nil {
		log.Error().
			Err(err).
			Msgf("Failed to delete address for userID: %d, addressID: %d", user.UserID, r.Msg.AddressId)
		return nil, mapAddressError(err)
	}

	return connect.NewResponse(&userv1.DeleteAddressResponse{}), nil
}

func mapAddressError(err error) *connect.Error {
	if errors.Is(err, service.ErrAddressNotFound) {
		return userError()
	}
	return userError()
}

func InitAddressHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := userv1connect.NewAddressServiceHandler(&AddressServer{}, opts...)
	log.Debug().Msgf("AddressService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
