package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/errand/v1/errandv1connect"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type BuyerErrandOrderServiceServer struct {
	errandv1connect.BuyerErrandOrderServiceHandler
}

func (s *BuyerErrandOrderServiceServer) GetBuyerErrandOrderBrief(
	ctx context.Context,
	r *connect.Request[errandv1.GetBuyerErrandOrderBriefRequest],
) (*connect.Response[errandv1.GetBuyerErrandOrderBriefResponse], error) {
	return nil, errandError()
}

func (s *BuyerErrandOrderServiceServer) GetBuyerErrandOrderDetail(
	ctx context.Context,
	r *connect.Request[errandv1.GetBuyerErrandOrderDetailRequest],
) (*connect.Response[errandv1.GetBuyerErrandOrderDetailResponse], error) {
	return nil, errandError()
}

func InitBuyerErrandOrderServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := errandv1connect.NewBuyerErrandOrderServiceHandler(&BuyerErrandOrderServiceServer{}, opts...)
	log.Debug().Msgf("BuyerErrandOrderService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
