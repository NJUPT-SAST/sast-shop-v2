package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/payment/v1/paymentv1connect"
	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	"connectrpc.com/connect"
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
	return nil, paymentError()
}

func (s *PaymentInternalServer) CancelBillBySource(
	ctx context.Context,
	r *connect.Request[paymentv1.CancelBillBySourceRequest],
) (*connect.Response[paymentv1.CancelBillBySourceResponse], error) {
	return nil, paymentError()
}

func InitPaymentInternalServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := paymentv1connect.NewPaymentInternalServiceHandler(&PaymentInternalServer{}, opts...)
	log.Debug().Msgf("PaymentInternalService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
