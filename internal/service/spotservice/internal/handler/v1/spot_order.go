package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/spot/v1/spotv1connect"
	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	"connectrpc.com/connect"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type SpotOrderServiceServer struct {
	spotv1connect.SpotOrderServiceHandler
}

func (s *SpotOrderServiceServer) ListSpotOrder(
	ctx context.Context,
	r *connect.Request[spotv1.ListSpotOrderRequest],
) (*connect.Response[spotv1.ListSpotOrderResponse], error) {
	return nil, spotError()
}

func (s *SpotOrderServiceServer) CreateSpotOrders(
	ctx context.Context,
	r *connect.Request[spotv1.CreateSpotOrdersRequest],
) (*connect.Response[spotv1.CreateSpotOrdersResponse], error) {
	return nil, spotError()
}

func (s *SpotOrderServiceServer) GetSpotOrderDetail(
	ctx context.Context,
	r *connect.Request[spotv1.GetSpotOrderDetailRequest],
) (*connect.Response[spotv1.GetSpotOrderDetailResponse], error) {
	return nil, spotError()
}

func (s *SpotOrderServiceServer) CancelSpotOrder(
	ctx context.Context,
	r *connect.Request[spotv1.CancelSpotOrderRequest],
) (*connect.Response[spotv1.CancelSpotOrderResponse], error) {
	return nil, spotError()
}

func (s *SpotOrderServiceServer) CompleteSpotOrder(
	ctx context.Context,
	r *connect.Request[spotv1.CompleteSpotOrderRequest],
) (*connect.Response[spotv1.CompleteSpotOrderResponse], error) {
	return nil, spotError()
}

func InitSpotOrderServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := spotv1connect.NewSpotOrderServiceHandler(&SpotOrderServiceServer{}, opts...)
	log.Debug().Msgf("SpotOrderService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
