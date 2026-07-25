package v1

import (
	"context"
	"errors"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/spot/v1/spotv1connect"
	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	"connectrpc.com/connect"
	rpcinterceptor "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/service"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type SpotOrderServiceServer struct {
	spotv1connect.SpotOrderServiceHandler
}

func authenticatedUserID(ctx context.Context) (int64, error) {
	user, ok := rpcinterceptor.UserFromContext(ctx)
	if !ok || user == nil || user.UserID <= 0 {
		return 0, connect.NewError(connect.CodeUnauthenticated, errors.New("missing authenticated user"))
	}
	return user.UserID, nil
}

func (s *SpotOrderServiceServer) ListSpotOrder(
	ctx context.Context,
	r *connect.Request[spotv1.ListSpotOrderRequest],
) (*connect.Response[spotv1.ListSpotOrderResponse], error) {
	userID, err := authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := service.ListSpotOrder(ctx, userID, r.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (s *SpotOrderServiceServer) CreateSpotOrders(
	ctx context.Context,
	r *connect.Request[spotv1.CreateSpotOrdersRequest],
) (*connect.Response[spotv1.CreateSpotOrdersResponse], error) {
	userID, err := authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}

	details, err := service.CreateSpotOrders(ctx, userID, r.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&spotv1.CreateSpotOrdersResponse{
		SpotOrderDetails: details,
	}), nil
}

func (s *SpotOrderServiceServer) GetSpotOrderDetail(
	ctx context.Context,
	r *connect.Request[spotv1.GetSpotOrderDetailRequest],
) (*connect.Response[spotv1.GetSpotOrderDetailResponse], error) {
	userID, err := authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}

	detail, err := service.GetSpotOrderDetail(ctx, userID, r.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&spotv1.GetSpotOrderDetailResponse{
		SpotOrderDetail: detail,
	}), nil
}

func (s *SpotOrderServiceServer) GetSpotOrderSellerContact(
	ctx context.Context,
	r *connect.Request[spotv1.GetSpotOrderSellerContactRequest],
) (*connect.Response[spotv1.GetSpotOrderSellerContactResponse], error) {
	userID, err := authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}

	openID, err := service.GetSpotOrderSellerContact(ctx, userID, r.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&spotv1.GetSpotOrderSellerContactResponse{
		SellerFeishuOpenId: openID,
	}), nil
}

func (s *SpotOrderServiceServer) CancelSpotOrder(
	ctx context.Context,
	r *connect.Request[spotv1.CancelSpotOrderRequest],
) (*connect.Response[spotv1.CancelSpotOrderResponse], error) {
	userID, err := authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}

	detail, err := service.CancelSpotOrder(ctx, userID, r.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&spotv1.CancelSpotOrderResponse{
		SpotOrderDetail: detail,
	}), nil
}

func (s *SpotOrderServiceServer) CompleteSpotOrder(
	ctx context.Context,
	r *connect.Request[spotv1.CompleteSpotOrderRequest],
) (*connect.Response[spotv1.CompleteSpotOrderResponse], error) {
	userID, err := authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}

	detail, err := service.CompleteSpotOrder(ctx, userID, r.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&spotv1.CompleteSpotOrderResponse{
		SpotOrderDetail: detail,
	}), nil
}

func InitSpotOrderServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := spotv1connect.NewSpotOrderServiceHandler(&SpotOrderServiceServer{}, opts...)
	log.Debug().Msgf("SpotOrderService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
