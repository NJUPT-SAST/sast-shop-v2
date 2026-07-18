package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/errand/v1/errandv1connect"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/service"
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
	if err := service.OnPaymentConfirmed(ctx, r.Msg); err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&errandv1.OnPaymentConfirmedResponse{}), nil
}

func (s *GroupTradeInternalServer) OnAllPaymentsConfirmed(
	ctx context.Context,
	r *connect.Request[errandv1.OnAllPaymentsConfirmedRequest],
) (*connect.Response[errandv1.OnAllPaymentsConfirmedResponse], error) {
	if err := service.OnAllPaymentsConfirmed(ctx, r.Msg); err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&errandv1.OnAllPaymentsConfirmedResponse{}), nil
}

func InitGroupTradeInternalServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := errandv1connect.NewGroupTradeInternalServiceHandler(&GroupTradeInternalServer{}, opts...)
	log.Debug().Msgf("GroupTradeInternalService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
