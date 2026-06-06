package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/spot/v1/spotv1connect"
	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	"connectrpc.com/connect"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type SpotGoodsServiceServer struct {
	spotv1connect.SpotGoodsServiceHandler
}

func (s *SpotGoodsServiceServer) ListSpotGoods(
	ctx context.Context,
	r *connect.Request[spotv1.ListSpotGoodsRequest],
) (*connect.Response[spotv1.ListSpotGoodsResponse], error) {
	return nil, spotError()
}

func (s *SpotGoodsServiceServer) GetSpotGoods(
	ctx context.Context,
	r *connect.Request[spotv1.GetSpotGoodsRequest],
) (*connect.Response[spotv1.GetSpotGoodsResponse], error) {
	return nil, spotError()
}

func (s *SpotGoodsServiceServer) CreateSpotGoods(
	ctx context.Context,
	r *connect.Request[spotv1.CreateSpotGoodsRequest],
) (*connect.Response[spotv1.CreateSpotGoodsResponse], error) {
	return nil, spotError()
}

func (s *SpotGoodsServiceServer) UpdateSpotGoodsStock(
	ctx context.Context,
	r *connect.Request[spotv1.UpdateSpotGoodsStockRequest],
) (*connect.Response[spotv1.UpdateSpotGoodsStockResponse], error) {
	return nil, spotError()
}

func (s *SpotGoodsServiceServer) UpdateSpotGoodsPrice(
	ctx context.Context,
	r *connect.Request[spotv1.UpdateSpotGoodsPriceRequest],
) (*connect.Response[spotv1.UpdateSpotGoodsPriceResponse], error) {
	return nil, spotError()
}

func InitSpotGoodsServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := spotv1connect.NewSpotGoodsServiceHandler(&SpotGoodsServiceServer{}, opts...)
	log.Debug().Msgf("SpotGoodsService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
