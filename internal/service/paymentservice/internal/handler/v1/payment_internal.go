package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/payment/v1/paymentv1connect"
	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/service"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type PaymentInternalServer struct {
	paymentv1connect.PaymentInternalServiceHandler
}

func (s *PaymentInternalServer) CreateBillForOrder(
	ctx context.Context,
	r *connect.Request[paymentv1.CreateBillForOrderRequest],
) (*connect.Response[paymentv1.CreateBillForOrderResponse], error) {
	bill, err := service.CreateBillForOrder(ctx,
		r.Msg.GetSourceType(),
		r.Msg.GetSourceId(),
		r.Msg.GetPayerId(),
		r.Msg.GetPayeeId(),
		r.Msg.GetAmountCents(),
	)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&paymentv1.CreateBillForOrderResponse{
		Bill: bill,
	}), nil
}

func (s *PaymentInternalServer) CancelBillBySource(
	ctx context.Context,
	r *connect.Request[paymentv1.CancelBillBySourceRequest],
) (*connect.Response[paymentv1.CancelBillBySourceResponse], error) {
	err := service.CancelBillBySource(ctx,
		r.Msg.GetSourceType(),
		r.Msg.GetSourceId(),
		r.Msg.PayerId,
	)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&paymentv1.CancelBillBySourceResponse{}), nil
}

func InitPaymentInternalServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := paymentv1connect.NewPaymentInternalServiceHandler(&PaymentInternalServer{}, opts...)
	log.Debug().Msgf("PaymentInternalService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
