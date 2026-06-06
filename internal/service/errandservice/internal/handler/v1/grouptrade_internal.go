package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/errand/v1/errandv1connect"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type GroupTradeInternalServer struct {
	errandv1connect.GroupTradeInternalServiceHandler
}

func (s *GroupTradeInternalServer) OnPaymentConfirmed(
	ctx context.Context,
	r *connect.Request[errandv1.OnPaymentConfirmedRequest],
) (*connect.Response[errandv1.OnPaymentConfirmedResponse], error) {
	return nil, errandError()
}

func (s *GroupTradeInternalServer) OnAllPaymentsConfirmed(
	ctx context.Context,
	r *connect.Request[errandv1.OnAllPaymentsConfirmedRequest],
) (*connect.Response[errandv1.OnAllPaymentsConfirmedResponse], error) {
	return nil, errandError()
}

func InitGroupTradeInternalServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := errandv1connect.NewGroupTradeInternalServiceHandler(&GroupTradeInternalServer{}, opts...)
	log.Debug().Msgf("GroupTradeInternalService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
