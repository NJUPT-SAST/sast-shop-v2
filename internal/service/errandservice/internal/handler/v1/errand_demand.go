package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/errand/v1/errandv1connect"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type ErrandDemandServiceServer struct {
	errandv1connect.ErrandDemandServiceHandler
}

func (s *ErrandDemandServiceServer) CreateErrandDemand(
	ctx context.Context,
	r *connect.Request[errandv1.CreateErrandDemandRequest],
) (*connect.Response[errandv1.CreateErrandDemandResponse], error) {
	return nil, errandError()
}

func (s *ErrandDemandServiceServer) GetDemandList(
	ctx context.Context,
	r *connect.Request[errandv1.GetDemandListRequest],
) (*connect.Response[errandv1.GetDemandListResponse], error) {
	return nil, errandError()
}

func (s *ErrandDemandServiceServer) GetDemandDetail(
	ctx context.Context,
	r *connect.Request[errandv1.GetDemandDetailRequest],
) (*connect.Response[errandv1.GetDemandDetailResponse], error) {
	return nil, errandError()
}

func InitErrandDemandServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := errandv1connect.NewErrandDemandServiceHandler(&ErrandDemandServiceServer{}, opts...)
	log.Debug().Msgf("ErrandDemandService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
